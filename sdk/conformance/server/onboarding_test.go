package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOnboardingAdmitsThenStreams walks the path a newcomer actually takes:
// a plain JSON POST that acknowledges the turn, then one stream filtered to it.
// `Accept: text/event-stream` on the admission selects nothing; that entry
// point is gone.
func TestOnboardingAdmitsThenStreams(t *testing.T) {
	state := newOnboardingState()
	server := httptest.NewServer(http.HandlerFunc(state.serve))
	t.Cleanup(server.Close)

	request, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/v1/invocations",
		strings.NewReader(`{
			"agent_key":"support",
			"idempotency_key":"onboarding-stream",
			"input":"hello",
			"model":{"provider":"openai","id":"gpt-test"}
		}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read acknowledgement: %v", err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("admit status = %d: %s", response.StatusCode, body)
	}
	var ack struct {
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &ack); err != nil {
		t.Fatalf("decode acknowledgement %s: %v", body, err)
	}
	if ack.ID == "" || ack.SessionID == "" {
		t.Fatalf("acknowledgement did not name the turn: %s", body)
	}

	streamRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/sessions/"+ack.SessionID+"/stream?invocation_id="+ack.ID,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	streamRequest.Header.Set("Authorization", "Bearer test-key")
	streamResponse, err := http.DefaultClient.Do(streamRequest)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	streamBody, err := io.ReadAll(streamResponse.Body)
	_ = streamResponse.Body.Close()
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if streamResponse.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d: %s", streamResponse.StatusCode, streamBody)
	}
	var eventTypes []string
	for _, frame := range strings.Split(string(streamBody), "\n\n") {
		for _, line := range strings.Split(frame, "\n") {
			if value, found := strings.CutPrefix(line, "event: "); found {
				eventTypes = append(eventTypes, value)
			}
		}
	}
	if strings.Join(eventTypes, ",") != "transcript.update" {
		t.Fatalf("event types = %#v, stream = %s", eventTypes, streamBody)
	}
	if !strings.Contains(string(streamBody), `"status":"completed"`) ||
		!strings.Contains(string(streamBody), `"text":"world"`) {
		t.Fatalf("stream = %s", streamBody)
	}
}
