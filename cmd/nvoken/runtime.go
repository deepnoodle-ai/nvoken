package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"

	"github.com/deepnoodle-ai/nvoken/internal/authstore"
)

const localBaseURL = defaultBaseURL

var version = "devel"

var operationCommands = map[string]string{
	"cancelInvocation":         "invocation cancel",
	"createInvocation":         "invoke",
	"createProviderCredential": "provider-credential create",
	"getInvocation":            "invocation get",
	"getInvocationResult":      "invocation result",
	"getModel":                 "model get",
	"getProviderCredential":    "provider-credential get",
	"getSession":               "session get",
	"getSessionTranscript":     "session transcript",
	"listInvocations":          "invocation list",
	"listModels":               "model list",
	"listProviderCredentials":  "provider-credential list",
	"listSessionMessages":      "session messages",
	"listSessions":             "session list",
	"revokeProviderCredential": "provider-credential revoke",
	"rotateProviderCredential": "provider-credential rotate",
	"streamInvocation":         "invocation stream",
	"streamSessionTranscript":  "session stream",
	"submitHostToolResults":    "tool-result submit",
}

type runtimeConfig struct {
	BaseURL string `json:"base_url"`
}

func registerRuntimeCommands(app *cli.App) {
	app.Command("invoke").
		Description("Durably admit an agent turn").
		Args("input").
		Flags(
			cli.String("agent", "a").Required().Help("Stable Agent key"),
			cli.String("idempotency-key", "i").Help("Stable admission identity; generated when omitted"),
			cli.String("instructions").Help("Agent instructions"),
			cli.String("provider").Required().Help("Model provider"),
			cli.String("model", "m").Required().Help("Exact model ID"),
			cli.String("tenant").Help("Tenant partition"),
			cli.String("session-id").Help("Existing Session ID"),
			cli.String("session-key").Help("Caller Session key"),
		).
		Run(runInvoke)

	invocations := app.Group("invocation").Description("Inspect and control Invocations")
	invocations.Command("get").Args("invocation-id").Run(runInvocationGet)
	invocations.Command("result").
		Description("Read the composed result: Invocation, messages, and assistant text").
		Args("invocation-id").
		Run(runInvocationResult)
	invocations.Command("wait").
		Args("invocation-id").
		Flags(cli.Int("timeout").Help("Local wait timeout in seconds; zero waits indefinitely")).
		Run(runInvocationWait)
	invocations.Command("stream").Args("invocation-id").Run(runInvocationStream)
	invocations.Command("cancel").Args("invocation-id").Run(runInvocationCancel)
	invocations.Command("list").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("session-id").Help("Filter by Session ID"),
			cli.String("agent-id").Help("Filter by Agent ID"),
		).
		Run(runInvocationList)

	models := app.Group("model").Description("Discover and inspect models")
	models.Command("list").
		Flags(
			cli.String("provider").Enum("anthropic", "openai").Help("Limit results to one provider"),
			cli.Bool("include-deprecated").Help("Include deprecated catalog entries"),
		).
		Run(runModelList)
	models.Command("get").
		Flags(
			cli.String("provider").Required().Enum("anthropic", "openai").Help("Model provider"),
			cli.String("model", "m").Required().Help("Exact model ID"),
		).
		Run(runModelGet)
	models.Command("pricing").
		Description("Inspect the standard price evidence for an exact model").
		Flags(
			cli.String("provider").Required().Enum("anthropic", "openai").Help("Model provider"),
			cli.String("model", "m").Required().Help("Exact model ID"),
		).
		Run(runModelPricing)

	sessions := app.Group("session").Description("Read Session state and transcript")
	sessions.Command("get").Args("session-id").Run(runSessionGet)
	sessions.Command("list").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("agent-id").Help("Filter by Agent ID"),
		).
		Run(runSessionList)
	sessions.Command("messages").
		Args("session-id").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runSessionMessages)
	sessions.Command("transcript").
		Args("session-id").
		Flags(
			cli.String("cursor").Help("Durable transcript cursor"),
			cli.String("page-token").Help("Fixed-cut page token"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runSessionTranscript)
	sessions.Command("stream").Args("session-id").Run(runSessionStream)

	tools := app.Group("tool-result").Description("Submit durable host ToolCall results")
	tools.Command("submit").
		Args("invocation-id", "content").
		Flags(
			cli.String("tool-call-id").Required().Help("Durable ToolCall identity"),
			cli.Bool("error").Help("Mark the result as an error"),
		).
		Run(runToolResultSubmit)
}

func runModelList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	options := nvoken.ListModelsOptions{
		IncludeDeprecated: optionalBool(command.Bool("include-deprecated")),
	}
	if provider := command.String("provider"); provider != "" {
		value := nvoken.ModelProvider(provider)
		options.Provider = &value
	}
	models, err := client.ListModels(command.Context(), options)
	if err != nil {
		return err
	}
	return writeOutput(command, models, func(writer io.Writer) error {
		for _, model := range models.Items {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\n",
				model.Provider,
				model.ID,
				model.Pricing.Status,
				modelLabel(model),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func runModelGet(command *cli.Context) error {
	model, err := selectedModel(command)
	if err != nil {
		return err
	}
	return writeOutput(command, model, func(writer io.Writer) error {
		_, err := fmt.Fprintf(
			writer,
			"%s\t%s\tcataloged=%t\t%s\n",
			model.Provider,
			model.ID,
			model.Cataloged,
			model.Pricing.Status,
		)
		return err
	})
}

func runModelPricing(command *cli.Context) error {
	model, err := selectedModel(command)
	if err != nil {
		return err
	}
	output := struct {
		Provider string              `json:"provider"`
		ID       string              `json:"id"`
		Pricing  nvoken.ModelPricing `json:"pricing"`
	}{
		Provider: string(model.Provider),
		ID:       model.ID,
		Pricing:  model.Pricing,
	}
	return writeOutput(command, output, func(writer io.Writer) error {
		_, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\n",
			model.Provider,
			model.ID,
			model.Pricing.Status,
			model.Pricing.PricingVersion,
		)
		return err
	})
}

func selectedModel(command *cli.Context) (*nvoken.ModelDescriptor, error) {
	client, err := runtimeClient(command)
	if err != nil {
		return nil, err
	}
	return client.GetModel(command.Context(), nvoken.Model{
		Provider: command.String("provider"),
		ID:       command.String("model"),
	})
}

func modelLabel(model nvoken.ModelDescriptor) string {
	label := ""
	if model.DisplayName != nil {
		label = *model.DisplayName
	}
	if model.Recommended != nil && *model.Recommended {
		if label != "" {
			label += " "
		}
		label += "(recommended)"
	}
	return label
}

func runInvoke(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	request := nvoken.InvokeRequest{
		AgentKey:       command.String("agent"),
		IdempotencyKey: command.String("idempotency-key"),
		Input:          command.Arg(0),
		Spec: nvoken.ExecutionSpec{
			Instructions: command.String("instructions"),
			Model: nvoken.Model{
				Provider: command.String("provider"),
				ID:       command.String("model"),
			},
		},
	}
	request.TenantKey = optionalString(command.String("tenant"))
	request.SessionID = optionalString(command.String("session-id"))
	request.SessionKey = optionalString(command.String("session-key"))
	handle, err := client.Invoke(command.Context(), request)
	if err != nil {
		return err
	}
	return writeOutput(command, handle, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", handle.InvocationID, handle.Status, handle.SessionID)
		return err
	})
}

func runInvocationGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	invocation, err := client.GetInvocation(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeInvocation(command, invocation)
}

func runInvocationResult(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	result, err := client.GetInvocationResult(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, result, func(writer io.Writer) error {
		invocation := result.Invocation
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", invocation.ID, invocation.Status, invocation.SessionID); err != nil {
			return err
		}
		if result.OutputText != nil {
			_, err := fmt.Fprintln(writer, *result.OutputText)
			return err
		}
		return nil
	})
}

func runInvocationWait(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	handle := client.Invocation(command.Arg(0))
	ctx := command.Context()
	if seconds := command.Int("timeout"); seconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
		defer cancel()
	}
	invocation, err := handle.Wait(ctx, nvoken.WaitOptions{})
	if err != nil {
		return err
	}
	return writeInvocation(command, invocation)
}

func runInvocationStream(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	handle := client.Invocation(command.Arg(0))
	return handle.Stream(command.Context(), func(event nvoken.StreamEvent) error {
		if jsonOutput(command) {
			return json.NewEncoder(command.Stdout()).Encode(map[string]any{
				"id":       event.ID,
				"type":     event.Type,
				"data":     event.Data,
				"retry_ms": event.Retry.Milliseconds(),
			})
		}
		_, err := fmt.Fprintf(command.Stdout(), "%s\t%s\n", event.Type, event.ID)
		return err
	})
}

func runInvocationCancel(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	invocation, err := client.CancelInvocation(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeInvocation(command, invocation)
}

func runInvocationList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListInvocations(command.Context(), nvoken.ListInvocationsOptions{
		SessionID: optionalString(command.String("session-id")),
		AgentID:   optionalString(command.String("agent-id")),
		Cursor:    optionalString(command.String("cursor")),
		Limit:     optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, invocation := range page.Items {
			if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", invocation.ID, invocation.Status, invocation.SessionID); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, page.NextCursor)
	})
}

func runSessionGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	session, err := client.GetSession(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, session, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", session.ID, session.AgentID)
		return err
	})
}

func runSessionList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListSessions(command.Context(), nvoken.ListSessionsOptions{
		AgentID: optionalString(command.String("agent-id")),
		Cursor:  optionalString(command.String("cursor")),
		Limit:   optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, session := range page.Items {
			if _, err := fmt.Fprintf(writer, "%s\t%s\n", session.ID, session.AgentID); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, page.NextCursor)
	})
}

func runSessionMessages(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListSessionMessages(command.Context(), command.Arg(0), nvoken.MessageListOptions{
		Cursor: optionalString(command.String("cursor")),
		Limit:  optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, message := range page.Items {
			if _, err := fmt.Fprintf(writer, "%d\t%s\t%s\n", message.Sequence, message.Role, message.ID); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, page.NextCursor)
	})
}

func runSessionTranscript(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	snapshot, err := client.GetTranscript(command.Context(), command.Arg(0), nvoken.TranscriptOptions{
		Cursor:    optionalString(command.String("cursor")),
		PageToken: optionalString(command.String("page-token")),
		Limit:     optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, snapshot, func(writer io.Writer) error {
		_, err := fmt.Fprintf(
			writer,
			"messages=%d\tchanges=%d\tcursor=%s\n",
			len(snapshot.Messages),
			len(snapshot.InvocationChanges),
			snapshot.ResumeCursor,
		)
		return err
	})
}

func runSessionStream(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	return client.StreamSession(command.Context(), command.Arg(0), func(event nvoken.StreamEvent, snapshot nvoken.ReducedSnapshot) error {
		if jsonOutput(command) {
			return json.NewEncoder(command.Stdout()).Encode(map[string]any{
				"event": map[string]any{
					"id":       event.ID,
					"type":     event.Type,
					"data":     event.Data,
					"retry_ms": event.Retry.Milliseconds(),
				},
				"snapshot": snapshot,
			})
		}
		_, err := fmt.Fprintf(command.Stdout(), "%s\t%s\n", event.Type, snapshot.ResumeCursor)
		return err
	})
}

func runToolResultSubmit(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	var content any
	if err := json.Unmarshal([]byte(command.Arg(1)), &content); err != nil {
		return fmt.Errorf("parse result content as JSON: %w", err)
	}
	result, err := client.SubmitToolResults(command.Context(), command.Arg(0), []nvoken.ToolResult{{
		ToolCallID: command.String("tool-call-id"),
		Content:    content,
		IsError:    command.Bool("error"),
	}})
	if err != nil {
		return err
	}
	return writeOutput(command, result, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", result.InvocationID, result.Status)
		return err
	})
}

func runtimeClient(command *cli.Context) (*nvoken.Client, error) {
	auth := authFor(command)
	if auth.BaseURLErr != nil {
		return nil, auth.BaseURLErr
	}
	if auth.APIKey == "" {
		if auth.Err != nil && !errors.Is(auth.Err, authstore.ErrNoDefaultProfile) {
			return nil, auth.Err
		}
		return nil, errors.New("not authenticated; run `nvoken auth login`, pass --api-key, or set NVOKEN_API_KEY")
	}
	return nvoken.NewClient(auth.BaseURL, auth.APIKey)
}

func resolveBaseURL(explicit string, configPath string) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return strings.TrimRight(explicit, "/"), nil
	}
	if environment := strings.TrimSpace(os.Getenv("NVOKEN_BASE_URL")); environment != "" {
		return strings.TrimRight(environment, "/"), nil
	}
	path, err := resolveConfigPath(configPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var config runtimeConfig
		if err := json.Unmarshal(data, &config); err != nil {
			return "", fmt.Errorf("decode nvoken config %s: %w", path, err)
		}
		if baseURL := strings.TrimSpace(config.BaseURL); baseURL != "" {
			return strings.TrimRight(baseURL, "/"), nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read nvoken config %s: %w", path, err)
	}
	return localBaseURL, nil
}

func resolveConfigPath(explicit string) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit, nil
	}
	if environment := strings.TrimSpace(os.Getenv("NVOKEN_CONFIG")); environment != "" {
		return environment, nil
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(directory, "nvoken", "config.json"), nil
}

func writeInvocation(command *cli.Context, invocation *nvoken.Invocation) error {
	return writeOutput(command, invocation, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", invocation.ID, invocation.Status, invocation.SessionID)
		return err
	})
}

func writeOutput(command *cli.Context, value any, text func(io.Writer) error) error {
	if jsonOutput(command) {
		encoder := json.NewEncoder(command.Stdout())
		encoder.SetEscapeHTML(false)
		return encoder.Encode(value)
	}
	return text(command.Stdout())
}

func writeNextCursor(writer io.Writer, cursor *string) error {
	if cursor == nil || *cursor == "" {
		return nil
	}
	_, err := fmt.Fprintf(writer, "next_cursor\t%s\n", *cursor)
	return err
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalInt(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func optionalBool(value bool) *bool {
	if !value {
		return nil
	}
	return &value
}
