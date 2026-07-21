package services

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const maxRecoveryCursorBytes = 4096

type collectionCursor struct {
	Version    int              `json:"version"`
	Kind       string           `json:"kind"`
	AccountID  string           `json:"account_id"`
	Filters    collectionFilter `json:"filters"`
	CreatedAt  time.Time        `json:"created_at"`
	ResourceID string           `json:"resource_id"`
}

type collectionFilter struct {
	TenantScope string `json:"tenant_scope"`
	SessionID   string `json:"session_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	SessionKey  string `json:"session_key,omitempty"`
	Status      string `json:"status,omitempty"`
}

type messageCursor struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	AccountID string `json:"account_id"`
	SessionID string `json:"session_id"`
	Sequence  int64  `json:"sequence"`
}

type transcriptPosition struct {
	MessageSequence   int64 `json:"message_sequence"`
	LifecycleRevision int64 `json:"lifecycle_revision"`
}

type transcriptCursor struct {
	Version   int                `json:"version"`
	Kind      string             `json:"kind"`
	AccountID string             `json:"account_id"`
	SessionID string             `json:"session_id"`
	Position  transcriptPosition `json:"position"`
}

type transcriptPageToken struct {
	Version   int                `json:"version"`
	Kind      string             `json:"kind"`
	AccountID string             `json:"account_id"`
	SessionID string             `json:"session_id"`
	Lower     transcriptPosition `json:"lower"`
	High      transcriptPosition `json:"high"`
}

func encodeRecoveryCursor(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode recovery cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeRecoveryCursor(encoded string, target any) error {
	if encoded == "" || len(encoded) > maxRecoveryCursorBytes {
		return errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) == 0 || len(payload) > maxRecoveryCursorBytes {
		return errors.New("invalid cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid cursor")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid cursor")
	}
	return nil
}

func encodeCollectionCursor(kind, accountID string, filters collectionFilter, createdAt time.Time, resourceID string) (string, error) {
	return encodeRecoveryCursor(collectionCursor{
		Version: 1, Kind: kind, AccountID: accountID, Filters: filters,
		CreatedAt: createdAt.UTC(), ResourceID: resourceID,
	})
}

func decodeCollectionCursor(encoded, kind, accountID string, filters collectionFilter) (time.Time, string, error) {
	var cursor collectionCursor
	if err := decodeRecoveryCursor(encoded, &cursor); err != nil || cursor.Version != 1 ||
		cursor.Kind != kind || cursor.AccountID != accountID || cursor.Filters != filters ||
		cursor.CreatedAt.IsZero() || cursor.ResourceID == "" {
		return time.Time{}, "", invalidRequest("cursor is invalid for this collection and filter set.")
	}
	return cursor.CreatedAt.UTC(), cursor.ResourceID, nil
}

func encodeMessageCursor(accountID, sessionID string, sequence int64) (string, error) {
	return encodeRecoveryCursor(messageCursor{
		Version: 1, Kind: "session_messages", AccountID: accountID,
		SessionID: sessionID, Sequence: sequence,
	})
}

func decodeMessageCursor(encoded, accountID, sessionID string) (int64, error) {
	var cursor messageCursor
	if err := decodeRecoveryCursor(encoded, &cursor); err != nil || cursor.Version != 1 ||
		cursor.Kind != "session_messages" || cursor.AccountID != accountID ||
		cursor.SessionID != sessionID || cursor.Sequence < 0 {
		return 0, invalidRequest("cursor is invalid for this Session.")
	}
	return cursor.Sequence, nil
}

func encodeTranscriptCursor(accountID, sessionID string, position transcriptPosition) (string, error) {
	return encodeRecoveryCursor(transcriptCursor{
		Version: 1, Kind: "session_transcript", AccountID: accountID,
		SessionID: sessionID, Position: position,
	})
}

func decodeTranscriptCursor(encoded, accountID, sessionID string) (transcriptPosition, error) {
	var cursor transcriptCursor
	if err := decodeRecoveryCursor(encoded, &cursor); err != nil || cursor.Version != 1 ||
		cursor.Kind != "session_transcript" || cursor.AccountID != accountID ||
		cursor.SessionID != sessionID || !cursor.Position.valid() {
		return transcriptPosition{}, invalidRequest("cursor is invalid for this Session.")
	}
	return cursor.Position, nil
}

func encodeTranscriptPageToken(accountID, sessionID string, lower, high transcriptPosition) (string, error) {
	return encodeRecoveryCursor(transcriptPageToken{
		Version: 1, Kind: "session_transcript_page", AccountID: accountID,
		SessionID: sessionID, Lower: lower, High: high,
	})
}

func decodeTranscriptPageToken(encoded, accountID, sessionID string) (transcriptPosition, transcriptPosition, error) {
	var token transcriptPageToken
	if err := decodeRecoveryCursor(encoded, &token); err != nil || token.Version != 1 ||
		token.Kind != "session_transcript_page" || token.AccountID != accountID ||
		token.SessionID != sessionID || !token.Lower.valid() || !token.High.valid() ||
		!token.Lower.atOrBefore(token.High) {
		return transcriptPosition{}, transcriptPosition{}, invalidRequest("page_token is invalid for this Session.")
	}
	return token.Lower, token.High, nil
}

func (p transcriptPosition) valid() bool {
	return p.MessageSequence >= 0 && p.LifecycleRevision >= 0
}

func (p transcriptPosition) atOrBefore(other transcriptPosition) bool {
	return p.MessageSequence <= other.MessageSequence && p.LifecycleRevision <= other.LifecycleRevision
}

func recoveryCursorEncodingError(err error) error {
	return &PublicError{Code: CodeInternal, Message: "The request could not be completed.", Cause: err}
}
