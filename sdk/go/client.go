package nvoken

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
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
		return nil, &Error{Category: ErrorValidation, Message: err.Error(), Cause: err}
	}
	ack, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationAcknowledgement], error) {
		response, callErr := c.raw.CreateInvocationWithBodyWithResponse(
			ctx,
			"application/json",
			bytes.NewReader(body),
		)
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
		Cursor:    options.Cursor,
		Limit:     options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.ProviderCredentialList], error) {
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
	if err != nil {
		return nil, err
	}
	return &ProviderCredentialList{
		HasMore:    result.HasMore,
		Items:      result.Items,
		NextCursor: result.NextCursor,
	}, nil
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

func (c *Client) ListInvocations(ctx context.Context, options ListInvocationsOptions) (*InvocationList, error) {
	params := &generated.ListInvocationsParams{
		TenantKey:     options.TenantKey,
		DefaultTenant: options.DefaultTenant,
		SessionID:     options.SessionID,
		AgentID:       options.AgentID,
		Status:        options.Status,
		Cursor:        options.Cursor,
		Limit:         options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.InvocationList], error) {
		response, err := c.raw.ListInvocationsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.InvocationList]{}, err
		}
		return callResult[generated.InvocationList]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
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
		AgentID:       options.AgentID,
		SessionKey:    options.SessionKey,
		Cursor:        options.Cursor,
		Limit:         options.Limit,
	}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.SessionList], error) {
		response, err := c.raw.ListSessionsWithResponse(ctx, params)
		if err != nil {
			return callResult[generated.SessionList]{}, err
		}
		return callResult[generated.SessionList]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
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

func (c *Client) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Session], error) {
		response, err := c.raw.GetSessionWithResponse(ctx, sessionID)
		if err != nil {
			return callResult[generated.Session]{}, err
		}
		return callResult[generated.Session]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
	})
}

func (c *Client) ListSessionMessages(ctx context.Context, sessionID string, options MessageListOptions) (*SessionMessageList, error) {
	params := &generated.ListSessionMessagesParams{Cursor: options.Cursor, Limit: options.Limit}
	result, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.SessionMessageList], error) {
		response, err := c.raw.ListSessionMessagesWithResponse(ctx, sessionID, params)
		if err != nil {
			return callResult[generated.SessionMessageList]{}, err
		}
		return callResult[generated.SessionMessageList]{Value: response.JSON200, Status: response.StatusCode(), Header: responseHeader(response.HTTPResponse), Body: response.Body}, nil
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
		ResumeCursor:      result.ResumeCursor,
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
		drain.ResumeCursor = page.ResumeCursor
		if !page.HasMore {
			if drain.ResumeCursor == "" {
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

func (c *Client) EachProviderCredential(
	ctx context.Context,
	options ListProviderCredentialsOptions,
	consume func(ProviderCredential) error,
) error {
	options.Cursor = nil
	for {
		page, err := c.ListProviderCredentials(ctx, options)
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
				Message:  "provider credential page has_more without next_cursor",
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
		h.AgentID = invocation.AgentID
		h.Status = invocation.Status
	}
	return invocation, err
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
