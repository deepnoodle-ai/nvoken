package services

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

const MaxCallbackURLBytes = 2048

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
		switch tool.Mode {
		case string(domain.ToolCallModeClient):
			if tool.Callback != nil || tool.callbackSet {
				return invalidRequest(fmt.Sprintf("spec.tools[%d].callback is allowed only for callback mode.", index))
			}
		case string(domain.ToolCallModeCallback):
			if tool.Callback == nil || !validCallbackURL(tool.Callback.URL) {
				return invalidRequest(fmt.Sprintf("spec.tools[%d].callback.url is invalid.", index))
			}
		default:
			return invalidRequest(fmt.Sprintf("spec.tools[%d].mode must be client or callback.", index))
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

func validCallbackURL(value string) bool {
	if value == "" || len(value) > MaxCallbackURLBytes || !utf8.ValidString(value) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.Fragment == "" && parsed.Opaque == ""
}

func hasCallbackTools(tools []ClientToolSpec) bool {
	for _, tool := range tools {
		if tool.Mode == string(domain.ToolCallModeCallback) {
			return true
		}
	}
	return false
}
