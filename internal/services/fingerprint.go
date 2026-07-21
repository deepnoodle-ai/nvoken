package services

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const fingerprintVersionV1 = 1
const fingerprintVersionV2 = 2

// InvocationFingerprintV1 hashes a fixed JSON representation whose object-key
// order is part of the versioned contract. Values are already typed, so source
// JSON member order cannot affect the result.
func InvocationFingerprintV1(input CreateInvocationInput) ([sha256.Size]byte, error) {
	canonical, err := invocationFingerprintBytesV1(input)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func invocationFingerprintBytesV1(input CreateInvocationInput) ([]byte, error) {
	kind, value := "none", ""
	if input.SessionID != nil {
		kind, value = "id", *input.SessionID
	} else if input.SessionKey != nil {
		kind, value = "key", *input.SessionKey
	}

	var buffer bytes.Buffer
	buffer.WriteString(`{"version":`)
	buffer.WriteString(fmt.Sprint(fingerprintVersionV1))
	buffer.WriteString(`,"session_selector":{"kind":`)
	if err := writeJSONString(&buffer, kind); err != nil {
		return nil, err
	}
	buffer.WriteString(`,"value":`)
	if err := writeJSONString(&buffer, value); err != nil {
		return nil, err
	}
	buffer.WriteString(`},"spec":{"instructions":`)
	if err := writeJSONString(&buffer, input.Spec.Instructions); err != nil {
		return nil, err
	}
	buffer.WriteString(`,"model":{"provider":`)
	if err := writeJSONString(&buffer, input.Spec.Model.Provider); err != nil {
		return nil, err
	}
	buffer.WriteString(`,"name":`)
	if err := writeJSONString(&buffer, input.Spec.Model.Name); err != nil {
		return nil, err
	}
	buffer.WriteString(`}},"input":{"content":[`)
	for index, block := range input.Input.Content {
		if index > 0 {
			buffer.WriteByte(',')
		}
		buffer.WriteString(`{"type":"text","text":`)
		if err := writeJSONString(&buffer, block.Text); err != nil {
			return nil, err
		}
		buffer.WriteByte('}')
	}
	buffer.WriteString(`]}}`)
	return buffer.Bytes(), nil
}

// InvocationFingerprintV2 extends the fixed v1 representation with the
// requested budget object. Null means omitted; explicit defaults therefore
// remain material input even when admission resolves to the same limit.
func InvocationFingerprintV2(input CreateInvocationInput) ([sha256.Size]byte, error) {
	canonical, err := invocationFingerprintBytesV2(input)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func invocationFingerprintBytesV2(input CreateInvocationInput) ([]byte, error) {
	v1, err := invocationFingerprintBytesV1(input)
	if err != nil {
		return nil, err
	}
	// Reuse v1's language-neutral string encoding while changing the version
	// and inserting budgets at the end of spec before input.
	canonical := string(v1)
	canonical = strings.Replace(canonical, `{"version":1`, `{"version":2`, 1)
	needle := `}},"input":`
	index := strings.Index(canonical, needle)
	if index < 0 {
		return nil, fmt.Errorf("v1 fingerprint shape is invalid")
	}
	var budgets bytes.Buffer
	budgets.WriteString(`},"budgets":`)
	if input.Spec.Budgets == nil {
		budgets.WriteString("null")
	} else {
		budgets.WriteString(`{"wall_clock_timeout_seconds":`)
		writeOptionalInt64(&budgets, input.Spec.Budgets.WallClockTimeoutSeconds)
		budgets.WriteString(`,"active_execution_timeout_seconds":`)
		writeOptionalInt64(&budgets, input.Spec.Budgets.ActiveExecutionTimeoutSeconds)
		budgets.WriteString(`,"max_output_tokens":`)
		writeOptionalInt(&budgets, input.Spec.Budgets.MaxOutputTokens)
		budgets.WriteString(`,"max_estimated_cost_microusd":`)
		if input.Spec.Budgets.MaxEstimatedCostUSD == nil {
			budgets.WriteString("null")
		} else {
			value := *input.Spec.Budgets.MaxEstimatedCostUSD
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("budget cost is not finite")
			}
			budgets.WriteString(strconv.FormatInt(int64(math.Round(value*1_000_000)), 10))
		}
		budgets.WriteString(`,"max_iterations":`)
		writeOptionalInt(&budgets, input.Spec.Budgets.MaxIterations)
		budgets.WriteByte('}')
	}
	return []byte(canonical[:index] + budgets.String() + canonical[index+1:]), nil
}

func writeOptionalInt(buffer *bytes.Buffer, value *int) {
	if value == nil {
		buffer.WriteString("null")
		return
	}
	buffer.WriteString(strconv.Itoa(*value))
}

func writeOptionalInt64(buffer *bytes.Buffer, value *int64) {
	if value == nil {
		buffer.WriteString("null")
		return
	}
	buffer.WriteString(strconv.FormatInt(*value, 10))
}

func writeJSONString(buffer *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("canonical JSON string contains invalid UTF-8")
	}
	buffer.WriteByte('"')
	for _, valueRune := range value {
		switch valueRune {
		case '"':
			buffer.WriteString("\\\"")
		case '\\':
			buffer.WriteString("\\\\")
		case '\b':
			buffer.WriteString("\\b")
		case '\f':
			buffer.WriteString("\\f")
		case '\n':
			buffer.WriteString("\\n")
		case '\r':
			buffer.WriteString("\\r")
		case '\t':
			buffer.WriteString("\\t")
		default:
			if valueRune < 0x20 {
				_, _ = fmt.Fprintf(buffer, "\\u%04x", valueRune)
				continue
			}
			buffer.WriteRune(valueRune)
		}
	}
	buffer.WriteByte('"')
	return nil
}
