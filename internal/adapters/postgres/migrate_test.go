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
	if len(versions) != 11 || versions[0] != 1 || versions[1] != 2 || versions[2] != 3 || versions[3] != 4 || versions[4] != 5 || versions[5] != 6 || versions[6] != 7 || versions[7] != 8 || versions[8] != 9 || versions[9] != 10 || versions[10] != 11 {
		t.Fatalf("migration versions = %v, want [1 2 3 4 5 6 7 8 9 10 11]", versions)
	}
}
