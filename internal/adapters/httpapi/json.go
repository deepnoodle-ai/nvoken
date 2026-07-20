package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/services"
)

const maxJSONNestingDepth = 16

func decodeInvocationRequest(w http.ResponseWriter, r *http.Request, target *services.CreateInvocationInput) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("Content-Type must be application/json.")
	}
	r.Body = http.MaxBytesReader(w, r.Body, services.MaxInvocationBodyBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if strings.Contains(err.Error(), "request body too large") || errors.As(err, &tooLarge) {
			return fmt.Errorf("Request body must be at most %d bytes.", services.MaxInvocationBodyBytes)
		}
		return fmt.Errorf("Request body could not be read.")
	}
	if len(payload) == 0 {
		return fmt.Errorf("Request body is required.")
	}
	if !utf8.Valid(payload) {
		return fmt.Errorf("Request body must contain valid UTF-8.")
	}
	if err := validateJSONSurrogates(payload); err != nil {
		return err
	}
	if err := validateJSONMembers(payload); err != nil {
		return err
	}
	if err := rejectNullOptionalStrings(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("Invalid request body: %v.", err)
	}
	if err := services.ValidateCreateInvocation(*target); err != nil {
		var public *services.PublicError
		if errors.As(err, &public) {
			return fmt.Errorf("%s", public.Message)
		}
		return fmt.Errorf("Invalid request body.")
	}
	return nil
}

// encoding/json replaces unpaired UTF-16 surrogate escapes with U+FFFD.
// Reject them before decoding so material request strings are never rewritten.
func validateJSONSurrogates(payload []byte) error {
	for index := 0; index < len(payload); {
		if payload[index] != '"' {
			index++
			continue
		}
		next, err := scanJSONStringSurrogates(payload, index+1)
		if err != nil {
			return err
		}
		index = next
	}
	return nil
}

func scanJSONStringSurrogates(payload []byte, index int) (int, error) {
	for index < len(payload) {
		switch payload[index] {
		case '"':
			return index + 1, nil
		case '\\':
			if index+1 >= len(payload) {
				return len(payload), nil
			}
			if payload[index+1] != 'u' {
				index += 2
				continue
			}
			codeUnit, ok := parseHexCodeUnit(payload, index+2)
			if !ok {
				return len(payload), nil
			}
			index += 6
			switch {
			case codeUnit >= 0xd800 && codeUnit <= 0xdbff:
				if index+6 > len(payload) || payload[index] != '\\' || payload[index+1] != 'u' {
					return 0, fmt.Errorf("Request body contains an unpaired JSON surrogate escape.")
				}
				low, ok := parseHexCodeUnit(payload, index+2)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return 0, fmt.Errorf("Request body contains an unpaired JSON surrogate escape.")
				}
				index += 6
			case codeUnit >= 0xdc00 && codeUnit <= 0xdfff:
				return 0, fmt.Errorf("Request body contains an unpaired JSON surrogate escape.")
			}
		default:
			index++
		}
	}
	return len(payload), nil
}

func parseHexCodeUnit(payload []byte, start int) (uint16, bool) {
	if start+4 > len(payload) {
		return 0, false
	}
	var value uint16
	for _, digit := range payload[start : start+4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func validateJSONMembers(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("Invalid request body: %v.", err)
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("Request body must be a JSON object.")
	}
	if err := scanJSONObject(decoder, 1); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("Request body must contain exactly one JSON value.")
		}
		return fmt.Errorf("Invalid request body: %v.", err)
	}
	return nil
}

func scanJSONObject(decoder *json.Decoder, depth int) error {
	if depth > maxJSONNestingDepth {
		return fmt.Errorf("Request body exceeds the maximum JSON nesting depth.")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("Invalid request body: %v.", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("Invalid request body: object member name must be a string.")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("Request body contains duplicate JSON member %q.", key)
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder, depth); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("Invalid request body: %v.", err)
	}
	if closing != json.Delim('}') {
		return fmt.Errorf("Invalid request body: expected object end.")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("Invalid request body: %v.", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return scanJSONObject(decoder, depth+1)
	case '[':
		if depth+1 > maxJSONNestingDepth {
			return fmt.Errorf("Request body exceeds the maximum JSON nesting depth.")
		}
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("Invalid request body: %v.", err)
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("Invalid request body: expected array end.")
		}
		return nil
	default:
		return fmt.Errorf("Invalid request body: unexpected delimiter.")
	}
}

func rejectNullOptionalStrings(payload []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return fmt.Errorf("Invalid request body: %v.", err)
	}
	for _, field := range []string{"tenant_ref", "session_id", "session_key"} {
		if raw, present := object[field]; present && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%s must be a string when supplied.", field)
		}
	}
	return nil
}
