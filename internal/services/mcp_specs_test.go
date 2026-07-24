package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestValidateMCPServerBoundaries(t *testing.T) {
	input := validServiceInput()
	input.Spec.MCPServers = make([]MCPServerSpec, MaxMCPServers)
	for index := range input.Spec.MCPServers {
		input.Spec.MCPServers[index] = MCPServerSpec{
			Name:         fmt.Sprintf("server_%d", index),
			URL:          fmt.Sprintf("https://mcp-%d.example.test/v1", index),
			Transport:    MCPTransportStreamableHTTP,
			AllowedTools: []string{"lookup", "update"},
			Headers: map[string]string{
				"Authorization": "Bearer secret",
			},
			Timeouts: &MCPTimeouts{
				DiscoverySeconds: MaxMCPDiscoverySeconds,
				CallSeconds:      MaxMCPCallSeconds,
			},
		}
	}
	if err := ValidateCreateInvocation(input); err != nil {
		t.Fatalf("maximum MCP declaration rejected: %v", err)
	}

	tests := map[string]func(*CreateInvocationInput){
		"too many servers": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers = append(candidate.Spec.MCPServers, MCPServerSpec{
				Name: "ninth",
				URL:  "https://ninth.example.test/mcp",
			})
		},
		"duplicate server": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[1].Name = candidate.Spec.MCPServers[0].Name
		},
		"reserved server": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].Name = "NvokenRemote"
		},
		"plain HTTP": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].URL = "http://mcp.example.test/v1"
		},
		"URL userinfo": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].URL = "https://secret@mcp.example.test/v1"
		},
		"URL fragment": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].URL = "https://mcp.example.test/v1#secret"
		},
		"unsupported transport": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].Transport = "sse"
		},
		"too many allowed tools": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].AllowedTools = make([]string, MaxMCPAllowedTools+1)
			for index := range candidate.Spec.MCPServers[0].AllowedTools {
				candidate.Spec.MCPServers[0].AllowedTools[index] = fmt.Sprintf("tool-%d", index)
			}
		},
		"duplicate allowed tool": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].AllowedTools = []string{"lookup", "lookup"}
		},
		"oversized headers": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].Headers = map[string]string{
				"Authorization": strings.Repeat("x", MaxMCPHeadersEncodedBytes),
			}
		},
		"invalid header value": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].Headers = map[string]string{
				"Authorization": "Bearer safe\r\nX-Leak: value",
			}
		},
		"discovery timeout": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].Timeouts = &MCPTimeouts{
				DiscoverySeconds: MaxMCPDiscoverySeconds + 1,
				CallSeconds:      DefaultMCPCallSeconds,
				discoverySet:     true,
			}
		},
		"call timeout": func(candidate *CreateInvocationInput) {
			candidate.Spec.MCPServers[0].Timeouts = &MCPTimeouts{
				DiscoverySeconds: DefaultMCPDiscoverySeconds,
				CallSeconds:      MaxMCPCallSeconds + 1,
				callSet:          true,
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := input
			candidate.Spec.MCPServers = cloneMCPServers(input.Spec.MCPServers)
			mutate(&candidate)
			if err := ValidateCreateInvocation(candidate); err == nil {
				t.Fatal("invalid MCP declaration was accepted")
			}
		})
	}

	for _, header := range []string{
		"Host",
		"Content-Length",
		"Transfer-Encoding",
		"Connection",
		"Proxy-Connection",
		"Proxy-Authorization",
		"Upgrade",
		"Trailer",
		"TE",
		"Cookie",
		"Set-Cookie",
		"Mcp-Session-Id",
	} {
		t.Run("reserved header "+header, func(t *testing.T) {
			candidate := input
			candidate.Spec.MCPServers = cloneMCPServers(input.Spec.MCPServers)
			candidate.Spec.MCPServers[0].Headers = map[string]string{header: "secret"}
			if err := ValidateCreateInvocation(candidate); err == nil {
				t.Fatal("reserved header was accepted")
			}
		})
	}
}

func TestMCPServerStrictOptionalShapes(t *testing.T) {
	for name, payload := range map[string]string{
		"unknown": `{"name":"test","url":"https://mcp.example.test","future":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			var server MCPServerSpec
			if err := json.Unmarshal([]byte(payload), &server); err == nil {
				t.Fatal("invalid server JSON decoded")
			}
		})
	}
	for name, payload := range map[string]string{
		"empty allowlist": `{"name":"test","url":"https://mcp.example.test","allowed_tools":[]}`,
		"null headers":    `{"name":"test","url":"https://mcp.example.test","headers":null}`,
		"null timeouts":   `{"name":"test","url":"https://mcp.example.test","timeouts":null}`,
		"null transport":  `{"name":"test","url":"https://mcp.example.test","transport":null}`,
		"zero timeout":    `{"name":"test","url":"https://mcp.example.test","timeouts":{"call_seconds":0}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var server MCPServerSpec
			if err := json.Unmarshal([]byte(payload), &server); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if err := validateMCPServers([]MCPServerSpec{server}); err == nil {
				t.Fatal("invalid optional shape was accepted")
			}
		})
	}
}

func TestInvocationFingerprintV8ExcludesSecretsAndResolvesDefaults(t *testing.T) {
	input := validServiceInput()
	input.Spec.MCPServers = []MCPServerSpec{
		{
			Name: "calendar",
			URL:  "https://calendar.example.test/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer first",
			},
		},
	}
	first, err := InvocationFingerprintV8(input)
	if err != nil {
		t.Fatalf("first fingerprint: %v", err)
	}
	input.Spec.MCPServers[0].Headers["Authorization"] = "Bearer second"
	second, err := InvocationFingerprintV8(input)
	if err != nil {
		t.Fatalf("second fingerprint: %v", err)
	}
	if first != second {
		t.Fatal("secret header changed fingerprint")
	}
	input.Spec.MCPServers[0].Transport = MCPTransportStreamableHTTP
	input.Spec.MCPServers[0].Timeouts = &MCPTimeouts{
		DiscoverySeconds: DefaultMCPDiscoverySeconds,
		CallSeconds:      DefaultMCPCallSeconds,
	}
	explicitDefaults, err := InvocationFingerprintV8(input)
	if err != nil {
		t.Fatalf("explicit defaults fingerprint: %v", err)
	}
	if first != explicitDefaults {
		t.Fatal("explicit defaults differed from resolved defaults")
	}
	input.Spec.MCPServers[0].URL = "https://changed.example.test/mcp"
	changed, err := InvocationFingerprintV8(input)
	if err != nil {
		t.Fatalf("changed fingerprint: %v", err)
	}
	if first == changed {
		t.Fatal("nonsecret server field did not change fingerprint")
	}
}

func TestDurableExecutionSpecExcludesMCPHeaders(t *testing.T) {
	spec := InlineExecutionSpec{
		Model: ModelSelection{
			Provider: "anthropic",
			ID:       "claude-test",
		},
		MCPServers: []MCPServerSpec{
			{
				Name: "calendar",
				URL:  "https://calendar.example.test/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer secret",
				},
			},
		},
	}
	encoded, err := json.Marshal(durableExecutionSpec(spec))
	if err != nil {
		t.Fatalf("marshal durable spec: %v", err)
	}
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "headers") {
		t.Fatalf("durable spec contains header material: %s", encoded)
	}
	for _, expected := range []string{
		`"transport":"streamable_http"`,
		`"discovery_seconds":10`,
		`"call_seconds":30`,
	} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("durable spec missing %s: %s", expected, encoded)
		}
	}
}

func cloneMCPServers(servers []MCPServerSpec) []MCPServerSpec {
	cloned := make([]MCPServerSpec, len(servers))
	for index, server := range servers {
		cloned[index] = resolvedMCPServerSpec(server)
	}
	return cloned
}
