package nvoken

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

// Agent is a remotely resolved Agent and a directly runnable local handle.
type Agent struct {
	client *Client
	value  AgentResource
	tools  map[string]Tool
}

func (a *Agent) Resource() AgentResource { return a.value }

func (a *Agent) BindTools(tools ...Tool) *Agent {
	clone := *a
	clone.tools = bindToolMap(a.tools, tools)
	return &clone
}

func (a *Agent) Conversation(options ConversationOptions) *Conversation {
	return newConversation(a.client, a, options)
}

func (a *Agent) Start(ctx context.Context, input TurnInput, options TurnOptions) (*Turn, error) {
	selector := generated.AgentSelector{}
	revision := generated.AgentRevisionSelector{}
	if err := revision.FromAgentRevisionSelector0(generated.AgentRevisionSelector0("current")); err != nil {
		return nil, validationError("encode current Agent revision", err)
	}
	if err := selector.FromAgentSelectorByID(generated.AgentSelectorByID{
		AgentID:  a.value.ID,
		Revision: revision,
	}); err != nil {
		return nil, validationError("encode Agent selection", err)
	}
	behavior := generated.TurnBehaviorSelection{}
	if err := behavior.FromAgentTurnBehavior(generated.AgentTurnBehavior{
		Agent: selector,
		Kind:  generated.AgentTurnBehaviorKindAgent,
	}); err != nil {
		return nil, validationError("encode Agent behavior", err)
	}
	return a.client.startTurn(ctx, input, &behavior, options, a.tools)
}

func (a *Agent) Run(ctx context.Context, input TurnInput, options TurnOptions) (*TurnResult, error) {
	turn, err := a.Start(ctx, input, options)
	if err != nil {
		return nil, err
	}
	return turn.Result(ctx)
}

func (a *Agent) Text(ctx context.Context, input TurnInput, options TurnOptions) (string, error) {
	result, err := a.Run(ctx, input, options)
	if err != nil {
		return "", err
	}
	if result.OutputText == nil {
		return "", &NoOutputTextError{TurnID: result.Resource.ID}
	}
	return *result.OutputText, nil
}

func (a *Agent) Publish(ctx context.Context, behavior Behavior, options PublishOptions) (*AgentRevision, error) {
	if behavior.OutputSchema != nil {
		if err := PreflightOutputSchema(*behavior.OutputSchema); err != nil {
			return nil, err
		}
	}
	idempotencyKey := options.IdempotencyKey
	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = generatedIdempotencyKey()
	}
	wireBehavior := behavior.generated()
	value, err := callReplaySafe(ctx, a.client.retry, true, func() (callResult[generated.AgentRevision], error) {
		response, callErr := a.client.raw.PublishAgentRevisionWithResponse(
			ctx,
			a.value.ID,
			&generated.PublishAgentRevisionParams{IdempotencyKey: idempotencyKey},
			wireBehavior,
		)
		if callErr != nil {
			return callResult[generated.AgentRevision]{}, callErr
		}
		return callResult[generated.AgentRevision]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	a.value.CurrentRevision = value.Revision
	return value, nil
}

func (a *Agent) Archive(ctx context.Context) (*Agent, error) {
	response, err := a.client.raw.ArchiveAgentWithResponse(ctx, a.value.ID)
	if err != nil {
		return nil, transportError(err)
	}
	if response.JSON200 == nil {
		return nil, errorFromResponse(response.StatusCode(), responseHeader(response.HTTPResponse), response.Body)
	}
	a.value = *response.JSON200
	return a, nil
}

func (a *Agent) Restore(ctx context.Context) (*Agent, error) {
	response, err := a.client.raw.RestoreAgentWithResponse(ctx, a.value.ID)
	if err != nil {
		return nil, transportError(err)
	}
	if response.JSON200 == nil {
		return nil, errorFromResponse(response.StatusCode(), responseHeader(response.HTTPResponse), response.Body)
	}
	a.value = *response.JSON200
	return a, nil
}

// InlineRunner runs immutable behavior without creating an Agent.
type InlineRunner struct {
	client   *Client
	behavior Behavior
	tools    map[string]Tool
}

func (r *InlineRunner) BindTools(tools ...Tool) *InlineRunner {
	clone := *r
	clone.tools = bindToolMap(r.tools, tools)
	return &clone
}

func (r *InlineRunner) Conversation(options ConversationOptions) *Conversation {
	return newConversation(r.client, r, options)
}

func (r *InlineRunner) Start(ctx context.Context, input TurnInput, options TurnOptions) (*Turn, error) {
	if r.behavior.OutputSchema != nil {
		if err := PreflightOutputSchema(*r.behavior.OutputSchema); err != nil {
			return nil, err
		}
	}
	if err := validateInlineMemorySelection(options.Memory); err != nil {
		return nil, err
	}
	behavior := generated.TurnBehaviorSelection{}
	if err := behavior.FromInlineTurnBehavior(generated.InlineTurnBehavior{
		Behavior: r.behavior.generated(),
		Kind:     generated.InlineTurnBehaviorKindInline,
	}); err != nil {
		return nil, validationError("encode inline behavior", err)
	}
	return r.client.startTurn(ctx, input, &behavior, options, r.tools)
}

func (r *InlineRunner) Run(ctx context.Context, input TurnInput, options TurnOptions) (*TurnResult, error) {
	turn, err := r.Start(ctx, input, options)
	if err != nil {
		return nil, err
	}
	return turn.Result(ctx)
}

func (r *InlineRunner) Text(ctx context.Context, input TurnInput, options TurnOptions) (string, error) {
	result, err := r.Run(ctx, input, options)
	if err != nil {
		return "", err
	}
	if result.OutputText == nil {
		return "", &NoOutputTextError{TurnID: result.Resource.ID}
	}
	return *result.OutputText, nil
}

type runnable interface {
	Start(context.Context, TurnInput, TurnOptions) (*Turn, error)
	Run(context.Context, TurnInput, TurnOptions) (*TurnResult, error)
	Text(context.Context, TurnInput, TurnOptions) (string, error)
}

// Conversation fixes continuity, actor, memory, and maximum limits. Calls
// through one handle are serialized in this process.
type Conversation struct {
	runner  runnable
	options ConversationOptions
	mu      *sync.Mutex
}

func newConversation(client *Client, runner runnable, options ConversationOptions) *Conversation {
	bound := cloneConversationOptions(options)
	key := "id:" + bound.Selection.ID
	if bound.Selection.ID == "" {
		owner := "tenant"
		if bound.Selection.Owner.kind == conversationOwnerUser {
			owner = "user:" + bound.Selection.Owner.user
		}
		key = "key:" + bound.TenantKey + ":" + owner + ":" + bound.Selection.Key
	}
	return &Conversation{runner: runner, options: bound, mu: client.conversationLocks.lock(key)}
}

func (c *Conversation) Start(ctx context.Context, input TurnInput, options ...ConversationTurnOptions) (*Turn, error) {
	turnOptions, err := c.turnOptions(options)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runner.Start(ctx, input, turnOptions)
}

func (c *Conversation) Run(ctx context.Context, input TurnInput, options ...ConversationTurnOptions) (*TurnResult, error) {
	turnOptions, err := c.turnOptions(options)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runner.Run(ctx, input, turnOptions)
}

func (c *Conversation) Text(ctx context.Context, input TurnInput, options ...ConversationTurnOptions) (string, error) {
	turnOptions, err := c.turnOptions(options)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runner.Text(ctx, input, turnOptions)
}

func (c *Conversation) turnOptions(options []ConversationTurnOptions) (TurnOptions, error) {
	if len(options) > 1 {
		return TurnOptions{}, &Error{Category: ErrorValidation, Message: "Conversation call accepts at most one options value"}
	}
	result := TurnOptions{
		TenantKey:    c.options.TenantKey,
		UserKey:      c.options.UserKey,
		Conversation: cloneConversationSelection(&c.options.Selection),
		Memory:       cloneMemorySelection(c.options.Memory),
		Limits:       cloneLimits(c.options.Limits),
	}
	if len(options) == 0 {
		return result, nil
	}
	result.IdempotencyKey = options[0].IdempotencyKey
	result.Metadata = cloneStringMap(options[0].Metadata)
	result.Wait = options[0].Wait
	limits, err := narrowConversationLimits(c.options.Limits, options[0].Limits)
	if err != nil {
		return TurnOptions{}, err
	}
	result.Limits = limits
	return result, nil
}

// Turn is a recovery handle. Constructing it performs no request.
type Turn struct {
	client         *Client
	id             string
	access         TurnAccess
	idempotencyKey string
	admission      *TurnAdmission
	tools          map[string]Tool
	toolState      *toolExecutionState
	wait           WaitOptions
}

func (t *Turn) ID() string { return t.id }

func (t *Turn) IdempotencyKey() string { return t.idempotencyKey }

func (t *Turn) BindTools(tools ...Tool) *Turn {
	clone := *t
	clone.tools = bindToolMap(t.tools, tools)
	return &clone
}

func (t *Turn) Status(ctx context.Context) (*TurnSnapshot, error) {
	if err := t.validateAccess(); err != nil {
		return nil, err
	}
	response, err := t.client.raw.GetTurnResultWithResponse(ctx, t.id, t.requestEditor())
	if err != nil {
		return nil, transportError(err)
	}
	if response.JSON200 == nil {
		return nil, errorFromResponse(response.StatusCode(), responseHeader(response.HTTPResponse), response.Body)
	}
	return &TurnSnapshot{
		Resource:   response.JSON200.Turn,
		Messages:   response.JSON200.Messages,
		OutputText: response.JSON200.OutputText,
	}, nil
}

func (t *Turn) Result(ctx context.Context) (*TurnResult, error) {
	interval := t.wait.PollInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	for {
		current, err := t.Status(ctx)
		if err != nil {
			return nil, turnWaitError(err, t)
		}
		if current.Resource.Status == generated.TurnStatusWaiting {
			settled, err := t.settleHostTools(ctx, current.Resource.ToolCalls)
			if err != nil {
				return nil, err
			}
			if settled {
				continue
			}
		}
		if current.Resource.EndedAt != nil {
			result := &TurnResult{TurnSnapshot: *current, Turn: t, Admission: t.admission}
			if current.Resource.Status == generated.TurnStatusFailed || current.Resource.Status == generated.TurnStatusCancelled {
				return nil, &TurnExecutionError{Result: result}
			}
			return result, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, turnWaitError(transportError(ctx.Err()), t)
		case <-timer.C:
		}
	}
}

func (t *Turn) settleHostTools(ctx context.Context, calls []ToolCallSummary) (bool, error) {
	results := make([]toolResult, 0)
	for _, call := range calls {
		if call.Mode != generated.ToolCallModeHost || call.Arguments == nil {
			continue
		}
		tool, ok := t.tools[call.Name]
		if !ok || tool.Handler == nil {
			continue
		}
		result, err := t.toolState.resolve(ctx, t.id, call, tool.Handler)
		if err != nil {
			return false, err
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		return false, nil
	}
	if _, err := t.submitToolResults(ctx, results); err != nil {
		return false, err
	}
	return true, nil
}

func narrowConversationLimits(base, override *Limits) (*Limits, error) {
	if override == nil {
		return cloneLimits(base), nil
	}
	if base == nil {
		return cloneLimits(override), nil
	}
	result := *cloneLimits(base)
	for _, pair := range []struct {
		name     string
		base     **int
		override *int
	}{
		{"active timeout", &result.ActiveTimeoutSeconds, override.ActiveTimeoutSeconds},
		{"max iterations", &result.MaxIterations, override.MaxIterations},
		{"max output tokens", &result.MaxOutputTokens, override.MaxOutputTokens},
		{"total timeout", &result.TotalTimeoutSeconds, override.TotalTimeoutSeconds},
		{"waiting timeout", &result.WaitingTimeoutSeconds, override.WaitingTimeoutSeconds},
	} {
		if pair.override == nil {
			continue
		}
		if *pair.base != nil && *pair.override > **pair.base {
			return nil, &Error{Category: ErrorValidation, Message: "Conversation call cannot widen " + pair.name}
		}
		value := *pair.override
		*pair.base = &value
	}
	if override.MaxEstimatedCostUsd != nil {
		if result.MaxEstimatedCostUsd != nil && *override.MaxEstimatedCostUsd > *result.MaxEstimatedCostUsd {
			return nil, &Error{Category: ErrorValidation, Message: "Conversation call cannot widen max estimated cost"}
		}
		value := *override.MaxEstimatedCostUsd
		result.MaxEstimatedCostUsd = &value
	}
	return &result, nil
}

func (t *Turn) submitToolResults(ctx context.Context, results []toolResult) (*generated.SubmitHostToolResultsResponse, error) {
	if err := t.validateAccess(); err != nil {
		return nil, err
	}
	body := generated.SubmitHostToolResultsRequest{}
	body.Results = make([]struct {
		Content    interface{}          `json:"content"`
		IsError    *bool                `json:"is_error,omitempty"`
		ToolCallID generated.ToolCallID `json:"tool_call_id"`
	}, 0, len(results))
	for _, result := range results {
		var isError *bool
		if result.IsError {
			value := true
			isError = &value
		}
		body.Results = append(body.Results, struct {
			Content    interface{}          `json:"content"`
			IsError    *bool                `json:"is_error,omitempty"`
			ToolCallID generated.ToolCallID `json:"tool_call_id"`
		}{
			Content:    result.Content,
			IsError:    isError,
			ToolCallID: result.ToolCallID,
		})
	}
	response, err := t.client.raw.SubmitHostToolResultsWithResponse(ctx, t.id, body, t.requestEditor())
	if err != nil {
		return nil, transportError(err)
	}
	if response.JSON202 == nil {
		return nil, errorFromResponse(response.StatusCode(), responseHeader(response.HTTPResponse), response.Body)
	}
	return response.JSON202, nil
}

func (t *Turn) validateAccess() error {
	if strings.TrimSpace(t.access.TenantKey) == "" {
		return &Error{Category: ErrorValidation, Message: "Turn tenant key is required"}
	}
	return nil
}

func (t *Turn) requestEditor() generated.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		if t.access.TenantKey != "" {
			request.Header.Set("X-Nvoken-Tenant-Key", t.access.TenantKey)
		}
		if t.access.UserKey != "" {
			request.Header.Set("X-Nvoken-User-Key", t.access.UserKey)
		}
		return nil
	}
}

type toolExecutionState struct {
	mu      sync.Mutex
	results map[string]toolResult
	running map[string]chan struct{}
}

func newToolExecutionState() *toolExecutionState {
	return &toolExecutionState{
		results: make(map[string]toolResult),
		running: make(map[string]chan struct{}),
	}
}

func (s *toolExecutionState) resolve(
	ctx context.Context,
	turnID string,
	call ToolCallSummary,
	handler ToolHandler,
) (toolResult, error) {
	for {
		s.mu.Lock()
		if result, ok := s.results[call.ID]; ok {
			s.mu.Unlock()
			return result, nil
		}
		if done := s.running[call.ID]; done != nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return toolResult{}, transportError(ctx.Err())
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		s.running[call.ID] = done
		s.mu.Unlock()

		content, handlerErr := handler(ctx, *call.Arguments, TurnToolContext{
			TurnID:     turnID,
			ToolCallID: call.ID,
		})
		result := toolResult{ToolCallID: call.ID, Content: content}
		if handlerErr != nil {
			result.IsError = true
			result.Content = map[string]any{"error": handlerErr.Error()}
		}

		s.mu.Lock()
		s.results[call.ID] = result
		delete(s.running, call.ID)
		close(done)
		s.mu.Unlock()
		return result, nil
	}
}

func validateInlineMemorySelection(selection *MemorySelection) error {
	if selection == nil || selection.Scope == "none" {
		return nil
	}
	if (selection.Scope == "tenant" || selection.Scope == "user") && strings.TrimSpace(selection.Namespace) == "" {
		return &Error{Category: ErrorValidation, Message: "inline tenant and user memory require an explicit namespace"}
	}
	return nil
}

func cloneConversationOptions(options ConversationOptions) ConversationOptions {
	return ConversationOptions{
		TenantKey: options.TenantKey,
		UserKey:   options.UserKey,
		Selection: *cloneConversationSelection(&options.Selection),
		Memory:    cloneMemorySelection(options.Memory),
		Limits:    cloneLimits(options.Limits),
	}
}

func cloneConversationSelection(selection *ConversationSelection) *ConversationSelection {
	if selection == nil {
		return nil
	}
	return &ConversationSelection{
		ID:       selection.ID,
		Key:      selection.Key,
		Owner:    selection.Owner,
		Metadata: cloneAnyMap(selection.Metadata),
	}
}

func cloneMemorySelection(selection *MemorySelection) *MemorySelection {
	if selection == nil {
		return nil
	}
	clone := *selection
	return &clone
}

func cloneLimits(limits *Limits) *Limits {
	if limits == nil {
		return nil
	}
	clone := *limits
	clone.ActiveTimeoutSeconds = cloneInt(limits.ActiveTimeoutSeconds)
	clone.MaxEstimatedCostUsd = cloneFloat32(limits.MaxEstimatedCostUsd)
	clone.MaxIterations = cloneInt(limits.MaxIterations)
	clone.MaxOutputTokens = cloneInt(limits.MaxOutputTokens)
	clone.TotalTimeoutSeconds = cloneInt(limits.TotalTimeoutSeconds)
	clone.WaitingTimeoutSeconds = cloneInt(limits.WaitingTimeoutSeconds)
	return &clone
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneFloat32(value *float32) *float32 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	clone := make(map[string]any, len(values))
	for key, value := range values {
		clone[key] = cloneJSONValue(value)
	}
	return clone
}

func cloneJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneAnyMap(value)
	case []any:
		clone := make([]any, len(value))
		for index, item := range value {
			clone[index] = cloneJSONValue(item)
		}
		return clone
	case map[string]string:
		return cloneStringMap(value)
	default:
		return value
	}
}

func bindToolMap(existing map[string]Tool, additions []Tool) map[string]Tool {
	bound := make(map[string]Tool, len(existing)+len(additions))
	for name, tool := range existing {
		bound[name] = tool
	}
	for _, tool := range additions {
		bound[tool.Name] = tool
	}
	return bound
}

func validationError(message string, err error) error {
	return &Error{Category: ErrorValidation, Message: fmt.Sprintf("%s: %v", message, err), Cause: err}
}
