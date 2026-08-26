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
	ctx := context.Background()
	agent, err := client.Agent(ctx, "support")
	if err != nil {
		log.Fatal(err)
	}
	answer, err := agent.Text(ctx, "Where is order 42?", nvoken.TurnOptions{
		TenantKey: "acme",
		UserKey:   "user-123",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(answer)
}
