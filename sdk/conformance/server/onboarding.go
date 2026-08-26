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
	mu                 sync.Mutex
	next               int
	conversationsByID  map[string]*onboardingConversation
	conversationsByKey map[string]*onboardingConversation
	turns              map[string]map[string]any
	turnMessages       map[string][]map[string]any
	idempotencyAck     map[string]map[string]any
}

type onboardingConversation struct {
	id        string
	key       string
	tenantKey string
	owner     map[string]any
	messages  []map[string]any
	facts     map[string]string
}

type onboardingCreateRequest struct {
	TenantKey      string `json:"tenant_key"`
	UserKey        string `json:"user_key"`
	IdempotencyKey string `json:"idempotency_key"`
	Behavior       struct {
		Kind     string         `json:"kind"`
		Agent    map[string]any `json:"agent"`
		Behavior map[string]any `json:"behavior"`
	} `json:"behavior"`
	Conversation *struct {
		Mode            string         `json:"mode"`
		ConversationID  string         `json:"conversation_id"`
		ConversationKey string         `json:"conversation_key"`
		Owner           map[string]any `json:"owner"`
	} `json:"conversation"`
	Input json.RawMessage `json:"input"`
}

func newOnboardingState() *onboardingState {
	return &onboardingState{
		conversationsByID:  map[string]*onboardingConversation{},
		conversationsByKey: map[string]*onboardingConversation{},
		turns:              map[string]map[string]any{},
		turnMessages:       map[string][]map[string]any{},
		idempotencyAck:     map[string]map[string]any{},
	}
}

func (s *onboardingState) serve(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer test-key" {
		writeError(response, http.StatusUnauthorized, "unauthenticated", "invalid App key")
		return
	}
	switch {
	case serveAgents(response, request):
	case serveModels(response, request):
	case request.URL.Path == "/v1/turns" && request.Method == http.MethodPost:
		s.create(response, request)
	case strings.HasPrefix(request.URL.Path, "/v1/turns/") && strings.HasSuffix(request.URL.Path, "/result") && request.Method == http.MethodGet:
		s.getTurnResult(response, strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/turns/"), "/result"))
	case strings.HasPrefix(request.URL.Path, "/v1/turns/") && strings.HasSuffix(request.URL.Path, "/stream") && request.Method == http.MethodGet:
		s.streamTurn(response, strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/turns/"), "/stream"))
	case strings.HasPrefix(request.URL.Path, "/v1/turns/") && request.Method == http.MethodGet:
		s.getTurn(response, strings.TrimPrefix(request.URL.Path, "/v1/turns/"))
	case strings.HasSuffix(request.URL.Path, "/messages") && request.Method == http.MethodGet:
		s.listMessages(response, request)
	case strings.HasPrefix(request.URL.Path, "/v1/conversations/") && request.Method == http.MethodGet:
		s.getConversation(response, request)
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
	text, ok := onboardingInputText(input.Input)
	validBehavior := input.Behavior.Kind == "agent" && len(input.Behavior.Agent) > 0 ||
		input.Behavior.Kind == "inline" && len(input.Behavior.Behavior) > 0
	if input.TenantKey == "" || input.IdempotencyKey == "" || !validBehavior || !ok {
		writeError(response, http.StatusBadRequest, "invalid_request", "missing onboarding request fields")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.idempotencyAck[input.IdempotencyKey]; ok {
		replay := cloneMap(prior)
		replay["deduplicated"] = true
		s.writeAdmission(response, request, replay)
		return
	}
	conversation, ok := s.resolveConversation(input)
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Conversation was not found")
		return
	}

	s.next++
	turnID := onboardingID("turn", s.next)
	createdAt := time.Date(2026, 7, 22, 12, 0, s.next%60, 0, time.UTC)
	conversationID := ""
	sequence := 1
	if conversation != nil {
		conversationID = conversation.id
		sequence = len(conversation.messages) + 1
	}
	messages := []map[string]any{onboardingMessage(
		conversationID,
		turnID,
		input.UserKey,
		sequence,
		"user",
		text,
		createdAt,
	)}

	status := "completed"
	var failure any
	if onboardingModelID(input) == "invalid-model" {
		status = "failed"
		failure = map[string]any{
			"code":    "provider_error",
			"message": "The provider rejected the requested model.",
			"details": map[string]any{"classification": "upstream_rejected"},
		}
	} else {
		answer := onboardingAnswer(conversation, text)
		messages = append(messages, onboardingMessage(
			conversationID,
			turnID,
			input.UserKey,
			sequence+1,
			"assistant",
			answer,
			createdAt.Add(time.Second),
		))
	}
	if conversation != nil {
		conversation.messages = append(conversation.messages, messages...)
	}
	s.turnMessages[turnID] = messages
	s.turns[turnID] = onboardingTurn(
		turnID,
		input,
		conversationID,
		status,
		failure,
		createdAt,
	)
	ack := onboardingTurn(turnID, input, conversationID, "queued", nil, createdAt)
	ack["deduplicated"] = false
	s.idempotencyAck[input.IdempotencyKey] = cloneMap(ack)
	s.writeAdmission(response, request, ack)
}

func (s *onboardingState) writeAdmission(
	response http.ResponseWriter,
	_ *http.Request,
	ack map[string]any,
) {
	// Admission is a plain JSON POST and nothing else. `Accept:
	// text/event-stream` on it used to select an inline stream; that entry
	// point is gone, so the acknowledgement is the whole response.
	writeJSON(response, http.StatusAccepted, ack)
}

// turnStatus reads the settled status off a composed result, which is
// what the change frame above reports.
func turnStatus(result map[string]any) any {
	turn, _ := result["turn"].(map[string]any)
	return turn["status"]
}

// turnStatusName is turnStatus narrowed to the string the fixtures
// always store, for callers that have to classify it rather than echo it.
func turnStatusName(result map[string]any) string {
	name, _ := turnStatus(result).(string)
	return name
}

func onboardingInputText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil && text != "" {
		return text, true
	}
	var blocks struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) != nil || len(blocks.Content) == 0 {
		return "", false
	}
	return blocks.Content[0].Text, blocks.Content[0].Type == "text" && blocks.Content[0].Text != ""
}

func onboardingModelID(input onboardingCreateRequest) string {
	behavior := input.Behavior.Behavior
	model, _ := behavior["model"].(map[string]any)
	value, _ := model["id"].(string)
	return value
}

func (s *onboardingState) resolveConversation(input onboardingCreateRequest) (*onboardingConversation, bool) {
	if input.Conversation == nil {
		return nil, true
	}
	if input.Conversation.Mode == "continue" {
		conversation, ok := s.conversationsByID[input.Conversation.ConversationID]
		return conversation, ok
	}
	if input.Conversation.Mode == "new" {
		conversation := &onboardingConversation{
			id:        onboardingID("conv", s.next+1),
			key:       input.Conversation.ConversationKey,
			tenantKey: input.TenantKey,
			owner:     input.Conversation.Owner,
			facts:     map[string]string{},
		}
		s.conversationsByID[conversation.id] = conversation
		if conversation.key != "" {
			s.conversationsByKey[conversation.key] = conversation
		}
		return conversation, true
	}
	if input.Conversation.Mode != "continue_or_create" || input.Conversation.ConversationKey == "" {
		return nil, false
	}
	if conversation, ok := s.conversationsByKey[input.Conversation.ConversationKey]; ok {
		return conversation, true
	}
	conversation := &onboardingConversation{
		id:        onboardingID("conv", s.next+1),
		key:       input.Conversation.ConversationKey,
		tenantKey: input.TenantKey,
		owner:     input.Conversation.Owner,
		facts:     map[string]string{},
	}
	s.conversationsByID[conversation.id] = conversation
	s.conversationsByKey[conversation.key] = conversation
	return conversation, true
}

func (s *onboardingState) getTurn(response http.ResponseWriter, turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.turns[turnID]
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Turn was not found")
		return
	}
	writeJSON(response, http.StatusOK, turn)
}

func (s *onboardingState) getTurnResult(response http.ResponseWriter, turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.turnResult(turnID)
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Turn was not found")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

// streamTurn is the route an onboarding run actually consumes. The SDK
// admits with a plain POST and then reads the Turn stream, so serving SSE
// on the acknowledgement alone left every newcomer path reading a 404 off the
// route it opens next. The turn is already settled by the time it is admitted
// here, so one result frame and the terminal end is the whole stream.
func (s *onboardingState) streamTurn(response http.ResponseWriter, turnID string) {
	s.mu.Lock()
	result, ok := s.turnResult(turnID)
	var conversationID any
	if ok {
		conversationID = s.turns[turnID]["conversation_id"]
	}
	s.mu.Unlock()
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Turn was not found")
		return
	}
	cursor := "onboarding-" + turnID
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("X-Request-ID", "req_019b0a12-8d51-7f34-aed2-0e07c1bdb329")
	response.WriteHeader(http.StatusOK)
	writeSSERetry(response)
	// A turn is over when a change for it carries a terminal status, and a
	// filtered stream closes right behind it without announcing anything.
	messages, _ := result["messages"].([]any)
	writeSSE(response, cursor, "transcript.update", map[string]any{
		"type":               "transcript.update",
		"conversation_id":    conversationID,
		"content_expires_at": nil,
		"messages":           messages,
		"turn_changes": []any{map[string]any{
			"turn_id":                  turnID,
			"conversation_id":          conversationID,
			"content_expires_at":       nil,
			"revision":                 1,
			"status":                   turnStatus(result),
			"terminal":                 terminalStatus(turnStatusName(result)),
			"through_message_sequence": nil,
			"error":                    nil,
			"structured_output":        nil,
			"occurred_at":              "2026-07-21T12:00:03Z",
		}},
		"cursor": cursor,
	})
	if flusher, ok := response.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *onboardingState) turnResult(turnID string) (map[string]any, bool) {
	turn, ok := s.turns[turnID]
	if !ok {
		return nil, false
	}
	messages := []any{}
	var text strings.Builder
	found := false
	for _, message := range s.turnMessages[turnID] {
		messages = append(messages, message)
		if message["role"] != "assistant" || message["phase"] != "final_answer" {
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
	if turn["status"] == "completed" && found {
		outputText = text.String()
	}
	return map[string]any{
		"turn":        turn,
		"messages":    messages,
		"output_text": outputText,
	}, true
}

func (s *onboardingState) listMessages(response http.ResponseWriter, request *http.Request) {
	conversationID := strings.TrimSuffix(
		strings.TrimPrefix(request.URL.Path, "/v1/conversations/"),
		"/messages",
	)
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, ok := s.conversationsByID[conversationID]
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Conversation was not found")
		return
	}
	items := make([]any, len(conversation.messages))
	for index, message := range conversation.messages {
		items[index] = message
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"items":       items,
		"has_more":    false,
		"next_cursor": nil,
	})
}

func (s *onboardingState) getConversation(response http.ResponseWriter, request *http.Request) {
	conversationID := strings.TrimPrefix(request.URL.Path, "/v1/conversations/")
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, ok := s.conversationsByID[conversationID]
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Conversation was not found")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"id":                 conversation.id,
		"tenant_key":         conversation.tenantKey,
		"owner":              conversation.owner,
		"conversation_key":   nullable(conversation.key != "", conversation.key),
		"forked_from":        nil,
		"active_turn_id":     nil,
		"active_turn_status": nil,
		"compaction":         nil,
		"retention":          nil,
		"expires_at":         nil,
		"metadata":           nil,
		"created_at":         "2026-07-22T12:00:00Z",
		"updated_at":         "2026-07-22T12:00:01Z",
	})
}

func onboardingMessage(
	conversationID string,
	turnID string,
	userKey string,
	sequence int,
	role string,
	text string,
	createdAt time.Time,
) map[string]any {
	message := map[string]any{
		"id":                 onboardingID("msg", sequence),
		"conversation_id":    nullable(conversationID != "", conversationID),
		"content_expires_at": nil,
		"agent_id":           agentID,
		"turn_id":            turnID,
		"user_key":           nullable(userKey != "", userKey),
		"sequence":           sequence,
		"role":               role,
		"content":            []any{map[string]any{"type": "text", "text": text}},
		"created_at":         createdAt.Format(time.RFC3339),
	}
	if role == "assistant" {
		message["phase"] = "final_answer"
	}
	return message
}

func onboardingTurn(
	id string,
	input onboardingCreateRequest,
	conversationID string,
	status string,
	failure any,
	createdAt time.Time,
) map[string]any {
	digest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	behaviorSource := map[string]any{"kind": "inline", "digest": digest}
	if input.Behavior.Kind == "agent" {
		behaviorSource = map[string]any{
			"kind": "agent_revision", "agent_id": agentID,
			"agent_revision_id": agentRevisionID, "revision": 1,
		}
	}
	return map[string]any{
		"id":                           id,
		"tenant_key":                   input.TenantKey,
		"user_key":                     nullable(input.UserKey != "", input.UserKey),
		"behavior_source":              behaviorSource,
		"effective_behavior":           nil,
		"effective_behavior_digest":    digest,
		"conversation_id":              nullable(conversationID != "", conversationID),
		"memory_space_id":              nil,
		"memory":                       nil,
		"content_expires_at":           nil,
		"triggered_by":                 nil,
		"context":                      nil,
		"status":                       status,
		"stop_reason":                  nullableStopReason(status),
		"attempt":                      1,
		"error":                        failure,
		"usage":                        nil,
		"provenance":                   nil,
		"structured_output":            nil,
		"structured_output_provenance": nil,
		"metadata":                     nil,
		"limits":                       map[string]any{"total_timeout_seconds": 300, "active_timeout_seconds": 120, "waiting_timeout_seconds": 180, "max_output_tokens": 300, "max_iterations": 1},
		"active_execution_ms":          10,
		"deadline_at":                  createdAt.Add(5 * time.Minute).Format(time.RFC3339),
		"created_at":                   createdAt.Format(time.RFC3339),
		"updated_at":                   createdAt.Add(time.Second).Format(time.RFC3339),
		"ended_at":                     nullable(status != "queued" && status != "running" && status != "waiting", createdAt.Add(time.Second).Format(time.RFC3339)),
		"tool_calls":                   []any{},
	}
}

func onboardingAnswer(conversation *onboardingConversation, input string) string {
	lower := strings.ToLower(input)
	switch {
	case strings.Contains(lower, "remember") && strings.Contains(lower, "code word") && strings.Contains(lower, "cedar"):
		if conversation != nil {
			conversation.facts["code_word"] = "cedar"
		}
		return "Understood—your code word is cedar."
	case strings.Contains(lower, "what is my code word"):
		if conversation != nil && conversation.facts["code_word"] != "" {
			value := conversation.facts["code_word"]
			return value
		}
	case strings.Contains(lower, "remember") && strings.Contains(lower, "launch city") && strings.Contains(lower, "lisbon"):
		if conversation != nil {
			conversation.facts["launch_city"] = "Lisbon"
		}
		return "Understood—your launch city is Lisbon."
	case strings.Contains(lower, "launch city"):
		if conversation != nil && conversation.facts["launch_city"] != "" {
			value := conversation.facts["launch_city"]
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
