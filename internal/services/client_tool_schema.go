package services

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

var clientToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateClientTools(tools []ClientToolSpec) error {
	if len(tools) > MaxClientTools {
		return invalidRequest(fmt.Sprintf("spec.tools must contain at most %d tools.", MaxClientTools))
	}
	seen := make(map[string]struct{}, len(tools))
	for index, tool := range tools {
		if len(tool.Name) == 0 || len(tool.Name) > MaxClientToolNameBytes ||
			!clientToolNamePattern.MatchString(tool.Name) || strings.HasPrefix(tool.Name, "nvoken_") {
			return invalidRequest(fmt.Sprintf("spec.tools[%d].name is invalid.", index))
		}
		if _, duplicate := seen[tool.Name]; duplicate {
			return invalidRequest("spec.tools names must be unique.")
		}
		seen[tool.Name] = struct{}{}
		if tool.Mode != "client" {
			return invalidRequest(fmt.Sprintf("spec.tools[%d].mode must be client.", index))
		}
		if !utf8.ValidString(tool.Description) || strings.TrimSpace(tool.Description) == "" ||
			utf8.RuneCountInString(tool.Description) > MaxClientToolDescriptionCharacters {
			return invalidRequest(fmt.Sprintf("spec.tools[%d].description is invalid.", index))
		}
		canonical, err := canonicalJSON(tool.InputSchema)
		if err != nil || len(canonical) > structuredoutput.MaxSchemaBytes {
			return invalidRequest(fmt.Sprintf("spec.tools[%d].input_schema is invalid.", index))
		}
		if _, err := structuredoutput.CompileSchema(json.RawMessage(canonical)); err != nil {
			return invalidRequest(fmt.Sprintf("spec.tools[%d].input_schema is invalid: %s.", index, err.Error()))
		}
	}
	return nil
}
