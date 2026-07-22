package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type onboardingState struct {
	mu             sync.Mutex
	next           int
	sessionsByID   map[string]*onboardingSession
	sessionsByKey  map[string]*onboardingSession
	invocations    map[string]map[string]any
	idempotencyAck map[string]map[string]any
}

type onboardingSession struct {
	id       string
	key      string
	agentID  string
	messages []map[string]any
	facts    map[string]string
}

type onboardingCreateRequest struct {
	AgentRef       string  `json:"agent_ref"`
	SessionID      *string `json:"session_id"`
	SessionKey     *string `json:"session_key"`
	IdempotencyKey string  `json:"idempotency_key"`
	Input          struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"input"`
	Spec struct {
		Model struct {
			Provider string `json:"provider"`
			Name     string `json:"name"`
		} `json:"model"`
	} `json:"spec"`
}

func newOnboardingState() *onboardingState {
	return &onboardingState{
		sessionsByID:   map[string]*onboardingSession{},
		sessionsByKey:  map[string]*onboardingSession{},
		invocations:    map[string]map[string]any{},
		idempotencyAck: map[string]map[string]any{},
	}
}

func (s *onboardingState) serve(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer test-key" {
		writeError(response, http.StatusUnauthorized, "unauthenticated", "invalid Runtime credential")
		return
	}
	switch {
	case request.URL.Path == "/v1/model-pricing-capabilities" && request.Method == http.MethodGet:
		writeJSON(response, http.StatusOK, map[string]any{
			"provider": request.URL.Query().Get("provider"), "model": request.URL.Query().Get("model"),
			"status": "priced", "registry_version": "conformance-v1",
		})
	case request.URL.Path == "/v1/invocations" && request.Method == http.MethodPost:
		s.create(response, request)
	case strings.HasPrefix(request.URL.Path, "/v1/invocations/") && strings.HasSuffix(request.URL.Path, "/result") && request.Method == http.MethodGet:
		s.getInvocationResult(response, strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/invocations/"), "/result"))
	case strings.HasPrefix(request.URL.Path, "/v1/invocations/") && request.Method == http.MethodGet:
		s.getInvocation(response, strings.TrimPrefix(request.URL.Path, "/v1/invocations/"))
	case strings.HasSuffix(request.URL.Path, "/messages") && request.Method == http.MethodGet:
		s.listMessages(response, request)
	case strings.HasPrefix(request.URL.Path, "/v1/sessions/") && request.Method == http.MethodGet:
		s.getSession(response, request)
	default:
		writeError(response, http.StatusNotFound, "not_found", "unknown onboarding route")
	}
}

func (s *onboardingState) create(response http.ResponseWriter, request *http.Request) {
	var input onboardingCreateRequest
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if input.IdempotencyKey == "" || len(input.Input.Content) == 0 {
		writeError(response, http.StatusBadRequest, "invalid_request", "missing onboarding request fields")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.idempotencyAck[input.IdempotencyKey]; ok {
		replay := cloneMap(prior)
		replay["deduplicated"] = true
		writeJSON(response, http.StatusAccepted, replay)
		return
	}
	session, ok := s.resolveSession(input)
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Session was not found")
		return
	}

	s.next++
	invocationID := onboardingID("invk", s.next)
	createdAt := time.Date(2026, 7, 22, 12, 0, s.next%60, 0, time.UTC)
	text := input.Input.Content[0].Text
	session.messages = append(session.messages, onboardingMessage(
		session,
		invocationID,
		"user",
		text,
		createdAt,
	))

	status := "completed"
	var failure any
	if input.Spec.Model.Name == "invalid-model" {
		status = "failed"
		failure = map[string]any{
			"code":    "provider_error",
			"message": "The provider rejected the requested model.",
			"details": map[string]any{"classification": "upstream_rejected"},
		}
	} else {
		answer := onboardingAnswer(session, text)
		session.messages = append(session.messages, onboardingMessage(
			session,
			invocationID,
			"assistant",
			answer,
			createdAt.Add(time.Second),
		))
	}
	s.invocations[invocationID] = onboardingInvocation(
		invocationID,
		session,
		status,
		failure,
		createdAt,
	)
	ack := map[string]any{
		"agent_id":      session.agentID,
		"session_id":    session.id,
		"invocation_id": invocationID,
		"status":        "queued",
		"deduplicated":  false,
	}
	s.idempotencyAck[input.IdempotencyKey] = cloneMap(ack)
	writeJSON(response, http.StatusAccepted, ack)
}

func (s *onboardingState) resolveSession(input onboardingCreateRequest) (*onboardingSession, bool) {
	if input.SessionID != nil {
		session, ok := s.sessionsByID[*input.SessionID]
		return session, ok
	}
	if input.SessionKey == nil || *input.SessionKey == "" {
		return nil, false
	}
	if session, ok := s.sessionsByKey[*input.SessionKey]; ok {
		return session, true
	}
	session := &onboardingSession{
		id:      onboardingID("sesn", s.next+1),
		key:     *input.SessionKey,
		agentID: onboardingID("agnt", s.next+1),
		facts:   map[string]string{},
	}
	s.sessionsByID[session.id] = session
	s.sessionsByKey[session.key] = session
	return session, true
}

func (s *onboardingState) getInvocation(response http.ResponseWriter, invocationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	invocation, ok := s.invocations[invocationID]
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Invocation was not found")
		return
	}
	writeJSON(response, http.StatusOK, invocation)
}

func (s *onboardingState) getInvocationResult(response http.ResponseWriter, invocationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	invocation, ok := s.invocations[invocationID]
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Invocation was not found")
		return
	}
	session, ok := s.sessionsByID[invocation["session_id"].(string)]
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Session was not found")
		return
	}
	messages := []any{}
	var text strings.Builder
	found := false
	for _, message := range session.messages {
		if message["invocation_id"] != invocationID {
			continue
		}
		messages = append(messages, message)
		if message["role"] != "assistant" {
			continue
		}
		for _, block := range message["content"].([]any) {
			entry, ok := block.(map[string]any)
			if !ok || entry["type"] != "text" {
				continue
			}
			if value, ok := entry["text"].(string); ok {
				found = true
				text.WriteString(value)
			}
		}
	}
	var outputText any
	if invocation["status"] == "completed" && found {
		outputText = text.String()
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"invocation":  invocation,
		"messages":    messages,
		"output_text": outputText,
	})
}

func (s *onboardingState) listMessages(response http.ResponseWriter, request *http.Request) {
	sessionID := strings.TrimSuffix(
		strings.TrimPrefix(request.URL.Path, "/v1/sessions/"),
		"/messages",
	)
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessionsByID[sessionID]
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Session was not found")
		return
	}
	items := make([]any, len(session.messages))
	for index, message := range session.messages {
		items[index] = message
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"items":       items,
		"has_more":    false,
		"next_cursor": nil,
	})
}

func (s *onboardingState) getSession(response http.ResponseWriter, request *http.Request) {
	sessionID := strings.TrimPrefix(request.URL.Path, "/v1/sessions/")
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessionsByID[sessionID]
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Session was not found")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"id":                       session.id,
		"agent_id":                 session.agentID,
		"tenant_ref":               nil,
		"session_key":              session.key,
		"active_invocation_id":     nil,
		"active_invocation_status": nil,
		"created_at":               "2026-07-22T12:00:00Z",
		"updated_at":               "2026-07-22T12:00:01Z",
	})
}

func onboardingMessage(
	session *onboardingSession,
	invocationID string,
	role string,
	text string,
	createdAt time.Time,
) map[string]any {
	sequence := len(session.messages) + 1
	return map[string]any{
		"id":            onboardingID("smsg", sequence),
		"session_id":    session.id,
		"agent_id":      session.agentID,
		"invocation_id": invocationID,
		"sequence":      sequence,
		"role":          role,
		"content":       []any{map[string]any{"type": "text", "text": text}},
		"created_at":    createdAt.Format(time.RFC3339),
	}
}

func onboardingInvocation(
	id string,
	session *onboardingSession,
	status string,
	failure any,
	createdAt time.Time,
) map[string]any {
	return map[string]any{
		"id":                           id,
		"agent_id":                     session.agentID,
		"session_id":                   session.id,
		"status":                       status,
		"error":                        failure,
		"usage":                        nil,
		"provenance":                   nil,
		"structured_output":            nil,
		"structured_output_provenance": nil,
		"budgets":                      map[string]any{"wall_clock_timeout_seconds": 300, "active_execution_timeout_seconds": 120, "max_output_tokens": 300, "max_iterations": 1},
		"active_execution_ms":          10,
		"wall_clock_deadline_at":       createdAt.Add(5 * time.Minute).Format(time.RFC3339),
		"created_at":                   createdAt.Format(time.RFC3339),
		"updated_at":                   createdAt.Add(time.Second).Format(time.RFC3339),
		"completed_at":                 createdAt.Add(time.Second).Format(time.RFC3339),
	}
}

func onboardingAnswer(session *onboardingSession, input string) string {
	lower := strings.ToLower(input)
	switch {
	case strings.Contains(lower, "remember") && strings.Contains(lower, "code word") && strings.Contains(lower, "cedar"):
		session.facts["code_word"] = "cedar"
		return "Understood—your code word is cedar."
	case strings.Contains(lower, "what is my code word"):
		if value := session.facts["code_word"]; value != "" {
			return value
		}
	case strings.Contains(lower, "remember") && strings.Contains(lower, "launch city") && strings.Contains(lower, "lisbon"):
		session.facts["launch_city"] = "Lisbon"
		return "Understood—your launch city is Lisbon."
	case strings.Contains(lower, "launch city"):
		if value := session.facts["launch_city"]; value != "" {
			return value
		}
	}
	return "world"
}

func onboardingID(prefix string, value int) string {
	return fmt.Sprintf("%s_019b0a12-8d51-7f34-aed2-%012x", prefix, value)
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
