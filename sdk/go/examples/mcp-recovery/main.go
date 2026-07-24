package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lookupInput struct {
	ID string `json:"id"`
}

type lookupOutput struct {
	Call  int64  `json:"call"`
	ID    string `json:"id"`
	Value string `json:"value"`
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: go run ./examples/mcp-recovery {serve|run}")
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve()
	case "run":
		err = run(context.Background())
	default:
		err = errors.New("command must be serve or run")
	}
	if err != nil {
		log.Fatal(err)
	}
}

func serve() error {
	token := environment("MCP_TOKEN", "development-mcp-token")
	delay, err := time.ParseDuration(environment("MCP_CALL_DELAY", "0s"))
	if err != nil {
		return fmt.Errorf("parse MCP_CALL_DELAY: %w", err)
	}
	var calls atomic.Int64
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "nvoken-recovery-example",
		Version: "1.0.0",
	}, nil)
	notDestructive := false
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lookup",
		Description: "Look up a deterministic recovery fixture.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: &notDestructive,
		},
	}, func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		input lookupInput,
	) (*mcp.CallToolResult, lookupOutput, error) {
		call := calls.Add(1)
		log.Printf("scripted MCP call %d started for %q", call, input.ID)
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			log.Printf("scripted MCP call %d disconnected: %v", call, ctx.Err())
			return nil, lookupOutput{}, ctx.Err()
		case <-timer.C:
		}
		log.Printf("scripted MCP call %d completed", call)
		return nil, lookupOutput{
			Call:  call,
			ID:    input.ID,
			Value: "recovered-" + input.ID,
		}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		nil,
	)
	authenticated := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(writer, request)
	})
	address := environment("MCP_LISTEN_ADDR", "127.0.0.1:8090")
	log.Printf("scripted MCP server listening on http://%s", address)
	return http.ListenAndServe(address, authenticated)
}

func run(ctx context.Context) error {
	baseURL := requiredEnvironment("NVOKEN_BASE_URL")
	apiKey := requiredEnvironment("NVOKEN_API_KEY")
	mcpURL := requiredEnvironment("MCP_URL")
	token := environment("MCP_TOKEN", "development-mcp-token")
	client, err := nvoken.NewClient(baseURL, apiKey)
	if err != nil {
		return err
	}
	server := nvoken.MCPServer{
		Name:         "scripted",
		URL:          mcpURL,
		AllowedTools: []string{"lookup"},
		Headers: map[string]string{
			"Authorization": "Bearer " + token,
		},
	}
	catalog, err := client.ListMCPTools(ctx, server)
	if err != nil {
		return fmt.Errorf("discover scripted MCP tools: %w", err)
	}
	for _, tool := range catalog.Tools {
		log.Printf("discovered %s (%s)", tool.ProjectedName, tool.RemoteName)
	}

	invocationID := os.Getenv("NVOKEN_INVOCATION_ID")
	var handle *nvoken.InvocationHandle
	if invocationID == "" {
		maxIterations := 3
		handle, err = client.Invoke(ctx, nvoken.InvokeRequest{
			AgentKey:       "mcp-recovery-example",
			IdempotencyKey: "mcp-recovery-" + strconv.FormatInt(time.Now().UnixNano(), 10),
			Input:          "Look up fixture 42 and report its value.",
			Spec: nvoken.ExecutionSpec{
				Instructions: "Always call scripted__lookup with id 42 before answering.",
				Model: nvoken.Model{
					Provider: requiredEnvironment("NVOKEN_PROVIDER"),
					ID:       requiredEnvironment("NVOKEN_MODEL"),
				},
				Limits: &nvoken.Limits{
					MaxIterations: &maxIterations,
				},
				MCPServers: []nvoken.MCPServer{server},
			},
		})
		if err != nil {
			return fmt.Errorf("admit MCP Invocation: %w", err)
		}
		log.Printf(
			"admitted invocation_id=%s session_id=%s; export NVOKEN_INVOCATION_ID=%s to resume",
			handle.InvocationID,
			handle.SessionID,
			handle.InvocationID,
		)
	} else {
		handle = client.Invocation(invocationID)
		log.Printf("resuming invocation_id=%s", invocationID)
	}

	invocation, err := handle.Wait(ctx, nvoken.WaitOptions{Until: nvoken.WaitUntilTerminal})
	if err != nil {
		return fmt.Errorf("wait for durable settlement: %w", err)
	}
	result, err := handle.Result(ctx)
	if err != nil {
		return fmt.Errorf("read authoritative result: %w", err)
	}
	transcriptLimit := 100
	transcript, err := client.DrainTranscript(
		ctx,
		invocation.SessionID,
		nil,
		&transcriptLimit,
	)
	if err != nil {
		return fmt.Errorf("drain fixed-cut transcript: %w", err)
	}
	evidence := map[string]any{
		"invocation":        result.Invocation,
		"messages":          result.Messages,
		"output_text":       result.OutputText,
		"transcript_cursor": transcript.ResumeCursor,
		"transcript_count":  len(transcript.Messages),
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func requiredEnvironment(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}
