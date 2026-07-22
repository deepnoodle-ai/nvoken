package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/daemon"
)

var singleDaemonSchemaCounter atomic.Uint64

func TestSingleDaemonProfileExampleCoversAndLoadsSupportedConfiguration(t *testing.T) {
	values := singleDaemonProfileValues(t)
	expected := singleDaemonProfileKeys()
	if !reflect.DeepEqual(mapKeys(values), expected) {
		t.Fatalf("single-daemon environment keys =\n%v\nwant\n%v", mapKeys(values), expected)
	}
	applySingleDaemonProfile(t, values, "postgres://nvoken:secret@localhost/nvoken")

	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("load single-daemon profile: %v", err)
	}
	if cfg.ProcessRole != daemon.ProcessRoleCombined || cfg.InvocationExecutionMode != "embedded" {
		t.Fatalf("single-daemon topology = role %q, mode %q", cfg.ProcessRole, cfg.InvocationExecutionMode)
	}
	if cfg.RedisURL != "" || cfg.CloudTasks.Queue != "" {
		t.Fatalf("single-daemon profile configured an external delivery dependency: %#v", cfg)
	}
	if cfg.DatabaseMaxConns != 20 || cfg.Engine.Concurrency != 8 || cfg.ShutdownTimeout != time.Minute {
		t.Fatalf("single-daemon safety bounds = %#v", cfg)
	}
	if cfg.CallbackSigningKey != "" || cfg.CredentialPolicy.DeploymentMode != "self_hosted" ||
		cfg.CredentialPolicy.DefaultSource != "installation_byok" {
		t.Fatalf("single-daemon optional capabilities = %#v", cfg)
	}
}

func TestSingleDaemonProfileExamplePassesDiagnostic(t *testing.T) {
	databaseURL := singleDaemonTestDatabase(t)
	values := singleDaemonProfileValues(t)
	applySingleDaemonProfile(t, values, databaseURL)
	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("load single-daemon profile: %v", err)
	}
	if err := daemon.Diagnose(context.Background(), cfg); err != nil {
		t.Fatalf("diagnose single-daemon profile: %v", err)
	}
}

func singleDaemonProfileValues(t *testing.T) map[string]string {
	t.Helper()
	path := "../../deploy/single-daemon/nvoken.env.example"
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != key || key == "" {
			t.Fatalf("invalid environment example line %q", line)
		}
		if _, duplicate := values[key]; duplicate {
			t.Fatalf("duplicate environment example key %q", key)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan environment example: %v", err)
	}
	return values
}

func singleDaemonProfileKeys() []string {
	nonProfile := map[string]struct{}{
		"CLOUD_TASKS_DISPATCH_DEADLINE":    {},
		"CLOUD_TASKS_EXECUTOR_URL":         {},
		"CLOUD_TASKS_OIDC_AUDIENCE":        {},
		"CLOUD_TASKS_OIDC_SERVICE_ACCOUNT": {},
		"CLOUD_TASKS_QUEUE":                {},
		"DISPATCH_SYNTHETIC_ATTEMPT_DELAY": {},
		"EXECUTOR_ATTEMPT_TIMEOUT":         {},
		"PLATFORM_ANTHROPIC_API_KEY":       {},
		"PLATFORM_FUNDING_ENABLED":         {},
		"PLATFORM_OPENAI_API_KEY":          {},
		"REDIS_CA_CERT":                    {},
		"REDIS_PASSWORD":                   {},
		"REDIS_URL":                        {},
	}
	typeOfConfig := reflect.TypeOf(config{})
	keys := make([]string, 0, typeOfConfig.NumField())
	for index := 0; index < typeOfConfig.NumField(); index++ {
		key := typeOfConfig.Field(index).Tag.Get("env")
		if _, excluded := nonProfile[key]; !excluded {
			keys = append(keys, key)
		}
	}
	keys = append(keys, "MIGRATION_TIMEOUT")
	slices.Sort(keys)
	return keys
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func applySingleDaemonProfile(t *testing.T, values map[string]string, databaseURL string) {
	t.Helper()
	for _, key := range []string{
		"CLOUD_TASKS_DISPATCH_DEADLINE",
		"CLOUD_TASKS_EXECUTOR_URL",
		"CLOUD_TASKS_OIDC_AUDIENCE",
		"CLOUD_TASKS_OIDC_SERVICE_ACCOUNT",
		"CLOUD_TASKS_QUEUE",
		"DISPATCH_SYNTHETIC_ATTEMPT_DELAY",
		"EXECUTOR_ATTEMPT_TIMEOUT",
		"PLATFORM_ANTHROPIC_API_KEY",
		"PLATFORM_FUNDING_ENABLED",
		"PLATFORM_OPENAI_API_KEY",
		"REDIS_CA_CERT",
		"REDIS_PASSWORD",
		"REDIS_URL",
	} {
		unsetTestEnvironment(t, key)
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("BOOTSTRAP_OWNER_SECRET", "bootstrap-owner-secret-0123456789")
	t.Setenv("CREDENTIAL_DELIVERY_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("ANTHROPIC_API_KEY", "provider-secret")
}

func unsetTestEnvironment(t *testing.T, key string) {
	t.Helper()
	value, present := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func singleDaemonTestDatabase(t *testing.T) string {
	t.Helper()
	baseURL := os.Getenv("NVOKEN_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("NVOKEN_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := postgres.OpenPool(ctx, baseURL)
	if err != nil {
		t.Fatalf("open single-daemon test database: %v", err)
	}
	schema := fmt.Sprintf(
		"nvoken_single_daemon_test_%d_%d",
		time.Now().UnixNano(),
		singleDaemonSchemaCounter.Add(1),
	)
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatalf("create single-daemon test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE")
		admin.Close()
	})
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse single-daemon test database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	databaseURL := parsed.String()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := postgres.NewMigrator(databaseURL, 5*time.Second, logger).Apply(ctx); err != nil {
		t.Fatalf("migrate single-daemon test schema: %v", err)
	}
	return databaseURL
}
