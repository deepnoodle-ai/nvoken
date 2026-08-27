package main

import (
	"bytes"
	"strings"
	"testing"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
)

func TestUsageTextUsesTurnVocabulary(t *testing.T) {
	var output bytes.Buffer
	metrics := nvoken.UsageMetrics{}
	metrics.Activity.Turns = 7
	metrics.Model.ModelCalls = 8
	metrics.Model.InputTokens = 9
	metrics.Model.OutputTokens = 10
	metrics.Tools.ToolCalls = 11
	metrics.Cost.ModelCost = nvoken.Money{Amount: "1.250000", Currency: "USD"}
	if err := writeUsageMetrics(&output, "all", metrics); err != nil {
		t.Fatalf("write usage metrics: %v", err)
	}
	if got := output.String(); !strings.Contains(got, "turns=7") || strings.Contains(got, "invocations=") {
		t.Fatalf("usage output = %q", got)
	}
}

func TestProviderKeyUsageTextUsesTurnVocabulary(t *testing.T) {
	var output bytes.Buffer
	usage := nvoken.ProviderKeyUsage{
		ID:       "385f4825-eca6-7768-907a-c2aa277ed80e",
		Provider: "future_provider",
		Scope:    "app",
		Turns:    3,
	}
	if err := writeProviderKeyUsage(&output, &usage); err != nil {
		t.Fatalf("write provider key usage: %v", err)
	}
	if got := output.String(); !strings.Contains(got, "turns=3") || strings.Contains(got, "invocations=") {
		t.Fatalf("provider key usage output = %q", got)
	}
}
