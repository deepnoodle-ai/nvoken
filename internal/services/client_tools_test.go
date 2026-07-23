package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

func TestValidateSubmitHostToolResultsBoundaries(t *testing.T) {
	toolCallID := "tcal_019f84a5-7838-7b57-a180-000000000001"
	valid := SubmitHostToolResultsInput{
		Results: []HostToolResultInput{
			{
				ToolCallID: toolCallID,
				Content:    json.RawMessage(`{"value":true}`),
			},
		},
	}
	if err := ValidateSubmitHostToolResults(valid); err != nil {
		t.Fatalf("valid client result rejected: %v", err)
	}

	tests := map[string]func(*SubmitHostToolResultsInput){
		"empty": func(input *SubmitHostToolResultsInput) {
			input.Results = nil
		},
		"duplicate": func(input *SubmitHostToolResultsInput) {
			input.Results = append(input.Results, input.Results[0])
		},
		"invalid id": func(input *SubmitHostToolResultsInput) {
			input.Results[0].ToolCallID = "provider-call"
		},
		"invalid json": func(input *SubmitHostToolResultsInput) {
			input.Results[0].Content = json.RawMessage(`{`)
		},
		"oversized": func(input *SubmitHostToolResultsInput) {
			input.Results[0].Content = json.RawMessage(`"` + strings.Repeat("x", structuredoutput.MaxValueBytes) + `"`)
		},
		"too deep": func(input *SubmitHostToolResultsInput) {
			input.Results[0].Content = json.RawMessage(
				strings.Repeat("[", structuredoutput.MaxValueDepth+1) +
					"0" +
					strings.Repeat("]", structuredoutput.MaxValueDepth+1),
			)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := valid
			input.Results = append([]HostToolResultInput(nil), valid.Results...)
			mutate(&input)
			if err := ValidateSubmitHostToolResults(input); err == nil {
				t.Fatal("invalid client result was accepted")
			}
		})
	}
}
