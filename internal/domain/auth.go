package domain

import "time"

type RuntimeOperation string

const (
	OperationCreateInvocation         RuntimeOperation = "create_invocation"
	OperationGetInvocation            RuntimeOperation = "get_invocation"
	OperationSubmitToolResults        RuntimeOperation = "submit_tool_results"
	OperationCancelInvocation         RuntimeOperation = "cancel_invocation"
	OperationListInvocations          RuntimeOperation = "list_invocations"
	OperationGetSession               RuntimeOperation = "get_session"
	OperationListSessions             RuntimeOperation = "list_sessions"
	OperationListMessages             RuntimeOperation = "list_session_messages"
	OperationGetTranscript            RuntimeOperation = "get_session_transcript"
	OperationGetAccount               RuntimeOperation = "get_account"
	OperationListCredentials          RuntimeOperation = "list_credentials"
	OperationCreateCredential         RuntimeOperation = "create_credential"
	OperationGetCredential            RuntimeOperation = "get_credential"
	OperationRotateCredential         RuntimeOperation = "rotate_credential"
	OperationRevokeCredential         RuntimeOperation = "revoke_credential"
	OperationListProviderCredentials  RuntimeOperation = "list_provider_credentials"
	OperationCreateProviderCredential RuntimeOperation = "create_provider_credential"
	OperationGetProviderCredential    RuntimeOperation = "get_provider_credential"
	OperationRotateProviderCredential RuntimeOperation = "rotate_provider_credential"
	OperationRevokeProviderCredential RuntimeOperation = "revoke_provider_credential"
)

type RuntimeAuthContext struct {
	AccountID            string
	TenantConstraint     *string
	SessionConstraint    *string
	Operations           map[RuntimeOperation]struct{}
	CredentialID         string
	CredentialKind       CredentialKind
	Subject              *OperatorSubject
	EffectiveProfile     CredentialProfile
	AuthenticationMethod string
	Assurance            string
}

func (c RuntimeAuthContext) Allows(operation RuntimeOperation) bool {
	_, ok := c.Operations[operation]
	return ok
}

func (c RuntimeAuthContext) AllowsSession(sessionID string) bool {
	return c.SessionConstraint == nil || *c.SessionConstraint == sessionID
}

type CredentialKind string

const (
	CredentialKindMachine CredentialKind = "machine"
	CredentialKindUser    CredentialKind = "user"
)

type CredentialProfile string

const (
	CredentialProfileRuntime  CredentialProfile = "Runtime"
	CredentialProfileViewer   CredentialProfile = "Viewer"
	CredentialProfileOperator CredentialProfile = "Operator"
)

type MembershipRole string

const (
	MembershipRoleViewer   MembershipRole = "Viewer"
	MembershipRoleOperator MembershipRole = "Operator"
	MembershipRoleOwner    MembershipRole = "Owner"
)

type CredentialStatus string

const (
	CredentialStatusActive  CredentialStatus = "active"
	CredentialStatusRevoked CredentialStatus = "revoked"
)

type OperatorSubject struct {
	ID        string
	AccountID string
	Issuer    string
	Subject   string
	CreatedAt time.Time
}

type Membership struct {
	ID        string
	AccountID string
	SubjectID string
	Role      MembershipRole
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Credential struct {
	ID                    string
	AccountID             string
	Kind                  CredentialKind
	Name                  string
	Prefix                string
	Verifier              []byte `json:"-"`
	Status                CredentialStatus
	Profile               *CredentialProfile
	RoleCap               *CredentialProfile
	OwnerSubjectID        *string
	CreatorSubjectID      *string
	CreatorCredentialID   *string
	TenantConstraint      *string
	SessionConstraint     *string
	OperationConstraints  []RuntimeOperation
	ExpiresAt             *time.Time
	RotatedFromID         *string
	RotationOverlapEndsAt *time.Time
	RevokedAt             *time.Time
	LastUsedAt            *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type CredentialIssuance struct {
	AccountID      string
	Scope          string
	IdempotencyKey string
	RequestHash    []byte
	CredentialID   string
	Ciphertext     []byte
	Nonce          []byte
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

type DeviceAuthorizationStatus string

const (
	DeviceAuthorizationPending   DeviceAuthorizationStatus = "pending"
	DeviceAuthorizationApproved  DeviceAuthorizationStatus = "approved"
	DeviceAuthorizationDenied    DeviceAuthorizationStatus = "denied"
	DeviceAuthorizationExchanged DeviceAuthorizationStatus = "exchanged"
)

type DeviceAuthorization struct {
	ID                   string
	AccountID            string
	DeviceCodeHash       []byte
	UserCodeHash         []byte
	UserCodeDisplay      string
	DeviceLabel          string
	RoleCap              CredentialProfile
	TenantConstraint     *string
	SessionConstraint    *string
	Status               DeviceAuthorizationStatus
	PollInterval         time.Duration
	NextPollAt           time.Time
	ConfirmationAttempts int
	ApprovedBySubjectID  *string
	CredentialID         *string
	ExpiresAt            time.Time
	DeliveryExpiresAt    *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type BrowserSession struct {
	ID        string
	AccountID string
	SubjectID string
	TokenHash []byte
	CSRFHash  []byte
	ExpiresAt time.Time
	CreatedAt time.Time
}
