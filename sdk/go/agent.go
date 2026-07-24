package nvoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type AgentOptions struct {
	AgentKey            string
	TenantKey           *string
	Spec                ExecutionSpec
	ProviderCredentials []ProviderCredentialSelection
}

type AgentInvocationOptions struct {
	IdempotencyKey               string
	TenantKey                    *string
	SessionID                    *string
	SessionKey                   *string
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
	client    *Client
	options   AgentOptions
	hostTools map[string]Tool
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
	hostTools := make(map[string]Tool)
	for _, tool := range options.Spec.Tools {
		if tool.Mode == ToolModeHost {
			hostTools[tool.Name] = tool
		}
	}
	return &Agent{
		client:    client,
		options:   options,
		hostTools: hostTools,
	}, nil
}

func (a *Agent) Invoke(
	ctx context.Context,
	input string,
	options AgentInvocationOptions,
) (*InvocationHandle, error) {
	tenantKey := options.TenantKey
	if tenantKey == nil {
		tenantKey = a.options.TenantKey
	}
	return a.client.Invoke(ctx, InvokeRequest{
		AgentKey:            a.options.AgentKey,
		TenantKey:           tenantKey,
		SessionID:           options.SessionID,
		SessionKey:          options.SessionKey,
		IdempotencyKey:      options.IdempotencyKey,
		Input:               input,
		Spec:                a.options.Spec,
		ProviderCredentials: a.options.ProviderCredentials,
	})
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
		if event.Type != "invocation.update" && event.Type != "stream.end" {
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
	} else if len(a.options.Spec.Tools) > 0 {
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
				Message: fmt.Sprintf(
					"Invocation %s ended with status %s",
					handle.InvocationID,
					invocation.Status,
				),
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
	if invocation.PendingToolCalls == nil {
		return false, nil
	}
	results := make([]ToolResult, 0, len(*invocation.PendingToolCalls))
	for _, pending := range *invocation.PendingToolCalls {
		if _, alreadySubmitted := submitted[pending.ID]; alreadySubmitted {
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
		content, err := tool.Handler(ctx, pending.Input)
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
