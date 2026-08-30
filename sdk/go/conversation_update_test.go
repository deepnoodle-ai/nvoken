package nvoken

import (
	"encoding/json"
	"testing"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
	"github.com/oapi-codegen/nullable"
)

func TestUpdateConversationRequestPreservesPolicyWriteIntent(t *testing.T) {
	tests := []struct {
		name              string
		request           generated.UpdateConversationRequest
		wantRetention     any
		wantCompaction    any
		wantRetentionSet  bool
		wantCompactionSet bool
	}{
		{
			name:    "omitted policies stay omitted",
			request: generated.UpdateConversationRequest{},
		},
		{
			name: "null policies are sent",
			request: generated.UpdateConversationRequest{
				Retention:  nullable.NewNullNullable[generated.RetentionPolicy](),
				Compaction: nullable.NewNullNullable[generated.CompactionPolicy](),
			},
			wantRetentionSet:  true,
			wantCompactionSet: true,
		},
		{
			name: "replacement policy is sent",
			request: generated.UpdateConversationRequest{
				Retention: nullable.NewNullableWithValue(generated.RetentionPolicy{
					TTLSeconds: 3600,
				}),
			},
			wantRetention:    map[string]any{"ttl_seconds": float64(3600)},
			wantRetentionSet: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.request)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(encoded, &body); err != nil {
				t.Fatal(err)
			}
			retention, retentionSet := body["retention"]
			if retentionSet != test.wantRetentionSet || !equalJSONValue(retention, test.wantRetention) {
				t.Fatalf("retention = %#v, present = %v; body = %s", retention, retentionSet, encoded)
			}
			compaction, compactionSet := body["compaction"]
			if compactionSet != test.wantCompactionSet || !equalJSONValue(compaction, test.wantCompaction) {
				t.Fatalf("compaction = %#v, present = %v; body = %s", compaction, compactionSet, encoded)
			}
		})
	}
}

func equalJSONValue(got, want any) bool {
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	return gotErr == nil && wantErr == nil && string(gotJSON) == string(wantJSON)
}
