package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres/sqlc"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func (s *Store) CreateOperatorSubject(ctx context.Context, subject domain.OperatorSubject) error {
	return s.q(ctx).CreateOperatorSubject(ctx, postgresdb.CreateOperatorSubjectParams{
		ID:        subject.ID,
		AccountID: subject.AccountID,
		Issuer:    subject.Issuer,
		Subject:   subject.Subject,
		CreatedAt: subject.CreatedAt,
	})
}

func (s *Store) GetOperatorSubject(ctx context.Context, id string) (domain.OperatorSubject, error) {
	row, err := s.q(ctx).GetOperatorSubject(ctx, id)
	if err != nil {
		return domain.OperatorSubject{}, normalizeIdentityNotFound(err)
	}
	return operatorSubjectFromRow(row), nil
}

func (s *Store) GetOperatorSubjectByIdentity(ctx context.Context, accountID, issuer, subject string) (domain.OperatorSubject, error) {
	row, err := s.q(ctx).GetOperatorSubjectByIdentity(ctx, postgresdb.GetOperatorSubjectByIdentityParams{
		AccountID: accountID,
		Issuer:    issuer,
		Subject:   subject,
	})
	if err != nil {
		return domain.OperatorSubject{}, normalizeIdentityNotFound(err)
	}
	return operatorSubjectFromRow(row), nil
}

func (s *Store) UpsertMembership(ctx context.Context, membership domain.Membership) (domain.Membership, error) {
	row, err := s.q(ctx).UpsertMembership(ctx, postgresdb.UpsertMembershipParams{
		ID:        membership.ID,
		AccountID: membership.AccountID,
		SubjectID: membership.SubjectID,
		Role:      string(membership.Role),
		CreatedAt: membership.CreatedAt,
		UpdatedAt: membership.UpdatedAt,
	})
	if err != nil {
		return domain.Membership{}, err
	}
	return membershipFromRow(row), nil
}

func (s *Store) GetMembershipBySubject(ctx context.Context, accountID, subjectID string) (domain.Membership, error) {
	row, err := s.q(ctx).GetMembershipBySubject(ctx, postgresdb.GetMembershipBySubjectParams{AccountID: accountID, SubjectID: subjectID})
	if err != nil {
		return domain.Membership{}, normalizeIdentityNotFound(err)
	}
	return membershipFromRow(row), nil
}

func (s *Store) DeleteMembershipBySubject(ctx context.Context, accountID, subjectID string) error {
	_, err := s.q(ctx).DeleteMembershipBySubject(ctx, postgresdb.DeleteMembershipBySubjectParams{AccountID: accountID, SubjectID: subjectID})
	return err
}

func (s *Store) CreateCredential(ctx context.Context, credential domain.Credential) error {
	operations := make([]string, len(credential.OperationConstraints))
	for i, operation := range credential.OperationConstraints {
		operations[i] = string(operation)
	}
	return s.q(ctx).CreateAPICredential(ctx, postgresdb.CreateAPICredentialParams{
		ID:                    credential.ID,
		AccountID:             credential.AccountID,
		Kind:                  string(credential.Kind),
		Name:                  credential.Name,
		Prefix:                credential.Prefix,
		Verifier:              credential.Verifier,
		Status:                string(credential.Status),
		OperationConstraints:  operations,
		CreatedAt:             credential.CreatedAt,
		UpdatedAt:             credential.UpdatedAt,
		Profile:               profileString(credential.Profile),
		RoleCap:               profileString(credential.RoleCap),
		OwnerSubjectID:        credential.OwnerSubjectID,
		CreatorSubjectID:      credential.CreatorSubjectID,
		CreatorCredentialID:   credential.CreatorCredentialID,
		TenantConstraint:      credential.TenantConstraint,
		SessionConstraint:     credential.SessionConstraint,
		ExpiresAt:             credential.ExpiresAt,
		RotatedFromID:         credential.RotatedFromID,
		RotationOverlapEndsAt: credential.RotationOverlapEndsAt,
		RevokedAt:             credential.RevokedAt,
		LastUsedAt:            credential.LastUsedAt,
	})
}

func (s *Store) GetCredential(ctx context.Context, id string) (domain.Credential, error) {
	row, err := s.q(ctx).GetAPICredential(ctx, id)
	if err != nil {
		return domain.Credential{}, normalizeIdentityNotFound(err)
	}
	return credentialFromRow(row), nil
}

func (s *Store) GetCredentialForUpdate(ctx context.Context, id string) (domain.Credential, error) {
	row, err := s.q(ctx).GetAPICredentialForUpdate(ctx, id)
	if err != nil {
		return domain.Credential{}, normalizeIdentityNotFound(err)
	}
	return credentialFromRow(row), nil
}

func (s *Store) GetCredentialByPrefix(ctx context.Context, prefix string) (domain.Credential, error) {
	row, err := s.q(ctx).GetAPICredentialByPrefix(ctx, prefix)
	if err != nil {
		return domain.Credential{}, normalizeIdentityNotFound(err)
	}
	return credentialFromRow(row), nil
}

func (s *Store) ListCredentials(ctx context.Context, accountID string) ([]domain.Credential, error) {
	rows, err := s.q(ctx).ListAPICredentials(ctx, accountID)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Credential, len(rows))
	for i, row := range rows {
		items[i] = credentialFromRow(row)
	}
	return items, nil
}

func (s *Store) TouchCredential(ctx context.Context, id string, usedAt time.Time) error {
	return s.q(ctx).TouchAPICredential(ctx, postgresdb.TouchAPICredentialParams{ID: id, UsedAt: usedAt})
}

func (s *Store) RevokeCredential(ctx context.Context, accountID, id string, at time.Time) (domain.Credential, error) {
	row, err := s.q(ctx).RevokeAPICredential(ctx, postgresdb.RevokeAPICredentialParams{ID: id, AccountID: accountID, RevokedAt: &at})
	if err != nil {
		return domain.Credential{}, normalizeIdentityNotFound(err)
	}
	return credentialFromRow(row), nil
}

func (s *Store) SetCredentialRotationOverlap(ctx context.Context, accountID, id string, overlapEndsAt, updatedAt time.Time) (domain.Credential, error) {
	row, err := s.q(ctx).SetCredentialRotationOverlap(ctx, postgresdb.SetCredentialRotationOverlapParams{
		ID:            id,
		AccountID:     accountID,
		OverlapEndsAt: &overlapEndsAt,
		UpdatedAt:     updatedAt,
	})
	if err != nil {
		return domain.Credential{}, normalizeIdentityNotFound(err)
	}
	return credentialFromRow(row), nil
}

func (s *Store) CreateCredentialIssuance(ctx context.Context, issuance domain.CredentialIssuance) error {
	return s.q(ctx).CreateCredentialIssuance(ctx, postgresdb.CreateCredentialIssuanceParams{
		AccountID:      issuance.AccountID,
		Scope:          issuance.Scope,
		IdempotencyKey: issuance.IdempotencyKey,
		RequestHash:    issuance.RequestHash,
		CredentialID:   issuance.CredentialID,
		Ciphertext:     issuance.Ciphertext,
		Nonce:          issuance.Nonce,
		ExpiresAt:      issuance.ExpiresAt,
		CreatedAt:      issuance.CreatedAt,
	})
}

func (s *Store) GetCredentialIssuance(ctx context.Context, accountID, scope, key string) (domain.CredentialIssuance, error) {
	row, err := s.q(ctx).GetCredentialIssuance(ctx, postgresdb.GetCredentialIssuanceParams{AccountID: accountID, Scope: scope, IdempotencyKey: key})
	if err != nil {
		return domain.CredentialIssuance{}, normalizeIdentityNotFound(err)
	}
	return domain.CredentialIssuance{AccountID: row.AccountID, Scope: row.Scope, IdempotencyKey: row.IdempotencyKey, RequestHash: row.RequestHash, CredentialID: row.CredentialID, Ciphertext: row.Ciphertext, Nonce: row.Nonce, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt}, nil
}

func (s *Store) ClearExpiredCredentialIssuance(ctx context.Context, accountID, scope, key string, observedAt time.Time) error {
	return s.q(ctx).ClearExpiredCredentialIssuance(ctx, postgresdb.ClearExpiredCredentialIssuanceParams{AccountID: accountID, Scope: scope, IdempotencyKey: key, ObservedAt: observedAt})
}

func (s *Store) GetStaticCredentialImport(ctx context.Context, accountID, key string) (string, error) {
	row, err := s.q(ctx).GetStaticCredentialImport(ctx, postgresdb.GetStaticCredentialImportParams{AccountID: accountID, ImportKey: key})
	if err != nil {
		return "", normalizeIdentityNotFound(err)
	}
	return row.CredentialID, nil
}

func (s *Store) CreateStaticCredentialImport(ctx context.Context, accountID, key, credentialID string, importedAt time.Time) error {
	return s.q(ctx).CreateStaticCredentialImport(ctx, postgresdb.CreateStaticCredentialImportParams{AccountID: accountID, ImportKey: key, CredentialID: credentialID, ImportedAt: importedAt})
}

func (s *Store) CreateDeviceAuthorization(ctx context.Context, grant domain.DeviceAuthorization) error {
	return s.q(ctx).CreateDeviceAuthorization(ctx, postgresdb.CreateDeviceAuthorizationParams{
		ID:                   grant.ID,
		AccountID:            grant.AccountID,
		DeviceCodeHash:       grant.DeviceCodeHash,
		UserCodeHash:         grant.UserCodeHash,
		UserCodeDisplay:      grant.UserCodeDisplay,
		DeviceLabel:          grant.DeviceLabel,
		RoleCap:              string(grant.RoleCap),
		Status:               string(grant.Status),
		PollIntervalSeconds:  int32(grant.PollInterval.Seconds()),
		NextPollAt:           grant.NextPollAt,
		ConfirmationAttempts: int32(grant.ConfirmationAttempts),
		ExpiresAt:            grant.ExpiresAt,
		CreatedAt:            grant.CreatedAt,
		UpdatedAt:            grant.UpdatedAt,
		TenantConstraint:     grant.TenantConstraint,
		SessionConstraint:    grant.SessionConstraint,
		ApprovedBySubjectID:  grant.ApprovedBySubjectID,
		CredentialID:         grant.CredentialID,
		DeliveryExpiresAt:    grant.DeliveryExpiresAt,
	})
}

func (s *Store) GetDeviceAuthorizationByDeviceCodeForUpdate(ctx context.Context, hash []byte) (domain.DeviceAuthorization, error) {
	row, err := s.q(ctx).GetDeviceAuthorizationByDeviceCodeForUpdate(ctx, hash)
	if err != nil {
		return domain.DeviceAuthorization{}, normalizeIdentityNotFound(err)
	}
	return deviceAuthorizationFromRow(row), nil
}

func (s *Store) GetDeviceAuthorizationByUserCodeForUpdate(ctx context.Context, hash []byte) (domain.DeviceAuthorization, error) {
	row, err := s.q(ctx).GetDeviceAuthorizationByUserCodeForUpdate(ctx, hash)
	if err != nil {
		return domain.DeviceAuthorization{}, normalizeIdentityNotFound(err)
	}
	return deviceAuthorizationFromRow(row), nil
}

func (s *Store) RecordDevicePoll(ctx context.Context, grant domain.DeviceAuthorization) (domain.DeviceAuthorization, error) {
	row, err := s.q(ctx).RecordDevicePoll(ctx, postgresdb.RecordDevicePollParams{ID: grant.ID, PollIntervalSeconds: int32(grant.PollInterval.Seconds()), NextPollAt: grant.NextPollAt, UpdatedAt: grant.UpdatedAt})
	if err != nil {
		return domain.DeviceAuthorization{}, normalizeIdentityNotFound(err)
	}
	return deviceAuthorizationFromRow(row), nil
}

func (s *Store) ApproveDeviceAuthorization(ctx context.Context, grant domain.DeviceAuthorization) (domain.DeviceAuthorization, error) {
	row, err := s.q(ctx).ApproveDeviceAuthorization(ctx, postgresdb.ApproveDeviceAuthorizationParams{ID: grant.ID, ApprovedBySubjectID: grant.ApprovedBySubjectID, CredentialID: grant.CredentialID, DeliveryExpiresAt: grant.DeliveryExpiresAt, UpdatedAt: grant.UpdatedAt})
	if err != nil {
		return domain.DeviceAuthorization{}, normalizeIdentityNotFound(err)
	}
	return deviceAuthorizationFromRow(row), nil
}

func (s *Store) DenyDeviceAuthorization(ctx context.Context, id string, at time.Time) (domain.DeviceAuthorization, error) {
	row, err := s.q(ctx).DenyDeviceAuthorization(ctx, postgresdb.DenyDeviceAuthorizationParams{ID: id, UpdatedAt: at})
	if err != nil {
		return domain.DeviceAuthorization{}, normalizeIdentityNotFound(err)
	}
	return deviceAuthorizationFromRow(row), nil
}

func (s *Store) ExchangeDeviceAuthorization(ctx context.Context, id string, at time.Time) (domain.DeviceAuthorization, error) {
	row, err := s.q(ctx).ExchangeDeviceAuthorization(ctx, postgresdb.ExchangeDeviceAuthorizationParams{ID: id, UpdatedAt: at})
	if err != nil {
		return domain.DeviceAuthorization{}, normalizeIdentityNotFound(err)
	}
	return deviceAuthorizationFromRow(row), nil
}

func (s *Store) IncrementDeviceConfirmationAttempts(ctx context.Context, id string, at time.Time) (domain.DeviceAuthorization, error) {
	row, err := s.q(ctx).IncrementDeviceConfirmationAttempts(ctx, postgresdb.IncrementDeviceConfirmationAttemptsParams{ID: id, UpdatedAt: at})
	if err != nil {
		return domain.DeviceAuthorization{}, normalizeIdentityNotFound(err)
	}
	return deviceAuthorizationFromRow(row), nil
}

func (s *Store) CreateBrowserSession(ctx context.Context, session domain.BrowserSession) error {
	return s.q(ctx).CreateBrowserSession(ctx, postgresdb.CreateBrowserSessionParams{ID: session.ID, AccountID: session.AccountID, SubjectID: session.SubjectID, TokenHash: session.TokenHash, CsrfHash: session.CSRFHash, ExpiresAt: session.ExpiresAt, CreatedAt: session.CreatedAt})
}

func (s *Store) GetBrowserSessionByTokenHash(ctx context.Context, hash []byte) (domain.BrowserSession, error) {
	row, err := s.q(ctx).GetBrowserSessionByTokenHash(ctx, hash)
	if err != nil {
		return domain.BrowserSession{}, normalizeIdentityNotFound(err)
	}
	return domain.BrowserSession{ID: row.ID, AccountID: row.AccountID, SubjectID: row.SubjectID, TokenHash: row.TokenHash, CSRFHash: row.CsrfHash, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt}, nil
}

func (s *Store) DeleteBrowserSession(ctx context.Context, id string) error {
	_, err := s.q(ctx).DeleteBrowserSession(ctx, id)
	return err
}

func operatorSubjectFromRow(row postgresdb.OperatorSubject) domain.OperatorSubject {
	return domain.OperatorSubject{ID: row.ID, AccountID: row.AccountID, Issuer: row.Issuer, Subject: row.Subject, CreatedAt: row.CreatedAt}
}
func membershipFromRow(row postgresdb.AccountMembership) domain.Membership {
	return domain.Membership{ID: row.ID, AccountID: row.AccountID, SubjectID: row.SubjectID, Role: domain.MembershipRole(row.Role), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
func profileString(value *domain.CredentialProfile) *string {
	if value == nil {
		return nil
	}
	result := string(*value)
	return &result
}

func credentialFromRow(row postgresdb.ApiCredential) domain.Credential {
	operations := make([]domain.RuntimeOperation, len(row.OperationConstraints))
	for i, operation := range row.OperationConstraints {
		operations[i] = domain.RuntimeOperation(operation)
	}
	return domain.Credential{
		ID:                    row.ID,
		AccountID:             row.AccountID,
		Kind:                  domain.CredentialKind(row.Kind),
		Name:                  row.Name,
		Prefix:                row.Prefix,
		Verifier:              row.Verifier,
		Status:                domain.CredentialStatus(row.Status),
		Profile:               credentialProfile(row.Profile),
		RoleCap:               credentialProfile(row.RoleCap),
		OwnerSubjectID:        row.OwnerSubjectID,
		CreatorSubjectID:      row.CreatorSubjectID,
		CreatorCredentialID:   row.CreatorCredentialID,
		TenantConstraint:      row.TenantConstraint,
		SessionConstraint:     row.SessionConstraint,
		OperationConstraints:  operations,
		ExpiresAt:             row.ExpiresAt,
		RotatedFromID:         row.RotatedFromID,
		RotationOverlapEndsAt: row.RotationOverlapEndsAt,
		RevokedAt:             row.RevokedAt,
		LastUsedAt:            row.LastUsedAt,
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
	}
}

func credentialProfile(value *string) *domain.CredentialProfile {
	if value == nil {
		return nil
	}
	result := domain.CredentialProfile(*value)
	return &result
}

func deviceAuthorizationFromRow(row postgresdb.DeviceAuthorization) domain.DeviceAuthorization {
	return domain.DeviceAuthorization{
		ID:                   row.ID,
		AccountID:            row.AccountID,
		DeviceCodeHash:       row.DeviceCodeHash,
		UserCodeHash:         row.UserCodeHash,
		UserCodeDisplay:      row.UserCodeDisplay,
		DeviceLabel:          row.DeviceLabel,
		RoleCap:              domain.CredentialProfile(row.RoleCap),
		TenantConstraint:     row.TenantConstraint,
		SessionConstraint:    row.SessionConstraint,
		Status:               domain.DeviceAuthorizationStatus(row.Status),
		PollInterval:         time.Duration(row.PollIntervalSeconds) * time.Second,
		NextPollAt:           row.NextPollAt,
		ConfirmationAttempts: int(row.ConfirmationAttempts),
		ApprovedBySubjectID:  row.ApprovedBySubjectID,
		CredentialID:         row.CredentialID,
		ExpiresAt:            row.ExpiresAt,
		DeliveryExpiresAt:    row.DeliveryExpiresAt,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}
}

func normalizeIdentityNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrNotFound
	}
	return err
}

var _ ports.IdentityRepository = (*Store)(nil)
