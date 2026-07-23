// Package observability defines the portable, bounded operational vocabulary
// emitted by both nvoken production profiles.
package observability

import (
	"context"
	"errors"
	"net"
)

const (
	EventProcessStarted      = "process_started"
	EventProcessStartFailed  = "process_start_failed"
	EventProcessFailed       = "process_failed"
	EventDiagnosticCheck     = "diagnostic_check"
	EventUpgradePreflight    = "upgrade_preflight"
	EventRestoreVerification = "restore_verification"

	EventHTTPRequest         = "http_request_completed"
	EventHTTPRequestFailed   = "http_request_failed"
	EventStreamResync        = "live_event_stream_resync"
	EventStreamClosed        = "live_event_stream_closed"
	EventLivePublishFailed   = "live_event_publish_failed"
	EventLiveSubscribeFailed = "live_event_subscribe_failed"
	EventLiveDecodeFailed    = "live_event_decode_failed"

	EventInvocationClaimed           = "invocation_claimed"
	EventInvocationClaimFailed       = "invocation_claim_failed"
	EventInvocationRecovered         = "invocation_recovered"
	EventInvocationRecoveryLoaded    = "invocation_recovery_loaded"
	EventInvocationExecutionFailed   = "invocation_execution_failed"
	EventInvocationLeaseLost         = "invocation_lease_lost"
	EventInvocationMaintenanceFailed = "invocation_maintenance_failed"
	EventInvocationSettled           = "invocation_settled"
	EventProviderGeneration          = "provider_generation"

	EventCallbackClaimFailed    = "callback_claim_failed"
	EventCallbackProcessFailed  = "callback_process_failed"
	EventCallbackRecoveryFailed = "callback_recovery_failed"
	EventCallbackPruneFailed    = "callback_prune_failed"
	EventCallbackClaimed        = "callback_delivery_claimed"
	EventCallbackRetry          = "callback_delivery_retry"
	EventCallbackSettled        = "callback_delivery_settled"
	EventCallbackAbandoned      = "callback_delivery_abandoned"
	EventCallbackStale          = "callback_delivery_stale"
	EventCallbackLeaseRecovered = "callback_lease_recovered"
	EventCallbackPruned         = "callback_pruned"

	EventDispatchClaimFailed = "dispatch_claim_failed"
	// EventDispatchPublishFailed preserves the original metric and alert key.
	EventDispatchPublishFailed   = "dispatch_publish_failure"
	EventDispatchAgedPending     = "dispatch_aged_pending"
	EventDispatchStalePublished  = "dispatch_stale_published"
	EventDispatchRepairFailed    = "dispatch_repair_failed"
	EventDispatchReconcileFailed = "dispatch_reconcile_failed"
	EventDispatchPruneFailed     = "dispatch_prune_failed"
	EventDispatchReconciled      = "dispatch_reconciled"
	EventDispatchPruned          = "dispatch_pruned"
	EventDispatchAttemptSettled  = "dispatch_attempt_settled"
	EventDispatchAttemptRetry    = "dispatch_attempt_retry"
	EventDispatchAttemptDecided  = "dispatch_attempt_decided"

	EventHostToolResultPartial      = "host_tool_result_partial"
	EventHostToolResumeQueued       = "host_tool_resume_queued"
	EventHostToolResultDeduplicated = "host_tool_result_deduplicated"
)

const (
	OutcomeSuccess     = "success"
	OutcomeFailed      = "failed"
	OutcomeCanceled    = "canceled"
	OutcomeSkipped     = "skipped"
	OutcomeClientError = "client_error"
	OutcomeServerError = "server_error"
)

// ErrorClass returns a bounded class suitable for logs. It intentionally does
// not return err.Error(), which may contain remote bodies, URLs, or secrets.
func ErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return "timeout"
		}
		return "transport"
	}
	return "internal"
}
