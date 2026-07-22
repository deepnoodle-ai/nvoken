package postgres

import (
	"testing"
	"time"
)

func TestConfigurePoolTimeoutUsesDefaultWithoutOverridingURL(t *testing.T) {
	runtimeParams := map[string]string{
		"statement_timeout": "90000",
	}
	configurePoolTimeout(runtimeParams, "statement_timeout", statementTimeout, 0)
	configurePoolTimeout(
		runtimeParams,
		"idle_in_transaction_session_timeout",
		idleInTransactionTimeout,
		0,
	)

	if got := runtimeParams["statement_timeout"]; got != "90000" {
		t.Fatalf("statement timeout = %q, want URL value", got)
	}
	if got := runtimeParams["idle_in_transaction_session_timeout"]; got != idleInTransactionTimeout {
		t.Fatalf("idle-in-transaction timeout = %q, want %q", got, idleInTransactionTimeout)
	}
}

func TestConfigurePoolTimeoutOverridesURLForDedicatedPool(t *testing.T) {
	runtimeParams := map[string]string{
		"idle_in_transaction_session_timeout": "30000",
		"statement_timeout":                   "120000",
	}
	configurePoolTimeout(runtimeParams, "statement_timeout", statementTimeout, 7*time.Minute)
	configurePoolTimeout(
		runtimeParams,
		"idle_in_transaction_session_timeout",
		idleInTransactionTimeout,
		7*time.Minute,
	)

	if got := runtimeParams["statement_timeout"]; got != "420000" {
		t.Fatalf("statement timeout = %q, want 420000", got)
	}
	if got := runtimeParams["idle_in_transaction_session_timeout"]; got != "420000" {
		t.Fatalf("idle-in-transaction timeout = %q, want 420000", got)
	}
}
