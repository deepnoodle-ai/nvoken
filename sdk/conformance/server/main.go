package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

const (
	agentID      = "agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320"
	sessionID    = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
	invocationID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
	waitID       = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328"
	toolCallID   = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
)

type state struct {
	mu                sync.Mutex
	admissionAttempts int
	resultAttempts    int
	cancelAttempts    int
	rateLimitAttempts int
	streamAttempts    int
	lastEventID       string
	onboarding        *onboardingState
}

func main() {
	address := os.Getenv("NVOKEN_CONFORMANCE_ADDR")
	if address == "" {
		address = "127.0.0.1:43109"
	}
	testState := &state{}
	if os.Getenv("NVOKEN_CONFORMANCE_ONBOARDING") == "1" {
		testState.onboarding = newOnboardingState()
	}
	server := &http.Server{
		Addr:    address,
		Handler: testState,
	}
	log.Printf("nvoken SDK conformance server listening on %s", address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func (s *state) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/healthz" {
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if request.URL.Path == "/__test/reset" && request.Method == http.MethodPost {
		s.mu.Lock()
		s.admissionAttempts = 0
		s.resultAttempts = 0
		s.cancelAttempts = 0
		s.rateLimitAttempts = 0
		s.streamAttempts = 0
		s.lastEventID = ""
		s.mu.Unlock()
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if request.URL.Path == "/__test/state" {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{
			"admission_attempts": s.admissionAttempts,
			"result_attempts":    s.resultAttempts,
			"cancel_attempts":    s.cancelAttempts,
			"stream_attempts":    s.streamAttempts,
			"last_event_id":      s.lastEventID,
		})
		return
	}
	if s.onboarding != nil {
		s.onboarding.serve(response, request)
		return
	}

	switch {
	case request.URL.Path == "/v1/model-pricing-capabilities" && request.Method == http.MethodGet:
		writeJSON(response, http.StatusOK, map[string]any{
			"provider": request.URL.Query().Get("provider"), "model": request.URL.Query().Get("model"),
			"status": "priced", "registry_version": "conformance-v1",
		})
	case request.URL.Path == "/v1/invocations" && request.Method == http.MethodPost:
		s.createInvocation(response, request)
	case request.URL.Path == "/v1/invocations" && request.Method == http.MethodGet:
		s.listInvocations(response, request)
	case strings.HasPrefix(request.URL.Path, "/v1/invocations/"):
		s.invocation(response, request)
	case request.URL.Path == "/v1/sessions" && request.Method == http.MethodGet:
		s.listSessions(response, request)
	case strings.HasPrefix(request.URL.Path, "/v1/sessions/"):
		s.session(response, request)
	default:
		writeError(response, http.StatusNotFound, "not_found", "unknown conformance route")
	}
}

func (s *state) createInvocation(response http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	s.admissionAttempts++
	attempt := s.admissionAttempts
	s.mu.Unlock()
	if attempt == 1 && disconnect(response) {
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{
		"agent_id":      agentID,
		"session_id":    sessionID,
		"invocation_id": invocationID,
		"status":        "queued",
		"deduplicated":  attempt > 1,
	})
}

func (s *state) listInvocations(response http.ResponseWriter, request *http.Request) {
	cursor := request.URL.Query().Get("cursor")
	writeJSON(response, http.StatusOK, map[string]any{
		"items":       []any{invocation("completed")},
		"has_more":    cursor == "",
		"next_cursor": nullable(cursor == "", "invocations-page-2"),
	})
}

func (s *state) invocation(response http.ResponseWriter, request *http.Request) {
	remainder := strings.TrimPrefix(request.URL.Path, "/v1/invocations/")
	if strings.HasSuffix(remainder, "/tool-results") && request.Method == http.MethodPost {
		s.mu.Lock()
		s.resultAttempts++
		attempt := s.resultAttempts
		s.mu.Unlock()
		if attempt == 1 && disconnect(response) {
			return
		}
		writeJSON(response, http.StatusAccepted, map[string]any{
			"invocation_id": invocationID,
			"session_id":    sessionID,
			"status":        "queued",
			"results": []any{map[string]any{
				"tool_call_id": toolCallID,
				"status":       "completed",
				"deduplicated": attempt > 1,
			}},
			"pending_tool_calls": []any{},
		})
		return
	}
	if strings.HasSuffix(remainder, "/cancel") && request.Method == http.MethodPost {
		s.mu.Lock()
		s.cancelAttempts++
		s.mu.Unlock()
		writeJSON(response, http.StatusOK, invocation("cancelled"))
		return
	}
	if request.Method != http.MethodGet {
		writeError(response, http.StatusMethodNotAllowed, "invalid_request", "method not allowed")
		return
	}
	switch remainder {
	case "conflict":
		writeError(response, http.StatusConflict, "idempotency_conflict", "request conflicts with durable state")
	case "rate-limit":
		s.mu.Lock()
		s.rateLimitAttempts++
		attempt := s.rateLimitAttempts
		s.mu.Unlock()
		if attempt == 1 {
			response.Header().Set("Retry-After", "1")
			writeError(response, http.StatusTooManyRequests, "rate_limited", "slow down")
			return
		}
		writeJSON(response, http.StatusOK, invocation("completed"))
	case "rate-limit-always":
		response.Header().Set("Retry-After", "1")
		writeError(response, http.StatusTooManyRequests, "rate_limited", "slow down")
	case "server-error":
		writeError(response, http.StatusServiceUnavailable, "unavailable", "try later")
	case waitID:
		writeJSON(response, http.StatusOK, invocationWithID(waitID, "running"))
	default:
		writeJSON(response, http.StatusOK, invocation("completed"))
	}
}

func (s *state) listSessions(response http.ResponseWriter, request *http.Request) {
	cursor := request.URL.Query().Get("cursor")
	writeJSON(response, http.StatusOK, map[string]any{
		"items":       []any{session()},
		"has_more":    cursor == "",
		"next_cursor": nullable(cursor == "", "sessions-page-2"),
	})
}

func (s *state) session(response http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/v1/sessions/")
	switch {
	case strings.HasSuffix(path, "/transcript/stream") && request.Method == http.MethodGet:
		s.stream(response, request)
	case strings.HasSuffix(path, "/transcript") && request.Method == http.MethodGet:
		writeJSON(response, http.StatusOK, secondSnapshot())
	case strings.HasSuffix(path, "/messages") && request.Method == http.MethodGet:
		cursor := request.URL.Query().Get("cursor")
		items := []any{firstMessage()}
		if cursor != "" {
			items = []any{secondMessage()}
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"items":       items,
			"has_more":    cursor == "",
			"next_cursor": nullable(cursor == "", "messages-page-2"),
		})
	case request.Method == http.MethodGet:
		writeJSON(response, http.StatusOK, session())
	default:
		writeError(response, http.StatusMethodNotAllowed, "invalid_request", "method not allowed")
	}
}

func (s *state) stream(response http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	s.streamAttempts++
	attempt := s.streamAttempts
	s.lastEventID = request.Header.Get("Last-Event-ID")
	s.mu.Unlock()
	response.Header().Set("Content-Type", "text/event-stream")
	response.WriteHeader(http.StatusOK)
	flusher, ok := response.(http.Flusher)
	if !ok {
		return
	}
	if attempt == 1 {
		_, _ = fmt.Fprint(response, "retry: 1\n")
		writeSSE(response, "cursor-1", "transcript.snapshot", firstSnapshot())
		flusher.Flush()
		return
	}
	writeSSE(response, "cursor-1", "transcript.snapshot", firstSnapshot())
	if attempt == 2 {
		writeSSE(response, "", "stream.end", map[string]any{
			"event_type":    "stream.end",
			"session_id":    sessionID,
			"reason":        "rotate",
			"resume_cursor": "cursor-1",
		})
		flusher.Flush()
		return
	}
	writeSSE(response, "cursor-2", "transcript.snapshot", secondSnapshot())
	writeSSE(response, "", "stream.end", map[string]any{
		"event_type":    "stream.end",
		"session_id":    sessionID,
		"reason":        "terminal",
		"resume_cursor": "cursor-2",
	})
	flusher.Flush()
}

func disconnect(response http.ResponseWriter) bool {
	hijacker, ok := response.(http.Hijacker)
	if !ok {
		return false
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func invocation(status string) map[string]any {
	return invocationWithID(invocationID, status)
}

func invocationWithID(id string, status string) map[string]any {
	completedAt := any(nil)
	if status == "completed" || status == "cancelled" || status == "failed" {
		completedAt = "2026-07-21T12:00:03Z"
	}
	return map[string]any{
		"id":                     id,
		"agent_id":               agentID,
		"session_id":             sessionID,
		"status":                 status,
		"error":                  nil,
		"usage":                  nil,
		"provenance":             nil,
		"output":                 nil,
		"output_provenance":      nil,
		"budgets":                map[string]any{"wall_clock_timeout_seconds": 300, "active_execution_timeout_seconds": 120, "max_iterations": 16},
		"active_execution_ms":    250,
		"wall_clock_deadline_at": "2026-07-21T12:05:00Z",
		"created_at":             "2026-07-21T12:00:00Z",
		"updated_at":             "2026-07-21T12:00:03Z",
		"completed_at":           completedAt,
	}
}

func session() map[string]any {
	return map[string]any{
		"id":                       sessionID,
		"agent_id":                 agentID,
		"tenant_ref":               "acme",
		"session_key":              "ticket-A-42",
		"active_invocation_id":     nil,
		"active_invocation_status": nil,
		"created_at":               "2026-07-21T12:00:00Z",
		"updated_at":               "2026-07-21T12:00:03Z",
	}
}

func firstMessage() map[string]any {
	return map[string]any{
		"id":            "smsg_019b0a12-8d51-7f34-aed2-0e07c1bdb323",
		"session_id":    sessionID,
		"agent_id":      agentID,
		"invocation_id": invocationID,
		"sequence":      1,
		"role":          "user",
		"content":       []any{map[string]any{"type": "text", "text": "hello"}},
		"created_at":    "2026-07-21T12:00:00Z",
	}
}

func secondMessage() map[string]any {
	return map[string]any{
		"id":            "smsg_019b0a12-8d51-7f34-aed2-0e07c1bdb324",
		"session_id":    sessionID,
		"agent_id":      agentID,
		"invocation_id": invocationID,
		"sequence":      2,
		"role":          "assistant",
		"content":       []any{map[string]any{"type": "text", "text": "world"}},
		"created_at":    "2026-07-21T12:00:02Z",
	}
}

func firstChange() map[string]any {
	return change(1, "running", 1, "2026-07-21T12:00:01Z")
}

func secondChange() map[string]any {
	return change(2, "completed", 2, "2026-07-21T12:00:03Z")
}

func change(revision int, status string, sequence int, occurredAt string) map[string]any {
	return map[string]any{
		"invocation_id":            invocationID,
		"revision":                 revision,
		"status":                   status,
		"through_message_sequence": sequence,
		"error":                    nil,
		"usage":                    nil,
		"provenance":               nil,
		"output":                   nil,
		"output_provenance":        nil,
		"occurred_at":              occurredAt,
	}
}

func firstSnapshot() map[string]any {
	return map[string]any{
		"messages":           []any{firstMessage()},
		"invocation_changes": []any{firstChange()},
		"has_more":           false,
		"resume_cursor":      "cursor-1",
		"next_page_token":    nil,
	}
}

func secondSnapshot() map[string]any {
	return map[string]any{
		"messages":           []any{firstMessage(), secondMessage()},
		"invocation_changes": []any{firstChange(), secondChange()},
		"has_more":           false,
		"resume_cursor":      "cursor-2",
		"next_page_token":    nil,
	}
}

func nullable(condition bool, value string) any {
	if condition {
		return value
	}
	return nil
}

func writeSSE(response http.ResponseWriter, id string, event string, value any) {
	encoded, _ := json.Marshal(value)
	if id != "" {
		_, _ = fmt.Fprintf(response, "id: %s\n", id)
	}
	_, _ = fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event, encoded)
}

func writeError(response http.ResponseWriter, status int, code string, message string) {
	writeJSON(response, status, map[string]any{
		"code":       code,
		"message":    message,
		"request_id": "req_019b0a12-8d51-7f34-aed2-0e07c1bdb329",
		"details":    map[string]any{"safe": true},
	})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("X-Request-ID", "req_019b0a12-8d51-7f34-aed2-0e07c1bdb329")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

var _ net.Conn
