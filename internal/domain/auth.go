package domain

type RuntimeOperation string

const (
	OperationCreateInvocation RuntimeOperation = "create_invocation"
	OperationGetInvocation    RuntimeOperation = "get_invocation"
	OperationGetSession       RuntimeOperation = "get_session"
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
