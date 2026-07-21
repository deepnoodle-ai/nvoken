// Package signing implements nvoken callback signature version 1.
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

const (
	Version = "v1"

	SignatureHeader         = "X-Nvoken-Signature"
	SignatureVersionHeader  = "X-Nvoken-Signature-Version"
	TimestampHeader         = "X-Nvoken-Timestamp"
	DeliveryIDHeader        = "X-Nvoken-Delivery-Id"
	SigningKeyIDHeader      = "X-Nvoken-Signing-Key-Id"
	SigningKeyVersionHeader = "X-Nvoken-Signing-Key-Version"
	IdempotencyKeyHeader    = "Idempotency-Key"

	signaturePrefix = "sha256="
)

type HeaderParams struct {
	Signature  string
	Timestamp  int64
	DeliveryID string
	ToolCallID string
	KeyID      string
	KeyVersion int64
}

func Sign(key []byte, body []byte, deliveryID string, timestamp int64) string {
	mac := hmac.New(sha256.New, key)
	writeCanonical(mac, body, deliveryID, timestamp)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

func Verify(key []byte, body []byte, deliveryID string, timestamp int64, signature string) error {
	if len(signature) <= len(signaturePrefix) || signature[:len(signaturePrefix)] != signaturePrefix {
		return errors.New("signature must use sha256 prefix")
	}
	got, err := hex.DecodeString(signature[len(signaturePrefix):])
	if err != nil {
		return fmt.Errorf("signature must be hex: %w", err)
	}
	want := Sign(key, body, deliveryID, timestamp)
	wantBytes, err := hex.DecodeString(want[len(signaturePrefix):])
	if err != nil {
		return err
	}
	if !hmac.Equal(got, wantBytes) {
		return errors.New("signature mismatch")
	}
	return nil
}

func ApplyHeaders(header http.Header, params HeaderParams) {
	header.Set(SignatureHeader, params.Signature)
	header.Set(SignatureVersionHeader, Version)
	header.Set(TimestampHeader, strconv.FormatInt(params.Timestamp, 10))
	header.Set(DeliveryIDHeader, params.DeliveryID)
	header.Set(SigningKeyIDHeader, params.KeyID)
	header.Set(SigningKeyVersionHeader, strconv.FormatInt(params.KeyVersion, 10))
	header.Set(IdempotencyKeyHeader, params.ToolCallID)
}

func writeCanonical(writer io.Writer, body []byte, deliveryID string, timestamp int64) {
	_, _ = io.WriteString(writer, Version)
	_, _ = io.WriteString(writer, ".")
	_, _ = io.WriteString(writer, deliveryID)
	_, _ = io.WriteString(writer, ".")
	_, _ = io.WriteString(writer, strconv.FormatInt(timestamp, 10))
	_, _ = io.WriteString(writer, ".")
	_, _ = writer.Write(body)
}
