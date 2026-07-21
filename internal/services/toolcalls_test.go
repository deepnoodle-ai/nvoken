package services

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

func TestToolRequestDigestPreservesExactJSONNumbers(t *testing.T) {
	left := json.RawMessage(`{"value":9007199254740992}`)
	right := json.RawMessage(`{"value":9007199254740993}`)
	leftDigest, err := toolRequestDigest("test", domain.ToolCallModeBuiltin, left)
	if err != nil {
		t.Fatalf("left digest: %v", err)
	}
	rightDigest, err := toolRequestDigest("test", domain.ToolCallModeBuiltin, right)
	if err != nil {
		t.Fatalf("right digest: %v", err)
	}
	if bytes.Equal(leftDigest, rightDigest) || jsonEqual(left, right) {
		t.Fatal("distinct integers above 2^53 converged")
	}
	if !jsonEqual(
		json.RawMessage(`{"value":100.20,"nested":{"b":2,"a":1}}`),
		json.RawMessage(`{"nested":{"a":1.0,"b":2e0},"value":1.002e2}`),
	) {
		t.Fatal("numerically equal JSON did not converge")
	}
}
