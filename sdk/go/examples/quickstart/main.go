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
	definition, err := client.CreateAgentDefinition(ctx, nvoken.CreateAgentDefinitionInput{
		DefinitionKey:  "support",
		Name:           "Support",
		IdempotencyKey: "quickstart-support-definition",
		Definition: nvoken.AgentDefinition{
			Instructions: "Help the customer with billing questions.",
			Model: nvoken.Model{
				Provider: "anthropic",
				ID:       "claude-sonnet-5",
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	agentKey := "support"
	agents, err := client.ListAgents(ctx, nvoken.ListAgentsOptions{AgentKey: &agentKey})
	if err != nil {
		log.Fatal(err)
	}
	var instance *nvoken.AgentIdentity
	if len(agents.Items) > 0 {
		instance = &agents.Items[0]
	} else {
		instance, err = client.CreateAgent(ctx, nvoken.CreateAgentInput{
			AgentKey:          agentKey,
			Name:              "Support",
			AgentDefinitionID: definition.ID,
		})
		if err != nil {
			log.Fatal(err)
		}
	}
	agent, err := client.Agent(nvoken.AgentOptions{
		AgentID: instance.ID,
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
