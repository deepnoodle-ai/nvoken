package identity

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestUUIDv7Generator(t *testing.T) {
	wantTime := time.Date(2026, time.July, 20, 12, 34, 56, 789000000, time.UTC)
	randomness := []byte{0xff, 0x12, 0xff, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}
	generator := newUUIDv7Generator(fixedClock{now: wantTime}, bytes.NewReader(bytes.Repeat(randomness, 16)))

	for _, prefix := range []domain.StableIDPrefix{
		domain.PrefixAccount,
		domain.PrefixTenantPartition,
		domain.PrefixAgent,
		domain.PrefixSession,
		domain.PrefixExecutionSpecSnapshot,
		domain.PrefixSessionMessage,
		domain.PrefixInvocation,
		domain.PrefixInvocationState,
		domain.PrefixToolCall,
		domain.PrefixToolCallAttempt,
		domain.PrefixModelUsageReceipt,
		domain.PrefixInvocationCheckpoint,
		domain.PrefixSyntheticDispatchWork,
		domain.PrefixExecutionDispatch,
	} {
		id, err := generator.NewID(prefix)
		if err != nil {
			t.Fatalf("NewID(%q): %v", prefix, err)
		}
		parts := strings.SplitN(id, "_", 2)
		if len(parts) != 2 || parts[0] != string(prefix) {
			t.Fatalf("NewID(%q) = %q", prefix, id)
		}
		raw, err := hex.DecodeString(strings.ReplaceAll(parts[1], "-", ""))
		if err != nil {
			t.Fatalf("decode %q: %v", id, err)
		}
		if got := raw[6] >> 4; got != 7 {
			t.Errorf("version = %d, want 7", got)
		}
		if got := raw[8] >> 6; got != 2 {
			t.Errorf("variant = %d, want 2", got)
		}
		millis := int64(raw[0])<<40 | int64(raw[1])<<32 | int64(raw[2])<<24 |
			int64(raw[3])<<16 | int64(raw[4])<<8 | int64(raw[5])
		if millis != wantTime.UnixMilli() {
			t.Errorf("timestamp = %d, want %d", millis, wantTime.UnixMilli())
		}
	}
}

func TestUUIDv7GeneratorRandomFailure(t *testing.T) {
	generator := newUUIDv7Generator(fixedClock{now: time.Now()}, bytes.NewReader(nil))
	if _, err := generator.NewID(domain.PrefixSession); err == nil {
		t.Fatal("NewID succeeded with no randomness")
	}
}

func TestUUIDv7GeneratorRejectsUnknownPrefix(t *testing.T) {
	generator := newUUIDv7Generator(fixedClock{now: time.Now()}, bytes.NewReader(make([]byte, 10)))
	if _, err := generator.NewID(domain.StableIDPrefix("unknown")); err == nil {
		t.Fatal("NewID accepted an unknown prefix")
	}
}
