package nvoken

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"
)

const (
	conformanceInvocationID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
	conformanceSessionID    = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
	conformanceToolCallID   = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
	conformanceWaitID       = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328"
)

func TestConformance(t *testing.T) {
	baseURL := os.Getenv("NVOKEN_CONFORMANCE_URL")
	if baseURL == "" {
		t.Skip("NVOKEN_CONFORMANCE_URL is not set")
	}
	resetConformance(t, baseURL)
	client, err := NewClient(baseURL, "test-key", WithRetryPolicy(RetryPolicy{
		MaximumAttempts: 3,
		MinimumDelay:    time.Millisecond,
		MaximumDelay:    5 * time.Millisecond,
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := InvokeRequest{
		AgentRef:       "support",
		IdempotencyKey: "conformance-lost-ack",
		Input:          "hello",
		Spec: ExecutionSpec{
			Instructions: "help",
			Model: Model{
				Provider: "openai",
				Name:     "gpt-test",
			},
		},
	}
	handle, err := client.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("lost-ack admission retry: %v", err)
	}
	if handle.InvocationID != conformanceInvocationID || handle.SessionID != conformanceSessionID {
		t.Fatalf("unexpected durable handle: %#v", handle)
	}
	resumed, err := client.Resume(context.Background(), conformanceInvocationID)
	if err != nil || resumed.Status != InvocationCompleted {
		t.Fatalf("resume by ID: status=%v err=%v", resumed.Status, err)
	}

	waitHandle, err := client.Resume(context.Background(), conformanceWaitID)
	if err != nil {
		t.Fatal(err)
	}
	waitContext, cancelWait := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelWait()
	_, err = waitHandle.Wait(waitContext, WaitOptions{
		MinimumDelay: time.Millisecond,
		MaximumDelay: 2 * time.Millisecond,
	})
	var waitError *Error
	if !errors.As(err, &waitError) || waitError.Category != ErrorTimeout {
		t.Fatalf("wait should end locally with timeout, got %v", err)
	}

	firstPage, err := client.ListInvocations(context.Background(), ListInvocationsOptions{})
	if err != nil || !firstPage.HasMore || firstPage.NextCursor == nil {
		t.Fatalf("invocation cursor page: %#v err=%v", firstPage, err)
	}
	secondPage, err := client.ListInvocations(context.Background(), ListInvocationsOptions{
		Cursor: firstPage.NextCursor,
	})
	if err != nil || secondPage.HasMore {
		t.Fatalf("invocation cursor continuation: %#v err=%v", secondPage, err)
	}
	messagePage, err := client.ListMessages(context.Background(), conformanceSessionID, MessageListOptions{})
	if err != nil || !messagePage.HasMore || messagePage.NextCursor == nil {
		t.Fatalf("message cursor page: %#v err=%v", messagePage, err)
	}

	result, err := handle.SubmitToolResults(context.Background(), []ToolResult{{
		ToolCallID: conformanceToolCallID,
		Content:    map[string]any{"ok": true},
	}})
	if err != nil || len(result.Results) != 1 || !result.Results[0].Deduplicated {
		t.Fatalf("tool result replay: %#v err=%v", result, err)
	}
	cancelled, err := handle.Cancel(context.Background())
	if err != nil || cancelled.Status != InvocationCancelled {
		t.Fatalf("explicit cancel: %#v err=%v", cancelled, err)
	}

	assertGoError(t, client, "conflict", ErrorConflict, http.StatusConflict)
	if _, err := client.Get(context.Background(), "rate-limit"); err != nil {
		t.Fatalf("429 should be retried: %v", err)
	}
	assertGoError(t, client, "rate-limit-always", ErrorRateLimit, http.StatusTooManyRequests)
	assertGoError(t, client, "server-error", ErrorServer, http.StatusServiceUnavailable)

	streamHandle, err := client.Resume(context.Background(), conformanceInvocationID)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot ReducedSnapshot
	if err := streamHandle.Stream(context.Background(), func(_ StreamEvent, next ReducedSnapshot) error {
		snapshot = next
		return nil
	}); err != nil {
		t.Fatalf("resumable stream: %v", err)
	}
	if len(snapshot.Messages) != 2 || len(snapshot.InvocationChanges) != 2 || snapshot.ResumeCursor != "cursor-2" {
		t.Fatalf("unexpected reduced stream snapshot: %#v", snapshot)
	}
	var serverState struct {
		AdmissionAttempts int    `json:"admission_attempts"`
		ResultAttempts    int    `json:"result_attempts"`
		CancelAttempts    int    `json:"cancel_attempts"`
		StreamAttempts    int    `json:"stream_attempts"`
		LastEventID       string `json:"last_event_id"`
	}
	readJSON(t, baseURL+"/__test/state", &serverState)
	if serverState.AdmissionAttempts != 2 || serverState.ResultAttempts != 2 || serverState.CancelAttempts != 1 || serverState.StreamAttempts != 3 || serverState.LastEventID != "cursor-1" {
		t.Fatalf("fault server did not observe replay semantics: %#v", serverState)
	}
}

func TestSharedCallbackVector(t *testing.T) {
	var vector struct {
		Key     string            `json:"key"`
		Now     int64             `json:"now"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	decodeFile(t, "../../docs/design/callback-signing-v1.json", &vector)
	header := make(http.Header)
	for name, value := range vector.Headers {
		header.Set(name, value)
	}
	verified, err := VerifyCallback([]byte(vector.Key), header, []byte(vector.Body), time.Unix(vector.Now, 0))
	if err != nil || verified.ToolCallID != conformanceToolCallID {
		t.Fatalf("verify shared callback vector: %#v err=%v", verified, err)
	}
	for name, mutate := range map[string]func(http.Header, []byte) (http.Header, []byte){
		"body": func(headers http.Header, body []byte) (http.Header, []byte) {
			return headers, append(append([]byte(nil), body...), ' ')
		},
		"timestamp": func(headers http.Header, body []byte) (http.Header, []byte) {
			headers.Set("X-Nvoken-Timestamp", "1784635801")
			return headers, body
		},
		"delivery": func(headers http.Header, body []byte) (http.Header, []byte) {
			headers.Set("X-Nvoken-Delivery-ID", "different")
			return headers, body
		},
		"signature": func(headers http.Header, body []byte) (http.Header, []byte) {
			headers.Set("X-Nvoken-Signature", "sha256=00")
			return headers, body
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedHeader := header.Clone()
			changedHeader, changedBody := mutate(changedHeader, []byte(vector.Body))
			if _, err := VerifyCallback([]byte(vector.Key), changedHeader, changedBody, time.Unix(vector.Now, 0)); err == nil {
				t.Fatal("tampered callback was accepted")
			}
		})
	}
	store := &memoryResultStore{}
	first, duplicate, err := DeduplicateCallbackResult(context.Background(), store, conformanceToolCallID, json.RawMessage(`{"ok":true}`))
	if err != nil || duplicate {
		t.Fatalf("first result: %s duplicate=%v err=%v", first, duplicate, err)
	}
	stored, duplicate, err := DeduplicateCallbackResult(context.Background(), store, conformanceToolCallID, json.RawMessage(`{"ok":false}`))
	if err != nil || !duplicate || string(stored) != `{"ok":true}` {
		t.Fatalf("duplicate result: %s duplicate=%v err=%v", stored, duplicate, err)
	}
}

func TestSharedReducerVector(t *testing.T) {
	var fixture struct {
		Events []struct {
			ID    string          `json:"id"`
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		} `json:"events"`
		Expected struct {
			MessageSequences    []int64 `json:"message_sequences"`
			InvocationRevisions []int64 `json:"invocation_revisions"`
			ResumeCursor        string  `json:"resume_cursor"`
		} `json:"expected"`
	}
	decodeFile(t, "../conformance/fixtures/reducer.json", &fixture)
	reducer := NewReducer()
	for _, event := range fixture.Events {
		if err := reducer.Apply(StreamEvent{
			ID:   event.ID,
			Type: event.Event,
			Data: event.Data,
		}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := reducer.Snapshot()
	if len(snapshot.Messages) != len(fixture.Expected.MessageSequences) || len(snapshot.InvocationChanges) != len(fixture.Expected.InvocationRevisions) {
		t.Fatalf("reducer counts differ: %#v", snapshot)
	}
	for index, sequence := range fixture.Expected.MessageSequences {
		if snapshot.Messages[index].Sequence != sequence {
			t.Fatalf("message sequence %d = %d, want %d", index, snapshot.Messages[index].Sequence, sequence)
		}
	}
	for index, revision := range fixture.Expected.InvocationRevisions {
		if snapshot.InvocationChanges[index].Revision != revision {
			t.Fatalf("Invocation revision %d = %d, want %d", index, snapshot.InvocationChanges[index].Revision, revision)
		}
	}
	if snapshot.ResumeCursor != fixture.Expected.ResumeCursor {
		t.Fatalf("resume cursor = %q, want %q", snapshot.ResumeCursor, fixture.Expected.ResumeCursor)
	}
}

type memoryResultStore struct {
	value json.RawMessage
}

func (s *memoryResultStore) PutIfAbsent(_ context.Context, _ string, result json.RawMessage) (json.RawMessage, bool, error) {
	if s.value != nil {
		return s.value, false, nil
	}
	s.value = append(json.RawMessage(nil), result...)
	return s.value, true, nil
}

func assertGoError(t *testing.T, client *Client, invocationID string, category ErrorCategory, status int) {
	t.Helper()
	_, err := client.Get(context.Background(), invocationID)
	var typed *Error
	if !errors.As(err, &typed) || typed.Category != category || typed.Status != status || typed.RequestID == "" {
		t.Fatalf("typed error %s: %#v", invocationID, err)
	}
	if category == ErrorRateLimit && typed.RetryAfter != time.Second {
		t.Fatalf("typed rate-limit error did not preserve Retry-After: %#v", typed)
	}
}

func resetConformance(t *testing.T, baseURL string) {
	t.Helper()
	response, err := http.Post(baseURL+"/__test/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func readJSON(t *testing.T, url string, target any) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func decodeFile(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
