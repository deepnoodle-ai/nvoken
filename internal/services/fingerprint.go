package services

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"unicode/utf8"
)

const fingerprintVersion = 1

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
	buffer.WriteString(fmt.Sprint(fingerprintVersion))
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
