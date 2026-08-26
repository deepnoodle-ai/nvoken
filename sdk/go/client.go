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
	raw               *generated.ClientWithResponses
	retry             RetryPolicy
	conversationLocks *conversationLockTable
}

type conversationLockTable struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newConversationLockTable() *conversationLockTable {
	return &conversationLockTable{locks: make(map[string]*sync.Mutex)}
}

func (t *conversationLockTable) lock(key string) *sync.Mutex {
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing := t.locks[key]; existing != nil {
		return existing
	}
	created := &sync.Mutex{}
	t.locks[key] = created
	return created
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
	return newClient(baseURL, apiKey, config.httpClient, config.retry)
}

func newClient(
	baseURL string,
	apiKey string,
	httpClient *http.Client,
	retry RetryPolicy,
) (*Client, error) {
	requestEditor := func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer "+apiKey)
		request.Header.Set("User-Agent", "nvoken-go/"+Version)
		return nil
	}
	raw, err := generated.NewClientWithResponses(
		baseURL,
		generated.WithHTTPClient(httpClient),
		generated.WithRequestEditorFn(requestEditor),
	)
	if err != nil {
		return nil, fmt.Errorf("create generated client: %w", err)
	}
	return &Client{
		raw:               raw,
		retry:             retry.normalized(),
		conversationLocks: newConversationLockTable(),
	}, nil
}

func (c *Client) Raw() *generated.ClientWithResponses { return c.raw }

func (c *Client) ListAdmissions(ctx context.Context, params *ListAdmissionsParams) (*AdmissionAttemptList, error) {
	response, err := c.raw.ListAdmissionsWithResponse(ctx, params)
	if err != nil {
		return nil, transportError(err)
	}
	if response.JSON200 == nil {
		return nil, errorFromResponse(response.StatusCode(), responseHeader(response.HTTPResponse), response.Body)
	}
	return response.JSON200, nil
}

func (c *Client) SummarizeAdmissions(ctx context.Context, params *SummarizeAdmissionsParams) (*AdmissionSummary, error) {
	response, err := c.raw.SummarizeAdmissionsWithResponse(ctx, params)
	if err != nil {
		return nil, transportError(err)
	}
	if response.JSON200 == nil {
		return nil, errorFromResponse(response.StatusCode(), responseHeader(response.HTTPResponse), response.Body)
	}
	return response.JSON200, nil
}

func (c *Client) ListTenants(ctx context.Context, params *ListTenantsParams) (*TenantList, error) {
	response, err := c.raw.ListTenantsWithResponse(ctx, params)
	if err != nil {
		return nil, transportError(err)
	}
	if response.JSON200 == nil {
		return nil, errorFromResponse(response.StatusCode(), responseHeader(response.HTTPResponse), response.Body)
	}
	return response.JSON200, nil
}

func (c *Client) DeleteTenant(ctx context.Context, id string) error {
	response, err := c.raw.DeleteTenantWithResponse(ctx, id)
	if err != nil {
		return transportError(err)
	}
	if response.StatusCode() != http.StatusNoContent {
		return errorFromResponse(response.StatusCode(), responseHeader(response.HTTPResponse), response.Body)
	}
	return nil
}

// Agent resolves one Agent key now. App ownership is the unmarked common
// case; pass one AgentLookupOptions for a tenant- or user-owned Agent.
func (c *Client) Agent(ctx context.Context, key string, options ...AgentLookupOptions) (*Agent, error) {
	if strings.TrimSpace(key) == "" {
		return nil, &Error{Category: ErrorValidation, Message: "Agent key is required"}
	}
	if len(options) > 1 {
		return nil, &Error{Category: ErrorValidation, Message: "Agent accepts at most one options value"}
	}
	owner := AgentOwner{}
	if len(options) == 1 {
		owner = options[0].OwnedBy
	}
	kind, tenant, user, err := agentOwnerCoordinates(owner)
	if err != nil {
		return nil, err
	}
	params := &generated.ListAgentsParams{
		OwnerKind: kind,
		TenantKey: optionalString(tenant),
		UserKey:   optionalString(user),
		AgentKey:  &key,
	}
	list, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.AgentList], error) {
		response, callErr := c.raw.ListAgentsWithResponse(ctx, params)
		if callErr != nil {
			return callResult[generated.AgentList]{}, callErr
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
	if len(list.Items) == 0 {
		return nil, &Error{Category: ErrorNotFound, Status: http.StatusNotFound, Message: "Agent not found"}
	}
	return &Agent{client: c, value: list.Items[0]}, nil
}

// Agents returns the management collection. Runnable hot paths normally use
// Client.Agent directly.
func (c *Client) Agents() *Agents { return &Agents{client: c} }

type Agents struct{ client *Client }

func (a *Agents) GetByID(ctx context.Context, id string) (*Agent, error) {
	value, err := callReplaySafe(ctx, a.client.retry, true, func() (callResult[generated.Agent], error) {
		response, callErr := a.client.raw.GetAgentWithResponse(ctx, id)
		if callErr != nil {
			return callResult[generated.Agent]{}, callErr
		}
		return callResult[generated.Agent]{
			Value:  response.JSON200,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &Agent{client: a.client, value: *value}, nil
}

func (a *Agents) List(ctx context.Context, options ListAgentsOptions) (*AgentPage, error) {
	kind, tenant, user, err := agentOwnerCoordinates(options.OwnedBy)
	if err != nil {
		return nil, err
	}
	params := &generated.ListAgentsParams{
		OwnerKind:       kind,
		TenantKey:       optionalString(tenant),
		UserKey:         optionalString(user),
		IncludeArchived: &options.Archived,
	}
	if options.Cursor != "" {
		cursor := generated.Cursor(options.Cursor)
		params.Cursor = &cursor
	}
	if options.Limit > 0 {
		limit := generated.Limit(options.Limit)
		params.Limit = &limit
	}
	page, err := callReplaySafe(ctx, a.client.retry, true, func() (callResult[generated.AgentList], error) {
		response, callErr := a.client.raw.ListAgentsWithResponse(ctx, params)
		if callErr != nil {
			return callResult[generated.AgentList]{}, callErr
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
	result := &AgentPage{HasMore: page.HasMore, NextCursor: page.NextCursor}
	result.Items = make([]*Agent, 0, len(page.Items))
	for _, item := range page.Items {
		result.Items = append(result.Items, &Agent{client: a.client, value: item})
	}
	return result, nil
}

func (a *Agents) Create(ctx context.Context, options CreateAgentOptions) (*Agent, error) {
	if strings.TrimSpace(options.Key) == "" {
		return nil, &Error{Category: ErrorValidation, Message: "Agent key is required"}
	}
	if options.Behavior.OutputSchema != nil {
		if err := PreflightOutputSchema(*options.Behavior.OutputSchema); err != nil {
			return nil, err
		}
	}
	idempotencyKey := options.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = generatedIdempotencyKey()
	}
	owner, err := encodeAgentOwner(options.OwnedBy)
	if err != nil {
		return nil, err
	}
	behavior := options.Behavior.generated()
	body := generated.CreateAgentRequest{
		AgentKey:     options.Key,
		Instructions: behavior.Instructions,
		Limits:       behavior.Limits,
		Memory:       behavior.Memory,
		Model:        behavior.Model,
		Name:         optionalString(options.Name),
		OutputSchema: behavior.OutputSchema,
		Owner:        owner,
		Tools:        behavior.Tools,
	}
	value, err := callReplaySafe(ctx, a.client.retry, true, func() (callResult[generated.Agent], error) {
		response, callErr := a.client.raw.CreateAgentWithResponse(
			ctx,
			&generated.CreateAgentParams{IdempotencyKey: idempotencyKey},
			body,
		)
		if callErr != nil {
			return callResult[generated.Agent]{}, callErr
		}
		return callResult[generated.Agent]{
			Value:  response.JSON201,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &Agent{client: a.client, value: *value}, nil
}

func (c *Client) Inline(behavior Behavior) *InlineRunner {
	return &InlineRunner{client: c, behavior: behavior}
}

// Turn constructs a local recovery handle. The first remote operation checks
// visibility using the credential and explicit access coordinates.
func (c *Client) Turn(id string, access TurnAccess) *Turn {
	return &Turn{
		client:    c,
		id:        id,
		access:    access,
		toolState: newToolExecutionState(),
	}
}

func (c *Client) startTurn(
	ctx context.Context,
	input TurnInput,
	behavior *generated.TurnBehaviorSelection,
	options TurnOptions,
	tools map[string]Tool,
) (*Turn, error) {
	if strings.TrimSpace(options.TenantKey) == "" {
		return nil, &Error{Category: ErrorValidation, Message: "Turn tenant key is required"}
	}
	idempotencyKey := options.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = generatedIdempotencyKey()
	}
	wireInput, err := encodeTurnInput(input)
	if err != nil {
		return nil, err
	}
	wireMemory, err := encodeMemorySelection(options.Memory)
	if err != nil {
		return nil, err
	}
	if options.Memory != nil && options.Memory.Scope == "user" && strings.TrimSpace(options.UserKey) == "" {
		return nil, &Error{Category: ErrorValidation, Message: "user memory requires a Turn user"}
	}
	wireConversation, err := encodeConversationSelection(options.Conversation)
	if err != nil {
		return nil, err
	}
	body := generated.CreateTurnRequest{
		Behavior:       behavior,
		Conversation:   wireConversation,
		IdempotencyKey: idempotencyKey,
		Input:          wireInput,
		Limits:         options.Limits,
		Memory:         wireMemory,
		TenantKey:      &options.TenantKey,
	}
	if options.UserKey != "" {
		body.UserKey = &options.UserKey
	}
	if len(options.Metadata) != 0 {
		metadata := generated.Metadata(options.Metadata)
		body.Metadata = &metadata
	}
	value, err := callReplaySafe(ctx, c.retry, true, func() (callResult[generated.Turn], error) {
		response, callErr := c.raw.CreateTurnWithResponse(ctx, &generated.CreateTurnParams{}, body)
		if callErr != nil {
			return callResult[generated.Turn]{}, callErr
		}
		return callResult[generated.Turn]{
			Value:  response.JSON202,
			Status: response.StatusCode(),
			Header: responseHeader(response.HTTPResponse),
			Body:   response.Body,
		}, nil
	})
	if err != nil {
		return nil, turnAdmissionError(err, idempotencyKey)
	}
	deduplicated := value.Deduplicated != nil && *value.Deduplicated
	return &Turn{
		client:         c,
		id:             value.ID,
		access:         TurnAccess{TenantKey: options.TenantKey, UserKey: options.UserKey},
		idempotencyKey: idempotencyKey,
		admission:      &TurnAdmission{IdempotencyKey: idempotencyKey, Deduplicated: deduplicated},
		tools:          bindToolMap(nil, mapValues(tools)),
		toolState:      newToolExecutionState(),
		wait:           options.Wait,
	}, nil
}

func encodeTurnInput(input TurnInput) (generated.TurnInput, error) {
	switch value := input.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return generated.TurnInput{}, &Error{Category: ErrorValidation, Message: "Turn input is required"}
		}
		wire := generated.TurnInput{}
		if err := wire.FromTurnInput0(value); err != nil {
			return generated.TurnInput{}, validationError("encode Turn input", err)
		}
		return wire, nil
	case []InputBlock:
		if len(value) == 0 {
			return generated.TurnInput{}, &Error{Category: ErrorValidation, Message: "Turn input blocks are required"}
		}
		if err := PreflightInputBlocks(value); err != nil {
			return generated.TurnInput{}, err
		}
		blocks := make([]generated.InputBlock, 0, len(value))
		for _, block := range value {
			encoded, err := json.Marshal(block.wire())
			if err != nil {
				return generated.TurnInput{}, validationError("encode Turn input block", err)
			}
			var wireBlock generated.InputBlock
			if err := json.Unmarshal(encoded, &wireBlock); err != nil {
				return generated.TurnInput{}, validationError("encode Turn input block", err)
			}
			blocks = append(blocks, wireBlock)
		}
		wire := generated.TurnInput{}
		if err := wire.FromTurnInput1(blocks); err != nil {
			return generated.TurnInput{}, validationError("encode Turn input blocks", err)
		}
		return wire, nil
	default:
		return generated.TurnInput{}, &Error{Category: ErrorValidation, Message: "Turn input must be a string or []InputBlock"}
	}
}

func mapValues(tools map[string]Tool) []Tool {
	values := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		values = append(values, tool)
	}
	return values
}

func agentOwnerCoordinates(owner AgentOwner) (generated.AgentOwnerKind, string, string, error) {
	switch owner.kind {
	case agentOwnerApp:
		if owner.tenant != "" || owner.user != "" {
			return "", "", "", &Error{Category: ErrorValidation, Message: "App-owned Agent cannot carry tenant or user coordinates"}
		}
		return generated.AgentOwnerKindApp, "", "", nil
	case agentOwnerTenant:
		if strings.TrimSpace(owner.tenant) == "" {
			return "", "", "", &Error{Category: ErrorValidation, Message: "tenant-owned Agent requires a tenant"}
		}
		if owner.user != "" {
			return "", "", "", &Error{Category: ErrorValidation, Message: "tenant-owned Agent cannot carry a user"}
		}
		return generated.AgentOwnerKindTenant, owner.tenant, "", nil
	case agentOwnerUser:
		if strings.TrimSpace(owner.tenant) == "" || strings.TrimSpace(owner.user) == "" {
			return "", "", "", &Error{Category: ErrorValidation, Message: "user-owned Agent requires both tenant and user"}
		}
		return generated.AgentOwnerKindUser, owner.tenant, owner.user, nil
	default:
		return "", "", "", &Error{Category: ErrorValidation, Message: "unknown Agent owner kind"}
	}
}

func encodeAgentOwner(owner AgentOwner) (generated.AgentOwner, error) {
	kind, tenant, user, err := agentOwnerCoordinates(owner)
	if err != nil {
		return generated.AgentOwner{}, err
	}
	wire := generated.AgentOwner{}
	switch kind {
	case generated.AgentOwnerKindApp:
		err = wire.FromAppAgentOwner(generated.AppAgentOwner{Kind: generated.AppAgentOwnerKindApp})
	case generated.AgentOwnerKindTenant:
		err = wire.FromTenantAgentOwner(generated.TenantAgentOwner{Kind: generated.TenantAgentOwnerKindTenant, TenantKey: tenant})
	case generated.AgentOwnerKindUser:
		err = wire.FromUserAgentOwner(generated.UserAgentOwner{Kind: generated.UserAgentOwnerKindUser, TenantKey: tenant, UserKey: user})
	}
	if err != nil {
		return generated.AgentOwner{}, validationError("encode Agent owner", err)
	}
	return wire, nil
}

func encodeMemorySelection(selection *MemorySelection) (*generated.TurnMemorySelection, error) {
	if selection == nil {
		return nil, nil
	}
	wire := generated.TurnMemorySelection{}
	var err error
	switch selection.Scope {
	case "none":
		err = wire.FromNoTurnMemory(generated.NoTurnMemory{Scope: generated.NoTurnMemoryScopeNone})
	case "tenant":
		namespace := optionalString(selection.Namespace)
		err = wire.FromTenantTurnMemory(generated.TenantTurnMemory{Scope: generated.TenantTurnMemoryScopeTenant, Namespace: namespace})
	case "user":
		namespace := optionalString(selection.Namespace)
		err = wire.FromUserTurnMemory(generated.UserTurnMemory{Scope: generated.UserTurnMemoryScopeUser, Namespace: namespace})
	default:
		return nil, &Error{Category: ErrorValidation, Message: "memory scope must be none, tenant, or user"}
	}
	if err != nil {
		return nil, validationError("encode memory selection", err)
	}
	return &wire, nil
}

func encodeConversationSelection(selection *ConversationSelection) (*generated.TurnConversation, error) {
	if selection == nil {
		return nil, nil
	}
	count := 0
	if selection.ID != "" {
		count++
	}
	if selection.Key != "" {
		count++
	}
	if count != 1 {
		return nil, &Error{Category: ErrorValidation, Message: "Conversation selection requires exactly one of ID or Key"}
	}
	wire := generated.TurnConversation{}
	var err error
	if selection.ID != "" {
		err = wire.FromContinueTurnConversation(generated.ContinueTurnConversation{
			ConversationID: selection.ID,
			Mode:           generated.Continue,
		})
	} else {
		owner := generated.ConversationOwner{}
		switch selection.Owner.kind {
		case conversationOwnerTenant:
			err = owner.FromTenantConversationOwner(generated.TenantConversationOwner{Kind: generated.TenantConversationOwnerKindTenant})
		case conversationOwnerUser:
			if strings.TrimSpace(selection.Owner.user) == "" {
				return nil, &Error{Category: ErrorValidation, Message: "user-owned Conversation requires a user"}
			}
			err = owner.FromUserConversationOwner(generated.UserConversationOwner{Kind: generated.UserConversationOwnerKindUser, UserKey: selection.Owner.user})
		default:
			return nil, &Error{Category: ErrorValidation, Message: "unknown Conversation owner kind"}
		}
		if err != nil {
			return nil, validationError("encode Conversation owner", err)
		}
		var metadata *generated.ConversationMetadata
		if len(selection.Metadata) != 0 {
			value := generated.ConversationMetadata(selection.Metadata)
			metadata = &value
		}
		err = wire.FromContinueOrCreateTurnConversation(generated.ContinueOrCreateTurnConversation{
			ConversationKey: selection.Key,
			Metadata:        metadata,
			Mode:            generated.ContinueOrCreate,
			Owner:           owner,
		})
	}
	if err != nil {
		return nil, validationError("encode Conversation selection", err)
	}
	return &wire, nil
}

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
// separate argument because MCPServer is durable AgentRevision
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

func generatedModelProvider(provider ModelProvider) (generated.ModelProvider, error) {
	if strings.TrimSpace(provider) == "" {
		return "", fmt.Errorf("model provider is required")
	}
	return generated.ModelProvider(provider), nil
}

func generatedMCPServer(server MCPServer) generated.MCPServer {
	result := generated.MCPServer{
		Name: server.Name,
		URL:  server.URL,
	}
	if server.Transport != nil {
		transport := *server.Transport
		result.Transport = &transport
	}
	if server.AllowedTools != nil {
		allowedTools := append([]string(nil), (*server.AllowedTools)...)
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
	if input.Name == "" || !input.Type.Valid() {
		return nil, &Error{Category: ErrorValidation, Message: "credential name and type are required"}
	}
	// The contract requires a key here, so one is generated when the caller
	// omits it. That protects this call's own retries; a caller that wants a
	// retry of its own to deduplicate has to supply the key itself.
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = generatedIdempotencyKey()
	}
	body := generated.CreateCredentialRequest{
		AppID:     input.AppID,
		ExpiresAt: input.ExpiresAt,
		Name:      input.Name,
		Type:      input.Type,
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
	if credentialID == "" || input.OverlapSeconds < 0 || input.OverlapSeconds > 86400 {
		return nil, &Error{Category: ErrorValidation, Message: "credential ID is required, and overlap seconds must be between 0 and 86400"}
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = generatedIdempotencyKey()
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
	if input.APIKey == "" {
		return nil, &Error{Category: ErrorValidation, Message: "provider API key is required"}
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = generatedIdempotencyKey()
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
		input.IdempotencyKey = generatedIdempotencyKey()
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
	if input.APIKey == "" {
		return nil, &Error{Category: ErrorValidation, Message: "provider API key is required"}
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = generatedIdempotencyKey()
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
// It requires an installation-admin key or a trusted Console presentation.
func (c *Client) RegisterApp(ctx context.Context, name string, options RegisterAppOptions) (*AppRegistration, error) {
	body := generated.RegisterAppJSONRequestBody{
		Name:                     name,
		ExternalRef:              options.ExternalRef,
		DisplayName:              options.DisplayName,
		OrgID:                    options.OrgID,
		CallbackTimeoutSeconds:   options.CallbackTimeoutSeconds,
		DefaultRateLimits:        options.DefaultRateLimits.generated(),
		MachineConcurrencyLimits: options.MachineConcurrencyLimits.generated(),
	}
	browser, err := options.BrowserAccess.generated()
	if err != nil {
		return nil, err
	}
	body.BrowserAccess = browser
	if options.CreditPolicy != nil {
		policy := generated.CreditPolicy(*options.CreditPolicy)
		body.CreditPolicy = &policy
	}
	return callReplaySafe(ctx, c.retry, false, func() (callResult[generated.AppRegistration], error) {
		response, err := c.raw.RegisterAppWithResponse(ctx, body)
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

// UpdateApp changes an App's mutable configuration: its presentation, its
// admission ceilings, its credit policy, and whether browser-direct and
// anonymous callers are allowed. Name and external_ref cannot be changed.
func (c *Client) UpdateApp(ctx context.Context, appID string, options UpdateAppOptions) (*App, error) {
	// Encoded by hand because three members distinguish "leave it alone" from
	// "turn it off", and a null is the only way to say the second. The
	// generated body omits a nil pointer, so it can express one of the two.
	body, err := options.encoded()
	if err != nil {
		return nil, err
	}
	return callReplaySafe(ctx, c.retry, true, func() (callResult[generated.App], error) {
		response, err := c.raw.UpdateAppWithBodyWithResponse(
			ctx,
			appID,
			"application/json",
			bytes.NewReader(body),
		)
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
