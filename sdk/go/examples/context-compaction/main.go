package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
)

func main() {
	ctx := context.Background()
	client, err := nvoken.NewClient(
		os.Getenv("NVOKEN_BASE_URL"),
		os.Getenv("NVOKEN_API_KEY"),
	)
	if err != nil {
		log.Fatal(err)
	}
	agent, err := client.Agent(nvoken.AgentOptions{
		AgentKey: "context-compaction-example",
	})
	if err != nil {
		log.Fatal(err)
	}
	sessionKey := "context-compaction-example"
	conversation, err := agent.BindSession(
		nvoken.SessionBinding{SessionKey: sessionKey},
		nvoken.SessionOptions{
			Compaction: &nvoken.ContextCompaction{
				TriggerTokens: nvoken.ContextCompactionAt(4096),
			},
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	first, err := conversation.Run(
		ctx,
		strings.Repeat("Remember this durable context. ", 800),
		nvoken.AgentInvocationOptions{},
	)
	if err != nil {
		log.Fatal(err)
	}
	if first.Handle.SessionID == nil {
		log.Fatal("conversation turn returned no Session ID")
	}
	before, err := client.ListSessionMessages(
		ctx,
		*first.Handle.SessionID,
		nvoken.MessageListOptions{},
	)
	if err != nil {
		log.Fatal(err)
	}
	second, err := conversation.Run(
		ctx,
		"What context did I ask you to remember?",
		nvoken.AgentInvocationOptions{},
	)
	if err != nil {
		log.Fatal(err)
	}
	after, err := client.ListSessionMessages(
		ctx,
		*first.Handle.SessionID,
		nvoken.MessageListOptions{},
	)
	if err != nil {
		log.Fatal(err)
	}
	for index, message := range before.Items {
		if !reflect.DeepEqual(message.Content, after.Items[index].Content) {
			log.Fatalf("canonical message %d changed after compaction", index)
		}
	}
	fmt.Printf(
		"Session %s kept %d canonical messages; second Invocation %s completed.\n",
		*first.Handle.SessionID,
		len(after.Items),
		second.Handle.InvocationID,
	)
}
