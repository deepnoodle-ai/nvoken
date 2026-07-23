package services

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

func TestLimitPolicyResolvesDefaultsAndExplicitLimits(t *testing.T) {
	policy := DefaultLimitPolicy()
	defaults, err := policy.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve defaults: %v", err)
	}
	if defaults.TotalTimeout != 30*time.Minute || defaults.ActiveTimeout != 30*time.Minute ||
		defaults.WaitingTimeout != 30*time.Minute ||
		defaults.MaxIterations != 1 || defaults.MaxOutputTokens != nil || defaults.MaxEstimatedCostMicros != nil {
		t.Fatalf("resolved defaults = %#v", defaults)
	}

	total, active, waiting := int64(60), int64(30), int64(15)
	output, iterations, cost := 4096, 2, 0.125001
	resolved, err := policy.Resolve(&InvocationLimitInput{
		TotalTimeoutSeconds:   &total,
		ActiveTimeoutSeconds:  &active,
		WaitingTimeoutSeconds: &waiting,
		MaxOutputTokens:       &output,
		MaxEstimatedCostUSD:   &cost,
		MaxIterations:         &iterations,
	})
	if err != nil {
		t.Fatalf("resolve explicit limits: %v", err)
	}
	if resolved.TotalTimeout != time.Minute || resolved.ActiveTimeout != 30*time.Second ||
		resolved.WaitingTimeout != 15*time.Second ||
		resolved.MaxOutputTokens == nil || *resolved.MaxOutputTokens != output ||
		resolved.MaxEstimatedCostMicros == nil || *resolved.MaxEstimatedCostMicros != 125001 ||
		resolved.MaxIterations != iterations {
		t.Fatalf("resolved explicit limits = %#v", resolved)
	}
}

func TestLimitPolicyResolvesStructuredOutputIterationFloor(t *testing.T) {
	policy := DefaultLimitPolicy()
	resolved, err := policy.ResolveForOutput(nil, true)
	if err != nil {
		t.Fatalf("resolve output defaults: %v", err)
	}
	if resolved.MaxIterations != 3 {
		t.Fatalf("output default iterations = %d, want 3", resolved.MaxIterations)
	}

	explicit := 2
	resolved, err = policy.ResolveForOutput(&InvocationLimitInput{
		MaxIterations: &explicit,
	}, true)
	if err != nil || resolved.MaxIterations != explicit {
		t.Fatalf("explicit output iterations = %d, error = %v", resolved.MaxIterations, err)
	}

	explicit = 1
	if _, err := policy.ResolveForOutput(&InvocationLimitInput{
		MaxIterations: &explicit,
	}, true); err == nil {
		t.Fatal("one output iteration was accepted")
	}
	policy.MaxIterations = 1
	if _, err := policy.ResolveForOutput(nil, true); err == nil {
		t.Fatal("installation maximum below output floor was accepted")
	}
}

func TestLimitPolicyResolvesHostToolIterationFloor(t *testing.T) {
	policy := DefaultLimitPolicy()
	resolved, err := policy.ResolveForFeatures(nil, false, true)
	if err != nil || resolved.MaxIterations != 3 {
		t.Fatalf("host tool default iterations = %d, error = %v", resolved.MaxIterations, err)
	}
	explicit := 1
	if _, err := policy.ResolveForFeatures(&InvocationLimitInput{
		MaxIterations: &explicit,
	}, false, true); err == nil {
		t.Fatal("one host tool iteration was accepted")
	}
}

func TestBudgetValidationRejectsUnsafeNumbers(t *testing.T) {
	for name, cost := range map[string]float64{
		"zero": 0, "negative": -1, "nan": math.NaN(), "infinite": math.Inf(1), "over precision": 0.1234567,
	} {
		t.Run(name, func(t *testing.T) {
			input := validServiceInput()
			input.Spec.Limits = &InvocationLimitInput{MaxEstimatedCostUSD: &cost}
			if err := ValidateCreateInvocation(input); err == nil {
				t.Fatal("invalid cost budget was accepted")
			}
		})
	}
}

func TestFingerprintV2MakesRequestedLimitsMaterial(t *testing.T) {
	input := validServiceInput()
	omitted, err := InvocationFingerprintV2(input)
	if err != nil {
		t.Fatalf("omitted fingerprint: %v", err)
	}
	wall := int64(DefaultLimitPolicy().DefaultTotalTimeout / time.Second)
	input.Spec.Limits = &InvocationLimitInput{TotalTimeoutSeconds: &wall}
	explicit, err := InvocationFingerprintV2(input)
	if err != nil {
		t.Fatalf("explicit fingerprint: %v", err)
	}
	if omitted == explicit {
		t.Fatal("explicit default matched omitted budget")
	}
}

func TestFingerprintV3CanonicalizesStructuredOutput(t *testing.T) {
	input := validServiceInput()
	omitted, err := InvocationFingerprintV3(input)
	if err != nil {
		t.Fatalf("omitted output fingerprint: %v", err)
	}
	input.Spec.Output = &StructuredOutputSpec{
		Schema: json.RawMessage(`{"type":"object","properties":{"score":{"type":"number","minimum":1}}}`),
	}
	first, err := InvocationFingerprintV3(input)
	if err != nil {
		t.Fatalf("first output fingerprint: %v", err)
	}
	input.Spec.Output.Schema = json.RawMessage(`{"properties":{"score":{"minimum":1.0,"type":"number"}},"type":"object"}`)
	second, err := InvocationFingerprintV3(input)
	if err != nil {
		t.Fatalf("equivalent output fingerprint: %v", err)
	}
	if first != second {
		t.Fatal("semantically equal output schemas produced different fingerprints")
	}
	if omitted == first {
		t.Fatal("adding output did not change fingerprint")
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
	if _, found, err := service.findIdempotent(context.Background(), "account", "partition", "agent", "key", input, input, v2); err != nil || !found {
		t.Fatalf("legacy replay found = %t, error = %v", found, err)
	}
	wall := int64(1800)
	input.Spec.Limits = &InvocationLimitInput{TotalTimeoutSeconds: &wall}
	v2, _ = InvocationFingerprintV2(input)
	if _, _, err := service.findIdempotent(context.Background(), "account", "partition", "agent", "key", input, input, v2); err == nil {
		t.Fatal("budget-bearing request matched legacy budgetless fingerprint")
	}
}

func TestV2FingerprintReplayRejectsAddedStructuredOutput(t *testing.T) {
	input := validServiceInput()
	v2, err := InvocationFingerprintV2(input)
	if err != nil {
		t.Fatalf("v2 fingerprint: %v", err)
	}
	store := &legacyFingerprintStore{
		invocation: domain.Invocation{
			FingerprintVersion: 2,
			RequestFingerprint: v2[:],
		},
	}
	service := &RuntimeService{
		store: store,
	}
	input.Spec.Output = &StructuredOutputSpec{
		Schema: json.RawMessage(`{"type":"object"}`),
	}
	v3, err := InvocationFingerprintV3(input)
	if err != nil {
		t.Fatalf("v3 fingerprint: %v", err)
	}
	if _, _, err := service.findIdempotent(
		context.Background(),
		"account",
		"partition",
		"agent",
		"key",
		input,
		input,
		v3,
	); err == nil {
		t.Fatal("output-bearing request matched a v2 row")
	}
}

func TestFingerprintV4CanonicalizesHostToolSchemasAndPreservesToolOrder(t *testing.T) {
	input := validServiceInput()
	input.Spec.Tools = []HostToolSpec{
		{
			Name:        "first",
			Description: "First tool",
			Mode:        "host",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"number","minimum":1}}}`),
		},
		{
			Name:        "second",
			Description: "Second tool",
			Mode:        "host",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	first, err := InvocationFingerprintV4(input)
	if err != nil {
		t.Fatalf("first host tool fingerprint: %v", err)
	}
	input.Spec.Tools[0].InputSchema = json.RawMessage(`{"properties":{"count":{"minimum":1.0,"type":"number"}},"type":"object"}`)
	equivalent, err := InvocationFingerprintV4(input)
	if err != nil {
		t.Fatalf("equivalent host tool fingerprint: %v", err)
	}
	if first != equivalent {
		t.Fatal("semantically equal host tool schemas produced different fingerprints")
	}
	input.Spec.Tools[0], input.Spec.Tools[1] = input.Spec.Tools[1], input.Spec.Tools[0]
	reordered, err := InvocationFingerprintV4(input)
	if err != nil {
		t.Fatalf("reordered host tool fingerprint: %v", err)
	}
	if first == reordered {
		t.Fatal("reordered host tools produced the same fingerprint")
	}
}

func TestV3FingerprintReplayRejectsAddedHostTools(t *testing.T) {
	input := validServiceInput()
	v3, err := InvocationFingerprintV3(input)
	if err != nil {
		t.Fatalf("v3 fingerprint: %v", err)
	}
	store := &legacyFingerprintStore{
		invocation: domain.Invocation{
			FingerprintVersion: 3,
			RequestFingerprint: v3[:],
		},
	}
	service := &RuntimeService{
		store: store,
	}
	input.Spec.Tools = []HostToolSpec{
		{
			Name:        "lookup",
			Description: "Look up data",
			Mode:        "host",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	v4, err := InvocationFingerprintV4(input)
	if err != nil {
		t.Fatalf("v4 fingerprint: %v", err)
	}
	if _, _, err := service.findIdempotent(
		context.Background(),
		"account",
		"partition",
		"agent",
		"key",
		input,
		input,
		v4,
	); err == nil {
		t.Fatal("tools-bearing request matched a v3 row")
	}
}

func TestExecutionDeadlineUsesRemainingActiveAndWallLimits(t *testing.T) {
	started := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	invocation := domain.Invocation{
		ActiveTimeoutMS: 10_000, ActiveExecutionMS: 7_000,
		DeadlineAt: started.Add(time.Hour),
	}
	deadline, scope, err := executionDeadline(invocation, started, 5*time.Second)
	if err != nil || scope != "active_execution" || !deadline.Equal(started.Add(3*time.Second)) {
		t.Fatalf("active deadline = %s %q, error = %v", deadline, scope, err)
	}
	invocation.DeadlineAt = started.Add(2 * time.Second)
	deadline, scope, err = executionDeadline(invocation, started, 5*time.Second)
	if err != nil || scope != "total" || !deadline.Equal(invocation.DeadlineAt) {
		t.Fatalf("wall deadline = %s %q, error = %v", deadline, scope, err)
	}
	invocation.ActiveExecutionMS = invocation.ActiveTimeoutMS
	if _, _, err := executionDeadline(invocation, started, 5*time.Second); err == nil {
		t.Fatal("exhausted active budget produced a claim deadline")
	}
}

type legacyFingerprintStore struct {
	admissionStore
	invocation domain.Invocation
}

func (s *legacyFingerprintStore) GetInvocationByIdempotencyKey(context.Context, string, string, string, string) (domain.Invocation, error) {
	return s.invocation, nil
}
