package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

const (
	agentID         = "e1ec4be4-ffd5-7789-83bc-fb8f0f1eb276"
	agentRevisionID = "0ff8916e-8d05-7324-ac7c-487b2bd361c4"
	conversationID  = "db4eaf24-1ac6-776e-8f98-badc6d0dc764"
	turnID          = "33b82f49-6105-75f4-b829-3e5d1f2f3dba"
	waitID          = "10d4c33e-928d-7d54-91bd-f411a1f5c600"
	toolCallID      = "63779fde-08fe-71c9-a953-873cd55651a4"
	exactModelID    = "experimental/model?variant=雪%#1"
	allocationID    = "5b22a5fa-89ea-7100-bac8-fe3b4889da9b"
)

type state struct {
	mu                   sync.Mutex
	admissionAttempts    int
	resultAttempts       int
	cancelAttempts       int
	interruptAttempts    int
	credentialAdmissions int
	rateLimitAttempts    int
	streamAttempts       int
	lastTurnID           string
	lastResumeCursor     string
	lastResumeSource     string
	lastStatuses         []string
	lastDeltas           string
	onboarding           *onboardingState
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
		s.interruptAttempts = 0
		s.credentialAdmissions = 0
		s.rateLimitAttempts = 0
		s.streamAttempts = 0
		s.lastTurnID = ""
		s.lastResumeCursor = ""
		s.lastResumeSource = ""
		s.lastStatuses = nil
		s.lastDeltas = ""
		s.mu.Unlock()
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if request.URL.Path == "/__test/state" {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{
			"admission_attempts":    s.admissionAttempts,
			"result_attempts":       s.resultAttempts,
			"cancel_attempts":       s.cancelAttempts,
			"interrupt_attempts":    s.interruptAttempts,
			"credential_admissions": s.credentialAdmissions,
			"stream_attempts":       s.streamAttempts,
			"last_turn_id":          s.lastTurnID,
			"last_resume_cursor":    s.lastResumeCursor,
			"last_resume_source":    s.lastResumeSource,
			"last_statuses":         s.lastStatuses,
			"last_deltas":           s.lastDeltas,
		})
		return
	}
	if s.onboarding != nil {
		s.onboarding.serve(response, request)
		return
	}

	switch {
	case serveAgents(response, request):
	case serveModels(response, request):
	case serveMCP(response, request):
	case serveCredits(response, request):
	case serveMemorySpaces(response, request):
	case request.URL.Path == "/v1/turns" && request.Method == http.MethodPost:
		s.createTurn(response, request)
	case request.URL.Path == "/v1/turns" && request.Method == http.MethodGet:
		s.listTurns(response, request)
	case strings.HasPrefix(request.URL.Path, "/v1/turns/"):
		s.turn(response, request)
	case request.URL.Path == "/v1/conversations" && request.Method == http.MethodPost:
		s.createConversation(response, request)
	case request.URL.Path == "/v1/conversations" && request.Method == http.MethodGet:
		s.listConversations(response, request)
	case strings.HasPrefix(request.URL.Path, "/v1/conversations/"):
		s.conversation(response, request)
	default:
		writeError(response, http.StatusNotFound, "not_found", "unknown conformance route")
	}
}

func serveCredits(response http.ResponseWriter, request *http.Request) bool {
	if request.URL.Path == "/v1/credits/allocations" && request.Method == http.MethodPost {
		var body struct {
			Amount struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
			} `json:"amount"`
			TenantKey      string `json:"tenant_key"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil ||
			body.Amount.Amount != "25.000000" || body.Amount.Currency != "USD" ||
			body.TenantKey != "acme" || body.IdempotencyKey == "" {
			writeError(response, http.StatusBadRequest, "invalid_request", "Credit allocation did not round-trip")
			return true
		}
		writeJSON(response, http.StatusCreated, map[string]any{
			"account":    creditAccount(),
			"allocation": creditAllocation(),
		})
		return true
	}
	if request.URL.Path == "/v1/credits" && request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, map[string]any{"items": []any{creditAccount()}, "next_cursor": nil})
		return true
	}
	if request.URL.Path == "/v1/credits/allocations" && request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, map[string]any{"items": []any{creditAllocation()}, "next_cursor": nil})
		return true
	}
	return false
}

func money(amount string) map[string]any {
	return map[string]any{"amount": amount, "currency": "USD"}
}

func creditAccount() map[string]any {
	return map[string]any{
		"tenant_key":        nil,
		"allocated":         money("25.000000"),
		"used":              money("3.250000"),
		"held":              money("1.500000"),
		"available":         money("20.250000"),
		"budget_hold_turns": 2,
		"created_at":        "2026-08-01T00:00:00Z",
		"updated_at":        "2026-08-08T12:00:00Z",
	}
}

func creditAllocation() map[string]any {
	return map[string]any{
		"id": allocationID, "tenant_key": nil, "amount": money("25.000000"),
		"reference": nil, "created_by": "cred_conformance", "created_at": "2026-08-01T00:00:00Z",
	}
}

func serveMemorySpaces(response http.ResponseWriter, request *http.Request) bool {
	const memorySpaceID = "6f4aa16c-33a8-7f0f-8eed-08e88bfd526e"
	resource := func() map[string]any {
		return map[string]any{
			"id": memorySpaceID, "tenant_key": "acme", "scope": "tenant",
			"user_key": nil, "namespace": "support-team", "retention_ttl_seconds": nil,
			"expires_at": nil, "erased_at": nil,
			"created_at": "2026-07-21T12:00:00Z", "updated_at": "2026-07-21T12:00:00Z",
		}
	}
	switch request.URL.Path {
	case "/v1/memory-spaces":
		switch request.Method {
		case http.MethodPost:
			var body struct {
				TenantKey string         `json:"tenant_key"`
				Selector  map[string]any `json:"selector"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil ||
				body.TenantKey == "" || body.Selector["scope"] == nil || body.Selector["namespace"] == nil {
				writeError(response, http.StatusBadRequest, "invalid_request", "MemorySpace did not round-trip")
				return true
			}
			value := resource()
			value["tenant_key"] = body.TenantKey
			value["scope"] = body.Selector["scope"]
			value["namespace"] = body.Selector["namespace"]
			value["user_key"] = body.Selector["user_key"]
			writeJSON(response, http.StatusOK, value)
		case http.MethodGet:
			writeJSON(response, http.StatusOK, map[string]any{
				"items": []any{resource()}, "has_more": false, "next_cursor": nil,
			})
		default:
			return false
		}
		return true
	case "/v1/memory-spaces/" + memorySpaceID:
		switch request.Method {
		case http.MethodGet:
			writeJSON(response, http.StatusOK, resource())
		case http.MethodDelete:
			value := resource()
			value["erased_at"] = "2026-07-21T12:05:00Z"
			writeJSON(response, http.StatusOK, value)
		default:
			return false
		}
		return true
	default:
		return false
	}
}

func serveAgents(response http.ResponseWriter, request *http.Request) bool {
	switch request.URL.Path {
	case "/v1/agents":
		if request.Method == http.MethodPost {
			var body struct {
				AgentKey     string         `json:"agent_key"`
				Name         string         `json:"name"`
				Owner        map[string]any `json:"owner"`
				Instructions string         `json:"instructions"`
				Model        map[string]any `json:"model"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil ||
				body.AgentKey == "" || body.Instructions == "" ||
				body.Owner["kind"] == nil || body.Model["id"] == nil {
				writeError(response, http.StatusBadRequest, "invalid_request", "Agent did not round-trip")
				return true
			}
			value := agent()
			value["agent_key"] = body.AgentKey
			value["name"] = nullable(body.Name != "", body.Name)
			if body.Name == "" {
				value["name"] = body.AgentKey
			}
			value["owner"] = body.Owner
			writeJSON(response, http.StatusCreated, value)
			return true
		}
		if request.Method != http.MethodGet {
			return false
		}
		if key := request.URL.Query().Get("agent_key"); key != "" && key != "support" {
			writeJSON(response, http.StatusOK, map[string]any{
				"items":       []any{},
				"has_more":    false,
				"next_cursor": nil,
			})
			return true
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"items":       []any{agent()},
			"has_more":    false,
			"next_cursor": nil,
		})
		return true
	case "/v1/agents/" + agentID:
		switch request.Method {
		case http.MethodGet:
			writeJSON(response, http.StatusOK, agent())
		case http.MethodDelete:
			value := agent()
			value["archived_at"] = "2026-07-21T12:05:00Z"
			writeJSON(response, http.StatusOK, value)
		default:
			return false
		}
		return true
	case "/v1/agents/" + agentID + "/restore":
		if request.Method != http.MethodPost {
			return false
		}
		writeJSON(response, http.StatusOK, agent())
		return true
	case "/v1/agents/" + agentID + "/revisions":
		switch request.Method {
		case http.MethodGet:
			writeJSON(response, http.StatusOK, map[string]any{
				"items": []any{agentRevision()}, "has_more": false, "next_cursor": nil,
			})
		case http.MethodPost:
			var behavior map[string]any
			if err := json.NewDecoder(request.Body).Decode(&behavior); err != nil ||
				behavior["instructions"] == nil || behavior["model"] == nil {
				writeError(response, http.StatusBadRequest, "invalid_request", "Agent revision did not round-trip")
				return true
			}
			value := agentRevision()
			value["behavior"] = behavior
			writeJSON(response, http.StatusCreated, value)
		default:
			return false
		}
		return true
	case "/v1/agents/" + agentID + "/revisions/1":
		if request.Method != http.MethodGet {
			return false
		}
		writeJSON(response, http.StatusOK, agentRevision())
		return true
	default:
		return false
	}
}

func serveMCP(response http.ResponseWriter, request *http.Request) bool {
	if request.URL.Path != "/v1/mcp/list-tools" || request.Method != http.MethodPost {
		return false
	}
	var body struct {
		Server  conformanceMCPServer `json:"server"`
		Headers map[string]string    `json:"headers"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil ||
		!body.Server.valid() ||
		body.Headers["Authorization"] != "Bearer conformance-mcp-secret" {
		writeError(response, http.StatusBadRequest, "invalid_request", "MCP server declaration did not round-trip")
		return true
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"tools": []any{map[string]any{
			"server_name":    "support",
			"remote_name":    "lookup",
			"projected_name": "support__lookup",
			"description":    "Look up a support record.",
			"input_schema": map[string]any{
				"type": "object",
			},
			"annotations": map[string]any{
				"present":          true,
				"read_only_hint":   true,
				"idempotent_hint":  true,
				"destructive_hint": false,
				"open_world_hint":  nil,
			},
		}},
		"exclusions": []any{},
	})
	return true
}

// conformanceMCPServer carries no headers. A reusable Agent revision is
// durable behavior, so secrets arrive separately in mcp_server_headers,
// or in the discovery request.
type conformanceMCPServer struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	AllowedTools []string `json:"allowed_tools"`
	Headers      any      `json:"headers"`
}

func (s conformanceMCPServer) valid() bool {
	return s.Name == "support" &&
		s.URL == "https://mcp.example.test/rpc" &&
		len(s.AllowedTools) == 1 &&
		s.AllowedTools[0] == "lookup" &&
		s.Headers == nil
}

// conformanceMCPServerHeaders is one entry of the per-Turn secret headers.
type conformanceMCPServerHeaders struct {
	Name    string            `json:"name"`
	Headers map[string]string `json:"headers"`
}

func (h conformanceMCPServerHeaders) valid() bool {
	return h.Name == "support" &&
		h.Headers["Authorization"] == "Bearer conformance-mcp-secret"
}

// conformanceContext is one recorded application state snapshot. It travels at
// the top level of the request, so an SDK that nests it inside Agent behavior
// or drops the tier fails here rather than at the Runtime.
type conformanceContext struct {
	Name    string `json:"name"`
	Tier    string `json:"tier"`
	Content string `json:"content"`
}

func (c conformanceContext) valid() bool {
	return c.Name != "" &&
		!strings.HasPrefix(c.Name, "app-") &&
		(c.Tier == "contextual" || c.Tier == "operator") &&
		c.Content != ""
}

func serveModels(response http.ResponseWriter, request *http.Request) bool {
	if request.Method != http.MethodGet {
		return false
	}
	if request.URL.Path == "/v1/models" {
		items := []map[string]any{
			catalogModel("openai", "gpt-test", "GPT Test"),
			catalogModel("future_provider", "future-model", "Future Model"),
		}
		if provider := request.URL.Query().Get("provider"); provider != "" {
			filtered := make([]map[string]any, 0, len(items))
			for _, item := range items {
				if item["provider"] == provider {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}
		response.Header().Set("ETag", `"conformance-catalog-v1"`)
		writeJSON(response, http.StatusOK, map[string]any{
			"items":           items,
			"catalog_version": "conformance-catalog-v1",
		})
		return true
	}
	const prefix = "/v1/models/"
	if !strings.HasPrefix(request.URL.Path, prefix) {
		return false
	}
	escaped := strings.TrimPrefix(request.URL.EscapedPath(), prefix)
	parts := strings.SplitN(escaped, "/", 2)
	if len(parts) != 2 {
		writeError(response, http.StatusNotFound, "not_found", "unknown model route")
		return true
	}
	provider, providerErr := url.PathUnescape(parts[0])
	modelID, modelErr := url.PathUnescape(parts[1])
	if providerErr != nil || modelErr != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "invalid encoded model path")
		return true
	}
	response.Header().Set("ETag", `"conformance-model-v1"`)
	if provider == "openai" && modelID == "gpt-test" {
		writeJSON(response, http.StatusOK, catalogModel(provider, modelID, "GPT Test"))
		return true
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"provider":  provider,
		"id":        modelID,
		"cataloged": false,
		"pricing": map[string]any{
			"status":          "unpriced",
			"pricing_version": "conformance-pricing-v1",
		},
	})
	return true
}

func catalogModel(provider, modelID, displayName string) map[string]any {
	return map[string]any{
		"provider":              provider,
		"id":                    modelID,
		"cataloged":             true,
		"display_name":          displayName,
		"description":           "A conformance fixture model.",
		"context_window_tokens": 128000,
		"max_output_tokens":     32000,
		"input_modalities":      []string{"text", "image", "document"},
		"recommended":           true,
		"deprecated":            false,
		"controls": map[string]any{
			"sampling": map[string]any{
				"temperature": true,
			},
			"reasoning": map[string]any{
				"effort": map[string]any{
					"supported":        true,
					"values":           []string{"low", "medium", "high", "xhigh", "max"},
					"with_temperature": true,
				},
				"budget_tokens": map[string]any{
					"supported":        true,
					"with_temperature": false,
					"minimum":          1024,
					"maximum":          31999,
				},
				"effort_budget_compatible": false,
			},
			"tools": map[string]any{
				"choice": map[string]any{
					"modes":          []string{"auto", "none", "required", "named"},
					"with_reasoning": []string{"auto", "none"},
				},
				"web_search": true,
			},
			"input": map[string]any{
				"media": map[string]any{
					"image": map[string]any{
						"supported":   true,
						"media_types": []string{"image/gif", "image/jpeg", "image/png", "image/webp"},
					},
					"document": map[string]any{
						"supported":   true,
						"media_types": []string{"application/pdf"},
					},
				},
			},
		},
		"pricing": map[string]any{
			"status":          "priced",
			"currency":        "USD",
			"unit":            "per_million_tokens",
			"input":           "1.25",
			"output":          "10",
			"updated_at":      "2026-07-23",
			"pricing_version": "conformance-pricing-v1",
		},
	}
}

type conformanceMCPHeaders = conformanceMCPServerHeaders

func (s *state) createTurn(response http.ResponseWriter, request *http.Request) {
	var body struct {
		TenantKey      string `json:"tenant_key"`
		UserKey        string `json:"user_key"`
		IdempotencyKey string `json:"idempotency_key"`
		Behavior       struct {
			Kind     string         `json:"kind"`
			Agent    map[string]any `json:"agent"`
			Behavior map[string]any `json:"behavior"`
		} `json:"behavior"`
		Conversation *struct {
			IfActive string `json:"if_active"`
		} `json:"conversation"`
		ProviderKeys []struct {
			Provider string `json:"provider"`
			Source   string `json:"source"`
			Key      struct {
				APIKey string `json:"api_key"`
			} `json:"key"`
		} `json:"provider_keys"`
		MCPServerHeaders []conformanceMCPHeaders `json:"mcp_server_headers"`
		Context          []conformanceContext    `json:"context"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "invalid conformance admission")
		return
	}
	provesSuperconversation := strings.Contains(body.IdempotencyKey, "lost-ack") ||
		body.IdempotencyKey == "cli-answer"
	if provesSuperconversation && (body.Conversation == nil || body.Conversation.IfActive != "supersede") {
		writeError(response, http.StatusBadRequest, "invalid_request", "if_active did not round-trip")
		return
	}
	if len(body.ProviderKeys) > 0 {
		if len(body.ProviderKeys) != 1 ||
			body.ProviderKeys[0].Provider != "openai" ||
			body.ProviderKeys[0].Source != "caller_ephemeral" ||
			body.ProviderKeys[0].Key.APIKey != "conformance-secret" {
			writeError(response, http.StatusBadRequest, "invalid_request", "provider keys did not round-trip")
			return
		}
	}
	validBehavior := body.Behavior.Kind == "agent" && len(body.Behavior.Agent) > 0 ||
		body.Behavior.Kind == "inline" && len(body.Behavior.Behavior) > 0
	if body.TenantKey == "" || !validBehavior {
		writeError(response, http.StatusBadRequest, "invalid_request", "one behavior source and tenant are required")
		return
	}
	if len(body.MCPServerHeaders) > 0 &&
		(len(body.MCPServerHeaders) != 1 || !body.MCPServerHeaders[0].valid()) {
		writeError(response, http.StatusBadRequest, "invalid_request", "MCP server headers did not round-trip")
		return
	}
	for _, item := range body.Context {
		if !item.valid() {
			writeError(response, http.StatusBadRequest, "invalid_request", "recorded context did not round-trip")
			return
		}
	}
	s.mu.Lock()
	s.admissionAttempts++
	if len(body.ProviderKeys) == 1 {
		s.credentialAdmissions++
	}
	attempt := s.admissionAttempts
	s.mu.Unlock()
	if attempt == 1 && disconnect(response) {
		return
	}
	admission := turn("queued")
	admission["deduplicated"] = attempt > 1
	writeJSON(response, http.StatusAccepted, admission)
}

func (s *state) listTurns(response http.ResponseWriter, request *http.Request) {
	if !validAgentFilter(request.URL.Query()) {
		writeError(response, http.StatusBadRequest, "invalid_request", "invalid Agent filter")
		return
	}
	s.mu.Lock()
	s.lastStatuses = append([]string(nil), request.URL.Query()["status"]...)
	s.mu.Unlock()
	cursor := request.URL.Query().Get("cursor")
	writeJSON(response, http.StatusOK, map[string]any{
		"items":       []any{turn("completed")},
		"has_more":    cursor == "",
		"next_cursor": nullable(cursor == "", "turns-page-2"),
	})
}

func (s *state) turn(response http.ResponseWriter, request *http.Request) {
	remainder := strings.TrimPrefix(request.URL.Path, "/v1/turns/")
	if strings.HasSuffix(remainder, "/stream") && request.Method == http.MethodGet {
		s.stream(response, request)
		return
	}
	if strings.HasSuffix(remainder, "/tool-calls") && request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, toolCallRecords())
		return
	}
	if strings.HasSuffix(remainder, "/tool-results") && request.Method == http.MethodPost {
		s.mu.Lock()
		s.resultAttempts++
		attempt := s.resultAttempts
		s.mu.Unlock()
		if attempt == 1 && disconnect(response) {
			return
		}
		writeJSON(response, http.StatusAccepted, map[string]any{
			"turn_id":            turnID,
			"conversation_id":    conversationID,
			"content_expires_at": nil,
			"status":             "queued",
			"results": []any{map[string]any{
				"tool_call_id": toolCallID,
				"status":       "completed",
				"deduplicated": attempt > 1,
			}},
			"tool_calls": []any{},
		})
		return
	}
	if strings.HasSuffix(remainder, "/result") && request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, turnResult())
		return
	}
	if strings.HasSuffix(remainder, "/cancel") && request.Method == http.MethodPost {
		s.mu.Lock()
		s.cancelAttempts++
		s.mu.Unlock()
		writeJSON(response, http.StatusOK, turn("cancelled"))
		return
	}
	if strings.HasSuffix(remainder, "/interrupt") && request.Method == http.MethodPost {
		s.mu.Lock()
		s.interruptAttempts++
		s.mu.Unlock()
		// A graceful stop settles completed and says why, which is what
		// separates it from cancellation on the wire.
		interrupted := turn("completed")
		interrupted["stop_reason"] = "interrupted"
		writeJSON(response, http.StatusOK, interrupted)
		return
	}
	if request.Method != http.MethodGet {
		writeError(response, http.StatusMethodNotAllowed, "invalid_request", "method not allowed")
		return
	}
	switch remainder {
	case "unauthenticated":
		writeError(response, http.StatusUnauthorized, "unauthenticated", "authenticate")
	case "forbidden":
		writeError(response, http.StatusForbidden, "forbidden", "permission denied")
	case "conflict":
		writeError(response, http.StatusConflict, "idempotency_conflict", "request conflicts with durable state")
	case "failed":
		writeJSON(response, http.StatusOK, turnWithID("failed", "failed"))
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
		writeJSON(response, http.StatusOK, turn("completed"))
	case "rate-limit-always":
		response.Header().Set("Retry-After", "1")
		writeError(response, http.StatusTooManyRequests, "rate_limited", "slow down")
	case "server-error":
		writeError(response, http.StatusServiceUnavailable, "unavailable", "try later")
	case waitID:
		writeJSON(response, http.StatusOK, turnWithID(waitID, "waiting"))
	default:
		writeJSON(response, http.StatusOK, turn("completed"))
	}
}

func toolCallRecords() map[string]any {
	return map[string]any{
		"items": []any{
			map[string]any{
				"turn_id": turnID, "conversation_id": conversationID, "content_expires_at": nil,
				"id": "611bd610-16cd-7f66-9466-3fdd2ba25423", "mode": "builtin",
				"name": "nvoken_fetch", "status": "completed", "iteration": 1,
				"created_at": "2026-08-08T17:02:11Z", "ended_at": "2026-08-08T17:02:12Z", "attempts": 1,
				"result_origin": "builtin",
			},
			map[string]any{
				"turn_id": turnID, "conversation_id": conversationID, "content_expires_at": nil,
				"id": "63779fde-08fe-71c9-a953-873cd55651a4", "mode": "host",
				"name": "ask_user", "status": "running", "iteration": 1,
				"created_at": "2026-08-08T17:02:13Z", "ended_at": nil, "attempts": 0,
				"result_origin": nil,
			},
			map[string]any{
				"turn_id": turnID, "conversation_id": conversationID, "content_expires_at": nil,
				"id": "96da0f81-1aea-7bf6-9b33-373db60d7240", "mode": "callback",
				"name": "create_ticket", "status": "completed", "iteration": 2,
				"created_at": "2026-08-08T17:02:14Z", "ended_at": "2026-08-08T17:02:19Z", "attempts": 0,
				"result_origin": "callback",
				"delivery":      map[string]any{"outcome": "succeeded", "attempts": 2, "last_http_status": 200},
			},
			map[string]any{
				"turn_id": turnID, "conversation_id": conversationID, "content_expires_at": nil,
				"id": "06eb8d58-9853-7042-b320-83a0d5363ef3", "mode": "mcp",
				"name": "support__lookup", "status": "failed", "iteration": 2,
				"created_at": "2026-08-08T17:02:20Z", "ended_at": "2026-08-08T17:02:22Z", "attempts": 1,
				"result_origin": "mcp",
			},
			// An acknowledged callback: the endpoint returned 202 with no body,
			// and the result arrived later through tool-results, so the origin
			// is host even though the mode is callback.
			map[string]any{
				"turn_id": turnID, "conversation_id": conversationID, "content_expires_at": nil,
				"id": "13cff7a1-c1a2-7ed9-b1d1-d9ac7f34d5b8", "mode": "callback",
				"name": "run_migration", "status": "completed", "iteration": 3,
				"created_at": "2026-08-08T17:02:23Z", "ended_at": "2026-08-08T17:09:41Z", "attempts": 0,
				"result_origin": "host",
				"delivery":      map[string]any{"outcome": "acknowledged", "attempts": 1, "last_http_status": 202},
			},
		},
		"has_more":    false,
		"next_cursor": nil,
	}
}

func (s *state) createConversation(response http.ResponseWriter, request *http.Request) {
	var body struct {
		TenantKey       string         `json:"tenant_key"`
		ConversationKey string         `json:"conversation_key"`
		Owner           map[string]any `json:"owner"`
		Metadata        map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil ||
		body.TenantKey == "" || body.Owner["kind"] == nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "Conversation did not round-trip")
		return
	}
	value := conversation()
	value["tenant_key"] = body.TenantKey
	value["owner"] = body.Owner
	if body.ConversationKey != "" {
		value["conversation_key"] = body.ConversationKey
	}
	if body.Metadata != nil {
		value["metadata"] = body.Metadata
	}
	writeJSON(response, http.StatusCreated, value)
}

func (s *state) listConversations(response http.ResponseWriter, request *http.Request) {
	if !validAgentFilter(request.URL.Query()) {
		writeError(response, http.StatusBadRequest, "invalid_request", "invalid Agent filter")
		return
	}
	cursor := request.URL.Query().Get("cursor")
	writeJSON(response, http.StatusOK, map[string]any{
		"items":       []any{conversation()},
		"has_more":    cursor == "",
		"next_cursor": nullable(cursor == "", "conversations-page-2"),
	})
}

func (s *state) conversation(response http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/v1/conversations/")
	switch {
	case strings.HasSuffix(path, "/fork") && request.Method == http.MethodPost:
		var body struct {
			FromMessageID   string         `json:"from_message_id"`
			ConversationKey string         `json:"conversation_key"`
			Owner           map[string]any `json:"owner"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil ||
			body.FromMessageID == "" || body.Owner["kind"] == nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "Conversation fork did not round-trip")
			return
		}
		value := conversation()
		value["owner"] = body.Owner
		value["conversation_key"] = body.ConversationKey
		value["forked_from"] = map[string]any{
			"conversation_id": conversationID,
			"message_id":      body.FromMessageID,
		}
		writeJSON(response, http.StatusCreated, value)
	case strings.HasSuffix(path, "/stream") && request.Method == http.MethodGet:
		s.stream(response, request)
	case strings.HasSuffix(path, "/transcript") && request.Method == http.MethodGet:
		writeJSON(response, http.StatusOK, secondSnapshot())
	case strings.HasSuffix(path, "/compactions") && request.Method == http.MethodGet:
		writeJSON(response, http.StatusOK, map[string]any{
			"items": []any{map[string]any{
				"id":             "0fcd800d-f140-77fb-a589-88c2b5915eac",
				"turn_id":        turnID,
				"covers_through": 2,
				"status":         "applied",
				"failure_class":  nil,
				"usage":          map[string]any{"input_tokens": 18, "output_tokens": 5, "model_calls": 1},
				"summary":        "The user chose the durable option.",
				"created_at":     "2026-08-08T17:02:11Z",
			}},
			"has_more":    false,
			"next_cursor": nil,
		})
	case strings.HasSuffix(path, "/messages") && request.Method == http.MethodGet:
		cursor := request.URL.Query().Get("cursor")
		// A descending walk starts at the newest message, so the same two
		// messages arrive in the opposite order behind a cursor that names the
		// direction that issued it. An SDK that drops the option reads the
		// ascending page and fails its assertion.
		leading, trailing := firstMessage(), secondMessage()
		nextCursor := "messages-page-2"
		if request.URL.Query().Get("order") == "desc" {
			leading, trailing = trailing, leading
			nextCursor = "messages-page-2-desc"
		}
		items := []any{leading}
		if cursor != "" {
			items = []any{trailing}
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"items":       items,
			"has_more":    cursor == "",
			"next_cursor": nullable(cursor == "", nextCursor),
		})
	case request.Method == http.MethodPatch:
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body) == 0 {
			writeError(response, http.StatusBadRequest, "invalid_request", "Conversation update did not round-trip")
			return
		}
		value := conversation()
		for key, item := range body {
			value[key] = item
		}
		writeJSON(response, http.StatusOK, value)
	case request.Method == http.MethodDelete:
		response.WriteHeader(http.StatusNoContent)
	case request.Method == http.MethodGet:
		writeJSON(response, http.StatusOK, conversation())
	default:
		writeError(response, http.StatusMethodNotAllowed, "invalid_request", "method not allowed")
	}
}

// stream serves both public scopes. Three attempts walk a reader through a
// connection that just drops, one that rotates, and one that delivers the
// terminal change. A Turn stream ends after that change; a Conversation stream
// stays a subscription and receives an idle close hint.
func (s *state) stream(response http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	s.streamAttempts++
	attempt := s.streamAttempts
	// The resume position has one name. It arrives as the `cursor` query
	// parameter or the `Last-Event-ID` header, and `cursor` wins when both are
	// present, so record it the way the service resolves it rather than the way
	// one SDK happens to spell it.
	s.lastResumeCursor = request.URL.Query().Get("cursor")
	s.lastResumeSource = "cursor"
	if s.lastResumeCursor == "" {
		s.lastResumeCursor = request.Header.Get("Last-Event-ID")
		s.lastResumeSource = "last_event_id"
	}
	if s.lastResumeCursor == "" {
		s.lastResumeSource = ""
	}
	s.lastDeltas = request.URL.Query().Get("deltas")
	isTurnStream := strings.HasPrefix(request.URL.Path, "/v1/turns/")
	s.lastTurnID = ""
	if isTurnStream {
		s.lastTurnID = strings.TrimSuffix(
			strings.TrimPrefix(request.URL.Path, "/v1/turns/"),
			"/stream",
		)
	}
	deltas := s.lastDeltas
	s.mu.Unlock()
	response.Header().Set("Content-Type", "text/event-stream")
	response.WriteHeader(http.StatusOK)
	flusher, ok := response.(http.Flusher)
	if !ok {
		return
	}
	writeSSERetry(response)
	if attempt == 1 {
		writeSSE(response, "cursor-1", "transcript.update", firstTranscriptUpdate())
		flusher.Flush()
		return
	}
	writeSSE(response, "cursor-1", "transcript.update", firstTranscriptUpdate())
	if attempt == 2 {
		writeSSE(response, "", "connection.closing", map[string]any{
			"type":               "connection.closing",
			"conversation_id":    conversationID,
			"content_expires_at": nil,
			"reason":             "rotate",
		})
		flusher.Flush()
		return
	}
	if deltas != "false" {
		writeSSE(response, "", "message.delta", messageDelta())
	}
	writeSSE(response, "cursor-2", "transcript.update", secondTranscriptUpdate())
	if isTurnStream {
		flusher.Flush()
		return
	}
	// Nothing is running now. A Conversation reader is told the idle
	// connection is being reclaimed and can reconnect when it next needs data.
	writeSSE(response, "", "connection.closing", map[string]any{
		"type":               "connection.closing",
		"conversation_id":    conversationID,
		"content_expires_at": nil,
		"reason":             "idle",
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

// acknowledgedLimits is what admission resolved, echoed on both
// acknowledgement paths so a host learns what an omitted limit became without
// a second read.
func acknowledgedLimits() map[string]any {
	return map[string]any{
		"total_timeout_seconds":   300,
		"active_timeout_seconds":  120,
		"waiting_timeout_seconds": 180,
		"max_iterations":          16,
	}
}

func turn(status string) map[string]any {
	return turnWithID(turnID, status)
}

func turnWithID(id string, status string) map[string]any {
	endedAt := any(nil)
	if status == "completed" || status == "cancelled" || status == "failed" {
		endedAt = "2026-07-21T12:00:03Z"
	}
	value := map[string]any{
		"id":         id,
		"tenant_key": "acme",
		"behavior_source": map[string]any{
			"kind":              "agent_revision",
			"agent_id":          agentID,
			"agent_revision_id": agentRevisionID,
			"revision":          1,
		},
		"effective_behavior":           agentRevision()["behavior"],
		"effective_behavior_digest":    "sha256:4242424242424242424242424242424242424242424242424242424242424242",
		"conversation_id":              conversationID,
		"memory_space_id":              nil,
		"memory":                       nil,
		"content_expires_at":           nil,
		"user_key":                     nil,
		"context":                      nil,
		"status":                       status,
		"stop_reason":                  nullableStopReason(status),
		"credit_block":                 nil,
		"attempt":                      1,
		"error":                        nil,
		"usage":                        nil,
		"provenance":                   nil,
		"structured_output":            nil,
		"structured_output_provenance": nil,
		"metadata":                     nil,
		"limits":                       map[string]any{"total_timeout_seconds": 300, "active_timeout_seconds": 120, "waiting_timeout_seconds": 180, "max_iterations": 16},
		"active_execution_ms":          250,
		"deadline_at":                  "2026-07-21T12:05:00Z",
		"created_at":                   "2026-07-21T12:00:00Z",
		"updated_at":                   "2026-07-21T12:00:03Z",
		"ended_at":                     endedAt,
		"tool_calls":                   []any{},
	}
	if status == "waiting" {
		value["tool_calls"] = []any{map[string]any{
			"id":          toolCallID,
			"name":        "lookup_order",
			"mode":        "host",
			"status":      "pending",
			"arguments":   map[string]any{"order_id": "order-42"},
			"deadline_at": "2026-07-21T12:05:00Z",
			"updated_at":  "2026-07-21T12:00:03Z",
		}}
	}
	return value
}

// A stop reason exists only on a completed Turn; every other status
// carries null, and clients that decode it must accept both shapes.
func nullableStopReason(status string) any {
	if status == "completed" {
		return "end_turn"
	}
	return nil
}

func turnResult() map[string]any {
	// The composed Turn carries a populated structured output so every
	// client proves the renamed fields against real values, not null.
	composed := turn("completed")
	composed["structured_output"] = map[string]any{"answer": "world"}
	composed["structured_output_provenance"] = map[string]any{
		"source":        "tool_call",
		"tool_call_id":  "f72debf2-5686-740c-82c1-3bd0e1dda710",
		"schema_sha256": "abababababababababababababababababababababababababababababababab",
	}
	return map[string]any{
		"turn": composed,
		"messages": []any{
			firstMessage(),
			firstResultAssistantMessage(),
			secondResultAssistantMessage(),
		},
		"output_text": "A refund is queued.",
	}
}

func conversation() map[string]any {
	return map[string]any{
		"id":                 conversationID,
		"tenant_key":         "acme",
		"owner":              map[string]any{"kind": "user", "user_key": "agent-smith"},
		"conversation_key":   "ticket-A-42",
		"forked_from":        nil,
		"active_turn_id":     nil,
		"active_turn_status": nil,
		"compaction":         nil,
		"retention": map[string]any{
			"ttl_seconds": 86400,
		},
		"expires_at": "2026-07-22T12:00:03Z",
		"metadata": map[string]any{
			"title": "Refund policy",
		},
		"created_at": "2026-07-21T12:00:00Z",
		"updated_at": "2026-07-21T12:00:03Z",
	}
}

func agent() map[string]any {
	return map[string]any{
		"id":               agentID,
		"agent_key":        "support",
		"name":             "Support",
		"owner":            map[string]any{"kind": "app"},
		"current_revision": 1,
		"created_at":       "2026-07-21T12:00:00Z",
		"updated_at":       "2026-07-21T12:00:00Z",
		"archived_at":      nil,
	}
}

func agentRevision() map[string]any {
	return map[string]any{
		"id":       agentRevisionID,
		"agent_id": agentID,
		"revision": 1,
		"behavior": map[string]any{
			"instructions": "Be concise and helpful.",
			"model": map[string]any{
				"provider": "openai",
				"id":       "gpt-test",
			},
		},
		"behavior_sha256": "sha256:4242424242424242424242424242424242424242424242424242424242424242",
		"created_at":      "2026-07-21T12:00:00Z",
	}
}

func validAgentFilter(query url.Values) bool {
	agentKey := query.Get("agent_key")
	agentIDFilter := query.Get("agent_id")
	return !(agentKey != "" && agentIDFilter != "") &&
		(agentKey == "" || agentKey == "support") &&
		(agentIDFilter == "" || agentIDFilter == agentID)
}

func firstMessage() map[string]any {
	return map[string]any{
		"id":                 "89f5ce23-9fb3-7a9e-9c7f-2dcb44c41346",
		"conversation_id":    conversationID,
		"content_expires_at": nil,
		"agent_id":           agentID,
		"turn_id":            turnID,
		"user_key":           nil,
		"sequence":           1,
		"role":               "user",
		"content":            []any{map[string]any{"type": "text", "text": "hello"}},
		"created_at":         "2026-07-21T12:00:00Z",
	}
}

// secondMessageID is the assistant message the preview below is building. The
// preview carries it, so the handoff to the saved message updates a row that
// already has its permanent identity.
const secondMessageID = "85770b50-8915-79ac-b01c-6eb1548ea9b8"

func secondMessage() map[string]any {
	return map[string]any{
		"id":                 secondMessageID,
		"conversation_id":    conversationID,
		"content_expires_at": nil,
		"agent_id":           agentID,
		"turn_id":            turnID,
		"user_key":           nil,
		"sequence":           2,
		"role":               "assistant",
		"content":            []any{map[string]any{"type": "text", "text": "world"}},
		"created_at":         "2026-07-21T12:00:02Z",
	}
}

func firstResultAssistantMessage() map[string]any {
	return map[string]any{
		"id":                 "f84e45a3-ad13-77b3-80c0-648971cf33d7",
		"conversation_id":    conversationID,
		"content_expires_at": nil,
		"agent_id":           agentID,
		"turn_id":            turnID,
		"user_key":           nil,
		"sequence":           2,
		"role":               "assistant",
		"phase":              "commentary",
		"content": []any{
			map[string]any{"type": "text", "text": "The charge was"},
			map[string]any{"type": "tool_use", "id": "call_fixture", "name": "lookup", "input": map[string]any{}},
			map[string]any{"type": "text", "text": " duplicated."},
		},
		"created_at": "2026-07-21T12:00:02Z",
	}
}

func secondResultAssistantMessage() map[string]any {
	return map[string]any{
		"id":                 "f29873b8-b85b-7b39-9f19-743348119f04",
		"conversation_id":    conversationID,
		"content_expires_at": nil,
		"agent_id":           agentID,
		"turn_id":            turnID,
		"user_key":           nil,
		"sequence":           3,
		"role":               "assistant",
		"phase":              "final_answer",
		"content":            []any{map[string]any{"type": "text", "text": "A refund is queued."}},
		"created_at":         "2026-07-21T12:00:03Z",
	}
}

func firstChange() map[string]any {
	return change(1, "running", 1, "2026-07-21T12:00:01Z")
}

func secondChange() map[string]any {
	return change(2, "completed", 2, "2026-07-21T12:00:03Z")
}

// terminalStatus mirrors the service: `terminal` is exactly the four final
// statuses, computed rather than written into each fixture so a fixture cannot
// claim a change is an ending that its own status disagrees with.
func terminalStatus(status string) bool {
	switch status {
	case "completed", "incomplete", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func change(revision int, status string, sequence int, occurredAt string) map[string]any {
	return map[string]any{
		"turn_id":                      turnID,
		"conversation_id":              conversationID,
		"content_expires_at":           nil,
		"revision":                     revision,
		"status":                       status,
		"terminal":                     terminalStatus(status),
		"through_message_sequence":     sequence,
		"error":                        nil,
		"usage":                        nil,
		"provenance":                   nil,
		"structured_output":            nil,
		"structured_output_provenance": nil,
		"occurred_at":                  occurredAt,
	}
}

func firstSnapshot() map[string]any {
	return map[string]any{
		"messages":        []any{firstMessage()},
		"turn_changes":    []any{firstChange()},
		"has_more":        false,
		"cursor":          "cursor-1",
		"next_page_token": nil,
	}
}

func firstTranscriptUpdate() map[string]any {
	return map[string]any{
		"type":               "transcript.update",
		"conversation_id":    conversationID,
		"content_expires_at": nil,
		"messages":           []any{firstMessage()},
		"turn_changes":       []any{firstChange()},
		"cursor":             "cursor-1",
	}
}

func secondSnapshot() map[string]any {
	return map[string]any{
		"messages":        []any{firstMessage(), secondMessage()},
		"turn_changes":    []any{firstChange(), secondChange()},
		"has_more":        false,
		"cursor":          "cursor-2",
		"next_page_token": nil,
	}
}

func secondTranscriptUpdate() map[string]any {
	return map[string]any{
		"type":               "transcript.update",
		"conversation_id":    conversationID,
		"content_expires_at": nil,
		"messages":           []any{firstMessage(), secondMessage()},
		"turn_changes":       []any{firstChange(), secondChange()},
		"cursor":             "cursor-2",
	}
}

// messageDelta is the one preview frame. `kind` says what the fragment is and
// `delta` carries it, for every kind.
func messageDelta() map[string]any {
	return map[string]any{
		"type":               "message.delta",
		"conversation_id":    conversationID,
		"content_expires_at": nil,
		"turn_id":            turnID,
		"attempt":            1,
		"message_id":         secondMessageID,
		"content_index":      0,
		"kind":               "text",
		"delta":              "streamed answer",
		"emitted_at":         "2026-07-21T12:00:02Z",
	}
}

func nullable(condition bool, value string) any {
	if condition {
		return value
	}
	return nil
}

// writeSSERetry reproduces the control frame the runtime opens every stream
// with: a standalone `retry:` field terminated by a blank line, carrying no
// data, before any event. The fixtures previously wrote the field without that
// blank line, which merged it into the following event's frame — so no SDK's
// parser ever saw a data-less frame, and every SDK shipped a parser that
// dispatched one into the consumer. Emit it on every attempt, as the runtime
// does, so a reconnect exercises the same framing as the first connection.
func writeSSERetry(response http.ResponseWriter) {
	_, _ = fmt.Fprint(response, "retry: 1\n\n")
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
