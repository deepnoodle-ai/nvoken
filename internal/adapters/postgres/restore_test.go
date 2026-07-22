package postgres

import "testing"

func TestRestoreManifestTracksEmbeddedSchema(t *testing.T) {
	expected, err := ExpectedSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if expected != restoreManifestSchemaVersion {
		t.Fatalf(
			"restore manifest schema = %06d, embedded schema = %06d; update the restore manifest and invariant queries",
			restoreManifestSchemaVersion,
			expected,
		)
	}
}

func TestVerifyRestoreRequiresPool(t *testing.T) {
	if _, err := VerifyRestore(t.Context(), nil); err == nil {
		t.Fatal("VerifyRestore accepted a nil pool")
	}
}
