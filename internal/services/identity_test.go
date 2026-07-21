package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func TestIdentityCredentialAndDeviceLifecycle(t *testing.T) {
	ctx := context.Background()
	clock := &identityTestClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	account := domain.Account{ID: "acct_019b0a12-0000-7000-8000-000000000001", CreatedAt: clock.Now()}
	store := newIdentityMemoryStore(account)
	ids := identity.NewUUIDv7Generator(clock)
	service, err := NewIdentityService(store, store, identityTestTransactions{}, clock, ids, IdentityConfig{
		AccountID: account.ID, VerificationBaseURL: "http://localhost:8080",
		DeliveryEncryptionKey: make([]byte, 32), BootstrapOwnerSecret: "bootstrap-owner-secret-0123456789",
		Random: rand.Reader,
	})
	if err != nil {
		t.Fatalf("new identity service: %v", err)
	}

	owner, err := service.BootstrapOwner(ctx)
	if err != nil {
		t.Fatalf("bootstrap Owner: %v", err)
	}
	if owner.Subject != bootstrapSubject {
		t.Fatalf("bootstrap subject = %#v", owner)
	}

	legacyToken := "0123456789abcdef0123456789abcdef"
	imported, err := service.ImportStaticRuntimeCredential(ctx, legacyToken, stringTestPointer("tenant-a"))
	if err != nil {
		t.Fatalf("import legacy credential: %v", err)
	}
	again, err := service.ImportStaticRuntimeCredential(ctx, "changed-configured-token-0123456789", stringTestPointer("tenant-b"))
	if err != nil || again.ID != imported.ID {
		t.Fatalf("repeat import = %#v, %v", again, err)
	}
	cutoverRestart, err := service.ImportStaticRuntimeCredential(ctx, "", nil)
	if err != nil || cutoverRestart.ID != imported.ID {
		t.Fatalf("post-cutover restart = %#v, %v", cutoverRestart, err)
	}
	store.setTouchError(errors.New("last-use metadata unavailable"))
	legacyAuth, err := service.Authenticate(ctx, legacyToken)
	if err != nil || legacyAuth.EffectiveProfile != domain.CredentialProfileRuntime || legacyAuth.TenantConstraint == nil || *legacyAuth.TenantConstraint != "tenant-a" {
		t.Fatalf("legacy auth = %#v, %v", legacyAuth, err)
	}
	store.setTouchError(nil)
	if _, err := service.Authenticate(ctx, legacyToken); err != nil {
		t.Fatalf("legacy auth after metadata recovery: %v", err)
	}
	touchCount := store.touchCount()
	if _, err := service.Authenticate(ctx, legacyToken); err != nil {
		t.Fatalf("repeated legacy auth: %v", err)
	}
	if store.touchCount() != touchCount {
		t.Fatalf("last-use metadata was rewritten inside %s", credentialLastUsedInterval)
	}
	if _, err := service.Authenticate(ctx, "changed-configured-token-0123456789"); !errors.Is(err, ports.ErrUnauthenticated) {
		t.Fatalf("changed config authenticated: %v", err)
	}

	challenge, err := service.StartDeviceAuthorization(ctx, DeviceCodeInput{DeviceLabel: "curtis@laptop"})
	if err != nil {
		t.Fatalf("start device authorization: %v", err)
	}
	if _, err := service.PollDeviceAuthorization(ctx, challenge.DeviceCode); !deviceFlowErrorIs(err, DeviceAuthorizationPending) {
		t.Fatalf("initial device poll error = %v", err)
	}
	if _, err := service.PollDeviceAuthorization(ctx, challenge.DeviceCode); !deviceFlowErrorIs(err, DeviceSlowDown) {
		t.Fatalf("fast device poll error = %v", err)
	}
	browser, err := service.CreateBootstrapBrowserSession(ctx, "bootstrap-owner-secret-0123456789")
	if err != nil {
		t.Fatalf("create browser session: %v", err)
	}
	browserSession, err := service.ResolveBrowserSession(ctx, browser.Token, browser.CSRFToken)
	if err != nil {
		t.Fatalf("resolve browser session: %v", err)
	}
	view, err := service.DeviceApproval(ctx, challenge.UserCode, browserSession)
	if err != nil || view.AccountID != account.ID || view.Approver.ID != owner.ID {
		t.Fatalf("approval view = %#v, %v", view, err)
	}
	if err := service.ConfirmDeviceAuthorization(ctx, challenge.UserCode, true, browserSession); err != nil {
		t.Fatalf("confirm device authorization: %v", err)
	}
	if err := service.ConfirmDeviceAuthorization(ctx, challenge.UserCode, true, browserSession); err == nil {
		t.Fatal("duplicate device confirmation succeeded")
	}
	restartedService, err := NewIdentityService(store, store, identityTestTransactions{}, clock, ids, IdentityConfig{
		AccountID: account.ID, VerificationBaseURL: "http://localhost:8080",
		DeliveryEncryptionKey: make([]byte, 32), BootstrapOwnerSecret: "bootstrap-owner-secret-0123456789",
		Random: rand.Reader,
	})
	if err != nil {
		t.Fatalf("restart identity service: %v", err)
	}
	issuedUser, err := restartedService.PollDeviceAuthorization(ctx, challenge.DeviceCode)
	if err != nil || issuedUser.Secret == "" || issuedUser.Credential.Kind != domain.CredentialKindUser {
		t.Fatalf("device token = %#v, %v", issuedUser, err)
	}
	replayedUser, err := service.PollDeviceAuthorization(ctx, challenge.DeviceCode)
	if err != nil || replayedUser.Secret != issuedUser.Secret || !replayedUser.Replayed {
		t.Fatalf("replayed device token = %#v, %v", replayedUser, err)
	}
	operatorAuth, err := service.Authenticate(ctx, issuedUser.Secret)
	if err != nil || operatorAuth.EffectiveProfile != domain.CredentialProfileOperator || operatorAuth.Subject == nil || operatorAuth.Subject.ID != owner.ID {
		t.Fatalf("user auth = %#v, %v", operatorAuth, err)
	}

	createInput := CredentialCreateInput{Name: "CI", Profile: domain.CredentialProfileRuntime, TenantConstraint: stringTestPointer("tenant-a")}
	issuedMachine, err := service.CreateMachineCredential(ctx, operatorAuth, "create-ci", createInput)
	if err != nil {
		t.Fatalf("create machine credential: %v", err)
	}
	replayedMachine, err := service.CreateMachineCredential(ctx, operatorAuth, "create-ci", createInput)
	if err != nil || replayedMachine.Secret != issuedMachine.Secret || replayedMachine.Credential.ID != issuedMachine.Credential.ID || !replayedMachine.Replayed {
		t.Fatalf("replayed machine issuance = %#v, %v", replayedMachine, err)
	}
	machineAuth, err := service.Authenticate(ctx, issuedMachine.Secret)
	if err != nil || machineAuth.TenantConstraint == nil || *machineAuth.TenantConstraint != "tenant-a" {
		t.Fatalf("machine auth = %#v, %v", machineAuth, err)
	}
	restricted, err := service.CreateMachineCredential(ctx, operatorAuth, "create-restricted", CredentialCreateInput{
		Name: "Read one Session", Profile: domain.CredentialProfileRuntime,
		SessionConstraint:    stringTestPointer("sesn_019b0a12-0000-7000-8000-000000000003"),
		OperationConstraints: []domain.RuntimeOperation{domain.OperationGetSession},
	})
	if err != nil {
		t.Fatalf("create restricted credential: %v", err)
	}
	restrictedAuth, err := service.Authenticate(ctx, restricted.Secret)
	if err != nil || !restrictedAuth.Allows(domain.OperationGetSession) || restrictedAuth.Allows(domain.OperationCreateInvocation) || !restrictedAuth.AllowsSession("sesn_019b0a12-0000-7000-8000-000000000003") || restrictedAuth.AllowsSession("sesn_019b0a12-0000-7000-8000-000000000004") {
		t.Fatalf("restricted auth = %#v, %v", restrictedAuth, err)
	}
	restrictedOperator, err := service.CreateMachineCredential(ctx, operatorAuth, "create-restricted-operator", CredentialCreateInput{
		Name:                 "Runtime-only operator",
		Profile:              domain.CredentialProfileOperator,
		OperationConstraints: []domain.RuntimeOperation{domain.OperationGetSession},
	})
	if err != nil {
		t.Fatalf("create restricted Operator credential: %v", err)
	}
	restrictedOperatorAuth, err := service.Authenticate(ctx, restrictedOperator.Secret)
	if err != nil || restrictedOperatorAuth.EffectiveProfile != domain.CredentialProfileOperator {
		t.Fatalf("restricted Operator auth = %#v, %v", restrictedOperatorAuth, err)
	}
	if _, err := service.ListCredentials(ctx, restrictedOperatorAuth); !publicErrorCodeIs(err, CodeForbidden) {
		t.Fatalf("restricted Operator listed credentials: %v", err)
	}
	if _, err := service.CurrentAccount(ctx, restrictedOperatorAuth); !publicErrorCodeIs(err, CodeForbidden) {
		t.Fatalf("restricted Operator inspected Account: %v", err)
	}

	expiresAt := clock.Now().Add(time.Minute)
	expiring, err := service.CreateMachineCredential(ctx, operatorAuth, "create-expiring", CredentialCreateInput{Name: "short-lived", Profile: domain.CredentialProfileRuntime, ExpiresAt: &expiresAt})
	if err != nil {
		t.Fatalf("create expiring credential: %v", err)
	}
	overlapSource, err := service.CreateMachineCredential(ctx, operatorAuth, "create-overlap", CredentialCreateInput{Name: "overlap", Profile: domain.CredentialProfileRuntime})
	if err != nil {
		t.Fatalf("create overlap credential: %v", err)
	}
	if _, err := service.RotateMachineCredential(ctx, operatorAuth, overlapSource.Credential.ID, "rotate-overlap", 10*time.Minute); err != nil {
		t.Fatalf("rotate with overlap: %v", err)
	}
	if _, err := service.Authenticate(ctx, overlapSource.Secret); err != nil {
		t.Fatalf("predecessor failed during overlap: %v", err)
	}
	clock.Advance(11 * time.Minute)
	if _, err := service.Authenticate(ctx, expiring.Secret); !errors.Is(err, ports.ErrUnauthenticated) {
		t.Fatalf("expired credential authentication error = %v", err)
	}
	if _, err := service.Authenticate(ctx, overlapSource.Secret); !errors.Is(err, ports.ErrUnauthenticated) {
		t.Fatalf("predecessor authenticated after overlap: %v", err)
	}
	if _, err := service.CreateMachineCredential(ctx, operatorAuth, "create-ci", createInput); !publicErrorCodeIs(err, CodeIdempotencyConflict) {
		t.Fatalf("expired issuance replay error = %v", err)
	}

	rotated, err := service.RotateMachineCredential(ctx, operatorAuth, issuedMachine.Credential.ID, "rotate-ci", 0)
	if err != nil {
		t.Fatalf("rotate machine credential: %v", err)
	}
	rotatedReplay, err := service.RotateMachineCredential(ctx, operatorAuth, issuedMachine.Credential.ID, "rotate-ci", 0)
	if err != nil || rotatedReplay.Secret != rotated.Secret || !rotatedReplay.Replayed {
		t.Fatalf("rotate replay = %#v, %v", rotatedReplay, err)
	}
	if _, err := service.Authenticate(ctx, issuedMachine.Secret); !errors.Is(err, ports.ErrUnauthenticated) {
		t.Fatalf("predecessor authenticated after immediate rotation: %v", err)
	}
	if _, err := service.Authenticate(ctx, rotated.Secret); err != nil {
		t.Fatalf("replacement did not authenticate: %v", err)
	}

	if _, err := service.ProvisionMembership(ctx, bootstrapIssuer, bootstrapSubject, domain.MembershipRoleViewer); err != nil {
		t.Fatalf("demote bootstrap membership: %v", err)
	}
	viewerAuth, err := service.Authenticate(ctx, issuedUser.Secret)
	if err != nil || viewerAuth.EffectiveProfile != domain.CredentialProfileViewer || viewerAuth.Allows(domain.OperationCreateInvocation) || !viewerAuth.Allows(domain.OperationGetSession) {
		t.Fatalf("demoted user auth = %#v, %v", viewerAuth, err)
	}
	if _, err := service.ListCredentials(ctx, viewerAuth); err == nil {
		t.Fatal("Viewer listed Account credentials")
	}
	if _, err := service.GetCredential(ctx, viewerAuth, issuedUser.Credential.ID); err != nil {
		t.Fatalf("Viewer could not inspect own user credential: %v", err)
	}
	if err := service.RemoveMembership(ctx, bootstrapIssuer, bootstrapSubject); err != nil {
		t.Fatalf("remove bootstrap membership: %v", err)
	}
	if _, err := service.Authenticate(ctx, issuedUser.Secret); !errors.Is(err, ports.ErrUnauthenticated) {
		t.Fatalf("user authenticated after membership removal: %v", err)
	}
	if _, err := service.RevokeCredential(ctx, viewerAuth, issuedUser.Credential.ID); err != nil {
		t.Fatalf("Viewer could not revoke own user credential: %v", err)
	}
	if _, err := service.Authenticate(ctx, issuedUser.Secret); !errors.Is(err, ports.ErrUnauthenticated) {
		t.Fatalf("revoked user authenticated: %v", err)
	}

	if _, err := service.ProvisionMembership(ctx, bootstrapIssuer, bootstrapSubject, domain.MembershipRoleOwner); err != nil {
		t.Fatalf("restore bootstrap membership for device denial: %v", err)
	}
	denied, err := service.StartDeviceAuthorization(ctx, DeviceCodeInput{DeviceLabel: "denied-device"})
	if err != nil {
		t.Fatalf("start denied device authorization: %v", err)
	}
	if err := service.ConfirmDeviceAuthorization(ctx, denied.UserCode, false, browserSession); err != nil {
		t.Fatalf("deny device authorization: %v", err)
	}
	if _, err := service.PollDeviceAuthorization(ctx, denied.DeviceCode); !deviceFlowErrorIs(err, DeviceAccessDenied) {
		t.Fatalf("denied device poll error = %v", err)
	}

	expired, err := service.StartDeviceAuthorization(ctx, DeviceCodeInput{DeviceLabel: "expired-device"})
	if err != nil {
		t.Fatalf("start expiring device authorization: %v", err)
	}
	clock.Advance(16 * time.Minute)
	if _, err := service.PollDeviceAuthorization(ctx, expired.DeviceCode); !deviceFlowErrorIs(err, DeviceExpiredToken) {
		t.Fatalf("expired device poll error = %v", err)
	}
}

type identityTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *identityTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *identityTestClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

type identityTestTransactions struct{}

func (identityTestTransactions) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type identityMemoryStore struct {
	ports.IdentityRepository
	ports.AccountRepository
	mu          sync.Mutex
	account     domain.Account
	subjects    map[string]domain.OperatorSubject
	memberships map[string]domain.Membership
	credentials map[string]domain.Credential
	prefixes    map[string]string
	issuances   map[string]domain.CredentialIssuance
	imports     map[string]string
	devices     map[string]domain.DeviceAuthorization
	deviceCodes map[string]string
	userCodes   map[string]string
	browsers    map[string]domain.BrowserSession
	touchErr    error
	touchCalls  int
}

func newIdentityMemoryStore(account domain.Account) *identityMemoryStore {
	return &identityMemoryStore{account: account, subjects: map[string]domain.OperatorSubject{}, memberships: map[string]domain.Membership{}, credentials: map[string]domain.Credential{}, prefixes: map[string]string{}, issuances: map[string]domain.CredentialIssuance{}, imports: map[string]string{}, devices: map[string]domain.DeviceAuthorization{}, deviceCodes: map[string]string{}, userCodes: map[string]string{}, browsers: map[string]domain.BrowserSession{}}
}

func (s *identityMemoryStore) LockInstallationBootstrap(context.Context) error { return nil }

func (s *identityMemoryStore) GetAccount(_ context.Context, id string) (domain.Account, error) {
	if id != s.account.ID {
		return domain.Account{}, ports.ErrNotFound
	}
	return s.account, nil
}
func (s *identityMemoryStore) CreateOperatorSubject(_ context.Context, value domain.OperatorSubject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subjects[value.ID] = value
	return nil
}
func (s *identityMemoryStore) GetOperatorSubject(_ context.Context, id string) (domain.OperatorSubject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.subjects[id]
	if !ok {
		return domain.OperatorSubject{}, ports.ErrNotFound
	}
	return value, nil
}
func (s *identityMemoryStore) GetOperatorSubjectByIdentity(_ context.Context, accountID, issuer, subject string) (domain.OperatorSubject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, value := range s.subjects {
		if value.AccountID == accountID && value.Issuer == issuer && value.Subject == subject {
			return value, nil
		}
	}
	return domain.OperatorSubject{}, ports.ErrNotFound
}
func (s *identityMemoryStore) UpsertMembership(_ context.Context, value domain.Membership) (domain.Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.memberships[value.SubjectID]; ok {
		value.ID = existing.ID
		value.CreatedAt = existing.CreatedAt
	}
	s.memberships[value.SubjectID] = value
	return value, nil
}
func (s *identityMemoryStore) GetMembershipBySubject(_ context.Context, accountID, subjectID string) (domain.Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.memberships[subjectID]
	if !ok || value.AccountID != accountID {
		return domain.Membership{}, ports.ErrNotFound
	}
	return value, nil
}
func (s *identityMemoryStore) DeleteMembershipBySubject(_ context.Context, accountID, subjectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if membership, ok := s.memberships[subjectID]; ok && membership.AccountID == accountID {
		delete(s.memberships, subjectID)
	}
	return nil
}
func (s *identityMemoryStore) CreateCredential(_ context.Context, value domain.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.prefixes[value.Prefix]; exists {
		return errors.New("duplicate prefix")
	}
	s.credentials[value.ID] = value
	s.prefixes[value.Prefix] = value.ID
	return nil
}
func (s *identityMemoryStore) GetCredential(_ context.Context, id string) (domain.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.credentials[id]
	if !ok {
		return domain.Credential{}, ports.ErrNotFound
	}
	return value, nil
}
func (s *identityMemoryStore) GetCredentialForUpdate(ctx context.Context, id string) (domain.Credential, error) {
	return s.GetCredential(ctx, id)
}
func (s *identityMemoryStore) GetCredentialByPrefix(_ context.Context, prefix string) (domain.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.prefixes[prefix]
	if !ok {
		return domain.Credential{}, ports.ErrNotFound
	}
	return s.credentials[id], nil
}
func (s *identityMemoryStore) ListCredentials(_ context.Context, accountID string) ([]domain.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []domain.Credential{}
	for _, value := range s.credentials {
		if value.AccountID == accountID {
			result = append(result, value)
		}
	}
	return result, nil
}
func (s *identityMemoryStore) TouchCredential(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchCalls++
	if s.touchErr != nil {
		return s.touchErr
	}
	value := s.credentials[id]
	value.LastUsedAt = &at
	value.UpdatedAt = at
	s.credentials[id] = value
	return nil
}

func (s *identityMemoryStore) setTouchError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchErr = err
}

func (s *identityMemoryStore) touchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.touchCalls
}
func (s *identityMemoryStore) RevokeCredential(_ context.Context, accountID, id string, at time.Time) (domain.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.credentials[id]
	if !ok || value.AccountID != accountID || value.Status != domain.CredentialStatusActive {
		return domain.Credential{}, ports.ErrNotFound
	}
	value.Status = domain.CredentialStatusRevoked
	value.RevokedAt = &at
	value.UpdatedAt = at
	s.credentials[id] = value
	return value, nil
}
func (s *identityMemoryStore) SetCredentialRotationOverlap(_ context.Context, accountID, id string, ends, at time.Time) (domain.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.credentials[id]
	if !ok || value.AccountID != accountID {
		return domain.Credential{}, ports.ErrNotFound
	}
	value.RotationOverlapEndsAt = &ends
	value.UpdatedAt = at
	s.credentials[id] = value
	return value, nil
}
func (s *identityMemoryStore) CreateCredentialIssuance(_ context.Context, value domain.CredentialIssuance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issuances[issuanceTestKey(value.AccountID, value.Scope, value.IdempotencyKey)] = value
	return nil
}
func (s *identityMemoryStore) GetCredentialIssuance(_ context.Context, accountID, scope, key string) (domain.CredentialIssuance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.issuances[issuanceTestKey(accountID, scope, key)]
	if !ok {
		return domain.CredentialIssuance{}, ports.ErrNotFound
	}
	return value, nil
}
func (s *identityMemoryStore) ClearExpiredCredentialIssuance(_ context.Context, accountID, scope, key string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.issuances[issuanceTestKey(accountID, scope, key)]
	value.Ciphertext = nil
	value.Nonce = nil
	s.issuances[issuanceTestKey(accountID, scope, key)] = value
	return nil
}
func (s *identityMemoryStore) GetStaticCredentialImport(_ context.Context, accountID, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.imports[accountID+"/"+key]
	if !ok {
		return "", ports.ErrNotFound
	}
	return value, nil
}
func (s *identityMemoryStore) CreateStaticCredentialImport(_ context.Context, accountID, key, credentialID string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.imports[accountID+"/"+key] = credentialID
	return nil
}
func (s *identityMemoryStore) CreateDeviceAuthorization(_ context.Context, value domain.DeviceAuthorization) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[value.ID] = value
	s.deviceCodes[hex.EncodeToString(value.DeviceCodeHash)] = value.ID
	s.userCodes[hex.EncodeToString(value.UserCodeHash)] = value.ID
	return nil
}
func (s *identityMemoryStore) GetDeviceAuthorizationByDeviceCodeForUpdate(_ context.Context, hash []byte) (domain.DeviceAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.deviceCodes[hex.EncodeToString(hash)]
	if !ok {
		return domain.DeviceAuthorization{}, ports.ErrNotFound
	}
	return s.devices[id], nil
}
func (s *identityMemoryStore) GetDeviceAuthorizationByUserCodeForUpdate(_ context.Context, hash []byte) (domain.DeviceAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.userCodes[hex.EncodeToString(hash)]
	if !ok {
		return domain.DeviceAuthorization{}, ports.ErrNotFound
	}
	return s.devices[id], nil
}
func (s *identityMemoryStore) RecordDevicePoll(_ context.Context, value domain.DeviceAuthorization) (domain.DeviceAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[value.ID] = value
	return value, nil
}
func (s *identityMemoryStore) ApproveDeviceAuthorization(_ context.Context, value domain.DeviceAuthorization) (domain.DeviceAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.devices[value.ID]
	current.Status = domain.DeviceAuthorizationApproved
	current.ApprovedBySubjectID = value.ApprovedBySubjectID
	current.CredentialID = value.CredentialID
	current.DeliveryExpiresAt = value.DeliveryExpiresAt
	current.UpdatedAt = value.UpdatedAt
	s.devices[value.ID] = current
	return current, nil
}
func (s *identityMemoryStore) DenyDeviceAuthorization(_ context.Context, id string, at time.Time) (domain.DeviceAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.devices[id]
	value.Status = domain.DeviceAuthorizationDenied
	value.UpdatedAt = at
	s.devices[id] = value
	return value, nil
}
func (s *identityMemoryStore) ExchangeDeviceAuthorization(_ context.Context, id string, at time.Time) (domain.DeviceAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.devices[id]
	value.Status = domain.DeviceAuthorizationExchanged
	value.UpdatedAt = at
	s.devices[id] = value
	return value, nil
}
func (s *identityMemoryStore) IncrementDeviceConfirmationAttempts(_ context.Context, id string, at time.Time) (domain.DeviceAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.devices[id]
	value.ConfirmationAttempts++
	value.UpdatedAt = at
	s.devices[id] = value
	return value, nil
}
func (s *identityMemoryStore) CreateBrowserSession(_ context.Context, value domain.BrowserSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.browsers[hex.EncodeToString(value.TokenHash)] = value
	return nil
}
func (s *identityMemoryStore) GetBrowserSessionByTokenHash(_ context.Context, hash []byte) (domain.BrowserSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.browsers[hex.EncodeToString(hash)]
	if !ok {
		return domain.BrowserSession{}, ports.ErrNotFound
	}
	return value, nil
}

func issuanceTestKey(accountID, scope, key string) string { return accountID + "/" + scope + "/" + key }
func stringTestPointer(value string) *string              { return &value }

func deviceFlowErrorIs(err error, code DeviceTokenError) bool {
	var flow *DeviceFlowError
	return errors.As(err, &flow) && flow.Code == code
}

func publicErrorCodeIs(err error, code ErrorCode) bool {
	var public *PublicError
	return errors.As(err, &public) && public.Code == code
}
