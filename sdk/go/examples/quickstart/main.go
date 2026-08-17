package main

import (
	"context"
	"fmt"
	"log"
	"os"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
)

func main() {
	ctx := context.Background()
	client, err := nvoken.NewClient(os.Getenv("NVOKEN_BASE_URL"), os.Getenv("NVOKEN_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	definition, err := client.CreateAgentDefinition(ctx, nvoken.AgentDefinition{
		DefinitionKey: "support",
		Name:          "Support",
		Instructions:  "Help the customer with billing questions.",
		Model: nvoken.Model{
			Provider: "anthropic",
			ID:       "claude-sonnet-5",
		},
	}, nvoken.CreateAgentDefinitionOptions{})
	if err != nil {
		log.Fatal(err)
	}
	// Declared from its keys. The Agent creates its record on first use, so
	// running this twice resolves onto the same one.
	agent, err := client.Agent(nvoken.AgentOptions{
		AgentKey:      "support",
		DefinitionKey: definition.DefinitionKey,
	})
	if err != nil {
		log.Fatal(err)
	}
	answer, err := agent.Text(
		ctx,
		"Why was I charged twice?",
		nvoken.AgentInvocationOptions{},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("agent> %s\n", answer)
}
