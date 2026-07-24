package nvoken

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

type Invocation = generated.Invocation
type InvocationResult = generated.InvocationResult
type InvocationStatus = generated.InvocationStatus
type Session = generated.Session
type SessionMessage = generated.SessionMessage
type PendingHostToolCall = generated.PendingHostToolCall
type ToolResultResponse = generated.SubmitHostToolResultsResponse
type ModelProvider = generated.ModelProvider
type ModelDescriptor = generated.ModelDescriptor
type ModelPricing = generated.ModelPricing
type ProviderCredential = generated.ProviderCredential
type ProviderCredentialScope = generated.ProviderCredentialScope
type ProviderCredentialStatus = generated.ProviderCredentialStatus
type InvocationChange = generated.InvocationChange

const (
	InvocationQueued                              = generated.InvocationStatusQueued
	InvocationRunning                             = generated.InvocationStatusRunning
	InvocationWaiting                             = generated.InvocationStatusWaiting
	InvocationCompleted                           = generated.InvocationStatusCompleted
	InvocationFailed                              = generated.InvocationStatusFailed
	InvocationCancelled                           = generated.InvocationStatusCancelled
	ModelProviderAnthropic          ModelProvider = "anthropic"
	ModelProviderOpenAI             ModelProvider = "openai"
	ProviderCredentialScopeAccount                = generated.Account
	ProviderCredentialScopeTenant                 = generated.Tenant
	ProviderCredentialStatusActive                = generated.ProviderCredentialStatusActive
	ProviderCredentialStatusRevoked               = generated.ProviderCredentialStatusRevoked
)

type ModelList struct {
	CatalogVersion string            `json:"catalog_version"`
	Items          []ModelDescriptor `json:"items"`
}

type InvocationList struct {
	HasMore    bool         `json:"has_more"`
	Items      []Invocation `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

type SessionList struct {
	HasMore    bool      `json:"has_more"`
	Items      []Session `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

type SessionMessageList struct {
	HasMore    bool             `json:"has_more"`
	Items      []SessionMessage `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

type ProviderCredentialList struct {
	HasMore    bool                 `json:"has_more"`
	Items      []ProviderCredential `json:"items"`
	NextCursor *string              `json:"next_cursor"`
}

type TranscriptSnapshot struct {
	HasMore           bool               `json:"has_more"`
	InvocationChanges []InvocationChange `json:"invocation_changes"`
	Messages          []SessionMessage   `json:"messages"`
	NextPageToken     *string            `json:"next_page_token"`
	ResumeCursor      string             `json:"resume_cursor"`
}

type TranscriptDrain struct {
	InvocationChanges []InvocationChange `json:"invocation_changes"`
	Messages          []SessionMessage   `json:"messages"`
	ResumeCursor      string             `json:"resume_cursor"`
}

type Model struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type Limits struct {
	TotalTimeoutSeconds   *int     `json:"total_timeout_seconds,omitempty"`
	ActiveTimeoutSeconds  *int     `json:"active_timeout_seconds,omitempty"`
	WaitingTimeoutSeconds *int     `json:"waiting_timeout_seconds,omitempty"`
	MaxOutputTokens       *int     `json:"max_output_tokens,omitempty"`
	MaxEstimatedCostUSD   *float32 `json:"max_estimated_cost_usd,omitempty"`
	MaxIterations         *int     `json:"max_iterations,omitempty"`
}

type ToolMode string

const (
	ToolModeHost     ToolMode = "host"
	ToolModeCallback ToolMode = "callback"
)

type ToolHandler func(context.Context, any) (any, error)

type Tool struct {
	Mode        ToolMode        `json:"mode"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema map[string]any  `json:"input_schema"`
	Callback    *CallbackTarget `json:"callback,omitempty"`
	Handler     ToolHandler     `json:"-"`
}

type CallbackTarget struct {
	URL string `json:"url"`
}

type ExecutionSpec struct {
	Instructions string         `json:"instructions"`
	Model        Model          `json:"model"`
	Limits       *Limits        `json:"limits,omitempty"`
	Tools        []Tool         `json:"tools,omitempty"`
	OutputSchema map[string]any `json:"-"`
}

type InvokeRequest struct {
	AgentKey            string
	TenantKey           *string
	SessionID           *string
	SessionKey          *string
	IdempotencyKey      string
	Input               string
	Spec                ExecutionSpec
	ProviderCredentials []ProviderCredentialSelection
}

type ProviderCredentialSelection struct {
	Provider string
	Source   ProviderCredentialSource
	APIKey   string
}

type ProviderCredentialSource string

const (
	ProviderCredentialCallerEphemeral ProviderCredentialSource = "caller_ephemeral"
	ProviderCredentialAccountBYOK     ProviderCredentialSource = "account_byok"
	ProviderCredentialTenantBYOK      ProviderCredentialSource = "tenant_byok"
	ProviderCredentialPlatform        ProviderCredentialSource = "platform"
)

type ListProviderCredentialsOptions struct {
	Provider  *ModelProvider
	Scope     *ProviderCredentialScope
	Status    *ProviderCredentialStatus
	TenantKey *string
	Cursor    *string
	Limit     *int
}

type ListModelsOptions struct {
	Provider          *ModelProvider
	IncludeDeprecated *bool
}

type CreateProviderCredentialInput struct {
	Provider       ModelProvider
	Scope          ProviderCredentialScope
	TenantKey      *string
	APIKey         string
	ExpiresAt      *time.Time
	IdempotencyKey string
}

type RotateProviderCredentialInput struct {
	APIKey         string
	ExpiresAt      *time.Time
	OverlapSeconds *int
	IdempotencyKey string
}

type ToolResult struct {
	ToolCallID string
	Content    any
	IsError    bool
}

type ListInvocationsOptions struct {
	TenantKey     *string
	DefaultTenant *bool
	SessionID     *string
	AgentID       *string
	Status        *InvocationStatus
	Cursor        *string
	Limit         *int
}

type ListSessionsOptions struct {
	TenantKey     *string
	DefaultTenant *bool
	AgentID       *string
	SessionKey    *string
	Cursor        *string
	Limit         *int
}

type MessageListOptions struct {
	Cursor *string
	Limit  *int
}

type TranscriptOptions struct {
	Cursor    *string
	PageToken *string
	Limit     *int
}

type WaitOptions struct {
	MinPollInterval time.Duration
	MaxPollInterval time.Duration
	Until           WaitCondition
	Timeout         time.Duration
}

type WaitCondition string

const (
	WaitUntilTerminal   WaitCondition = "terminal"
	WaitUntilActionable WaitCondition = "actionable"
)

func (o WaitOptions) normalized() WaitOptions {
	if o.MinPollInterval <= 0 {
		o.MinPollInterval = 100 * time.Millisecond
	}
	if o.MaxPollInterval <= 0 {
		o.MaxPollInterval = 2 * time.Second
	}
	if o.MaxPollInterval < o.MinPollInterval {
		o.MaxPollInterval = o.MinPollInterval
	}
	if o.Until == "" {
		o.Until = WaitUntilTerminal
	}
	return o
}

func (r InvokeRequest) generated() (generated.CreateInvocationRequest, error) {
	if r.AgentKey == "" {
		return generated.CreateInvocationRequest{}, fmt.Errorf("agent key is required")
	}
	if r.Input == "" {
		return generated.CreateInvocationRequest{}, fmt.Errorf("input is required")
	}
	spec := map[string]any{"model": r.Spec.Model}
	if r.Spec.Instructions != "" {
		spec["instructions"] = r.Spec.Instructions
	}
	if r.Spec.Limits != nil {
		spec["limits"] = r.Spec.Limits
	}
	if len(r.Spec.Tools) > 0 {
		for _, tool := range r.Spec.Tools {
			switch tool.Mode {
			case ToolModeHost:
				if tool.Callback != nil {
					return generated.CreateInvocationRequest{}, fmt.Errorf(
						"host tool %q cannot include a callback target",
						tool.Name,
					)
				}
			case ToolModeCallback:
				if tool.Callback == nil || tool.Callback.URL == "" {
					return generated.CreateInvocationRequest{}, fmt.Errorf(
						"callback tool %q requires a callback target",
						tool.Name,
					)
				}
				if tool.Handler != nil {
					return generated.CreateInvocationRequest{}, fmt.Errorf(
						"callback tool %q cannot include a local handler",
						tool.Name,
					)
				}
			default:
				return generated.CreateInvocationRequest{}, fmt.Errorf(
					"tool %q has unsupported mode %q",
					tool.Name,
					tool.Mode,
				)
			}
		}
		spec["tools"] = r.Spec.Tools
	}
	if r.Spec.OutputSchema != nil {
		spec["output"] = map[string]any{"schema": r.Spec.OutputSchema}
	}
	wire := map[string]any{
		"agent_key":       r.AgentKey,
		"idempotency_key": r.IdempotencyKey,
		"input":           r.Input,
		"spec":            spec,
	}
	if r.TenantKey != nil {
		wire["tenant_key"] = *r.TenantKey
	}
	if r.SessionID != nil {
		wire["session_id"] = *r.SessionID
	}
	if r.SessionKey != nil {
		wire["session_key"] = *r.SessionKey
	}
	if len(r.ProviderCredentials) > 1 {
		return generated.CreateInvocationRequest{}, fmt.Errorf(
			"at most one provider credential selection is supported",
		)
	}
	if len(r.ProviderCredentials) == 1 {
		selection := r.ProviderCredentials[0]
		if selection.Provider == "" {
			return generated.CreateInvocationRequest{}, fmt.Errorf(
				"provider credential selection provider is required",
			)
		}
		item := map[string]any{
			"provider": selection.Provider,
			"source":   selection.Source,
		}
		switch selection.Source {
		case ProviderCredentialCallerEphemeral:
			if selection.APIKey == "" {
				return generated.CreateInvocationRequest{}, fmt.Errorf(
					"caller-ephemeral provider credentials require an API key",
				)
			}
			item["credential"] = map[string]any{"api_key": selection.APIKey}
		case ProviderCredentialAccountBYOK, ProviderCredentialTenantBYOK, ProviderCredentialPlatform:
			if selection.APIKey != "" {
				return generated.CreateInvocationRequest{}, fmt.Errorf(
					"%s provider credentials cannot include an API key",
					selection.Source,
				)
			}
		default:
			return generated.CreateInvocationRequest{}, fmt.Errorf(
				"unsupported provider credential source %q",
				selection.Source,
			)
		}
		wire["provider_credentials"] = []map[string]any{item}
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return generated.CreateInvocationRequest{}, fmt.Errorf("encode invocation: %w", err)
	}
	var request generated.CreateInvocationRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return generated.CreateInvocationRequest{}, fmt.Errorf("convert invocation to generated transport: %w", err)
	}
	return request, nil
}

func generatedModelProvider(provider string) (generated.ModelProvider, error) {
	value := generated.ModelProvider(provider)
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(value) {
		return "", fmt.Errorf("model provider must be a valid canonical identifier")
	}
	return value, nil
}

func generatedToolResults(results []ToolResult) (generated.SubmitHostToolResultsRequest, error) {
	wire := struct {
		Results []map[string]any `json:"results"`
	}{Results: make([]map[string]any, 0, len(results))}
	for _, result := range results {
		item := map[string]any{
			"tool_call_id": result.ToolCallID,
			"content":      result.Content,
		}
		if result.IsError {
			item["is_error"] = true
		}
		wire.Results = append(wire.Results, item)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return generated.SubmitHostToolResultsRequest{}, fmt.Errorf("encode tool results: %w", err)
	}
	var request generated.SubmitHostToolResultsRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return generated.SubmitHostToolResultsRequest{}, fmt.Errorf("convert tool results to generated transport: %w", err)
	}
	return request, nil
}

func terminal(status InvocationStatus) bool {
	return status == InvocationCompleted || status == InvocationFailed || status == InvocationCancelled
}

func waitSatisfied(status InvocationStatus, until WaitCondition) bool {
	switch until {
	case WaitUntilTerminal:
		return terminal(status)
	case WaitUntilActionable:
		return status == InvocationWaiting || terminal(status)
	default:
		return false
	}
}
