package nvoken

import (
	"encoding/json"
	"fmt"
)

// requiredFrameKeys names the JSON members the contract requires on each stream
// payload this SDK decodes.
//
// It is a second copy of facts the contract already states, which is normally
// the thing to avoid — so `TestRequiredFrameKeysMatchTheContract` holds it
// against `openapi/nvoken.yaml` and fails when they diverge. The copy exists
// because Go has no generated validator: `encoding/json` fills an absent member
// with the type's zero value, and for a required bool that is a confident wrong
// answer rather than a missing one. A change that never carried `terminal`
// decoded as "not the end of the turn" and read exactly like one that did.
//
// The other SDKs get this for free — serde in Rust, pydantic in Python, the
// generated `instanceOf` guards in TypeScript — so this is Go catching up to
// them rather than a rule of its own.
var requiredFrameKeys = map[string][]string{
	"TranscriptUpdateEvent": {"type", "session_id", "messages", "invocation_changes", "cursor"},
	"MessageDeltaEvent": {
		"type", "session_id", "invocation_id", "attempt", "message_id",
		"content_index", "kind", "delta", "emitted_at",
	},
	"StreamResyncEvent": {"type", "session_id", "reason"},
	// No ConnectionClosingEvent: this SDK never decodes one. The read loop
	// reconnects from its own cursor without reading the frame.
	"InvocationChange": {
		"invocation_id", "revision", "status", "terminal",
		"through_message_sequence", "error", "structured_output", "occurred_at",
	},
	"SessionMessage": {"id", "session_id", "invocation_id", "sequence", "role", "content", "created_at"},
}

// requireFrameKeys reports whether one payload carries every member the contract
// requires. A present member holding JSON null still counts: nullable required
// members are a real shape, and the check is about the member existing rather
// than about what it holds.
//
// Unknown members are ignored, which is the compatibility rule: frames gain
// fields over time and a decoder must keep reading. Requiring what the contract
// requires is the other half of that rule, not a contradiction of it.
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

// requireFrameKeysEach applies requireFrameKeys to every element of a JSON
// array member, or to nothing when the member is absent or null. The collections
// on a transcript.update are what the reducer folds on, so an element missing a
// member matters as much as the frame around it.
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

// requireTranscriptUpdateKeys validates the frame and both collections it
// carries in one parse of the payload.
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
	if err := requireFrameKeysEach("SessionMessage", members["messages"]); err != nil {
		return err
	}
	return requireFrameKeysEach("InvocationChange", members["invocation_changes"])
}
