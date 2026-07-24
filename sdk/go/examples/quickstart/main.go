package main

import (
	"context"
	"fmt"
	"log"
	"os"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
)

func main() {
	client, err := nvoken.NewClient(os.Getenv("NVOKEN_BASE_URL"), os.Getenv("NVOKEN_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	agent, err := client.Agent(nvoken.AgentOptions{
		AgentKey: "support",
		Spec: nvoken.ExecutionSpec{
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
	answer, err := agent.Text(
		context.Background(),
		"Why was I charged twice?",
		nvoken.AgentInvocationOptions{},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("agent> %s\n", answer)
}
