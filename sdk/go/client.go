package nvoken

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
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
	raw   *generated.ClientWithResponses
	retry RetryPolicy
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
	raw, err := generated.NewClientWithResponses(
		baseURL,
		generated.WithHTTPClient(config.httpClient),
		generated.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
			request.Header.Set("Authorization", "Bearer "+apiKey)
			request.Header.Set("User-Agent", "nvoken-go/0.1.0")
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create generated Runtime client: %w", err)
	}
	return &Client{raw: raw, retry: config.retry.normalized()}, nil
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
	body, err := request.generated()
	if err != nil {
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	ack, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationAcknowledgement], error) {
		response, callErr := c.raw.CreateInvocationWithResponse(ctx, body)
		if callErr != nil {
			return callResult[generated.InvocationAcknowledgement]{}, callErr
		}
		return callResult[generated.InvocationAcknowledgement]{
			Value:  response.JSON202,
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
		InvocationID:   ack.InvocationID,
		IdempotencyKey: request.IdempotencyKey,
		SessionID:      ack.SessionID,
		AgentID:        ack.AgentID,
		Status:         ack.Status,
		Deduplicated:   &ack.Deduplicated,
		DeadlineAt:     ack.DeadlineAt,
	}, nil
}

func (c *Client) Invocation(invocationID string) *InvocationHandle {
	return &InvocationHandle{client: c, InvocationID: invocationID}
}

func (c *Client) GetInvocation(ctx context.Context, invocationID string) (*Invocation, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Invocation], error) {
		response, err := c.raw.GetInvocationWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.Invocation]{}, err
		}
		return callResult[generated.Invocation]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) GetInvocationResult(ctx context.Context, invocationID string) (*InvocationResult, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationResult], error) {
		response, err := c.raw.GetInvocationResultWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.InvocationResult]{}, err
		}
		return callResult[generated.InvocationResult]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) CancelInvocation(ctx context.Context, invocationID string) (*Invocation, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Invocation], error) {
		response, err := c.raw.CancelInvocationWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.Invocation]{}, err
		}
		return callResult[generated.Invocation]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) PricingCapability(
	ctx context.Context,
	provider ModelProvider,
	model string,
) (*ModelPricingCapability, error) {
	params := &generated.GetModelPricingCapabilityParams{
		Provider: provider,
		Model:    model,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ModelPricingCapability], error) {
		response, err := c.raw.GetModelPricingCapabilityWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.ModelPricingCapability]{}, err
		}
		return callResult[generated.ModelPricingCapability]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
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

func (c *Client) ListProviderCredentials(
	ctx context.Context,
	options ListProviderCredentialsOptions,
) (*ProviderCredentialList, error) {
	var status *generated.ListProviderCredentialsParamsStatus
	if options.Status != nil {
		value := generated.ListProviderCredentialsParamsStatus(*options.Status)
		status = &value
	}
	params := &generated.ListProviderCredentialsParams{
		Provider:  options.Provider,
		Scope:     options.Scope,
		Status:    status,
		TenantKey: options.TenantKey,
		Limit:     options.Limit,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderCredentialList], error) {
		response, err := c.raw.ListProviderCredentialsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.ProviderCredentialList]{}, err
		}
		return callResult[generated.ProviderCredentialList]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) CreateProviderCredential(
	ctx context.Context,
	input CreateProviderCredentialInput,
) (*ProviderCredential, error) {
	if input.APIKey == "" || input.IdempotencyKey == "" {
		return nil, &Error{Category: ErrorValidation, Message: "provider API key and idempotency key are required"}
	}
	body := generated.CreateProviderCredentialRequest{
		Credential: generated.ProviderStaticCredential{
			APIKey: &input.APIKey,
		},
		ExpiresAt:      input.ExpiresAt,
		IdempotencyKey: input.IdempotencyKey,
		Provider:       input.Provider,
		Scope:          input.Scope,
		TenantKey:      input.TenantKey,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderCredential], error) {
		response, err := c.raw.CreateProviderCredentialWithResponse(ctx, body)
		if err != nil {
			return callResult[generated.ProviderCredential]{}, err
		}
		return callResult[generated.ProviderCredential]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) GetProviderCredential(ctx context.Context, id string) (*ProviderCredential, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderCredential], error) {
		response, err := c.raw.GetProviderCredentialWithResponse(ctx, id)
		if err != nil {
			return callResult[generated.ProviderCredential]{}, err
		}
		return callResult[generated.ProviderCredential]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) RotateProviderCredential(
	ctx context.Context,
	id string,
	input RotateProviderCredentialInput,
) (*ProviderCredential, error) {
	if input.APIKey == "" || input.IdempotencyKey == "" {
		return nil, &Error{Category: ErrorValidation, Message: "provider API key and idempotency key are required"}
	}
	body := generated.RotateProviderCredentialRequest{
		Credential: generated.ProviderStaticCredential{
			APIKey: &input.APIKey,
		},
		ExpiresAt:      input.ExpiresAt,
		IdempotencyKey: input.IdempotencyKey,
		OverlapSeconds: input.OverlapSeconds,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderCredential], error) {
		response, err := c.raw.RotateProviderCredentialWithResponse(ctx, id, body)
		if err != nil {
			return callResult[generated.ProviderCredential]{}, err
		}
		return callResult[generated.ProviderCredential]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) RevokeProviderCredential(ctx context.Context, id string) (*ProviderCredential, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderCredential], error) {
		response, err := c.raw.RevokeProviderCredentialWithResponse(ctx, id)
		if err != nil {
			return callResult[generated.ProviderCredential]{}, err
		}
		return callResult[generated.ProviderCredential]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
}

func (c *Client) ListInvocations(ctx context.Context, options ListInvocationsOptions) (*generated.InvocationList, error) {
	params := &generated.ListInvocationsParams{
		TenantKey:     options.TenantKey,
		DefaultTenant: options.DefaultTenant,
		SessionID:     options.SessionID,
		AgentID:       options.AgentID,
		Status:        options.Status,
		Cursor:        options.Cursor,
		Limit:         options.Limit,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationList], error) {
		response, err := c.raw.ListInvocationsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.InvocationList]{}, err
		}
		return callResult[generated.InvocationList]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) ListSessions(ctx context.Context, options ListSessionsOptions) (*generated.SessionList, error) {
	params := &generated.ListSessionsParams{
		TenantKey:     options.TenantKey,
		DefaultTenant: options.DefaultTenant,
		AgentID:       options.AgentID,
		SessionKey:    options.SessionKey,
		Cursor:        options.Cursor,
		Limit:         options.Limit,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.SessionList], error) {
		response, err := c.raw.ListSessionsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.SessionList]{}, err
		}
		return callResult[generated.SessionList]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Session], error) {
		response, err := c.raw.GetSessionWithResponse(ctx, sessionID)
		if err != nil {
			return callResult[generated.Session]{}, err
		}
		return callResult[generated.Session]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) ListMessages(ctx context.Context, sessionID string, options MessageListOptions) (*generated.SessionMessageList, error) {
	params := &generated.ListSessionMessagesParams{Cursor: options.Cursor, Limit: options.Limit}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.SessionMessageList], error) {
		response, err := c.raw.ListSessionMessagesWithResponse(ctx, sessionID, params)
		if err != nil {
			return callResult[generated.SessionMessageList]{}, err
		}
		return callResult[generated.SessionMessageList]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) GetTranscript(ctx context.Context, sessionID string, options TranscriptOptions) (*generated.TranscriptSnapshot, error) {
	params := &generated.GetSessionTranscriptParams{
		Cursor:    options.Cursor,
		PageToken: options.PageToken,
		Limit:     options.Limit,
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.TranscriptSnapshot], error) {
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
}

type InvocationHandle struct {
	client         *Client
	InvocationID   string           `json:"invocation_id"`
	IdempotencyKey string           `json:"idempotency_key,omitempty"`
	SessionID      string           `json:"session_id,omitempty"`
	AgentID        string           `json:"agent_id,omitempty"`
	Status         InvocationStatus `json:"status,omitempty"`
	Deduplicated   *bool            `json:"deduplicated,omitempty"`
	DeadlineAt     time.Time        `json:"deadline_at,omitempty"`
}

func (h *InvocationHandle) Refresh(ctx context.Context) (*Invocation, error) {
	invocation, err := h.client.GetInvocation(ctx, h.InvocationID)
	if err == nil {
		h.SessionID = invocation.SessionID
		h.AgentID = invocation.AgentID
		h.Status = invocation.Status
		h.DeadlineAt = invocation.DeadlineAt
	}
	return invocation, err
}

func (h *InvocationHandle) Wait(ctx context.Context, options WaitOptions) (*Invocation, error) {
	options = options.normalized()
	delay := options.MinPollInterval
	for {
		invocation, err := h.Refresh(ctx)
		if err != nil {
			return nil, err
		}
		if terminal(invocation.Status) {
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
		h.AgentID = result.Invocation.AgentID
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
func (h *InvocationHandle) Text(ctx context.Context) (string, error) {
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
		h.AgentID = invocation.AgentID
		h.Status = invocation.Status
	}
	return invocation, err
}

func (h *InvocationHandle) WaitForAction(ctx context.Context, options WaitOptions) (*Invocation, error) {
	options = options.normalized()
	delay := options.MinPollInterval
	for {
		invocation, err := h.Refresh(ctx)
		if err != nil {
			return nil, err
		}
		if invocation.Status == InvocationWaiting || terminal(invocation.Status) {
			return invocation, nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, transportError(ctx.Err())
		case <-timer.C:
		}
		delay = min(delay*2, options.MaxPollInterval)
	}
}

func (h *InvocationHandle) WaitForResult(ctx context.Context, options WaitOptions) (*InvocationResult, error) {
	invocation, err := h.Wait(ctx, options)
	if err != nil {
		return nil, err
	}
	if invocation.Status != InvocationCompleted {
		return nil, &Error{
			Category: ErrorConflict,
			Message:  fmt.Sprintf("Invocation %s ended with status %s", h.InvocationID, invocation.Status),
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
