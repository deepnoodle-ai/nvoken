CREATE INDEX session_messages_by_invocation
    ON session_messages (invocation_id, sequence);

UPDATE nvoken_schema_compatibility
SET schema_version = 15,
    minimum_binary_schema_version = 14;
