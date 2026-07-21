-- Generation-only terminal evidence. Session messages remain the sole durable
-- transcript; these Invocation objects contain aggregate usage and provenance
-- only, never provider requests or response content.

BEGIN;

ALTER TABLE invocations
    ADD COLUMN usage jsonb,
    ADD COLUMN provenance jsonb,
    ADD CONSTRAINT invocations_usage_object CHECK (
        usage IS NULL OR jsonb_typeof(usage) = 'object'
    ),
    ADD CONSTRAINT invocations_provenance_object CHECK (
        provenance IS NULL OR jsonb_typeof(provenance) = 'object'
    ),
    ADD CONSTRAINT invocations_execution_evidence_pair CHECK (
        (usage IS NULL) = (provenance IS NULL)
    ),
    ADD CONSTRAINT invocations_execution_evidence_terminal CHECK (
        (usage IS NULL AND provenance IS NULL) OR status = 'completed'
    );

COMMIT;
