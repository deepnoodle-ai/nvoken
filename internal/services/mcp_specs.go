package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxMCPServers              = 8
	MaxMCPServerNameBytes      = 24
	MaxMCPServerURLBytes       = 2048
	MaxMCPAllowedTools         = 32
	MaxMCPRemoteToolNameBytes  = 128
	MaxMCPHeaders              = 16
	MaxMCPHeadersEncodedBytes  = 8 << 10
	DefaultMCPDiscoverySeconds = 10
	MaxMCPDiscoverySeconds     = 30
	DefaultMCPCallSeconds      = 30
	MaxMCPCallSeconds          = 120
)

const MCPTransportStreamableHTTP = "streamable_http"

var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type MCPServerSpec struct {
	Name         string            `json:"name"`
	URL          string            `json:"url"`
	Transport    string            `json:"transport,omitempty"`
	AllowedTools []string          `json:"allowed_tools,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Timeouts     *MCPTimeouts      `json:"timeouts,omitempty"`
	transportSet bool
	allowedSet   bool
	headersSet   bool
	timeoutsSet  bool
}

func (s *MCPServerSpec) UnmarshalJSON(payload []byte) error {
	type wire MCPServerSpec
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(payload, &members); err != nil {
		return err
	}
	*s = MCPServerSpec(decoded)
	_, s.transportSet = members["transport"]
	_, s.allowedSet = members["allowed_tools"]
	_, s.headersSet = members["headers"]
	_, s.timeoutsSet = members["timeouts"]
	return nil
}

type MCPTimeouts struct {
	DiscoverySeconds int `json:"discovery_seconds,omitempty"`
	CallSeconds      int `json:"call_seconds,omitempty"`
	discoverySet     bool
	callSet          bool
}

func (t *MCPTimeouts) UnmarshalJSON(payload []byte) error {
	type wire MCPTimeouts
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(payload, &members); err != nil {
		return err
	}
	*t = MCPTimeouts(decoded)
	_, t.discoverySet = members["discovery_seconds"]
	_, t.callSet = members["call_seconds"]
	return nil
}

func validateMCPServers(servers []MCPServerSpec) error {
	if len(servers) > MaxMCPServers {
		return invalidRequest(fmt.Sprintf("spec.mcp_servers must contain at most %d servers.", MaxMCPServers))
	}
	seenServers := make(map[string]struct{}, len(servers))
	for index, server := range servers {
		field := fmt.Sprintf("spec.mcp_servers[%d]", index)
		if len(server.Name) == 0 || len(server.Name) > MaxMCPServerNameBytes ||
			!mcpServerNamePattern.MatchString(server.Name) ||
			strings.HasPrefix(strings.ToLower(server.Name), "nvoken") {
			return invalidRequest(field + ".name is invalid.")
		}
		if _, duplicate := seenServers[server.Name]; duplicate {
			return invalidRequest("spec.mcp_servers names must be unique.")
		}
		seenServers[server.Name] = struct{}{}
		if !validMCPServerURL(server.URL) {
			return invalidRequest(field + ".url is invalid.")
		}
		if server.transportSet && server.Transport == "" {
			return invalidRequest(field + ".transport must be streamable_http.")
		}
		if server.Transport != "" && server.Transport != MCPTransportStreamableHTTP {
			return invalidRequest(field + ".transport must be streamable_http.")
		}
		if (server.allowedSet || server.AllowedTools != nil) && len(server.AllowedTools) == 0 {
			return invalidRequest(field + ".allowed_tools must not be empty when supplied.")
		}
		if len(server.AllowedTools) > MaxMCPAllowedTools {
			return invalidRequest(fmt.Sprintf("%s.allowed_tools must contain at most %d names.", field, MaxMCPAllowedTools))
		}
		seenTools := make(map[string]struct{}, len(server.AllowedTools))
		for toolIndex, name := range server.AllowedTools {
			if !utf8.ValidString(name) || strings.TrimSpace(name) == "" || len(name) > MaxMCPRemoteToolNameBytes {
				return invalidRequest(fmt.Sprintf("%s.allowed_tools[%d] is invalid.", field, toolIndex))
			}
			if _, duplicate := seenTools[name]; duplicate {
				return invalidRequest(field + ".allowed_tools must contain unique names.")
			}
			seenTools[name] = struct{}{}
		}
		if server.headersSet && server.Headers == nil {
			return invalidRequest(field + ".headers must be an object when supplied.")
		}
		if err := validateMCPHeaders(field+".headers", server.Headers); err != nil {
			return err
		}
		if server.timeoutsSet && server.Timeouts == nil {
			return invalidRequest(field + ".timeouts must be an object when supplied.")
		}
		if err := validateMCPTimeouts(field+".timeouts", server.Timeouts); err != nil {
			return err
		}
	}
	return nil
}

func validMCPServerURL(value string) bool {
	if value == "" || len(value) > MaxMCPServerURLBytes || !utf8.ValidString(value) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.Fragment == "" && parsed.Opaque == ""
}

func validateMCPHeaders(field string, headers map[string]string) error {
	if len(headers) > MaxMCPHeaders {
		return invalidRequest(fmt.Sprintf("%s must contain at most %d entries.", field, MaxMCPHeaders))
	}
	if len(headers) != 0 {
		encoded, err := json.Marshal(headers)
		if err != nil || len(encoded) > MaxMCPHeadersEncodedBytes {
			return invalidRequest(fmt.Sprintf("%s must encode to at most %d bytes.", field, MaxMCPHeadersEncodedBytes))
		}
	}
	for name, value := range headers {
		if !validMCPHeaderName(name) {
			return invalidRequest(field + " contains a reserved or invalid name.")
		}
		if !utf8.ValidString(value) || strings.TrimSpace(value) == "" ||
			strings.ContainsAny(value, "\r\n\x00") {
			return invalidRequest(field + " contains an invalid value.")
		}
	}
	return nil
}

func validMCPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		alphaNumeric := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9'
		if !alphaNumeric && !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			return false
		}
	}
	switch strings.ToLower(name) {
	case "host", "content-length", "transfer-encoding", "connection",
		"proxy-connection", "proxy-authorization", "upgrade", "trailer", "te",
		"cookie", "set-cookie", "mcp-session-id":
		return false
	default:
		return true
	}
}

func validateMCPTimeouts(field string, timeouts *MCPTimeouts) error {
	if timeouts == nil {
		return nil
	}
	if (timeouts.discoverySet && timeouts.DiscoverySeconds == 0) ||
		(timeouts.DiscoverySeconds != 0 &&
			(timeouts.DiscoverySeconds < 1 || timeouts.DiscoverySeconds > MaxMCPDiscoverySeconds)) {
		return invalidRequest(fmt.Sprintf("%s.discovery_seconds must be between 1 and %d.", field, MaxMCPDiscoverySeconds))
	}
	if (timeouts.callSet && timeouts.CallSeconds == 0) ||
		(timeouts.CallSeconds != 0 &&
			(timeouts.CallSeconds < 1 || timeouts.CallSeconds > MaxMCPCallSeconds)) {
		return invalidRequest(fmt.Sprintf("%s.call_seconds must be between 1 and %d.", field, MaxMCPCallSeconds))
	}
	return nil
}

func resolvedMCPServerSpec(server MCPServerSpec) MCPServerSpec {
	resolved := server
	if resolved.Transport == "" {
		resolved.Transport = MCPTransportStreamableHTTP
	}
	if server.AllowedTools != nil {
		resolved.AllowedTools = append([]string(nil), server.AllowedTools...)
	}
	if server.Headers != nil {
		resolved.Headers = make(map[string]string, len(server.Headers))
		for name, value := range server.Headers {
			resolved.Headers[name] = value
		}
	}
	if resolved.Timeouts == nil {
		resolved.Timeouts = &MCPTimeouts{}
	} else {
		copy := *resolved.Timeouts
		resolved.Timeouts = &copy
	}
	if resolved.Timeouts.DiscoverySeconds == 0 {
		resolved.Timeouts.DiscoverySeconds = DefaultMCPDiscoverySeconds
	}
	if resolved.Timeouts.CallSeconds == 0 {
		resolved.Timeouts.CallSeconds = DefaultMCPCallSeconds
	}
	return resolved
}

func durableExecutionSpec(spec InlineExecutionSpec) InlineExecutionSpec {
	durable := spec
	if spec.MCPServers != nil {
		durable.MCPServers = make([]MCPServerSpec, len(spec.MCPServers))
		for index, server := range spec.MCPServers {
			durable.MCPServers[index] = resolvedMCPServerSpec(server)
			durable.MCPServers[index].Headers = nil
			durable.MCPServers[index].headersSet = false
		}
	}
	return durable
}
