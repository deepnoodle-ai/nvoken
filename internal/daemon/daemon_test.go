package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type componentFunc func(context.Context) error

func (f componentFunc) Run(ctx context.Context) error { return f(ctx) }

func TestRunComponentsCancelsAndJoinsSibling(t *testing.T) {
	wantErr := errors.New("server failed")
	siblingJoined := make(chan struct{})
	var cancelled atomic.Bool
	allJoined, err := runComponents(context.Background(), time.Second,
		componentFunc(func(context.Context) error { return wantErr }),
		componentFunc(func(ctx context.Context) error {
			<-ctx.Done()
			cancelled.Store(true)
			close(siblingJoined)
			return nil
		}),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runComponents error = %v", err)
	}
	if !allJoined {
		t.Fatal("components were not joined")
	}
	if !cancelled.Load() {
		t.Fatal("sibling was not cancelled and joined")
	}
	select {
	case <-siblingJoined:
	default:
		t.Fatal("sibling did not finish before return")
	}
}

func TestRunComponentsTreatsParentCancellationAsCleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	component := componentFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	joined, err := runComponents(ctx, time.Second, component, component)
	if err != nil {
		t.Fatalf("runComponents cancellation error = %v", err)
	}
	if !joined {
		t.Fatal("cancelled components were not joined")
	}
}

func TestRunComponentsBoundsUncooperativeShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	done := make(chan struct{})
	component := componentFunc(func(context.Context) error {
		defer close(done)
		<-release
		return nil
	})
	cancel()

	started := time.Now()
	joined, err := runComponents(ctx, 20*time.Millisecond, component)
	if err != nil {
		t.Fatalf("cancelled timeout error = %v", err)
	}
	if joined {
		t.Fatal("uncooperative component reported joined")
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Fatalf("shutdown elapsed = %s", elapsed)
	}
	close(release)
	<-done
}

func TestExecutionOwnerIsUniqueAndBounded(t *testing.T) {
	first, err := executionOwner()
	if err != nil {
		t.Fatal(err)
	}
	second, err := executionOwner()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || strings.TrimSpace(first) == "" {
		t.Fatalf("owners are not unique: %q and %q", first, second)
	}
	if len(first) > 255 {
		t.Fatalf("owner is %d bytes, want at most 255", len(first))
	}
}

func TestRuntimeTopologySeparatesSchedulersAndSurfaces(t *testing.T) {
	tests := []struct {
		name       string
		role       ProcessRole
		mode       services.InvocationExecutionMode
		cloudTasks bool
		want       runtimeTopology
	}{
		{
			name: "embedded combined", role: ProcessRoleCombined, mode: services.InvocationExecutionEmbedded,
			want: runtimeTopology{publicAPI: true, embeddedRunner: true},
		},
		{
			name: "embedded with synthetic dispatch control", role: ProcessRoleCombined, mode: services.InvocationExecutionEmbedded, cloudTasks: true,
			want: runtimeTopology{publicAPI: true, embeddedRunner: true, dispatchControl: true},
		},
		{
			name: "cloud tasks combined", role: ProcessRoleCombined, mode: services.InvocationExecutionCloudTasks, cloudTasks: true,
			want: runtimeTopology{publicAPI: true, reaper: true, dispatchControl: true},
		},
		{
			name: "private executor", role: ProcessRoleExecutor, mode: services.InvocationExecutionCloudTasks,
			want: runtimeTopology{privateExecutor: true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveRuntimeTopology(test.role, test.mode, test.cloudTasks)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("topology = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestRuntimeTopologyRejectsCloudModeWithoutDispatchControl(t *testing.T) {
	_, err := resolveRuntimeTopology(ProcessRoleCombined, services.InvocationExecutionCloudTasks, false)
	if err == nil || !strings.Contains(err.Error(), "requires Cloud Tasks") {
		t.Fatalf("topology error = %v", err)
	}
}

func TestProcessStartupIdentityIsSafeAndCompleteForBothRoles(t *testing.T) {
	expectedSchema, err := postgres.ExpectedSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []Config{
		{
			BuildVersion:            "build-combined",
			ProcessRole:             ProcessRoleCombined,
			InvocationExecutionMode: services.InvocationExecutionEmbedded,
			AnthropicAPIKey:         "anthropic-secret",
			CallbackSigningKey:      "callback-secret",
			RedisURL:                "rediss://redis.example.test",
		},
		{
			BuildVersion:            "build-executor",
			ProcessRole:             ProcessRoleExecutor,
			InvocationExecutionMode: services.InvocationExecutionCloudTasks,
			OpenAIAPIKey:            "openai-secret",
		},
	} {
		var output bytes.Buffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
		logProcessStarted(cfg, postgres.SchemaStatus{
			State:                      postgres.SchemaCompatible,
			Current:                    expectedSchema,
			Expected:                   expectedSchema,
			MinimumBinarySchemaVersion: expectedSchema,
			CompatibilitySchemaVersion: expectedSchema,
		})
		slog.SetDefault(previous)

		var entry map[string]any
		if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
			t.Fatalf("decode startup log: %v", err)
		}
		if entry["event"] != "process_started" || entry["build_version"] != cfg.BuildVersion ||
			entry["process_role"] != string(cfg.ProcessRole) ||
			entry["execution_mode"] != string(cfg.InvocationExecutionMode) ||
			entry["schema_version"] != float64(expectedSchema) ||
			entry["database_schema_version"] != float64(expectedSchema) ||
			entry["minimum_binary_schema_version"] != float64(expectedSchema) ||
			entry["schema_compatibility"] != string(postgres.SchemaCompatible) {
			t.Fatalf("startup identity = %#v", entry)
		}
		for _, secret := range []string{"anthropic-secret", "openai-secret", "callback-secret", "redis.example.test"} {
			if strings.Contains(output.String(), secret) {
				t.Fatalf("startup log contains secret or endpoint %q: %s", secret, output.String())
			}
		}
	}
}

func TestProcessStartupIdentityDefaultsLocalBuildVersion(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	logProcessStarted(Config{
		ProcessRole:             ProcessRoleCombined,
		InvocationExecutionMode: services.InvocationExecutionEmbedded,
	}, postgres.SchemaStatus{
		State:    postgres.SchemaCompatible,
		Current:  14,
		Expected: 14,
	})
	slog.SetDefault(previous)

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode startup log: %v", err)
	}
	if entry["build_version"] != "devel" {
		t.Fatalf("startup identity = %#v, want local devel build", entry)
	}
}

func TestSchemaStartupFailurePreservesBoundedVerdict(t *testing.T) {
	for _, state := range []postgres.SchemaState{
		postgres.SchemaEmpty,
		postgres.SchemaDirty,
		postgres.SchemaBehind,
		postgres.SchemaAhead,
		postgres.SchemaUnknown,
		postgres.SchemaInvalid,
	} {
		t.Run(string(state), func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			logSchemaProcessStartFailure(postgres.SchemaStatus{
				State:    state,
				Current:  12,
				Expected: 13,
				Dirty:    state == postgres.SchemaDirty,
			})
			slog.SetDefault(previous)

			var entry map[string]any
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatalf("decode startup failure log: %v", err)
			}
			if entry["event"] != "process_start_failed" || entry["check"] != "database_schema" ||
				entry["error_class"] != string(state) || entry["schema_version"] != float64(12) ||
				entry["expected_schema_version"] != float64(13) {
				t.Fatalf("schema startup failure = %#v", entry)
			}
		})
	}
}

func TestDiagnoseReportsUnreachableDatabaseWithoutLeakingConfiguration(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	err := diagnose(context.Background(), Config{
		DatabaseURL:       "postgres://operator:database-secret@127.0.0.1:1/nvoken",
		DatabaseMaxConns:  1,
		DiagnosticTimeout: 100 * time.Millisecond,
	}, logger)
	if err == nil {
		t.Fatal("diagnose succeeded against unreachable database")
	}
	logs := output.String()
	for _, required := range []string{
		`"component":"configuration","outcome":"success"`,
		`"component":"database_connectivity","outcome":"failed"`,
		`"component":"database_schema","outcome":"skipped"`,
		`"component":"live_event_redis","outcome":"skipped","error_class":"not_configured"`,
		`"component":"cloud_tasks_queue","outcome":"skipped","error_class":"not_configured"`,
	} {
		if !strings.Contains(logs, required) {
			t.Fatalf("diagnostic logs omit %s: %s", required, logs)
		}
	}
	for _, forbidden := range []string{"database-secret", "127.0.0.1", "postgres://"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("diagnostic logs contain %q: %s", forbidden, logs)
		}
	}
}
