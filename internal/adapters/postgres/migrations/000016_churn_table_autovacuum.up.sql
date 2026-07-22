-- Per-table autovacuum thresholds for the high-churn runtime tables. Rows in
-- these tables are rewritten repeatedly during normal operation (sequence
-- counters, lifecycle transitions, lease heartbeats, attempt and delivery
-- flips), and every such update changes an indexed column, so none are HOT.
-- The Postgres default waits for dead tuples to reach 20% of the table
-- before vacuuming; these settings start cleanup and statistics refresh at
-- 5% and 2% so bloat and stale plans do not compound on large tables.
-- Storage parameters only: no table rewrite, no shape change, and the
-- previous release binary remains safe (ordinary migration).

BEGIN;

ALTER TABLE sessions SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

ALTER TABLE invocations SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

ALTER TABLE tool_calls SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

ALTER TABLE execution_dispatches SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

ALTER TABLE callback_deliveries SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

UPDATE nvoken_schema_compatibility
SET schema_version = 16,
    minimum_binary_schema_version = 14;

COMMIT;
