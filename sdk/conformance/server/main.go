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
	agentID      = "agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320"
	sessionID    = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
	invocationID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
	waitID       = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328"
	toolCallID   = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
	definitionID = "def_019b0a12-8d51-7f34-aed2-0e07c1bdb329"
	exactModelID = "experimental/model?variant=雪%#1"
	allocationID = "alloc_019b0a12-8d51-7f34-aed2-0e07c1bdb330"
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
	lastEventID          string
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
		s.lastEventID = ""
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
			"last_event_id":         s.lastEventID,
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
	case createAgentDefinition(response, request):
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

func serveCredits(response http.ResponseWriter, request *http.Request) bool {
	if request.URL.Path == "/v1/credits/allocations" && request.Method == http.MethodPost {
		var body struct {
			Amount struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
			} `json:"amount"`
			DefaultTenant  bool   `json:"default_tenant"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil ||
			body.Amount.Amount != "25.000000" || body.Amount.Currency != "USD" ||
			!body.DefaultTenant || body.IdempotencyKey == "" {
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
		"tenant_key":         nil,
		"allocated":          money("25.000000"),
		"used":               money("3.250000"),
		"held":               money("1.500000"),
		"available":          money("20.250000"),
		"paused_invocations": 2,
		"created_at":         "2026-08-01T00:00:00Z",
		"updated_at":         "2026-08-08T12:00:00Z",
	}
}

func creditAllocation() map[string]any {
	return map[string]any{
		"id": allocationID, "tenant_key": nil, "amount": money("25.000000"),
		"reference": nil, "created_by": "cred_conformance", "created_at": "2026-08-01T00:00:00Z",
	}
}

func serveAgents(response http.ResponseWriter, request *http.Request) bool {
	if request.Method != http.MethodGet {
		return false
	}
	switch request.URL.Path {
	case "/v1/agents":
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
		writeJSON(response, http.StatusOK, agent())
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

// conformanceMCPServer carries no headers. A reusable Agent Definition is
// durable configuration, so secrets arrive separately in mcp_server_headers,
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

// conformanceMCPServerHeaders is one entry of the per-Invocation secret headers.
type conformanceMCPServerHeaders struct {
	Name    string            `json:"name"`
	Headers map[string]string `json:"headers"`
}

func (h conformanceMCPServerHeaders) valid() bool {
	return h.Name == "support" &&
		h.Headers["Authorization"] == "Bearer conformance-mcp-secret"
}

// conformanceContext is one recorded application state snapshot. It travels at
// the top level of the request, so an SDK that nests it inside the Agent
// Definition or drops the tier fails here rather than at the Runtime.
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

// conformanceDefinition is the execution configuration an Invocation nests
// under agent_definition and resource creation sends on its own.
type conformanceDefinition struct {
	Instructions string                 `json:"instructions"`
	Model        map[string]string      `json:"model"`
	MCPServers   []conformanceMCPServer `json:"mcp_servers"`
	Sampling     *struct {
		Temperature *float64 `json:"temperature"`
	} `json:"sampling"`
	Reasoning *struct {
		Effort       *string `json:"effort"`
		BudgetTokens *int    `json:"budget_tokens"`
	} `json:"reasoning"`
}

type conformanceMCPHeaders = conformanceMCPServerHeaders

// createAgentDefinition creates one stable Agent Definition resource.
func createAgentDefinition(response http.ResponseWriter, request *http.Request) bool {
	if request.URL.Path != "/v1/agent-definitions" || request.Method != http.MethodPost {
		return false
	}
	var definition conformanceDefinition
	if err := json.NewDecoder(request.Body).Decode(&definition); err != nil ||
		definition.Model["provider"] == "" || definition.Model["id"] == "" {
		writeError(response, http.StatusBadRequest, "invalid_request", "Agent Definition did not round-trip")
		return true
	}
	if len(definition.MCPServers) > 0 &&
		(len(definition.MCPServers) != 1 || !definition.MCPServers[0].valid()) {
		writeError(response, http.StatusBadRequest, "invalid_request", "MCP server declaration did not round-trip")
		return true
	}
	resolved := map[string]any{"model": definition.Model}
	if definition.Instructions != "" {
		resolved["instructions"] = definition.Instructions
	}
	resolved["id"] = definitionID
	resolved["revision"] = 1
	resolved["created_at"] = "2026-08-08T12:00:00Z"
	resolved["updated_at"] = "2026-08-08T12:00:00Z"
	writeJSON(response, http.StatusCreated, resolved)
	return true
}

func (s *state) createInvocation(response http.ResponseWriter, request *http.Request) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		IfActive       string `json:"if_active"`
		ProviderKeys   []struct {
			Provider string `json:"provider"`
			Source   string `json:"source"`
			Key      struct {
				APIKey string `json:"api_key"`
			} `json:"key"`
		} `json:"provider_keys"`
		AgentDefinition   *conformanceDefinition  `json:"agent_definition"`
		AgentDefinitionID string                  `json:"agent_definition_id"`
		MCPServerHeaders  []conformanceMCPHeaders `json:"mcp_server_headers"`
		Context           []conformanceContext    `json:"context"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "invalid conformance admission")
		return
	}
	provesSupersession := strings.Contains(body.IdempotencyKey, "lost-ack") ||
		body.IdempotencyKey == "cli-answer"
	if provesSupersession && body.IfActive != "supersede" {
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
	if (body.AgentDefinition == nil) == (body.AgentDefinitionID == "") {
		writeError(response, http.StatusBadRequest, "invalid_request", "exactly one Agent Definition form is required")
		return
	}
	if body.AgentDefinition != nil &&
		(body.AgentDefinition.Model["provider"] == "" || body.AgentDefinition.Model["id"] == "") {
		writeError(response, http.StatusBadRequest, "invalid_request", "inline Agent Definition did not round-trip")
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
	admission := invocation("queued")
	admission["deduplicated"] = attempt > 1
	writeJSON(response, http.StatusAccepted, admission)
}

func (s *state) listInvocations(response http.ResponseWriter, request *http.Request) {
	if !validAgentFilter(request.URL.Query()) {
		writeError(response, http.StatusBadRequest, "invalid_request", "invalid Agent filter")
		return
	}
	s.mu.Lock()
	s.lastStatuses = append([]string(nil), request.URL.Query()["status"]...)
	s.mu.Unlock()
	cursor := request.URL.Query().Get("cursor")
	writeJSON(response, http.StatusOK, map[string]any{
		"items":       []any{invocation("completed")},
		"has_more":    cursor == "",
		"next_cursor": nullable(cursor == "", "invocations-page-2"),
	})
}

func (s *state) invocation(response http.ResponseWriter, request *http.Request) {
	remainder := strings.TrimPrefix(request.URL.Path, "/v1/invocations/")
	if strings.HasSuffix(remainder, "/stream") && request.Method == http.MethodGet {
		s.invocationStream(response, request)
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
	if strings.HasSuffix(remainder, "/result") && request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, invocationResult())
		return
	}
	if strings.HasSuffix(remainder, "/cancel") && request.Method == http.MethodPost {
		s.mu.Lock()
		s.cancelAttempts++
		s.mu.Unlock()
		writeJSON(response, http.StatusOK, invocation("cancelled"))
		return
	}
	if strings.HasSuffix(remainder, "/interrupt") && request.Method == http.MethodPost {
		s.mu.Lock()
		s.interruptAttempts++
		s.mu.Unlock()
		// A graceful stop settles completed and says why, which is what
		// separates it from cancellation on the wire.
		interrupted := invocation("completed")
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
		writeJSON(response, http.StatusOK, invocationWithID("failed", "failed"))
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
		writeJSON(response, http.StatusOK, invocationWithID(waitID, "waiting"))
	default:
		writeJSON(response, http.StatusOK, invocation("completed"))
	}
}

func toolCallRecords() map[string]any {
	return map[string]any{
		"items": []any{
			map[string]any{
				"id": "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb324", "mode": "builtin",
				"name": "nvoken_fetch", "status": "completed", "iteration": 1,
				"created_at": "2026-08-08T17:02:11Z", "ended_at": "2026-08-08T17:02:12Z", "attempts": 1,
				"result_origin": "builtin",
			},
			map[string]any{
				"id": "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325", "mode": "host",
				"name": "ask_user", "status": "running", "iteration": 1,
				"created_at": "2026-08-08T17:02:13Z", "ended_at": nil, "attempts": 0,
				"result_origin": nil,
			},
			map[string]any{
				"id": "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb326", "mode": "callback",
				"name": "create_ticket", "status": "completed", "iteration": 2,
				"created_at": "2026-08-08T17:02:14Z", "ended_at": "2026-08-08T17:02:19Z", "attempts": 0,
				"result_origin": "callback",
				"delivery":      map[string]any{"outcome": "succeeded", "attempts": 2, "last_http_status": 200},
			},
			map[string]any{
				"id": "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb327", "mode": "mcp",
				"name": "support__lookup", "status": "failed", "iteration": 2,
				"created_at": "2026-08-08T17:02:20Z", "ended_at": "2026-08-08T17:02:22Z", "attempts": 1,
				"result_origin": "mcp",
			},
			// An acknowledged callback: the endpoint returned 202 with no body,
			// and the result arrived later through tool-results, so the origin
			// is host even though the mode is callback.
			map[string]any{
				"id": "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb32a", "mode": "callback",
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

func (s *state) listSessions(response http.ResponseWriter, request *http.Request) {
	if !validAgentFilter(request.URL.Query()) {
		writeError(response, http.StatusBadRequest, "invalid_request", "invalid Agent filter")
		return
	}
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
	case strings.HasSuffix(path, "/compactions") && request.Method == http.MethodGet:
		writeJSON(response, http.StatusOK, map[string]any{
			"items": []any{map[string]any{
				"id":             "cmp_019b0a12-8d51-7f34-aed2-0e07c1bdb322",
				"invocation_id":  invocationID,
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
	s.lastDeltas = request.URL.Query().Get("deltas")
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
		writeSSE(response, "", "stream.end", map[string]any{
			"type":          "stream.end",
			"session_id":    sessionID,
			"invocation_id": nil,
			"reason":        "rotate",
			"resume_cursor": "cursor-1",
		})
		flusher.Flush()
		return
	}
	writeSSE(response, "cursor-2", "transcript.update", secondTranscriptUpdate())
	writeSSE(response, "", "stream.end", map[string]any{
		"type":          "stream.end",
		"session_id":    sessionID,
		"invocation_id": nil,
		"reason":        "terminal",
		"resume_cursor": "cursor-2",
	})
	flusher.Flush()
}

func (s *state) invocationStream(response http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	s.streamAttempts++
	attempt := s.streamAttempts
	s.lastEventID = request.Header.Get("Last-Event-ID")
	s.lastDeltas = request.URL.Query().Get("deltas")
	s.mu.Unlock()
	response.Header().Set("Content-Type", "text/event-stream")
	response.WriteHeader(http.StatusOK)
	flusher, ok := response.(http.Flusher)
	if !ok {
		return
	}
	update := func(cursor string) {
		writeSSE(response, cursor, "invocation.update", map[string]any{
			"type":          "invocation.update",
			"session_id":    sessionID,
			"invocation_id": invocationID,
			"invocation":    invocation("completed"),
			"new_messages":  []any{secondMessage()},
		})
	}
	writeSSERetry(response)
	if attempt == 1 {
		update("cursor-1")
		flusher.Flush()
		return
	}
	if attempt == 2 {
		writeSSE(response, "", "stream.end", map[string]any{
			"type":          "stream.end",
			"session_id":    sessionID,
			"invocation_id": invocationID,
			"reason":        "rotate",
			"resume_cursor": "cursor-1",
		})
		flusher.Flush()
		return
	}
	update("cursor-2")
	writeSSE(response, "cursor-3", "invocation.result", map[string]any{
		"type":          "invocation.result",
		"session_id":    sessionID,
		"invocation_id": invocationID,
		"result":        invocationResult(),
	})
	writeSSE(response, "", "stream.end", map[string]any{
		"type":          "stream.end",
		"session_id":    sessionID,
		"invocation_id": invocationID,
		"reason":        "terminal",
		"resume_cursor": "cursor-3",
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

func invocation(status string) map[string]any {
	return invocationWithID(invocationID, status)
}

func invocationWithID(id string, status string) map[string]any {
	endedAt := any(nil)
	if status == "completed" || status == "cancelled" || status == "failed" {
		endedAt = "2026-07-21T12:00:03Z"
	}
	value := map[string]any{
		"id":                           id,
		"agent_id":                     agentID,
		"session_id":                   sessionID,
		"user_key":                     nil,
		"agent_definition_id":          definitionID,
		"agent_definition_revision":    1,
		"agent_definition":             nil,
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
		"waiting_execution_ms":         0,
		"deadline_at":                  "2026-07-21T12:05:00Z",
		"created_at":                   "2026-07-21T12:00:00Z",
		"updated_at":                   "2026-07-21T12:00:03Z",
		"ended_at":                     endedAt,
	}
	if status == "waiting" {
		value["pending_tool_calls"] = []any{map[string]any{
			"id":          toolCallID,
			"name":        "lookup_order",
			"input":       map[string]any{"order_id": "order-42"},
			"deadline_at": "2026-07-21T12:05:00Z",
		}}
	}
	return value
}

// A stop reason exists only on a completed Invocation; every other status
// carries null, and clients that decode it must accept both shapes.
func nullableStopReason(status string) any {
	if status == "completed" {
		return "end_turn"
	}
	return nil
}

func invocationResult() map[string]any {
	// The composed Invocation carries a populated structured output so every
	// client proves the renamed fields against real values, not null.
	composed := invocation("completed")
	composed["structured_output"] = map[string]any{"answer": "world"}
	composed["structured_output_provenance"] = map[string]any{
		"source":        "tool_call",
		"tool_call_id":  "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb999",
		"schema_sha256": "abababababababababababababababababababababababababababababababab",
	}
	return map[string]any{
		"invocation": composed,
		"messages": []any{
			firstMessage(),
			firstResultAssistantMessage(),
			secondResultAssistantMessage(),
		},
		"output_text": "The charge was duplicated.\n\nA refund is queued.",
	}
}

func session() map[string]any {
	return map[string]any{
		"id":                       sessionID,
		"agent_id":                 agentID,
		"tenant_key":               "acme",
		"session_key":              "ticket-A-42",
		"user_key":                 "agent-smith",
		"forked_from":              nil,
		"active_invocation_id":     nil,
		"active_invocation_status": nil,
		"credit_block":             nil,
		"context": map[string]any{
			"estimated_tokens":      12,
			"context_window_tokens": 128000,
			"model": map[string]any{
				"provider": "openai",
				"id":       "gpt-test",
			},
		},
		"compaction": nil,
		"retention": map[string]any{
			"ttl_seconds": 86400,
		},
		"expires_at": "2026-07-22T12:00:03Z",
		"metadata": map[string]any{
			"title": "Refund policy",
		},
		"usage": map[string]any{
			"input_tokens":  9,
			"output_tokens": 3,
			"model_calls":   2,
		},
		"created_at": "2026-07-21T12:00:00Z",
		"updated_at": "2026-07-21T12:00:03Z",
	}
}

func agent() map[string]any {
	return map[string]any{
		"id":         agentID,
		"agent_key":  "support",
		"created_at": "2026-07-21T12:00:00Z",
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
		"id":            "smsg_019b0a12-8d51-7f34-aed2-0e07c1bdb323",
		"session_id":    sessionID,
		"agent_id":      agentID,
		"invocation_id": invocationID,
		"user_key":      nil,
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
		"user_key":      nil,
		"sequence":      2,
		"role":          "assistant",
		"content":       []any{map[string]any{"type": "text", "text": "world"}},
		"created_at":    "2026-07-21T12:00:02Z",
	}
}

func firstResultAssistantMessage() map[string]any {
	return map[string]any{
		"id":            "smsg_019b0a12-8d51-7f34-aed2-0e07c1bdb325",
		"session_id":    sessionID,
		"agent_id":      agentID,
		"invocation_id": invocationID,
		"user_key":      nil,
		"sequence":      2,
		"role":          "assistant",
		"content": []any{
			map[string]any{"type": "text", "text": "The charge was"},
			map[string]any{"type": "tool_use", "id": "tcal_fixture", "name": "lookup", "input": map[string]any{}},
			map[string]any{"type": "text", "text": " duplicated."},
		},
		"created_at": "2026-07-21T12:00:02Z",
	}
}

func secondResultAssistantMessage() map[string]any {
	return map[string]any{
		"id":            "smsg_019b0a12-8d51-7f34-aed2-0e07c1bdb326",
		"session_id":    sessionID,
		"agent_id":      agentID,
		"invocation_id": invocationID,
		"user_key":      nil,
		"sequence":      3,
		"role":          "assistant",
		"content":       []any{map[string]any{"type": "text", "text": "A refund is queued."}},
		"created_at":    "2026-07-21T12:00:03Z",
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
		"invocation_id":                invocationID,
		"revision":                     revision,
		"status":                       status,
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
		"messages":           []any{firstMessage()},
		"invocation_changes": []any{firstChange()},
		"has_more":           false,
		"resume_cursor":      "cursor-1",
		"next_page_token":    nil,
	}
}

func firstTranscriptUpdate() map[string]any {
	return map[string]any{
		"type":               "transcript.update",
		"session_id":         sessionID,
		"messages":           []any{firstMessage()},
		"invocation_changes": []any{firstChange()},
		"resume_cursor":      "cursor-1",
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

func secondTranscriptUpdate() map[string]any {
	return map[string]any{
		"type":               "transcript.update",
		"session_id":         sessionID,
		"messages":           []any{firstMessage(), secondMessage()},
		"invocation_changes": []any{firstChange(), secondChange()},
		"resume_cursor":      "cursor-2",
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
