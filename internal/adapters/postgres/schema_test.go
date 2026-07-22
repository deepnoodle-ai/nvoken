package postgres

import "testing"

func TestEvaluateSchemaStatus(t *testing.T) {
	for _, test := range []struct {
		name       string
		current    uint
		dirty      bool
		rows       int
		want       SchemaState
		compatible bool
	}{
		{name: "compatible", current: 13, rows: 1, want: SchemaCompatible, compatible: true},
		{name: "empty", rows: 0, want: SchemaEmpty},
		{name: "dirty", current: 13, dirty: true, rows: 1, want: SchemaDirty},
		{name: "behind", current: 12, rows: 1, want: SchemaBehind},
		{name: "ahead", current: 14, rows: 1, want: SchemaAhead},
		{name: "invalid row count", current: 13, rows: 2, want: SchemaInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := evaluateSchemaStatus(13, test.current, test.dirty, test.rows)
			compatibilityErr := got.CompatibilityError()
			if got.State != test.want || got.Compatible() != test.compatible ||
				(compatibilityErr == nil) != test.compatible || got.Expected != 13 || got.Rows != test.rows {
				t.Fatalf("schema status = %#v", got)
			}
		})
	}
}
