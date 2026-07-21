package services

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const (
	defaultIssuanceDeliveryWindow = 10 * time.Minute
	defaultDeviceLifetime         = 15 * time.Minute
	defaultDevicePollInterval     = 5 * time.Second
	defaultBrowserSessionLifetime = 15 * time.Minute
	credentialLastUsedInterval    = 5 * time.Minute
	maximumRotationOverlap        = 24 * time.Hour
	maximumCredentialNameRunes    = 100
	maximumDeviceLabelRunes       = 100
	bootstrapIssuer               = "nvoken:installation"
	bootstrapSubject              = "bootstrap-owner"
	staticImportKey               = "runtime-api-key-v1"
)

type IdentityConfig struct {
	AccountID              string
	VerificationBaseURL    string
	DeliveryEncryptionKey  []byte
	BootstrapOwnerSecret   string
	IssuanceDeliveryWindow time.Duration
	DeviceLifetime         time.Duration
	DevicePollInterval     time.Duration
	BrowserSessionLifetime time.Duration
	Random                 io.Reader
}

type IdentityService struct {
	store               ports.IdentityRepository
	accounts            ports.AccountRepository
	txm                 ports.TransactionManager
	clock               ports.Clock
	ids                 ports.IDGenerator
	random              io.Reader
	aead                cipher.AEAD
	accountID           string
	verificationBaseURL string
	bootstrapHash       [sha256.Size]byte
	issuanceWindow      time.Duration
	deviceLifetime      time.Duration
	devicePoll          time.Duration
	browserLifetime     time.Duration
}

func NewIdentityService(
	store ports.IdentityRepository,
	accounts ports.AccountRepository,
	txm ports.TransactionManager,
	clock ports.Clock,
	ids ports.IDGenerator,
	cfg IdentityConfig,
) (*IdentityService, error) {
	if store == nil || accounts == nil || txm == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("identity service dependencies are required")
	}
	if !domain.ValidStableID(cfg.AccountID, domain.PrefixAccount) {
		return nil, fmt.Errorf("identity Account ID is invalid")
	}
	if len(cfg.DeliveryEncryptionKey) != 32 {
		return nil, fmt.Errorf("credential delivery encryption key must be exactly 32 bytes")
	}
	if len(cfg.BootstrapOwnerSecret) < 32 {
		return nil, fmt.Errorf("bootstrap Owner secret must be at least 32 bytes")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.VerificationBaseURL), "/")
	if baseURL != "" {
		parsedBaseURL, err := url.Parse(baseURL)
		if err != nil || parsedBaseURL.Host == "" || (parsedBaseURL.Scheme != "https" && !(parsedBaseURL.Scheme == "http" && isLoopbackHost(parsedBaseURL.Hostname()))) {
			return nil, fmt.Errorf("verification base URL must use HTTPS, except for loopback development")
		}
	}
	block, err := aes.NewCipher(cfg.DeliveryEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("create credential delivery cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential delivery AEAD: %w", err)
	}
	s := &IdentityService{
		store:               store,
		accounts:            accounts,
		txm:                 txm,
		clock:               clock,
		ids:                 ids,
		random:              cfg.Random,
		aead:                aead,
		accountID:           cfg.AccountID,
		verificationBaseURL: baseURL,
		bootstrapHash:       sha256.Sum256([]byte(cfg.BootstrapOwnerSecret)),
		issuanceWindow:      cfg.IssuanceDeliveryWindow,
		deviceLifetime:      cfg.DeviceLifetime,
		devicePoll:          cfg.DevicePollInterval,
		browserLifetime:     cfg.BrowserSessionLifetime,
	}
	if s.random == nil {
		s.random = rand.Reader
	}
	if s.issuanceWindow <= 0 {
		s.issuanceWindow = defaultIssuanceDeliveryWindow
	}
	if s.deviceLifetime <= 0 {
		s.deviceLifetime = defaultDeviceLifetime
	}
	if s.devicePoll <= 0 {
		s.devicePoll = defaultDevicePollInterval
	}
	if s.browserLifetime <= 0 {
		s.browserLifetime = defaultBrowserSessionLifetime
	}
	return s, nil
}

type CredentialCreateInput struct {
	Name                 string                    `json:"name"`
	Profile              domain.CredentialProfile  `json:"profile"`
	TenantConstraint     *string                   `json:"tenant_ref,omitempty"`
	SessionConstraint    *string                   `json:"session_id,omitempty"`
	OperationConstraints []domain.RuntimeOperation `json:"operations,omitempty"`
	ExpiresAt            *time.Time                `json:"expires_at,omitempty"`
}

type CredentialRotateInput struct {
	Overlap time.Duration `json:"-"`
}

type CredentialIssuanceResult struct {
	Credential domain.Credential `json:"credential"`
	Secret     string            `json:"secret"`
	ExpiresAt  time.Time         `json:"delivery_expires_at"`
	Replayed   bool              `json:"replayed"`
}

type AccountIdentity struct {
	Account       domain.Account
	Credential    domain.Credential
	Subject       *domain.OperatorSubject
	EffectiveRole domain.CredentialProfile
	Method        string
	Assurance     string
	Operations    []domain.RuntimeOperation
}

type DeviceCodeInput struct {
	DeviceLabel       string                   `json:"device_label"`
	RoleCap           domain.CredentialProfile `json:"role_cap,omitempty"`
	TenantConstraint  *string                  `json:"tenant_ref,omitempty"`
	SessionConstraint *string                  `json:"session_id,omitempty"`
}

type DeviceCodeResult struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type DeviceTokenError string

const (
	DeviceAuthorizationPending DeviceTokenError = "authorization_pending"
	DeviceSlowDown             DeviceTokenError = "slow_down"
	DeviceAccessDenied         DeviceTokenError = "access_denied"
	DeviceExpiredToken         DeviceTokenError = "expired_token"
	DeviceUnsupportedGrantType DeviceTokenError = "unsupported_grant_type"
)

type DeviceFlowError struct{ Code DeviceTokenError }

func (e *DeviceFlowError) Error() string { return string(e.Code) }

type DeviceApprovalView struct {
	AccountID         string
	Approver          domain.OperatorSubject
	DeviceLabel       string
	RoleCap           domain.CredentialProfile
	TenantConstraint  *string
	SessionConstraint *string
	ExpiresAt         time.Time
}

type BootstrapBrowserSession struct {
	Token     string
	CSRFToken string
	ExpiresAt time.Time
}

func (s *IdentityService) BootstrapOwner(ctx context.Context) (domain.OperatorSubject, error) {
	var result domain.OperatorSubject
	err := s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		if err := s.accounts.LockInstallationBootstrap(ctx); err != nil {
			return fmt.Errorf("lock identity bootstrap: %w", err)
		}
		subject, err := s.store.GetOperatorSubjectByIdentity(ctx, s.accountID, bootstrapIssuer, bootstrapSubject)
		if errors.Is(err, ports.ErrNotFound) {
			id, idErr := s.ids.NewID(domain.PrefixOperatorSubject)
			if idErr != nil {
				return idErr
			}
			subject = domain.OperatorSubject{ID: id, AccountID: s.accountID, Issuer: bootstrapIssuer, Subject: bootstrapSubject, CreatedAt: s.clock.Now().UTC()}
			if err := s.store.CreateOperatorSubject(ctx, subject); err != nil {
				return fmt.Errorf("create bootstrap subject: %w", err)
			}
		} else if err != nil {
			return err
		}
		membershipID, err := s.ids.NewID(domain.PrefixMembership)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		if _, err := s.store.UpsertMembership(ctx, domain.Membership{ID: membershipID, AccountID: s.accountID, SubjectID: subject.ID, Role: domain.MembershipRoleOwner, CreatedAt: now, UpdatedAt: now}); err != nil {
			return fmt.Errorf("provision bootstrap Owner membership: %w", err)
		}
		result = subject
		return nil
	})
	return result, err
}

// ProvisionMembership is installation plumbing used by bootstrap today and by
// future OIDC or hosted-control-plane adapters. It is deliberately not an HTTP API.
func (s *IdentityService) ProvisionMembership(ctx context.Context, issuer, subject string, role domain.MembershipRole) (domain.Membership, error) {
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(subject) == "" || !validMembershipRole(role) {
		return domain.Membership{}, invalidIdentityRequest("issuer, subject, and role are required")
	}
	var result domain.Membership
	err := s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		operator, err := s.store.GetOperatorSubjectByIdentity(ctx, s.accountID, issuer, subject)
		if errors.Is(err, ports.ErrNotFound) {
			id, idErr := s.ids.NewID(domain.PrefixOperatorSubject)
			if idErr != nil {
				return idErr
			}
			operator = domain.OperatorSubject{ID: id, AccountID: s.accountID, Issuer: issuer, Subject: subject, CreatedAt: s.clock.Now().UTC()}
			if err := s.store.CreateOperatorSubject(ctx, operator); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		membershipID, err := s.ids.NewID(domain.PrefixMembership)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		result, err = s.store.UpsertMembership(ctx, domain.Membership{ID: membershipID, AccountID: s.accountID, SubjectID: operator.ID, Role: role, CreatedAt: now, UpdatedAt: now})
		return err
	})
	return result, err
}

// RemoveMembership is installation plumbing for immediately removing a
// subject's user-credential authority. It is deliberately not an HTTP API.
func (s *IdentityService) RemoveMembership(ctx context.Context, issuer, subject string) error {
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(subject) == "" {
		return invalidIdentityRequest("issuer and subject are required")
	}
	return s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		operator, err := s.store.GetOperatorSubjectByIdentity(ctx, s.accountID, issuer, subject)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.store.DeleteMembershipBySubject(ctx, s.accountID, operator.ID)
	})
}

func (s *IdentityService) ImportStaticRuntimeCredential(ctx context.Context, token string, tenantConstraint *string) (domain.Credential, error) {
	var result domain.Credential
	err := s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		if err := s.accounts.LockInstallationBootstrap(ctx); err != nil {
			return fmt.Errorf("lock static credential import: %w", err)
		}
		credentialID, err := s.store.GetStaticCredentialImport(ctx, s.accountID, staticImportKey)
		if err == nil {
			result, err = s.store.GetCredential(ctx, credentialID)
			return err
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		if len(token) < 32 {
			return fmt.Errorf("RUNTIME_API_KEY is required until the installation credential has been imported and explicitly cut over")
		}
		if err := validateConstraint(tenantConstraint, "runtime tenant constraint", false); err != nil {
			return err
		}
		id, err := s.ids.NewID(domain.PrefixCredential)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		profile := domain.CredentialProfileRuntime
		result = domain.Credential{
			ID:               id,
			AccountID:        s.accountID,
			Kind:             domain.CredentialKindMachine,
			Name:             "Imported RUNTIME_API_KEY",
			Prefix:           tokenLookupPrefix(token),
			Verifier:         tokenVerifier(token),
			Status:           domain.CredentialStatusActive,
			Profile:          &profile,
			TenantConstraint: cloneString(tenantConstraint),
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := s.store.CreateCredential(ctx, result); err != nil {
			return err
		}
		return s.store.CreateStaticCredentialImport(ctx, s.accountID, staticImportKey, result.ID, now)
	})
	return result, err
}

func (s *IdentityService) Authenticate(ctx context.Context, token string) (domain.RuntimeAuthContext, error) {
	if token == "" {
		return domain.RuntimeAuthContext{}, ports.ErrUnauthenticated
	}
	credential, err := s.store.GetCredentialByPrefix(ctx, tokenLookupPrefix(token))
	if err != nil {
		dummy := sha256.Sum256([]byte("nvoken-invalid-credential"))
		presented := tokenVerifier(token)
		_ = subtle.ConstantTimeCompare(dummy[:], presented)
		return domain.RuntimeAuthContext{}, ports.ErrUnauthenticated
	}
	now := s.clock.Now().UTC()
	presented := tokenVerifier(token)
	if subtle.ConstantTimeCompare(credential.Verifier, presented) != 1 || !credentialUsable(credential, now) {
		return domain.RuntimeAuthContext{}, ports.ErrUnauthenticated
	}
	profile, subject, err := s.resolveCredentialProfile(ctx, credential)
	if err != nil {
		return domain.RuntimeAuthContext{}, ports.ErrUnauthenticated
	}
	operations := profileOperations(profile)
	if credential.Kind == domain.CredentialKindUser {
		operations[domain.OperationGetCredential] = struct{}{}
		operations[domain.OperationRevokeCredential] = struct{}{}
	}
	if len(credential.OperationConstraints) > 0 {
		allowed := make(map[domain.RuntimeOperation]struct{}, len(credential.OperationConstraints))
		for _, operation := range credential.OperationConstraints {
			if _, ok := operations[operation]; ok {
				allowed[operation] = struct{}{}
			}
		}
		operations = allowed
	}
	if credential.LastUsedAt == nil || now.Sub(*credential.LastUsedAt) >= credentialLastUsedInterval {
		// Usage metadata must not turn an otherwise valid credential into an
		// authentication outage. The adapter also guards this update so
		// concurrent requests cannot continually rewrite the same row.
		_ = s.store.TouchCredential(ctx, credential.ID, now)
	}
	method := "api_key"
	if credential.Kind == domain.CredentialKindUser {
		method = "device_authorization"
	}
	return domain.RuntimeAuthContext{
		AccountID:            credential.AccountID,
		TenantConstraint:     cloneString(credential.TenantConstraint),
		SessionConstraint:    cloneString(credential.SessionConstraint),
		Operations:           operations,
		CredentialID:         credential.ID,
		CredentialKind:       credential.Kind,
		Subject:              subject,
		EffectiveProfile:     profile,
		AuthenticationMethod: method,
		Assurance:            "bearer",
	}, nil
}

func (s *IdentityService) CurrentAccount(ctx context.Context, auth domain.RuntimeAuthContext) (AccountIdentity, error) {
	if !auth.Allows(domain.OperationGetAccount) {
		return AccountIdentity{}, &PublicError{Code: CodeForbidden, Message: "The authenticated credential is not permitted to make this request."}
	}
	credential, err := s.store.GetCredential(ctx, auth.CredentialID)
	if err != nil || credential.AccountID != auth.AccountID {
		return AccountIdentity{}, &PublicError{Code: CodeNotFound, Message: "The credential was not found."}
	}
	account, err := s.accounts.GetAccount(ctx, auth.AccountID)
	if err != nil {
		return AccountIdentity{}, err
	}
	operations := make([]domain.RuntimeOperation, 0, len(auth.Operations))
	for operation := range auth.Operations {
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i] < operations[j] })
	return AccountIdentity{Account: account, Credential: credential, Subject: auth.Subject, EffectiveRole: auth.EffectiveProfile, Method: auth.AuthenticationMethod, Assurance: auth.Assurance, Operations: operations}, nil
}

func (s *IdentityService) ListCredentials(ctx context.Context, auth domain.RuntimeAuthContext) ([]domain.Credential, error) {
	if auth.EffectiveProfile != domain.CredentialProfileOperator || !auth.Allows(domain.OperationListCredentials) {
		return nil, &PublicError{Code: CodeForbidden, Message: "Operator authority is required."}
	}
	return s.store.ListCredentials(ctx, auth.AccountID)
}

func (s *IdentityService) GetCredential(ctx context.Context, auth domain.RuntimeAuthContext, id string) (domain.Credential, error) {
	credential, err := s.store.GetCredential(ctx, id)
	if err != nil || credential.AccountID != auth.AccountID {
		return domain.Credential{}, &PublicError{Code: CodeNotFound, Message: "The credential was not found."}
	}
	if auth.Allows(domain.OperationGetCredential) && (auth.EffectiveProfile == domain.CredentialProfileOperator || (credential.Kind == domain.CredentialKindUser && credential.ID == auth.CredentialID)) {
		return credential, nil
	}
	return domain.Credential{}, &PublicError{Code: CodeForbidden, Message: "The credential is not available to this caller."}
}

func (s *IdentityService) CreateMachineCredential(ctx context.Context, auth domain.RuntimeAuthContext, key string, input CredentialCreateInput) (CredentialIssuanceResult, error) {
	if auth.EffectiveProfile != domain.CredentialProfileOperator || !auth.Allows(domain.OperationCreateCredential) {
		return CredentialIssuanceResult{}, &PublicError{Code: CodeForbidden, Message: "Operator authority is required."}
	}
	if err := validateIdempotencyKey(key); err != nil {
		return CredentialIssuanceResult{}, err
	}
	if err := validateCredentialCreate(input, s.clock.Now().UTC()); err != nil {
		return CredentialIssuanceResult{}, err
	}
	requestHash, err := hashRequest(input)
	if err != nil {
		return CredentialIssuanceResult{}, err
	}
	return s.issueMachineCredential(ctx, auth, "create", key, requestHash, input, nil)
}

func (s *IdentityService) RotateMachineCredential(ctx context.Context, auth domain.RuntimeAuthContext, id, key string, overlap time.Duration) (CredentialIssuanceResult, error) {
	if auth.EffectiveProfile != domain.CredentialProfileOperator || !auth.Allows(domain.OperationRotateCredential) {
		return CredentialIssuanceResult{}, &PublicError{Code: CodeForbidden, Message: "Operator authority is required."}
	}
	if err := validateIdempotencyKey(key); err != nil {
		return CredentialIssuanceResult{}, err
	}
	if overlap < 0 || overlap > maximumRotationOverlap {
		return CredentialIssuanceResult{}, invalidIdentityRequest("rotation overlap must be between zero and 24h")
	}
	requestHash, err := hashRequest(struct {
		CredentialID string `json:"credential_id"`
		OverlapMS    int64  `json:"overlap_ms"`
	}{id, overlap.Milliseconds()})
	if err != nil {
		return CredentialIssuanceResult{}, err
	}
	var replayed CredentialIssuanceResult
	err = s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		var replayErr error
		replayed, replayErr = s.replayIssuance(ctx, auth.AccountID, "rotate:"+id, key, requestHash)
		return replayErr
	})
	if err == nil {
		replayed.Replayed = true
		return replayed, nil
	}
	if !errors.Is(err, ports.ErrNotFound) {
		return CredentialIssuanceResult{}, err
	}
	var original domain.Credential
	err = s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		var err error
		original, err = s.store.GetCredentialForUpdate(ctx, id)
		if err != nil || original.AccountID != auth.AccountID || original.Kind != domain.CredentialKindMachine {
			return &PublicError{Code: CodeNotFound, Message: "The credential was not found."}
		}
		return nil
	})
	if err != nil {
		return CredentialIssuanceResult{}, err
	}
	input := CredentialCreateInput{Name: original.Name, Profile: *original.Profile, TenantConstraint: cloneString(original.TenantConstraint), SessionConstraint: cloneString(original.SessionConstraint), OperationConstraints: append([]domain.RuntimeOperation(nil), original.OperationConstraints...), ExpiresAt: cloneTime(original.ExpiresAt)}
	return s.issueMachineCredential(ctx, auth, "rotate:"+id, key, requestHash, input, &rotationSource{credential: original, overlap: overlap})
}

func (s *IdentityService) RevokeCredential(ctx context.Context, auth domain.RuntimeAuthContext, id string) (domain.Credential, error) {
	credential, err := s.store.GetCredential(ctx, id)
	if err != nil || credential.AccountID != auth.AccountID {
		return domain.Credential{}, &PublicError{Code: CodeNotFound, Message: "The credential was not found."}
	}
	allowed := auth.Allows(domain.OperationRevokeCredential) && (auth.EffectiveProfile == domain.CredentialProfileOperator || (credential.Kind == domain.CredentialKindUser && credential.ID == auth.CredentialID))
	if !allowed {
		return domain.Credential{}, &PublicError{Code: CodeForbidden, Message: "The credential cannot be revoked by this caller."}
	}
	if credential.Status == domain.CredentialStatusRevoked {
		return credential, nil
	}
	return s.store.RevokeCredential(ctx, auth.AccountID, id, s.clock.Now().UTC())
}

type rotationSource struct {
	credential domain.Credential
	overlap    time.Duration
}

func (s *IdentityService) issueMachineCredential(ctx context.Context, auth domain.RuntimeAuthContext, scope, key string, requestHash []byte, input CredentialCreateInput, rotation *rotationSource) (CredentialIssuanceResult, error) {
	var result CredentialIssuanceResult
	err := s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		replayed, err := s.replayIssuance(ctx, auth.AccountID, scope, key, requestHash)
		if err == nil {
			result = replayed
			result.Replayed = true
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		if rotation != nil {
			locked, err := s.store.GetCredentialForUpdate(ctx, rotation.credential.ID)
			if err != nil || locked.AccountID != auth.AccountID || locked.Status != domain.CredentialStatusActive {
				return &PublicError{Code: CodeNotFound, Message: "The credential was not found."}
			}
			rotation.credential = locked
		}
		secret, prefix, verifier, err := s.newCredentialSecret()
		if err != nil {
			return err
		}
		id, err := s.ids.NewID(domain.PrefixCredential)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		profile := input.Profile
		credential := domain.Credential{
			ID:                   id,
			AccountID:            auth.AccountID,
			Kind:                 domain.CredentialKindMachine,
			Name:                 strings.TrimSpace(input.Name),
			Prefix:               prefix,
			Verifier:             verifier,
			Status:               domain.CredentialStatusActive,
			Profile:              &profile,
			TenantConstraint:     cloneString(input.TenantConstraint),
			SessionConstraint:    cloneString(input.SessionConstraint),
			OperationConstraints: append([]domain.RuntimeOperation(nil), input.OperationConstraints...),
			ExpiresAt:            cloneTime(input.ExpiresAt),
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		if auth.Subject != nil {
			credential.CreatorSubjectID = &auth.Subject.ID
		}
		if auth.CredentialID != "" {
			credential.CreatorCredentialID = &auth.CredentialID
		}
		if rotation != nil {
			credential.RotatedFromID = &rotation.credential.ID
		}
		if err := s.store.CreateCredential(ctx, credential); err != nil {
			return err
		}
		if rotation != nil {
			if rotation.overlap == 0 {
				if _, err := s.store.RevokeCredential(ctx, auth.AccountID, rotation.credential.ID, now); err != nil {
					return err
				}
			} else {
				if _, err := s.store.SetCredentialRotationOverlap(ctx, auth.AccountID, rotation.credential.ID, now.Add(rotation.overlap), now); err != nil {
					return err
				}
			}
		}
		issuance, err := s.newIssuance(auth.AccountID, scope, key, requestHash, credential.ID, secret, now)
		if err != nil {
			return err
		}
		if err := s.store.CreateCredentialIssuance(ctx, issuance); err != nil {
			return err
		}
		result = CredentialIssuanceResult{Credential: credential, Secret: secret, ExpiresAt: issuance.ExpiresAt}
		return nil
	})
	return result, err
}

func (s *IdentityService) StartDeviceAuthorization(ctx context.Context, input DeviceCodeInput) (DeviceCodeResult, error) {
	if err := validateDeviceInput(input); err != nil {
		return DeviceCodeResult{}, err
	}
	deviceCode, err := randomToken(s.random, 32)
	if err != nil {
		return DeviceCodeResult{}, err
	}
	userRaw := make([]byte, 5)
	if _, err := io.ReadFull(s.random, userRaw); err != nil {
		return DeviceCodeResult{}, err
	}
	userHex := strings.ToUpper(hex.EncodeToString(userRaw))
	userCode := userHex[:5] + "-" + userHex[5:]
	id, err := s.ids.NewID(domain.PrefixDeviceAuthorization)
	if err != nil {
		return DeviceCodeResult{}, err
	}
	now := s.clock.Now().UTC()
	cap := input.RoleCap
	if cap == "" {
		cap = domain.CredentialProfileOperator
	}
	grant := domain.DeviceAuthorization{
		ID:                id,
		AccountID:         s.accountID,
		DeviceCodeHash:    hashBytes(deviceCode),
		UserCodeHash:      hashBytes(normalizeUserCode(userCode)),
		UserCodeDisplay:   userCode,
		DeviceLabel:       strings.TrimSpace(input.DeviceLabel),
		RoleCap:           cap,
		TenantConstraint:  cloneString(input.TenantConstraint),
		SessionConstraint: cloneString(input.SessionConstraint),
		Status:            domain.DeviceAuthorizationPending,
		PollInterval:      s.devicePoll,
		NextPollAt:        now,
		ExpiresAt:         now.Add(s.deviceLifetime),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.store.CreateDeviceAuthorization(ctx, grant); err != nil {
		return DeviceCodeResult{}, err
	}
	verificationURI := s.verificationBaseURL + "/auth/device"
	return DeviceCodeResult{
		DeviceCode:              deviceCode,
		UserCode:                userCode,
		VerificationURI:         verificationURI,
		VerificationURIComplete: verificationURI + "?user_code=" + userCode,
		ExpiresIn:               int(s.deviceLifetime.Seconds()),
		Interval:                int(s.devicePoll.Seconds()),
	}, nil
}

func (s *IdentityService) PollDeviceAuthorization(ctx context.Context, deviceCode string) (CredentialIssuanceResult, error) {
	var result CredentialIssuanceResult
	err := s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		grant, err := s.store.GetDeviceAuthorizationByDeviceCodeForUpdate(ctx, hashBytes(deviceCode))
		if err != nil {
			return &DeviceFlowError{Code: DeviceExpiredToken}
		}
		now := s.clock.Now().UTC()
		if !now.Before(grant.ExpiresAt) {
			return &DeviceFlowError{Code: DeviceExpiredToken}
		}
		if grant.Status == domain.DeviceAuthorizationDenied {
			return &DeviceFlowError{Code: DeviceAccessDenied}
		}
		if grant.Status == domain.DeviceAuthorizationPending {
			if now.Before(grant.NextPollAt) {
				grant.PollInterval += 5 * time.Second
				if grant.PollInterval > time.Minute {
					grant.PollInterval = time.Minute
				}
				grant.NextPollAt = now.Add(grant.PollInterval)
				grant.UpdatedAt = now
				_, _ = s.store.RecordDevicePoll(ctx, grant)
				return &DeviceFlowError{Code: DeviceSlowDown}
			}
			grant.NextPollAt = now.Add(grant.PollInterval)
			grant.UpdatedAt = now
			_, _ = s.store.RecordDevicePoll(ctx, grant)
			return &DeviceFlowError{Code: DeviceAuthorizationPending}
		}
		if grant.DeliveryExpiresAt == nil || !now.Before(*grant.DeliveryExpiresAt) {
			return &DeviceFlowError{Code: DeviceExpiredToken}
		}
		issuance, err := s.store.GetCredentialIssuance(ctx, grant.AccountID, "device:"+grant.ID, grant.ID)
		if err != nil || issuance.Ciphertext == nil {
			return &DeviceFlowError{Code: DeviceExpiredToken}
		}
		secret, err := s.decryptIssuance(issuance)
		if err != nil {
			return err
		}
		credential, err := s.store.GetCredential(ctx, issuance.CredentialID)
		if err != nil {
			return err
		}
		if _, err := s.store.ExchangeDeviceAuthorization(ctx, grant.ID, now); err != nil {
			return err
		}
		result = CredentialIssuanceResult{Credential: credential, Secret: secret, ExpiresAt: issuance.ExpiresAt, Replayed: grant.Status == domain.DeviceAuthorizationExchanged}
		return nil
	})
	return result, err
}

func (s *IdentityService) DeviceApproval(ctx context.Context, userCode string, session domain.BrowserSession) (DeviceApprovalView, error) {
	var view DeviceApprovalView
	err := s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		grant, err := s.store.GetDeviceAuthorizationByUserCodeForUpdate(ctx, hashBytes(normalizeUserCode(userCode)))
		if err != nil || grant.AccountID != session.AccountID || grant.Status != domain.DeviceAuthorizationPending || !s.clock.Now().UTC().Before(grant.ExpiresAt) {
			return &PublicError{Code: CodeNotFound, Message: "The device authorization was not found."}
		}
		operator, err := s.store.GetOperatorSubject(ctx, session.SubjectID)
		if err != nil {
			return err
		}
		membership, err := s.store.GetMembershipBySubject(ctx, session.AccountID, session.SubjectID)
		if err != nil || (membership.Role != domain.MembershipRoleOwner && membership.Role != domain.MembershipRoleOperator) {
			return &PublicError{Code: CodeForbidden, Message: "Operator authority is required."}
		}
		view = DeviceApprovalView{AccountID: grant.AccountID, Approver: operator, DeviceLabel: grant.DeviceLabel, RoleCap: grant.RoleCap, TenantConstraint: cloneString(grant.TenantConstraint), SessionConstraint: cloneString(grant.SessionConstraint), ExpiresAt: grant.ExpiresAt}
		return nil
	})
	return view, err
}

func (s *IdentityService) ConfirmDeviceAuthorization(ctx context.Context, userCode string, approve bool, session domain.BrowserSession) error {
	return s.txm.WithTransaction(ctx, func(ctx context.Context) error {
		grant, err := s.store.GetDeviceAuthorizationByUserCodeForUpdate(ctx, hashBytes(normalizeUserCode(userCode)))
		if err != nil || grant.AccountID != session.AccountID || grant.Status != domain.DeviceAuthorizationPending || !s.clock.Now().UTC().Before(grant.ExpiresAt) {
			return &PublicError{Code: CodeNotFound, Message: "The device authorization was not found."}
		}
		membership, err := s.store.GetMembershipBySubject(ctx, session.AccountID, session.SubjectID)
		if err != nil || (membership.Role != domain.MembershipRoleOwner && membership.Role != domain.MembershipRoleOperator) {
			return &PublicError{Code: CodeForbidden, Message: "Operator authority is required."}
		}
		grant, _ = s.store.IncrementDeviceConfirmationAttempts(ctx, grant.ID, s.clock.Now().UTC())
		if grant.ConfirmationAttempts > 5 {
			_, _ = s.store.DenyDeviceAuthorization(ctx, grant.ID, s.clock.Now().UTC())
			return &PublicError{Code: CodeForbidden, Message: "The device authorization can no longer be confirmed."}
		}
		if !approve {
			_, err := s.store.DenyDeviceAuthorization(ctx, grant.ID, s.clock.Now().UTC())
			return err
		}
		secret, prefix, verifier, err := s.newCredentialSecret()
		if err != nil {
			return err
		}
		credentialID, err := s.ids.NewID(domain.PrefixCredential)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		ownerID := session.SubjectID
		cap := grant.RoleCap
		credential := domain.Credential{
			ID:                credentialID,
			AccountID:         grant.AccountID,
			Kind:              domain.CredentialKindUser,
			Name:              grant.DeviceLabel,
			Prefix:            prefix,
			Verifier:          verifier,
			Status:            domain.CredentialStatusActive,
			RoleCap:           &cap,
			OwnerSubjectID:    &ownerID,
			CreatorSubjectID:  &ownerID,
			TenantConstraint:  cloneString(grant.TenantConstraint),
			SessionConstraint: cloneString(grant.SessionConstraint),
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := s.store.CreateCredential(ctx, credential); err != nil {
			return err
		}
		requestHash := hashBytes(grant.ID)
		issuance, err := s.newIssuance(grant.AccountID, "device:"+grant.ID, grant.ID, requestHash, credential.ID, secret, now)
		if err != nil {
			return err
		}
		if err := s.store.CreateCredentialIssuance(ctx, issuance); err != nil {
			return err
		}
		grant.ApprovedBySubjectID = &ownerID
		grant.CredentialID = &credentialID
		grant.DeliveryExpiresAt = &issuance.ExpiresAt
		grant.UpdatedAt = now
		_, err = s.store.ApproveDeviceAuthorization(ctx, grant)
		return err
	})
}

func (s *IdentityService) CreateBootstrapBrowserSession(ctx context.Context, secret string) (BootstrapBrowserSession, error) {
	presented := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(s.bootstrapHash[:], presented[:]) != 1 {
		return BootstrapBrowserSession{}, ports.ErrUnauthenticated
	}
	owner, err := s.BootstrapOwner(ctx)
	if err != nil {
		return BootstrapBrowserSession{}, err
	}
	token, err := randomToken(s.random, 32)
	if err != nil {
		return BootstrapBrowserSession{}, err
	}
	csrf, err := randomToken(s.random, 32)
	if err != nil {
		return BootstrapBrowserSession{}, err
	}
	id, err := s.ids.NewID(domain.PrefixBrowserSession)
	if err != nil {
		return BootstrapBrowserSession{}, err
	}
	now := s.clock.Now().UTC()
	expiresAt := now.Add(s.browserLifetime)
	if err := s.store.CreateBrowserSession(ctx, domain.BrowserSession{ID: id, AccountID: s.accountID, SubjectID: owner.ID, TokenHash: hashBytes(token), CSRFHash: hashBytes(csrf), ExpiresAt: expiresAt, CreatedAt: now}); err != nil {
		return BootstrapBrowserSession{}, err
	}
	return BootstrapBrowserSession{Token: token, CSRFToken: csrf, ExpiresAt: expiresAt}, nil
}

func (s *IdentityService) ResolveBrowserSession(ctx context.Context, token, csrf string) (domain.BrowserSession, error) {
	session, err := s.store.GetBrowserSessionByTokenHash(ctx, hashBytes(token))
	if err != nil || !s.clock.Now().UTC().Before(session.ExpiresAt) {
		return domain.BrowserSession{}, ports.ErrUnauthenticated
	}
	if csrf != "" {
		presented := hashBytes(csrf)
		if subtle.ConstantTimeCompare(session.CSRFHash, presented) != 1 {
			return domain.BrowserSession{}, ports.ErrUnauthenticated
		}
	}
	return session, nil
}

func (s *IdentityService) replayIssuance(ctx context.Context, accountID, scope, key string, requestHash []byte) (CredentialIssuanceResult, error) {
	issuance, err := s.store.GetCredentialIssuance(ctx, accountID, scope, key)
	if err != nil {
		return CredentialIssuanceResult{}, err
	}
	if subtle.ConstantTimeCompare(issuance.RequestHash, requestHash) != 1 {
		return CredentialIssuanceResult{}, &PublicError{Code: CodeIdempotencyConflict, Message: "The idempotency key was already used for a different credential request."}
	}
	now := s.clock.Now().UTC()
	if !now.Before(issuance.ExpiresAt) || len(issuance.Ciphertext) == 0 {
		_ = s.store.ClearExpiredCredentialIssuance(ctx, accountID, scope, key, now)
		return CredentialIssuanceResult{}, &PublicError{Code: CodeIdempotencyConflict, Message: "The credential was created, but its one-time delivery window has expired."}
	}
	secret, err := s.decryptIssuance(issuance)
	if err != nil {
		return CredentialIssuanceResult{}, err
	}
	credential, err := s.store.GetCredential(ctx, issuance.CredentialID)
	if err != nil {
		return CredentialIssuanceResult{}, err
	}
	return CredentialIssuanceResult{Credential: credential, Secret: secret, ExpiresAt: issuance.ExpiresAt}, nil
}

func (s *IdentityService) newIssuance(accountID, scope, key string, requestHash []byte, credentialID, secret string, now time.Time) (domain.CredentialIssuance, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return domain.CredentialIssuance{}, err
	}
	aad := []byte(accountID + "\x00" + scope + "\x00" + key + "\x00" + credentialID)
	ciphertext := s.aead.Seal(nil, nonce, []byte(secret), aad)
	return domain.CredentialIssuance{AccountID: accountID, Scope: scope, IdempotencyKey: key, RequestHash: requestHash, CredentialID: credentialID, Ciphertext: ciphertext, Nonce: nonce, ExpiresAt: now.Add(s.issuanceWindow), CreatedAt: now}, nil
}

func (s *IdentityService) decryptIssuance(issuance domain.CredentialIssuance) (string, error) {
	aad := []byte(issuance.AccountID + "\x00" + issuance.Scope + "\x00" + issuance.IdempotencyKey + "\x00" + issuance.CredentialID)
	plaintext, err := s.aead.Open(nil, issuance.Nonce, issuance.Ciphertext, aad)
	if err != nil {
		return "", fmt.Errorf("decrypt credential issuance: %w", err)
	}
	return string(plaintext), nil
}

func (s *IdentityService) newCredentialSecret() (string, string, []byte, error) {
	prefixRandom := make([]byte, 9)
	if _, err := io.ReadFull(s.random, prefixRandom); err != nil {
		return "", "", nil, err
	}
	prefix := "nvk_" + base64.RawURLEncoding.EncodeToString(prefixRandom)
	secretPart, err := randomToken(s.random, 32)
	if err != nil {
		return "", "", nil, err
	}
	secret := prefix + "." + secretPart
	return secret, prefix, tokenVerifier(secret), nil
}

func (s *IdentityService) resolveCredentialProfile(ctx context.Context, credential domain.Credential) (domain.CredentialProfile, *domain.OperatorSubject, error) {
	if credential.Kind == domain.CredentialKindMachine && credential.Profile != nil {
		return *credential.Profile, nil, nil
	}
	if credential.Kind != domain.CredentialKindUser || credential.OwnerSubjectID == nil || credential.RoleCap == nil {
		return "", nil, ports.ErrUnauthenticated
	}
	membership, err := s.store.GetMembershipBySubject(ctx, credential.AccountID, *credential.OwnerSubjectID)
	if err != nil {
		return "", nil, err
	}
	profile := domain.CredentialProfileViewer
	if (membership.Role == domain.MembershipRoleOwner || membership.Role == domain.MembershipRoleOperator) && *credential.RoleCap == domain.CredentialProfileOperator {
		profile = domain.CredentialProfileOperator
	}
	subject, err := s.store.GetOperatorSubject(ctx, *credential.OwnerSubjectID)
	if err != nil {
		return "", nil, err
	}
	return profile, &subject, nil
}

func profileOperations(profile domain.CredentialProfile) map[domain.RuntimeOperation]struct{} {
	runtimeAll := []domain.RuntimeOperation{domain.OperationCreateInvocation, domain.OperationGetInvocation, domain.OperationSubmitToolResults, domain.OperationCancelInvocation, domain.OperationListInvocations, domain.OperationGetSession, domain.OperationListSessions, domain.OperationListMessages, domain.OperationGetTranscript}
	runtimeRead := []domain.RuntimeOperation{domain.OperationGetInvocation, domain.OperationListInvocations, domain.OperationGetSession, domain.OperationListSessions, domain.OperationListMessages, domain.OperationGetTranscript}
	selected := append(runtimeAll, domain.OperationGetAccount)
	switch profile {
	case domain.CredentialProfileViewer:
		selected = append(runtimeRead, domain.OperationGetAccount)
	case domain.CredentialProfileOperator:
		selected = append(selected, domain.OperationListCredentials, domain.OperationCreateCredential, domain.OperationGetCredential, domain.OperationRotateCredential, domain.OperationRevokeCredential)
	}
	result := make(map[domain.RuntimeOperation]struct{}, len(selected))
	for _, operation := range selected {
		result[operation] = struct{}{}
	}
	return result
}

func credentialUsable(credential domain.Credential, now time.Time) bool {
	return credential.Status == domain.CredentialStatusActive && (credential.ExpiresAt == nil || now.Before(*credential.ExpiresAt)) && (credential.RotationOverlapEndsAt == nil || now.Before(*credential.RotationOverlapEndsAt))
}

func tokenLookupPrefix(token string) string {
	if prefix, _, ok := strings.Cut(token, "."); ok && strings.HasPrefix(prefix, "nvk_") {
		return prefix
	}
	hash := sha256.Sum256([]byte(token))
	return "nvk_legacy_" + hex.EncodeToString(hash[:6])
}
func tokenVerifier(token string) []byte { hash := sha256.Sum256([]byte(token)); return hash[:] }
func hashBytes(value string) []byte     { hash := sha256.Sum256([]byte(value)); return hash[:] }
func randomToken(random io.Reader, size int) (string, error) {
	raw := make([]byte, size)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func normalizeUserCode(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
}
func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func hashRequest(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	return hash[:], nil
}
func validateIdempotencyKey(key string) error {
	if strings.TrimSpace(key) == "" || len(key) > 255 {
		return invalidIdentityRequest("Idempotency-Key must contain 1 to 255 characters")
	}
	return nil
}
func invalidIdentityRequest(message string) error {
	return &PublicError{Code: CodeInvalidRequest, Message: message}
}
func validMembershipRole(role domain.MembershipRole) bool {
	return role == domain.MembershipRoleOwner || role == domain.MembershipRoleOperator || role == domain.MembershipRoleViewer
}

func validateCredentialCreate(input CredentialCreateInput, now time.Time) error {
	if strings.TrimSpace(input.Name) == "" || !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) > maximumCredentialNameRunes {
		return invalidIdentityRequest("credential name must contain 1 to 100 Unicode characters")
	}
	if input.Profile != domain.CredentialProfileRuntime && input.Profile != domain.CredentialProfileViewer && input.Profile != domain.CredentialProfileOperator {
		return invalidIdentityRequest("profile must be Runtime, Viewer, or Operator")
	}
	if err := validateConstraint(input.TenantConstraint, "tenant_ref", false); err != nil {
		return invalidIdentityRequest(err.Error())
	}
	if err := validateConstraint(input.SessionConstraint, "session_id", true); err != nil {
		return invalidIdentityRequest(err.Error())
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(now) {
		return invalidIdentityRequest("expires_at must be in the future")
	}
	base := profileOperations(input.Profile)
	seen := map[domain.RuntimeOperation]struct{}{}
	for _, operation := range input.OperationConstraints {
		if _, ok := base[operation]; !ok {
			return invalidIdentityRequest("operations may only narrow the selected profile")
		}
		if _, duplicate := seen[operation]; duplicate {
			return invalidIdentityRequest("operations must not contain duplicates")
		}
		seen[operation] = struct{}{}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateDeviceInput(input DeviceCodeInput) error {
	if strings.TrimSpace(input.DeviceLabel) == "" || !utf8.ValidString(input.DeviceLabel) || utf8.RuneCountInString(input.DeviceLabel) > maximumDeviceLabelRunes {
		return invalidIdentityRequest("device_label must contain 1 to 100 Unicode characters")
	}
	if input.RoleCap != "" && input.RoleCap != domain.CredentialProfileOperator && input.RoleCap != domain.CredentialProfileViewer {
		return invalidIdentityRequest("role_cap must be Operator or Viewer")
	}
	if err := validateConstraint(input.TenantConstraint, "tenant_ref", false); err != nil {
		return invalidIdentityRequest(err.Error())
	}
	if err := validateConstraint(input.SessionConstraint, "session_id", true); err != nil {
		return invalidIdentityRequest(err.Error())
	}
	return nil
}

func validateConstraint(value *string, name string, stableSessionID bool) error {
	if value == nil {
		return nil
	}
	if !utf8.ValidString(*value) || strings.TrimSpace(*value) == "" {
		return fmt.Errorf("%s must be valid UTF-8 and not blank", name)
	}
	if utf8.RuneCountInString(*value) > 255 {
		return fmt.Errorf("%s must be at most 255 Unicode characters", name)
	}
	if stableSessionID && !domain.ValidStableID(*value, domain.PrefixSession) {
		return fmt.Errorf("%s must be a valid Session ID", name)
	}
	return nil
}

var _ ports.RuntimeAuthenticator = (*IdentityService)(nil)
