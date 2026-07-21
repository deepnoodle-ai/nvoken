package domain

import "strings"

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
	PrefixToolCall              StableIDPrefix = "tcal"
	PrefixToolCallAttempt       StableIDPrefix = "tcat"
	PrefixModelUsageReceipt     StableIDPrefix = "usgr"
	PrefixInvocationCheckpoint  StableIDPrefix = "icpt"
	PrefixSyntheticDispatchWork StableIDPrefix = "synw"
	PrefixExecutionDispatch     StableIDPrefix = "dsp"
)

func (p StableIDPrefix) Valid() bool {
	switch p {
	case PrefixAccount, PrefixTenantPartition, PrefixAgent, PrefixSession,
		PrefixExecutionSpecSnapshot, PrefixSessionMessage, PrefixInvocation,
		PrefixInvocationState, PrefixToolCall, PrefixToolCallAttempt,
		PrefixModelUsageReceipt, PrefixInvocationCheckpoint,
		PrefixSyntheticDispatchWork, PrefixExecutionDispatch:
		return true
	default:
		return false
	}
}

func ValidStableID(value string, prefix StableIDPrefix) bool {
	if !prefix.Valid() || !strings.HasPrefix(value, string(prefix)+"_") {
		return false
	}
	uuid := strings.TrimPrefix(value, string(prefix)+"_")
	if len(uuid) != 36 || uuid[8] != '-' || uuid[13] != '-' || uuid[18] != '-' || uuid[23] != '-' {
		return false
	}
	if uuid[14] != '7' || !strings.ContainsRune("89ab", rune(uuid[19])) {
		return false
	}
	for i, char := range uuid {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}
