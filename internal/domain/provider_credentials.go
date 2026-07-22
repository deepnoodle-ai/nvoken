package domain

import "time"

type ModelProvider string

const (
	ModelProviderAnthropic ModelProvider = "anthropic"
	ModelProviderOpenAI    ModelProvider = "openai"
)

type ModelPricingStatus string

const (
	ModelPricingPriced   ModelPricingStatus = "priced"
	ModelPricingUnpriced ModelPricingStatus = "unpriced"
	ModelPricingUnknown  ModelPricingStatus = "unknown"
)

type ModelPricingCapability struct {
	Provider        ModelProvider
	Model           string
	Status          ModelPricingStatus
	RegistryVersion string
}

type ProviderCredentialSource string

const (
	ProviderCredentialSourceCallerEphemeral  ProviderCredentialSource = "caller_ephemeral"
	ProviderCredentialSourceAccountBYOK      ProviderCredentialSource = "account_byok"
	ProviderCredentialSourceTenantBYOK       ProviderCredentialSource = "tenant_byok"
	ProviderCredentialSourcePlatform         ProviderCredentialSource = "platform"
	ProviderCredentialSourceInstallationBYOK ProviderCredentialSource = "installation_byok"
)

func (s ProviderCredentialSource) Valid() bool {
	switch s {
	case ProviderCredentialSourceCallerEphemeral,
		ProviderCredentialSourceAccountBYOK,
		ProviderCredentialSourceTenantBYOK,
		ProviderCredentialSourcePlatform,
		ProviderCredentialSourceInstallationBYOK:
		return true
	default:
		return false
	}
}

type ProviderCredentialScope string

const (
	ProviderCredentialScopeAccount ProviderCredentialScope = "account"
	ProviderCredentialScopeTenant  ProviderCredentialScope = "tenant"
)

type ProviderCredentialStatus string

const (
	ProviderCredentialActive  ProviderCredentialStatus = "active"
	ProviderCredentialRevoked ProviderCredentialStatus = "revoked"
)

type ProviderCredentialVersionStatus string

const (
	ProviderCredentialVersionActive  ProviderCredentialVersionStatus = "active"
	ProviderCredentialVersionOverlap ProviderCredentialVersionStatus = "overlap"
	ProviderCredentialVersionExpired ProviderCredentialVersionStatus = "expired"
	ProviderCredentialVersionRevoked ProviderCredentialVersionStatus = "revoked"
)

type ProviderCredential struct {
	ID                   string
	AccountID            string
	TenantPartitionID    *string
	Provider             string
	Scope                ProviderCredentialScope
	Status               ProviderCredentialStatus
	CurrentVersionID     string
	CurrentVersion       int
	CreateIdempotencyKey string
	CreateFingerprint    []byte
	CreatedBy            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	RevokedAt            *time.Time
}

type ProviderCredentialVersion struct {
	ID                     string
	ProviderCredentialID   string
	AccountID              string
	TenantPartitionID      *string
	Provider               string
	Version                int
	Status                 ProviderCredentialVersionStatus
	PreviousVersionID      *string
	EncryptionKeyID        *string
	Nonce                  []byte
	Ciphertext             []byte
	ExpiresAt              *time.Time
	OverlapExpiresAt       *time.Time
	RotationIdempotencyKey *string
	RotationFingerprint    []byte
	CreatedBy              string
	CreatedAt              time.Time
	DestroyedAt            *time.Time
}

type InvocationProviderCredential struct {
	ID                   string
	InvocationID         string
	AccountID            string
	TenantPartitionID    string
	Provider             string
	Source               ProviderCredentialSource
	ProviderCredentialID *string
	CredentialVersionID  *string
	Selector             *string
	EncryptionKeyID      *string
	Nonce                []byte
	Ciphertext           []byte
	ExpiresAt            *time.Time
	ClearedAt            *time.Time
	CreatedAt            time.Time
}

type EncryptedCredential struct {
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

type ResolvedProviderCredential struct {
	Provider             string
	Source               ProviderCredentialSource
	ProviderCredentialID string
	CredentialVersionID  string
	APIKey               string
}
