package postgres

import (
	"strings"
	"testing"
)

func TestEvaluateUpgradePath(t *testing.T) {
	const transition = uint(14)
	compatible := MigrationCompatibility{
		SchemaVersion:              15,
		MinimumBinarySchemaVersion: 14,
		Classification:             MigrationOrdinary,
	}
	breaking := MigrationCompatibility{
		SchemaVersion:              15,
		MinimumBinarySchemaVersion: 15,
		Classification:             MigrationOrdinary,
	}

	ordinary, err := EvaluateUpgradePath(14, 14, compatible, []MigrationCompatibility{compatible}, transition, UpgradeOrdinary)
	if err != nil || !ordinary {
		t.Fatalf("compatible ordinary path = %t, %v", ordinary, err)
	}

	ordinary, err = EvaluateUpgradePath(14, 14, breaking, []MigrationCompatibility{breaking}, transition, UpgradeOrdinary)
	if err == nil || ordinary || !strings.Contains(err.Error(), "would strand current binary schema 000014") {
		t.Fatalf("breaking ordinary path = %t, %v", ordinary, err)
	}

	transitionTarget := MigrationCompatibility{
		SchemaVersion:              transition,
		MinimumBinarySchemaVersion: transition,
		Classification:             MigrationTransition,
	}
	ordinary, err = EvaluateUpgradePath(13, 13, transitionTarget, []MigrationCompatibility{transitionTarget}, transition, UpgradeTransition)
	if err != nil || ordinary {
		t.Fatalf("explicit transition path = %t, %v", ordinary, err)
	}

	if _, err := EvaluateUpgradePath(13, 13, transitionTarget, []MigrationCompatibility{transitionTarget}, transition, UpgradeOrdinary); err == nil ||
		!strings.Contains(err.Error(), "one-time compatibility transition") {
		t.Fatalf("ordinary transition error = %v", err)
	}
}
