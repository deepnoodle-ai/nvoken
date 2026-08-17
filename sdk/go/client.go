package nvoken

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

type RetryPolicy struct {
	MaxAttempts int
	MinDelay    time.Duration
	MaxDelay    time.Duration
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 4
	}
	if p.MinDelay <= 0 {
		p.MinDelay = 100 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 2 * time.Second
	}
	return p
}

type Client struct {
	raw          *generated.ClientWithResponses
	retry        RetryPolicy
	sessionMu    sync.Mutex
	sessionLocks map[string]*sync.Mutex
}

type ClientOption func(*clientOptions)

type clientOptions struct {
	httpClient *http.Client
	retry      RetryPolicy
}

func WithHTTPClient(client *http.Client) ClientOption {
	return func(options *clientOptions) { options.httpClient = client }
}

func WithRetryPolicy(policy RetryPolicy) ClientOption {
	return func(options *clientOptions) { options.retry = policy }
}

func NewClient(baseURL, apiKey string, options ...ClientOption) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	config := clientOptions{httpClient: &http.Client{}}
	for _, option := range options {
		option(&config)
	}
	requestEditor := func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer "+apiKey)
		request.Header.Set("User-Agent", "nvoken-go/"+Version)
		return nil
	}
	raw, err := generated.NewClientWithResponses(
		baseURL,
		generated.WithHTTPClient(config.httpClient),
		generated.WithRequestEditorFn(requestEditor),
	)
	if err != nil {
		return nil, fmt.Errorf("create generated client: %w", err)
	}
	return &Client{
		raw:          raw,
		retry:        config.retry.normalized(),
		sessionLocks: make(map[string]*sync.Mutex),
	}, nil
}

func (c *Client) Raw() *generated.ClientWithResponses { return c.raw }

type callResult[T any] struct {
	Value  *T
	Status int
	Header http.Header
	Body   []byte
}

func callReplaySafe[T any](ctx context.Context, policy RetryPolicy, replaySafe bool, call func() (callResult[T], error)) (*T, error) {
	policy = policy.normalized()
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		result, err := call()
		if err == nil && result.Status >= 200 && result.Status < 300 && result.Value != nil {
			return result.Value, nil
		}
		if err == nil && result.Status >= 200 && result.Status < 300 {
			return nil, &Error{Category: ErrorUnexpectedResponse, Status: result.Status, Message: "nvoken returned an empty success response"}
		}
		if err != nil {
			lastErr = transportError(err)
		} else {
			lastErr = errorFromResponse(result.Status, result.Header, result.Body)
		}
		if !replaySafe || attempt == policy.MaxAttempts || !retryable(err, result.Status) {
			return nil, lastErr
		}
		delay := retryDelay(policy, attempt, result.Header)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, transportError(ctx.Err())
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func retryable(transport error, status int) bool {
	if transport != nil {
		return true
	}
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryDelay(policy RetryPolicy, attempt int, header http.Header) time.Duration {
	if delay := parseRetryAfter(header.Get("Retry-After"), time.Now()); delay > 0 {
		if delay > policy.MaxDelay {
			return policy.MaxDelay
		}
		return delay
	}
	delay := policy.MinDelay << (attempt - 1)
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	if delay <= 1 {
		return delay
	}
	return delay/2 + time.Duration(mathrand.Int64N(int64(delay/2)+1))
}

// optionalString omits an empty value rather than sending it, so a field the
// runtime defaults stays defaulted there instead of being filled in twice.
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func responseHeader(response *http.Response) http.Header {
	if response == nil {
		return make(http.Header)
	}
	return response.Header
}

func (c *Client) Invoke(ctx context.Context, request InvokeRequest) (*InvocationHandle, error) {
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = generatedIdempotencyKey()
	}
	body, err := request.encoded()
	if err != nil {
		if sdkError, ok := err.(*Error); ok {
			return nil, sdkError
		}
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	invocation, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Invocation], error) {
		response, callErr := c.raw.CreateInvocationWithBodyWithResponse(
			ctx,
			&generated.CreateInvocationParams{},
			"application/json",
			bytes.NewReader(body),
		)
		if callErr != nil {
			return callResult[generated.Invocation]{}, callErr
		}
		value := response.JSON202
		if true {
		}
		return callResult[generated.Invocation]{
			Value:  value,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &InvocationHandle{
		client:         c,
		InvocationID:   invocation.ID,
		IdempotencyKey: request.IdempotencyKey,
		SessionID:      invocation.SessionID,
		AgentID:        agentIDOrEmpty(invocation.AgentID),
		Status:         invocation.Status,
		Deduplicated:   invocation.Deduplicated,
		DeadlineAt:     invocation.DeadlineAt,
	}, nil
}

func (c *Client) Invocation(invocationID string) *InvocationHandle {
	return &InvocationHandle{client: c, InvocationID: invocationID}
}

// CreateAgentDefinition creates a stable App-owned Agent Definition resource.
// The idempotency key makes retries safe; equal definitions created with
// different keys receive different resource IDs.
func (c *Client) CreateAgentDefinition(
	ctx context.Context,
	input CreateAgentDefinitionInput,
) (*AgentDefinitionResource, error) {
	if input.IdempotencyKey == "" {
		return nil, &Error{Category: ErrorValidation, Message: "Agent Definition idempotency key is required"}
	}
	if input.DefinitionKey == "" || input.Name == "" {
		return nil, &Error{Category: ErrorValidation, Message: "Agent Definition key and name are required"}
	}
	encoded, err := input.Definition.encoded()
	if err != nil {
		if sdkError, ok := err.(*Error); ok {
			return nil, sdkError
		}
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	encoded["definition_key"] = input.DefinitionKey
	encoded["name"] = input.Name
	body, err := json.Marshal(encoded)
	if err != nil {
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	resource, err := callReplaySafe(
		ctx,
		c.retry,
		true,
		func() (callResult[generated.AgentDefinitionResource], error) {
			response, callErr := c.raw.CreateAgentDefinitionWithBodyWithResponse(
				ctx,
				&generated.CreateAgentDefinitionParams{IdempotencyKey: optionalString(input.IdempotencyKey)},
				"application/json",
				bytes.NewReader(body),
			)
			if callErr != nil {
				return callResult[generated.AgentDefinitionResource]{}, callErr
			}
			return callResult[generated.AgentDefinitionResource]{
				Value:  response.JSON201,
				Status: response.StatusCode(),
				Header: responseHeader(response.HTTPResponse),
				Body:   response.Body,
			}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return resource, nil
}

func (c *Client) GetAgentDefinition(ctx context.Context, id string) (*AgentDefinitionResource, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AgentDefinitionResource], error) {
		response, err := c.raw.GetAgentDefinitionWithResponse(ctx, id)
		if err != nil {
			return callResult[generated.AgentDefinitionResource]{}, err
		}
		return callResult[generated.AgentDefinitionResource]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetAgentDefinitionRevision(
	ctx context.Context,
	id string,
	revision int64,
) (*AgentDefinitionResource, error) {
	if revision < 1 {
		return nil, &Error{Category: ErrorValidation, Message: "Agent Definition revision must be positive"}
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AgentDefinitionResource], error) {
		response, err := c.raw.GetAgentDefinitionRevisionWithResponse(ctx, id, revision)
		if err != nil {
			return callResult[generated.AgentDefinitionResource]{}, err
		}
		return callResult[generated.AgentDefinitionResource]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ListAgentDefinitions(
	ctx context.Context,
	options ListAgentDefinitionsOptions,
) (*AgentDefinitionResourceList, error) {
	params := &generated.ListAgentDefinitionsParams{
		IncludeArchived: options.IncludeArchived,
		Cursor:          options.Cursor,
		Limit:           options.Limit,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AgentDefinitionResourceList], error) {
		response, err := c.raw.ListAgentDefinitionsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.AgentDefinitionResourceList]{}, err
		}
		return callResult[generated.AgentDefinitionResourceList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) UpdateAgentDefinition(
	ctx context.Context,
	id string,
	input UpdateAgentDefinitionInput,
) (*AgentDefinitionResource, error) {
	if input.ExpectedRevision < 1 {
		return nil, &Error{Category: ErrorValidation, Message: "expected Agent Definition revision must be positive"}
	}
	if input.Name == "" {
		return nil, &Error{Category: ErrorValidation, Message: "Agent Definition name is required"}
	}
	encoded, err := input.Definition.encoded()
	if err != nil {
		if sdkError, ok := err.(*Error); ok {
			return nil, sdkError
		}
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	encoded["name"] = input.Name
	body, err := json.Marshal(encoded)
	if err != nil {
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AgentDefinitionResource], error) {
		response, callErr := c.raw.UpdateAgentDefinitionWithBodyWithResponse(
			ctx,
			id,
			&generated.UpdateAgentDefinitionParams{IfMatch: fmt.Sprintf("\"%d\"", input.ExpectedRevision)},
			"application/json",
			bytes.NewReader(body),
		)
		if callErr != nil {
			return callResult[generated.AgentDefinitionResource]{}, callErr
		}
		return callResult[generated.AgentDefinitionResource]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ArchiveAgentDefinition(ctx context.Context, id string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.ArchiveAgentDefinitionWithResponse(ctx, id)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) RestoreAgentDefinition(ctx context.Context, id string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.RestoreAgentDefinitionWithResponse(ctx, id)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) GetInvocation(ctx context.Context, invocationID string) (*Invocation, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Invocation], error) {
		response, err := c.raw.GetInvocationWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.Invocation]{}, err
		}
		return callResult[generated.Invocation]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
}

func (c *Client) GetInvocationResult(ctx context.Context, invocationID string) (*InvocationResult, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationResult], error) {
		response, err := c.raw.GetInvocationResultWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.InvocationResult]{}, err
		}
		return callResult[generated.InvocationResult]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
}

func (c *Client) GetInvocationTimeline(ctx context.Context, invocationID string) (*InvocationTimeline, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationTimeline], error) {
		response, err := c.raw.GetInvocationTimelineWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.InvocationTimeline]{}, err
		}
		return callResult[generated.InvocationTimeline]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// ListInvocationTraces reads the hosted agent traces associated with one
// Invocation. The durable Invocation timeline remains the execution authority.
func (c *Client) ListInvocationTraces(
	ctx context.Context,
	invocationID string,
	options ObservationListOptions,
) (*TraceList, error) {
	params := &generated.ListInvocationTracesParams{
		Cursor: options.Cursor,
		Limit:  options.Limit,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.TraceList], error) {
		response, err := c.raw.ListInvocationTracesWithResponse(ctx, invocationID, params)
		if err != nil {
			return callResult[generated.TraceList]{}, err
		}
		return callResult[generated.TraceList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// ListInvocationLogs reads the bounded operational logs associated with one
// Invocation. Prompt, response, and tool payload content is not exposed here.
func (c *Client) ListInvocationLogs(
	ctx context.Context,
	invocationID string,
	options ObservationListOptions,
) (*InvocationLogList, error) {
	params := &generated.ListInvocationLogsParams{
		Cursor:  options.Cursor,
		Limit:   options.Limit,
		TraceID: options.TraceID,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationLogList], error) {
		response, err := c.raw.ListInvocationLogsWithResponse(ctx, invocationID, params)
		if err != nil {
			return callResult[generated.InvocationLogList]{}, err
		}
		return callResult[generated.InvocationLogList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// GetTrace reads one hosted trace after the service re-establishes access from
// its Invocation association. Possession of a trace ID alone grants no access.
func (c *Client) GetTrace(ctx context.Context, traceID string) (*Trace, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Trace], error) {
		response, err := c.raw.GetTraceWithResponse(ctx, traceID)
		if err != nil {
			return callResult[generated.Trace]{}, err
		}
		return callResult[generated.Trace]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ListAdmissions(
	ctx context.Context,
	params *ListAdmissionsParams,
) (*AdmissionAttemptList, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AdmissionAttemptList], error) {
		response, err := c.raw.ListAdmissionsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.AdmissionAttemptList]{}, err
		}
		return callResult[generated.AdmissionAttemptList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) SummarizeAdmissions(
	ctx context.Context,
	params *SummarizeAdmissionsParams,
) (*AdmissionSummary, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AdmissionSummary], error) {
		response, err := c.raw.SummarizeAdmissionsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.AdmissionSummary]{}, err
		}
		return callResult[generated.AdmissionSummary]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ListTenants(
	ctx context.Context,
	params *ListTenantsParams,
) (*TenantList, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.TenantList], error) {
		response, err := c.raw.ListTenantsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.TenantList]{}, err
		}
		return callResult[generated.TenantList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) DeleteTenant(ctx context.Context, tenantID string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, callErr := c.raw.DeleteTenantWithResponse(ctx, tenantID)
		if callErr != nil {
			return callResult[struct{}]{}, callErr
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) CancelInvocation(ctx context.Context, invocationID string) (*Invocation, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Invocation], error) {
		response, err := c.raw.CancelInvocationWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.Invocation]{}, err
		}
		return callResult[generated.Invocation]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
}

// InterruptInvocation stops an Invocation gracefully and keeps its work. The
// turn settles `completed` with stop reason `interrupted` once it reaches an
// execution seam, so the messages it already produced stay in the Session.
// CancelInvocation is the discarding stop.
func (c *Client) InterruptInvocation(ctx context.Context, invocationID string) (*Invocation, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Invocation], error) {
		response, err := c.raw.InterruptInvocationWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.Invocation]{}, err
		}
		return callResult[generated.Invocation]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
}

// ResumeInvocation raises the one exhausted turn-level ceiling on a paused
// Invocation. The service validates that exactly the exhausted limit changed
// and that its replacement is above both the previous limit and usage so far.
func (c *Client) ResumeInvocation(
	ctx context.Context,
	invocationID string,
	limits Limits,
) (*Invocation, error) {
	body, err := json.Marshal(struct {
		Limits Limits `json:"limits"`
	}{Limits: limits})
	if err != nil {
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	return callReplaySafe(ctx, c.retry, false, func() (callResult[generated.Invocation], error) {
		response, err := c.raw.ResumeInvocationWithBodyWithResponse(
			ctx,
			invocationID,
			"application/json",
			bytes.NewReader(body),
		)
		if err != nil {
			return callResult[generated.Invocation]{}, err
		}
		return callResult[generated.Invocation]{
			Value:  response.JSON202,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// CreateNudge appends steering to a running Invocation without ending it.
// The turn keeps everything it has already produced — the difference from
// supersession, which rewinds — and the model sees the input at its next
// execution seam rather than immediately.
//
// Input the turn never reaches is marked `expired` when the Invocation
// settles; nvoken never re-homes it onto a later turn, so re-sending missed
// direction as the next Invocation's input is the caller's decision to make.
func (c *Client) CreateNudge(
	ctx context.Context,
	invocationID string,
	request NudgeRequest,
) (*NudgeAcknowledgement, error) {
	if strings.TrimSpace(request.Content) == "" {
		return nil, &Error{Category: ErrorValidation, Message: "nudge content is required"}
	}
	body, err := json.Marshal(struct {
		Content        string `json:"content"`
		IdempotencyKey string `json:"idempotency_key,omitempty"`
	}{Content: request.Content, IdempotencyKey: request.IdempotencyKey})
	if err != nil {
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	// Replay-safe only with a key: without one a retried POST would stage the
	// same direction twice.
	replaySafe := request.IdempotencyKey != ""
	return callReplaySafe(ctx, c.retry, replaySafe, func() (callResult[generated.NudgeAcknowledgement], error) {
		response, callErr := c.raw.CreateNudgeWithBodyWithResponse(
			ctx,
			invocationID,
			"application/json",
			bytes.NewReader(body),
		)
		if callErr != nil {
			return callResult[generated.NudgeAcknowledgement]{}, callErr
		}
		return callResult[generated.NudgeAcknowledgement]{
			Value:  response.JSON202,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// ListNudges reads the staged queue in the order the turn will consume
// it, ended rows included. It is the reconciliation source for a surface
// that shows queued direction.
func (c *Client) ListNudges(
	ctx context.Context,
	invocationID string,
	options ListNudgesOptions,
) (*NudgeList, error) {
	params := &generated.ListNudgesParams{
		Status: options.Status,
		Cursor: options.Cursor,
		Limit:  options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.NudgeList], error) {
		response, callErr := c.raw.ListNudgesWithResponse(ctx, invocationID, params)
		if callErr != nil {
			return callResult[generated.NudgeList]{}, callErr
		}
		return callResult[generated.NudgeList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ListToolCalls reads durable execution records in discovery order. Inputs and
// results remain in the Session transcript. Callback records include retained
// delivery outcome metadata.
func (c *Client) ListToolCalls(
	ctx context.Context,
	invocationID string,
	options ListToolCallsOptions,
) (*ToolCallList, error) {
	params := &generated.ListToolCallsParams{
		Cursor: options.Cursor,
		Limit:  options.Limit,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ToolCallList], error) {
		response, err := c.raw.ListToolCallsWithResponse(ctx, invocationID, params)
		if err != nil {
			return callResult[generated.ToolCallList]{}, err
		}
		return callResult[generated.ToolCallList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// CancelNudge withdraws staged input the turn has not taken. Input the
// executor already drained is reported as a conflict rather than removed from
// a transcript it is already part of.
func (c *Client) CancelNudge(
	ctx context.Context,
	invocationID string,
	nudgeID string,
) (*Nudge, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Nudge], error) {
		response, err := c.raw.CancelNudgeWithResponse(ctx, invocationID, nudgeID)
		if err != nil {
			return callResult[generated.Nudge]{}, err
		}
		return callResult[generated.Nudge]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ListModels(
	ctx context.Context,
	options ListModelsOptions,
) (*ModelList, error) {
	params := &generated.ListModelsParams{
		Provider:          options.Provider,
		IncludeDeprecated: options.IncludeDeprecated,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ModelList], error) {
		response, err := c.raw.ListModelsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.ModelList]{}, err
		}
		return callResult[generated.ModelList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &ModelList{
		CatalogVersion: result.CatalogVersion,
		Items:          result.Items,
	}, nil
}

// ListMCPTools discovers the tools a remote MCP server projects. Headers are a
// separate argument because MCPServer is durable Agent Definition
// configuration and therefore carries no secrets; these are used for this one
// discovery request and never stored.
func (c *Client) ListMCPTools(
	ctx context.Context,
	server MCPServer,
	headers map[string]string,
) (*MCPListToolsResponse, error) {
	body := generated.MCPListToolsRequest{
		Server: generatedMCPServer(server),
	}
	if len(headers) > 0 {
		discovery := make(map[string]string, len(headers))
		for name, value := range headers {
			discovery[name] = value
		}
		body.Headers = &discovery
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.MCPListToolsResponse], error) {
		response, err := c.raw.ListMCPToolsWithResponse(ctx, body)
		if err != nil {
			return callResult[generated.MCPListToolsResponse]{}, err
		}
		return callResult[generated.MCPListToolsResponse]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetModel(ctx context.Context, model Model) (*ModelDescriptor, error) {
	if model.ID == "" {
		return nil, &Error{Category: ErrorValidation, Message: "model id is required"}
	}
	provider, err := generatedModelProvider(model.Provider)
	if err != nil {
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ModelDescriptor], error) {
		response, callErr := c.raw.GetModelWithResponse(
			ctx,
			provider,
			model.ID,
			&generated.GetModelParams{},
		)
		if callErr != nil {
			return callResult[generated.ModelDescriptor]{}, callErr
		}
		return callResult[generated.ModelDescriptor]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func generatedMCPServer(server MCPServer) generated.MCPServer {
	result := generated.MCPServer{
		Name: server.Name,
		URL:  server.URL,
	}
	if server.Transport != "" {
		transport := generated.MCPServerTransport(server.Transport)
		result.Transport = &transport
	}
	if server.AllowedTools != nil {
		allowedTools := append([]string(nil), server.AllowedTools...)
		result.AllowedTools = &allowedTools
	}
	if server.Timeouts != nil {
		result.Timeouts = &generated.MCPTimeouts{
			DiscoverySeconds: server.Timeouts.DiscoverySeconds,
			CallSeconds:      server.Timeouts.CallSeconds,
		}
	}
	return result
}

func (c *Client) SubmitToolResults(ctx context.Context, invocationID string, results []ToolResult) (*ToolResultResponse, error) {
	body, err := generatedToolResults(results)
	if err != nil {
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.SubmitHostToolResultsResponse], error) {
		response, callErr := c.raw.SubmitHostToolResultsWithResponse(ctx, invocationID, body)
		if callErr != nil {
			return callResult[generated.SubmitHostToolResultsResponse]{}, callErr
		}
		return callResult[generated.SubmitHostToolResultsResponse]{Value: response.JSON202, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) GetCurrentIdentity(ctx context.Context) (*CurrentIdentity, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.CurrentIdentity], error) {
		response, err := c.raw.GetCurrentIdentityWithResponse(ctx)
		if err != nil {
			return callResult[generated.CurrentIdentity]{}, err
		}
		return callResult[generated.CurrentIdentity]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ListCredentials(
	ctx context.Context,
	options ListCredentialsOptions,
) (*CredentialList, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.CredentialList], error) {
		response, err := c.raw.ListCredentialsWithResponse(ctx, &generated.ListCredentialsParams{
			Status: options.Status,
			Cursor: options.Cursor,
			Limit:  options.Limit,
		})
		if err != nil {
			return callResult[generated.CredentialList]{}, err
		}
		return callResult[generated.CredentialList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) CreateCredential(
	ctx context.Context,
	input CreateCredentialInput,
) (*CredentialIssuance, error) {
	if input.Name == "" || !input.Profile.Valid() || input.IdempotencyKey == "" {
		return nil, &Error{Category: ErrorValidation, Message: "credential name, profile, and idempotency key are required"}
	}
	body := generated.CreateCredentialRequest{
		AppID:     input.AppID,
		ExpiresAt: input.ExpiresAt,
		Name:      input.Name,
		Profile:   input.Profile,
		SessionID: input.SessionID,
		TenantKey: input.TenantKey,
	}
	if input.Operations != nil {
		operations := append([]RuntimeOperation(nil), input.Operations...)
		body.Operations = &operations
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.CredentialIssuance], error) {
		response, err := c.raw.CreateCredentialWithResponse(
			ctx,
			&generated.CreateCredentialParams{IdempotencyKey: input.IdempotencyKey},
			body,
		)
		if err != nil {
			return callResult[generated.CredentialIssuance]{}, err
		}
		return callResult[generated.CredentialIssuance]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return credentialIssuance(result)
}

func (c *Client) GetCredential(ctx context.Context, credentialID string) (*Credential, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Credential], error) {
		response, err := c.raw.GetCredentialWithResponse(ctx, credentialID)
		if err != nil {
			return callResult[generated.Credential]{}, err
		}
		return callResult[generated.Credential]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) RotateCredential(
	ctx context.Context,
	credentialID string,
	input RotateCredentialInput,
) (*CredentialIssuance, error) {
	if credentialID == "" || input.IdempotencyKey == "" || input.OverlapSeconds < 0 || input.OverlapSeconds > 86400 {
		return nil, &Error{Category: ErrorValidation, Message: "credential ID and idempotency key are required, and overlap seconds must be between 0 and 86400"}
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.CredentialIssuance], error) {
		response, err := c.raw.RotateCredentialWithResponse(
			ctx,
			credentialID,
			&generated.RotateCredentialParams{IdempotencyKey: input.IdempotencyKey},
			generated.RotateCredentialJSONRequestBody{OverlapSeconds: input.OverlapSeconds},
		)
		if err != nil {
			return callResult[generated.CredentialIssuance]{}, err
		}
		return callResult[generated.CredentialIssuance]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return credentialIssuance(result)
}

func (c *Client) RevokeCredential(ctx context.Context, credentialID string) (*Credential, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Credential], error) {
		response, err := c.raw.RevokeCredentialWithResponse(ctx, credentialID)
		if err != nil {
			return callResult[generated.Credential]{}, err
		}
		return callResult[generated.Credential]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func credentialIssuance(value *generated.CredentialIssuance) (*CredentialIssuance, error) {
	if value == nil || value.Secret == nil || *value.Secret == "" {
		return nil, &Error{Category: ErrorUnexpectedResponse, Message: "nvoken returned credential issuance without one-time secret material"}
	}
	return &CredentialIssuance{
		Credential:        value.Credential,
		Secret:            *value.Secret,
		DeliveryExpiresAt: value.DeliveryExpiresAt,
		Replayed:          value.Replayed,
	}, nil
}

func (c *Client) ListProviderKeys(
	ctx context.Context,
	options ListProviderKeysOptions,
) (*ProviderKeyList, error) {
	var status *generated.ListProviderKeysParamsStatus
	if options.Status != nil {
		value := generated.ListProviderKeysParamsStatus(*options.Status)
		status = &value
	}
	params := &generated.ListProviderKeysParams{
		Provider:  options.Provider,
		Scope:     options.Scope,
		Status:    status,
		TenantKey: options.TenantKey,
		Cursor:    options.Cursor,
		Limit:     options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderKeyList], error) {
		response, err := c.raw.ListProviderKeysWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.ProviderKeyList]{}, err
		}
		return callResult[generated.ProviderKeyList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &ProviderKeyList{
		HasMore:    result.HasMore,
		Items:      result.Items,
		NextCursor: result.NextCursor,
	}, nil
}

func (c *Client) CreateProviderKey(
	ctx context.Context,
	input CreateProviderKeyInput,
) (*ProviderKey, error) {
	if input.APIKey == "" || input.IdempotencyKey == "" {
		return nil, &Error{Category: ErrorValidation, Message: "provider API key and idempotency key are required"}
	}
	body := generated.CreateProviderKeyRequest{
		Key: generated.ProviderStaticKey{
			APIKey: &input.APIKey,
		},
		ExpiresAt:      input.ExpiresAt,
		IdempotencyKey: input.IdempotencyKey,
		Provider:       input.Provider,
		Scope:          input.Scope,
		TenantKey:      input.TenantKey,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderKey], error) {
		response, err := c.raw.CreateProviderKeyWithResponse(ctx, body)
		if err != nil {
			return callResult[generated.ProviderKey]{}, err
		}
		return callResult[generated.ProviderKey]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetProviderKey(ctx context.Context, id string) (*ProviderKey, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderKey], error) {
		response, err := c.raw.GetProviderKeyWithResponse(ctx, id)
		if err != nil {
			return callResult[generated.ProviderKey]{}, err
		}
		return callResult[generated.ProviderKey]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetProviderKeyUsage(ctx context.Context, id string) (*ProviderKeyUsage, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderKeyUsage], error) {
		response, err := c.raw.GetProviderKeyUsageWithResponse(ctx, id)
		if err != nil {
			return callResult[generated.ProviderKeyUsage]{}, err
		}
		return callResult[generated.ProviderKeyUsage]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetUsageTimeseries(ctx context.Context, params *GetUsageTimeseriesParams) (*UsageTimeseries, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.UsageTimeseries], error) {
		response, err := c.raw.GetUsageTimeseriesWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.UsageTimeseries]{}, err
		}
		return callResult[generated.UsageTimeseries]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetUsageBreakdown(ctx context.Context, params *GetUsageBreakdownParams) (*UsageBreakdown, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.UsageBreakdown], error) {
		response, err := c.raw.GetUsageBreakdownWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.UsageBreakdown]{}, err
		}
		return callResult[generated.UsageBreakdown]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// ListUsageRecords returns the JSON representation. Use Raw when requesting
// the CSV representation so the response body and continuation header remain
// available without lossy conversion.
func (c *Client) ListUsageRecords(ctx context.Context, params *ListUsageRecordsParams) (*UsageRecords, error) {
	if params != nil && params.Format != nil && string(*params.Format) == "csv" {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "ListUsageRecords returns JSON; use Raw to request CSV",
		}
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.UsageRecords], error) {
		response, err := c.raw.ListUsageRecordsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.UsageRecords]{}, err
		}
		return callResult[generated.UsageRecords]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) AllocateCredits(ctx context.Context, input AllocateCreditsInput) (*AllocateCreditsResult, error) {
	if input.IdempotencyKey == "" {
		return nil, &Error{Category: ErrorValidation, Message: "credit allocation idempotency key is required"}
	}
	body := generated.AllocateCreditsRequest{
		Amount:         input.Amount,
		DefaultTenant:  input.DefaultTenant,
		IdempotencyKey: input.IdempotencyKey,
		Reference:      input.Reference,
		TenantKey:      input.TenantKey,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AllocateCreditsResult], error) {
		response, err := c.raw.AllocateCreditsWithResponse(ctx, body)
		if err != nil {
			return callResult[generated.AllocateCreditsResult]{}, err
		}
		return callResult[generated.AllocateCreditsResult]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ListCreditAccounts(ctx context.Context, params *ListCreditAccountsParams) (*CreditAccountList, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.CreditAccountList], error) {
		response, err := c.raw.ListCreditAccountsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.CreditAccountList]{}, err
		}
		return callResult[generated.CreditAccountList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ListCreditAllocations(ctx context.Context, params *ListCreditAllocationsParams) (*CreditAllocationList, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.CreditAllocationList], error) {
		response, err := c.raw.ListCreditAllocationsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.CreditAllocationList]{}, err
		}
		return callResult[generated.CreditAllocationList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) RotateProviderKey(
	ctx context.Context,
	id string,
	input RotateProviderKeyInput,
) (*ProviderKey, error) {
	if input.APIKey == "" || input.IdempotencyKey == "" {
		return nil, &Error{Category: ErrorValidation, Message: "provider API key and idempotency key are required"}
	}
	body := generated.RotateProviderKeyRequest{
		Key: generated.ProviderStaticKey{
			APIKey: &input.APIKey,
		},
		ExpiresAt:      input.ExpiresAt,
		IdempotencyKey: input.IdempotencyKey,
		OverlapSeconds: input.OverlapSeconds,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderKey], error) {
		response, err := c.raw.RotateProviderKeyWithResponse(ctx, id, body)
		if err != nil {
			return callResult[generated.ProviderKey]{}, err
		}
		return callResult[generated.ProviderKey]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) RevokeProviderKey(ctx context.Context, id string) (*ProviderKey, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderKey], error) {
		response, err := c.raw.RevokeProviderKeyWithResponse(ctx, id)
		if err != nil {
			return callResult[generated.ProviderKey]{}, err
		}
		return callResult[generated.ProviderKey]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// RegisterApp registers one host application and returns its generated app_id.
// It requires an Org-scoped or installation Operator credential.
func (c *Client) RegisterApp(ctx context.Context, name string, options RegisterAppOptions) (*AppRegistration, error) {
	return callReplaySafe(ctx, c.retry, false, func() (callResult[generated.AppRegistration], error) {
		response, err := c.raw.RegisterAppWithResponse(ctx, generated.RegisterAppJSONRequestBody{
			Name:                   name,
			ExternalRef:            options.ExternalRef,
			DisplayName:            options.DisplayName,
			OrgID:                  options.OrgID,
			CallbackTimeoutSeconds: options.CallbackTimeoutSeconds,
		})
		if err != nil {
			return callResult[generated.AppRegistration]{}, err
		}
		return callResult[generated.AppRegistration]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetApp(ctx context.Context, appID string) (*App, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.App], error) {
		response, err := c.raw.GetAppWithResponse(ctx, appID)
		if err != nil {
			return callResult[generated.App]{}, err
		}
		return callResult[generated.App]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// ListMemories browses or searches durable memories for one Agent and scope.
func (c *Client) ListMemories(ctx context.Context, options ListMemoriesOptions) (*MemoryList, error) {
	params := &generated.ListMemoriesParams{
		AgentID:    options.AgentID,
		TenantKey:  options.TenantKey,
		UserKey:    options.UserKey,
		Query:      options.Query,
		SearchMode: options.SearchMode,
		Kind:       options.Kind,
		Cursor:     options.Cursor,
		Limit:      options.Limit,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.MemoryList], error) {
		response, err := c.raw.ListMemoriesWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.MemoryList]{}, err
		}
		return callResult[generated.MemoryList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// GetMemory reads one durable memory by its opaque ID.
func (c *Client) GetMemory(ctx context.Context, memoryID string) (*Memory, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Memory], error) {
		response, err := c.raw.GetMemoryWithResponse(ctx, memoryID)
		if err != nil {
			return callResult[generated.Memory]{}, err
		}
		return callResult[generated.Memory]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// DeleteMemory erases one durable memory and its derived search projection.
func (c *Client) DeleteMemory(ctx context.Context, memoryID string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.DeleteMemoryWithResponse(ctx, memoryID)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) ListApps(ctx context.Context, options ListAppsOptions) (*AppList, error) {
	var status *generated.ListAppsParamsStatus
	if options.Status != nil {
		value := generated.ListAppsParamsStatus(*options.Status)
		status = &value
	}
	params := &generated.ListAppsParams{
		ExternalRef: options.ExternalRef,
		Status:      status,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AppList], error) {
		response, err := c.raw.ListAppsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.AppList]{}, err
		}
		return callResult[generated.AppList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ArchiveApp(ctx context.Context, appID string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.ArchiveAppWithResponse(ctx, appID)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) RestoreApp(ctx context.Context, appID string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.RestoreAppWithResponse(ctx, appID)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) ListAppClientKeys(ctx context.Context, appID string) (*ClientKeyList, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ClientKeyList], error) {
		response, err := c.raw.ListAppClientKeysWithResponse(ctx, appID)
		if err != nil {
			return callResult[generated.ClientKeyList]{}, err
		}
		return callResult[generated.ClientKeyList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) CreateAppClientKey(ctx context.Context, appID string, input CreateAppClientKeyInput) (*ClientKey, error) {
	if input.Name == "" || len(input.PublicKey) == 0 {
		return nil, &Error{Category: ErrorValidation, Message: "client key name and public key are required"}
	}
	return callReplaySafe(ctx, c.retry, false, func() (callResult[generated.ClientKey], error) {
		response, err := c.raw.CreateAppClientKeyWithResponse(ctx, appID, generated.CreateClientKeyRequest{
			Name:      input.Name,
			PublicKey: input.PublicKey,
		})
		if err != nil {
			return callResult[generated.ClientKey]{}, err
		}
		return callResult[generated.ClientKey]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) RevokeAppClientKey(ctx context.Context, appID, keyID string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.RevokeAppClientKeyWithResponse(ctx, appID, keyID)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

// ListAppSigningKeys returns every receiver-facing signing key version an App
// holds and marks the one that signs. Key material is never returned.
func (c *Client) ListAppSigningKeys(ctx context.Context, appID string) (*AppSigningKeyList, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AppSigningKeyList], error) {
		response, err := c.raw.ListAppSigningKeysWithResponse(ctx, appID)
		if err != nil {
			return callResult[generated.AppSigningKeyList]{}, err
		}
		return callResult[generated.AppSigningKeyList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// MintAppSigningKey writes the next version for one purpose and returns its
// plaintext exactly once. There is no way to read it again.
//
// nvoken keeps signing with the current version. Add the returned secret to
// your verifier beside the one already there, then ActivateAppSigningKey, then
// RetireAppSigningKey. In that order no delivery ever fails verification, which
// matters because a receiver's 401 is not retried: it settles the ToolCall as a
// delivery failure.
//
// Set input.Activate only when there is no working verifier left to protect —
// recovering a lost secret, where the three steps collapse into this one.
func (c *Client) MintAppSigningKey(
	ctx context.Context,
	appID string,
	input MintAppSigningKeyInput,
) (*AppSigningKeySecret, error) {
	if !input.Purpose.Valid() {
		return nil, &Error{Category: ErrorValidation, Message: "signing key purpose must be callback or webhook"}
	}
	body := generated.MintAppSigningKeyRequest{Purpose: input.Purpose}
	if input.Activate {
		body.Activate = &input.Activate
	}
	// Minting is not replay-safe: a retried mint that the service already
	// applied would write a second version and return a secret for a version
	// the caller never saw the first response for.
	return callReplaySafe(ctx, c.retry, false, func() (callResult[generated.AppSigningKeySecret], error) {
		response, err := c.raw.MintAppSigningKeyWithResponse(ctx, appID, body)
		if err != nil {
			return callResult[generated.AppSigningKeySecret]{}, err
		}
		return callResult[generated.AppSigningKeySecret]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// ActivateAppSigningKey moves signing to an existing version. The transport
// resolves the key per send, so it takes effect on the next delivery. Do this
// only once your receiver verifies against that version's secret.
func (c *Client) ActivateAppSigningKey(
	ctx context.Context,
	appID string,
	purpose AppSigningKeyPurpose,
	version int64,
) (*AppSigningKey, error) {
	if !purpose.Valid() {
		return nil, &Error{Category: ErrorValidation, Message: "signing key purpose must be callback or webhook"}
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AppSigningKey], error) {
		response, err := c.raw.ActivateAppSigningKeyWithResponse(ctx, appID, purpose, int(version))
		if err != nil {
			return callResult[generated.AppSigningKey]{}, err
		}
		return callResult[generated.AppSigningKey]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// RetireAppSigningKey deletes a superseded version once signing has moved off
// it and your receiver has dropped it. Retiring the version that is signing is
// refused rather than silently silencing every delivery the App makes.
func (c *Client) RetireAppSigningKey(
	ctx context.Context,
	appID string,
	purpose AppSigningKeyPurpose,
	version int64,
) error {
	if !purpose.Valid() {
		return &Error{Category: ErrorValidation, Message: "signing key purpose must be callback or webhook"}
	}
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.RetireAppSigningKeyWithResponse(ctx, appID, purpose, int(version))
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

// UpdateApp changes an app's mutable presentation fields; name and
// external_ref cannot be changed.
func (c *Client) UpdateApp(ctx context.Context, appID string, options UpdateAppOptions) (*App, error) {
	if options.DisplayName == nil && options.OrgID == nil &&
		options.CallbackTimeoutSeconds == nil {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "app update requires display name, Org ID, or callback timeout",
		}
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.App], error) {
		response, err := c.raw.UpdateAppWithResponse(ctx, appID, generated.UpdateAppJSONRequestBody{
			DisplayName:            options.DisplayName,
			OrgID:                  options.OrgID,
			CallbackTimeoutSeconds: options.CallbackTimeoutSeconds,
		})
		if err != nil {
			return callResult[generated.App]{}, err
		}
		return callResult[generated.App]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ListOrgs(ctx context.Context, options ListOrgsOptions) (*OrgList, error) {
	var status *generated.ListOrgsParamsStatus
	if options.Status != nil {
		value := generated.ListOrgsParamsStatus(*options.Status)
		status = &value
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.OrgList], error) {
		response, err := c.raw.ListOrgsWithResponse(ctx, &generated.ListOrgsParams{Status: status})
		if err != nil {
			return callResult[generated.OrgList]{}, err
		}
		return callResult[generated.OrgList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ArchiveOrg(ctx context.Context, orgID string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.ArchiveOrgWithResponse(ctx, orgID)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) RestoreOrg(ctx context.Context, orgID string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.RestoreOrgWithResponse(ctx, orgID)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) RegisterOrg(ctx context.Context, displayName string, options RegisterOrgOptions) (*Org, error) {
	return callReplaySafe(ctx, c.retry, options.ExternalRef != nil, func() (callResult[generated.Org], error) {
		response, err := c.raw.RegisterOrgWithResponse(ctx, generated.RegisterOrgJSONRequestBody{
			DisplayName: displayName,
			ExternalRef: options.ExternalRef,
		})
		if err != nil {
			return callResult[generated.Org]{}, err
		}
		value := response.JSON201
		if value == nil {
			value = response.JSON200
		}
		return callResult[generated.Org]{
			Value:  value,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetOrg(ctx context.Context, orgID string) (*Org, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Org], error) {
		response, err := c.raw.GetOrgWithResponse(ctx, orgID)
		if err != nil {
			return callResult[generated.Org]{}, err
		}
		return callResult[generated.Org]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) UpdateOrg(ctx context.Context, orgID, displayName string) (*Org, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Org], error) {
		response, err := c.raw.UpdateOrgWithResponse(ctx, orgID, generated.UpdateOrgJSONRequestBody{
			DisplayName: displayName,
		})
		if err != nil {
			return callResult[generated.Org]{}, err
		}
		return callResult[generated.Org]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetAgent(ctx context.Context, agentID string) (*AgentIdentity, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Agent], error) {
		response, err := c.raw.GetAgentWithResponse(ctx, agentID)
		if err != nil {
			return callResult[generated.Agent]{}, err
		}
		return callResult[generated.Agent]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// CreateAgent deliberately creates or resolves one tenant-scoped Agent
// instance. Repeating the same tenant/key/Definition tuple is a safe upsert.
func (c *Client) CreateAgent(ctx context.Context, input CreateAgentInput) (*AgentIdentity, error) {
	if input.AgentKey == "" || input.AgentDefinitionID == "" {
		return nil, &Error{Category: ErrorValidation, Message: "Agent key and Agent Definition ID are required"}
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Agent], error) {
		response, err := c.raw.CreateAgentWithResponse(ctx, generated.CreateAgentJSONRequestBody{
			TenantKey:         input.TenantKey,
			AgentKey:          input.AgentKey,
			Name:              optionalString(input.Name),
			AgentDefinitionID: input.AgentDefinitionID,
			PinnedRevision:    input.PinnedRevision,
		})
		if err != nil {
			return callResult[generated.Agent]{}, err
		}
		value := response.JSON201
		if value == nil {
			value = response.JSON200
		}
		return callResult[generated.Agent]{
			Value:  value,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) UpdateAgent(ctx context.Context, agentID string, input UpdateAgentInput) (*AgentIdentity, error) {
	if input.Name == nil && input.PinnedRevision == nil && !input.ClearPinnedRevision {
		return nil, &Error{Category: ErrorValidation, Message: "Agent update requires a name or revision pin"}
	}
	if input.PinnedRevision != nil && input.ClearPinnedRevision {
		return nil, &Error{Category: ErrorValidation, Message: "Agent revision pin cannot be set and cleared together"}
	}
	body := make(map[string]any)
	if input.Name != nil {
		body["name"] = *input.Name
	}
	if input.PinnedRevision != nil {
		body["pinned_revision"] = *input.PinnedRevision
	} else if input.ClearPinnedRevision {
		body["pinned_revision"] = nil
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Agent], error) {
		response, err := c.raw.UpdateAgentWithBodyWithResponse(
			ctx,
			agentID,
			"application/json",
			bytes.NewReader(encoded),
		)
		if err != nil {
			return callResult[generated.Agent]{}, err
		}
		return callResult[generated.Agent]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ArchiveAgent(ctx context.Context, agentID string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.ArchiveAgentWithResponse(ctx, agentID)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) RestoreAgent(ctx context.Context, agentID string) error {
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.RestoreAgentWithResponse(ctx, agentID)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

func (c *Client) ListAgents(ctx context.Context, options ListAgentsOptions) (*AgentList, error) {
	params := &generated.ListAgentsParams{
		TenantKey:         options.TenantKey,
		AgentKey:          options.AgentKey,
		AgentDefinitionID: options.AgentDefinitionID,
		IncludeArchived:   options.IncludeArchived,
		Cursor:            options.Cursor,
		Limit:             options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AgentList], error) {
		response, err := c.raw.ListAgentsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.AgentList]{}, err
		}
		return callResult[generated.AgentList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &AgentList{
		HasMore:    result.HasMore,
		Items:      result.Items,
		NextCursor: result.NextCursor,
	}, nil
}

func (c *Client) ListInvocations(ctx context.Context, options ListInvocationsOptions) (*InvocationList, error) {
	statuses := append([]InvocationStatus(nil), options.Statuses...)
	if options.Status != nil {
		statuses = append(statuses, *options.Status)
	}
	var statusFilter *[]InvocationStatus
	if len(statuses) != 0 {
		statusFilter = &statuses
	}
	params := &generated.ListInvocationsParams{
		TenantKey:     options.TenantKey,
		DefaultTenant: options.DefaultTenant,
		UserKey:       options.UserKey,
		SessionID:     options.SessionID,
		AgentID:       options.AgentID,
		AgentKey:      options.AgentKey,
		Status:        statusFilter,
		Cursor:        options.Cursor,
		Limit:         options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationList], error) {
		response, err := c.raw.ListInvocationsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.InvocationList]{}, err
		}
		return callResult[generated.InvocationList]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
	if err != nil {
		return nil, err
	}
	return &InvocationList{
		HasMore:    result.HasMore,
		Items:      result.Items,
		NextCursor: result.NextCursor,
	}, nil
}

func (c *Client) ListSessions(ctx context.Context, options ListSessionsOptions) (*SessionList, error) {
	params := &generated.ListSessionsParams{
		TenantKey:     options.TenantKey,
		DefaultTenant: options.DefaultTenant,
		UserKey:       options.UserKey,
		AgentID:       options.AgentID,
		AgentKey:      options.AgentKey,
		SessionKey:    options.SessionKey,
		Cursor:        options.Cursor,
		Limit:         options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.SessionList], error) {
		response, err := c.raw.ListSessionsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.SessionList]{}, err
		}
		return callResult[generated.SessionList]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
	if err != nil {
		return nil, err
	}
	return &SessionList{
		HasMore:    result.HasMore,
		Items:      result.Items,
		NextCursor: result.NextCursor,
	}, nil
}

// CreateSession creates a Session with zero Invocations and optional
// host-asserted starting history. Without a SessionKey every call creates a
// fresh Session, so the call is only retried when a keyed upsert makes replay
// safe.
func (c *Client) CreateSession(ctx context.Context, options CreateSessionOptions) (*Session, error) {
	body := generated.CreateSessionJSONRequestBody{
		AgentID:    options.AgentID,
		AgentKey:   options.AgentKey,
		TenantKey:  options.TenantKey,
		UserKey:    options.UserKey,
		SessionKey: options.SessionKey,
	}
	if options.SessionOptions != nil {
		sessionOptions, err := options.SessionOptions.generated()
		if err != nil {
			return nil, err
		}
		body.SessionOptions = sessionOptions
	}
	if len(options.SeedMessages) > 0 {
		seedMessages := make([]generated.SeedMessage, len(options.SeedMessages))
		for index, seed := range options.SeedMessages {
			content := generated.SeedMessageContent{}
			if err := content.FromSeedMessageContent0(seed.Content); err != nil {
				return nil, err
			}
			seedMessages[index] = generated.SeedMessage{
				Role:    seed.Role,
				Content: content,
			}
		}
		body.SeedMessages = &seedMessages
	}
	return callReplaySafe(ctx, c.retry, options.SessionKey != nil, func() (callResult[generated.Session], error) {
		response, err := c.raw.CreateSessionWithResponse(ctx, body)
		if err != nil {
			return callResult[generated.Session]{}, err
		}
		return callResult[generated.Session]{Value: response.JSON201, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
}

// ForkSession copies source history through an inclusive message into a new
// Session. The source is unchanged, and the child starts with no usage or
// compaction summary. Without a SessionKey every call creates a fresh child.
func (c *Client) ForkSession(
	ctx context.Context,
	sourceSessionID string,
	options ForkSessionOptions,
) (*Session, error) {
	if (options.FromMessageID == nil) == (options.FromSequence == nil) {
		return nil, fmt.Errorf("fork session requires exactly one message ID or sequence")
	}
	fromMessage := generated.ForkSessionRequest_FromMessage{}
	if options.FromMessageID != nil {
		if err := fromMessage.FromSessionMessageID(*options.FromMessageID); err != nil {
			return nil, err
		}
	} else if err := fromMessage.FromForkSessionRequestFromMessage1(*options.FromSequence); err != nil {
		return nil, err
	}
	body := generated.ForkSessionJSONRequestBody{
		FromMessage: fromMessage,
		SessionKey:  options.SessionKey,
		UserKey:     options.UserKey,
	}
	if options.SessionOptions != nil {
		sessionOptions, err := options.SessionOptions.generatedFork()
		if err != nil {
			return nil, err
		}
		body.SessionOptions = sessionOptions
	}
	return callReplaySafe(ctx, c.retry, options.SessionKey != nil, func() (callResult[generated.Session], error) {
		response, err := c.raw.ForkSessionWithResponse(ctx, sourceSessionID, body)
		if err != nil {
			return callResult[generated.Session]{}, err
		}
		return callResult[generated.Session]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

// DeleteSession erases a Session and everything under it: its Invocations,
// transcript, checkpoints, tool calls, artifacts, and undelivered
// webhooks. The erasure is immediate and irreversible.
//
// A Session holding a nonterminal Invocation is refused unless options set
// Force. Erasure skips settlement — the Invocation is removed rather than
// ended, so it records no terminal status and emits no invocation.ended
// webhook — which is why a caller that bills or reconciles on settlement must
// cancel first. Force is for erasing on an end user's behalf, where removing
// the transcript now outranks keeping a settled record.
//
// An unknown or out-of-scope Session is not found, so a retry after a lost
// response can treat that as already-done.
//
// This is not account erasure by itself: nvoken keeps no account tombstone, so
// a caller honouring a deletion request must stop admitting work for the
// tenant before paginating and deleting.
func (c *Client) DeleteSession(
	ctx context.Context,
	sessionID string,
	options ...DeleteSessionOption,
) error {
	settings := deleteSessionSettings{}
	for _, option := range options {
		option(&settings)
	}
	params := &generated.DeleteSessionParams{}
	if settings.force {
		force := true
		params.Force = &force
	}
	// Deletion is idempotent by shape — a repeat is not-found rather than a
	// second erasure — so it is safe to replay.
	_, err := callReplaySafe(ctx, c.retry, true, func() (callResult[struct{}], error) {
		response, err := c.raw.DeleteSessionWithResponse(ctx, sessionID, params)
		if err != nil {
			return callResult[struct{}]{}, err
		}
		result := callResult[struct{}]{
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}
		if result.Status == http.StatusNoContent {
			result.Value = &struct{}{}
		}
		return result, nil
	})
	return err
}

type deleteSessionSettings struct{ force bool }

// DeleteSessionOption configures one erasure.
type DeleteSessionOption func(*deleteSessionSettings)

// WithForceDelete erases a Session even when it holds a nonterminal
// Invocation, discarding that turn's settlement.
func WithForceDelete() DeleteSessionOption {
	return func(settings *deleteSessionSettings) { settings.force = true }
}

// UpdateSession merges a metadata patch into a Session: a present key
// replaces, an explicit null deletes, and an unmentioned key survives. Merging
// rather than replacing is what stops independent writers — a title UI and
// correlation tooling — from silently discarding each other's keys.
func (c *Client) UpdateSession(
	ctx context.Context,
	sessionID string,
	metadata map[string]*string,
) (*Session, error) {
	body := generated.UpdateSessionJSONRequestBody{Metadata: &metadata}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Session], error) {
		response, err := c.raw.UpdateSessionWithResponse(ctx, sessionID, body)
		if err != nil {
			return callResult[generated.Session]{}, err
		}
		return callResult[generated.Session]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Session], error) {
		response, err := c.raw.GetSessionWithResponse(ctx, sessionID)
		if err != nil {
			return callResult[generated.Session]{}, err
		}
		return callResult[generated.Session]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
}

func (c *Client) ListSessionMessages(ctx context.Context, sessionID string, options MessageListOptions) (*SessionMessageList, error) {
	params := &generated.ListSessionMessagesParams{Cursor: options.Cursor, Limit: options.Limit, Order: options.Order}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.SessionMessageList], error) {
		response, err := c.raw.ListSessionMessagesWithResponse(ctx, sessionID, params)
		if err != nil {
			return callResult[generated.SessionMessageList]{}, err
		}
		return callResult[generated.SessionMessageList]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, err
	})
	if err != nil {
		return nil, err
	}
	return &SessionMessageList{
		HasMore:    result.HasMore,
		Items:      result.Items,
		NextCursor: result.NextCursor,
	}, nil
}

// ListSessionCompactions returns newest-first immutable records for applied
// and fell-through summary passes. Only applied records affect model context.
func (c *Client) ListSessionCompactions(
	ctx context.Context,
	sessionID string,
	options CompactionListOptions,
) (*SessionCompactionList, error) {
	params := &generated.ListSessionCompactionsParams{
		Cursor: options.Cursor,
		Limit:  options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.SessionCompactionList], error) {
		response, err := c.raw.ListSessionCompactionsWithResponse(ctx, sessionID, params)
		if err != nil {
			return callResult[generated.SessionCompactionList]{}, err
		}
		return callResult[generated.SessionCompactionList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &SessionCompactionList{
		HasMore:    result.HasMore,
		Items:      result.Items,
		NextCursor: result.NextCursor,
	}, nil
}

func (c *Client) GetTranscript(ctx context.Context, sessionID string, options TranscriptOptions) (*TranscriptSnapshot, error) {
	params := &generated.GetSessionTranscriptParams{
		Cursor:    options.Cursor,
		PageToken: options.PageToken,
		Limit:     options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.TranscriptSnapshot], error) {
		response, err := c.raw.GetSessionTranscriptWithResponse(ctx, sessionID, params)
		if err != nil {
			return callResult[generated.TranscriptSnapshot]{}, err
		}
		return callResult[generated.TranscriptSnapshot]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &TranscriptSnapshot{
		HasMore:           result.HasMore,
		InvocationChanges: result.InvocationChanges,
		Messages:          result.Messages,
		NextPageToken:     result.NextPageToken,
		Cursor:            result.Cursor,
	}, nil
}

func (c *Client) DrainTranscript(
	ctx context.Context,
	sessionID string,
	cursor *string,
	limit *int,
) (*TranscriptDrain, error) {
	drain := &TranscriptDrain{}
	var pageToken *string
	for {
		page, err := c.GetTranscript(ctx, sessionID, TranscriptOptions{
			Cursor:    cursor,
			PageToken: pageToken,
			Limit:     limit,
		})
		if err != nil {
			return nil, err
		}
		drain.Messages = append(drain.Messages, page.Messages...)
		drain.InvocationChanges = append(
			drain.InvocationChanges,
			page.InvocationChanges...,
		)
		drain.Cursor = page.Cursor
		if !page.HasMore {
			if drain.Cursor == "" {
				return nil, &Error{
					Category: ErrorUnexpectedResponse,
					Message:  "transcript drain returned no resume cursor",
				}
			}
			return drain, nil
		}
		if page.NextPageToken == nil || *page.NextPageToken == "" {
			return nil, &Error{
				Category: ErrorUnexpectedResponse,
				Message:  "transcript page has_more without next_page_token",
			}
		}
		cursor = nil
		pageToken = page.NextPageToken
	}
}

func (c *Client) GetSessionByKey(
	ctx context.Context,
	sessionKey string,
	options ListSessionsOptions,
) (*Session, error) {
	options.SessionKey = &sessionKey
	limit := 2
	options.Limit = &limit
	page, err := c.ListSessions(ctx, options)
	if err != nil {
		return nil, err
	}
	switch len(page.Items) {
	case 0:
		return nil, &Error{
			Category: ErrorNotFound,
			Message:  fmt.Sprintf("Session key %q was not found", sessionKey),
		}
	case 1:
		return &page.Items[0], nil
	default:
		return nil, &Error{
			Category: ErrorConflict,
			Message:  fmt.Sprintf("Session key %q matched more than one Session", sessionKey),
		}
	}
}

func (c *Client) EachInvocation(
	ctx context.Context,
	options ListInvocationsOptions,
	consume func(Invocation) error,
) error {
	options.Cursor = nil
	for {
		page, err := c.ListInvocations(ctx, options)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			if err := consume(item); err != nil {
				return err
			}
		}
		if !page.HasMore {
			return nil
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return &Error{
				Category: ErrorUnexpectedResponse,
				Message:  "Invocation page has_more without next_cursor",
			}
		}
		options.Cursor = page.NextCursor
	}
}

func (c *Client) EachSession(
	ctx context.Context,
	options ListSessionsOptions,
	consume func(Session) error,
) error {
	options.Cursor = nil
	for {
		page, err := c.ListSessions(ctx, options)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			if err := consume(item); err != nil {
				return err
			}
		}
		if !page.HasMore {
			return nil
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return &Error{
				Category: ErrorUnexpectedResponse,
				Message:  "Session page has_more without next_cursor",
			}
		}
		options.Cursor = page.NextCursor
	}
}

func (c *Client) EachSessionMessage(
	ctx context.Context,
	sessionID string,
	options MessageListOptions,
	consume func(SessionMessage) error,
) error {
	options.Cursor = nil
	for {
		page, err := c.ListSessionMessages(ctx, sessionID, options)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			if err := consume(item); err != nil {
				return err
			}
		}
		if !page.HasMore {
			return nil
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return &Error{
				Category: ErrorUnexpectedResponse,
				Message:  "message page has_more without next_cursor",
			}
		}
		options.Cursor = page.NextCursor
	}
}

func (c *Client) EachProviderKey(
	ctx context.Context,
	options ListProviderKeysOptions,
	consume func(ProviderKey) error,
) error {
	options.Cursor = nil
	for {
		page, err := c.ListProviderKeys(ctx, options)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			if err := consume(item); err != nil {
				return err
			}
		}
		if !page.HasMore {
			return nil
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return &Error{
				Category: ErrorUnexpectedResponse,
				Message:  "provider key page has_more without next_cursor",
			}
		}
		options.Cursor = page.NextCursor
	}
}

type InvocationHandle struct {
	client         *Client
	InvocationID   string           `json:"invocation_id"`
	IdempotencyKey string           `json:"idempotency_key,omitempty"`
	SessionID      string           `json:"session_id,omitempty"`
	AgentID        string           `json:"agent_id,omitempty"`
	Status         InvocationStatus `json:"status,omitempty"`
	Deduplicated   *bool            `json:"deduplicated,omitempty"`
	DeadlineAt     *time.Time       `json:"deadline_at,omitempty"`
}

func (h *InvocationHandle) Refresh(ctx context.Context) (*Invocation, error) {
	invocation, err := h.client.GetInvocation(ctx, h.InvocationID)
	if err == nil {
		h.SessionID = invocation.SessionID
		h.AgentID = agentIDOrEmpty(invocation.AgentID)
		h.Status = invocation.Status
		h.DeadlineAt = invocation.DeadlineAt
	}
	return invocation, err
}

func (h *InvocationHandle) Wait(ctx context.Context, options WaitOptions) (*Invocation, error) {
	options = options.normalized()
	if options.Until != WaitUntilTerminal && options.Until != WaitUntilActionable {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  fmt.Sprintf("unsupported wait condition %q", options.Until),
		}
	}
	if options.Timeout < 0 {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "wait timeout cannot be negative",
		}
	}
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	delay := options.MinPollInterval
	for {
		invocation, err := h.Refresh(ctx)
		if err != nil {
			return nil, err
		}
		if waitSatisfied(invocation.Status, options.Until) {
			return invocation, nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, transportError(ctx.Err())
		case <-timer.C:
		}
		delay *= 2
		if delay > options.MaxPollInterval {
			delay = options.MaxPollInterval
		}
	}
}

// Result reads the composed InvocationResult at any status: the
// authoritative Invocation, this Invocation's canonical messages, and the
// output_text projection.
func (h *InvocationHandle) Result(ctx context.Context) (*InvocationResult, error) {
	result, err := h.client.GetInvocationResult(ctx, h.InvocationID)
	if err == nil {
		h.SessionID = result.Invocation.SessionID
		h.AgentID = agentIDOrEmpty(result.Invocation.AgentID)
		h.Status = result.Invocation.Status
	}
	return result, err
}

// ListMessages returns this Invocation's canonical messages from the
// composed result read.
func (h *InvocationHandle) ListMessages(ctx context.Context) ([]SessionMessage, error) {
	result, err := h.Result(ctx)
	if err != nil {
		return nil, err
	}
	return result.Messages, nil
}

// Text returns the completed turn's canonical assistant text. It fails
// with ErrorUnexpectedResponse when the wire output_text is null or the
// empty string: the wire keeps those distinct, but this helper
// deliberately treats both as "no useful answer". Read Result directly
// to observe the distinction.
func (h *InvocationHandle) OutputText(ctx context.Context) (string, error) {
	result, err := h.Result(ctx)
	if err != nil {
		return "", err
	}
	if result.OutputText == nil || *result.OutputText == "" {
		return "", &Error{
			Category: ErrorUnexpectedResponse,
			Message:  "Invocation " + h.InvocationID + " has no canonical assistant text",
		}
	}
	return *result.OutputText, nil
}

func (h *InvocationHandle) SubmitToolResults(ctx context.Context, results []ToolResult) (*ToolResultResponse, error) {
	response, err := h.client.SubmitToolResults(ctx, h.InvocationID, results)
	if err == nil {
		h.Status = response.Status
	}
	return response, err
}

func (h *InvocationHandle) Cancel(ctx context.Context) (*Invocation, error) {
	invocation, err := h.client.CancelInvocation(ctx, h.InvocationID)
	if err == nil {
		h.SessionID = invocation.SessionID
		h.AgentID = agentIDOrEmpty(invocation.AgentID)
		h.Status = invocation.Status
	}
	return invocation, err
}

func (h *InvocationHandle) Interrupt(ctx context.Context) (*Invocation, error) {
	invocation, err := h.client.InterruptInvocation(ctx, h.InvocationID)
	if err == nil {
		h.SessionID = invocation.SessionID
		h.AgentID = agentIDOrEmpty(invocation.AgentID)
		h.Status = invocation.Status
	}
	return invocation, err
}

// Nudge appends steering to this running turn. It is not an interrupt: the
// model sees the input at the next execution seam, and nothing in flight is
// aborted for it.
func (h *InvocationHandle) Nudge(ctx context.Context, content string) (*NudgeAcknowledgement, error) {
	return h.client.CreateNudge(ctx, h.InvocationID, NudgeRequest{Content: content})
}

// NudgeWith is Nudge with an idempotency key, so a retried call stages the
// direction once.
func (h *InvocationHandle) NudgeWith(ctx context.Context, request NudgeRequest) (*NudgeAcknowledgement, error) {
	return h.client.CreateNudge(ctx, h.InvocationID, request)
}

func (h *InvocationHandle) ListNudges(ctx context.Context, options ListNudgesOptions) (*NudgeList, error) {
	return h.client.ListNudges(ctx, h.InvocationID, options)
}

func (h *InvocationHandle) ListToolCalls(ctx context.Context, options ListToolCallsOptions) (*ToolCallList, error) {
	return h.client.ListToolCalls(ctx, h.InvocationID, options)
}

func (h *InvocationHandle) CancelNudge(ctx context.Context, nudgeID string) (*Nudge, error) {
	return h.client.CancelNudge(ctx, h.InvocationID, nudgeID)
}

func (h *InvocationHandle) WaitForAction(ctx context.Context, options WaitOptions) (*Invocation, error) {
	options.Until = WaitUntilActionable
	return h.Wait(ctx, options)
}

func (h *InvocationHandle) WaitForResult(ctx context.Context, options WaitOptions) (*InvocationResult, error) {
	invocation, err := h.Wait(ctx, options)
	if err != nil {
		return nil, err
	}
	if invocation.Status != InvocationCompleted {
		return nil, &Error{
			Category: ErrorConflict,
			Message:  invocationEndedMessage(h.InvocationID, invocation),
		}
	}
	return h.Result(ctx)
}

func generatedIdempotencyKey() string {
	var value [16]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return fmt.Sprintf("nvoken-%d", time.Now().UnixNano())
	}
	return "nvoken-" + hex.EncodeToString(value[:])
}

func agentIDOrEmpty(value generated.AgentID) string {
	return string(value)
}
