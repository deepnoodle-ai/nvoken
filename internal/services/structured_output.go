package services

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

func structuredOutputSchemaDigest(schema json.RawMessage) ([]byte, error) {
	canonical, err := canonicalJSON(schema)
	if err != nil {
		return nil, fmt.Errorf("canonicalize schema: %w", err)
	}
	if len(canonical) > structuredoutput.MaxSchemaBytes {
		return nil, fmt.Errorf("compact schema exceeds %d bytes", structuredoutput.MaxSchemaBytes)
	}
	if _, err := structuredoutput.CompileSchema(canonical); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	return digest[:], nil
}
