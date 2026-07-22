package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SchemaState string

const (
	SchemaCompatible SchemaState = "compatible"
	SchemaEmpty      SchemaState = "empty"
	SchemaDirty      SchemaState = "dirty"
	SchemaBehind     SchemaState = "behind"
	SchemaAhead      SchemaState = "ahead"
	SchemaInvalid    SchemaState = "invalid"
)

type SchemaStatus struct {
	State    SchemaState
	Current  uint
	Expected uint
	Dirty    bool
	Rows     int
}

func (s SchemaStatus) Compatible() bool { return s.State == SchemaCompatible }

// CompatibilityError explains why the schema cannot be served without
// changing the bounded state reported by InspectSchema.
func (s SchemaStatus) CompatibilityError() error {
	switch s.State {
	case SchemaCompatible:
		return nil
	case SchemaEmpty, SchemaInvalid:
		return fmt.Errorf("database schema state has %d rows, want 1", s.Rows)
	case SchemaDirty:
		return fmt.Errorf("database schema version %06d is dirty", s.Current)
	case SchemaBehind, SchemaAhead:
		return fmt.Errorf("database schema version %06d is incompatible with expected %06d", s.Current, s.Expected)
	default:
		return fmt.Errorf("database schema state %q is invalid", s.State)
	}
}

// CheckSchema verifies the serve path can safely use the database. It performs
// no DDL and deliberately cannot repair an empty, dirty, old, or future schema.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	status, err := InspectSchema(ctx, pool)
	if err != nil {
		return err
	}
	return status.CompatibilityError()
}

// InspectSchema reports the same read-only compatibility verdict used by the
// serve path. It never creates or alters the migration table.
func InspectSchema(ctx context.Context, pool *pgxpool.Pool) (SchemaStatus, error) {
	expected, err := ExpectedSchemaVersion()
	if err != nil {
		return SchemaStatus{}, err
	}
	rows, err := pool.Query(ctx, "SELECT version, dirty FROM "+migrationTable+" LIMIT 2")
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "42P01" {
			return evaluateSchemaStatus(expected, 0, false, 0), nil
		}
		return SchemaStatus{}, fmt.Errorf("read database schema state: %w", err)
	}
	defer rows.Close()

	var version uint
	var dirty bool
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&version, &dirty); err != nil {
			return SchemaStatus{}, fmt.Errorf("scan database schema state: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return SchemaStatus{}, fmt.Errorf("read database schema state: %w", err)
	}
	return evaluateSchemaStatus(expected, version, dirty, count), nil
}

func evaluateSchemaStatus(expected, current uint, dirty bool, rows int) SchemaStatus {
	status := SchemaStatus{
		Current:  current,
		Expected: expected,
		Dirty:    dirty,
		Rows:     rows,
	}
	switch {
	case rows == 0:
		status.State = SchemaEmpty
	case rows != 1:
		status.State = SchemaInvalid
	case dirty:
		status.State = SchemaDirty
	case current < expected:
		status.State = SchemaBehind
	case current > expected:
		status.State = SchemaAhead
	default:
		status.State = SchemaCompatible
	}
	return status
}

func ExpectedSchemaVersion() (uint, error) {
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
