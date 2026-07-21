package executorhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestExecutorRoutesArePrivateAndMinimal(t *testing.T) {
	attempts := &fakeAttempts{}
	var logs bytes.Buffer
	handler := newHandler(attempts, slog.New(slog.NewJSONHandler(&logs, nil)), time.Second)

	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/health", http.StatusOK},
		{http.MethodPost, "/internal/execution-dispatches/dsp_test/attempts", http.StatusNoContent},
		{http.MethodGet, "/v1/invocations", http.StatusNotFound},
		{http.MethodPost, "/v1/invocations", http.StatusNotFound},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s status = %d, want %d", test.method, test.path, response.Code, test.status)
		}
	}
	if attempts.calls != 1 || attempts.lastID != "dsp_test" {
		t.Fatalf("attempt calls = %d, ID = %q", attempts.calls, attempts.lastID)
	}
	if !strings.Contains(logs.String(), `"event":"dispatch_attempt_decided"`) ||
		!strings.Contains(logs.String(), `"handler_outcome":"settled"`) {
		t.Fatalf("logs omit bounded executor outcome: %s", logs.String())
	}
}

func TestExecutorAcknowledgesPoisonBodyWithoutAttempt(t *testing.T) {
	attempts := &fakeAttempts{}
	var logs bytes.Buffer
	handler := newHandler(attempts, slog.New(slog.NewJSONHandler(&logs, nil)), time.Second)
	request := httptest.NewRequest(http.MethodPost, "/internal/execution-dispatches/dsp_test/attempts", strings.NewReader("unexpected"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || attempts.calls != 0 {
		t.Fatalf("status/calls = %d/%d", response.Code, attempts.calls)
	}
	if !strings.Contains(logs.String(), `"event":"dispatch_attempt_decided"`) ||
		!strings.Contains(logs.String(), `"handler_outcome":"poison_body"`) {
		t.Fatalf("logs omit bounded poison delivery outcome: %s", logs.String())
	}
}

func TestExecutorRetriesOnlyUndecidedAttempt(t *testing.T) {
	attempts := &fakeAttempts{err: errors.New("database unavailable")}
	handler := newHandler(attempts, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	request := httptest.NewRequest(http.MethodPost, "/internal/execution-dispatches/dsp_test/attempts", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if response.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", response.Header().Get("Retry-After"))
	}
}

func TestExecutorRetriesLiveDuplicateAttempt(t *testing.T) {
	attempts := &fakeAttempts{err: ports.ErrDispatchAttemptActive}
	handler := newHandler(attempts, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	request := httptest.NewRequest(http.MethodPost, "/internal/execution-dispatches/dsp_test/attempts", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("status/Retry-After = %d/%q, want 503/1", response.Code, response.Header().Get("Retry-After"))
	}
}

func TestExecutorCancelledRequestRemainsRetryable(t *testing.T) {
	attempts := &fakeAttempts{waitForContext: true}
	handler := newHandler(attempts, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/internal/execution-dispatches/dsp_test/attempts", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

type fakeAttempts struct {
	mu             sync.Mutex
	calls          int
	lastID         string
	err            error
	waitForContext bool
}

func (f *fakeAttempts) Attempt(ctx context.Context, id string) (services.DispatchAttemptOutcome, error) {
	f.mu.Lock()
	f.calls++
	f.lastID = id
	f.mu.Unlock()
	if f.waitForContext {
		<-ctx.Done()
		return services.DispatchAttemptNoop, ctx.Err()
	}
	return services.DispatchAttemptSettled, f.err
}
