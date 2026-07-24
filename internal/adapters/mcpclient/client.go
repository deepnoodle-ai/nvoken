// Package mcpclient adapts remote streamable-HTTP MCP servers to nvoken's
// provider-neutral discovery and call port.
package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/httpguard"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultDNSDeadline      = 5 * time.Second
	defaultConnectDeadline  = 5 * time.Second
	defaultTLSDeadline      = 5 * time.Second
	maxProtocolResponseBody = 512 << 10
)

var (
	ErrResponseTooLarge   = errors.New("MCP response exceeded the supported size")
	ErrUnsupportedContent = errors.New("MCP result contains unsupported content")
	ErrInvalidResponse    = errors.New("MCP server returned an invalid response")
)

type Client struct {
	httpClient *http.Client
}

type Config struct {
	HTTPClient *http.Client
}

func New(cfg Config) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = httpguard.NewPublicClient(
			defaultDNSDeadline,
			defaultConnectDeadline,
			defaultTLSDeadline,
		)
	}
	return &Client{httpClient: httpClient}
}

func (c *Client) Discover(
	ctx context.Context,
	connection domain.MCPServerConnection,
) ([]domain.MCPRemoteTool, error) {
	session, err := c.connect(ctx, connection)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()

	var tools []domain.MCPRemoteTool
	params := &mcp.ListToolsParams{}
	for {
		result, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, ErrInvalidResponse
		}
		for _, tool := range result.Tools {
			if tool == nil {
				return nil, ErrInvalidResponse
			}
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return nil, ErrInvalidResponse
			}
			remote := domain.MCPRemoteTool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: schema,
			}
			if tool.Annotations != nil {
				remote.ReadOnly = tool.Annotations.ReadOnlyHint
				remote.Idempotent = tool.Annotations.IdempotentHint
				remote.Destructive = tool.Annotations.DestructiveHint
			}
			tools = append(tools, remote)
		}
		if result.NextCursor == "" {
			return tools, nil
		}
		params.Cursor = result.NextCursor
	}
}

func (c *Client) Call(
	ctx context.Context,
	connection domain.MCPServerConnection,
	toolName string,
	input json.RawMessage,
) (domain.MCPCallResult, error) {
	var arguments map[string]any
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if err := decoder.Decode(&arguments); err != nil || arguments == nil {
		return domain.MCPCallResult{}, ErrInvalidResponse
	}
	session, err := c.connect(ctx, connection)
	if err != nil {
		return domain.MCPCallResult{}, err
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	})
	if err != nil {
		return domain.MCPCallResult{}, err
	}
	if result == nil {
		return domain.MCPCallResult{}, ErrInvalidResponse
	}
	text := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		block, ok := content.(*mcp.TextContent)
		if !ok || block == nil {
			return domain.MCPCallResult{}, ErrUnsupportedContent
		}
		text = append(text, block.Text)
	}
	content, err := json.Marshal(text)
	if err != nil {
		return domain.MCPCallResult{}, ErrInvalidResponse
	}
	var structured json.RawMessage
	if result.StructuredContent != nil {
		structured, err = json.Marshal(result.StructuredContent)
		if err != nil || len(structured) == 0 || structured[0] != '{' {
			return domain.MCPCallResult{}, ErrInvalidResponse
		}
	}
	return domain.MCPCallResult{
		Content:           content,
		StructuredContent: structured,
		IsError:           result.IsError,
	}, nil
}

func (c *Client) connect(
	ctx context.Context,
	connection domain.MCPServerConnection,
) (*mcp.ClientSession, error) {
	if c == nil || c.httpClient == nil {
		return nil, fmt.Errorf("MCP client is not configured")
	}
	httpClient := *c.httpClient
	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	transport = &boundedResponseTransport{
		base: transport,
		max:  maxProtocolResponseBody,
	}
	if len(connection.Headers) != 0 {
		transport = &headerTransport{
			base:    transport,
			headers: connection.Headers,
		}
	}
	httpClient.Transport = transport
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "nvoken",
		Version: "0.1.0",
	}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             connection.URL,
		HTTPClient:           &httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	for name, value := range t.headers {
		cloned.Header.Set(name, value)
	}
	return t.base.RoundTrip(cloned)
}

type boundedResponseTransport struct {
	base http.RoundTripper
	max  int64
}

func (t *boundedResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.Body != nil {
		response.Body = &boundedReadCloser{
			reader: io.LimitReader(response.Body, t.max+1),
			closer: response.Body,
			max:    t.max,
		}
	}
	return response, nil
}

type boundedReadCloser struct {
	reader io.Reader
	closer io.Closer
	max    int64
	read   int64
}

func (r *boundedReadCloser) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	r.read += int64(count)
	if r.read > r.max {
		allowed := count - int(r.read-r.max)
		if allowed < 0 {
			allowed = 0
		}
		return allowed, ErrResponseTooLarge
	}
	return count, err
}

func (r *boundedReadCloser) Close() error {
	return r.closer.Close()
}
