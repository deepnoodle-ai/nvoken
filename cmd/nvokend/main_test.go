package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
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

func TestVersionDoesNotLoadConfiguration(t *testing.T) {
	t.Setenv("RUNTIME_API_KEY", "invalid")
	previous := buildVersion
	buildVersion = "0.1.1-test"
	t.Cleanup(func() { buildVersion = previous })

	var output bytes.Buffer
	if err := runWithOutput([]string{"--version"}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "0.1.1-test\n" {
		t.Fatalf("version output = %q", output.String())
	}

	output.Reset()
	if err := runWithOutput([]string{"version"}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "0.1.1-test\n" {
		t.Fatalf("version command output = %q", output.String())
	}
}

func TestHelpDoesNotLoadConfiguration(t *testing.T) {
	t.Setenv("RUNTIME_API_KEY", "invalid")
	var output bytes.Buffer
	if err := runWithOutput([]string{"--help"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "quickstart") || !strings.Contains(output.String(), "serve") {
		t.Fatalf("help output = %q", output.String())
	}
}

func TestRunLogsOneSafeConfigurationFailure(t *testing.T) {
	setServeConfig(t)
	t.Setenv("RUNTIME_API_KEY", "short-secret")

	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	if err := run([]string{"serve"}); err == nil {
		t.Fatal("run() succeeded with invalid configuration")
	}
	logText := output.String()
	if strings.Count(logText, `"event":"process_start_failed"`) != 1 ||
		!strings.Contains(logText, `"check":"configuration"`) ||
		!strings.Contains(logText, `"error_class":"invalid_configuration"`) {
		t.Fatalf("startup failure log = %s", logText)
	}
	if strings.Contains(logText, "short-secret") {
		t.Fatalf("startup failure log contains secret: %s", logText)
	}
}
