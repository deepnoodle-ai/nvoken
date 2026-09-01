package nvoken

import (
	"encoding/json"
	"fmt"
)

// requiredFrameKeys names the JSON members the contract requires on each
// stream payload this SDK decodes. scripts/check_go_frame_keys.py holds this
// table against the published OpenAPI contract.
var requiredFrameKeys = map[string][]string{
	"TranscriptUpdateEvent": {"type", "messages", "turn_changes", "has_more", "cursor"},
	"MessageDeltaEvent": {
		"type", "turn_id", "attempt", "message_id", "content_index", "offset",
		"kind", "delta", "emitted_at",
	},
	"StreamResyncEvent": {"type", "reason"},
	"TurnChange": {
		"turn_id", "conversation_id", "content_expires_at", "revision", "status", "terminal",
		"current", "through_message_sequence", "error", "structured_output", "occurred_at",
	},
	"ConversationMessage": {
		"id", "conversation_id", "content_expires_at", "turn_id", "sequence", "role", "content", "created_at",
	},
}

func requireFrameKeys(schema string, raw json.RawMessage) error {
	required, known := requiredFrameKeys[schema]
	if !known {
		return fmt.Errorf("no required-member list for %s", schema)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return fmt.Errorf("decode %s: %w", schema, err)
	}
	for _, name := range required {
		if _, present := members[name]; !present {
			return fmt.Errorf("%s is missing required member %q", schema, name)
		}
	}
	return nil
}

func requireFrameKeysEach(schema string, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return fmt.Errorf("decode %s list: %w", schema, err)
	}
	for _, element := range elements {
		if err := requireFrameKeys(schema, element); err != nil {
			return err
		}
	}
	return nil
}

func requireTranscriptUpdateKeys(raw json.RawMessage) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return fmt.Errorf("decode TranscriptUpdateEvent: %w", err)
	}
	for _, name := range requiredFrameKeys["TranscriptUpdateEvent"] {
		if _, present := members[name]; !present {
			return fmt.Errorf("TranscriptUpdateEvent is missing required member %q", name)
		}
	}
	if err := requireFrameKeysEach("ConversationMessage", members["messages"]); err != nil {
		return err
	}
	return requireFrameKeysEach("TurnChange", members["turn_changes"])
}
