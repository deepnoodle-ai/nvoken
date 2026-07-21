// Package structuredoutput validates the bounded schema and value contract
// shared by admission, generation adapters, and terminal settlement.
package structuredoutput

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"

	"github.com/getkin/kin-openapi/openapi3"
)

const (
	MaxSchemaBytes   = 32 * 1024
	MaxSchemaDepth   = 16
	MaxValueBytes    = 256 * 1024
	MaxValueDepth    = 32
	ReservedToolName = "nvoken_submit_output"
	ProvenanceSource = "tool_call"
)

var allowedKeywords = map[string]struct{}{
	"type":                 {},
	"title":                {},
	"description":          {},
	"properties":           {},
	"required":             {},
	"additionalProperties": {},
	"items":                {},
	"enum":                 {},
	"pattern":              {},
	"minLength":            {},
	"maxLength":            {},
	"minItems":             {},
	"maxItems":             {},
	"minimum":              {},
	"maximum":              {},
}

type Compiled struct {
	schema *openapi3.Schema
}

func CompileSchema(raw json.RawMessage) (*Compiled, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, errors.New("schema must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("schema must be a JSON object")
	}
	if err := requireEOF(decoder); err != nil {
		return nil, errors.New("schema must contain one JSON value")
	}
	root, ok := value.(map[string]any)
	if !ok || len(root) == 0 {
		return nil, errors.New("schema must be a nonempty JSON object")
	}
	if err := validateSchemaNode(root, 1, true); err != nil {
		return nil, err
	}
	var schema openapi3.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("schema is invalid: %w", err)
	}
	if err := schema.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("schema is invalid: %w", err)
	}
	return &Compiled{schema: &schema}, nil
}

func (c *Compiled) ValidateValue(raw json.RawMessage) error {
	if c == nil || c.schema == nil {
		return errors.New("structured output schema is not compiled")
	}
	if len(raw) == 0 || len(raw) > MaxValueBytes || !json.Valid(raw) {
		return errors.New("structured output is not a bounded JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var exact any
	if err := decoder.Decode(&exact); err != nil {
		return errors.New("structured output is not valid JSON")
	}
	if err := requireEOF(decoder); err != nil {
		return errors.New("structured output must contain one JSON value")
	}
	if _, ok := exact.(map[string]any); !ok {
		return errors.New("structured output must be a JSON object")
	}
	if depth := jsonDepth(exact); depth > MaxValueDepth {
		return fmt.Errorf("structured output exceeds the maximum nesting depth of %d", MaxValueDepth)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return errors.New("structured output is not valid JSON")
	}
	if err := c.schema.VisitJSON(value); err != nil {
		return fmt.Errorf("structured output does not match schema: %w", err)
	}
	return nil
}

func validateSchemaNode(node map[string]any, depth int, root bool) error {
	if depth > MaxSchemaDepth {
		return fmt.Errorf("schema exceeds the maximum nesting depth of %d", MaxSchemaDepth)
	}
	for keyword := range node {
		if _, ok := allowedKeywords[keyword]; !ok {
			return fmt.Errorf("unsupported schema keyword %q", keyword)
		}
	}
	typeName, ok := node["type"].(string)
	if !ok || !supportedType(typeName) {
		return errors.New("every schema position requires one supported string type")
	}
	if root && typeName != "object" {
		return errors.New("schema root type must be object")
	}
	if err := validateAnnotations(node); err != nil {
		return err
	}
	if err := validateBounds(node); err != nil {
		return err
	}
	if err := validateKeywordApplicability(node, typeName, root); err != nil {
		return err
	}
	if pattern, exists := node["pattern"]; exists {
		text, ok := pattern.(string)
		if !ok {
			return errors.New("schema pattern must be a string")
		}
		if _, err := regexp.Compile(text); err != nil {
			return errors.New("schema pattern must be a valid regular expression")
		}
	}
	if enum, exists := node["enum"]; exists {
		values, ok := enum.([]any)
		if !ok || len(values) == 0 {
			return errors.New("schema enum must be a nonempty array")
		}
	}
	properties, hasProperties := node["properties"]
	required, hasRequired := node["required"]
	additional, hasAdditional := node["additionalProperties"]
	if hasProperties || hasRequired || hasAdditional {
		if typeName != "object" {
			return errors.New("object schema keywords require type object")
		}
	}
	propertyNames := map[string]struct{}{}
	if hasProperties {
		propertyMap, ok := properties.(map[string]any)
		if !ok {
			return errors.New("schema properties must be an object")
		}
		for name, child := range propertyMap {
			childMap, ok := child.(map[string]any)
			if !ok || len(childMap) == 0 {
				return fmt.Errorf("property %q must contain a schema object", name)
			}
			propertyNames[name] = struct{}{}
			if err := validateSchemaNode(childMap, depth+1, false); err != nil {
				return fmt.Errorf("property %q: %w", name, err)
			}
		}
	}
	if hasRequired {
		items, ok := required.([]any)
		if !ok {
			return errors.New("schema required must be an array of property names")
		}
		seen := map[string]struct{}{}
		for _, item := range items {
			name, ok := item.(string)
			if !ok || name == "" {
				return errors.New("schema required must contain nonempty strings")
			}
			if _, duplicate := seen[name]; duplicate {
				return errors.New("schema required must not contain duplicates")
			}
			if _, exists := propertyNames[name]; !exists {
				return fmt.Errorf("required property %q is not declared", name)
			}
			seen[name] = struct{}{}
		}
	}
	if hasAdditional {
		switch value := additional.(type) {
		case bool:
		case map[string]any:
			if err := validateSchemaNode(value, depth+1, false); err != nil {
				return fmt.Errorf("additionalProperties: %w", err)
			}
		default:
			return errors.New("additionalProperties must be a boolean or schema object")
		}
	}
	items, hasItems := node["items"]
	if typeName == "array" && !hasItems {
		return errors.New("array schemas require items")
	}
	if hasItems {
		if typeName != "array" {
			return errors.New("schema items requires type array")
		}
		itemSchema, ok := items.(map[string]any)
		if !ok || len(itemSchema) == 0 {
			return errors.New("schema items must be a schema object")
		}
		if err := validateSchemaNode(itemSchema, depth+1, false); err != nil {
			return fmt.Errorf("items: %w", err)
		}
	}
	return nil
}

func validateAnnotations(node map[string]any) error {
	for _, keyword := range []string{"title", "description"} {
		if value, exists := node[keyword]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("schema %s must be a string", keyword)
			}
		}
	}
	return nil
}

func validateBounds(node map[string]any) error {
	for _, keyword := range []string{"minLength", "maxLength", "minItems", "maxItems"} {
		if value, exists := node[keyword]; exists {
			number, ok := value.(json.Number)
			if !ok {
				return fmt.Errorf("schema %s must be a nonnegative integer", keyword)
			}
			integer, err := number.Int64()
			if err != nil || integer < 0 {
				return fmt.Errorf("schema %s must be a nonnegative integer", keyword)
			}
		}
	}
	for _, keyword := range []string{"minimum", "maximum"} {
		if value, exists := node[keyword]; exists {
			if _, ok := value.(json.Number); !ok {
				return fmt.Errorf("schema %s must be a number", keyword)
			}
		}
	}
	if min, minOK := integerKeyword(node, "minLength"); minOK {
		if max, maxOK := integerKeyword(node, "maxLength"); maxOK && min > max {
			return errors.New("schema minLength must not exceed maxLength")
		}
	}
	if min, minOK := integerKeyword(node, "minItems"); minOK {
		if max, maxOK := integerKeyword(node, "maxItems"); maxOK && min > max {
			return errors.New("schema minItems must not exceed maxItems")
		}
	}
	if minimum, minOK := numberKeyword(node, "minimum"); minOK {
		if maximum, maxOK := numberKeyword(node, "maximum"); maxOK && minimum.Cmp(maximum) > 0 {
			return errors.New("schema minimum must not exceed maximum")
		}
	}
	return nil
}

func validateKeywordApplicability(node map[string]any, typeName string, root bool) error {
	if hasAnyKeyword(node, "pattern", "minLength", "maxLength") && typeName != "string" {
		return errors.New("string schema keywords require type string")
	}
	if hasAnyKeyword(node, "minItems", "maxItems") && typeName != "array" {
		return errors.New("array schema bounds require type array")
	}
	if hasAnyKeyword(node, "minimum", "maximum") && typeName != "number" && typeName != "integer" {
		return errors.New("numeric schema keywords require type number or integer")
	}
	if root {
		if _, exists := node["enum"]; exists {
			return errors.New("schema root enum is not supported")
		}
	}
	return nil
}

func hasAnyKeyword(node map[string]any, keywords ...string) bool {
	for _, keyword := range keywords {
		if _, exists := node[keyword]; exists {
			return true
		}
	}
	return false
}

func integerKeyword(node map[string]any, keyword string) (int64, bool) {
	number, ok := node[keyword].(json.Number)
	if !ok {
		return 0, false
	}
	value, err := number.Int64()
	return value, err == nil
}

func numberKeyword(node map[string]any, keyword string) (*big.Float, bool) {
	number, ok := node[keyword].(json.Number)
	if !ok {
		return nil, false
	}
	precision := uint(len(number.String())*4 + 16)
	value, _, err := big.ParseFloat(number.String(), 10, precision, big.ToNearestEven)
	return value, err == nil
}

func supportedType(value string) bool {
	switch value {
	case "object", "array", "string", "number", "integer", "boolean":
		return true
	default:
		return false
	}
}

func jsonDepth(value any) int {
	switch current := value.(type) {
	case []any:
		depth := 1
		for _, item := range current {
			if candidate := 1 + jsonDepth(item); candidate > depth {
				depth = candidate
			}
		}
		return depth
	case map[string]any:
		depth := 1
		for _, item := range current {
			if candidate := 1 + jsonDepth(item); candidate > depth {
				depth = candidate
			}
		}
		return depth
	default:
		return 0
	}
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
