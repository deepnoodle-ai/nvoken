package nvoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

// AgentOptions fixes one identity and Agent Definition every turn from this
// Agent runs with. Supply AgentDefinitionID to reuse a resource, or leave it
// empty and provide the flat inline definition fields. Tools always register
// local handlers; for inline definitions they are also sent as declarations.
type AgentOptions struct {
	AgentKey          string
	AgentDefinitionID string
	TenantKey         *string
	Instructions      string
	Model             Model
	Sampling          *Sampling
	Reasoning         *Reasoning
	ToolChoice        *ToolChoice
	Limits            *Limits
	Tools             []Tool
	MCPServers        []MCPServer
	ProviderTools     []ProviderTool
	OutputSchema      map[string]any
	// MCPServerHeaders carries per-turn secret headers for the MCP servers this
	// Agent declares. They stay outside the Agent Definition so no reusable
	// revision depends on a secret.
	MCPServerHeaders  []MCPServerHeaders
	ProviderKeys      []ProviderKeySelection
	Webhook           *WebhookTarget
	OnBudgetExhausted BudgetExhaustionBehavior
}

func (o AgentOptions) agentDefinition() AgentDefinition {
	return AgentDefinition{
		Instructions:  o.Instructions,
		Model:         o.Model,
		Sampling:      o.Sampling,
		Reasoning:     o.Reasoning,
		ToolChoice:    o.ToolChoice,
		Limits:        o.Limits,
		Tools:         o.Tools,
		MCPServers:    o.MCPServers,
		ProviderTools: o.ProviderTools,
		OutputSchema:  o.OutputSchema,
	}
}

func (o AgentOptions) hasInlineDefinitionFields() bool {
	return o.Instructions != "" || o.Model != (Model{}) || o.Sampling != nil ||
		o.Reasoning != nil || o.ToolChoice != nil || o.Limits != nil ||
		len(o.MCPServers) != 0 || len(o.ProviderTools) != 0 || o.OutputSchema != nil
}

type AgentInvocationOptions struct {
	IdempotencyKey    string
	TenantKey         *string
	SessionID         *string
	SessionKey        *string
	SessionOptions    *SessionOptions
	IfActive          IfActivePolicy
	OnBudgetExhausted BudgetExhaustionBehavior
	Webhook           *WebhookTarget
	// Context carries the application state snapshots to record ahead of this
	// turn's input. Per-call rather than per-Agent, because a snapshot is what
	// changes between turns while the Agent Definition stays fixed.
	Context []ContextItem
	// Metadata is opaque host correlation data recorded on this Invocation.
	// Per-call rather than per-Agent: it is immutable and material to
	// idempotency, so an Agent-level default would make two otherwise distinct
	// calls conflict.
	Metadata                     map[string]string
	Wait                         WaitOptions
	LeaveWaitingOnMissingHandler bool
}

type AgentResult struct {
	Handle           *InvocationHandle
	Invocation       Invocation
	Messages         []SessionMessage
	OutputText       *string
	StructuredOutput json.RawMessage
	Raw              *InvocationResult
}

type AgentStreamEvent struct {
	Handle *InvocationHandle
	Event  StreamEvent
}

type MissingToolHandlerError struct {
	InvocationID        string
	ToolName            string
	InvocationCancelled bool
	CancelError         error
}

func (e *MissingToolHandlerError) Error() string {
	action := "left waiting"
	if e.InvocationCancelled {
		action = "cancelled"
	}
	return fmt.Sprintf(
		"Invocation %s is waiting for unhandled tool %q and was %s",
		e.InvocationID,
		e.ToolName,
		action,
	)
}

func (e *MissingToolHandlerError) Unwrap() error {
	return e.CancelError
}

type NoOutputTextError struct {
	InvocationID string
	ResultKind   string
}

func (e *NoOutputTextError) Error() string {
	return fmt.Sprintf(
		"Invocation %s completed with %s, not text",
		e.InvocationID,
		e.ResultKind,
	)
}

type Agent struct {
	client  *Client
	options AgentOptions
	// hostTools serves the calls this Agent answers locally. callbackTools
	// names the ones nvoken delivers over HTTPS instead: those can appear in a
	// waiting Invocation's pending calls once the endpoint has acknowledged a
	// delivery without settling it, and they are answered by whatever accepted
	// that acknowledgement, not from here.
	hostTools     map[string]Tool
	callbackTools map[string]struct{}
}

func (c *Client) Agent(options AgentOptions) (*Agent, error) {
	return NewAgent(c, options)
}

func NewAgent(client *Client, options AgentOptions) (*Agent, error) {
	if client == nil {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "Agent client is required",
		}
	}
	if options.AgentKey == "" {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "Agent key is required",
		}
	}
	if options.AgentDefinitionID != "" && options.hasInlineDefinitionFields() {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "Supply an Agent Definition ID or inline definition fields, not both",
		}
	}
	if options.AgentDefinitionID == "" && options.Model == (Model{}) {
		if client.DefaultModel == nil {
			return nil, &Error{
				Category: ErrorValidation,
				Message:  "Agent model is required for an inline definition",
			}
		}
		options.Model = *client.DefaultModel
	}
	hostTools := make(map[string]Tool)
	callbackTools := make(map[string]struct{})
	for _, tool := range options.Tools {
		switch tool.Mode {
		case ToolModeHost:
			hostTools[tool.Name] = tool
		case ToolModeCallback:
			callbackTools[tool.Name] = struct{}{}
		}
	}
	return &Agent{
		client:        client,
		options:       options,
		hostTools:     hostTools,
		callbackTools: callbackTools,
	}, nil
}

func (a *Agent) Invoke(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
) (*InvocationHandle, error) {
	return a.client.Invoke(ctx, a.request(input, options))
}

// request composes the Agent identity and definition with one call's overrides. It
// is the single place a per-call option reaches the wire, so the conformance
// suite can pin the whole admitted body without a server.
func (a *Agent) request(input string, options AgentInvocationOptions) InvokeRequest {
	tenantKey := options.TenantKey
	if tenantKey == nil {
		tenantKey = a.options.TenantKey
	}
	onBudgetExhausted := options.OnBudgetExhausted
	if onBudgetExhausted == "" {
		onBudgetExhausted = a.options.OnBudgetExhausted
	}
	request := InvokeRequest{
		AgentKey:          a.options.AgentKey,
		TenantKey:         tenantKey,
		SessionID:         options.SessionID,
		SessionKey:        options.SessionKey,
		SessionOptions:    options.SessionOptions,
		IdempotencyKey:    options.IdempotencyKey,
		IfActive:          options.IfActive,
		OnBudgetExhausted: onBudgetExhausted,
		Input:             input,
		MCPServerHeaders:  a.options.MCPServerHeaders,
		ProviderKeys:      a.options.ProviderKeys,
		// A per-call target overrides the agent default so one Agent can
		// webhook different endpoints without a second Agent.
		Webhook:  webhookTarget(options.Webhook, a.options.Webhook),
		Context:  options.Context,
		Metadata: options.Metadata,
	}
	if a.options.AgentDefinitionID != "" {
		request.AgentDefinitionID = a.options.AgentDefinitionID
	} else {
		definition := a.options.agentDefinition()
		request.AgentDefinition = &definition
	}
	return request
}

func (a *Agent) Stream(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
	consume func(AgentStreamEvent) error,
) (*InvocationHandle, error) {
	handle, err := a.Invoke(ctx, input, options)
	if err != nil {
		return nil, err
	}
	submitted := make(map[string]struct{})
	err = handle.Stream(ctx, func(event StreamEvent) error {
		if consume != nil {
			if err := consume(AgentStreamEvent{
				Handle: handle,
				Event:  event,
			}); err != nil {
				return err
			}
		}
		// A turn that stopped for your tools says so on a change. Reading the
		// composed Invocation only once one arrives is both cheaper than
		// refreshing on every frame and durable: the change replays on
		// reconnect, so a turn that parked while you were away still parks
		// when you return.
		if !waitingChange(event, handle.InvocationID) {
			return nil
		}
		invocation, err := handle.Refresh(ctx)
		if err != nil {
			return err
		}
		if invocation.Status != InvocationWaiting {
			return nil
		}
		_, err = a.dispatchWaiting(
			ctx,
			handle,
			invocation,
			submitted,
			options.LeaveWaitingOnMissingHandler,
		)
		return err
	})
	return handle, err
}

func (a *Agent) Run(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
) (*AgentResult, error) {
	result, _, err := a.runWithHandle(ctx, input, options)
	return result, err
}

func (a *Agent) runWithHandle(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
) (*AgentResult, *InvocationHandle, error) {
	handle, err := a.Stream(ctx, input, options, nil)
	if err != nil && !recoverThroughAuthoritativeRead(err) {
		return nil, handle, err
	}
	if handle == nil {
		return nil, nil, err
	}
	result, settleErr := a.settleByRead(ctx, handle, options)
	if settleErr != nil {
		return nil, handle, settleErr
	}
	agentResult, resultErr := newAgentResult(handle, result)
	return agentResult, handle, resultErr
}

func (a *Agent) Text(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
) (string, error) {
	result, err := a.Run(ctx, input, options)
	if err != nil {
		return "", err
	}
	return a.textFromResult(result)
}

func (a *Agent) textFromResult(result *AgentResult) (string, error) {
	if result.OutputText != nil && *result.OutputText != "" {
		return *result.OutputText, nil
	}
	resultKind := "no assistant output"
	if len(result.StructuredOutput) > 0 && string(result.StructuredOutput) != "null" {
		resultKind = "structured output"
	} else if len(a.options.Tools) > 0 {
		resultKind = "tool-only output"
	}
	return "", &NoOutputTextError{
		InvocationID: result.Handle.InvocationID,
		ResultKind:   resultKind,
	}
}

func (a *Agent) Session(binding SessionBinding) (*AgentSession, error) {
	if (binding.SessionID == "") == (binding.SessionKey == "") {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "exactly one of SessionID or SessionKey is required",
		}
	}
	tenantKey := binding.TenantKey
	if tenantKey == nil {
		tenantKey = a.options.TenantKey
	}
	key := "id:" + binding.SessionID
	if binding.SessionID == "" {
		tenant := "default"
		if tenantKey != nil {
			tenant = *tenantKey
		}
		key = "key:" + tenant + ":" + binding.SessionKey
	}
	a.client.sessionMu.Lock()
	lock := a.client.sessionLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		a.client.sessionLocks[key] = lock
	}
	a.client.sessionMu.Unlock()
	return &AgentSession{
		agent:      a,
		lock:       lock,
		sessionID:  binding.SessionID,
		sessionKey: binding.SessionKey,
		tenantKey:  tenantKey,
	}, nil
}

func (a *Agent) settleByRead(
	ctx context.Context,
	handle *InvocationHandle,
	options AgentInvocationOptions,
) (*InvocationResult, error) {
	submitted := make(map[string]struct{})
	for {
		invocation, err := handle.WaitForAction(ctx, options.Wait)
		if err != nil {
			return nil, err
		}
		if invocation.Status == InvocationWaiting {
			dispatched, err := a.dispatchWaiting(
				ctx,
				handle,
				invocation,
				submitted,
				options.LeaveWaitingOnMissingHandler,
			)
			if err != nil {
				return nil, err
			}
			if !dispatched {
				timer := time.NewTimer(50 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, transportError(ctx.Err())
				case <-timer.C:
				}
			}
			continue
		}
		if invocation.Status != InvocationCompleted {
			return nil, &Error{
				Category: ErrorConflict,
				Code:     invocationFailureCode(invocation),
				Message:  invocationEndedMessage(handle.InvocationID, invocation),
			}
		}
		return handle.Result(ctx)
	}
}

func (a *Agent) dispatchWaiting(
	ctx context.Context,
	handle *InvocationHandle,
	invocation *Invocation,
	submitted map[string]struct{},
	leaveWaiting bool,
) (bool, error) {
	answerable := AnswerableToolCalls(invocation)
	if len(answerable) == 0 {
		return false, nil
	}
	results := make([]ToolResult, 0, len(answerable))
	for _, pending := range answerable {
		if _, alreadySubmitted := submitted[pending.ID]; alreadySubmitted {
			continue
		}
		if _, isCallback := a.callbackTools[pending.Name]; isCallback {
			continue
		}
		tool, ok := a.hostTools[pending.Name]
		if !ok || tool.Handler == nil {
			missing := &MissingToolHandlerError{
				InvocationID: handle.InvocationID,
				ToolName:     pending.Name,
			}
			if !leaveWaiting {
				_, missing.CancelError = handle.Cancel(ctx)
				missing.InvocationCancelled = missing.CancelError == nil
			}
			return false, missing
		}
		content, err := tool.Handler(ctx, toolCallArguments(pending))
		result := ToolResult{
			ToolCallID: pending.ID,
			Content:    content,
		}
		if err != nil {
			result.Content = map[string]any{
				"error": err.Error(),
				"type":  fmt.Sprintf("%T", err),
			}
			result.IsError = true
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		return false, nil
	}
	if _, err := handle.SubmitToolResults(ctx, results); err != nil {
		return false, err
	}
	for _, result := range results {
		submitted[result.ToolCallID] = struct{}{}
	}
	return true, nil
}

type SessionBinding struct {
	SessionID  string
	SessionKey string
	TenantKey  *string
}

type AgentSession struct {
	agent      *Agent
	lock       *sync.Mutex
	sessionID  string
	sessionKey string
	tenantKey  *string
}

func (s *AgentSession) Invoke(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
) (*InvocationHandle, error) {
	if err := s.bind(&options); err != nil {
		return nil, err
	}
	s.lock.Lock()
	handle, err := s.agent.Invoke(ctx, input, options)
	if err != nil {
		s.lock.Unlock()
		return nil, err
	}
	go s.releaseWhenTerminal(handle, options.Wait)
	return handle, nil
}

func (s *AgentSession) Run(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
) (*AgentResult, error) {
	if err := s.bind(&options); err != nil {
		return nil, err
	}
	s.lock.Lock()
	result, handle, err := s.agent.runWithHandle(ctx, input, options)
	if err != nil && handle != nil {
		go s.releaseWhenTerminal(handle, options.Wait)
	} else {
		s.lock.Unlock()
	}
	return result, err
}

func (s *AgentSession) Text(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
) (string, error) {
	result, err := s.Run(ctx, input, options)
	if err != nil {
		return "", err
	}
	return s.agent.textFromResult(result)
}

func (s *AgentSession) Stream(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
	consume func(AgentStreamEvent) error,
) (*InvocationHandle, error) {
	if err := s.bind(&options); err != nil {
		return nil, err
	}
	s.lock.Lock()
	handle, err := s.agent.Stream(ctx, input, options, consume)
	if err != nil && handle != nil {
		go s.releaseWhenTerminal(handle, options.Wait)
	} else {
		s.lock.Unlock()
	}
	return handle, err
}

func (s *AgentSession) bind(options *AgentInvocationOptions) error {
	if options.SessionID != nil || options.SessionKey != nil {
		return &Error{
			Category: ErrorValidation,
			Message:  "bound Session calls cannot override their Session",
		}
	}
	options.TenantKey = s.tenantKey
	if s.sessionID != "" {
		options.SessionID = &s.sessionID
	} else {
		options.SessionKey = &s.sessionKey
	}
	return nil
}

func (s *AgentSession) releaseWhenTerminal(
	handle *InvocationHandle,
	options WaitOptions,
) {
	options.Timeout = 0
	options.Until = WaitUntilTerminal
	for {
		_, err := handle.Wait(context.Background(), options)
		if err == nil {
			s.lock.Unlock()
			return
		}
		time.Sleep(time.Second)
	}
}

func DecodeStructuredOutput[T any](result *AgentResult) (T, error) {
	var value T
	if result == nil || len(result.StructuredOutput) == 0 ||
		string(result.StructuredOutput) == "null" {
		return value, &NoOutputTextError{
			ResultKind: "no structured output",
		}
	}
	if err := json.Unmarshal(result.StructuredOutput, &value); err != nil {
		return value, fmt.Errorf("decode structured output: %w", err)
	}
	return value, nil
}

func newAgentResult(
	handle *InvocationHandle,
	result *InvocationResult,
) (*AgentResult, error) {
	structured, err := json.Marshal(result.Invocation.StructuredOutput)
	if err != nil {
		return nil, fmt.Errorf("encode structured output: %w", err)
	}
	if result.Invocation.StructuredOutput == nil {
		structured = nil
	}
	return &AgentResult{
		Handle:           handle,
		Invocation:       result.Invocation,
		Messages:         result.Messages,
		OutputText:       result.OutputText,
		StructuredOutput: structured,
		Raw:              result,
	}, nil
}

func recoverThroughAuthoritativeRead(err error) bool {
	var typed *Error
	if !errors.As(err, &typed) {
		return false
	}
	return typed.Category == ErrorTransport || typed.Category == ErrorServer
}

func invocationFailureCode(invocation *Invocation) string {
	if invocation.Error == nil {
		return ""
	}
	return string(invocation.Error.Code)
}

// invocationEndedMessage explains an ending that was not the answer asked for.
// An `incomplete` turn carries no error, so its stop reason is the only thing
// that names the budget that stopped it.
func invocationEndedMessage(invocationID string, invocation *Invocation) string {
	if invocation.StopReason != nil {
		return fmt.Sprintf(
			"Invocation %s ended with status %s (%s)",
			invocationID, invocation.Status, *invocation.StopReason,
		)
	}
	return fmt.Sprintf("Invocation %s ended with status %s", invocationID, invocation.Status)
}

// webhookTarget prefers the per-call target and falls back to the Agent's.
func webhookTarget(perCall, agentDefault *WebhookTarget) *WebhookTarget {
	if perCall != nil {
		return perCall
	}
	return agentDefault
}

// AnswerToolCallsOptions configures one unattended answering pass.
type AnswerToolCallsOptions struct {
	// Claim runs before each tool. Returning false skips that call — use it to
	// take an execution lease keyed by the ToolCall ID, so a streaming reader
	// and this worker cannot both start the same non-idempotent tool.
	Claim func(context.Context, ToolCallSummary) (bool, error)
	// LeaveWaitingOnMissingHandler reports an error rather than skipping a call
	// this Agent has no handler for. The default skips, because an unattended
	// worker is often one of several answering different tools.
	LeaveWaitingOnMissingHandler bool
}

// AnswerToolCalls answers the host tool calls a parked Invocation is
// waiting on, without streaming it.
//
// This is the unattended path. An Invocation's webhook target receives a signed
// invocation.waiting post when the turn parks, and a worker calls this to
// finish it, so a turn makes progress with nobody watching. The same handlers
// still serve an attached reader — the first accepted result per call wins, so
// the two coexist rather than being a per-deployment choice.
//
// Acknowledge the webhook before calling this. Webhook delivery uses a
// 10-second request timeout while a host tool budget is minutes, so a receiver
// that executes tools inline has its delivery marked failed and retried while
// the work is still running. Verify the signature, enqueue, return 2xx, and
// call this from the worker.
//
// Fence side effects with Claim. First-accepted-result deduplicates the
// transcript; it does not stop two processes from both beginning a call. An
// attached reader and this worker can race, and webhooks are
// at-least-once, so two deliveries can race each other.
//
// Reports how many results were submitted. Zero means the Invocation was no
// longer waiting or every call was claimed elsewhere — both ordinary outcomes.
func (a *Agent) AnswerToolCalls(
	ctx context.Context,
	invocationID string,
	options AnswerToolCallsOptions,
) (int, error) {
	invocation, err := a.client.GetInvocation(ctx, invocationID)
	if err != nil {
		return 0, err
	}
	answerable := AnswerableToolCalls(invocation)
	if invocation.Status != InvocationWaiting || len(answerable) == 0 {
		return 0, nil
	}
	handle := a.client.Invocation(invocationID)
	results := make([]ToolResult, 0, len(answerable))
	for _, pending := range answerable {
		if _, isCallback := a.callbackTools[pending.Name]; isCallback {
			continue
		}
		tool, ok := a.hostTools[pending.Name]
		if !ok || tool.Handler == nil {
			if options.LeaveWaitingOnMissingHandler {
				return 0, &MissingToolHandlerError{
					InvocationID: invocationID,
					ToolName:     pending.Name,
				}
			}
			continue
		}
		if options.Claim != nil {
			claimed, err := options.Claim(ctx, pending)
			if err != nil {
				return 0, err
			}
			if !claimed {
				continue
			}
		}
		content, err := tool.Handler(ctx, toolCallArguments(pending))
		result := ToolResult{ToolCallID: pending.ID, Content: content}
		if err != nil {
			result.Content = map[string]any{
				"error": err.Error(),
				"type":  fmt.Sprintf("%T", err),
			}
			result.IsError = true
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		return 0, nil
	}
	if _, err := handle.SubmitToolResults(ctx, results); err != nil {
		return 0, err
	}
	return len(results), nil
}

// waitingChange reports whether a transcript.update carries a change parking
// this turn on your tools.
func waitingChange(event StreamEvent, invocationID string) bool {
	if event.Type != "transcript.update" {
		return false
	}
	var update generated.TranscriptUpdateEvent
	if json.Unmarshal(event.Data, &update) != nil {
		return false
	}
	for _, change := range update.InvocationChanges {
		if change.InvocationID == invocationID && change.Status == generated.InvocationStatusWaiting {
			return true
		}
	}
	return false
}
