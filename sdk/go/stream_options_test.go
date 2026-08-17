package nvoken

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionStreamStartsFromCursorAndNarrowsToInvocation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/v1/sessions/sesn_test/stream" {
			http.NotFound(writer, request)
			return
		}
		query := request.URL.Query()
		if query.Get("cursor") != "resume-after" || query.Get("invocation_id") != "inv_test" || query.Get("deltas") != "false" {
			t.Errorf("stream query = %q", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "id: settled\n")
		_, _ = fmt.Fprint(writer, "event: transcript.update\n")
		_, _ = fmt.Fprint(writer, `data: {"type":"transcript.update","session_id":"sesn_test","messages":[],"cursor":"settled","invocation_changes":[{"invocation_id":"inv_test","revision":1,"status":"completed","terminal":true,"through_message_sequence":null,"error":null,"structured_output":null,"occurred_at":"2026-08-16T12:00:00Z"}]}`+"\n\n")
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	deltas := false
	cursor := "resume-after"
	invocationID := "inv_test"
	err = client.StreamSessionWithOptions(context.Background(), "sesn_test", StreamOptions{
		Deltas:       &deltas,
		Cursor:       &cursor,
		InvocationID: &invocationID,
	}, func(StreamEvent, ReducedSnapshot) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}
