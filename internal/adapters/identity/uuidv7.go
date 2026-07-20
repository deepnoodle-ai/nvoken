// Package identity implements durable runtime identity generation.
package identity

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type UUIDv7Generator struct {
	clock  ports.Clock
	random io.Reader
}

func NewUUIDv7Generator(clock ports.Clock) *UUIDv7Generator {
	return newUUIDv7Generator(clock, rand.Reader)
}

func newUUIDv7Generator(clock ports.Clock, random io.Reader) *UUIDv7Generator {
	return &UUIDv7Generator{clock: clock, random: random}
}

func (g *UUIDv7Generator) NewID(prefix domain.StableIDPrefix) (string, error) {
	if !prefix.Valid() {
		return "", fmt.Errorf("unknown durable ID prefix %q", prefix)
	}
	if g.clock == nil || g.random == nil {
		return "", fmt.Errorf("uuidv7 generator is not configured")
	}

	millis := g.clock.Now().UnixMilli()
	if millis < 0 || millis >= 1<<48 {
		return "", fmt.Errorf("uuidv7 timestamp out of range: %d", millis)
	}

	var randomness [10]byte
	if _, err := io.ReadFull(g.random, randomness[:]); err != nil {
		return "", fmt.Errorf("read uuidv7 randomness: %w", err)
	}

	var raw [16]byte
	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], uint64(millis))
	copy(raw[0:6], timestamp[2:])
	raw[6] = 0x70 | randomness[0]&0x0f
	raw[7] = randomness[1]
	raw[8] = 0x80 | randomness[2]&0x3f
	copy(raw[9:], randomness[3:])

	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(prefix) + "_" + string(encoded), nil
}
