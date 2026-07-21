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
	if len(versions) != 4 || versions[0] != 1 || versions[1] != 2 || versions[2] != 3 || versions[3] != 4 {
		t.Fatalf("migration versions = %v, want [1 2 3 4]", versions)
	}
}
