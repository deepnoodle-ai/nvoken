// Package auth implements installation-owned Runtime authentication adapters.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const minimumRuntimeTokenBytes = 32

type StaticConfig struct {
	Token            string
	AccountID        string
	TenantConstraint *string
}

// StaticAuthenticator is the narrow single-Account self-hosted adapter. The
// configured secret remains installation state and is never persisted.
type StaticAuthenticator struct {
	tokenHash        [sha256.Size]byte
	accountID        string
	tenantConstraint *string
}

func NewStaticAuthenticator(cfg StaticConfig) (*StaticAuthenticator, error) {
	if len(cfg.Token) < minimumRuntimeTokenBytes {
		return nil, fmt.Errorf("runtime API key must be at least %d bytes", minimumRuntimeTokenBytes)
	}
	if !domain.ValidStableID(cfg.AccountID, domain.PrefixAccount) {
		return nil, fmt.Errorf("runtime Account ID is invalid")
	}
	if cfg.TenantConstraint != nil {
		if !utf8.ValidString(*cfg.TenantConstraint) || strings.TrimSpace(*cfg.TenantConstraint) == "" {
			return nil, fmt.Errorf("runtime tenant constraint must be valid UTF-8 and not blank")
		}
		if utf8.RuneCountInString(*cfg.TenantConstraint) > 255 {
			return nil, fmt.Errorf("runtime tenant constraint must be at most 255 Unicode characters")
		}
	}
	return &StaticAuthenticator{
		tokenHash:        sha256.Sum256([]byte(cfg.Token)),
		accountID:        cfg.AccountID,
		tenantConstraint: cloneStringPointer(cfg.TenantConstraint),
	}, nil
}

func (a *StaticAuthenticator) Authenticate(_ context.Context, token string) (domain.RuntimeAuthContext, error) {
	if a == nil {
		return domain.RuntimeAuthContext{}, ports.ErrUnauthenticated
	}
	presented := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(a.tokenHash[:], presented[:]) != 1 {
		return domain.RuntimeAuthContext{}, ports.ErrUnauthenticated
	}
	return domain.RuntimeAuthContext{
		AccountID:        a.accountID,
		TenantConstraint: cloneStringPointer(a.tenantConstraint),
		Operations: map[domain.RuntimeOperation]struct{}{
			domain.OperationCreateInvocation: {},
			domain.OperationGetInvocation:    {},
			domain.OperationListInvocations:  {},
			domain.OperationGetSession:       {},
			domain.OperationListSessions:     {},
			domain.OperationListMessages:     {},
			domain.OperationGetTranscript:    {},
		},
	}, nil
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

var _ ports.RuntimeAuthenticator = (*StaticAuthenticator)(nil)
