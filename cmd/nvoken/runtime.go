package main

import (
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
	"listMCPTools":             "mcp list-tools",
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
		Description("Admit a durable turn; text mode streams and prints its answer").
		Args("input").
		Flags(
			cli.String("agent", "a").Required().Help("Stable Agent key"),
			cli.String("idempotency-key", "i").Help("Stable admission identity; reuse it unchanged after any uncertain acknowledgement"),
			cli.String("instructions").Help("Agent instructions; cannot be combined with --spec-file"),
			cli.String("provider").Help("Model provider; required without --spec-file"),
			cli.String("model", "m").Help("Exact model ID; required without --spec-file"),
			cli.String("spec-file").Help("JSON file containing the exact public wire spec object"),
			cli.String("tenant").Help("Tenant partition"),
			cli.String("session-id").Help("Existing Session ID"),
			cli.String("session-key").Help("Caller Session key; concurrent turns for one Session are rejected"),
			cli.Int("timeout").Help("Text-mode answer timeout in seconds; zero waits indefinitely"),
		).
		Run(runInvoke)

	invocations := app.Group("invocation").Description("Inspect and control Invocations")
	invocations.Command("get").Args("invocation-id").Run(runInvocationGet)
	invocations.Command("result").
		Description("Read the composed result: Invocation, messages, and assistant text").
		Args("invocation-id").
		Run(runInvocationResult)
	invocations.Command("wait").
		Description("Wait until terminal or actionable; waiting requires a tool result or cancellation").
		Args("invocation-id").
		Flags(
			cli.Int("timeout").Help("Local wait timeout in seconds; zero waits indefinitely"),
			cli.String("until").Default("terminal").Enum("terminal", "actionable").Help("Stop condition"),
		).
		Run(runInvocationWait)
	invocations.Command("stream").
		Description("Render provisional deltas; reconnect with the durable cursor after interruption").
		Args("invocation-id").
		Run(runInvocationStream)
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
	models.Command("check").
		Description("Run a small billed probe to verify configured provider access").
		Args("selection").
		Flags(
			cli.String("agent").Default("nvoken-model-check").Help("Stable Agent key used for the probe"),
			cli.String("tenant").Help("Tenant partition whose configured credential should be checked"),
			cli.Int("timeout").Default(30).Help("Local probe timeout in seconds"),
		).
		Run(runModelCheck)

	sessions := app.Group("session").Description("Read Session state and transcript")
	sessions.Command("get").Args("session-id").Run(runSessionGet)
	sessions.Command("resolve").
		Description("Recover a Session by caller-owned host keys").
		Flags(
			cli.String("session-key").Required().Help("Caller Session key"),
			cli.String("tenant").Help("Tenant partition containing the Session"),
			cli.Bool("default-tenant").Help("Resolve only in the Account default tenant"),
			cli.String("agent-id").Help("Exact Agent ID"),
		).
		Run(runSessionResolve)
	sessions.Command("list").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("agent-id").Help("Filter by Agent ID"),
			cli.String("session-key").Help("Filter by caller Session key"),
			cli.String("tenant").Help("Filter by tenant partition"),
			cli.Bool("default-tenant").Help("Filter by the Account default tenant"),
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
		Description("Display the fixed-cut durable transcript; text mode drains and renders messages").
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

func runModelCheck(command *cli.Context) error {
	provider, modelID, found := strings.Cut(command.Arg(0), "/")
	if !found || provider == "" || modelID == "" {
		return errors.New("model selection must be provider/model")
	}
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	model, err := client.GetModel(command.Context(), nvoken.Model{
		Provider: provider,
		ID:       modelID,
	})
	if err != nil {
		return err
	}
	maxIterations := 1
	maxOutputTokens := 8
	handle, err := client.Invoke(command.Context(), nvoken.InvokeRequest{
		AgentKey:  command.String("agent"),
		TenantKey: optionalString(command.String("tenant")),
		Input:     "Reply with exactly OK.",
		Spec: nvoken.ExecutionSpec{
			Instructions: "Reply with exactly OK and no other text.",
			Model: nvoken.Model{
				Provider: provider,
				ID:       modelID,
			},
			Limits: &nvoken.Limits{
				MaxOutputTokens: &maxOutputTokens,
				MaxIterations:   &maxIterations,
			},
		},
	})
	if err != nil {
		return err
	}
	invocation, err := handle.Wait(command.Context(), nvoken.WaitOptions{
		Timeout: time.Duration(command.Int("timeout")) * time.Second,
	})
	if err != nil {
		return err
	}
	result := struct {
		Provider       string                  `json:"provider"`
		ID             string                  `json:"id"`
		Cataloged      bool                    `json:"cataloged"`
		Pricing        nvoken.ModelPricing     `json:"pricing"`
		InvocationID   string                  `json:"invocation_id"`
		Invocation     nvoken.InvocationStatus `json:"invocation_status"`
		ProviderError  *string                 `json:"provider_error"`
		ProviderAccess bool                    `json:"provider_access"`
	}{
		Provider:       provider,
		ID:             modelID,
		Cataloged:      model.Cataloged,
		Pricing:        model.Pricing,
		InvocationID:   invocation.ID,
		Invocation:     invocation.Status,
		ProviderAccess: invocation.Status == nvoken.InvocationCompleted,
	}
	if invocation.Error != nil {
		result.ProviderError = &invocation.Error.Message
	}
	if err := writeOutput(command, result, func(writer io.Writer) error {
		status := "PASS"
		if !result.ProviderAccess {
			status = "FAIL"
		}
		_, err := fmt.Fprintf(
			writer,
			"%s\t%s/%s\tcataloged=%t\tpricing=%s\tinvocation=%s\n",
			status,
			result.Provider,
			result.ID,
			result.Cataloged,
			result.Pricing.Status,
			result.InvocationID,
		)
		return err
	}); err != nil {
		return err
	}
	if result.ProviderAccess {
		return nil
	}
	if result.ProviderError != nil {
		return fmt.Errorf("model check failed: %s", *result.ProviderError)
	}
	return fmt.Errorf("model check failed with Invocation status %s", result.Invocation)
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
	provider := command.String("provider")
	model := command.String("model")
	request := nvoken.InvokeRequest{
		AgentKey:       command.String("agent"),
		IdempotencyKey: command.String("idempotency-key"),
		Input:          command.Arg(0),
		Spec: nvoken.ExecutionSpec{
			Instructions: command.String("instructions"),
			Model: nvoken.Model{
				Provider: provider,
				ID:       model,
			},
		},
	}
	if specFile := command.String("spec-file"); specFile != "" {
		if provider != "" || model != "" || command.String("instructions") != "" {
			return errors.New("--spec-file cannot be combined with --provider, --model, or --instructions")
		}
		request.SpecJSON, err = os.ReadFile(specFile)
		if err != nil {
			return fmt.Errorf("read execution spec %s: %w", specFile, err)
		}
	} else if provider == "" || model == "" {
		return errors.New("--provider and --model are required without --spec-file")
	}
	request.TenantKey = optionalString(command.String("tenant"))
	request.SessionID = optionalString(command.String("session-id"))
	request.SessionKey = optionalString(command.String("session-key"))
	handle, err := client.Invoke(command.Context(), request)
	if err != nil {
		return err
	}
	if jsonOutput(command) {
		return writeOutput(command, handle, nil)
	}
	renderedDelta := false
	if err := handle.Stream(command.Context(), func(event nvoken.StreamEvent) error {
		text, ok, err := outputTextDelta(event)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		renderedDelta = true
		_, err = fmt.Fprint(command.Stdout(), text)
		return err
	}); err != nil {
		return err
	}
	result, err := handle.WaitForResult(command.Context(), nvoken.WaitOptions{
		Timeout: time.Duration(command.Int("timeout")) * time.Second,
	})
	if err != nil {
		return err
	}
	if result.OutputText == nil || *result.OutputText == "" {
		return fmt.Errorf("Invocation %s completed without assistant text", handle.InvocationID)
	}
	if renderedDelta {
		_, err = fmt.Fprintln(command.Stdout())
		return err
	}
	_, err = fmt.Fprintln(command.Stdout(), *result.OutputText)
	return err
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
	invocation, err := handle.Wait(command.Context(), nvoken.WaitOptions{
		Until:   nvoken.WaitCondition(command.String("until")),
		Timeout: time.Duration(command.Int("timeout")) * time.Second,
	})
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
	renderedDelta := false
	return handle.Stream(command.Context(), func(event nvoken.StreamEvent) error {
		if jsonOutput(command) {
			return json.NewEncoder(command.Stdout()).Encode(map[string]any{
				"id":       event.ID,
				"type":     event.Type,
				"data":     event.Data,
				"retry_ms": event.Retry.Milliseconds(),
			})
		}
		if text, ok, err := outputTextDelta(event); err != nil {
			return err
		} else if ok {
			renderedDelta = true
			_, err = fmt.Fprint(command.Stdout(), text)
			return err
		}
		if (event.Type == "invocation.result" || event.Type == "stream.end") &&
			renderedDelta {
			renderedDelta = false
			_, err = fmt.Fprintln(command.Stdout())
			return err
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

func runSessionResolve(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	session, err := client.GetSessionByKey(
		command.Context(),
		command.String("session-key"),
		nvoken.ListSessionsOptions{
			TenantKey:     optionalString(command.String("tenant")),
			DefaultTenant: optionalBool(command.Bool("default-tenant")),
			AgentID:       optionalString(command.String("agent-id")),
		},
	)
	if err != nil {
		return err
	}
	return writeSession(command, session)
}

func runSessionList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListSessions(command.Context(), nvoken.ListSessionsOptions{
		TenantKey:     optionalString(command.String("tenant")),
		DefaultTenant: optionalBool(command.Bool("default-tenant")),
		AgentID:       optionalString(command.String("agent-id")),
		SessionKey:    optionalString(command.String("session-key")),
		Cursor:        optionalString(command.String("cursor")),
		Limit:         optionalInt(command.Int("limit")),
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
			if err := writeMessageText(writer, message); err != nil {
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
	if !jsonOutput(command) && command.String("page-token") == "" {
		drain, err := client.DrainTranscript(
			command.Context(),
			command.Arg(0),
			optionalString(command.String("cursor")),
			optionalInt(command.Int("limit")),
		)
		if err != nil {
			return err
		}
		return writeTranscriptText(
			command.Stdout(),
			drain.Messages,
			drain.InvocationChanges,
			drain.ResumeCursor,
		)
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
		return writeTranscriptText(
			writer,
			snapshot.Messages,
			snapshot.InvocationChanges,
			snapshot.ResumeCursor,
		)
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
		if text, ok, err := outputTextDelta(event); err != nil {
			return err
		} else if ok {
			_, err = fmt.Fprint(command.Stdout(), text)
			return err
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

func writeSession(command *cli.Context, session *nvoken.Session) error {
	return writeOutput(command, session, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", session.ID, session.AgentID)
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

func outputTextDelta(event nvoken.StreamEvent) (string, bool, error) {
	if event.Type != "output_text.delta" {
		return "", false, nil
	}
	var delta struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(event.Data, &delta); err != nil {
		return "", false, fmt.Errorf("decode output text delta: %w", err)
	}
	return delta.Text, true, nil
}

func writeMessageText(writer io.Writer, message nvoken.SessionMessage) error {
	parts := make([]string, 0, len(message.Content))
	for _, block := range message.Content {
		if block.Type == "text" {
			if text, ok := block.AdditionalProperties["text"].(string); ok {
				parts = append(parts, text)
				continue
			}
		}
		encoded, err := json.Marshal(block)
		if err != nil {
			return fmt.Errorf("encode message content: %w", err)
		}
		parts = append(parts, string(encoded))
	}
	_, err := fmt.Fprintf(
		writer,
		"%d\t%s\t%s\n",
		message.Sequence,
		message.Role,
		strings.Join(parts, ""),
	)
	return err
}

func writeTranscriptText(
	writer io.Writer,
	messages []nvoken.SessionMessage,
	changes []nvoken.InvocationChange,
	resumeCursor string,
) error {
	for _, message := range messages {
		if err := writeMessageText(writer, message); err != nil {
			return err
		}
	}
	for _, change := range changes {
		if _, err := fmt.Fprintf(
			writer,
			"invocation\t%s\t%s\n",
			change.InvocationID,
			change.Status,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(writer, "resume_cursor\t%s\n", resumeCursor)
	return err
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
