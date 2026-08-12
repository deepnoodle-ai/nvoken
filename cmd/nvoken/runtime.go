package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"

	"github.com/deepnoodle-ai/nvoken/internal/authstore"
)

const localBaseURL = defaultBaseURL

// probeMaxOutputTokens is the output budget `model check` gives its probe.
//
// It was 8, on the reasoning that a probe asking for "OK" needs nothing more.
// That is wrong for a reasoning model: the budget covers reasoning tokens too,
// and providers reject a request that cannot fit them. The probe then reported
// FAIL for providers that were configured correctly and working — the exact
// answer a credential check must never get wrong. Keep this well clear of any
// provider's floor; a probe costs a fraction of a cent either way, and a false
// negative costs an investigation.
const probeMaxOutputTokens = 2048

var version = "devel"

var operationCommands = map[string]string{
	"cancelInvocation":        "invocation cancel",
	"interruptInvocation":     "invocation interrupt",
	"resumeInvocation":        "invocation resume",
	"createNudge":             "invocation nudge",
	"listNudges":              "invocation nudges",
	"listToolCalls":           "invocation tool-calls",
	"cancelNudge":             "invocation cancel-nudge",
	"createCredential":        "credentials create",
	"createInvocation":        "invoke",
	"createAgentDefinition":   "agent-definition create",
	"listAgentDefinitions":    "agent-definition list",
	"getAgentDefinition":      "agent-definition get",
	"updateAgentDefinition":   "agent-definition update",
	"archiveAgentDefinition":  "agent-definition archive",
	"restoreAgentDefinition":  "agent-definition restore",
	"createAppClientKey":      "client-key create",
	"listAppClientKeys":       "client-key list",
	"revokeAppClientKey":      "client-key revoke",
	"createSession":           "session create",
	"deleteTenant":            "tenant delete",
	"forkSession":             "session fork",
	"createProviderKey":       "provider-key create",
	"allocateCredits":         "credits allocate",
	"listCreditAccounts":      "credits accounts",
	"listCreditAllocations":   "credits allocations",
	"getCredential":           "credentials get",
	"getCurrentIdentity":      "auth status",
	"getAgent":                "agent get",
	"getApp":                  "app get",
	"listApps":                "app list",
	"registerApp":             "app register",
	"updateApp":               "app update",
	"archiveApp":              "app archive",
	"restoreApp":              "app restore",
	"getInvocation":           "invocation get",
	"getInvocationResult":     "invocation result",
	"getInvocationTimeline":   "invocation timeline",
	"getMemory":               "memory get",
	"getTrace":                "trace get",
	"getModel":                "model get",
	"getOrg":                  "org get",
	"archiveOrg":              "org archive",
	"restoreOrg":              "org restore",
	"getProviderKey":          "provider-key get",
	"getUsageBreakdown":       "usage breakdown",
	"getUsageTimeseries":      "usage timeseries",
	"getProviderKeyUsage":     "provider-key usage",
	"getSession":              "session get",
	"deleteSession":           "session delete",
	"updateSession":           "session set-metadata",
	"getSessionTranscript":    "session transcript",
	"listAgents":              "agent list",
	"listCredentials":         "credentials list",
	"listInvocations":         "invocation list",
	"listAdmissions":          "admission list",
	"listInvocationLogs":      "invocation logs",
	"listInvocationTraces":    "invocation traces",
	"listMCPTools":            "mcp list-tools",
	"listMemories":            "memory list",
	"listModels":              "model list",
	"listOrgs":                "org list",
	"listProviderKeys":        "provider-key list",
	"listSessionCompactions":  "session compactions",
	"listSessionMessages":     "session messages",
	"listSessions":            "session list",
	"listTenants":             "tenant list",
	"listUsageRecords":        "usage records",
	"registerOrg":             "org register",
	"revokeProviderKey":       "provider-key revoke",
	"revokeCredential":        "credentials revoke",
	"rotateCredential":        "credentials rotate",
	"rotateProviderKey":       "provider-key rotate",
	"streamInvocation":        "invocation stream",
	"streamSessionTranscript": "session stream",
	"submitHostToolResults":   "tool-result submit",
	"summarizeAdmissions":     "admission summary",
	"deleteMemory":            "memory delete",
	"issueAnonymousToken":     "app anonymous-token",
	"updateOrg":               "org update",
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
			cli.String("agent-definition-id").Help("App-owned Agent Definition resource to run instead of inline fields"),
			cli.String("instructions").Help("Inline Agent instructions"),
			cli.String("provider").Help("Inline model provider; required without --agent-definition-id"),
			cli.String("model", "m").Help("Inline exact model ID; required without --agent-definition-id"),
			cli.String("tenant").Help("Tenant partition"),
			cli.String("user").Help("End-user label recorded on this Invocation and its messages; filtering only"),
			cli.String("session-id").Help("Existing Session ID"),
			cli.String("session-key").Help("Caller Session key; one turn may be active at a time"),
			cli.String("if-active").Default("reject").Enum("reject", "supersede", "interrupt").Help("Reject active work, atomically replace it, or stop it gracefully and keep what it produced"),
			cli.String("webhook-url").Help("HTTPS endpoint for signed Invocation webhooks"),
			cli.Strings("webhook-event").Help("Restrict webhooks to invocation.waiting, invocation.paused, or invocation.ended; repeatable, default all"),
			cli.Strings("context").Help(`Record an application state snapshot as name=content, such as customer="plan: pro"; repeatable`),
			cli.Strings("context-operator").Help("Record a snapshot as higher-authority application state; same name=content form, repeatable"),
			cli.Strings("image").Help("Attach a local image file; repeatable"),
			cli.Strings("document").Help("Attach a local PDF file; repeatable"),
			cli.Strings("image-url").Help("Attach a public HTTPS image URL; repeatable"),
			cli.Strings("document-url").Help("Attach a public HTTPS PDF URL; repeatable"),
			cli.Int("timeout").Help("Text-mode answer timeout in seconds; zero waits indefinitely"),
		).
		Run(runInvoke)

	agentDefinitions := app.Group("agent-definition").
		Description("Manage App-owned Agent Definitions")
	agentDefinitions.Command("create").
		Description("Create one Agent Definition resource without starting a turn").
		Flags(
			cli.String("file", "f").Help("JSON Agent Definition to create; - reads stdin"),
			cli.String("instructions").Help("Agent instructions; ignored with --file"),
			cli.String("provider").Help("Model provider; required without --file"),
			cli.String("model", "m").Help("Exact model ID; required without --file"),
			cli.String("idempotency-key").Required().Help("Stable create request identity"),
		).
		Run(runCreateAgentDefinition)
	agentDefinitions.Command("get").Args("agent-definition-id").Run(runGetAgentDefinition)
	agentDefinitions.Command("list").
		Flags(
			cli.Bool("include-archived").Help("Include archived Agent Definitions"),
			cli.String("cursor").Help("Continue a previous page"),
			cli.Int("limit").Help("Maximum items in this page"),
		).
		Run(runListAgentDefinitions)
	agentDefinitions.Command("update").
		Description("Replace one Agent Definition at an expected revision").
		Args("agent-definition-id").
		Flags(
			cli.String("file", "f").Help("Replacement JSON Agent Definition; - reads stdin"),
			cli.String("instructions").Help("Agent instructions; ignored with --file"),
			cli.String("provider").Help("Model provider; required without --file"),
			cli.String("model", "m").Help("Exact model ID; required without --file"),
			cli.Int("revision").Required().Help("Current revision from GET"),
		).
		Run(runUpdateAgentDefinition)
	agentDefinitions.Command("archive").Args("agent-definition-id").Run(runArchiveAgentDefinition)
	agentDefinitions.Command("restore").Args("agent-definition-id").Run(runRestoreAgentDefinition)

	apps := app.Group("app").Description("Register and read host applications")
	apps.Command("register").
		Description("Register one app; requires a credential not associated with an app").
		Args("name").
		Flags(
			cli.String("external-ref").Help("Opaque owner reference grounding console issuer tokens"),
			cli.String("display-name").Help("Human-facing label; name stays the unique handle"),
			cli.String("org-id").Help("Owning Org; Org-scoped callers may omit this to use their own"),
			cli.Int("callback-timeout").Help("Callback HTTP reply deadline in seconds, 1 to 60; default 10"),
		).
		Run(runAppRegister)
	apps.Command("get").Args("app-id").Run(runAppGet)
	apps.Command("list").
		Description("List visible apps; an app-bound credential sees only its own").
		Flags(
			cli.String("external-ref").Help("Return only apps with exactly this external_ref"),
			cli.String("status").Enum("active", "archived", "all").Help("Filter by archive status; defaults to active"),
		).
		Run(runAppList)
	apps.Command("update").
		Description("Update an app's display name or transfer its owning Org").
		Args("app-id").
		Flags(
			cli.String("display-name").Help("Replacement human-facing label"),
			cli.String("org-id").Help("Replacement owning Org; installation administrators only"),
			cli.Int("callback-timeout").Help("Replacement callback HTTP reply deadline in seconds, 1 to 60"),
		).
		Run(runAppUpdate)
	apps.Command("archive").Args("app-id").Run(runAppArchive)
	apps.Command("restore").Args("app-id").Run(runAppRestore)
	apps.Command("anonymous-token").
		Description("Mint public browser access without machine authentication").
		Args("app-id").
		Flags(
			cli.String("origin").Required().Help("Exact browser origin configured on the App"),
			cli.String("visitor-token").Help("Previously returned visitor token; omit on a first visit"),
		).
		Run(runAppAnonymousToken)

	agents := app.Group("agent").Description("Read installation-wide Agent identity anchors")
	agents.Command("get").
		Description("Read identity only; behavior remains per Invocation").
		Args("agent-id").
		Run(runAgentGet)
	agents.Command("list").
		Flags(
			cli.String("agent-key").Help("Filter by exact host-owned Agent key"),
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runAgentList)

	invocations := app.Group("invocation").Description("Inspect and control Invocations")
	invocations.Command("get").Args("invocation-id").Run(runInvocationGet)
	invocations.Command("result").
		Description("Read the composed result: Invocation, messages, and assistant text").
		Args("invocation-id").
		Run(runInvocationResult)
	invocations.Command("timeline").
		Description("Read the durable execution waterfall without prompts or tool payloads").
		Args("invocation-id").
		Run(runInvocationTimeline)
	invocations.Command("traces").
		Description("List hosted agent traces; the durable timeline remains authoritative").
		Args("invocation-id").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runInvocationTraces)
	invocations.Command("logs").
		Description("List bounded operational logs without prompt or tool payload content").
		Args("invocation-id").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("trace-id").Help("Return only logs correlated to one trace"),
		).
		Run(runInvocationLogs)
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
		Flags(
			cli.Bool("deltas").Default(true).Help("Include provisional output and thinking deltas"),
		).
		Run(runInvocationStream)
	invocations.Command("cancel").Args("invocation-id").Run(runInvocationCancel)
	invocations.Command("interrupt").
		Description("Stop gracefully at the next seam and keep the work; cancel discards it").
		Args("invocation-id").
		Run(runInvocationInterrupt)
	invocations.Command("resume").
		Description("Raise the exhausted turn ceiling and continue a paused Invocation").
		Args("invocation-id").
		Flags(
			cli.Int("max-iterations").Help("Replacement model-call ceiling"),
			cli.Int("max-output-tokens").Help("Replacement output-token ceiling"),
			cli.String("max-estimated-cost-usd").Help("Replacement estimated-cost ceiling in USD"),
		).
		Run(runInvocationResume)
	invocations.Command("nudge").
		Description("Append steering to a running Invocation; the model sees it at the next seam").
		Args("invocation-id", "content").
		Flags(
			cli.String("idempotency-key").Help("Per-Invocation retry key; the same key and content stages once"),
		).
		Run(runInvocationNudge)
	invocations.Command("nudges").
		Description("List nudges in the order the turn will consume them").
		Args("invocation-id").
		Flags(
			cli.String("status").Enum("pending", "drained", "expired", "cancelled").Help("Restrict to one status"),
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runInvocationNudges)
	invocations.Command("tool-calls").
		Description("List durable ToolCall lifecycle records in discovery order").
		Args("invocation-id").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runInvocationToolCalls)
	invocations.Command("cancel-nudge").
		Description("Withdraw a nudge the turn has not taken yet").
		Args("invocation-id", "nudge-id").
		Run(runInvocationCancelNudge)
	invocations.Command("list").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("session-id").Help("Filter by Session ID"),
			cli.String("agent-id").Help("Filter by Agent ID"),
			cli.String("agent-key").Help("Filter by host-owned Agent key; mutually exclusive with --agent-id"),
			cli.String("user").Help("Filter by the per-turn end-user key"),
			cli.Strings("status").Help("Filter by Invocation status; repeat for a union"),
		).
		Run(runInvocationList)

	traces := app.Group("trace").Description("Inspect hosted agent traces")
	traces.Command("get").
		Description("Read the bounded span tree for one trace").
		Args("trace-id").
		Run(runTraceGet)

	models := app.Group("model").Description("Discover and inspect models")
	models.Command("list").
		Flags(
			cli.String("provider").Enum("anthropic", "openai", "xai", "google").Help("Limit results to one provider"),
			cli.Bool("include-deprecated").Help("Include deprecated catalog entries"),
		).
		Run(runModelList)
	models.Command("get").
		Flags(
			cli.String("provider").Required().Enum("anthropic", "openai", "xai", "google").Help("Model provider"),
			cli.String("model", "m").Required().Help("Exact model ID"),
		).
		Run(runModelGet)
	models.Command("pricing").
		Description("Inspect the standard price evidence for an exact model").
		Flags(
			cli.String("provider").Required().Enum("anthropic", "openai", "xai", "google").Help("Model provider"),
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
			cli.Int("max-output-tokens").Default(probeMaxOutputTokens).Help("Output budget for the probe; too low makes reasoning models fail as if the provider were unreachable"),
		).
		Run(runModelCheck)

	sessions := app.Group("session").Description("Create and read Session state and transcript")
	sessions.Command("create").
		Description("Create or seed a Session without admitting an Invocation").
		Flags(
			cli.String("agent-key").Help("Optional Agent key; omitted leaves the Session unbound until its first Invocation"),
			cli.String("tenant").Help("Tenant partition"),
			cli.String("user").Help("End-user label recorded on the Session; filtering only"),
			cli.String("session-key").Help("Caller Session key; requires --agent-key and makes creation an upsert"),
			cli.String("seed-messages").Help(`JSON user/assistant text array, such as [{"role":"user","content":"hello"}]`),
		).
		Run(runSessionCreate)
	sessions.Command("fork").
		Description("Copy a transcript prefix into a new Session; the source is unchanged").
		Args("session-id", "from-message").
		Flags(
			cli.String("session-key").Help("Caller key for the child; makes the fork an upsert"),
			cli.String("user").Help("End-user label recorded as the child Session opener"),
			cli.Int("retention-seconds").Help("Idle retention window for the child"),
			cli.String("metadata").Help(`JSON string map recorded on the child, such as {"branch":"alternative"}`),
		).
		Run(runSessionFork)
	sessions.Command("get").Args("session-id").Run(runSessionGet)
	sessions.Command("delete").
		Description("Erase a Session and its whole transcript; immediate and irreversible").
		Args("session-id").
		Flags(cli.Bool("yes").Required().Help("Confirm the erasure; required, because this cannot be undone")).
		Run(runSessionDelete)
	sessions.Command("set-metadata").
		Description("Merge host correlation metadata into a Session").
		Args("session-id", "patch").
		Run(runSessionSetMetadata)
	sessions.Command("resolve").
		Description("Recover a Session by caller-owned host keys").
		Flags(
			cli.String("session-key").Required().Help("Caller Session key"),
			cli.String("tenant").Help("Tenant partition containing the Session"),
			cli.Bool("default-tenant").Help("Resolve only in the default tenant"),
			cli.String("agent-id").Help("Exact Agent ID"),
			cli.String("agent-key").Help("Exact host-owned Agent key; mutually exclusive with --agent-id"),
		).
		Run(runSessionResolve)
	sessions.Command("list").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("agent-id").Help("Filter by Agent ID"),
			cli.String("agent-key").Help("Filter by host-owned Agent key; mutually exclusive with --agent-id"),
			cli.String("session-key").Help("Filter by caller Session key"),
			cli.String("tenant").Help("Filter by tenant partition"),
			cli.Bool("default-tenant").Help("Filter by the default tenant"),
			cli.String("user").Help("Filter by host-owned end-user key"),
		).
		Run(runSessionList)
	sessions.Command("messages").
		Args("session-id").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runSessionMessages)
	sessions.Command("compactions").
		Description("Display immutable applied and fell-through compaction records").
		Args("session-id").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runSessionCompactions)
	sessions.Command("transcript").
		Description("Display the fixed-cut durable transcript; text mode drains and renders messages").
		Args("session-id").
		Flags(
			cli.String("cursor").Help("Durable transcript cursor"),
			cli.String("page-token").Help("Fixed-cut page token"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runSessionTranscript)
	sessions.Command("stream").
		Args("session-id").
		Flags(
			cli.Bool("deltas").Default(true).Help("Include provisional output and thinking deltas"),
		).
		Run(runSessionStream)

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
	maxOutputTokens := command.Int("max-output-tokens")
	if maxOutputTokens < 1 {
		return errors.New("--max-output-tokens must be positive")
	}
	handle, err := client.Invoke(command.Context(), nvoken.InvokeRequest{
		AgentKey:       command.String("agent"),
		TenantKey:      optionalString(command.String("tenant")),
		Input:          "Reply with exactly OK.",
		IdempotencyKey: fmt.Sprintf("model-check:%s:%s:%d", provider, modelID, time.Now().UnixNano()),
		AgentDefinition: &nvoken.AgentDefinition{
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

// attachedInputBlocks reads local files, sniffs their media type, and encodes
// them beside the text argument. It returns nil when no file is attached so the
// ordinary text path is unchanged.
func attachedInputBlocks(command *cli.Context) ([]nvoken.InputBlock, error) {
	images := command.Strings("image")
	documents := command.Strings("document")
	imageURLs := command.Strings("image-url")
	documentURLs := command.Strings("document-url")
	if len(images) == 0 && len(documents) == 0 && len(imageURLs) == 0 && len(documentURLs) == 0 {
		return nil, nil
	}
	blocks := make([]nvoken.InputBlock, 0, 1+len(images)+len(documents)+len(imageURLs)+len(documentURLs))
	if text := command.Arg(0); strings.TrimSpace(text) != "" {
		blocks = append(blocks, nvoken.TextInputBlock(text))
	}
	for _, path := range images {
		block, err := attachedFileBlock(path, nvoken.InputBlockTypeImage)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	for _, path := range documents {
		block, err := attachedFileBlock(path, nvoken.InputBlockTypeDocument)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	for _, sourceURL := range imageURLs {
		blocks = append(blocks, nvoken.ImageURLInputBlock(sourceURL))
	}
	for _, sourceURL := range documentURLs {
		blocks = append(blocks, nvoken.DocumentURLInputBlock(sourceURL, ""))
	}
	if err := nvoken.PreflightInputBlocks(blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

func runCreateAgentDefinition(command *cli.Context) error {
	definition, err := agentDefinitionFlags(command)
	if err != nil {
		return err
	}
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	resource, err := client.CreateAgentDefinition(command.Context(), nvoken.CreateAgentDefinitionInput{
		Definition:     *definition,
		IdempotencyKey: command.String("idempotency-key"),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, resource, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\trevision=%d\n", resource.ID, resource.Revision)
		return err
	})
}

func runGetAgentDefinition(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	resource, err := client.GetAgentDefinition(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, resource, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\trevision=%d\t%s/%s\n", resource.ID, resource.Revision, resource.Model.Provider, resource.Model.ID)
		return err
	})
}

func runListAgentDefinitions(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	resources, err := client.ListAgentDefinitions(command.Context(), nvoken.ListAgentDefinitionsOptions{
		IncludeArchived: optionalBool(command.Bool("include-archived")),
		Cursor:          optionalString(command.String("cursor")),
		Limit:           optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, resources, func(writer io.Writer) error {
		for index := range resources.Items {
			resource := &resources.Items[index]
			if _, err := fmt.Fprintf(writer, "%s\trevision=%d\t%s/%s\n", resource.ID, resource.Revision, resource.Model.Provider, resource.Model.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

func runUpdateAgentDefinition(command *cli.Context) error {
	definition, err := agentDefinitionFlags(command)
	if err != nil {
		return err
	}
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	resource, err := client.UpdateAgentDefinition(command.Context(), command.Arg(0), nvoken.UpdateAgentDefinitionInput{
		Definition:       *definition,
		ExpectedRevision: int64(command.Int("revision")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, resource, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\trevision=%d\n", resource.ID, resource.Revision)
		return err
	})
}

func runArchiveAgentDefinition(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	return client.ArchiveAgentDefinition(command.Context(), command.Arg(0))
}

func runRestoreAgentDefinition(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	return client.RestoreAgentDefinition(command.Context(), command.Arg(0))
}

// agentDefinitionFlags reads a whole definition from a file, or builds the
// minimal one the inline flags can express. Tools, MCP servers, and output
// schemas have no flag spelling that stays readable, so a definition using them
// is supplied as JSON.
func agentDefinitionFlags(command *cli.Context) (*nvoken.AgentDefinition, error) {
	path := command.String("file")
	provider := command.String("provider")
	model := command.String("model")
	if path == "" {
		if provider == "" || model == "" {
			return nil, errors.New("--provider and --model are required without --file")
		}
		return &nvoken.AgentDefinition{
			Instructions: command.String("instructions"),
			Model:        nvoken.Model{Provider: provider, ID: model},
		}, nil
	}
	if provider != "" || model != "" {
		return nil, errors.New("--file is mutually exclusive with --provider and --model")
	}
	payload, err := readDefinitionFile(command, path)
	if err != nil {
		return nil, err
	}
	definition := &nvoken.AgentDefinition{}
	if err := json.Unmarshal(payload, definition); err != nil {
		return nil, fmt.Errorf("parse agent definition: %w", err)
	}
	return definition, nil
}

func readDefinitionFile(command *cli.Context, path string) ([]byte, error) {
	if path == "-" {
		payload, err := io.ReadAll(command.Stdin())
		if err != nil {
			return nil, fmt.Errorf("read agent definition from stdin: %w", err)
		}
		return payload, nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return payload, nil
}

func attachedFileBlock(path string, blockType string) (nvoken.InputBlock, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nvoken.InputBlock{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(payload) == 0 {
		return nvoken.InputBlock{}, fmt.Errorf("%s is empty", path)
	}
	mediaType := http.DetectContentType(payload)
	encoded := base64.StdEncoding.EncodeToString(payload)
	if blockType == nvoken.InputBlockTypeImage {
		return nvoken.ImageInputBlock(mediaType, encoded), nil
	}
	return nvoken.DocumentInputBlock(mediaType, encoded, filepath.Base(path)), nil
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
	agentDefinitionID := command.String("agent-definition-id")
	instructions := command.String("instructions")
	if agentDefinitionID != "" {
		if provider != "" || model != "" || instructions != "" {
			return errors.New("--agent-definition-id is mutually exclusive with --provider, --model, and --instructions")
		}
	} else if provider == "" || model == "" {
		return errors.New("--provider and --model are required without --agent-definition-id")
	}
	blocks, err := attachedInputBlocks(command)
	if err != nil {
		return err
	}
	input := command.Arg(0)
	if len(blocks) != 0 {
		input = ""
	}
	request := nvoken.InvokeRequest{
		AgentKey:          command.String("agent"),
		IdempotencyKey:    command.String("idempotency-key"),
		IfActive:          nvoken.IfActivePolicy(command.String("if-active")),
		Input:             input,
		InputBlocks:       blocks,
		AgentDefinitionID: agentDefinitionID,
	}
	if agentDefinitionID == "" {
		request.AgentDefinition = &nvoken.AgentDefinition{
			Instructions: instructions,
			Model: nvoken.Model{
				Provider: provider,
				ID:       model,
			},
		}
	}
	request.TenantKey = optionalString(command.String("tenant"))
	request.UserKey = optionalString(command.String("user"))
	request.SessionID = optionalString(command.String("session-id"))
	request.SessionKey = optionalString(command.String("session-key"))
	if request.Webhook, err = notifyTargetFlags(command); err != nil {
		return err
	}
	if request.Context, err = contextFlags(command); err != nil {
		return err
	}
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

func runInvocationTimeline(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	timeline, err := client.GetInvocationTimeline(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, timeline, func(writer io.Writer) error {
		for _, step := range timeline.Steps {
			duration := 0
			if step.DurationMs != nil {
				duration = *step.DurationMs
			}
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%dms\n",
				step.StartedAt.UTC().Format(time.RFC3339Nano),
				step.Kind,
				step.Status,
				step.DetailID,
				duration,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func runInvocationTraces(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListInvocationTraces(
		command.Context(),
		command.Arg(0),
		nvoken.ObservationListOptions{
			Cursor: optionalString(command.String("cursor")),
			Limit:  optionalInt(command.Int("limit")),
		},
	)
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		if page.Status == "disabled" {
			_, err := fmt.Fprintln(writer, "hosted observations disabled")
			return err
		}
		for _, trace := range page.Items {
			duration := 0
			if trace.DurationMs != nil {
				duration = *trace.DurationMs
			}
			completeness := "complete"
			if trace.IsPartial {
				completeness = "partial"
			}
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\tattempt=%s\t%d\t%dms\n",
				trace.StartedAt.UTC().Format(time.RFC3339Nano),
				trace.TraceID,
				trace.Status,
				completeness,
				optionalIntText(trace.Attempt),
				trace.SpanCount,
				duration,
			); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, page.NextCursor)
	})
}

func runInvocationLogs(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListInvocationLogs(
		command.Context(),
		command.Arg(0),
		nvoken.ObservationListOptions{
			Cursor:  optionalString(command.String("cursor")),
			Limit:   optionalInt(command.Int("limit")),
			TraceID: optionalString(command.String("trace-id")),
		},
	)
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		if page.Status == "disabled" {
			_, err := fmt.Fprintln(writer, "hosted observations disabled")
			return err
		}
		for _, log := range page.Items {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\tattempt=%s\titeration=%s\tlease_attempt=%s\t%s\n",
				log.Timestamp.UTC().Format(time.RFC3339Nano),
				log.Severity,
				optionalIntText(log.Attempt),
				optionalIntText(log.Iteration),
				optionalIntText(log.LeaseAttempt),
				log.Message,
			); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, page.NextCursor)
	})
}

func runTraceGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	trace, err := client.GetTrace(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, trace, func(writer io.Writer) error {
		for _, span := range trace.Spans {
			duration := 0
			if span.DurationMs != nil {
				duration = *span.DurationMs
			}
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%dms\n",
				span.StartedAt.UTC().Format(time.RFC3339Nano),
				span.SpanID,
				span.Status,
				span.Name,
				duration,
			); err != nil {
				return err
			}
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
	deltas := command.Bool("deltas")
	return handle.StreamWithOptions(command.Context(), nvoken.StreamOptions{Deltas: &deltas}, func(event nvoken.StreamEvent) error {
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

func runInvocationInterrupt(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	invocation, err := client.InterruptInvocation(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeInvocation(command, invocation)
}

func runInvocationResume(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	limits := nvoken.Limits{}
	fields := 0
	if value := command.Int("max-iterations"); value > 0 {
		limits.MaxIterations = &value
		fields++
	}
	if value := command.Int("max-output-tokens"); value > 0 {
		limits.MaxOutputTokens = &value
		fields++
	}
	if value := command.String("max-estimated-cost-usd"); value != "" {
		parsed, parseErr := strconv.ParseFloat(value, 32)
		if parseErr != nil || parsed <= 0 {
			return fmt.Errorf("max-estimated-cost-usd must be a positive number")
		}
		cost := float32(parsed)
		limits.MaxEstimatedCostUSD = &cost
		fields++
	}
	if fields != 1 {
		return fmt.Errorf("set exactly one replacement ceiling")
	}
	invocation, err := client.ResumeInvocation(command.Context(), command.Arg(0), limits)
	if err != nil {
		return err
	}
	return writeInvocation(command, invocation)
}

func runInvocationNudge(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	acknowledgement, err := client.CreateNudge(command.Context(), command.Arg(0), nvoken.NudgeRequest{
		Content:        command.Arg(1),
		IdempotencyKey: command.String("idempotency-key"),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, acknowledgement, func(writer io.Writer) error {
		_, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%d\n",
			acknowledgement.NudgeID,
			acknowledgement.Status,
			acknowledgement.AfterSequence,
		)
		return err
	})
}

func runInvocationNudges(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	options := nvoken.ListNudgesOptions{
		Cursor: optionalString(command.String("cursor")),
		Limit:  optionalInt(command.Int("limit")),
	}
	if status := command.String("status"); status != "" {
		value := nvoken.NudgeStatus(status)
		options.Status = &value
	}
	page, err := client.ListNudges(command.Context(), command.Arg(0), options)
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, input := range page.Items {
			if _, err := fmt.Fprintf(writer, "%s\t%s\n", input.ID, input.Status); err != nil {
				return err
			}
		}
		return nil
	})
}

func runInvocationToolCalls(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListToolCalls(
		command.Context(),
		command.Arg(0),
		nvoken.ListToolCallsOptions{
			Cursor: optionalString(command.String("cursor")),
			Limit:  optionalInt(command.Int("limit")),
		},
	)
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, call := range page.Items {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%d\n",
				call.ID,
				call.Mode,
				call.Status,
				call.Name,
				call.Attempts,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func runInvocationCancelNudge(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	cancelled, err := client.CancelNudge(command.Context(), command.Arg(0), command.Arg(1))
	if err != nil {
		return err
	}
	return writeOutput(command, cancelled, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", cancelled.ID, cancelled.Status)
		return err
	})
}

// optionalCallbackTimeout distinguishes an unset flag from a supplied one. Zero
// is outside the contract's 1 to 60 range, so it can stand for absent.
func optionalCallbackTimeout(command *cli.Context) *int64 {
	seconds := command.Int("callback-timeout")
	if seconds == 0 {
		return nil
	}
	value := int64(seconds)
	return &value
}

func runAppRegister(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	registered, err := client.RegisterApp(command.Context(), command.Arg(0), nvoken.RegisterAppOptions{
		ExternalRef:            optionalString(command.String("external-ref")),
		DisplayName:            optionalString(command.String("display-name")),
		OrgID:                  optionalString(command.String("org-id")),
		CallbackTimeoutSeconds: optionalCallbackTimeout(command),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, registered, func(writer io.Writer) error {
		if _, err := fmt.Fprintf(writer, "%s\t%s\n", registered.App.ID, registered.App.Name); err != nil {
			return err
		}
		for _, key := range registered.SigningKeys {
			if _, err := fmt.Fprintf(writer, "signing-key\t%s\t%s\tv%d\t%s\n", key.Purpose, key.KeyID, key.Version, key.Secret); err != nil {
				return err
			}
		}
		return nil
	})
}

func runAppGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	found, err := client.GetApp(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, found, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", found.ID, found.Name)
		return err
	})
}

func runAppUpdate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	updated, err := client.UpdateApp(command.Context(), command.Arg(0), nvoken.UpdateAppOptions{
		DisplayName:            optionalString(command.String("display-name")),
		OrgID:                  optionalString(command.String("org-id")),
		CallbackTimeoutSeconds: optionalCallbackTimeout(command),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, updated, func(writer io.Writer) error {
		name := updated.Name
		if updated.DisplayName != nil {
			name = *updated.DisplayName
		}
		_, err := fmt.Fprintf(writer, "%s\t%s\n", updated.ID, name)
		return err
	})
}

func runAppList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	list, err := client.ListApps(command.Context(), nvoken.ListAppsOptions{
		ExternalRef: optionalString(command.String("external-ref")),
		Status:      optionalArchiveStatus(command.String("status")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, list, func(writer io.Writer) error {
		for _, item := range list.Items {
			if _, err := fmt.Fprintf(writer, "%s\t%s\n", item.ID, item.Name); err != nil {
				return err
			}
		}
		return nil
	})
}

func runAppArchive(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	return client.ArchiveApp(command.Context(), command.Arg(0))
}

func runAppRestore(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	return client.RestoreApp(command.Context(), command.Arg(0))
}

func optionalArchiveStatus(value string) *nvoken.ArchiveStatus {
	if value == "" {
		return nil
	}
	status := nvoken.ArchiveStatus(value)
	return &status
}

func runAgentGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	agent, err := client.GetAgent(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, agent, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", agent.ID, agent.AgentKey)
		return err
	})
}

func runAgentList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListAgents(command.Context(), nvoken.ListAgentsOptions{
		AgentKey: optionalString(command.String("agent-key")),
		Cursor:   optionalString(command.String("cursor")),
		Limit:    optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, agent := range page.Items {
			if _, err := fmt.Fprintf(writer, "%s\t%s\n", agent.ID, agent.AgentKey); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, page.NextCursor)
	})
}

func runInvocationList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	statuses := make([]nvoken.InvocationStatus, len(command.Strings("status")))
	for index, status := range command.Strings("status") {
		statuses[index] = nvoken.InvocationStatus(status)
	}
	page, err := client.ListInvocations(command.Context(), nvoken.ListInvocationsOptions{
		UserKey:   optionalString(command.String("user")),
		SessionID: optionalString(command.String("session-id")),
		AgentID:   optionalString(command.String("agent-id")),
		AgentKey:  optionalString(command.String("agent-key")),
		Statuses:  statuses,
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
		_, err := fmt.Fprintf(writer, "%s\t%s\n", session.ID, sessionAgentLabel(session.AgentID))
		return err
	})
}

// runSessionDelete requires --yes. Every other destructive verb here is
// reversible or scoped to one credential; this one removes a whole transcript,
// so a mistyped id should cost a flag rather than the data. A required flag
// rather than an interactive prompt, so scripts and pipes behave the same.
func runSessionDelete(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	sessionID := command.Arg(0)
	if err := client.DeleteSession(command.Context(), sessionID); err != nil {
		return err
	}
	return writeOutput(command, map[string]string{"deleted": sessionID}, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "deleted\t%s\n", sessionID)
		return err
	})
}

func runSessionSetMetadata(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	// The patch is a JSON object so null — which deletes a key — can be said
	// at all. A flag-pair syntax cannot express it without inventing a
	// sentinel string that would then be unusable as a value.
	var metadata map[string]*string
	if err := json.Unmarshal([]byte(command.Arg(1)), &metadata); err != nil {
		return fmt.Errorf(`patch must be a JSON object of string or null values, such as {"title":"Refunds","trace_id":null}: %w`, err)
	}
	if len(metadata) == 0 {
		return fmt.Errorf("patch must set or delete at least one key")
	}
	session, err := client.UpdateSession(command.Context(), command.Arg(0), metadata)
	if err != nil {
		return err
	}
	return writeOutput(command, session, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", session.ID, sessionAgentLabel(session.AgentID))
		return err
	})
}

func runSessionCreate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	var seeds []nvoken.SeedMessage
	if encoded := command.String("seed-messages"); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &seeds); err != nil {
			return fmt.Errorf("seed-messages must be a JSON user/assistant text array: %w", err)
		}
		if len(seeds) == 0 {
			return fmt.Errorf("seed-messages must contain at least one message")
		}
	}
	session, err := client.CreateSession(command.Context(), nvoken.CreateSessionOptions{
		AgentKey:     optionalString(command.String("agent-key")),
		TenantKey:    optionalString(command.String("tenant")),
		UserKey:      optionalString(command.String("user")),
		SessionKey:   optionalString(command.String("session-key")),
		SeedMessages: seeds,
	})
	if err != nil {
		return err
	}
	return writeSession(command, session)
}

func runSessionFork(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	point := command.Arg(1)
	options := nvoken.ForkSessionOptions{
		SessionKey: optionalString(command.String("session-key")),
		UserKey:    optionalString(command.String("user")),
	}
	if sequence, parseErr := strconv.ParseInt(point, 10, 64); parseErr == nil {
		if sequence < 1 {
			return fmt.Errorf("from-message sequence must be at least 1")
		}
		options.FromSequence = &sequence
	} else {
		options.FromMessageID = &point
	}
	retentionSeconds := command.Int("retention-seconds")
	metadata := map[string]string(nil)
	if encoded := command.String("metadata"); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &metadata); err != nil {
			return fmt.Errorf("metadata must be a JSON string map: %w", err)
		}
		if len(metadata) == 0 {
			return fmt.Errorf("metadata must contain at least one entry")
		}
	}
	if retentionSeconds != 0 || len(metadata) != 0 {
		options.SessionOptions = &nvoken.SessionOptions{Metadata: metadata}
		if retentionSeconds != 0 {
			options.SessionOptions.Retention = &nvoken.SessionRetention{TTLSeconds: retentionSeconds}
		}
	}
	session, err := client.ForkSession(command.Context(), command.Arg(0), options)
	if err != nil {
		return err
	}
	return writeSession(command, session)
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
			UserKey:       optionalString(command.String("user")),
			AgentID:       optionalString(command.String("agent-id")),
			AgentKey:      optionalString(command.String("agent-key")),
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
		UserKey:       optionalString(command.String("user")),
		AgentID:       optionalString(command.String("agent-id")),
		AgentKey:      optionalString(command.String("agent-key")),
		SessionKey:    optionalString(command.String("session-key")),
		Cursor:        optionalString(command.String("cursor")),
		Limit:         optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, session := range page.Items {
			if _, err := fmt.Fprintf(writer, "%s\t%s\n", session.ID, sessionAgentLabel(session.AgentID)); err != nil {
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

func runSessionCompactions(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListSessionCompactions(
		command.Context(),
		command.Arg(0),
		nvoken.CompactionListOptions{
			Cursor: optionalString(command.String("cursor")),
			Limit:  optionalInt(command.Int("limit")),
		},
	)
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, compaction := range page.Items {
			failureClass := "-"
			if compaction.FailureClass != nil {
				failureClass = *compaction.FailureClass
			}
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%d\t%s\t%s\n",
				compaction.ID,
				compaction.Status,
				compaction.CoversThrough,
				compaction.InvocationID,
				failureClass,
			); err != nil {
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
	deltas := command.Bool("deltas")
	return client.StreamSessionWithOptions(command.Context(), command.Arg(0), nvoken.StreamOptions{Deltas: &deltas}, func(event nvoken.StreamEvent, snapshot nvoken.ReducedSnapshot) error {
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
		_, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\n",
			invocation.ID,
			invocation.Status,
			invocation.SessionID,
			stopReasonLabel(invocation.StopReason),
		)
		return err
	})
}

// stopReasonLabel keeps the column count stable: only a completed or
// incomplete Invocation says why it stopped, and every other status prints a
// placeholder rather than shifting the fields that follow.
func stopReasonLabel(reason *nvoken.InvocationStopReason) string {
	if reason == nil {
		return "-"
	}
	return string(*reason)
}

func writeSession(command *cli.Context, session *nvoken.Session) error {
	return writeOutput(command, session, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", session.ID, sessionAgentLabel(session.AgentID))
		return err
	})
}

// sessionAgentLabel renders a session's agent binding; a session created
// ahead of its first invocation has none yet.
func sessionAgentLabel(agentID *string) string {
	if agentID == nil {
		return "-"
	}
	return *agentID
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
		blockType, err := block.Discriminator()
		if err != nil {
			return fmt.Errorf("decode message content type: %w", err)
		}
		if blockType == "text" {
			textBlock, err := block.AsTextBlock()
			if err != nil {
				return fmt.Errorf("decode text content: %w", err)
			}
			parts = append(parts, textBlock.Text)
			continue
		}
		// A recorded context snapshot is text the host wrote, so it reads as
		// its name and content rather than as the raw block every other
		// non-text type falls back to.
		if blockType == "reminder" {
			reminder, err := block.AsReminderBlock()
			if err != nil {
				return fmt.Errorf("decode reminder content: %w", err)
			}
			parts = append(parts, fmt.Sprintf("[%s] %s", reminder.Name, reminder.Content))
			continue
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

func optionalIntText(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
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

// notifyTargetFlags refuses --webhook-event without --webhook-url rather than
// ignoring it, so a mistyped invocation cannot look like it asked for
// webhooks and silently get none.
// contextFlags reads the recorded application context. Every --context is
// emitted before every --context-operator, so the same flags always render the
// same order-sensitive list and a replay under one idempotency key matches.
func contextFlags(command *cli.Context) ([]nvoken.ContextItem, error) {
	var items []nvoken.ContextItem
	for _, tier := range []struct {
		flag string
		tier nvoken.ContextTier
	}{
		{"context", nvoken.ContextTierContextual},
		{"context-operator", nvoken.ContextTierOperator},
	} {
		for _, entry := range command.Strings(tier.flag) {
			name, content, found := strings.Cut(entry, "=")
			if !found || name == "" || content == "" {
				return nil, fmt.Errorf("--%s expects name=content, got %q", tier.flag, entry)
			}
			items = append(items, nvoken.ContextItem{
				Name:    name,
				Tier:    tier.tier,
				Content: content,
			})
		}
	}
	return items, nil
}

func notifyTargetFlags(command *cli.Context) (*nvoken.WebhookTarget, error) {
	url := command.String("webhook-url")
	events := command.Strings("webhook-event")
	if url == "" {
		if len(events) != 0 {
			return nil, errors.New("--webhook-event requires --webhook-url")
		}
		return nil, nil
	}
	target := nvoken.WebhookTarget{URL: url}
	for _, event := range events {
		target.Events = append(target.Events, nvoken.WebhookEvent(event))
	}
	return &target, nil
}
