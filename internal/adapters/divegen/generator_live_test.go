//go:build integration

package divegen

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

func TestLiveProviders(t *testing.T) {
	for _, test := range []struct {
		name, provider, keyEnv, modelEnv string
	}{
		{"Anthropic", "anthropic", "ANTHROPIC_API_KEY", "NVOKEN_ANTHROPIC_TEST_MODEL"},
		{"OpenAI", "openai", "OPENAI_API_KEY", "NVOKEN_OPENAI_TEST_MODEL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			key, model := os.Getenv(test.keyEnv), os.Getenv(test.modelEnv)
			if key == "" || model == "" {
				t.Skipf("set %s and %s to run this opt-in smoke test", test.keyEnv, test.modelEnv)
			}
			config := Config{}
			if test.provider == "anthropic" {
				config.AnthropicAPIKey = key
			} else {
				config.OpenAIAPIKey = key
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			response, err := New(config).Generate(ctx, domain.GenerationRequest{
				Instructions: "Answer with only the word ok.", Provider: test.provider, Model: model,
				Messages: []domain.GenerationMessage{{Role: domain.MessageRoleUser, Content: []byte(`[{"type":"text","text":"Reply now."}]`)}},
			})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if len(response.Messages) == 0 {
				t.Fatal("provider returned no assistant message")
			}
		})
	}
}
