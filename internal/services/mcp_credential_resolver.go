package services

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type MCPServerCredentialResolver struct {
	store  ports.MCPRepository
	cipher ports.CredentialCipher
	clock  ports.Clock
}

func NewMCPServerCredentialResolver(
	store ports.MCPRepository,
	cipher ports.CredentialCipher,
	clock ports.Clock,
) *MCPServerCredentialResolver {
	return &MCPServerCredentialResolver{
		store:  store,
		cipher: cipher,
		clock:  clock,
	}
}

func (r *MCPServerCredentialResolver) ResolveMCPServerHeaders(
	ctx context.Context,
	invocationID string,
	serverName string,
) (map[string]string, error) {
	if r == nil || r.store == nil || r.clock == nil {
		return nil, ports.ErrMCPServerCredentialUnavailable
	}
	binding, err := r.store.GetInvocationMCPServerBinding(ctx, invocationID, serverName)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, ports.ErrMCPServerCredentialUnavailable
	}
	if err != nil {
		return nil, err
	}
	if binding.InvocationID != invocationID || binding.ServerName != serverName || binding.ClearedAt != nil {
		return nil, ports.ErrMCPServerCredentialUnavailable
	}
	if binding.EncryptionKeyID == nil && len(binding.Nonce) == 0 && len(binding.Ciphertext) == 0 {
		return map[string]string{}, nil
	}
	if r.cipher == nil || binding.EncryptionKeyID == nil ||
		len(binding.Nonce) == 0 || len(binding.Ciphertext) == 0 ||
		binding.ExpiresAt == nil || !binding.ExpiresAt.After(r.clock.Now().UTC()) {
		return nil, ports.ErrMCPServerCredentialUnavailable
	}
	plaintext, err := r.cipher.Decrypt(domain.EncryptedCredential{
		KeyID:      *binding.EncryptionKeyID,
		Nonce:      binding.Nonce,
		Ciphertext: binding.Ciphertext,
	}, invocationMCPServerBindingAAD(binding.InvocationID, binding.ServerName, binding.ID))
	if err != nil {
		return nil, ports.ErrMCPServerCredentialUnavailable
	}
	var headers map[string]string
	if err := json.Unmarshal(plaintext, &headers); err != nil {
		return nil, ports.ErrMCPServerCredentialUnavailable
	}
	if headers == nil {
		headers = map[string]string{}
	}
	if err := validateMCPHeaders("headers", headers); err != nil {
		return nil, ports.ErrMCPServerCredentialUnavailable
	}
	return headers, nil
}
