package postgres

import (
	"context"
	"slices"
	"testing"
)

// TestChurnTableAutovacuumOptions proves migration 000016's per-table
// autovacuum thresholds are present after a full migration, so a later
// migration cannot drop them unnoticed. Rows in these tables are rewritten
// continuously and never HOT; cleanup and statistics refresh must start well
// before the Postgres default of 20% dead tuples.
func TestChurnTableAutovacuumOptions(t *testing.T) {
	pool, _ := testDatabase(t, true)
	ctx := context.Background()

	churnTables := []string{
		"sessions",
		"invocations",
		"tool_calls",
		"execution_dispatches",
		"callback_deliveries",
	}
	rows, err := pool.Query(ctx, `
		SELECT c.relname, c.reloptions
		FROM pg_catalog.pg_class AS c
		JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relname = ANY($1)
	`, churnTables)
	if err != nil {
		t.Fatalf("read churn table storage parameters: %v", err)
	}
	defer rows.Close()

	options := make(map[string][]string, len(churnTables))
	for rows.Next() {
		var table string
		var parameters []string
		if err := rows.Scan(&table, &parameters); err != nil {
			t.Fatalf("scan churn table storage parameters: %v", err)
		}
		options[table] = parameters
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read churn table storage parameters: %v", err)
	}

	for _, table := range churnTables {
		parameters, ok := options[table]
		if !ok {
			t.Errorf("churn table %s is missing", table)
			continue
		}
		for _, expected := range []string{
			"autovacuum_vacuum_scale_factor=0.05",
			"autovacuum_analyze_scale_factor=0.02",
		} {
			if !slices.Contains(parameters, expected) {
				t.Errorf("table %s storage parameters = %v, missing %s", table, parameters, expected)
			}
		}
	}
}
