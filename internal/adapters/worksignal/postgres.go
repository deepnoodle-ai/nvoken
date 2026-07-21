package worksignal

import (
	"context"
	"sync"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/jackc/pgx/v5/pgxpool"
)

const cancellationChannel = "nvoken_invocation_cancellation"

// PostgresCancellation uses LISTEN/NOTIFY only as a coalescable latency hint.
// Durable cancellation and stale-owner rejection remain on the Invocation row.
type PostgresCancellation struct{ pool *pgxpool.Pool }

func NewPostgresCancellation(pool *pgxpool.Pool) *PostgresCancellation {
	return &PostgresCancellation{pool: pool}
}

func (s *PostgresCancellation) NotifyCancellation(ctx context.Context, invocationID string) {
	if s == nil || s.pool == nil || invocationID == "" {
		return
	}
	_, _ = s.pool.Exec(ctx, `SELECT pg_notify('`+cancellationChannel+`', $1)`, invocationID)
}

func (s *PostgresCancellation) SubscribeCancellations(parent context.Context) ports.CancellationSubscription {
	ctx, cancel := context.WithCancel(parent)
	return &postgresCancellationSubscription{pool: s.pool, ctx: ctx, cancel: cancel}
}

type postgresCancellationSubscription struct {
	pool   *pgxpool.Pool
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	waitMu sync.Mutex
	conn   *pgxpool.Conn
}

func (s *postgresCancellationSubscription) Wait(caller context.Context, timeout time.Duration) (string, bool) {
	if s == nil {
		return "", false
	}
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	if s.pool == nil || timeout <= 0 || s.ctx.Err() != nil {
		return "", false
	}
	conn, ok := s.connection()
	if !ok {
		return "", false
	}
	waitCtx, cancel := context.WithTimeout(caller, timeout)
	stopCloseWake := context.AfterFunc(s.ctx, cancel)
	defer stopCloseWake()
	defer cancel()
	notification, err := conn.Conn().WaitForNotification(waitCtx)
	if err != nil {
		if waitCtx.Err() == nil {
			s.resetConnection(conn)
		}
		return "", false
	}
	if notification == nil || notification.Payload == "" {
		return "", false
	}
	return notification.Payload, true
}

func (s *postgresCancellationSubscription) connection() (*pgxpool.Conn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return s.conn, true
	}
	conn, err := s.pool.Acquire(s.ctx)
	if err != nil {
		return nil, false
	}
	if _, err := conn.Exec(s.ctx, `LISTEN `+cancellationChannel); err != nil {
		conn.Release()
		return nil, false
	}
	s.conn = conn
	return conn, true
}

func (s *postgresCancellationSubscription) resetConnection(conn *pgxpool.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == conn {
		s.conn.Release()
		s.conn = nil
	}
}

func (s *postgresCancellationSubscription) Close() {
	if s == nil {
		return
	}
	s.cancel()
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Release()
		s.conn = nil
	}
}

var _ ports.CancellationSignaller = (*PostgresCancellation)(nil)
