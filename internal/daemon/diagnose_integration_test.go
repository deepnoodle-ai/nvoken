package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
)

var diagnosticSchemaCounter atomic.Uint64

func TestDiagnoseReportsEverySchemaVerdictWithoutMutation(t *testing.T) {
	expected, err := postgres.ExpectedSchemaVersion()
	if err != nil {
		t.Fatalf("expected schema version: %v", err)
	}
	tests := []struct {
		name        string
		migrate     bool
		mutate      func(context.Context, *pgxpool.Pool) error
		wantState   postgres.SchemaState
		wantSuccess bool
	}{
		{
			name:        "compatible",
			migrate:     true,
			wantState:   postgres.SchemaCompatible,
			wantSuccess: true,
		},
		{
			name:      "empty",
			wantState: postgres.SchemaEmpty,
		},
		{
			name:    "dirty",
			migrate: true,
			mutate: func(ctx context.Context, pool *pgxpool.Pool) error {
				_, err := pool.Exec(ctx, "UPDATE nvoken_schema_migrations SET dirty = true")
				return err
			},
			wantState: postgres.SchemaDirty,
		},
		{
			name:    "behind",
			migrate: true,
			mutate: func(ctx context.Context, pool *pgxpool.Pool) error {
				// Mirror the exact rows the previous release's final
				// migration leaves behind, compatibility row included.
				declarations, _, err := postgres.EmbeddedMigrationCompatibility()
				if err != nil {
					return err
				}
				previous := postgres.MigrationCompatibility{}
				for _, declaration := range declarations {
					if declaration.SchemaVersion == expected-1 {
						previous = declaration
					}
				}
				if previous.SchemaVersion == 0 {
					return fmt.Errorf("no compatibility declaration for schema %06d", expected-1)
				}
				if _, err := pool.Exec(ctx, "UPDATE nvoken_schema_migrations SET version = $1", previous.SchemaVersion); err != nil {
					return err
				}
				_, err = pool.Exec(ctx, `
					UPDATE nvoken_schema_compatibility
					SET schema_version = $1, minimum_binary_schema_version = $2
				`, previous.SchemaVersion, previous.MinimumBinarySchemaVersion)
				return err
			},
			wantState: postgres.SchemaBehind,
		},
		{
			name:    "ahead",
			migrate: true,
			mutate: func(ctx context.Context, pool *pgxpool.Pool) error {
				if _, err := pool.Exec(ctx, "UPDATE nvoken_schema_migrations SET version = $1", expected+1); err != nil {
					return err
				}
				_, err := pool.Exec(ctx, `
					UPDATE nvoken_schema_compatibility
					SET schema_version = $1, minimum_binary_schema_version = $1
				`, expected+1)
				return err
			},
			wantState: postgres.SchemaAhead,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			databaseURL := diagnosticTestDatabase(t, test.migrate)
			ctx := context.Background()
			pool, err := postgres.OpenPool(ctx, databaseURL)
			if err != nil {
				t.Fatalf("open diagnostic test database: %v", err)
			}
			t.Cleanup(pool.Close)
			if test.mutate != nil {
				if err := test.mutate(ctx, pool); err != nil {
					t.Fatalf("prepare schema state: %v", err)
				}
			}
			before, err := postgres.InspectSchema(ctx, pool)
			if err != nil || before.State != test.wantState {
				t.Fatalf("prepared schema status = %#v, error = %v", before, err)
			}

			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			diagnoseErr := diagnose(ctx, Config{
				DatabaseURL:       databaseURL,
				DatabaseMaxConns:  1,
				DiagnosticTimeout: 5 * time.Second,
			}, logger)
			if (diagnoseErr == nil) != test.wantSuccess {
				t.Fatalf("diagnose error = %v, want success %t", diagnoseErr, test.wantSuccess)
			}
			entry := diagnosticEntry(t, output.Bytes(), "database_schema")
			wantOutcome := "failed"
			if test.wantSuccess {
				wantOutcome = "success"
			}
			if entry["outcome"] != wantOutcome || entry["error_class"] != string(test.wantState) ||
				entry["expected_schema_version"] != float64(expected) {
				t.Fatalf("database schema diagnostic = %#v", entry)
			}

			after, err := postgres.InspectSchema(ctx, pool)
			if err != nil {
				t.Fatalf("inspect schema after diagnose: %v", err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("diagnose mutated schema: before %#v, after %#v", before, after)
			}
		})
	}
}

func diagnosticTestDatabase(t *testing.T, migrate bool) string {
	t.Helper()
	baseURL := os.Getenv("NVOKEN_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("NVOKEN_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := postgres.OpenPool(ctx, baseURL)
	if err != nil {
		t.Fatalf("open diagnostic test admin database: %v", err)
	}
	schema := fmt.Sprintf(
		"nvoken_diagnostic_test_%d_%d",
		time.Now().UnixNano(),
		diagnosticSchemaCounter.Add(1),
	)
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatalf("create diagnostic test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE")
		admin.Close()
	})

	schemaURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse diagnostic test database URL: %v", err)
	}
	query := schemaURL.Query()
	query.Set("search_path", schema)
	schemaURL.RawQuery = query.Encode()
	if migrate {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		if err := postgres.NewMigrator(schemaURL.String(), 5*time.Second, logger).Apply(ctx); err != nil {
			t.Fatalf("migrate diagnostic test schema: %v", err)
		}
	}
	return schemaURL.String()
}

func diagnosticEntry(t *testing.T, output []byte, component string) map[string]any {
	t.Helper()
	for _, line := range bytes.Split(output, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("decode diagnostic log: %v", err)
		}
		if entry["component"] == component {
			return entry
		}
	}
	t.Fatalf("diagnostic log omits component %q: %s", component, output)
	return nil
}
