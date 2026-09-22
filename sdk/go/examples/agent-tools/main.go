package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

func main() {
	ctx := context.Background()
	client, err := nvoken.NewClient(
		cmp.Or(os.Getenv("NVOKEN_BASE_URL"), "http://localhost:8080"),
		os.Getenv("NVOKEN_API_KEY"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// The tool contract is published with the Agent and shown to the model.
	var lookupOrder nvoken.ToolDeclaration
	if err := lookupOrder.FromHostToolDeclaration(generated.HostToolDeclaration{
		Mode:        generated.ModeHost,
		Name:        "lookup_order",
		Description: "Look up one order by ID.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"orderId": map[string]any{"type": "string"}},
			"required":             []string{"orderId"},
			"additionalProperties": false,
		},
	}); err != nil {
		log.Fatal(err)
	}
	var model nvoken.ModelInput
	if err := model.FromModelInput1(cmp.Or(os.Getenv("NVOKEN_MODEL"), "anthropic/claude-sonnet-5")); err != nil {
		log.Fatal(err)
	}

	runID := rand.Text()
	tenant := "agent-tools-" + runID
	agent, err := client.Agents().Create(ctx, nvoken.CreateAgentOptions{
		Key:     "order-support-" + runID,
		Name:    "Order support",
		OwnedBy: nvoken.TenantOwned(tenant),
		Behavior: nvoken.Behavior{
			Instructions: "Use lookup_order for order questions. Remember relevant details from the Conversation.",
			Model:        model,
			Tools:        []nvoken.ToolDeclaration{lookupOrder},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	// The handler runs in this process whenever a Turn waits on lookup_order.
	support := agent.BindTools(nvoken.Tool{
		Name: "lookup_order",
		Handler: func(_ context.Context, input any, call nvoken.TurnToolContext) (any, error) {
			orderID := input.(map[string]any)["orderId"]
			fmt.Printf("[lookup_order ran here for %v]\n", orderID)
			return map[string]any{
				"orderId":           orderID,
				"state":             "shipped",
				"estimatedDelivery": "tomorrow",
				// Stable across retries, so safe for deduplicating side effects.
				"idempotencyKey": call.ToolCallID,
			}, nil
		},
	})

	// Both Turns share one Conversation, so the second sees the first's tool result.
	chat := support.Conversation(nvoken.ConversationOptions{
		TenantKey: tenant,
		Selection: *nvoken.ContinueOrCreateConversation("order-chat-"+runID, nvoken.TenantConversation()),
	})
	for _, question := range []string{
		"Look up order-42. Say its state and estimated delivery.",
		"What was the estimated delivery? Do not call the tool again.",
	} {
		answer, err := chat.Text(ctx, question)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(answer)
	}
}
