CREATE TABLE nvoken_schema_compatibility (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE,
    schema_version BIGINT NOT NULL,
    minimum_binary_schema_version BIGINT NOT NULL,
    CONSTRAINT nvoken_schema_compatibility_singleton CHECK (singleton),
    CONSTRAINT nvoken_schema_compatibility_schema_positive CHECK (schema_version > 0),
    CONSTRAINT nvoken_schema_compatibility_minimum_positive CHECK (minimum_binary_schema_version > 0),
    CONSTRAINT nvoken_schema_compatibility_minimum_bounded CHECK (minimum_binary_schema_version <= schema_version)
);

INSERT INTO nvoken_schema_compatibility (
    singleton,
    schema_version,
    minimum_binary_schema_version
) VALUES (
    TRUE,
    14,
    14
);
