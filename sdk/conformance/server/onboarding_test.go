package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOnboardingAdmissionStreamsAcceptedResultAndEnd(t *testing.T) {
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
		t.Fatalf("open stream: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("stream status = %d: %s", response.StatusCode, body)
	}
	var eventTypes []string
	for _, frame := range strings.Split(string(body), "\n\n") {
		for _, line := range strings.Split(frame, "\n") {
			if value, found := strings.CutPrefix(line, "event: "); found {
				eventTypes = append(eventTypes, value)
			}
		}
	}
	if strings.Join(eventTypes, ",") != "invocation.accepted,invocation.result,stream.end" {
		t.Fatalf("event types = %#v, stream = %s", eventTypes, body)
	}
	if !strings.Contains(string(body), `"output_text":"world"`) {
		t.Fatalf("stream result = %s", body)
	}
}
