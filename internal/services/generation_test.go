package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type fakeGenerationStore struct {
	snapshot domain.ExecutionSpecSnapshot
	messages []domain.SessionMessage
	err      error
}

func (s *fakeGenerationStore) GetExecutionSpecSnapshot(context.Context, string) (domain.ExecutionSpecSnapshot, error) {
	if s.err != nil {
		return domain.ExecutionSpecSnapshot{}, s.err
	}
	return s.snapshot, nil
}

func (*fakeGenerationStore) CreateExecutionSpecSnapshot(context.Context, domain.ExecutionSpecSnapshot) error {
	return errors.New("not used")
}

func (s *fakeGenerationStore) ListSessionMessages(context.Context, string) ([]domain.SessionMessage, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]domain.SessionMessage(nil), s.messages...), nil
}

func (*fakeGenerationStore) AppendSessionMessage(context.Context, domain.SessionMessage) error {
	return errors.New("not used")
}

type fakeModelGenerator struct {
	requests []domain.GenerationRequest
	response domain.GenerationResponse
	err      error
}

type cancellingModelGenerator struct {
	cancel   context.CancelFunc
	response domain.GenerationResponse
}

func (g cancellingModelGenerator) Generate(context.Context, domain.GenerationRequest) (domain.GenerationResponse, error) {
	g.cancel()
	return g.response, nil
}

func (g *fakeModelGenerator) Generate(ctx context.Context, request domain.GenerationRequest) (domain.GenerationResponse, error) {
	g.requests = append(g.requests, request)
	if g.err != nil {
		return domain.GenerationResponse{}, g.err
	}
	return g.response, nil
}

func TestGenerationExecutorReconstructsExactDurableTurn(t *testing.T) {
	claim := generationClaim()
	store := generationStoreFixture(claim)
	generator := &fakeModelGenerator{response: successfulGenerationResponse()}
	var logs bytes.Buffer
	executor := NewGenerationExecutor(store, generator, slog.New(slog.NewJSONHandler(&logs, nil)))

	result, err := executor.Execute(context.Background(), claim)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != domain.InvocationCompleted || len(result.AssistantMessages) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(generator.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(generator.requests))
	}
	wantMessages := []domain.GenerationMessage{
		{Role: domain.MessageRoleUser, Content: json.RawMessage(`[{"type":"text","text":"first"}]`)},
		{Role: domain.MessageRoleAssistant, Content: json.RawMessage(`[{"type":"text","text":"answer"}]`)},
		{Role: domain.MessageRoleUser, Content: json.RawMessage(`[{"type":"text","text":"current"}]`)},
	}
	got := generator.requests[0]
	if got.Instructions != "durable instructions" || got.Provider != "anthropic" || got.Model != "claude-test" {
		t.Fatalf("request selection = %#v", got)
	}
	if !reflect.DeepEqual(got.Messages, wantMessages) {
		t.Fatalf("messages = %#v, want %#v", got.Messages, wantMessages)
	}
	if result.Provenance == nil || result.Provenance.CredentialSource != credentialSourceInstallationBYOK ||
		result.Provenance.ServedModel != "claude-served" {
		t.Fatalf("provenance = %#v", result.Provenance)
	}
	logText := logs.String()
	for _, forbidden := range []string{"durable instructions", `\"text\":\"first\"`, `\"text\":\"answer\"`, `\"text\":\"current\"`} {
		if strings.Contains(logText, forbidden) {
			t.Fatalf("logs contain durable content %q: %s", forbidden, logText)
		}
	}
	for _, required := range []string{claim.Invocation.ID, "anthropic", "claude-test", "claude-served", "input_tokens"} {
		if !strings.Contains(logText, required) {
			t.Fatalf("logs do not contain %q: %s", required, logText)
		}
	}
}

func TestGenerationExecutorRejectsInvalidDurableInputsWithoutModelCall(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeGenerationStore, domain.InvocationClaim)
	}{
		{"unknown spec field", func(store *fakeGenerationStore, _ domain.InvocationClaim) {
			store.snapshot.Spec = json.RawMessage(`{"instructions":"x","model":{"provider":"openai","name":"gpt-test"},"tools":[]}`)
		}},
		{"tool history", func(store *fakeGenerationStore, _ domain.InvocationClaim) {
			store.messages[0].Content = json.RawMessage(`[{"type":"tool_use","id":"secret"}]`)
		}},
		{"wrong scope", func(store *fakeGenerationStore, _ domain.InvocationClaim) {
			store.messages[0].AccountID = "acct_other"
		}},
		{"duplicated current input", func(store *fakeGenerationStore, claim domain.InvocationClaim) {
			store.messages = append(store.messages, domain.SessionMessage{
				SessionID: claim.Invocation.SessionID, AccountID: claim.Invocation.AccountID,
				TenantPartitionID: claim.Invocation.TenantPartitionID, AgentID: claim.Invocation.AgentID,
				InvocationID: "inv_other", Sequence: 4, Role: domain.MessageRoleAssistant,
				Content: json.RawMessage(`[{"type":"text","text":"late"}]`),
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claim := generationClaim()
			store := generationStoreFixture(claim)
			test.mutate(store, claim)
			generator := &fakeModelGenerator{response: successfulGenerationResponse()}
			result, err := NewGenerationExecutor(store, generator, nil).Execute(context.Background(), claim)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			assertFailureCode(t, result, "internal")
			if len(generator.requests) != 0 {
				t.Fatalf("model calls = %d, want 0", len(generator.requests))
			}
		})
	}
}

func TestGenerationExecutorMapsProviderFailuresAndInvalidResponses(t *testing.T) {
	for _, test := range []struct {
		name      string
		generator *fakeModelGenerator
	}{
		{"missing key", &fakeModelGenerator{err: ports.ErrProviderKeyMissing}},
		{"provider failure", &fakeModelGenerator{err: errors.New("provider response contains secret")}},
		{"empty output", &fakeModelGenerator{response: domain.GenerationResponse{Usage: domain.ModelUsage{}, ServedModel: "test"}}},
		{"tool output", &fakeModelGenerator{response: domain.GenerationResponse{
			Messages: []domain.GenerationMessage{{Role: domain.MessageRoleAssistant, Content: json.RawMessage(`[{"type":"tool_use","id":"secret"}]`)}},
			Usage:    domain.ModelUsage{}, ServedModel: "test",
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			claim := generationClaim()
			var logs bytes.Buffer
			executor := NewGenerationExecutor(generationStoreFixture(claim), test.generator, slog.New(slog.NewJSONHandler(&logs, nil)))
			result, err := executor.Execute(context.Background(), claim)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			assertFailureCode(t, result, "provider_error")
			if strings.Contains(logs.String(), "provider response contains secret") {
				t.Fatalf("logs contain provider error: %s", logs.String())
			}
		})
	}
}

func TestGenerationExecutorMapsDurableConversionFailureToInternal(t *testing.T) {
	claim := generationClaim()
	generator := &fakeModelGenerator{err: ports.ErrGenerationInputInvalid}
	result, err := NewGenerationExecutor(generationStoreFixture(claim), generator, nil).Execute(context.Background(), claim)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertFailureCode(t, result, "internal")
}

func TestGenerationExecutorEnforcesResolvedBudgetsAndRetainsEvidence(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*domain.Invocation, *domain.GenerationResponse)
		wantKind  string
	}{
		{"output tokens", func(invocation *domain.Invocation, _ *domain.GenerationResponse) {
			limit := 3
			invocation.MaxOutputTokens = &limit
		}, "output_tokens"},
		{"estimated cost", func(invocation *domain.Invocation, _ *domain.GenerationResponse) {
			limit := int64(100_000)
			invocation.MaxEstimatedCostMicros = &limit
		}, "estimated_cost"},
		{"missing estimated cost", func(invocation *domain.Invocation, response *domain.GenerationResponse) {
			limit := int64(100_000)
			invocation.MaxEstimatedCostMicros = &limit
			response.Usage.EstimatedCost = nil
		}, "estimated_cost_unavailable"},
		{"iterations", func(invocation *domain.Invocation, response *domain.GenerationResponse) {
			invocation.MaxIterations = 1
			response.Usage.Iterations = 2
		}, "iterations"},
	} {
		t.Run(test.name, func(t *testing.T) {
			claim := generationClaim()
			claim.Invocation.MaxIterations = 3
			response := successfulGenerationResponse()
			response.Usage.Iterations = 1
			test.configure(&claim.Invocation, &response)
			generator := &fakeModelGenerator{response: response}
			result, err := NewGenerationExecutor(generationStoreFixture(claim), generator, nil).Execute(context.Background(), claim)
			if err != nil || result.Status != domain.InvocationFailed || len(result.AssistantMessages) != 0 || result.Usage == nil || result.Provenance == nil {
				t.Fatalf("budget result = %#v, error = %v", result, err)
			}
			var failure struct {
				Details map[string]string `json:"details"`
			}
			if json.Unmarshal(result.Error, &failure) != nil || failure.Details["kind"] != test.wantKind {
				t.Fatalf("failure = %s, want kind %q", result.Error, test.wantKind)
			}
		})
	}
}

func TestValidateExecutionResultAcceptsPairedEvidenceOnFailure(t *testing.T) {
	result := providerGenerationFailure()
	result.Usage = &domain.ModelUsage{}
	result.Provenance = &domain.ModelProvenance{
		Provider: "anthropic", RequestedModel: "test", ServedModel: "test",
		CredentialSource: credentialSourceInstallationBYOK,
	}
	if err := validateExecutionResult(result); err != nil {
		t.Fatalf("failed result with paired evidence: %v", err)
	}
	result.Provenance = nil
	if err := validateExecutionResult(result); err == nil {
		t.Fatal("failed result with unpaired evidence passed validation")
	}
}

func TestGenerationExecutorReturnsCancellationWithoutSemanticResult(t *testing.T) {
	claim := generationClaim()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	generator := &fakeModelGenerator{err: context.Canceled}
	result, err := NewGenerationExecutor(generationStoreFixture(claim), generator, nil).Execute(ctx, claim)
	if !errors.Is(err, context.Canceled) || result.Status != "" {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestGenerationExecutorRetainsEvidenceWhenDeadlineFiresAfterResponse(t *testing.T) {
	claim := generationClaim()
	scope := "wall_clock"
	claim.Invocation.ExecutionDeadlineScope = &scope
	claim.Invocation.MaxIterations = 1
	ctx, cancel := context.WithCancel(context.Background())
	response := successfulGenerationResponse()
	response.Usage.Iterations = 1
	result, err := NewGenerationExecutor(
		generationStoreFixture(claim), cancellingModelGenerator{cancel: cancel, response: response}, nil,
	).Execute(ctx, claim)
	if err != nil || result.Status != domain.InvocationFailed || result.Usage == nil || result.Provenance == nil || len(result.AssistantMessages) != 0 {
		t.Fatalf("deadline result = %#v, error = %v", result, err)
	}
	assertFailureCode(t, result, "deadline_exceeded")
}

func generationClaim() domain.InvocationClaim {
	return domain.InvocationClaim{
		Invocation: domain.Invocation{
			ID: "inv_current", SessionID: "ses_current", AccountID: "acct_current",
			TenantPartitionID: "ten_current", AgentID: "agt_current", SpecSnapshotID: "spec_current",
		},
		Owner: "owner", Attempt: 2,
	}
}

func generationStoreFixture(claim domain.InvocationClaim) *fakeGenerationStore {
	message := func(invocation string, sequence int64, role domain.MessageRole, text string) domain.SessionMessage {
		return domain.SessionMessage{
			SessionID: claim.Invocation.SessionID, AccountID: claim.Invocation.AccountID,
			TenantPartitionID: claim.Invocation.TenantPartitionID, AgentID: claim.Invocation.AgentID,
			InvocationID: invocation, Sequence: sequence, Role: role,
			Content: json.RawMessage(`[{"type":"text","text":"` + text + `"}]`),
		}
	}
	return &fakeGenerationStore{
		snapshot: domain.ExecutionSpecSnapshot{
			ID: claim.Invocation.SpecSnapshotID, AccountID: claim.Invocation.AccountID,
			Spec: json.RawMessage(`{"instructions":"durable instructions","model":{"provider":"ANTHROPIC","name":"claude-test"}}`),
		},
		messages: []domain.SessionMessage{
			message("inv_first", 1, domain.MessageRoleUser, "first"),
			message("inv_first", 2, domain.MessageRoleAssistant, "answer"),
			message(claim.Invocation.ID, 3, domain.MessageRoleUser, "current"),
		},
	}
}

func successfulGenerationResponse() domain.GenerationResponse {
	return domain.GenerationResponse{
		Messages: []domain.GenerationMessage{{
			Role:    domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"text","text":"new answer"}]`),
		}},
		Usage: domain.ModelUsage{
			InputTokens: 10, OutputTokens: 4, CacheCreationInputTokens: 2,
			CacheReadInputTokens: 3, ReasoningTokens: 1,
			EstimatedCost: &domain.ModelCost{Input: .1, Output: .2, Total: .3, Currency: "USD", Model: "claude-served"},
		},
		ServedModel: "claude-served",
	}
}

func assertFailureCode(t *testing.T, result domain.InvocationExecutionResult, want string) {
	t.Helper()
	if result.Status != domain.InvocationFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(result.Error, &failure); err != nil || failure.Code != want {
		t.Fatalf("failure = %s, want code %q (error %v)", result.Error, want, err)
	}
}
