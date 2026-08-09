package nvoken

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const (
	MaxOutputSchemaBytes  = 32 * 1024
	MaxOutputSchemaDepth  = 16
	MaxOutputPatternBytes = 1024
	SchemaPreflightCode   = "schema_preflight_failed"
	schemaUnsupportedCode = "unsupported_keyword"
	schemaInvalidCode     = "invalid_schema"
	schemaLimitCode       = "limit_exceeded"
)

var outputSchemaKeywords = map[string]struct{}{
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
	"uniqueItems":          {},
	"minimum":              {},
	"maximum":              {},
}

type SchemaIssue struct {
	Code    string
	Path    string
	Keyword string
	Message string
}

func PreflightOutputSchema(schema map[string]any) error {
	payload, err := json.Marshal(schema)
	if err != nil {
		return outputSchemaError(schemaIssue{
			Code:    schemaInvalidCode,
			Message: "schema must be JSON serializable",
		})
	}
	if len(payload) > MaxOutputSchemaBytes {
		return outputSchemaError(schemaIssue{
			Code:    schemaLimitCode,
			Message: fmt.Sprintf("compact schema exceeds %d bytes", MaxOutputSchemaBytes),
		})
	}
	issue := validateOutputSchemaNode(schema, "", 1, true)
	if issue != nil {
		return outputSchemaError(*issue)
	}
	return nil
}

type schemaIssue = SchemaIssue

func outputSchemaError(issue schemaIssue) error {
	details := map[string]any{
		"kind": "structured_output_schema",
		"code": issue.Code,
		"path": issue.Path,
	}
	if issue.Keyword != "" {
		details["keyword"] = issue.Keyword
	}
	return &Error{
		Category: ErrorValidation,
		Code:     SchemaPreflightCode,
		Message:  "output schema is invalid: " + issue.Message,
		Details:  details,
	}
}

func validateOutputSchemaNode(
	node map[string]any,
	path string,
	depth int,
	root bool,
) *schemaIssue {
	if depth > MaxOutputSchemaDepth {
		return &schemaIssue{
			Code:    schemaLimitCode,
			Path:    path,
			Message: fmt.Sprintf("schema exceeds the maximum nesting depth of %d", MaxOutputSchemaDepth),
		}
	}
	keywords := make([]string, 0, len(node))
	for keyword := range node {
		keywords = append(keywords, keyword)
	}
	sort.Strings(keywords)
	for _, keyword := range keywords {
		if _, ok := outputSchemaKeywords[keyword]; !ok {
			return schemaMemberIssue(
				schemaUnsupportedCode,
				path,
				keyword,
				fmt.Sprintf("unsupported schema keyword %q", keyword),
			)
		}
	}
	typeName, ok := node["type"].(string)
	if !ok || !supportedOutputSchemaType(typeName) {
		return schemaMemberIssue(
			schemaInvalidCode,
			path,
			"type",
			"every schema position requires one supported string type",
		)
	}
	if root && typeName != "object" {
		return schemaMemberIssue(
			schemaInvalidCode,
			path,
			"type",
			"schema root type must be object",
		)
	}
	for _, keyword := range []string{"description", "title"} {
		if value, exists := node[keyword]; exists {
			if _, ok := value.(string); !ok {
				return schemaMemberIssue(
					schemaInvalidCode,
					path,
					keyword,
					fmt.Sprintf("schema %s must be a string", keyword),
				)
			}
		}
	}
	for _, keyword := range []string{"maxItems", "maxLength", "minItems", "minLength"} {
		if value, exists := node[keyword]; exists && !nonnegativeInteger(value) {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				keyword,
				fmt.Sprintf("schema %s must be a nonnegative integer", keyword),
			)
		}
	}
	for _, keyword := range []string{"maximum", "minimum"} {
		if value, exists := node[keyword]; exists && !jsonNumber(value) {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				keyword,
				fmt.Sprintf("schema %s must be a number", keyword),
			)
		}
	}
	if value, exists := node["uniqueItems"]; exists {
		if _, ok := value.(bool); !ok {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				"uniqueItems",
				"schema uniqueItems must be a boolean",
			)
		}
	}
	if issue := validateOutputSchemaBoundOrder(node, path); issue != nil {
		return issue
	}
	if typeName != "string" {
		if keyword := firstOutputSchemaKeyword(node, "maxLength", "minLength", "pattern"); keyword != "" {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				keyword,
				"string schema keywords require type string",
			)
		}
	}
	if typeName != "array" {
		if keyword := firstOutputSchemaKeyword(node, "maxItems", "minItems", "uniqueItems"); keyword != "" {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				keyword,
				"array schema bounds require type array",
			)
		}
	}
	if typeName != "number" && typeName != "integer" {
		if keyword := firstOutputSchemaKeyword(node, "maximum", "minimum"); keyword != "" {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				keyword,
				"numeric schema keywords require type number or integer",
			)
		}
	}
	if root {
		if _, exists := node["enum"]; exists {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				"enum",
				"schema root enum is not supported",
			)
		}
	}
	if pattern, exists := node["pattern"]; exists {
		text, ok := pattern.(string)
		if !ok {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				"pattern",
				"schema pattern must be a string",
			)
		}
		if len([]byte(text)) > MaxOutputPatternBytes {
			return schemaMemberIssue(
				schemaLimitCode,
				path,
				"pattern",
				fmt.Sprintf("schema pattern exceeds the maximum size of %d bytes", MaxOutputPatternBytes),
			)
		}
	}
	if enum, exists := node["enum"]; exists {
		if values, ok := schemaSlice(enum); !ok || len(values) == 0 {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				"enum",
				"schema enum must be a nonempty array",
			)
		}
	}
	properties, hasProperties := node["properties"]
	required, hasRequired := node["required"]
	additional, hasAdditional := node["additionalProperties"]
	if (hasProperties || hasRequired || hasAdditional) && typeName != "object" {
		keyword := firstOutputSchemaKeyword(
			node,
			"additionalProperties",
			"properties",
			"required",
		)
		return schemaMemberIssue(
			schemaInvalidCode,
			path,
			keyword,
			"object schema keywords require type object",
		)
	}
	propertyNames := map[string]struct{}{}
	if hasProperties {
		propertyMap, ok := properties.(map[string]any)
		if !ok {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				"properties",
				"schema properties must be an object",
			)
		}
		names := make([]string, 0, len(propertyMap))
		for name := range propertyMap {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child, ok := propertyMap[name].(map[string]any)
			childPath := schemaPointer(schemaPointer(path, "properties"), name)
			if !ok || len(child) == 0 {
				return &schemaIssue{
					Code:    schemaInvalidCode,
					Path:    childPath,
					Message: fmt.Sprintf("property %q must contain a schema object", name),
				}
			}
			propertyNames[name] = struct{}{}
			if issue := validateOutputSchemaNode(child, childPath, depth+1, false); issue != nil {
				return issue
			}
		}
	}
	if hasRequired {
		items, ok := schemaSlice(required)
		if !ok {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				"required",
				"schema required must be an array of property names",
			)
		}
		seen := map[string]struct{}{}
		for index, item := range items {
			itemPath := schemaPointer(schemaPointer(path, "required"), strconv.Itoa(index))
			name, ok := item.(string)
			if !ok || name == "" {
				return &schemaIssue{
					Code:    schemaInvalidCode,
					Path:    itemPath,
					Keyword: "required",
					Message: "schema required must contain nonempty strings",
				}
			}
			if _, duplicate := seen[name]; duplicate {
				return &schemaIssue{
					Code:    schemaInvalidCode,
					Path:    itemPath,
					Keyword: "required",
					Message: "schema required must not contain duplicates",
				}
			}
			if _, exists := propertyNames[name]; !exists {
				return &schemaIssue{
					Code:    schemaInvalidCode,
					Path:    itemPath,
					Keyword: "required",
					Message: fmt.Sprintf("required property %q is not declared", name),
				}
			}
			seen[name] = struct{}{}
		}
	}
	if hasAdditional {
		switch value := additional.(type) {
		case bool:
		case map[string]any:
			childPath := schemaPointer(path, "additionalProperties")
			if len(value) == 0 {
				return &schemaIssue{
					Code:    schemaInvalidCode,
					Path:    childPath,
					Keyword: "additionalProperties",
					Message: "additionalProperties must contain a schema object",
				}
			}
			if issue := validateOutputSchemaNode(value, childPath, depth+1, false); issue != nil {
				return issue
			}
		default:
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				"additionalProperties",
				"additionalProperties must be a boolean or schema object",
			)
		}
	}
	items, hasItems := node["items"]
	if typeName == "array" && !hasItems {
		return schemaMemberIssue(
			schemaInvalidCode,
			path,
			"items",
			"array schemas require items",
		)
	}
	if hasItems {
		if typeName != "array" {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				"items",
				"schema items requires type array",
			)
		}
		itemSchema, ok := items.(map[string]any)
		if !ok || len(itemSchema) == 0 {
			return schemaMemberIssue(
				schemaInvalidCode,
				path,
				"items",
				"schema items must be a schema object",
			)
		}
		childPath := schemaPointer(path, "items")
		if issue := validateOutputSchemaNode(itemSchema, childPath, depth+1, false); issue != nil {
			return issue
		}
	}
	return nil
}

func validateOutputSchemaBoundOrder(node map[string]any, path string) *schemaIssue {
	if compareSchemaNumbers(node["minLength"], node["maxLength"]) > 0 {
		return schemaMemberIssue(
			schemaInvalidCode,
			path,
			"maxLength",
			"schema minLength must not exceed maxLength",
		)
	}
	if compareSchemaNumbers(node["minItems"], node["maxItems"]) > 0 {
		return schemaMemberIssue(
			schemaInvalidCode,
			path,
			"maxItems",
			"schema minItems must not exceed maxItems",
		)
	}
	if compareSchemaNumbers(node["minimum"], node["maximum"]) > 0 {
		return schemaMemberIssue(
			schemaInvalidCode,
			path,
			"maximum",
			"schema minimum must not exceed maximum",
		)
	}
	return nil
}

func compareSchemaNumbers(left, right any) int {
	leftNumber, leftOK := schemaNumber(left)
	rightNumber, rightOK := schemaNumber(right)
	if !leftOK || !rightOK {
		return 0
	}
	return leftNumber.Cmp(rightNumber)
}

func schemaNumber(value any) (*big.Rat, bool) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	number, ok := new(big.Rat).SetString(string(payload))
	return number, ok
}

func nonnegativeInteger(value any) bool {
	number, ok := schemaNumber(value)
	return ok && number.Sign() >= 0 && number.IsInt()
}

func jsonNumber(value any) bool {
	kind := reflect.ValueOf(value)
	if !kind.IsValid() {
		return false
	}
	switch kind.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		_, ok := value.(json.Number)
		return ok
	}
}

func schemaSlice(value any) ([]any, bool) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() ||
		(reflected.Kind() != reflect.Array && reflected.Kind() != reflect.Slice) {
		return nil, false
	}
	items := make([]any, reflected.Len())
	for index := range items {
		items[index] = reflected.Index(index).Interface()
	}
	return items, true
}

func supportedOutputSchemaType(value string) bool {
	switch value {
	case "object", "array", "string", "number", "integer", "boolean":
		return true
	default:
		return false
	}
}

func firstOutputSchemaKeyword(node map[string]any, keywords ...string) string {
	for _, keyword := range keywords {
		if _, exists := node[keyword]; exists {
			return keyword
		}
	}
	return ""
}

func schemaMemberIssue(code, path, keyword, message string) *schemaIssue {
	return &schemaIssue{
		Code:    code,
		Path:    schemaPointer(path, keyword),
		Keyword: keyword,
		Message: message,
	}
}

func schemaPointer(path, member string) string {
	escaped := strings.ReplaceAll(member, "~", "~0")
	escaped = strings.ReplaceAll(escaped, "/", "~1")
	return path + "/" + escaped
}
