package nvoken

import (
	"encoding/json"
	"testing"
)

func TestReducerRefusesTurnChangeMissingTerminal(t *testing.T) {
	change := map[string]any{
		"turn_id":                  "476dd7be-97a1-78f3-8096-d7032468a80a",
		"conversation_id":          nil,
		"content_expires_at":       nil,
		"revision":                 1,
		"status":                   "completed",
		"current":                  true,
		"through_message_sequence": nil,
		"error":                    nil,
		"structured_output":        nil,
		"occurred_at":              "2026-08-15T00:00:00Z",
	}
	data, err := json.Marshal(map[string]any{
		"type":         "transcript.update",
		"messages":     []any{},
		"turn_changes": []any{change},
		"has_more":     false,
		"cursor":       "cursor-1",
	})
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}

	reducer := NewReducer()
	if err := reducer.Apply(StreamEvent{Type: "transcript.update", Data: data}); err == nil {
		t.Fatal("reducer accepted a Turn change with no terminal member")
	}

	change["terminal"] = true
	data, err = json.Marshal(map[string]any{
		"type":         "transcript.update",
		"messages":     []any{},
		"turn_changes": []any{change},
		"has_more":     false,
		"cursor":       "cursor-1",
	})
	if err != nil {
		t.Fatalf("marshal complete frame: %v", err)
	}
	if err := reducer.Apply(StreamEvent{Type: "transcript.update", Data: data}); err != nil {
		t.Fatalf("reducer refused a complete change: %v", err)
	}
	if !reducer.Settled("476dd7be-97a1-78f3-8096-d7032468a80a") {
		t.Fatal("complete terminal change did not settle the Turn")
	}
}

func TestRequiredNullableFrameMemberIsPresent(t *testing.T) {
	if err := requireFrameKeys("TurnChange", json.RawMessage(
		`{"turn_id":"476dd7be-97a1-78f3-8096-d7032468a80a","conversation_id":null,"content_expires_at":null,`+
			`"revision":1,"status":"running","terminal":false,"current":true,"through_message_sequence":null,`+
			`"error":null,"structured_output":null,"occurred_at":"2026-08-15T00:00:00Z"}`,
	)); err != nil {
		t.Fatalf("null-valued required member rejected: %v", err)
	}
}

func TestUnknownFrameMembersAreIgnored(t *testing.T) {
	if err := requireFrameKeys("StreamResyncEvent", json.RawMessage(
		`{"type":"stream.resync","reason":"live_delivery_gap","added_later":1}`,
	)); err != nil {
		t.Fatalf("unknown member rejected: %v", err)
	}
}
