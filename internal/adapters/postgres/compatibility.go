package postgres

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

const compatibilityTable = "nvoken_schema_compatibility"

type MigrationClassification string

const (
	MigrationOrdinary   MigrationClassification = "ordinary"
	MigrationTransition MigrationClassification = "transition"
)

type MigrationCompatibility struct {
	SchemaVersion              uint                    `json:"schema_version"`
	MinimumBinarySchemaVersion uint                    `json:"minimum_binary_schema_version"`
	Classification             MigrationClassification `json:"classification"`
}

type compatibilityManifest struct {
	TransitionSchemaVersion uint                     `json:"transition_schema_version"`
	Migrations              []MigrationCompatibility `json:"migrations"`
}

func EmbeddedMigrationCompatibility() ([]MigrationCompatibility, uint, error) {
	contents, err := fs.ReadFile(migrationFiles, "migrations/compatibility.json")
	if err != nil {
		return nil, 0, fmt.Errorf("read migration compatibility manifest: %w", err)
	}
	var manifest compatibilityManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return nil, 0, fmt.Errorf("decode migration compatibility manifest: %w", err)
	}
	if manifest.TransitionSchemaVersion == 0 {
		return nil, 0, fmt.Errorf("migration compatibility transition version must be positive")
	}
	declarations := append([]MigrationCompatibility(nil), manifest.Migrations...)
	sort.Slice(declarations, func(i, j int) bool {
		return declarations[i].SchemaVersion < declarations[j].SchemaVersion
	})
	seen := make(map[uint]struct{}, len(declarations))
	for _, declaration := range declarations {
		if declaration.SchemaVersion < manifest.TransitionSchemaVersion {
			return nil, 0, fmt.Errorf("migration compatibility declaration %06d precedes transition %06d", declaration.SchemaVersion, manifest.TransitionSchemaVersion)
		}
		if declaration.MinimumBinarySchemaVersion == 0 || declaration.MinimumBinarySchemaVersion > declaration.SchemaVersion {
			return nil, 0, fmt.Errorf("migration compatibility declaration %06d has invalid minimum binary schema version %06d", declaration.SchemaVersion, declaration.MinimumBinarySchemaVersion)
		}
		if declaration.Classification != MigrationOrdinary && declaration.Classification != MigrationTransition {
			return nil, 0, fmt.Errorf("migration compatibility declaration %06d has invalid classification %q", declaration.SchemaVersion, declaration.Classification)
		}
		if _, ok := seen[declaration.SchemaVersion]; ok {
			return nil, 0, fmt.Errorf("duplicate migration compatibility declaration %06d", declaration.SchemaVersion)
		}
		seen[declaration.SchemaVersion] = struct{}{}
	}
	transition, ok := compatibilityForVersion(declarations, manifest.TransitionSchemaVersion)
	if !ok || transition.Classification != MigrationTransition || transition.MinimumBinarySchemaVersion != manifest.TransitionSchemaVersion {
		return nil, 0, fmt.Errorf("migration compatibility transition %06d must declare itself as the minimum transition binary", manifest.TransitionSchemaVersion)
	}
	driver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return nil, 0, fmt.Errorf("open embedded migrations: %w", err)
	}
	defer func() { _ = driver.Close() }()
	versions, err := migrationVersions(driver)
	if err != nil {
		return nil, 0, err
	}
	embedded := make(map[uint]struct{}, len(versions))
	for _, version := range versions {
		embedded[version] = struct{}{}
		if version >= manifest.TransitionSchemaVersion {
			if _, ok := compatibilityForVersion(declarations, version); !ok {
				return nil, 0, fmt.Errorf("schema migration %06d has no compatibility declaration", version)
			}
		}
	}
	for _, declaration := range declarations {
		if _, ok := embedded[declaration.SchemaVersion]; !ok {
			return nil, 0, fmt.Errorf("migration compatibility declaration %06d has no embedded migration", declaration.SchemaVersion)
		}
	}
	return declarations, manifest.TransitionSchemaVersion, nil
}

func TargetMigrationCompatibility() (MigrationCompatibility, uint, error) {
	expected, err := ExpectedSchemaVersion()
	if err != nil {
		return MigrationCompatibility{}, 0, err
	}
	declarations, transition, err := EmbeddedMigrationCompatibility()
	if err != nil {
		return MigrationCompatibility{}, 0, err
	}
	declaration, ok := compatibilityForVersion(declarations, expected)
	if !ok {
		return MigrationCompatibility{}, 0, fmt.Errorf("schema migration %06d has no compatibility declaration", expected)
	}
	return declaration, transition, nil
}

func compatibilityForVersion(declarations []MigrationCompatibility, version uint) (MigrationCompatibility, bool) {
	for _, declaration := range declarations {
		if declaration.SchemaVersion == version {
			return declaration, true
		}
	}
	return MigrationCompatibility{}, false
}
