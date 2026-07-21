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
	maxBudgetSeconds      = int64((7 * 24 * time.Hour) / time.Second)
	maxBudgetOutputTokens = 10_000_000
	maxBudgetCostMicros   = int64(1_000_000 * 1_000_000)
	maxBudgetIterations   = 10_000
)

type InvocationBudgetInput struct {
	WallClockTimeoutSeconds       *int64   `json:"wall_clock_timeout_seconds,omitempty"`
	ActiveExecutionTimeoutSeconds *int64   `json:"active_execution_timeout_seconds,omitempty"`
	MaxOutputTokens               *int     `json:"max_output_tokens,omitempty"`
	MaxEstimatedCostUSD           *float64 `json:"max_estimated_cost_usd,omitempty"`
	MaxIterations                 *int     `json:"max_iterations,omitempty"`
}

type InvocationBudgetRead struct {
	WallClockTimeoutSeconds       int64    `json:"wall_clock_timeout_seconds"`
	ActiveExecutionTimeoutSeconds int64    `json:"active_execution_timeout_seconds"`
	MaxOutputTokens               *int     `json:"max_output_tokens,omitempty"`
	MaxEstimatedCostUSD           *float64 `json:"max_estimated_cost_usd,omitempty"`
	MaxIterations                 int      `json:"max_iterations"`
}

type BudgetPolicy struct {
	DefaultWallClockTimeout       time.Duration
	DefaultActiveExecutionTimeout time.Duration
	DefaultMaxIterations          int
	MaxWallClockTimeout           time.Duration
	MaxActiveExecutionTimeout     time.Duration
	MaxOutputTokens               int
	MaxEstimatedCostMicros        int64
	MaxIterations                 int
}

type ResolvedBudgets struct {
	WallClockTimeout       time.Duration
	ActiveExecutionTimeout time.Duration
	MaxOutputTokens        *int
	MaxEstimatedCostMicros *int64
	MaxIterations          int
}

func DefaultBudgetPolicy() BudgetPolicy {
	return BudgetPolicy{
		DefaultWallClockTimeout: 30 * time.Minute, DefaultActiveExecutionTimeout: 30 * time.Minute,
		DefaultMaxIterations: 1, MaxWallClockTimeout: 24 * time.Hour,
		MaxActiveExecutionTimeout: 24 * time.Hour, MaxOutputTokens: 1_000_000,
		MaxEstimatedCostMicros: 1_000_000_000, MaxIterations: 100,
	}
}

func validateRequestedBudgets(input *InvocationBudgetInput) error {
	if input == nil {
		return nil
	}
	if input.WallClockTimeoutSeconds != nil && (*input.WallClockTimeoutSeconds <= 0 || *input.WallClockTimeoutSeconds > maxBudgetSeconds) {
		return invalidRequest("spec.budgets.wall_clock_timeout_seconds must be a positive supported value.")
	}
	if input.ActiveExecutionTimeoutSeconds != nil && (*input.ActiveExecutionTimeoutSeconds <= 0 || *input.ActiveExecutionTimeoutSeconds > maxBudgetSeconds) {
		return invalidRequest("spec.budgets.active_execution_timeout_seconds must be a positive supported value.")
	}
	if input.MaxOutputTokens != nil && (*input.MaxOutputTokens <= 0 || *input.MaxOutputTokens > maxBudgetOutputTokens) {
		return invalidRequest("spec.budgets.max_output_tokens must be a positive supported value.")
	}
	if input.MaxIterations != nil && (*input.MaxIterations <= 0 || *input.MaxIterations > maxBudgetIterations) {
		return invalidRequest("spec.budgets.max_iterations must be a positive supported value.")
	}
	if input.MaxEstimatedCostUSD != nil {
		value := *input.MaxEstimatedCostUSD
		scaled := value * 1_000_000
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > float64(maxBudgetCostMicros)/1_000_000 ||
			math.Abs(scaled-math.Round(scaled)) > 1e-6 {
			return invalidRequest("spec.budgets.max_estimated_cost_usd must be positive, finite, and have at most six decimal places.")
		}
	}
	return nil
}

func (p BudgetPolicy) Resolve(input *InvocationBudgetInput) (ResolvedBudgets, error) {
	if p.DefaultWallClockTimeout <= 0 || p.DefaultActiveExecutionTimeout <= 0 || p.DefaultMaxIterations <= 0 ||
		p.MaxWallClockTimeout < p.DefaultWallClockTimeout || p.MaxActiveExecutionTimeout < p.DefaultActiveExecutionTimeout ||
		p.MaxOutputTokens <= 0 || p.MaxEstimatedCostMicros <= 0 || p.MaxIterations < p.DefaultMaxIterations {
		return ResolvedBudgets{}, fmt.Errorf("invocation budget policy is invalid")
	}
	if p.MaxWallClockTimeout > time.Duration(maxBudgetSeconds)*time.Second ||
		p.MaxActiveExecutionTimeout > time.Duration(maxBudgetSeconds)*time.Second ||
		p.MaxOutputTokens > maxBudgetOutputTokens || p.MaxEstimatedCostMicros > maxBudgetCostMicros ||
		p.MaxIterations > maxBudgetIterations {
		return ResolvedBudgets{}, fmt.Errorf("invocation budget policy exceeds fixed safety limits")
	}
	if p.DefaultWallClockTimeout%time.Second != 0 || p.DefaultActiveExecutionTimeout%time.Second != 0 ||
		p.MaxWallClockTimeout%time.Second != 0 || p.MaxActiveExecutionTimeout%time.Second != 0 {
		return ResolvedBudgets{}, fmt.Errorf("invocation time budgets must use whole seconds")
	}
	if err := validateRequestedBudgets(input); err != nil {
		return ResolvedBudgets{}, err
	}
	resolved := ResolvedBudgets{
		WallClockTimeout: p.DefaultWallClockTimeout, ActiveExecutionTimeout: p.DefaultActiveExecutionTimeout,
		MaxIterations: p.DefaultMaxIterations,
	}
	if input != nil {
		if input.WallClockTimeoutSeconds != nil {
			resolved.WallClockTimeout = time.Duration(*input.WallClockTimeoutSeconds) * time.Second
		}
		if input.ActiveExecutionTimeoutSeconds != nil {
			resolved.ActiveExecutionTimeout = time.Duration(*input.ActiveExecutionTimeoutSeconds) * time.Second
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
	if resolved.WallClockTimeout > p.MaxWallClockTimeout {
		return ResolvedBudgets{}, invalidRequest("spec.budgets.wall_clock_timeout_seconds exceeds the installation maximum.")
	}
	if resolved.ActiveExecutionTimeout > p.MaxActiveExecutionTimeout {
		return ResolvedBudgets{}, invalidRequest("spec.budgets.active_execution_timeout_seconds exceeds the installation maximum.")
	}
	if resolved.MaxOutputTokens != nil && *resolved.MaxOutputTokens > p.MaxOutputTokens {
		return ResolvedBudgets{}, invalidRequest("spec.budgets.max_output_tokens exceeds the installation maximum.")
	}
	if resolved.MaxEstimatedCostMicros != nil && *resolved.MaxEstimatedCostMicros > p.MaxEstimatedCostMicros {
		return ResolvedBudgets{}, invalidRequest("spec.budgets.max_estimated_cost_usd exceeds the installation maximum.")
	}
	if resolved.MaxIterations > p.MaxIterations {
		return ResolvedBudgets{}, invalidRequest("spec.budgets.max_iterations exceeds the installation maximum.")
	}
	return resolved, nil
}

func budgetReadFromDomain(invocation domain.Invocation) InvocationBudgetRead {
	read := InvocationBudgetRead{
		WallClockTimeoutSeconds:       invocation.WallClockTimeoutMS / 1000,
		ActiveExecutionTimeoutSeconds: invocation.ActiveTimeoutMS / 1000,
		MaxOutputTokens:               invocation.MaxOutputTokens, MaxIterations: invocation.MaxIterations,
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
	if errors.Is(err, ports.ErrNotFound) || (err == nil && observed.AccountID != auth.AccountID) {
		return InvocationRead{}, notFound()
	}
	if err != nil {
		return InvocationRead{}, err
	}
	partition, err := s.store.GetTenantPartition(ctx, observed.TenantPartitionID)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && (partition.AccountID != auth.AccountID || !tenantMatches(auth.TenantConstraint, partition.TenantRef))) {
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
