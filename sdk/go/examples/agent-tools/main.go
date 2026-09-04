// ABOUTME: Shows a Go host tool that nvoken invokes inside this process, at the moment nvoken decides.
// ABOUTME: Sends two Turns through one Conversation so the first Turn's tool result carries into the second.

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

// lookupOrderInput is the shape the tool contract below promises the model.
type lookupOrderInput struct {
	OrderID string `json:"orderId"`
}

type runOptions struct {
	client *nvoken.Client
	model  string
	runID  string
	out    io.Writer
}

func main() {
	apiKey := os.Getenv("NVOKEN_API_KEY")
	if apiKey == "" {
		log.Fatal("NVOKEN_API_KEY is required")
	}
	baseURL := os.Getenv("NVOKEN_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	provider := os.Getenv("NVOKEN_MODEL_PROVIDER")
	if provider == "" {
		provider = "anthropic"
	}
	model := os.Getenv("NVOKEN_MODEL")
	if model == "" {
		model = "claude-sonnet-5"
	}
	client, err := nvoken.NewClient(baseURL, apiKey)
	if err != nil {
		log.Fatal(err)
	}
	if err := run(context.Background(), runOptions{
		client: client,
		model:  provider + "/" + model,
		runID:  newRunID(),
		out:    os.Stdout,
	}); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, options runOptions) error {
	tenant := "agent-tools-" + options.runID

	// The tool contract is published with the Agent. nvoken keeps it and
	// shows it to the model; only the name below is shared with the handler.
	contract, err := lookupOrderContract()
	if err != nil {
		return err
	}
	var model nvoken.ModelInput
	if err := model.FromModelInput1(options.model); err != nil {
		return fmt.Errorf("encode model: %w", err)
	}
	created, err := options.client.Agents().Create(ctx, nvoken.CreateAgentOptions{
		Key:     "order-support-" + options.runID,
		Name:    "Order support",
		OwnedBy: nvoken.TenantOwned(tenant),
		Behavior: nvoken.Behavior{
			Instructions: "Use lookup_order for order questions. Remember relevant details from the Conversation.",
			Model:        model,
			Tools:        []nvoken.ToolDeclaration{contract},
		},
	})
	if err != nil {
		return fmt.Errorf("create Agent: %w", err)
	}

	// The handler stays in this process. Binding sends nothing to nvoken;
	// the SDK calls the handler when a Turn reports a waiting host call.
	var handlerRuns atomic.Int32
	support := created.BindTools(nvoken.Tool{
		Name: "lookup_order",
		Handler: func(_ context.Context, input any, call nvoken.TurnToolContext) (any, error) {
			handlerRuns.Add(1)
			var order lookupOrderInput
			if err := decodeToolInput(input, &order); err != nil {
				return nil, err
			}
			fmt.Fprintf(options.out, "host tool lookup_order ran in this process for ToolCall %s in Turn %s (orderId=%s)\n",
				call.ToolCallID, call.TurnID, order.OrderID)
			return map[string]any{
				"orderId":           order.OrderID,
				"state":             "shipped",
				"estimatedDelivery": "tomorrow",
				// The ToolCall ID is stable across retries and recovery, so it
				// is the right idempotency key for any side effect here.
				"idempotencyKey": call.ToolCallID,
			}, nil
		},
	})

	// One Conversation handle for both Turns. nvoken commits the first
	// Turn's tool call and result to the transcript and replays them to
	// the second Turn.
	chat := support.Conversation(nvoken.ConversationOptions{
		TenantKey: tenant,
		Selection: *nvoken.ContinueOrCreateConversation("order-chat-"+options.runID, nvoken.TenantConversation()),
	})

	first, err := chat.Text(ctx, "Look up order-42. Say its state and estimated delivery.")
	if err != nil {
		return fmt.Errorf("first Turn: %w", err)
	}
	fmt.Fprintf(options.out, "turn 1> %s\n", first)

	second, err := chat.Text(ctx, "What was the estimated delivery? Do not call the tool again.")
	if err != nil {
		return fmt.Errorf("second Turn: %w", err)
	}
	fmt.Fprintf(options.out, "turn 2> %s\n", second)

	if runs := handlerRuns.Load(); runs != 1 {
		return fmt.Errorf("lookup_order handler ran %d times, want 1: the second Turn should answer from the Conversation transcript", runs)
	}
	if !strings.Contains(strings.ToLower(second), "tomorrow") {
		return fmt.Errorf("second Turn did not carry the estimated delivery (tomorrow) forward: %q", second)
	}
	return nil
}

func lookupOrderContract() (nvoken.ToolDeclaration, error) {
	var contract nvoken.ToolDeclaration
	err := contract.FromHostToolDeclaration(generated.HostToolDeclaration{
		Mode:        generated.ModeHost,
		Name:        "lookup_order",
		Description: "Look up one order by ID.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"orderId": map[string]any{"type": "string"}},
			"required":             []string{"orderId"},
			"additionalProperties": false,
		},
	})
	if err != nil {
		return contract, fmt.Errorf("encode lookup_order contract: %w", err)
	}
	return contract, nil
}

// decodeToolInput turns the untyped arguments nvoken delivers into the
// struct this handler expects.
func decodeToolInput(input any, target any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode tool input: %w", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode tool input: %w", err)
	}
	return nil
}

func newRunID() string {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		log.Fatal(err)
	}
	return hex.EncodeToString(raw[:])
}
