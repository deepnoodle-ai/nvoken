package nvoken

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// reducerFixture is the shared client-side fold contract. All four SDKs read
// this one file, so a language whose reducer drifts fails here rather than in
// whichever consumer noticed first.
type reducerFixture struct {
	Events []struct {
		ID    string          `json:"id"`
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	} `json:"events"`
	Expected struct {
		MessageSequences []int64  `json:"message_sequences"`
		TurnRevisions    []int64  `json:"turn_revisions"`
		Cursor           string   `json:"cursor"`
		Previews         []string `json:"previews"`
	} `json:"expected"`
}

func TestSharedReducerFixtureFoldsToOneChangePerTurn(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "conformance", "fixtures", "reducer.json"))
	if err != nil {
		t.Fatalf("read reducer fixture: %v", err)
	}
	var fixture reducerFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode reducer fixture: %v", err)
	}

	reducer := NewReducer()
	for _, event := range fixture.Events {
		if err := reducer.Apply(StreamEvent{ID: event.ID, Type: event.Event, Data: event.Data}); err != nil {
			t.Fatalf("apply %s: %v", event.Event, err)
		}
	}

	snapshot := reducer.Snapshot()
	sequences := make([]int64, 0, len(snapshot.Messages))
	for _, message := range snapshot.Messages {
		sequences = append(sequences, message.Sequence)
	}
	if !equalInt64(sequences, fixture.Expected.MessageSequences) {
		t.Fatalf("message sequences = %v, want %v", sequences, fixture.Expected.MessageSequences)
	}

	// Two revisions arrived for one Turn. The fold keeps the highest and
	// discards the earlier one, so the log never grows a second entry that
	// also claims to be current.
	revisions := make([]int64, 0, len(snapshot.TurnChanges))
	for _, change := range snapshot.TurnChanges {
		revisions = append(revisions, change.Revision)
	}
	if !equalInt64(revisions, fixture.Expected.TurnRevisions) {
		t.Fatalf("turn revisions = %v, want %v", revisions, fixture.Expected.TurnRevisions)
	}
	if snapshot.Cursor != fixture.Expected.Cursor {
		t.Fatalf("cursor = %q, want %q", snapshot.Cursor, fixture.Expected.Cursor)
	}
	if len(snapshot.Previews) != len(fixture.Expected.Previews) {
		t.Fatalf("previews = %d, want %d", len(snapshot.Previews), len(fixture.Expected.Previews))
	}
	if !reducer.Settled(snapshot.TurnChanges[0].TurnID) {
		t.Fatal("the folded change carries a terminal status but the Turn is not settled")
	}
}

func equalInt64(actual, expected []int64) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}
