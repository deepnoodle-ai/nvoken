package nvoken

import (
	"encoding/json"
	"testing"
)

// A required bool is the case that motivated this. `encoding/json` leaves an
// absent member at its zero value, so a change that never carried `terminal`
// decoded as "not the end of the turn" and was indistinguishable from one that
// genuinely was not.
func TestReducerRefusesAChangeMissingTerminal(t *testing.T) {
	change := map[string]any{
		"invocation_id":            "inv_1",
		"revision":                 1,
		"status":                   "completed",
		"through_message_sequence": nil,
		"error":                    nil,
		"structured_output":        nil,
		"occurred_at":              "2026-08-15T00:00:00Z",
	}
	data, err := json.Marshal(map[string]any{
		"type":               "transcript.update",
		"session_id":         "sess_1",
		"messages":           []any{},
		"invocation_changes": []any{change},
		"cursor":             "cursor-1",
	})
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}

	reducer := NewReducer()
	if err := reducer.Apply(StreamEvent{Type: "transcript.update", Data: data}); err == nil {
		t.Fatal("reducer accepted a change with no terminal member")
	}

	change["terminal"] = true
	data, err = json.Marshal(map[string]any{
		"type":               "transcript.update",
		"session_id":         "sess_1",
		"messages":           []any{},
		"invocation_changes": []any{change},
		"cursor":             "cursor-1",
	})
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if err := reducer.Apply(StreamEvent{Type: "transcript.update", Data: data}); err != nil {
		t.Fatalf("reducer refused a complete change: %v", err)
	}
	if !reducer.Settled("inv_1") {
		t.Fatal("a complete terminal change did not settle the turn")
	}
}

// A required member holding null is present. Nullable-and-required is a real
// shape in this contract — `error` and `structured_output` are both — so the
// check must be about the member existing, not about what it holds.
func TestRequiredMemberHoldingNullIsPresent(t *testing.T) {
	if err := requireFrameKeys("StreamResyncEvent", json.RawMessage(
		`{"type":"stream.resync","session_id":"sess_1","reason":null}`,
	)); err != nil {
		t.Fatalf("null-valued required member rejected: %v", err)
	}
}

// Frames gain fields over time and a reader must keep going.
func TestUnknownMembersAreIgnored(t *testing.T) {
	if err := requireFrameKeys("StreamResyncEvent", json.RawMessage(
		`{"type":"stream.resync","session_id":"sess_1","reason":"live_delivery_gap","added_later":1}`,
	)); err != nil {
		t.Fatalf("unknown member rejected: %v", err)
	}
}
