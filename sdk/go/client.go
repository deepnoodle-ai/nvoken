package nvoken

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

type RetryPolicy struct {
	MaximumAttempts int
	MinimumDelay    time.Duration
	MaximumDelay    time.Duration
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaximumAttempts <= 0 {
		p.MaximumAttempts = 4
	}
	if p.MinimumDelay <= 0 {
		p.MinimumDelay = 100 * time.Millisecond
	}
	if p.MaximumDelay <= 0 {
		p.MaximumDelay = 2 * time.Second
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
	for attempt := 1; attempt <= policy.MaximumAttempts; attempt++ {
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
		if !replaySafe || attempt == policy.MaximumAttempts || !retryable(err, result.Status) {
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
		if delay > policy.MaximumDelay {
			return policy.MaximumDelay
		}
		return delay
	}
	delay := policy.MinimumDelay << (attempt - 1)
	if delay > policy.MaximumDelay {
		delay = policy.MaximumDelay
	}
	if delay <= 1 {
		return delay
	}
	return delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1))
}

func responseHeader(response *http.Response) http.Header {
	if response == nil {
		return make(http.Header)
	}
	return response.Header
}

func (c *Client) Invoke(ctx context.Context, request InvokeRequest) (*Handle, error) {
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
	return &Handle{client: c, InvocationID: ack.InvocationID, SessionID: ack.SessionID, Status: ack.Status}, nil
}

func (c *Client) Resume(ctx context.Context, invocationID string) (*Handle, error) {
	invocation, err := c.Get(ctx, invocationID)
	if err != nil {
		return nil, err
	}
	return &Handle{client: c, InvocationID: invocation.ID, SessionID: invocation.SessionID, Status: invocation.Status}, nil
}

func (c *Client) Get(ctx context.Context, invocationID string) (*Invocation, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Invocation], error) {
		response, err := c.raw.GetInvocationWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.Invocation]{}, err
		}
		return callResult[generated.Invocation]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) GetResult(ctx context.Context, invocationID string) (*InvocationResult, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationResult], error) {
		response, err := c.raw.GetInvocationResultWithResponse(ctx, invocationID)
		if err != nil {
			return callResult[generated.InvocationResult]{}, err
		}
		return callResult[generated.InvocationResult]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) Cancel(ctx context.Context, invocationID string) (*Invocation, error) {
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
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.SubmitClientToolResultsResponse], error) {
		response, callErr := c.raw.SubmitClientToolResultsWithResponse(ctx, invocationID, body)
		if callErr != nil {
			return callResult[generated.SubmitClientToolResultsResponse]{}, callErr
		}
		return callResult[generated.SubmitClientToolResultsResponse]{Value: response.JSON202, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
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
		TenantRef: options.TenantRef,
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
		TenantRef:      input.TenantRef,
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
		TenantRef:     options.TenantRef,
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
		TenantRef:     options.TenantRef,
		DefaultTenant: options.DefaultTenant,
		AgentID:       options.AgentID,
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

type Handle struct {
	client       *Client
	InvocationID string           `json:"invocation_id"`
	SessionID    string           `json:"session_id"`
	Status       InvocationStatus `json:"status"`
}

func (h *Handle) Refresh(ctx context.Context) (*Invocation, error) {
	invocation, err := h.client.Get(ctx, h.InvocationID)
	if err == nil {
		h.Status = invocation.Status
	}
	return invocation, err
}

func (h *Handle) Wait(ctx context.Context, options WaitOptions) (*Invocation, error) {
	options = options.normalized()
	delay := options.MinimumDelay
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
		if delay > options.MaximumDelay {
			delay = options.MaximumDelay
		}
	}
}

// Result reads the composed InvocationResult at any status: the
// authoritative Invocation, this Invocation's canonical messages, and the
// output_text projection.
func (h *Handle) Result(ctx context.Context) (*InvocationResult, error) {
	result, err := h.client.GetResult(ctx, h.InvocationID)
	if err == nil {
		h.Status = result.Invocation.Status
	}
	return result, err
}

// ListMessages returns this Invocation's canonical messages from the
// composed result read.
func (h *Handle) ListMessages(ctx context.Context) ([]SessionMessage, error) {
	result, err := h.Result(ctx)
	if err != nil {
		return nil, err
	}
	return result.Messages, nil
}

// Text returns the completed turn's canonical assistant text.
func (h *Handle) Text(ctx context.Context) (string, error) {
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

func (h *Handle) SubmitToolResults(ctx context.Context, results []ToolResult) (*ToolResultResponse, error) {
	response, err := h.client.SubmitToolResults(ctx, h.InvocationID, results)
	if err == nil {
		h.Status = response.Status
	}
	return response, err
}

func (h *Handle) Cancel(ctx context.Context) (*Invocation, error) {
	invocation, err := h.client.Cancel(ctx, h.InvocationID)
	if err == nil {
		h.Status = invocation.Status
	}
	return invocation, err
}
