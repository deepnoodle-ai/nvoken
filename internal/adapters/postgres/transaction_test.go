package postgres

import (
	"errors"
	"testing"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestNormalizeTransactionErrorClassifiesRetryableConflicts(t *testing.T) {
	for _, code := range []string{"40001", "40P01", "55P03", "57014"} {
		t.Run(code, func(t *testing.T) {
			postgresError := &pgconn.PgError{Code: code, Message: "retry transaction"}
			err := normalizeTransactionError(postgresError)
			if !errors.Is(err, ports.ErrRetryable) || !errors.Is(err, postgresError) {
				t.Fatalf("normalized error = %v", err)
			}
		})
	}
	for _, constraint := range []string{"invocations_one_nonterminal_per_session", "invocations_idempotency_scope"} {
		t.Run(constraint, func(t *testing.T) {
			postgresError := &pgconn.PgError{Code: "23505", ConstraintName: constraint, Message: "unique violation"}
			if err := normalizeTransactionError(postgresError); !errors.Is(err, ports.ErrConcurrentAdmission) {
				t.Fatalf("normalized error = %v", err)
			}
		})
	}
	ordinaryUnique := &pgconn.PgError{Code: "23505", ConstraintName: "unexpected_unique", Message: "unique violation"}
	if got := normalizeTransactionError(ordinaryUnique); got != ordinaryUnique {
		t.Fatalf("unexpected uniqueness error changed to %v", got)
	}

	original := errors.New("ordinary failure")
	if got := normalizeTransactionError(original); got != original {
		t.Fatalf("ordinary error changed to %v", got)
	}
}
