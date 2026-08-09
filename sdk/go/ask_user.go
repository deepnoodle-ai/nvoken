package nvoken

import "encoding/json"

// A structured question to the end user is a host tool, not a new resource.
// The park/webhook/resume machinery already *is* "block until someone answers",
// so nvoken does not need a pending-interaction state or a response endpoint to
// deliver this. What it does need to supply is a standard shape, so the model
// and the host UI agree on what a question looks like without every
// integration inventing its own.
//
// This is a convention, not runtime behaviour: nvoken treats AskUserToolName
// like any other host tool. Adopting it costs nothing and means a host UI
// written against one agent renders questions from another. The shape matches
// dive's toolkit ask_user, so an agent already written against that needs no
// translation layer.
const (
	// AskUserToolName is the well-known name. It carries no `nvoken_` prefix
	// because the host executes it, not the runtime.
	AskUserToolName = "ask_user"

	AskUserKindConfirm     = "confirm"
	AskUserKindSelect      = "select"
	AskUserKindMultiselect = "multiselect"
	AskUserKindInput       = "input"
)

// AskUserInput is what the model sends.
type AskUserInput struct {
	Question  string          `json:"question"`
	Type      string          `json:"type"`
	Options   []AskUserOption `json:"options,omitempty"`
	Default   string          `json:"default,omitempty"`
	MinSelect int             `json:"min_select,omitempty"`
	MaxSelect int             `json:"max_select,omitempty"`
	Multiline bool            `json:"multiline,omitempty"`
}

type AskUserOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// AskUserOutput is what the host returns as the tool result. Canceled is not an
// error: a user declining to answer is a legitimate outcome the model should
// see and reason about, whereas an error result would read as a broken tool.
type AskUserOutput struct {
	Response string   `json:"response,omitempty"`
	Values   []string `json:"values,omitempty"`
	Canceled bool     `json:"canceled"`
}

// AskUserInputSchema is the tool input schema, in the bounded subset nvoken
// admits. Declare a host tool with this schema and the model will produce
// questions an AskUserInput can decode.
func AskUserInputSchema() map[string]any {
	var schema map[string]any
	if err := json.Unmarshal(askUserInputSchemaJSON, &schema); err != nil {
		panic("nvoken: ask_user input schema is invalid: " + err.Error())
	}
	return schema
}

var askUserInputSchemaJSON = json.RawMessage(`{
  "type": "object",
  "properties": {
    "question": {"type": "string", "minLength": 1, "maxLength": 2000,
      "description": "The question to put to the user."},
    "type": {"type": "string", "enum": ["confirm", "select", "multiselect", "input"],
      "description": "How the user answers."},
    "options": {
      "type": "array",
      "maxItems": 20,
      "description": "Choices for select and multiselect. Ignored otherwise.",
      "items": {
        "type": "object",
        "properties": {
          "value": {"type": "string", "minLength": 1, "maxLength": 200},
          "label": {"type": "string", "minLength": 1, "maxLength": 200},
          "description": {"type": "string", "maxLength": 500},
          "default": {"type": "boolean"}
        },
        "required": ["value", "label"],
        "additionalProperties": false
      }
    },
    "default": {"type": "string", "maxLength": 2000,
      "description": "Pre-filled answer: \"true\"/\"false\" for confirm, text for input."},
    "min_select": {"type": "integer", "minimum": 0, "maximum": 20},
    "max_select": {"type": "integer", "minimum": 0, "maximum": 20},
    "multiline": {"type": "boolean"}
  },
  "required": ["question", "type"],
  "additionalProperties": false
}`)

// AskUserTool is a ready-to-use host tool declaration. Attach a handler that
// renders the question and returns an AskUserOutput.
func AskUserTool(description string, handler ToolHandler) Tool {
	if description == "" {
		description = "Ask the user a question and wait for their answer. " +
			"Use it when a decision is genuinely theirs to make, not to " +
			"confirm work you can verify yourself."
	}
	return Tool{
		Mode:        ToolModeHost,
		Name:        AskUserToolName,
		Description: description,
		InputSchema: AskUserInputSchema(),
		Handler:     handler,
	}
}
