package domain

import "time"

type SyntheticDispatchWorkStatus string

const (
	SyntheticDispatchWorkPending SyntheticDispatchWorkStatus = "pending"
	SyntheticDispatchWorkSettled SyntheticDispatchWorkStatus = "settled"
)

type SyntheticDispatchWork struct {
	ID              string
	Status          SyntheticDispatchWorkStatus
	SettlementCount int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	SettledAt       *time.Time
}

type ExecutionDispatchKind string

const (
	ExecutionDispatchSynthetic  ExecutionDispatchKind = "synthetic"
	ExecutionDispatchInvocation ExecutionDispatchKind = "invocation"
)

type ExecutionDispatchStatus string

const (
	ExecutionDispatchPending    ExecutionDispatchStatus = "pending"
	ExecutionDispatchPublishing ExecutionDispatchStatus = "publishing"
	ExecutionDispatchPublished  ExecutionDispatchStatus = "published"
	ExecutionDispatchSettled    ExecutionDispatchStatus = "settled"
	ExecutionDispatchAbandoned  ExecutionDispatchStatus = "abandoned"
)

func (s ExecutionDispatchStatus) Terminal() bool {
	return s == ExecutionDispatchSettled || s == ExecutionDispatchAbandoned
}

type ExecutionDispatch struct {
	ID                    string
	Kind                  ExecutionDispatchKind
	WorkID                string
	AccountID             *string
	TenantPartitionID     *string
	Queue                 string
	Status                ExecutionDispatchStatus
	AvailableAt           time.Time
	TaskName              *string
	PublishAttempts       int
	LastError             *string
	PublisherOwner        *string
	PublisherLeaseExpires *time.Time
	PublisherAttempt      int64
	PublishedAt           *time.Time
	SettledAt             *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type ExecutionDispatchClaim struct {
	Dispatch ExecutionDispatch
	Owner    string
	Attempt  int64
}
