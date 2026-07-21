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
	handle, err := client.Invoke(context.Background(), nvoken.InvokeRequest{
		AgentRef:       "support",
		IdempotencyKey: "ticket-42:message-1",
		Input:          "Why was I charged twice?",
		Spec: nvoken.ExecutionSpec{
			Instructions: "Help the customer with billing questions.",
			Model: nvoken.Model{
				Provider: "anthropic",
				Name:     "claude-sonnet-5",
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	invocation, err := handle.Wait(context.Background(), nvoken.WaitOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s %s\n", invocation.ID, invocation.Status)
}
