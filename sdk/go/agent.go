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

// AgentOptions declares one tenant's Agent. Nothing here reaches the server
// when the Agent is constructed: (TenantKey, AgentKey) names the record and
// DefinitionKey names the Agent Definition it follows, so the first use — or
// an explicit Ensure — creates the record if it is missing. Supply AgentID
// instead to name a record that already exists.
//
// Tools register this process's handlers; their declarations live on the
// Agent's versioned Agent Definition.
type AgentOptions struct {
	AgentID   string
	AgentKey  string
	TenantKey *string
	// DefinitionKey is the definition_key of the Agent Definition this Agent
	// follows. AgentDefinitionID is the same pointer by opaque ID; supply at
	// most one, and only alongside AgentKey.
	DefinitionKey     string
	AgentDefinitionID string
	// PinnedRevision holds this instance to one Definition revision. Leave it
	// nil to follow the latest, which is what makes revising the Definition
	// the rollout.
	PinnedRevision *int64
	// Name is the display name recorded at creation. It defaults to AgentKey.
	Name  string
	Tools []Tool
	// MCPServerHeaders carries per-turn secret headers for the MCP servers this
	// Agent declares. They stay outside the Agent Definition so no reusable
	// revision depends on a secret.
	MCPServerHeaders  []MCPServerHeaders
	ProviderKeys      []ProviderKeySelection
	Webhook           *WebhookTarget
	OnBudgetExhausted BudgetExhaustionBehavior
}

type AgentInvocationOptions struct {
	IdempotencyKey    string
	AgentRevision     *int64
	Overrides         *AgentDefinitionOverrides
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
	// that acknowledgement, not from here. It is now a fallback for a server
	// too old to stamp `mode` on a summary; see runsLocally.
	hostTools     map[string]Tool
	callbackTools map[string]struct{}

	// mu guards record, and serializes the creation itself so concurrent
	// first uses of one declared Agent make one create between them.
	mu     sync.Mutex
	record *AgentResource
}

// runsLocally reports whether a pending call is this caller's to execute.
//
// The call's own mode is the authority, not what this Agent happens to
// declare: an Invocation running a server-owned Agent Definition can park on
// callback tools this object never listed, and answering those here would run
// work nvoken is already delivering elsewhere.
//
// A server too old to send mode leaves it empty, where a name check is all
// there is. Fall back to it rather than treating every call as somebody
// else's, which would leave the turn parked forever.
func (a *Agent) runsLocally(call ToolCallSummary) bool {
	if call.Mode != "" {
		return call.Mode == ToolCallModeHost
	}
	_, isCallback := a.callbackTools[call.Name]
	return !isCallback
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
	if (options.AgentID == "") == (options.AgentKey == "") {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "Supply exactly one of Agent ID and Agent key",
		}
	}
	if options.DefinitionKey != "" && options.AgentDefinitionID != "" {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "Supply at most one of Definition key and Agent Definition ID",
		}
	}
	if options.AgentID != "" && (options.DefinitionKey != "" || options.AgentDefinitionID != "") {
		return nil, &Error{
			Category: ErrorValidation,
			Message: "An Agent named by ID already names its record; the Definition" +
				" belongs to a declaration by Agent key",
		}
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

// Ensure creates this Agent's server record if it is missing and returns it.
//
// Creation never mutates: the same keys backed by the same Definition resolve
// onto the existing record, a different Definition is agent_key_conflict, an
// archived record is agent_archived, and a declared PinnedRevision that the
// record does not follow is refused rather than quietly ignored. A first use
// calls this for you; call it directly to create the record at a moment you
// choose, or to read back what the server holds.
func (a *Agent) Ensure(ctx context.Context) (*AgentResource, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.record != nil {
		return a.record, nil
	}
	if a.options.DefinitionKey != "" || a.options.AgentDefinitionID != "" {
		record, err := a.client.CreateAgent(ctx, CreateAgentInput{
			TenantKey:         a.options.TenantKey,
			AgentKey:          a.options.AgentKey,
			Name:              a.options.Name,
			AgentDefinitionID: a.options.AgentDefinitionID,
			DefinitionKey:     a.options.DefinitionKey,
			PinnedRevision:    a.options.PinnedRevision,
		})
		if err != nil {
			return nil, err
		}
		if err := a.verifyDeclaration(record); err != nil {
			return nil, err
		}
		a.record = record
		return record, nil
	}
	if a.options.AgentID != "" {
		record, err := a.client.GetAgent(ctx, a.options.AgentID)
		if err != nil {
			return nil, err
		}
		a.record = record
		return record, nil
	}
	return nil, &Error{
		Category: ErrorValidation,
		Message: "Agent " + a.options.AgentKey + " cannot be created without knowing" +
			" which Agent Definition it follows: declare a Definition key, or" +
			" declare an Agent ID for a record that already exists",
	}
}

// Refresh re-reads the record from the server.
func (a *Agent) Refresh(ctx context.Context) (*AgentResource, error) {
	ensured, err := a.Ensure(ctx)
	if err != nil {
		return nil, err
	}
	record, err := a.client.GetAgent(ctx, ensured.ID)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.record = record
	return record, nil
}

// Resource returns the server record once Ensure or a first use has run.
func (a *Agent) Resource() *AgentResource {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.record
}

// ID returns the record's ID once it is known, and otherwise the declared one.
func (a *Agent) ID() string {
	if record := a.Resource(); record != nil {
		return record.ID
	}
	return a.options.AgentID
}

// verifyDeclaration refuses a record that contradicts the declaration.
// Creation resolves on the Definition pointer, which the server already
// guards; the pin is the remaining substantive field, and a declaration naming
// a revision the record does not follow would otherwise run configuration the
// caller never asked for. A differing Name is cosmetic and is left alone.
func (a *Agent) verifyDeclaration(record *AgentResource) error {
	declared := a.options.PinnedRevision
	if declared == nil {
		return nil
	}
	if record.PinnedRevision != nil && *record.PinnedRevision == *declared {
		return nil
	}
	following := "latest"
	if record.PinnedRevision != nil {
		following = fmt.Sprintf("revision %d", *record.PinnedRevision)
	}
	return &Error{
		Category: ErrorConflict,
		Status:   409,
		Code:     "agent_pin_conflict",
		Message: fmt.Sprintf(
			"Agent %s follows %s, not the declared revision %d",
			record.AgentKey, following, *declared,
		),
		Details: map[string]any{"agent_id": record.ID},
	}
}

// ready creates the record before the turn that needs it, and only when the
// declaration says how. An Agent named by ID, or by Agent key alone, is one
// the caller has said already exists: admitting it directly keeps the turn at
// one round trip and lets the server answer for a key that names nothing.
func (a *Agent) ready(ctx context.Context) error {
	if a.options.DefinitionKey == "" && a.options.AgentDefinitionID == "" {
		return nil
	}
	_, err := a.Ensure(ctx)
	return err
}

func (a *Agent) Invoke(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
) (*InvocationHandle, error) {
	if err := a.ready(ctx); err != nil {
		return nil, err
	}
	return a.client.Invoke(ctx, a.request(input, options))
}

// request composes the Agent identity with one call's steering and secrets. It
// is the single place a per-call option reaches the wire, so the conformance
// suite can pin the whole admitted body without a server.
func (a *Agent) request(input string, options AgentInvocationOptions) InvokeRequest {
	onBudgetExhausted := options.OnBudgetExhausted
	if onBudgetExhausted == "" {
		onBudgetExhausted = a.options.OnBudgetExhausted
	}
	// The record's ID wins once it is known, so a declared Agent stops being
	// re-resolved by key on every turn.
	agentID, agentKey := a.options.AgentID, a.options.AgentKey
	if record := a.Resource(); record != nil {
		agentID, agentKey = record.ID, ""
	}
	request := InvokeRequest{
		AgentID:           agentID,
		AgentKey:          agentKey,
		TenantKey:         a.options.TenantKey,
		SessionID:         options.SessionID,
		SessionKey:        options.SessionKey,
		SessionOptions:    options.SessionOptions,
		IdempotencyKey:    options.IdempotencyKey,
		AgentRevision:     options.AgentRevision,
		Overrides:         options.Overrides,
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
	key := "id:" + binding.SessionID
	if binding.SessionID == "" {
		tenant := "default"
		if a.options.TenantKey != nil {
			tenant = *a.options.TenantKey
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
		if !a.runsLocally(pending) {
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
}

type AgentSession struct {
	agent      *Agent
	lock       *sync.Mutex
	sessionID  string
	sessionKey string
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
		if !a.runsLocally(pending) {
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
