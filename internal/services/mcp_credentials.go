package services

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

func (s *RuntimeService) validateMCPServerBindings(input CreateInvocationInput) error {
	if len(input.Spec.MCPServers) != 0 && s.mcpClient == nil {
		return invalidRequest("spec.mcp_servers is not configured for this installation.")
	}
	for _, server := range input.Spec.MCPServers {
		if len(server.Headers) != 0 && s.credentialCipher == nil {
			return invalidRequest("spec.mcp_servers headers are not enabled for this installation.")
		}
	}
	return nil
}

func (s *RuntimeService) invocationMCPServerBinding(
	invocation domain.Invocation,
	server MCPServerSpec,
	bindingID string,
	now time.Time,
) (domain.InvocationMCPServerBinding, error) {
	binding := domain.InvocationMCPServerBinding{
		ID:                bindingID,
		InvocationID:      invocation.ID,
		AccountID:         invocation.AccountID,
		TenantPartitionID: invocation.TenantPartitionID,
		ServerName:        server.Name,
		CreatedAt:         now,
	}
	if len(server.Headers) == 0 {
		return binding, nil
	}
	if s.credentialCipher == nil {
		return domain.InvocationMCPServerBinding{}, invalidRequest("spec.mcp_servers headers are not enabled for this installation.")
	}
	payload, err := json.Marshal(server.Headers)
	if err != nil {
		return domain.InvocationMCPServerBinding{}, invalidRequest("spec.mcp_servers headers are invalid.")
	}
	encrypted, err := s.credentialCipher.Encrypt(
		payload,
		invocationMCPServerBindingAAD(invocation.ID, server.Name, bindingID),
	)
	if err != nil {
		return domain.InvocationMCPServerBinding{}, &PublicError{
			Code:    CodeUnavailable,
			Message: "MCP credential encryption is unavailable.",
			Cause:   err,
		}
	}
	keyID := encrypted.KeyID
	expiresAt := invocation.DeadlineAt.Add(s.credentialCleanupGrace)
	binding.EncryptionKeyID = &keyID
	binding.Nonce = encrypted.Nonce
	binding.Ciphertext = encrypted.Ciphertext
	binding.ExpiresAt = &expiresAt
	return binding, nil
}

func invocationMCPServerBindingAAD(invocationID, serverName, bindingID string) []byte {
	payload, _ := json.Marshal([]string{
		"invocation_mcp_server_binding_v1",
		invocationID,
		strings.ToLower(serverName),
		bindingID,
	})
	return payload
}
