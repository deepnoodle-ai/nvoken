package domain

import (
	"encoding/json"
	"time"
)

type InvocationMCPServerBinding struct {
	ID                string
	InvocationID      string
	AccountID         string
	TenantPartitionID string
	ServerName        string
	EncryptionKeyID   *string
	Nonce             []byte
	Ciphertext        []byte
	ExpiresAt         *time.Time
	ClearedAt         *time.Time
	CreatedAt         time.Time
}

type MCPToolAnnotations struct {
	ReadOnlyHint    *bool `json:"read_only_hint"`
	IdempotentHint  *bool `json:"idempotent_hint"`
	DestructiveHint *bool `json:"destructive_hint"`
}

type MCPProjectedTool struct {
	ServerName    string             `json:"server_name"`
	ProjectedName string             `json:"projected_name"`
	RemoteName    string             `json:"remote_name"`
	Description   string             `json:"description"`
	InputSchema   json.RawMessage    `json:"input_schema"`
	Annotations   MCPToolAnnotations `json:"annotations"`
}

type MCPToolExclusion struct {
	ServerName string `json:"server_name"`
	RemoteName string `json:"remote_name"`
	Reason     string `json:"reason"`
}

type MCPDiscoveryCatalog struct {
	Tools      []MCPProjectedTool `json:"tools"`
	Exclusions []MCPToolExclusion `json:"exclusions"`
}

type MCPServerConnection struct {
	Name             string
	URL              string
	Headers          map[string]string
	DiscoveryTimeout time.Duration
	CallTimeout      time.Duration
}

type MCPRemoteTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	ReadOnly    bool
	Idempotent  bool
	Destructive *bool
}

type MCPCallResult struct {
	Content           json.RawMessage
	StructuredContent json.RawMessage
	IsError           bool
}

type InvocationMCPDiscovery struct {
	ID                string
	InvocationID      string
	AccountID         string
	TenantPartitionID string
	Catalog           json.RawMessage
	CreatedAt         time.Time
}
