package postgres

import (
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestEmbeddedMigrationVersions(t *testing.T) {
	driver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("open embedded migrations: %v", err)
	}
	defer func() { _ = driver.Close() }()

	versions, err := migrationVersions(driver)
	if err != nil {
		t.Fatalf("migrationVersions: %v", err)
	}
	if len(versions) != 14 || versions[0] != 1 || versions[1] != 2 || versions[2] != 3 || versions[3] != 4 || versions[4] != 5 || versions[5] != 6 || versions[6] != 7 || versions[7] != 8 || versions[8] != 9 || versions[9] != 10 || versions[10] != 11 || versions[11] != 12 || versions[12] != 13 || versions[13] != 14 {
		t.Fatalf("migration versions = %v, want [1 2 3 4 5 6 7 8 9 10 11 12 13 14]", versions)
	}
}

func TestEveryPostTransitionMigrationDeclaresCompatibility(t *testing.T) {
	declarations, transition, err := EmbeddedMigrationCompatibility()
	if err != nil {
		t.Fatalf("EmbeddedMigrationCompatibility: %v", err)
	}
	if transition != 14 || len(declarations) != 1 || declarations[0] != (MigrationCompatibility{
		SchemaVersion:              14,
		MinimumBinarySchemaVersion: 14,
		Classification:             MigrationTransition,
	}) {
		t.Fatalf("compatibility declarations = %#v, transition = %d", declarations, transition)
	}
}
