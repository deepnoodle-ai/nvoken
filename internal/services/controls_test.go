package services

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

func TestBudgetPolicyResolvesDefaultsAndExplicitLimits(t *testing.T) {
	policy := DefaultBudgetPolicy()
	defaults, err := policy.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve defaults: %v", err)
	}
	if defaults.WallClockTimeout != 30*time.Minute || defaults.ActiveExecutionTimeout != 30*time.Minute ||
		defaults.MaxIterations != 1 || defaults.MaxOutputTokens != nil || defaults.MaxEstimatedCostMicros != nil {
		t.Fatalf("resolved defaults = %#v", defaults)
	}

	wall, active := int64(60), int64(30)
	output, iterations, cost := 4096, 2, 0.125001
	resolved, err := policy.Resolve(&InvocationBudgetInput{
		WallClockTimeoutSeconds: &wall, ActiveExecutionTimeoutSeconds: &active,
		MaxOutputTokens: &output, MaxEstimatedCostUSD: &cost, MaxIterations: &iterations,
	})
	if err != nil {
		t.Fatalf("resolve explicit budgets: %v", err)
	}
	if resolved.WallClockTimeout != time.Minute || resolved.ActiveExecutionTimeout != 30*time.Second ||
		resolved.MaxOutputTokens == nil || *resolved.MaxOutputTokens != output ||
		resolved.MaxEstimatedCostMicros == nil || *resolved.MaxEstimatedCostMicros != 125001 ||
		resolved.MaxIterations != iterations {
		t.Fatalf("resolved explicit budgets = %#v", resolved)
	}
}

func TestBudgetValidationRejectsUnsafeNumbers(t *testing.T) {
	for name, cost := range map[string]float64{
		"zero": 0, "negative": -1, "nan": math.NaN(), "infinite": math.Inf(1), "over precision": 0.1234567,
	} {
		t.Run(name, func(t *testing.T) {
			input := validServiceInput()
			input.Spec.Budgets = &InvocationBudgetInput{MaxEstimatedCostUSD: &cost}
			if err := ValidateCreateInvocation(input); err == nil {
				t.Fatal("invalid cost budget was accepted")
			}
		})
	}
}

func TestFingerprintV2MakesRequestedBudgetsMaterial(t *testing.T) {
	input := validServiceInput()
	omitted, err := InvocationFingerprintV2(input)
	if err != nil {
		t.Fatalf("omitted fingerprint: %v", err)
	}
	wall := int64(DefaultBudgetPolicy().DefaultWallClockTimeout / time.Second)
	input.Spec.Budgets = &InvocationBudgetInput{WallClockTimeoutSeconds: &wall}
	explicit, err := InvocationFingerprintV2(input)
	if err != nil {
		t.Fatalf("explicit fingerprint: %v", err)
	}
	if omitted == explicit {
		t.Fatal("explicit default matched omitted budget")
	}
}

func TestLegacyBudgetlessFingerprintReplayCompatibility(t *testing.T) {
	input := validServiceInput()
	legacy, err := InvocationFingerprintV1(input)
	if err != nil {
		t.Fatalf("legacy fingerprint: %v", err)
	}
	store := &legacyFingerprintStore{invocation: domain.Invocation{
		FingerprintVersion: 1, RequestFingerprint: legacy[:],
	}}
	service := &RuntimeService{store: store}
	v2, _ := InvocationFingerprintV2(input)
	if _, found, err := service.findIdempotent(context.Background(), "account", "partition", "agent", "key", input, v2); err != nil || !found {
		t.Fatalf("legacy replay found = %t, error = %v", found, err)
	}
	wall := int64(1800)
	input.Spec.Budgets = &InvocationBudgetInput{WallClockTimeoutSeconds: &wall}
	v2, _ = InvocationFingerprintV2(input)
	if _, _, err := service.findIdempotent(context.Background(), "account", "partition", "agent", "key", input, v2); err == nil {
		t.Fatal("budget-bearing request matched legacy budgetless fingerprint")
	}
}

func TestExecutionDeadlineUsesRemainingActiveAndWallBudgets(t *testing.T) {
	started := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	invocation := domain.Invocation{
		ActiveTimeoutMS: 10_000, ActiveExecutionMS: 7_000,
		WallClockDeadlineAt: started.Add(time.Hour),
	}
	deadline, scope, err := executionDeadline(invocation, started, 5*time.Second)
	if err != nil || scope != "active_execution" || !deadline.Equal(started.Add(3*time.Second)) {
		t.Fatalf("active deadline = %s %q, error = %v", deadline, scope, err)
	}
	invocation.WallClockDeadlineAt = started.Add(2 * time.Second)
	deadline, scope, err = executionDeadline(invocation, started, 5*time.Second)
	if err != nil || scope != "wall_clock" || !deadline.Equal(invocation.WallClockDeadlineAt) {
		t.Fatalf("wall deadline = %s %q, error = %v", deadline, scope, err)
	}
}

type legacyFingerprintStore struct {
	admissionStore
	invocation domain.Invocation
}

func (s *legacyFingerprintStore) GetInvocationByIdempotencyKey(context.Context, string, string, string, string) (domain.Invocation, error) {
	return s.invocation, nil
}
