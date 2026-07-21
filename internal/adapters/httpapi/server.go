// Package httpapi serves the nvoken HTTP API.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

const (
	serverReadHeaderTimeout = 10 * time.Second
	serverReadTimeout       = 30 * time.Second
	// Leave response headroom after the bounded body read and the Postgres
	// adapter's 120-second statement timeout while still bounding handlers.
	serverWriteTimeout     = 180 * time.Second
	serverIdleTimeout      = 60 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultStreamPoll      = time.Second
	defaultStreamKeepalive = 15 * time.Second
	defaultStreamLifetime  = 55 * time.Minute
	defaultStreamWrite     = 10 * time.Second
)

type RuntimeService interface {
	Admit(context.Context, domain.RuntimeAuthContext, services.CreateInvocationInput) (services.InvocationAcknowledgement, error)
	GetInvocation(context.Context, domain.RuntimeAuthContext, string) (services.InvocationRead, error)
	ListInvocations(context.Context, domain.RuntimeAuthContext, services.InvocationListInput) (services.InvocationList, error)
	GetSession(context.Context, domain.RuntimeAuthContext, string) (services.SessionRead, error)
	ListSessions(context.Context, domain.RuntimeAuthContext, services.SessionListInput) (services.SessionList, error)
	ListSessionMessages(context.Context, domain.RuntimeAuthContext, string, services.MessageListInput) (services.SessionMessageList, error)
	GetSessionTranscript(context.Context, domain.RuntimeAuthContext, string, services.TranscriptInput) (services.TranscriptSnapshot, error)
	GetSessionTranscriptStreamState(context.Context, domain.RuntimeAuthContext, string) (services.TranscriptStreamState, error)
}

type cancellationRuntimeService interface {
	CancelInvocation(context.Context, domain.RuntimeAuthContext, string) (services.InvocationRead, error)
}

type Config struct {
	Addr            string
	Authenticator   ports.RuntimeAuthenticator
	Runtime         RuntimeService
	Logger          *slog.Logger
	ShutdownTimeout time.Duration
	LiveEvents      ports.LiveEventBus
	Stream          StreamConfig
}

type StreamConfig struct {
	PollInterval      time.Duration
	KeepaliveInterval time.Duration
	MaxLifetime       time.Duration
	WriteTimeout      time.Duration
}

type Server struct {
	http            *http.Server
	shutdownTimeout time.Duration
	cancelStreams   context.CancelFunc
}

func NewServer(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	stream := normalizedStreamConfig(cfg.Stream)
	streamShutdown, cancelStreams := context.WithCancel(context.Background())
	handler := newHandler(handlerConfig{
		authenticator:  cfg.Authenticator,
		runtime:        cfg.Runtime,
		logger:         logger,
		liveEvents:     cfg.LiveEvents,
		stream:         stream,
		streamShutdown: streamShutdown,
	})
	shutdownTimeout := cfg.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	return &Server{
		http: &http.Server{
			Addr:              cfg.Addr,
			Handler:           handler,
			ReadHeaderTimeout: serverReadHeaderTimeout,
			ReadTimeout:       serverReadTimeout,
			// Streaming handlers set a bounded deadline on every write. A global
			// WriteTimeout would terminate every SSE connection at the same age.
			WriteTimeout:   0,
			IdleTimeout:    serverIdleTimeout,
			MaxHeaderBytes: 1 << 20,
		},
		shutdownTimeout: shutdownTimeout,
		cancelStreams:   cancelStreams,
	}
}

type handlerConfig struct {
	authenticator  ports.RuntimeAuthenticator
	runtime        RuntimeService
	logger         *slog.Logger
	liveEvents     ports.LiveEventBus
	stream         StreamConfig
	streamShutdown context.Context
}

type handler struct {
	authenticator  ports.RuntimeAuthenticator
	runtime        RuntimeService
	logger         *slog.Logger
	liveEvents     ports.LiveEventBus
	stream         StreamConfig
	streamShutdown context.Context
}

func newHandler(cfg handlerConfig) http.Handler {
	h := &handler{
		authenticator: cfg.authenticator, runtime: cfg.runtime, logger: cfg.logger,
		liveEvents: cfg.liveEvents, stream: normalizedStreamConfig(cfg.stream), streamShutdown: cfg.streamShutdown,
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if h.streamShutdown == nil {
		h.streamShutdown = context.Background()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.requireMethod(http.MethodGet, h.health))
	mux.HandleFunc("/v1/invocations", h.invocations)
	mux.HandleFunc("/v1/invocations/{invocation_id}", h.requireMethod(http.MethodGet, h.getInvocation))
	mux.HandleFunc("/v1/invocations/{invocation_id}/cancel", h.requireMethod(http.MethodPost, h.cancelInvocation))
	mux.HandleFunc("/v1/sessions", h.requireMethod(http.MethodGet, h.listSessions))
	mux.HandleFunc("/v1/sessions/{session_id}/messages", h.requireMethod(http.MethodGet, h.listSessionMessages))
	mux.HandleFunc("/v1/sessions/{session_id}/transcript", h.requireMethod(http.MethodGet, h.getSessionTranscript))
	mux.HandleFunc("/v1/sessions/{session_id}/transcript/stream", h.requireMethod(http.MethodGet, h.streamSessionTranscript))
	mux.HandleFunc("/v1/sessions/{session_id}", h.requireMethod(http.MethodGet, h.getSession))
	mux.HandleFunc("/", h.notFound)
	return h.logRequests(mux)
}

func (h *handler) invocations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createInvocation(w, r)
	case http.MethodGet:
		h.listInvocations(w, r)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{
			Code: "invalid_request", Message: "The request method is not allowed.",
			RequestID: requestIDFromContext(r.Context()),
		})
	}
}

func (h *handler) requireMethod(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeJSON(w, http.StatusMethodNotAllowed, errorResponse{
				Code: "invalid_request", Message: "The request method is not allowed.",
				RequestID: requestIDFromContext(r.Context()),
			})
			return
		}
		next(w, r)
	}
}

func (h *handler) notFound(w http.ResponseWriter, r *http.Request) {
	h.writeError(w, requestIDFromContext(r.Context()), &services.PublicError{
		Code: services.CodeNotFound, Message: "The requested resource was not found.",
	})
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *handler) createInvocation(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	var input services.CreateInvocationInput
	if err := decodeInvocationRequest(w, r, &input); err != nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: err.Error()})
		return
	}
	if h.runtime == nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	acknowledgement, err := h.runtime.Admit(r.Context(), auth, input)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	writeJSON(w, http.StatusAccepted, invocationAcknowledgementResponse{
		AgentID: acknowledgement.AgentID, SessionID: acknowledgement.SessionID,
		InvocationID: acknowledgement.InvocationID, Status: acknowledgement.Status,
		Deduplicated: acknowledgement.Deduplicated,
	})
}

func (h *handler) getInvocation(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	if h.runtime == nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	invocation, err := h.runtime.GetInvocation(r.Context(), auth, r.PathValue("invocation_id"))
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	writeJSON(w, http.StatusOK, invocationResponseFromService(invocation))
}

func (h *handler) cancelInvocation(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	var body [1]byte
	if count, readErr := r.Body.Read(body[:]); count != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: "The cancellation request body must be empty."})
		return
	}
	if h.runtime == nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	cancellationRuntime, ok := h.runtime.(cancellationRuntimeService)
	if !ok {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	invocation, err := cancellationRuntime.CancelInvocation(r.Context(), auth, r.PathValue("invocation_id"))
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	writeJSON(w, http.StatusOK, invocationResponseFromService(invocation))
}

func (h *handler) listInvocations(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	query, err := strictQuery(r, "tenant_ref", "default_tenant", "session_id", "agent_id", "status", "cursor", "limit")
	if err != nil {
		h.writeError(w, requestID, invalidQuery(err))
		return
	}
	input, err := invocationListInput(query)
	if err != nil {
		h.writeError(w, requestID, invalidQuery(err))
		return
	}
	if h.runtime == nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	page, err := h.runtime.ListInvocations(r.Context(), auth, input)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	items := make([]invocationResponse, len(page.Items))
	for i, item := range page.Items {
		items[i] = invocationResponseFromService(item)
	}
	writeJSON(w, http.StatusOK, invocationListResponse{Items: items, HasMore: page.HasMore, NextCursor: page.NextCursor})
}

func (h *handler) getSession(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	if h.runtime == nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	session, err := h.runtime.GetSession(r.Context(), auth, r.PathValue("session_id"))
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		ID: session.ID, AgentID: session.AgentID, TenantRef: session.TenantRef,
		SessionKey: session.SessionKey, ActiveInvocationID: session.ActiveInvocationID,
		ActiveInvocationStatus: session.ActiveInvocationStatus,
		CreatedAt:              session.CreatedAt, UpdatedAt: session.UpdatedAt,
	})
}

func (h *handler) listSessions(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	query, err := strictQuery(r, "tenant_ref", "default_tenant", "agent_id", "session_key", "cursor", "limit")
	if err != nil {
		h.writeError(w, requestID, invalidQuery(err))
		return
	}
	input, err := sessionListInput(query)
	if err != nil {
		h.writeError(w, requestID, invalidQuery(err))
		return
	}
	if h.runtime == nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	page, err := h.runtime.ListSessions(r.Context(), auth, input)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	items := make([]sessionResponse, len(page.Items))
	for i, item := range page.Items {
		items[i] = sessionResponseFromService(item)
	}
	writeJSON(w, http.StatusOK, sessionListResponse{Items: items, HasMore: page.HasMore, NextCursor: page.NextCursor})
}

func (h *handler) listSessionMessages(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	query, err := strictQuery(r, "cursor", "limit")
	if err != nil {
		h.writeError(w, requestID, invalidQuery(err))
		return
	}
	limit, err := queryLimit(query)
	if err != nil {
		h.writeError(w, requestID, invalidQuery(err))
		return
	}
	if h.runtime == nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	page, err := h.runtime.ListSessionMessages(r.Context(), auth, r.PathValue("session_id"), services.MessageListInput{
		Cursor: query.Get("cursor"), Limit: limit,
	})
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	items := make([]sessionMessageResponse, len(page.Items))
	for i, item := range page.Items {
		items[i] = sessionMessageResponseFromDomain(item)
	}
	writeJSON(w, http.StatusOK, sessionMessageListResponse{Items: items, HasMore: page.HasMore, NextCursor: page.NextCursor})
}

func (h *handler) getSessionTranscript(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	query, err := strictQuery(r, "cursor", "page_token", "limit")
	if err != nil {
		h.writeError(w, requestID, invalidQuery(err))
		return
	}
	limit, err := queryLimit(query)
	if err != nil {
		h.writeError(w, requestID, invalidQuery(err))
		return
	}
	if h.runtime == nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	snapshot, err := h.runtime.GetSessionTranscript(r.Context(), auth, r.PathValue("session_id"), services.TranscriptInput{
		Cursor: query.Get("cursor"), PageToken: query.Get("page_token"), Limit: limit,
	})
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	messages := make([]sessionMessageResponse, len(snapshot.Messages))
	for i, item := range snapshot.Messages {
		messages[i] = sessionMessageResponseFromDomain(item)
	}
	changes := make([]invocationChangeResponse, len(snapshot.InvocationChanges))
	for i, item := range snapshot.InvocationChanges {
		changes[i] = invocationChangeResponseFromDomain(item)
	}
	writeJSON(w, http.StatusOK, transcriptSnapshotResponse{
		Messages: messages, InvocationChanges: changes, HasMore: snapshot.HasMore,
		ResumeCursor: snapshot.ResumeCursor, NextPageToken: snapshot.NextPageToken,
	})
}

func (h *handler) authenticate(r *http.Request) (domain.RuntimeAuthContext, error) {
	if h.authenticator == nil {
		return domain.RuntimeAuthContext{}, &authenticationError{}
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return domain.RuntimeAuthContext{}, &authenticationError{}
	}
	scheme, token, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t\r\n") {
		return domain.RuntimeAuthContext{}, &authenticationError{}
	}
	auth, err := h.authenticator.Authenticate(r.Context(), token)
	if err != nil {
		return domain.RuntimeAuthContext{}, &authenticationError{cause: err}
	}
	return auth, nil
}

type authenticationError struct{ cause error }

func (e *authenticationError) Error() string { return "A valid Runtime credential is required." }
func (e *authenticationError) Unwrap() error { return e.cause }

type errorResponse struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Details   map[string]any `json:"details,omitempty"`
}

func (h *handler) writeError(w http.ResponseWriter, requestID string, err error) {
	status := http.StatusInternalServerError
	response := errorResponse{Code: "internal", Message: "The request could not be completed.", RequestID: requestID}
	var authentication *authenticationError
	if errors.As(err, &authentication) {
		status = http.StatusUnauthorized
		response.Code = "unauthenticated"
		response.Message = authentication.Error()
		writeJSON(w, status, response)
		return
	}
	var public *services.PublicError
	if errors.As(err, &public) {
		response.Code = string(public.Code)
		response.Message = public.Message
		response.Details = public.Details
		switch public.Code {
		case services.CodeInvalidRequest:
			status = http.StatusBadRequest
		case services.CodeForbidden:
			status = http.StatusForbidden
		case services.CodeNotFound:
			status = http.StatusNotFound
		case services.CodeIdempotencyConflict, services.CodeSessionInvocationActive:
			status = http.StatusConflict
		case services.CodeUnavailable:
			status = http.StatusServiceUnavailable
		default:
			status = http.StatusInternalServerError
		}
		if status == http.StatusInternalServerError {
			h.logger.Error("runtime request failed", "request_id", requestID, "error", err)
		}
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusServiceUnavailable
		response.Code = "unavailable"
		response.Message = "The service is temporarily unavailable."
	} else {
		h.logger.Error("runtime request failed", "request_id", requestID, "error", err)
	}
	writeJSON(w, status, response)
}

type invocationAcknowledgementResponse struct {
	AgentID      string                  `json:"agent_id"`
	SessionID    string                  `json:"session_id"`
	InvocationID string                  `json:"invocation_id"`
	Status       domain.InvocationStatus `json:"status"`
	Deduplicated bool                    `json:"deduplicated"`
}

type invocationResponse struct {
	ID                  string                        `json:"id"`
	AgentID             string                        `json:"agent_id"`
	SessionID           string                        `json:"session_id"`
	Status              domain.InvocationStatus       `json:"status"`
	Error               any                           `json:"error"`
	Usage               any                           `json:"usage"`
	Provenance          any                           `json:"provenance"`
	Budgets             services.InvocationBudgetRead `json:"budgets"`
	ActiveExecutionMS   int64                         `json:"active_execution_ms"`
	WallClockDeadlineAt time.Time                     `json:"wall_clock_deadline_at"`
	CreatedAt           time.Time                     `json:"created_at"`
	UpdatedAt           time.Time                     `json:"updated_at"`
	CompletedAt         *time.Time                    `json:"completed_at"`
}

type invocationListResponse struct {
	Items      []invocationResponse `json:"items"`
	HasMore    bool                 `json:"has_more"`
	NextCursor *string              `json:"next_cursor"`
}

type sessionResponse struct {
	ID                     string                   `json:"id"`
	AgentID                string                   `json:"agent_id"`
	TenantRef              *string                  `json:"tenant_ref"`
	SessionKey             *string                  `json:"session_key"`
	ActiveInvocationID     *string                  `json:"active_invocation_id"`
	ActiveInvocationStatus *domain.InvocationStatus `json:"active_invocation_status"`
	CreatedAt              time.Time                `json:"created_at"`
	UpdatedAt              time.Time                `json:"updated_at"`
}

type sessionListResponse struct {
	Items      []sessionResponse `json:"items"`
	HasMore    bool              `json:"has_more"`
	NextCursor *string           `json:"next_cursor"`
}

type sessionMessageResponse struct {
	ID           string             `json:"id"`
	SessionID    string             `json:"session_id"`
	AgentID      string             `json:"agent_id"`
	InvocationID string             `json:"invocation_id"`
	Sequence     int64              `json:"sequence"`
	Role         domain.MessageRole `json:"role"`
	Content      json.RawMessage    `json:"content"`
	CreatedAt    time.Time          `json:"created_at"`
}

type sessionMessageListResponse struct {
	Items      []sessionMessageResponse `json:"items"`
	HasMore    bool                     `json:"has_more"`
	NextCursor *string                  `json:"next_cursor"`
}

type invocationChangeResponse struct {
	InvocationID           string                  `json:"invocation_id"`
	Revision               int64                   `json:"revision"`
	Status                 domain.InvocationStatus `json:"status"`
	ThroughMessageSequence *int64                  `json:"through_message_sequence"`
	Error                  any                     `json:"error"`
	Usage                  any                     `json:"usage"`
	Provenance             any                     `json:"provenance"`
	OccurredAt             time.Time               `json:"occurred_at"`
}

type transcriptSnapshotResponse struct {
	Messages          []sessionMessageResponse   `json:"messages"`
	InvocationChanges []invocationChangeResponse `json:"invocation_changes"`
	HasMore           bool                       `json:"has_more"`
	ResumeCursor      string                     `json:"resume_cursor"`
	NextPageToken     *string                    `json:"next_page_token"`
}

func invocationResponseFromService(invocation services.InvocationRead) invocationResponse {
	return invocationResponse{
		ID: invocation.ID, AgentID: invocation.AgentID, SessionID: invocation.SessionID,
		Status: invocation.Status, Error: rawJSONOrNil(invocation.Error),
		Usage: rawJSONOrNil(invocation.Usage), Provenance: rawJSONOrNil(invocation.Provenance),
		Budgets: invocation.Budgets, ActiveExecutionMS: invocation.ActiveExecutionMS,
		WallClockDeadlineAt: invocation.WallClockDeadlineAt,
		CreatedAt:           invocation.CreatedAt, UpdatedAt: invocation.UpdatedAt, CompletedAt: invocation.CompletedAt,
	}
}

func sessionResponseFromService(session services.SessionRead) sessionResponse {
	return sessionResponse{
		ID: session.ID, AgentID: session.AgentID, TenantRef: session.TenantRef,
		SessionKey: session.SessionKey, ActiveInvocationID: session.ActiveInvocationID,
		ActiveInvocationStatus: session.ActiveInvocationStatus,
		CreatedAt:              session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}
}

func sessionMessageResponseFromDomain(message domain.SessionMessage) sessionMessageResponse {
	return sessionMessageResponse{
		ID: message.ID, SessionID: message.SessionID, AgentID: message.AgentID,
		InvocationID: message.InvocationID, Sequence: message.Sequence, Role: message.Role,
		Content: message.Content, CreatedAt: message.CreatedAt,
	}
}

func invocationChangeResponseFromDomain(change domain.InvocationLifecycleChange) invocationChangeResponse {
	return invocationChangeResponse{
		InvocationID: change.InvocationID, Revision: change.Revision, Status: change.Status,
		ThroughMessageSequence: change.ThroughMessageSequence,
		Error:                  rawJSONOrNil(change.Error), Usage: rawJSONOrNil(change.Usage),
		Provenance: rawJSONOrNil(change.Provenance), OccurredAt: change.CreatedAt,
	}
}

func strictQuery(r *http.Request, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errors.New("query parameters are invalid")
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allowedSet[key]; !ok {
			return nil, fmt.Errorf("query parameter %s is not supported", key)
		}
		if len(entries) != 1 {
			return nil, fmt.Errorf("query parameter %s must appear once", key)
		}
		if (key == "cursor" || key == "page_token") && entries[0] == "" {
			return nil, fmt.Errorf("query parameter %s must not be blank", key)
		}
	}
	return values, nil
}

func invocationListInput(query url.Values) (services.InvocationListInput, error) {
	limit, err := queryLimit(query)
	if err != nil {
		return services.InvocationListInput{}, err
	}
	defaultTenant, err := queryBool(query, "default_tenant")
	if err != nil {
		return services.InvocationListInput{}, err
	}
	input := services.InvocationListInput{
		TenantRef: optionalQueryString(query, "tenant_ref"), DefaultTenant: defaultTenant,
		SessionID: optionalQueryString(query, "session_id"), AgentID: optionalQueryString(query, "agent_id"),
		Cursor: query.Get("cursor"), Limit: limit,
	}
	if value := optionalQueryString(query, "status"); value != nil {
		status := domain.InvocationStatus(*value)
		input.Status = &status
	}
	return input, nil
}

func sessionListInput(query url.Values) (services.SessionListInput, error) {
	limit, err := queryLimit(query)
	if err != nil {
		return services.SessionListInput{}, err
	}
	defaultTenant, err := queryBool(query, "default_tenant")
	if err != nil {
		return services.SessionListInput{}, err
	}
	return services.SessionListInput{
		TenantRef: optionalQueryString(query, "tenant_ref"), DefaultTenant: defaultTenant,
		AgentID: optionalQueryString(query, "agent_id"), SessionKey: optionalQueryString(query, "session_key"),
		Cursor: query.Get("cursor"), Limit: limit,
	}, nil
}

func optionalQueryString(query url.Values, key string) *string {
	values, ok := query[key]
	if !ok {
		return nil
	}
	value := values[0]
	return &value
}

func queryLimit(query url.Values) (int, error) {
	value := optionalQueryString(query, "limit")
	if value == nil {
		return 0, nil
	}
	limit, err := strconv.Atoi(*value)
	if err != nil {
		return 0, errors.New("limit must be an integer")
	}
	if limit < 1 || limit > services.MaxRecoveryPageSize {
		return 0, fmt.Errorf("limit must be between 1 and %d", services.MaxRecoveryPageSize)
	}
	return limit, nil
}

func queryBool(query url.Values, key string) (bool, error) {
	value := optionalQueryString(query, key)
	if value == nil || *value == "false" {
		return false, nil
	}
	if *value == "true" {
		return true, nil
	}
	return false, fmt.Errorf("%s must be true or false", key)
}

func invalidQuery(err error) error {
	return &services.PublicError{Code: services.CodeInvalidRequest, Message: err.Error() + "."}
}

func rawJSONOrNil(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return nil
	}
	return decoded
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type requestIDContextKey struct{}

func (h *handler) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID, err := newRequestID()
		if err != nil {
			requestID = "req_unavailable"
		}
		w.Header().Set("X-Request-ID", requestID)
		if !strings.HasSuffix(r.URL.Path, "/transcript/stream") {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(serverWriteTimeout))
		}
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		request := r.WithContext(ctx)
		next.ServeHTTP(recorder, request)
		h.logger.Info("http request",
			"request_id", requestID,
			"method", r.Method,
			"route", request.Pattern,
			"status", recorder.status,
			"latency_ms", time.Since(started).Milliseconds(),
		)
	})
}

func normalizedStreamConfig(config StreamConfig) StreamConfig {
	if config.PollInterval <= 0 {
		config.PollInterval = defaultStreamPoll
	}
	if config.KeepaliveInterval <= 0 {
		config.KeepaliveInterval = defaultStreamKeepalive
	}
	if config.MaxLifetime <= 0 {
		config.MaxLifetime = defaultStreamLifetime
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = defaultStreamWrite
	}
	return config
}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	if requestID == "" {
		return "req_unavailable"
	}
	return requestID
}

func newRequestID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate request ID: %w", err)
	}
	return "req_" + hex.EncodeToString(random[:]), nil
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusRecorder) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(payload)
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	slog.Info("nvokend listening", "addr", s.http.Addr)
	errChan := make(chan error, 1)
	go func() { errChan <- s.http.ListenAndServe() }()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
	}
	if s.cancelStreams != nil {
		s.cancelStreams()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	shutdownErr := s.http.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		// Shutdown stops listeners immediately but can time out waiting for an
		// active handler. Force-close remaining connections so Server.Run itself
		// still joins within the process supervisor's larger total budget.
		closeErr := s.http.Close()
		listenErr := <-errChan
		if errors.Is(listenErr, http.ErrServerClosed) {
			listenErr = nil
		}
		return errors.Join(shutdownErr, closeErr, listenErr)
	}
	if err := <-errChan; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
