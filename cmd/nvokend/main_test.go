package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestCloudLoggingJSONShape(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{
		ReplaceAttr: cloudLoggingReplaceAttr,
	}))

	logger.Info("runtime ready", "invocation_id", "invk_test")

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode structured log: %v", err)
	}
	if entry["severity"] != "INFO" || entry["message"] != "runtime ready" {
		t.Fatalf("Cloud Logging fields = %#v", entry)
	}
	if entry["invocation_id"] != "invk_test" {
		t.Fatalf("operational fields = %#v", entry)
	}
	if _, ok := entry["msg"]; ok {
		t.Fatalf("unexpected default message key: %#v", entry)
	}
	if _, ok := entry["level"]; ok {
		t.Fatalf("unexpected default level key: %#v", entry)
	}
}
