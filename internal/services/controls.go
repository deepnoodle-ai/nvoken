package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const (
	maxLimitSeconds      = int64((7 * 24 * time.Hour) / time.Second)
	maxLimitOutputTokens = 10_000_000
	maxLimitCostMicros   = int64(1_000_000 * 1_000_000)
	maxLimitIterations   = 10_000
)

type InvocationLimitInput struct {
	TotalTimeoutSeconds   *int64   `json:"total_timeout_seconds,omitempty"`
	ActiveTimeoutSeconds  *int64   `json:"active_timeout_seconds,omitempty"`
	WaitingTimeoutSeconds *int64   `json:"waiting_timeout_seconds,omitempty"`
	MaxOutputTokens       *int     `json:"max_output_tokens,omitempty"`
	MaxEstimatedCostUSD   *float64 `json:"max_estimated_cost_usd,omitempty"`
	MaxIterations         *int     `json:"max_iterations,omitempty"`
}

type InvocationLimitRead struct {
	TotalTimeoutSeconds   int64    `json:"total_timeout_seconds"`
	ActiveTimeoutSeconds  int64    `json:"active_timeout_seconds"`
	WaitingTimeoutSeconds int64    `json:"waiting_timeout_seconds"`
	MaxOutputTokens       *int     `json:"max_output_tokens,omitempty"`
	MaxEstimatedCostUSD   *float64 `json:"max_estimated_cost_usd,omitempty"`
	MaxIterations         int      `json:"max_iterations"`
}

type LimitPolicy struct {
	DefaultTotalTimeout    time.Duration
	DefaultActiveTimeout   time.Duration
	DefaultWaitingTimeout  time.Duration
	DefaultMaxIterations   int
	MaxTotalTimeout        time.Duration
	MaxActiveTimeout       time.Duration
	MaxWaitingTimeout      time.Duration
	MaxOutputTokens        int
	MaxEstimatedCostMicros int64
	MaxIterations          int
}

type ResolvedLimits struct {
	TotalTimeout           time.Duration
	ActiveTimeout          time.Duration
	WaitingTimeout         time.Duration
	MaxOutputTokens        *int
	MaxEstimatedCostMicros *int64
	MaxIterations          int
}

func DefaultLimitPolicy() LimitPolicy {
	return LimitPolicy{
		DefaultTotalTimeout:    30 * time.Minute,
		DefaultActiveTimeout:   30 * time.Minute,
		DefaultWaitingTimeout:  30 * time.Minute,
		DefaultMaxIterations:   1,
		MaxTotalTimeout:        24 * time.Hour,
		MaxActiveTimeout:       24 * time.Hour,
		MaxWaitingTimeout:      24 * time.Hour,
		MaxOutputTokens:        1_000_000,
		MaxEstimatedCostMicros: 1_000_000_000,
		MaxIterations:          100,
	}
}

func validateRequestedLimits(input *InvocationLimitInput) error {
	if input == nil {
		return nil
	}
	if input.TotalTimeoutSeconds != nil && (*input.TotalTimeoutSeconds <= 0 || *input.TotalTimeoutSeconds > maxLimitSeconds) {
		return invalidRequest("spec.limits.total_timeout_seconds must be a positive supported value.")
	}
	if input.ActiveTimeoutSeconds != nil && (*input.ActiveTimeoutSeconds <= 0 || *input.ActiveTimeoutSeconds > maxLimitSeconds) {
		return invalidRequest("spec.limits.active_timeout_seconds must be a positive supported value.")
	}
	if input.WaitingTimeoutSeconds != nil && (*input.WaitingTimeoutSeconds <= 0 || *input.WaitingTimeoutSeconds > maxLimitSeconds) {
		return invalidRequest("spec.limits.waiting_timeout_seconds must be a positive supported value.")
	}
	if input.MaxOutputTokens != nil && (*input.MaxOutputTokens <= 0 || *input.MaxOutputTokens > maxLimitOutputTokens) {
		return invalidRequest("spec.limits.max_output_tokens must be a positive supported value.")
	}
	if input.MaxIterations != nil && (*input.MaxIterations <= 0 || *input.MaxIterations > maxLimitIterations) {
		return invalidRequest("spec.limits.max_iterations must be a positive supported value.")
	}
	if input.MaxEstimatedCostUSD != nil {
		value := *input.MaxEstimatedCostUSD
		scaled := value * 1_000_000
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > float64(maxLimitCostMicros)/1_000_000 ||
			math.Abs(scaled-math.Round(scaled)) > 1e-6 {
			return invalidRequest("spec.limits.max_estimated_cost_usd must be positive, finite, and have at most six decimal places.")
		}
	}
	return nil
}

func (p LimitPolicy) Resolve(input *InvocationLimitInput) (ResolvedLimits, error) {
	if p.DefaultTotalTimeout <= 0 || p.DefaultActiveTimeout <= 0 || p.DefaultWaitingTimeout <= 0 || p.DefaultMaxIterations <= 0 ||
		p.MaxTotalTimeout < p.DefaultTotalTimeout || p.MaxActiveTimeout < p.DefaultActiveTimeout ||
		p.MaxWaitingTimeout < p.DefaultWaitingTimeout ||
		p.MaxOutputTokens <= 0 || p.MaxEstimatedCostMicros <= 0 || p.MaxIterations < p.DefaultMaxIterations {
		return ResolvedLimits{}, fmt.Errorf("invocation budget policy is invalid")
	}
	if p.MaxTotalTimeout > time.Duration(maxLimitSeconds)*time.Second ||
		p.MaxActiveTimeout > time.Duration(maxLimitSeconds)*time.Second ||
		p.MaxWaitingTimeout > time.Duration(maxLimitSeconds)*time.Second ||
		p.MaxOutputTokens > maxLimitOutputTokens || p.MaxEstimatedCostMicros > maxLimitCostMicros ||
		p.MaxIterations > maxLimitIterations {
		return ResolvedLimits{}, fmt.Errorf("invocation budget policy exceeds fixed safety limits")
	}
	if p.DefaultTotalTimeout%time.Second != 0 || p.DefaultActiveTimeout%time.Second != 0 || p.DefaultWaitingTimeout%time.Second != 0 ||
		p.MaxTotalTimeout%time.Second != 0 || p.MaxActiveTimeout%time.Second != 0 || p.MaxWaitingTimeout%time.Second != 0 {
		return ResolvedLimits{}, fmt.Errorf("invocation time limits must use whole seconds")
	}
	if err := validateRequestedLimits(input); err != nil {
		return ResolvedLimits{}, err
	}
	resolved := ResolvedLimits{
		TotalTimeout:   p.DefaultTotalTimeout,
		ActiveTimeout:  p.DefaultActiveTimeout,
		WaitingTimeout: p.DefaultWaitingTimeout,
		MaxIterations:  p.DefaultMaxIterations,
	}
	if input != nil {
		if input.TotalTimeoutSeconds != nil {
			resolved.TotalTimeout = time.Duration(*input.TotalTimeoutSeconds) * time.Second
		}
		if input.ActiveTimeoutSeconds != nil {
			resolved.ActiveTimeout = time.Duration(*input.ActiveTimeoutSeconds) * time.Second
		}
		if input.WaitingTimeoutSeconds != nil {
			resolved.WaitingTimeout = time.Duration(*input.WaitingTimeoutSeconds) * time.Second
		}
		if input.MaxOutputTokens != nil {
			value := *input.MaxOutputTokens
			resolved.MaxOutputTokens = &value
		}
		if input.MaxEstimatedCostUSD != nil {
			value := int64(math.Round(*input.MaxEstimatedCostUSD * 1_000_000))
			resolved.MaxEstimatedCostMicros = &value
		}
		if input.MaxIterations != nil {
			resolved.MaxIterations = *input.MaxIterations
		}
	}
	if resolved.TotalTimeout > p.MaxTotalTimeout {
		return ResolvedLimits{}, invalidRequest("spec.limits.total_timeout_seconds exceeds the installation maximum.")
	}
	if resolved.ActiveTimeout > p.MaxActiveTimeout {
		return ResolvedLimits{}, invalidRequest("spec.limits.active_timeout_seconds exceeds the installation maximum.")
	}
	if resolved.WaitingTimeout > p.MaxWaitingTimeout {
		return ResolvedLimits{}, invalidRequest("spec.limits.waiting_timeout_seconds exceeds the installation maximum.")
	}
	if resolved.MaxOutputTokens != nil && *resolved.MaxOutputTokens > p.MaxOutputTokens {
		return ResolvedLimits{}, invalidRequest("spec.limits.max_output_tokens exceeds the installation maximum.")
	}
	if resolved.MaxEstimatedCostMicros != nil && *resolved.MaxEstimatedCostMicros > p.MaxEstimatedCostMicros {
		return ResolvedLimits{}, invalidRequest("spec.limits.max_estimated_cost_usd exceeds the installation maximum.")
	}
	if resolved.MaxIterations > p.MaxIterations {
		return ResolvedLimits{}, invalidRequest("spec.limits.max_iterations exceeds the installation maximum.")
	}
	return resolved, nil
}

func (p LimitPolicy) ResolveForOutput(input *InvocationLimitInput, structured bool) (ResolvedLimits, error) {
	return p.ResolveForFeatures(input, structured, false)
}

func (p LimitPolicy) ResolveForFeatures(
	input *InvocationLimitInput,
	structuredOutput bool,
	hostTools bool,
) (ResolvedLimits, error) {
	resolved, err := p.Resolve(input)
	if err != nil || (!structuredOutput && !hostTools) {
		return resolved, err
	}
	if p.MaxIterations < 2 {
		return ResolvedLimits{}, invalidRequest("The installation iteration maximum does not support multi-iteration specs.")
	}
	if input != nil && input.MaxIterations != nil {
		if *input.MaxIterations < 2 {
			feature := "spec.output or spec.tools"
			if structuredOutput && !hostTools {
				feature = "spec.output"
			} else if hostTools && !structuredOutput {
				feature = "spec.tools"
			}
			return ResolvedLimits{}, invalidRequest("spec.limits.max_iterations must be at least 2 when " + feature + " is present.")
		}
		return resolved, nil
	}
	resolved.MaxIterations = min(3, p.MaxIterations)
	return resolved, nil
}

func limitReadFromDomain(invocation domain.Invocation) InvocationLimitRead {
	read := InvocationLimitRead{
		TotalTimeoutSeconds:   invocation.TotalTimeoutMS / 1000,
		ActiveTimeoutSeconds:  invocation.ActiveTimeoutMS / 1000,
		WaitingTimeoutSeconds: invocation.WaitingTimeoutMS / 1000,
		MaxOutputTokens:       invocation.MaxOutputTokens,
		MaxIterations:         invocation.MaxIterations,
	}
	if invocation.MaxEstimatedCostMicros != nil {
		value := float64(*invocation.MaxEstimatedCostMicros) / 1_000_000
		read.MaxEstimatedCostUSD = &value
	}
	return read
}

func (s *RuntimeService) CancelInvocation(ctx context.Context, auth domain.RuntimeAuthContext, invocationID string) (InvocationRead, error) {
	if err := s.ready(); err != nil {
		return InvocationRead{}, err
	}
	if err := authorize(auth, domain.OperationCancelInvocation); err != nil {
		return InvocationRead{}, err
	}
	if !domain.ValidStableID(invocationID, domain.PrefixInvocation) {
		return InvocationRead{}, invalidRequest("invocation_id is invalid.")
	}
	observed, err := s.store.GetInvocation(ctx, invocationID)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && (observed.AccountID != auth.AccountID || !auth.AllowsSession(observed.SessionID))) {
		return InvocationRead{}, notFound()
	}
	if err != nil {
		return InvocationRead{}, err
	}
	partition, err := s.store.GetTenantPartition(ctx, observed.TenantPartitionID)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && (partition.AccountID != auth.AccountID || !tenantMatches(auth.TenantConstraint, partition.TenantKey))) {
		return InvocationRead{}, notFound()
	}
	if err != nil {
		return InvocationRead{}, err
	}

	var result domain.Invocation
	transitioned := false
	err = s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, observed.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, invocationID)
		if errors.Is(err, ports.ErrNotFound) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if invocation.AccountID != auth.AccountID || invocation.TenantPartitionID != observed.TenantPartitionID {
			return notFound()
		}
		if invocation.Status.Terminal() {
			result = invocation
			return nil
		}
		currentState, err := s.store.GetCurrentInvocationState(txCtx, invocation.ID)
		if err != nil {
			return err
		}
		stateID, err := s.ids.NewID(domain.PrefixInvocationState)
		if err != nil {
			return err
		}
		revision, err := s.store.ReserveLifecycleRevision(txCtx, invocation.SessionID)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		throughMessageSequence := currentState.ThroughMessageSequence
		checkpointWatermark, err := closeOpenToolCallsForTerminal(
			txCtx, s.store, s.ids, invocation, domain.InvocationCancelled,
			"Tool execution stopped because the Invocation was cancelled.", now,
		)
		if err != nil {
			return err
		}
		if checkpointWatermark != nil && (throughMessageSequence == nil || *checkpointWatermark > *throughMessageSequence) {
			throughMessageSequence = checkpointWatermark
		}
		result, err = s.store.CancelInvocation(txCtx, invocation.ID, revision, now)
		if err != nil {
			return err
		}
		transitioned = true
		if err := s.store.AppendInvocationState(txCtx, lifecycleState(
			result, stateID, revision, domain.InvocationCancelled, throughMessageSequence, now,
		)); err != nil {
			return err
		}
		_, err = s.store.SettleActiveExecutionDispatchForWork(txCtx, domain.ExecutionDispatchInvocation, invocation.ID, now)
		return err
	})
	if err != nil {
		return InvocationRead{}, err
	}
	if result.Status == domain.InvocationCancelled && s.cancellations != nil {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		s.cancellations.NotifyCancellation(notifyCtx, result.ID)
		cancel()
	}
	s.logger.Info("Invocation cancellation committed",
		"invocation_id", result.ID, "status", result.Status, "transitioned", transitioned)
	return invocationReadFromDomain(result), nil
}
