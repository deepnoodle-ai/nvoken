package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
)

func TestMigrationPreflightIsReadOnlyAndMigrateRepeatsIt(t *testing.T) {
	databaseURL := diagnosticTestDatabase(t, false)
	ctx := context.Background()
	cfg := MigrationConfig{
		DatabaseURL:        databaseURL,
		Timeout:            5 * time.Second,
		TargetBuildVersion: "build-14",
		Mode:               postgres.UpgradeOrdinary,
	}
	if err := PreflightMigration(ctx, cfg); err != nil {
		t.Fatalf("empty database preflight: %v", err)
	}
	pool, err := postgres.OpenPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer pool.Close()
	before, err := postgres.InspectSchema(ctx, pool)
	if err != nil || before.State != postgres.SchemaEmpty {
		t.Fatalf("preflight mutated database = %#v, %v", before, err)
	}
	if err := Migrate(ctx, cfg); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	after, err := postgres.InspectSchema(ctx, pool)
	if err != nil || after.State != postgres.SchemaCompatible {
		t.Fatalf("migrated database = %#v, %v", after, err)
	}

	cfg.CurrentBuildVersion = "build-14"
	cfg.CurrentBinarySchemaVersion = after.Expected
	if err := Migrate(ctx, cfg); err != nil {
		t.Fatalf("no-op migrate with release pair: %v", err)
	}
}
