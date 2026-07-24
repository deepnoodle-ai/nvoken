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

const currentAdmissionFingerprintVersion = 7

const fingerprintVersionV1 = 1
const fingerprintVersionV2 = 2
const fingerprintVersionV3 = 3
const fingerprintVersionV4 = 4
const fingerprintVersionV5 = 5
const fingerprintVersionV6 = 6
const fingerprintVersionV7 = 7

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
	buffer.WriteString(strconv.Itoa(fingerprintVersionV1))
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
	if err := writeJSONString(&buffer, input.Spec.Model.ID); err != nil {
		return nil, err
	}
	buffer.WriteString(`}},"input":`)
	if err := writeFingerprintInput(&buffer, input.Input); err != nil {
		return nil, err
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func writeFingerprintInput(buffer *bytes.Buffer, input InvocationInput) error {
	buffer.WriteString(`{"content":[`)
	for index, block := range input.Content {
		if index > 0 {
			buffer.WriteByte(',')
		}
		buffer.WriteString(`{"type":"text","text":`)
		if err := writeJSONString(buffer, block.Text); err != nil {
			return err
		}
		buffer.WriteByte('}')
	}
	buffer.WriteString(`]}`)
	return nil
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
	canonical = strings.Replace(
		canonical,
		`{"version":`+strconv.Itoa(fingerprintVersionV1),
		`{"version":`+strconv.Itoa(fingerprintVersionV2),
		1,
	)
	needle := `}},"input":`
	index := strings.Index(canonical, needle)
	if index < 0 {
		return nil, fmt.Errorf("v1 fingerprint shape is invalid")
	}
	var budgets bytes.Buffer
	budgets.WriteString(`},"budgets":`)
	if input.Spec.Limits == nil {
		budgets.WriteString("null")
	} else {
		budgets.WriteString(`{"wall_clock_timeout_seconds":`)
		writeOptionalInt64(&budgets, input.Spec.Limits.TotalTimeoutSeconds)
		budgets.WriteString(`,"active_execution_timeout_seconds":`)
		writeOptionalInt64(&budgets, input.Spec.Limits.ActiveTimeoutSeconds)
		budgets.WriteString(`,"max_output_tokens":`)
		writeOptionalInt(&budgets, input.Spec.Limits.MaxOutputTokens)
		budgets.WriteString(`,"max_estimated_cost_microusd":`)
		if input.Spec.Limits.MaxEstimatedCostUSD == nil {
			budgets.WriteString("null")
		} else {
			value := *input.Spec.Limits.MaxEstimatedCostUSD
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("budget cost is not finite")
			}
			budgets.WriteString(strconv.FormatInt(int64(math.Round(value*1_000_000)), 10))
		}
		budgets.WriteString(`,"max_iterations":`)
		writeOptionalInt(&budgets, input.Spec.Limits.MaxIterations)
		budgets.WriteByte('}')
	}
	return []byte(canonical[:index] + budgets.String() + canonical[index+1:]), nil
}

// InvocationFingerprintV3 adds the structured-output contract. The schema is
// exact-canonical JSON so source member order and equivalent number spellings
// do not change idempotency identity.
func InvocationFingerprintV3(input CreateInvocationInput) ([sha256.Size]byte, error) {
	canonical, err := invocationFingerprintBytesV3(input)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func invocationFingerprintBytesV3(input CreateInvocationInput) ([]byte, error) {
	v2, err := invocationFingerprintBytesV2(input)
	if err != nil {
		return nil, err
	}
	canonical := string(v2)
	canonical = strings.Replace(
		canonical,
		`{"version":`+strconv.Itoa(fingerprintVersionV2),
		`{"version":`+strconv.Itoa(fingerprintVersionV3),
		1,
	)
	needle := `},"input":`
	// Do not carry this delimiter search forward mechanically: a later
	// version must build its fixed object directly because v3 schemas may
	// themselves contain an object member named "input".
	index := strings.Index(canonical, needle)
	if index < 0 {
		return nil, fmt.Errorf("v2 fingerprint shape is invalid")
	}
	var output bytes.Buffer
	output.WriteString(`,"output":`)
	if input.Spec.Output == nil {
		output.WriteString("null")
	} else {
		schema, err := canonicalJSON(input.Spec.Output.Schema)
		if err != nil {
			return nil, err
		}
		output.WriteString(`{"schema":`)
		output.Write(schema)
		output.WriteByte('}')
	}
	return []byte(canonical[:index] + output.String() + canonical[index:]), nil
}

// InvocationFingerprintV4 adds the ordered client-tool declarations. Schema
// objects are recursively canonicalized, while definition order remains
// material because providers observe that order.
func InvocationFingerprintV4(input CreateInvocationInput) ([sha256.Size]byte, error) {
	canonical, err := invocationFingerprintBytesV4(input)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func invocationFingerprintBytesV4(input CreateInvocationInput) ([]byte, error) {
	v3, err := invocationFingerprintBytesV3(input)
	if err != nil {
		return nil, err
	}
	canonical := string(v3)
	canonical = strings.Replace(
		canonical,
		`{"version":`+strconv.Itoa(fingerprintVersionV3),
		`{"version":`+strconv.Itoa(fingerprintVersionV4),
		1,
	)
	var inputSuffix bytes.Buffer
	inputSuffix.WriteString(`},"input":`)
	if err := writeFingerprintInput(&inputSuffix, input.Input); err != nil {
		return nil, err
	}
	inputSuffix.WriteByte('}')
	suffix := inputSuffix.Bytes()
	if !bytes.HasSuffix(v3, suffix) {
		return nil, fmt.Errorf("v3 fingerprint shape is invalid")
	}
	index := len(v3) - len(suffix)
	var tools bytes.Buffer
	tools.WriteString(`,"tools":[`)
	for toolIndex, tool := range input.Spec.Tools {
		if toolIndex > 0 {
			tools.WriteByte(',')
		}
		tools.WriteString(`{"name":`)
		if err := writeJSONString(&tools, tool.Name); err != nil {
			return nil, err
		}
		tools.WriteString(`,"description":`)
		if err := writeJSONString(&tools, tool.Description); err != nil {
			return nil, err
		}
		tools.WriteString(`,"mode":`)
		mode := tool.Mode
		if mode == "host" {
			mode = "client"
		}
		if err := writeJSONString(&tools, mode); err != nil {
			return nil, err
		}
		schema, err := canonicalJSON(tool.InputSchema)
		if err != nil {
			return nil, err
		}
		tools.WriteString(`,"input_schema":`)
		tools.Write(schema)
		tools.WriteByte('}')
	}
	tools.WriteByte(']')
	return []byte(canonical[:index] + tools.String() + canonical[index:]), nil
}

// InvocationFingerprintV5 adds callback routing to ordered tool declarations.
// Client tools encode callback as null so the fixed representation has one
// language-neutral shape for every tool mode.
func InvocationFingerprintV5(input CreateInvocationInput) ([sha256.Size]byte, error) {
	canonical, err := invocationFingerprintBytesV5(input)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func invocationFingerprintBytesV5(input CreateInvocationInput) ([]byte, error) {
	v3, err := invocationFingerprintBytesV3(input)
	if err != nil {
		return nil, err
	}
	canonical := string(v3)
	canonical = strings.Replace(
		canonical,
		`{"version":`+strconv.Itoa(fingerprintVersionV3),
		`{"version":`+strconv.Itoa(fingerprintVersionV5),
		1,
	)
	var inputSuffix bytes.Buffer
	inputSuffix.WriteString(`},"input":`)
	if err := writeFingerprintInput(&inputSuffix, input.Input); err != nil {
		return nil, err
	}
	inputSuffix.WriteByte('}')
	suffix := inputSuffix.Bytes()
	if !bytes.HasSuffix(v3, suffix) {
		return nil, fmt.Errorf("v3 fingerprint shape is invalid")
	}
	index := len(v3) - len(suffix)

	var tools bytes.Buffer
	tools.WriteString(`,"tools":[`)
	for toolIndex, tool := range input.Spec.Tools {
		if toolIndex > 0 {
			tools.WriteByte(',')
		}
		tools.WriteString(`{"name":`)
		if err := writeJSONString(&tools, tool.Name); err != nil {
			return nil, err
		}
		tools.WriteString(`,"description":`)
		if err := writeJSONString(&tools, tool.Description); err != nil {
			return nil, err
		}
		tools.WriteString(`,"mode":`)
		mode := tool.Mode
		if mode == "host" {
			mode = "client"
		}
		if err := writeJSONString(&tools, mode); err != nil {
			return nil, err
		}
		schema, err := canonicalJSON(tool.InputSchema)
		if err != nil {
			return nil, err
		}
		tools.WriteString(`,"input_schema":`)
		tools.Write(schema)
		tools.WriteString(`,"callback":`)
		if tool.Callback == nil {
			tools.WriteString("null")
		} else {
			tools.WriteString(`{"url":`)
			if err := writeJSONString(&tools, tool.Callback.URL); err != nil {
				return nil, err
			}
			tools.WriteByte('}')
		}
		tools.WriteByte('}')
	}
	tools.WriteByte(']')
	return []byte(canonical[:index] + tools.String() + canonical[index:]), nil
}

// InvocationFingerprintV6 adds the literal nonsecret provider credential
// selection outside the execution spec. Caller secret bytes and materialized
// defaults never enter the canonical representation.
func InvocationFingerprintV6(input CreateInvocationInput) ([sha256.Size]byte, error) {
	canonical, err := invocationFingerprintBytesV6(input)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func invocationFingerprintBytesV6(input CreateInvocationInput) ([]byte, error) {
	v5, err := invocationFingerprintBytesV5(input)
	if err != nil {
		return nil, err
	}
	canonical := string(v5)
	canonical = strings.Replace(
		canonical,
		`{"version":`+strconv.Itoa(fingerprintVersionV5),
		`{"version":`+strconv.Itoa(fingerprintVersionV6),
		1,
	)
	var inputSuffix bytes.Buffer
	inputSuffix.WriteString(`},"input":`)
	if err := writeFingerprintInput(&inputSuffix, input.Input); err != nil {
		return nil, err
	}
	inputSuffix.WriteByte('}')
	suffix := inputSuffix.Bytes()
	if !bytes.HasSuffix(v5, suffix) {
		return nil, fmt.Errorf("v5 fingerprint shape is invalid")
	}
	index := len(v5) - len(suffix)
	var selections bytes.Buffer
	selections.WriteString(`,"provider_credentials":`)
	if input.ProviderCredentials == nil {
		selections.WriteString("null")
	} else {
		selections.WriteByte('[')
		for selectionIndex, selection := range input.ProviderCredentials {
			if selectionIndex > 0 {
				selections.WriteByte(',')
			}
			selections.WriteString(`{"provider":`)
			if err := writeJSONString(&selections, selection.Provider); err != nil {
				return nil, err
			}
			selections.WriteString(`,"source":`)
			if err := writeJSONString(&selections, string(selection.Source)); err != nil {
				return nil, err
			}
			selections.WriteByte('}')
		}
		selections.WriteByte(']')
	}
	return []byte(canonical[:index] + selections.String() + canonical[index:]), nil
}

// InvocationFingerprintV7 adopts the public model.id, spec.limits, and host
// tool vocabulary. Older versions remain readable so retained rows can still
// be compared using the canonical contract under which they were admitted.
func InvocationFingerprintV7(input CreateInvocationInput) ([sha256.Size]byte, error) {
	canonical, err := invocationFingerprintBytesV7(input)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func invocationFingerprintBytesV7(input CreateInvocationInput) ([]byte, error) {
	v6, err := invocationFingerprintBytesV6(input)
	if err != nil {
		return nil, err
	}
	canonical := string(v6)
	canonical = strings.Replace(
		canonical,
		`{"version":`+strconv.Itoa(fingerprintVersionV6),
		`{"version":`+strconv.Itoa(fingerprintVersionV7),
		1,
	)
	canonical = strings.Replace(canonical, `,"name":`, `,"id":`, 1)
	canonical = strings.Replace(canonical, `},"budgets":`, `},"limits":`, 1)
	canonical = strings.ReplaceAll(canonical, `"mode":"client"`, `"mode":"host"`)
	needle := `,"active_execution_timeout_seconds":`
	canonical = strings.Replace(canonical, `"wall_clock_timeout_seconds":`, `"total_timeout_seconds":`, 1)
	canonical = strings.Replace(canonical, needle, `,"active_timeout_seconds":`, 1)
	waitingNeedle := `,"max_output_tokens":`
	var waiting bytes.Buffer
	waiting.WriteString(`,"waiting_timeout_seconds":`)
	if input.Spec.Limits == nil {
		waiting.WriteString("null")
	} else {
		writeOptionalInt64(&waiting, input.Spec.Limits.WaitingTimeoutSeconds)
	}
	canonical = strings.Replace(canonical, waitingNeedle, waiting.String()+waitingNeedle, 1)
	return []byte(canonical), nil
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
