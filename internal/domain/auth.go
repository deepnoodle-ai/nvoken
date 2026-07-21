package domain

type RuntimeOperation string

const (
	OperationCreateInvocation RuntimeOperation = "create_invocation"
	OperationGetInvocation    RuntimeOperation = "get_invocation"
	OperationListInvocations  RuntimeOperation = "list_invocations"
	OperationGetSession       RuntimeOperation = "get_session"
	OperationListSessions     RuntimeOperation = "list_sessions"
	OperationListMessages     RuntimeOperation = "list_session_messages"
	OperationGetTranscript    RuntimeOperation = "get_session_transcript"
)

type RuntimeAuthContext struct {
	AccountID        string
	TenantConstraint *string
	Operations       map[RuntimeOperation]struct{}
}

func (c RuntimeAuthContext) Allows(operation RuntimeOperation) bool {
	_, ok := c.Operations[operation]
	return ok
}
