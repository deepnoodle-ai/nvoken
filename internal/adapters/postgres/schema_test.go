package postgres

import "testing"

func TestEvaluateSchemaStatus(t *testing.T) {
	const transition = uint(14)
	for _, test := range []struct {
		name                 string
		expected             uint
		current              uint
		dirty                bool
		rows                 int
		compatibilityVersion uint
		minimumVersion       uint
		compatibilityRows    int
		want                 SchemaState
		compatible           bool
	}{
		{
			name:       "legacy exact match",
			expected:   13,
			current:    13,
			rows:       1,
			want:       SchemaCompatible,
			compatible: true,
		},
		{
			name:                 "transition exact match",
			expected:             14,
			current:              14,
			rows:                 1,
			compatibilityVersion: 14,
			minimumVersion:       14,
			compatibilityRows:    1,
			want:                 SchemaCompatible,
			compatible:           true,
		},
		{
			name:                 "declared compatible newer",
			expected:             14,
			current:              15,
			rows:                 1,
			compatibilityVersion: 15,
			minimumVersion:       14,
			compatibilityRows:    1,
			want:                 SchemaCompatibleNewer,
			compatible:           true,
		},
		{
			name:       "empty",
			expected:   14,
			rows:       0,
			want:       SchemaEmpty,
			compatible: false,
		},
		{
			name:       "dirty",
			expected:   14,
			current:    14,
			dirty:      true,
			rows:       1,
			want:       SchemaDirty,
			compatible: false,
		},
		{
			name:       "behind",
			expected:   14,
			current:    13,
			rows:       1,
			want:       SchemaBehind,
			compatible: false,
		},
		{
			name:                 "declared unsafe ahead",
			expected:             14,
			current:              15,
			rows:                 1,
			compatibilityVersion: 15,
			minimumVersion:       15,
			compatibilityRows:    1,
			want:                 SchemaAhead,
			compatible:           false,
		},
		{
			name:       "unknown ahead",
			expected:   14,
			current:    15,
			rows:       1,
			want:       SchemaUnknown,
			compatible: false,
		},
		{
			name:       "invalid migration row count",
			expected:   14,
			current:    14,
			rows:       2,
			want:       SchemaInvalid,
			compatible: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := evaluateSchemaStatus(
				test.expected,
				test.current,
				test.dirty,
				test.rows,
				transition,
				test.compatibilityVersion,
				test.minimumVersion,
				test.compatibilityRows,
			)
			compatibilityErr := got.CompatibilityError()
			if got.State != test.want || got.Compatible() != test.compatible ||
				(compatibilityErr == nil) != test.compatible || got.Expected != test.expected || got.Rows != test.rows {
				t.Fatalf("schema status = %#v", got)
			}
		})
	}
}
