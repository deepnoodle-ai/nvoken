package structuredoutput

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaAndValueValidation(t *testing.T) {
	raw := json.RawMessage(`{
        "type":"object",
        "properties":{
            "const":{"type":"string","minLength":1},
            "items":{"type":"array","items":{"type":"integer"},"maxItems":2}
        },
        "required":["const"],
        "additionalProperties":false
    }`)
	compiled, err := CompileSchema(raw)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	if err := compiled.ValidateValue(json.RawMessage(`{"const":"ok","items":[1,2]}`)); err != nil {
		t.Fatalf("validate value: %v", err)
	}
	for name, value := range map[string]json.RawMessage{
		"missing required": json.RawMessage(`{"items":[]}`),
		"wrong type":       json.RawMessage(`{"const":7}`),
		"too many items":   json.RawMessage(`{"const":"ok","items":[1,2,3]}`),
		"extra property":   json.RawMessage(`{"const":"ok","extra":true}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := compiled.ValidateValue(value); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
}

func TestSchemaRejectsUnsupportedAndMalformedConstraints(t *testing.T) {
	tests := map[string]json.RawMessage{
		"empty":              json.RawMessage(`{}`),
		"non object root":    json.RawMessage(`{"type":"string"}`),
		"ref":                json.RawMessage(`{"type":"object","$ref":"#/$defs/x"}`),
		"nullable":           json.RawMessage(`{"type":"object","properties":{"x":{"type":"string","nullable":true}}}`),
		"type union":         json.RawMessage(`{"type":["object","null"]}`),
		"negative minLength": json.RawMessage(`{"type":"object","properties":{"x":{"type":"string","minLength":-1}}}`),
		"invalid pattern":    json.RawMessage(`{"type":"object","properties":{"x":{"type":"string","pattern":"["}}}`),
		"unknown required":   json.RawMessage(`{"type":"object","properties":{},"required":["x"]}`),
		"misplaced pattern":  json.RawMessage(`{"type":"object","pattern":"x"}`),
		"misplaced minimum":  json.RawMessage(`{"type":"object","minimum":1}`),
		"inverted numbers":   json.RawMessage(`{"type":"object","properties":{"x":{"type":"number","minimum":2,"maximum":1}}}`),
		"root enum":          json.RawMessage(`{"type":"object","enum":[{}]}`),
	}
	for name, schema := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := CompileSchema(schema); err == nil {
				t.Fatal("schema compiled")
			}
		})
	}
}

func TestSchemaAndValueDepthBoundaries(t *testing.T) {
	atLimit := nestedSchema(MaxSchemaDepth)
	compiled, err := CompileSchema(atLimit)
	if err != nil {
		t.Fatalf("schema at depth limit: %v", err)
	}
	if _, err := CompileSchema(nestedSchema(MaxSchemaDepth + 1)); err == nil ||
		!strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("schema past depth limit error = %v", err)
	}
	if err := compiled.ValidateValue(nestedValue(MaxSchemaDepth - 1)); err != nil {
		t.Fatalf("matching nested value: %v", err)
	}

	shallow, err := CompileSchema(json.RawMessage(`{"type":"object","additionalProperties":true}`))
	if err != nil {
		t.Fatalf("compile shallow schema: %v", err)
	}
	if err := shallow.ValidateValue(nestedValue(MaxValueDepth)); err != nil {
		t.Fatalf("value at depth limit: %v", err)
	}
	if err := shallow.ValidateValue(nestedValue(MaxValueDepth + 1)); err == nil ||
		!strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("value past depth limit error = %v", err)
	}
}

func nestedSchema(depth int) json.RawMessage {
	node := map[string]any{
		"type": "string",
	}
	for current := 1; current < depth; current++ {
		node = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"child": node,
			},
			"required": []string{"child"},
		}
	}
	raw, _ := json.Marshal(node)
	return raw
}

func nestedValue(nesting int) json.RawMessage {
	var value any = "leaf"
	for current := 0; current < nesting; current++ {
		value = map[string]any{
			"child": value,
		}
	}
	raw, _ := json.Marshal(value)
	return raw
}
