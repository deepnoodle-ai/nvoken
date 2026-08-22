package main

import (
	"fmt"
	"io"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerMemoryCommands(app *cli.App) {
	memories := app.Group("memory").Description("Browse and erase durable Agent memories")
	memories.Command("list").
		Description("Browse or search one Agent's durable memories").
		Flags(
			cli.String("agent-id").Required().Help("Agent identity whose memories to search"),
			cli.String("tenant-key").Help("Select one tenant-scoped partition"),
			cli.String("user-key").Help("Select one user-scoped partition"),
			cli.String("query").Help("Keyword or semantic search text"),
			cli.String("search-mode").Enum("keyword", "semantic", "hybrid").Help("Search strategy; defaults to hybrid"),
			cli.String("kind").Enum("fact", "preference", "episode", "summary").Help("Restrict to one memory kind"),
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runMemoryList)
	memories.Command("get").
		Description("Read one durable Agent memory").
		AddArg(requiredArg("memory-id", "Opaque durable-memory ID")).
		Run(runMemoryGet)
	memories.Command("delete").
		Description("Erase one durable memory and its search projection").
		AddArg(requiredArg("memory-id", "Opaque durable-memory ID")).
		Run(runMemoryDelete)
}

func runMemoryList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	var searchMode *nvoken.MemorySearchMode
	if value := command.String("search-mode"); value != "" {
		typed := nvoken.MemorySearchMode(value)
		searchMode = &typed
	}
	var kind *nvoken.MemoryKind
	if value := command.String("kind"); value != "" {
		typed := nvoken.MemoryKind(value)
		kind = &typed
	}
	list, err := client.ListMemories(command.Context(), nvoken.ListMemoriesOptions{
		AgentID:    command.String("agent-id"),
		TenantKey:  optionalString(command.String("tenant-key")),
		UserKey:    optionalString(command.String("user-key")),
		Query:      optionalString(command.String("query")),
		SearchMode: searchMode,
		Kind:       kind,
		Cursor:     optionalString(command.String("cursor")),
		Limit:      optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, list, func(writer io.Writer) error {
		for _, result := range list.Items {
			memory := result.Memory
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\tscore=%.6f\timportance=%d\tpinned=%t\n",
				memory.ID,
				memory.Kind,
				memory.Key,
				result.Score,
				memory.Importance,
				memory.Pinned,
			); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(
			writer,
			"search_coverage\tindexed=%d\ttotal=%d\tcomplete=%t\tsemantic_available=%t\n",
			list.SearchCoverage.IndexedEntries,
			list.SearchCoverage.TotalEntries,
			list.SearchCoverage.Complete,
			list.SearchCoverage.SemanticAvailable,
		); err != nil {
			return err
		}
		return writeNextCursor(writer, list.NextCursor)
	})
}

func runMemoryGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	memory, err := client.GetMemory(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, memory, func(writer io.Writer) error {
		if _, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%s\timportance=%d\tpinned=%t\n",
			memory.ID,
			memory.Kind,
			memory.Key,
			memory.Importance,
			memory.Pinned,
		); err != nil {
			return err
		}
		_, err := fmt.Fprintln(writer, memory.Content)
		return err
	})
}

func runMemoryDelete(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	memoryID := command.Arg(0)
	if err := client.DeleteMemory(command.Context(), memoryID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "deleted", "memory_id", memoryID)
}

func runAppAnonymousToken(command *cli.Context) error {
	client, err := apiClient(authFor(command), false)
	if err != nil {
		return err
	}
	response, err := client.IssueAnonymousTokenWithResponse(
		command.Context(),
		command.Arg(0),
		&generated.IssueAnonymousTokenParams{
			Origin:         command.String("origin"),
			IdempotencyKey: command.String("idempotency-key"),
		},
		generated.AnonymousTokenRequest{VisitorToken: optionalString(command.String("visitor-token"))},
	)
	if err != nil {
		return err
	}
	if response.JSON201 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	tokens := response.JSON201
	return writeOutput(command, tokens, func(writer io.Writer) error {
		if _, err := fmt.Fprintf(writer, "access_token\t%s\n", tokens.AccessToken); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "access_token_expires_in_seconds\t%d\n", tokens.AccessTokenExpiresInSeconds); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "visitor_token\t%s\n", tokens.VisitorToken); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "visitor_token_expires_at\t%s\n", tokens.VisitorTokenExpiresAt.Format("2006-01-02T15:04:05Z07:00")); err != nil {
			return err
		}
		sessionID := "-"
		if tokens.SessionID != nil {
			sessionID = *tokens.SessionID
		}
		_, err := fmt.Fprintf(writer, "session_id\t%s\n", sessionID)
		return err
	})
}
