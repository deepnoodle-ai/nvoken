package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type UpgradeMode string

const (
	UpgradeOrdinary   UpgradeMode = "ordinary"
	UpgradeTransition UpgradeMode = "transition"
)

type UpgradePreflightRequest struct {
	CurrentBuildVersion        string
	CurrentBinarySchemaVersion uint
	TargetBuildVersion         string
	Mode                       UpgradeMode
}

type UpgradePreflightResult struct {
	CurrentBuildVersion              string
	TargetBuildVersion               string
	CurrentBinarySchemaVersion       uint
	CurrentDatabaseSchemaVersion     uint
	TargetSchemaVersion              uint
	TargetMinimumBinarySchemaVersion uint
	TargetClassification             MigrationClassification
	Mode                             UpgradeMode
	OrdinaryCompatibilityWindow      bool
}

// PreflightUpgrade is the read-only release gate. It proves the current
// serving binary can use the current database and that every pending migration
// preserves that binary, unless the one-time transition is explicitly chosen.
func PreflightUpgrade(
	ctx context.Context,
	pool *pgxpool.Pool,
	request UpgradePreflightRequest,
) (UpgradePreflightResult, error) {
	if request.Mode == "" {
		request.Mode = UpgradeOrdinary
	}
	if request.Mode != UpgradeOrdinary && request.Mode != UpgradeTransition {
		return UpgradePreflightResult{}, fmt.Errorf("upgrade mode must be %q or %q", UpgradeOrdinary, UpgradeTransition)
	}
	if strings.TrimSpace(request.TargetBuildVersion) == "" {
		return UpgradePreflightResult{}, fmt.Errorf("target build version is required")
	}
	declarations, transition, err := EmbeddedMigrationCompatibility()
	if err != nil {
		return UpgradePreflightResult{}, err
	}
	target, _, err := TargetMigrationCompatibility()
	if err != nil {
		return UpgradePreflightResult{}, err
	}
	targetStatus, err := InspectSchema(ctx, pool)
	if err != nil {
		return UpgradePreflightResult{}, err
	}
	result := UpgradePreflightResult{
		CurrentBuildVersion:              strings.TrimSpace(request.CurrentBuildVersion),
		TargetBuildVersion:               strings.TrimSpace(request.TargetBuildVersion),
		CurrentBinarySchemaVersion:       request.CurrentBinarySchemaVersion,
		CurrentDatabaseSchemaVersion:     targetStatus.Current,
		TargetSchemaVersion:              target.SchemaVersion,
		TargetMinimumBinarySchemaVersion: target.MinimumBinarySchemaVersion,
		TargetClassification:             target.Classification,
		Mode:                             request.Mode,
	}
	if targetStatus.State == SchemaEmpty {
		if request.Mode == UpgradeTransition {
			return result, fmt.Errorf("transition mode is not valid for an empty database")
		}
		result.CurrentBuildVersion = "none"
		result.OrdinaryCompatibilityWindow = true
		return result, nil
	}
	if targetStatus.State == SchemaDirty || targetStatus.State == SchemaInvalid || targetStatus.State == SchemaUnknown {
		return result, targetStatus.CompatibilityError()
	}
	if targetStatus.Current > target.SchemaVersion {
		return result, fmt.Errorf("database schema version %06d is newer than target schema %06d", targetStatus.Current, target.SchemaVersion)
	}
	if targetStatus.Current == target.SchemaVersion {
		if target.Classification == MigrationTransition && request.Mode == UpgradeTransition {
			// The transition migration may have succeeded even when the following
			// service rollout did not. Let an operator safely retry that rollout;
			// the previous exact-match binary is already outside the window.
			return result, nil
		}
		if request.Mode == UpgradeTransition {
			return result, fmt.Errorf("transition mode is valid only for schema %06d", transition)
		}
		currentBuildKnown := result.CurrentBuildVersion != "" && result.CurrentBuildVersion != "none"
		currentSchemaKnown := request.CurrentBinarySchemaVersion != 0
		if currentBuildKnown != currentSchemaKnown {
			return result, fmt.Errorf("current build version and current binary schema version must be supplied together")
		}
		if currentSchemaKnown {
			currentBinaryStatus, err := InspectSchemaForVersion(ctx, pool, request.CurrentBinarySchemaVersion)
			if err != nil {
				return result, err
			}
			if err := currentBinaryStatus.CompatibilityError(); err != nil {
				return result, fmt.Errorf("current binary cannot serve current database: %w", err)
			}
		}
		if result.CurrentBuildVersion == "" {
			result.CurrentBuildVersion = "unspecified"
		}
		result.OrdinaryCompatibilityWindow = true
		return result, nil
	}
	if result.CurrentBuildVersion == "" || request.CurrentBinarySchemaVersion == 0 {
		return result, fmt.Errorf("current build version and current binary schema version are required for a nonempty database")
	}
	currentBinaryStatus, err := InspectSchemaForVersion(ctx, pool, request.CurrentBinarySchemaVersion)
	if err != nil {
		return result, err
	}
	if err := currentBinaryStatus.CompatibilityError(); err != nil {
		return result, fmt.Errorf("current binary cannot serve current database: %w", err)
	}
	ordinaryWindow, err := EvaluateUpgradePath(
		targetStatus.Current,
		request.CurrentBinarySchemaVersion,
		target,
		declarations,
		transition,
		request.Mode,
	)
	if err != nil {
		return result, err
	}
	result.OrdinaryCompatibilityWindow = ordinaryWindow
	return result, nil
}

// EvaluateUpgradePath checks embedded migration declarations without touching
// a database. Release tests use fixture declarations to prove a breaking
// migration cannot pass the ordinary path before any DDL runs.
func EvaluateUpgradePath(
	currentDatabaseSchemaVersion uint,
	currentBinarySchemaVersion uint,
	target MigrationCompatibility,
	declarations []MigrationCompatibility,
	transition uint,
	mode UpgradeMode,
) (bool, error) {
	if currentDatabaseSchemaVersion >= target.SchemaVersion {
		return true, nil
	}
	if currentDatabaseSchemaVersion < transition {
		if target.SchemaVersion != transition || target.Classification != MigrationTransition {
			return false, fmt.Errorf("compatibility transition must land on schema %06d before later migrations", transition)
		}
		if mode != UpgradeTransition {
			return false, fmt.Errorf("schema %06d is the one-time compatibility transition; rerun with migration mode %q", transition, UpgradeTransition)
		}
		if currentBinarySchemaVersion != currentDatabaseSchemaVersion {
			return false, fmt.Errorf("transition requires current binary schema %06d to exactly match database schema %06d", currentBinarySchemaVersion, currentDatabaseSchemaVersion)
		}
		return false, nil
	}
	if mode != UpgradeOrdinary {
		return false, fmt.Errorf("migration mode %q is not valid after compatibility transition %06d", mode, transition)
	}
	for _, declaration := range declarations {
		if declaration.SchemaVersion <= currentDatabaseSchemaVersion || declaration.SchemaVersion > target.SchemaVersion {
			continue
		}
		if declaration.Classification != MigrationOrdinary {
			return false, fmt.Errorf("pending schema %06d is not an ordinary migration", declaration.SchemaVersion)
		}
		if declaration.MinimumBinarySchemaVersion > currentBinarySchemaVersion {
			return false, fmt.Errorf(
				"schema %06d requires binary schema %06d and would strand current binary schema %06d",
				declaration.SchemaVersion,
				declaration.MinimumBinarySchemaVersion,
				currentBinarySchemaVersion,
			)
		}
	}
	return true, nil
}
