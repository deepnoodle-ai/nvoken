package domain

// StableIDPrefix identifies one durable runtime record type. IDs are generated
// in application code so callers can allocate all records before a transaction.
type StableIDPrefix string

const (
	PrefixAccount               StableIDPrefix = "acct"
	PrefixTenantPartition       StableIDPrefix = "tprt"
	PrefixAgent                 StableIDPrefix = "agnt"
	PrefixSession               StableIDPrefix = "sesn"
	PrefixExecutionSpecSnapshot StableIDPrefix = "spec"
	PrefixSessionMessage        StableIDPrefix = "smsg"
	PrefixInvocation            StableIDPrefix = "invk"
	PrefixInvocationState       StableIDPrefix = "ivst"
)

func (p StableIDPrefix) Valid() bool {
	switch p {
	case PrefixAccount, PrefixTenantPartition, PrefixAgent, PrefixSession,
		PrefixExecutionSpecSnapshot, PrefixSessionMessage, PrefixInvocation,
		PrefixInvocationState:
		return true
	default:
		return false
	}
}
