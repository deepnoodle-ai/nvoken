package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CheckSchema verifies the serve path can safely use the database. It performs
// no DDL and deliberately cannot repair an empty, dirty, old, or future schema.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	expected, err := latestMigrationVersion()
	if err != nil {
		return err
	}
	rows, err := pool.Query(ctx, "SELECT version, dirty FROM "+migrationTable+" LIMIT 2")
	if err != nil {
		return fmt.Errorf("read database schema state: %w", err)
	}
	defer rows.Close()

	var version uint
	var dirty bool
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&version, &dirty); err != nil {
			return fmt.Errorf("scan database schema state: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read database schema state: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("database schema state has %d rows, want 1", count)
	}
	if dirty {
		return fmt.Errorf("database schema version %06d is dirty", version)
	}
	if version != expected {
		return fmt.Errorf("database schema version %06d is incompatible with expected %06d", version, expected)
	}
	return nil
}

func latestMigrationVersion() (uint, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return 0, fmt.Errorf("read embedded migrations: %w", err)
	}
	var latest uint64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return 0, fmt.Errorf("invalid migration filename %q", name)
		}
		version, err := strconv.ParseUint(prefix, 10, 64)
		if err != nil || version == 0 {
			return 0, fmt.Errorf("invalid migration version in %q", name)
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("no embedded migrations")
	}
	return uint(latest), nil
}
