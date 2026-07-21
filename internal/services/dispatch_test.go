package services

import (
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

func TestSummarizeAgedDispatchesAggregatesByOperatorSignal(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	summaries := summarizeAgedDispatches([]domain.ExecutionDispatch{
		{ID: "dsp_pending_newer", Status: domain.ExecutionDispatchPending, UpdatedAt: now.Add(-6 * time.Minute), PublishAttempts: 1},
		{ID: "dsp_publishing_oldest", Status: domain.ExecutionDispatchPublishing, UpdatedAt: now.Add(-9 * time.Minute), PublishAttempts: 4},
		{ID: "dsp_published", Status: domain.ExecutionDispatchPublished, UpdatedAt: now.Add(-7 * time.Minute), PublishAttempts: 2},
	})

	if len(summaries) != 2 {
		t.Fatalf("summary count = %d, want 2", len(summaries))
	}
	pending := summaries[0]
	if pending.Event != "dispatch_aged_pending" || pending.Count != 2 || pending.Oldest.ID != "dsp_publishing_oldest" || pending.MaxPublishAttempts != 4 {
		t.Fatalf("pending summary = %#v", pending)
	}
	published := summaries[1]
	if published.Event != "dispatch_stale_published" || published.Count != 1 || published.Oldest.ID != "dsp_published" || published.MaxPublishAttempts != 2 {
		t.Fatalf("published summary = %#v", published)
	}
}
