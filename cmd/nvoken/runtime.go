package main

import (
	"bytes"
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
	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
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
	"cancelInvocation":           "invocation cancel",
	"getHealth":                  "health",
	"getReadiness":               "ready",
	"interruptInvocation":        "invocation interrupt",
	"resumeInvocation":           "invocation resume",
	"createNudge":                "invocation nudge",
	"listNudges":                 "invocation nudges",
	"listToolCalls":              "invocation tool-calls",
	"cancelNudge":                "invocation cancel-nudge",
	"createCredential":           "credentials create",
	"createInvocation":           "invoke",
	"createAgentDefinition":      "agent-definition create",
	"getAgentDefinitionRevision": "agent-definition revision",
	"listAgentDefinitions":       "agent-definition list",
	"getAgentDefinition":         "agent-definition get",
	"updateAgentDefinition":      "agent-definition update",
	"archiveAgentDefinition":     "agent-definition archive",
	"restoreAgentDefinition":     "agent-definition restore",
	"createAppClientKey":         "client-key create",
	"listAppClientKeys":          "client-key list",
	"revokeAppClientKey":         "client-key revoke",
	"listAppSigningKeys":         "signing-key list",
	"mintAppSigningKey":          "signing-key mint",
	"activateAppSigningKey":      "signing-key activate",
	"retireAppSigningKey":        "signing-key retire",
	"createSession":              "session create",
	"deleteTenant":               "tenant delete",
	"forkSession":                "session fork",
	"createProviderKey":          "provider-key create",
	"allocateCredits":            "credits allocate",
	"listCreditAccounts":         "credits accounts",
	"listCreditAllocations":      "credits allocations",
	"getCredential":              "credentials get",
	"getCurrentIdentity":         "auth status",
	"getAgent":                   "agent get",
	"createAgent":                "agent create",
	"updateAgent":                "agent update",
	"archiveAgent":               "agent archive",
	"restoreAgent":               "agent restore",
	"getApp":                     "app get",
	"listApps":                   "app list",
	"registerApp":                "app register",
	"updateApp":                  "app update",
	"archiveApp":                 "app archive",
	"restoreApp":                 "app restore",
	"getInvocation":              "invocation get",
	"getInvocationResult":        "invocation result",
	"getInvocationTimeline":      "invocation timeline",
	"getMemory":                  "memory get",
	"getTrace":                   "trace get",
	"getModel":                   "model get",
	"getOrg":                     "org get",
	"archiveOrg":                 "org archive",
	"restoreOrg":                 "org restore",
	"getProviderKey":             "provider-key get",
	"getUsageBreakdown":          "usage breakdown",
	"getUsageTimeseries":         "usage timeseries",
	"getProviderKeyUsage":        "provider-key usage",
	"getSession":                 "session get",
	"deleteSession":              "session delete",
	"updateSession":              "session set-metadata",
	"getSessionTranscript":       "session transcript",
	"listAgents":                 "agent list",
	"listCredentials":            "credentials list",
	"listInvocations":            "invocation list",
	"listAdmissions":             "admission list",
	"listInvocationLogs":         "invocation logs",
	"listInvocationTraces":       "invocation traces",
	"listMCPTools":               "mcp list-tools",
	"listMemories":               "memory list",
	"listModels":                 "model list",
	"listOrgs":                   "org list",
	"listProviderKeys":           "provider-key list",
	"listSessionCompactions":     "session compactions",
	"listSessionMessages":        "session messages",
	"listSessions":               "session list",
	"listTenants":                "tenant list",
	"listUsageRecords":           "usage records",
	"registerOrg":                "org register",
	"revokeProviderKey":          "provider-key revoke",
	"revokeCredential":           "credentials revoke",
	"rotateCredential":           "credentials rotate",
	"rotateProviderKey":          "provider-key rotate",
	"streamSession":              "session stream",
	"submitHostToolResults":      "tool-result submit",
	"summarizeAdmissions":        "admission summary",
	"deleteMemory":               "memory delete",
	"issueAnonymousToken":        "app anonymous-token",
	"updateOrg":                  "org update",
}

type runtimeConfig struct {
	BaseURL string `json:"base_url"`
}

func registerRuntimeCommands(app *cli.App) {
	app.Command("invoke").
		Description("Admit a durable turn; text mode streams and prints its answer").
		AddArg(optionalArg("input", "User text for the turn; omit only with --request-file")).
		Flags(
			cli.String("request-file", "f").Help("Complete CreateInvocationRequest JSON; - reads stdin and replaces all request flags"),
			cli.String("agent-key", "a").Help("Stable Agent key within the effective tenant"),
			cli.String("agent-id").Help("Opaque Agent ID; mutually exclusive with --agent-key"),
			cli.String("idempotency-key", "i").Help("Stable admission identity; reuse it unchanged after any uncertain acknowledgement"),
			cli.Int("definition-revision").Help("One-turn Agent Definition revision pin"),
			cli.String("provider").Help("Safe one-turn model provider override; requires --model"),
			cli.String("model", "m").Help("Safe one-turn exact model ID override; requires --provider"),
			cli.String("tenant").Help("Tenant partition"),
			cli.String("user").Help("End-user label recorded on this Invocation and its messages; filtering only"),
			cli.String("session-id").Help("Existing Session ID"),
			cli.String("session-key").Help("Caller Session key; one turn may be active at a time"),
			cli.String("parent-invocation-id").Help("Invocation whose durable ToolCall triggered this turn; requires --tool-call-id"),
			cli.String("tool-call-id").Help("ToolCall on --parent-invocation-id that triggered this turn"),
			cli.String("if-active").Default("reject").Enum("reject", "supersede", "interrupt").Help("Reject active work, atomically replace it, or stop it gracefully and keep what it produced"),
			cli.String("webhook-url").Help("HTTPS endpoint for signed Invocation webhooks"),
			cli.Strings("webhook-event").Help("Restrict webhooks to invocation.waiting, invocation.budget_hold, or invocation.ended; repeatable, default all"),
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
			cli.String("definition-key").Required().Help("Immutable caller-owned Definition key"),
			cli.String("name").Help("Human-facing Definition name; defaults to the Definition key"),
			cli.String("file", "f").Help("JSON Agent Definition to create; - reads stdin"),
			cli.String("instructions").Help("Agent instructions; ignored with --file"),
			cli.String("provider").Help("Model provider; required without --file"),
			cli.String("model", "m").Help("Exact model ID; required without --file"),
			cli.String("idempotency-key").Help("Pin replay to this create; the Definition key already scopes replay"),
		).
		Run(runCreateAgentDefinition)
	agentDefinitions.Command("get").
		Description("Read the current revision of one Agent Definition").
		AddArg(requiredArg("definition-id", "Opaque Agent Definition ID")).
		Run(runGetAgentDefinition)
	agentDefinitions.Command("revision").
		Description("Read one immutable Agent Definition revision").
		AddArg(requiredArg("definition-id", "Opaque Agent Definition ID")).
		AddArg(requiredArg("revision", "Positive revision number")).
		Run(runGetAgentDefinitionRevision)
	agentDefinitions.Command("list").
		Description("List Agent Definitions visible to the current App").
		Flags(
			cli.Bool("include-archived").Help("Include archived Agent Definitions"),
			cli.String("cursor").Help("Continue a previous page"),
			cli.Int("limit").Help("Maximum items in this page"),
		).
		Run(runListAgentDefinitions)
	agentDefinitions.Command("update").
		Description("Replace one Agent Definition at an expected revision").
		AddArg(requiredArg("definition-id", "Opaque Agent Definition ID")).
		Flags(
			cli.String("name").Help("Replacement Definition name; defaults to the Definition key"),
			cli.String("file", "f").Help("Replacement JSON Agent Definition, as `get --json` prints it; - reads stdin"),
			cli.String("instructions").Help("Agent instructions; ignored with --file"),
			cli.String("provider").Help("Model provider; required without --file"),
			cli.String("model", "m").Help("Exact model ID; required without --file"),
			cli.Int("revision").Required().Help("Current revision from GET"),
		).
		Run(runUpdateAgentDefinition)
	agentDefinitions.Command("archive").
		Description("Archive an Agent Definition and refuse new admissions through it").
		AddArg(requiredArg("definition-id", "Opaque Agent Definition ID")).
		Run(runArchiveAgentDefinition)
	agentDefinitions.Command("restore").
		Description("Restore an archived Agent Definition").
		AddArg(requiredArg("definition-id", "Opaque Agent Definition ID")).
		Run(runRestoreAgentDefinition)

	apps := app.Group("app").Description("Register and read host applications")
	apps.Command("init").
		Description("Register an App and emit its ready-to-use environment").
		AddArg(requiredArg("name", "Unique host-chosen App name")).
		Flags(
			cli.String("external-ref").Help("Opaque owner reference grounding console issuer tokens"),
			cli.String("display-name").Help("Human-facing label; name stays the unique handle"),
			cli.String("org-id").Help("Owning Org; Org-scoped callers may omit this to use their own"),
			cli.Int("callback-timeout").Help("Callback HTTP reply deadline in seconds, 1 to 60; default 10"),
			cli.String("credential-name").Help("Full App key name; defaults to '<App name> app'"),
			cli.Bool("browser").Help("Enable browser access and generate and register an Ed25519 client keypair"),
			cli.Strings("origin").Help("Exact allowed browser origin; repeatable and required with --browser"),
			cli.String("webhook-url").Help("HTTPS Invocation webhook URL; required with --browser"),
			cli.String("client-key-name").Default("browser").Help("Operator-facing browser client-key name"),
			cli.Int("max-concurrent-invocations").Default(50).Help("App-wide concurrent Invocation limit in browser mode"),
			cli.Int("max-admissions-per-minute").Default(300).Help("App-wide admission limit in browser mode"),
			cli.Int("max-concurrent-invocations-per-tenant").Default(20).Help("Per-tenant browser concurrency limit"),
			cli.Int("max-concurrent-invocations-per-user").Default(3).Help("Per-user browser concurrency limit"),
			cli.Int("max-admissions-per-user-per-minute").Default(20).Help("Per-user browser admission limit"),
		).
		Run(runAppInit)
	apps.Command("register").
		Description("Register one app; requires a credential not associated with an app").
		AddArg(optionalArg("name", "Unique host-chosen App name; omit only with --request-file")).
		Flags(
			cli.String("request-file", "f").Help("Complete RegisterAppRequest JSON; - reads stdin and replaces the name and request flags"),
			cli.String("external-ref").Help("Opaque owner reference grounding console issuer tokens"),
			cli.String("display-name").Help("Human-facing label; name stays the unique handle"),
			cli.String("org-id").Help("Owning Org; Org-scoped callers may omit this to use their own"),
			cli.Int("callback-timeout").Help("Callback HTTP reply deadline in seconds, 1 to 60; default 10"),
		).
		Run(runAppRegister)
	apps.Command("get").
		Description("Read one host application").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		Run(runAppGet)
	apps.Command("list").
		Description("List visible apps; an app-bound credential sees only its own").
		Flags(
			cli.String("external-ref").Help("Return only apps with exactly this external_ref"),
			cli.String("status").Enum("active", "archived", "all").Help("Filter by archive status; defaults to active"),
		).
		Run(runAppList)
	apps.Command("update").
		Description("Update an App's mutable configuration").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		Flags(
			cli.String("request-file", "f").Help("Complete UpdateAppRequest JSON, including explicit nulls; - reads stdin and replaces request flags"),
			cli.String("display-name").Help("Replacement human-facing label"),
			cli.String("org-id").Help("Replacement owning Org; installation administrators only"),
			cli.Int("callback-timeout").Help("Replacement callback HTTP reply deadline in seconds, 1 to 60"),
		).
		Run(runAppUpdate)
	apps.Command("archive").
		Description("Archive an App and refuse new admissions and grant minting").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		Run(runAppArchive)
	apps.Command("restore").
		Description("Restore an archived App").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		Run(runAppRestore)
	apps.Command("anonymous-token").
		Description("Mint public browser access without machine authentication").
		AddArg(requiredArg("app-id", "App configured for managed anonymous access")).
		Flags(
			cli.String("origin").Required().Help("Exact browser origin configured on the App"),
			cli.String("idempotency-key").Required().Help("Stable key to reuse when retrying this exchange"),
			cli.String("visitor-token").Help("Previously returned visitor token; omit on a first visit"),
		).
		Run(runAppAnonymousToken)

	agents := app.Group("agent").Description("Manage tenant-scoped Agent instances")
	agents.Command("create").
		Description("Create one tenant-scoped Agent instance").
		Flags(
			cli.String("agent-key").Required().Help("Stable key unique within the tenant"),
			cli.String("name").Help("Human-facing Agent name; defaults to the Agent key"),
			cli.String("definition-id").Help("Immutable Agent Definition binding by ID"),
			cli.String("definition-key").Help("The same binding by Definition key"),
			cli.String("tenant-key").Help("Tenant partition; omit for the default tenant"),
			cli.Int("pinned-revision").Help("Optional default Agent Definition revision pin"),
		).
		Run(runAgentCreate)
	agents.Command("get").
		Description("Read one Agent instance").
		AddArg(requiredArg("agent-id", "Opaque Agent ID")).
		Run(runAgentGet)
	agents.Command("list").
		Description("List tenant-scoped Agent instances").
		Flags(
			cli.String("tenant-key").Help("Filter by tenant partition"),
			cli.String("agent-key").Help("Filter by exact host-owned Agent key"),
			cli.String("definition-id").Help("Filter by Agent Definition"),
			cli.Bool("include-archived").Help("Include archived Agents"),
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runAgentList)
	agents.Command("update").
		Description("Update an Agent's name or default Definition revision pin").
		AddArg(requiredArg("agent-id", "Opaque Agent ID")).
		Flags(
			cli.String("name").Help("Replacement human-facing name"),
			cli.Int("pinned-revision").Help("Replacement revision pin"),
			cli.Bool("clear-pinned-revision").Help("Remove the revision pin"),
		).
		Run(runAgentUpdate)
	agents.Command("archive").
		Description("Archive an Agent and refuse new admissions through it").
		AddArg(requiredArg("agent-id", "Opaque Agent ID")).
		Run(runAgentArchive)
	agents.Command("restore").
		Description("Restore an archived Agent").
		AddArg(requiredArg("agent-id", "Opaque Agent ID")).
		Run(runAgentRestore)

	invocations := app.Group("invocation").Description("Inspect and control Invocations")
	invocations.Command("get").
		Description("Read authoritative state for one Invocation").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Run(runInvocationGet)
	invocations.Command("result").
		Description("Read the composed result: Invocation, messages, and assistant text").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Run(runInvocationResult)
	invocations.Command("timeline").
		Description("Read the durable execution waterfall without prompts or tool payloads").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Run(runInvocationTimeline)
	invocations.Command("traces").
		Description("List hosted agent traces; the durable timeline remains authoritative").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runInvocationTraces)
	invocations.Command("logs").
		Description("List bounded operational logs without prompt or tool payload content").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("trace-id").Help("Return only logs correlated to one trace"),
		).
		Run(runInvocationLogs)
	invocations.Command("wait").
		Description("Wait until terminal or actionable; waiting requires a tool result or cancellation").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Flags(
			cli.Int("timeout").Help("Local wait timeout in seconds; zero waits indefinitely"),
			cli.String("until").Default("terminal").Enum("terminal", "actionable").Help("Stop condition"),
		).
		Run(runInvocationWait)
	invocations.Command("stream").
		Description("Follow one turn until its terminal change; resume with a durable cursor after interruption").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Flags(
			cli.Bool("deltas").Default(true).Help("Include provisional message deltas"),
			cli.String("cursor").Help("Durable stream cursor to resume after"),
		).
		Run(runInvocationStream)
	invocations.Command("cancel").
		Description("Cancel an Invocation immediately and discard unfinished work").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Run(runInvocationCancel)
	invocations.Command("interrupt").
		Description("Stop gracefully at the next seam and keep the work; cancel discards it").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Run(runInvocationInterrupt)
	invocations.Command("resume").
		Description("Raise the exhausted turn ceiling and continue a budget-held Invocation").
		AddArg(requiredArg("invocation-id", "Opaque budget-held Invocation ID")).
		Flags(
			cli.Int("max-iterations").Help("Replacement model-call ceiling"),
			cli.Int("max-output-tokens").Help("Replacement output-token ceiling"),
			cli.String("max-estimated-cost-usd").Help("Replacement estimated-cost ceiling in USD"),
		).
		Run(runInvocationResume)
	invocations.Command("nudge").
		Description("Append steering to a running Invocation; the model sees it at the next seam").
		AddArg(requiredArg("invocation-id", "Opaque running Invocation ID")).
		AddArg(requiredArg("content", "Text direction to stage at the next execution seam")).
		Flags(
			cli.String("idempotency-key").Help("Per-Invocation retry key; the same key and content stages once"),
		).
		Run(runInvocationNudge)
	invocations.Command("nudges").
		Description("List nudges in the order the turn will consume them").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Flags(
			cli.String("status").Enum("pending", "drained", "expired", "cancelled").Help("Restrict to one status"),
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runInvocationNudges)
	invocations.Command("tool-calls").
		Description("List durable ToolCall lifecycle records in discovery order").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runInvocationToolCalls)
	invocations.Command("cancel-nudge").
		Description("Withdraw a nudge the turn has not taken yet").
		AddArg(requiredArg("invocation-id", "Opaque Invocation ID")).
		AddArg(requiredArg("nudge-id", "Pending Nudge ID to withdraw")).
		Run(runInvocationCancelNudge)
	invocations.Command("list").
		Description("List authoritative Invocations with exact filters").
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("tenant").Help("Filter by one non-default tenant partition"),
			cli.Bool("default-tenant").Help("Filter by the App default tenant"),
			cli.String("session-id").Help("Filter by Session ID"),
			cli.String("agent-id").Help("Filter by Agent ID"),
			cli.String("agent-key").Help("Filter by host-owned Agent key; mutually exclusive with --agent-id"),
			cli.String("user").Help("Filter by the per-turn end-user key"),
			cli.Strings("status").Help("Filter by Invocation status; repeat for a union"),
			cli.String("parent-invocation-id").Help("Filter by direct parent Invocation ID; use null for top-level Invocations"),
		).
		Run(runInvocationList)

	traces := app.Group("trace").Description("Inspect hosted agent traces")
	traces.Command("get").
		Description("Read the bounded span tree for one trace").
		AddArg(requiredArg("trace-id", "W3C trace ID attributed to an Invocation")).
		Run(runTraceGet)

	models := app.Group("model").Description("Discover and inspect models")
	models.Command("list").
		Description("List the installed model catalog and pricing status").
		Flags(
			cli.String("provider").Help("Limit results to one installed canonical provider"),
			cli.Bool("include-deprecated").Help("Include deprecated catalog entries"),
		).
		Run(runModelList)
	models.Command("get").
		Description("Inspect one exact provider and model selection").
		Flags(
			cli.String("provider").Required().Help("Installed canonical model provider"),
			cli.String("model", "m").Required().Help("Exact model ID"),
		).
		Run(runModelGet)
	models.Command("pricing").
		Description("Inspect the standard price evidence for an exact model").
		Flags(
			cli.String("provider").Required().Help("Installed canonical model provider"),
			cli.String("model", "m").Required().Help("Exact model ID"),
		).
		Run(runModelPricing)
	models.Command("check").
		Description("Run a small billed probe to verify configured provider access").
		AddArg(requiredArg("selection", "Exact provider/model selection, split at the first slash")).
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
			cli.String("request-file", "f").Help("Complete CreateSessionRequest JSON; - reads stdin and replaces request flags"),
			cli.String("agent-id").Help("Optional Agent ID; mutually exclusive with --agent-key"),
			cli.String("agent-key").Help("Optional Agent key; omitted leaves the Session unbound until its first Invocation"),
			cli.String("tenant").Help("Tenant partition"),
			cli.String("user").Help("End-user label recorded on the Session; filtering only"),
			cli.String("session-key").Help("Caller Session key; requires --agent-key and makes creation an upsert"),
			cli.String("seed-messages").Help(`JSON user/assistant text array, such as [{"role":"user","content":"hello"}]`),
		).
		Run(runSessionCreate)
	sessions.Command("fork").
		Description("Copy a transcript prefix into a new Session; the source is unchanged").
		AddArg(requiredArg("session-id", "Source Session ID")).
		AddArg(optionalArg("from-message", "Inclusive source message ID or sequence; omit only with --request-file")).
		Flags(
			cli.String("request-file", "f").Help("Complete ForkSessionRequest JSON; - reads stdin and replaces request flags"),
			cli.String("session-key").Help("Caller key for the child; makes the fork an upsert"),
			cli.Int("retention-seconds").Help("Idle retention window for the child"),
			cli.String("authorization-context").Help(`JSON string map binding the child, such as {"board":"b_42"}`),
		).
		Run(runSessionFork)
	sessions.Command("get").
		Description("Read authoritative Session identity and current state").
		AddArg(requiredArg("session-id", "Opaque Session ID")).
		Run(runSessionGet)
	sessions.Command("delete").
		Description("Erase a Session and its whole transcript; immediate and irreversible").
		AddArg(requiredArg("session-id", "Session ID to erase")).
		Flags(cli.Bool("yes").Required().Help("Confirm the erasure; required, because this cannot be undone")).
		Run(runSessionDelete)
	sessions.Command("set-metadata").
		Description("Merge host correlation metadata into a Session").
		AddArg(requiredArg("session-id", "Opaque Session ID")).
		AddArg(requiredArg("patch", "JSON object whose string values set keys and null values delete them")).
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
		Description("List Sessions with tenant, Agent, key, and user filters").
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
		Description("Page through canonical persisted Session messages").
		AddArg(requiredArg("session-id", "Opaque Session ID")).
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("order").Default("asc").Enum("asc", "desc").Help("Message sequence order; a cursor is bound to this direction"),
		).
		Run(runSessionMessages)
	sessions.Command("compactions").
		Description("Display immutable applied and fell-through compaction records").
		AddArg(requiredArg("session-id", "Opaque Session ID")).
		Flags(
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runSessionCompactions)
	sessions.Command("transcript").
		Description("Display the fixed-cut durable transcript; text mode drains and renders messages").
		AddArg(requiredArg("session-id", "Opaque Session ID")).
		Flags(
			cli.String("cursor").Help("Durable transcript cursor"),
			cli.String("page-token").Help("Fixed-cut page token"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runSessionTranscript)
	sessions.Command("stream").
		Description("Subscribe to a Session; stays open while it is idle, so interrupt to stop").
		AddArg(requiredArg("session-id", "Opaque Session ID")).
		Flags(
			cli.Bool("deltas").Default(true).Help("Include provisional message deltas"),
			cli.String("invocation-id").Help("Narrow frames to one Invocation and close when it settles"),
			cli.String("cursor").Help("Durable stream cursor to resume after"),
		).
		Run(runSessionStream)

	tools := app.Group("tool-result").Description("Submit durable host ToolCall results")
	tools.Command("submit").
		Description("Settle one or more pending host or callback ToolCalls").
		AddArg(requiredArg("invocation-id", "Invocation holding the pending ToolCalls")).
		AddArg(optionalArg("content", "Single result content as JSON; omit with --file")).
		Flags(
			cli.String("file", "f").Help("JSON result array with tool_call_id, content, and optional is_error; - reads stdin"),
			cli.String("tool-call-id").Help("Durable ToolCall identity; required in single-result mode"),
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
		Overrides: &nvoken.AgentDefinitionOverrides{
			Model: &nvoken.Model{
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
	resource, err := client.CreateAgentDefinition(
		command.Context(),
		*definition,
		nvoken.CreateAgentDefinitionOptions{IdempotencyKey: command.String("idempotency-key")},
	)
	if err != nil {
		return err
	}
	return writeOutput(command, resource, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\trevision=%d\n", resource.ID, resource.Revision)
		return err
	})
}

func runGetAgentDefinitionRevision(command *cli.Context) error {
	revision, err := strconv.ParseInt(command.Arg(1), 10, 64)
	if err != nil || revision < 1 {
		return errors.New("revision must be a positive integer")
	}
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	resource, err := client.GetAgentDefinitionRevision(command.Context(), command.Arg(0), revision)
	if err != nil {
		return err
	}
	return writeOutput(command, resource, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\trevision=%d\t%s/%s\n", resource.ID, resource.Revision, resource.Model.Provider, resource.Model.ID)
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
		return writeNextCursor(writer, resources.NextCursor)
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
	resource, err := client.UpdateAgentDefinition(
		command.Context(),
		command.Arg(0),
		*definition,
		nvoken.UpdateAgentDefinitionOptions{ExpectedRevision: int64(command.Int("revision"))},
	)
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
	definitionID := command.Arg(0)
	if err := client.ArchiveAgentDefinition(command.Context(), definitionID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "archived", "definition_id", definitionID)
}

func runRestoreAgentDefinition(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	definitionID := command.Arg(0)
	if err := client.RestoreAgentDefinition(command.Context(), definitionID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "restored", "definition_id", definitionID)
}

// agentDefinitionFlags reads a whole definition from a file, or builds the
// minimal one the inline flags can express. Tools, MCP servers, and output
// schemas have no flag spelling that stays readable, so a definition using them
// is supplied as JSON — the same flat shape the API accepts, so a definition
// read back with --json can be edited and sent straight back.
//
// --definition-key and --name are part of that shape, so a flag naming either
// wins over the file rather than sitting beside it.
func agentDefinitionFlags(command *cli.Context) (*nvoken.AgentDefinition, error) {
	path := command.String("file")
	provider := command.String("provider")
	model := command.String("model")
	definition := &nvoken.AgentDefinition{}
	switch {
	case path == "":
		if provider == "" || model == "" {
			return nil, errors.New("--provider and --model are required without --file")
		}
		definition.Instructions = command.String("instructions")
		definition.Model = nvoken.Model{Provider: provider, ID: model}
	case provider != "" || model != "":
		return nil, errors.New("--file is mutually exclusive with --provider and --model")
	default:
		payload, err := readDefinitionFile(command, path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, definition); err != nil {
			return nil, fmt.Errorf("parse agent definition: %w", err)
		}
	}
	if key := command.String("definition-key"); key != "" {
		definition.DefinitionKey = key
	}
	if name := command.String("name"); name != "" {
		definition.Name = name
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

func readJSONRequestFile(command *cli.Context, path string, maximum int64) ([]byte, error) {
	payload, err := readRequestFile(command, path, maximum)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("top-level value is not an object")
		}
		return nil, fmt.Errorf("parse request file %s as a JSON object: %w", path, err)
	}
	return payload, nil
}

func readRequestFile(command *cli.Context, path string, maximum int64) ([]byte, error) {
	var (
		reader io.Reader
		close  func() error
	)
	if path == "-" {
		reader = command.Stdin()
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open request file %s: %w", path, err)
		}
		reader = file
		close = file.Close
	}
	if close != nil {
		defer close()
	}
	payload, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read request file %s: %w", path, err)
	}
	if int64(len(payload)) > maximum {
		return nil, fmt.Errorf("request file %s exceeds %d bytes", path, maximum)
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
	if path := command.String("request-file"); path != "" {
		return runInvokeRequestFile(command, client, path)
	}
	agentID := command.String("agent-id")
	agentKey := command.String("agent-key")
	if (agentID == "") == (agentKey == "") {
		return errors.New("supply exactly one of --agent-id and --agent-key")
	}
	provider := command.String("provider")
	model := command.String("model")
	if (provider == "") != (model == "") {
		return errors.New("--provider and --model must be supplied together")
	}
	blocks, err := attachedInputBlocks(command)
	if err != nil {
		return err
	}
	input := command.Arg(0)
	if input == "" && len(blocks) == 0 {
		return errors.New("input is required unless --request-file supplies the complete request")
	}
	if len(blocks) != 0 {
		input = ""
	}
	request := nvoken.InvokeRequest{
		AgentID:        agentID,
		AgentKey:       agentKey,
		IdempotencyKey: command.String("idempotency-key"),
		IfActive:       nvoken.IfActivePolicy(command.String("if-active")),
		Input:          input,
		InputBlocks:    blocks,
	}
	if provider != "" {
		request.Overrides = &nvoken.AgentDefinitionOverrides{
			Model: &nvoken.Model{
				Provider: provider,
				ID:       model,
			},
		}
	}
	if revision := command.Int("definition-revision"); revision > 0 {
		value := int64(revision)
		request.DefinitionRevision = &value
	}
	request.TenantKey = optionalString(command.String("tenant"))
	request.UserKey = optionalString(command.String("user"))
	request.SessionID = optionalString(command.String("session-id"))
	request.SessionKey = optionalString(command.String("session-key"))
	parentInvocationID := command.String("parent-invocation-id")
	toolCallID := command.String("tool-call-id")
	if (parentInvocationID == "") != (toolCallID == "") {
		return errors.New("--parent-invocation-id and --tool-call-id must be supplied together")
	}
	if parentInvocationID != "" {
		request.TriggeredBy = &nvoken.InvocationTrigger{
			Type:               "tool_call",
			ParentInvocationID: parentInvocationID,
			ToolCallID:         toolCallID,
		}
	}
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
	return renderAcceptedInvocation(command, handle)
}

func runInvokeRequestFile(command *cli.Context, client *nvoken.Client, path string) error {
	if command.Arg(0) != "" {
		return errors.New("input and --request-file are mutually exclusive")
	}
	for _, flag := range []string{
		"agent-key", "agent-id", "idempotency-key", "definition-revision", "provider", "model",
		"tenant", "user", "session-id", "session-key", "parent-invocation-id", "tool-call-id",
		"if-active", "webhook-url",
		"webhook-event", "context", "context-operator", "image", "document", "image-url",
		"document-url",
	} {
		if command.IsSet(flag) {
			return fmt.Errorf("--request-file cannot be combined with --%s", flag)
		}
	}
	payload, err := readJSONRequestFile(command, path, 25<<20)
	if err != nil {
		return err
	}
	response, err := client.Raw().CreateInvocationWithBodyWithResponse(
		command.Context(),
		&generated.CreateInvocationParams{},
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	if response.JSON202 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	accepted := response.JSON202
	handle := client.Invocation(accepted.ID)
	handle.SessionID = accepted.SessionID
	handle.AgentID = accepted.AgentID
	handle.Status = accepted.Status
	handle.Deduplicated = accepted.Deduplicated
	handle.DeadlineAt = accepted.DeadlineAt
	var identity struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if json.Unmarshal(payload, &identity) == nil {
		handle.IdempotencyKey = identity.IdempotencyKey
	}
	return renderAcceptedInvocation(command, handle)
}

func renderAcceptedInvocation(command *cli.Context, handle *nvoken.InvocationHandle) error {
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
	return handle.StreamWithOptions(command.Context(), nvoken.StreamOptions{
		Deltas: &deltas,
		Cursor: optionalString(command.String("cursor")),
	}, func(event nvoken.StreamEvent) error {
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
		// A turn ends on its terminal change. Close off the prose that was
		// streaming into the terminal before printing the frame that says so.
		if event.Type == "transcript.update" && renderedDelta {
			renderedDelta = false
			if _, err := fmt.Fprintln(command.Stdout()); err != nil {
				return err
			}
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
		return writeNextCursor(writer, page.NextCursor)
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
		return writeNextCursor(writer, page.NextCursor)
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
	if path := command.String("request-file"); path != "" {
		if command.Arg(0) != "" {
			return errors.New("name and --request-file are mutually exclusive")
		}
		for _, flag := range []string{"external-ref", "display-name", "org-id", "callback-timeout"} {
			if command.IsSet(flag) {
				return fmt.Errorf("--request-file cannot be combined with --%s", flag)
			}
		}
		payload, err := readJSONRequestFile(command, path, 1<<20)
		if err != nil {
			return err
		}
		response, err := client.Raw().RegisterAppWithBodyWithResponse(
			command.Context(),
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			return err
		}
		if response.JSON201 == nil {
			return responseError(response.StatusCode(), response.Body)
		}
		return writeAppRegistration(command, response.JSON201)
	}
	if strings.TrimSpace(command.Arg(0)) == "" {
		return errors.New("name is required unless --request-file supplies the complete request")
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
	return writeAppRegistration(command, registered)
}

func writeAppRegistration(command *cli.Context, registered *nvoken.AppRegistration) error {
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
	if path := command.String("request-file"); path != "" {
		for _, flag := range []string{"display-name", "org-id", "callback-timeout"} {
			if command.IsSet(flag) {
				return fmt.Errorf("--request-file cannot be combined with --%s", flag)
			}
		}
		payload, err := readJSONRequestFile(command, path, 1<<20)
		if err != nil {
			return err
		}
		response, err := client.Raw().UpdateAppWithBodyWithResponse(
			command.Context(),
			command.Arg(0),
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			return err
		}
		if response.JSON200 == nil {
			return responseError(response.StatusCode(), response.Body)
		}
		return writeApp(command, response.JSON200)
	}
	updated, err := client.UpdateApp(command.Context(), command.Arg(0), nvoken.UpdateAppOptions{
		DisplayName:            optionalString(command.String("display-name")),
		OrgID:                  optionalString(command.String("org-id")),
		CallbackTimeoutSeconds: optionalCallbackTimeout(command),
	})
	if err != nil {
		return err
	}
	return writeApp(command, updated)
}

func writeApp(command *cli.Context, app *nvoken.App) error {
	return writeOutput(command, app, func(writer io.Writer) error {
		name := app.Name
		if app.DisplayName != nil {
			name = *app.DisplayName
		}
		_, err := fmt.Fprintf(writer, "%s\t%s\n", app.ID, name)
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
	appID := command.Arg(0)
	if err := client.ArchiveApp(command.Context(), appID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "archived", "app_id", appID)
}

func runAppRestore(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	appID := command.Arg(0)
	if err := client.RestoreApp(command.Context(), appID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "restored", "app_id", appID)
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
		_, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", agent.ID, agent.AgentKey, agent.Name)
		return err
	})
}

func runAgentCreate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	input := nvoken.CreateAgentInput{
		TenantKey:     optionalString(command.String("tenant-key")),
		AgentKey:      command.String("agent-key"),
		Name:          command.String("name"),
		DefinitionID:  command.String("definition-id"),
		DefinitionKey: command.String("definition-key"),
	}
	if revision := command.Int("pinned-revision"); revision > 0 {
		value := int64(revision)
		input.PinnedRevision = &value
	}
	agent, err := client.CreateAgent(command.Context(), input)
	if err != nil {
		return err
	}
	return writeOutput(command, agent, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", agent.ID, agent.AgentKey, agent.Name)
		return err
	})
}

func runAgentList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListAgents(command.Context(), nvoken.ListAgentsOptions{
		TenantKey:       optionalString(command.String("tenant-key")),
		AgentKey:        optionalString(command.String("agent-key")),
		DefinitionID:    optionalString(command.String("definition-id")),
		IncludeArchived: optionalBool(command.Bool("include-archived")),
		Cursor:          optionalString(command.String("cursor")),
		Limit:           optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, agent := range page.Items {
			if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", agent.ID, agent.AgentKey, agent.Name); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, page.NextCursor)
	})
}

func runAgentUpdate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	input := nvoken.UpdateAgentInput{
		Name:                optionalString(command.String("name")),
		ClearPinnedRevision: command.Bool("clear-pinned-revision"),
	}
	if revision := command.Int("pinned-revision"); revision > 0 {
		value := int64(revision)
		input.PinnedRevision = &value
	}
	agent, err := client.UpdateAgent(command.Context(), command.Arg(0), input)
	if err != nil {
		return err
	}
	return writeOutput(command, agent, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", agent.ID, agent.AgentKey, agent.Name)
		return err
	})
}

func runAgentArchive(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	agentID := command.Arg(0)
	if err := client.ArchiveAgent(command.Context(), agentID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "archived", "agent_id", agentID)
}

func runAgentRestore(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	agentID := command.Arg(0)
	if err := client.RestoreAgent(command.Context(), agentID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "restored", "agent_id", agentID)
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
		TenantKey:          optionalString(command.String("tenant")),
		DefaultTenant:      optionalBool(command.Bool("default-tenant")),
		UserKey:            optionalString(command.String("user")),
		SessionID:          optionalString(command.String("session-id")),
		AgentID:            optionalString(command.String("agent-id")),
		AgentKey:           optionalString(command.String("agent-key")),
		Statuses:           statuses,
		ParentInvocationID: optionalString(command.String("parent-invocation-id")),
		Cursor:             optionalString(command.String("cursor")),
		Limit:              optionalInt(command.Int("limit")),
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
	if path := command.String("request-file"); path != "" {
		for _, flag := range []string{"agent-id", "agent-key", "tenant", "user", "session-key", "seed-messages"} {
			if command.IsSet(flag) {
				return fmt.Errorf("--request-file cannot be combined with --%s", flag)
			}
		}
		payload, err := readJSONRequestFile(command, path, 1<<20)
		if err != nil {
			return err
		}
		response, err := client.Raw().CreateSessionWithBodyWithResponse(
			command.Context(),
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			return err
		}
		if response.JSON201 == nil {
			return responseError(response.StatusCode(), response.Body)
		}
		return writeSession(command, response.JSON201)
	}
	if command.String("agent-id") != "" && command.String("agent-key") != "" {
		return errors.New("--agent-id and --agent-key are mutually exclusive")
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
		AgentID:      optionalString(command.String("agent-id")),
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
	if path := command.String("request-file"); path != "" {
		if command.Arg(1) != "" {
			return errors.New("from-message and --request-file are mutually exclusive")
		}
		for _, flag := range []string{"session-key", "retention-seconds", "authorization-context"} {
			if command.IsSet(flag) {
				return fmt.Errorf("--request-file cannot be combined with --%s", flag)
			}
		}
		payload, err := readJSONRequestFile(command, path, 1<<20)
		if err != nil {
			return err
		}
		response, err := client.Raw().ForkSessionWithBodyWithResponse(
			command.Context(),
			command.Arg(0),
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			return err
		}
		if response.JSON201 == nil {
			return responseError(response.StatusCode(), response.Body)
		}
		return writeSession(command, response.JSON201)
	}
	point := command.Arg(1)
	if strings.TrimSpace(point) == "" {
		return errors.New("from-message is required unless --request-file supplies the complete request")
	}
	options := nvoken.ForkSessionOptions{
		SessionKey: optionalString(command.String("session-key")),
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
	authorizationContext := map[string]string(nil)
	if encoded := command.String("authorization-context"); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &authorizationContext); err != nil {
			return fmt.Errorf("authorization context must be a JSON string map: %w", err)
		}
		if len(authorizationContext) == 0 {
			return fmt.Errorf("authorization context must contain at least one entry")
		}
	}
	if retentionSeconds != 0 || len(authorizationContext) != 0 {
		options.SessionOptions = &nvoken.SessionOptions{AuthorizationContext: authorizationContext}
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
		Order:  optionalListOrder(command.String("order")),
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
			drain.Cursor,
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
			snapshot.Cursor,
		)
	})
}

func runSessionStream(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	deltas := command.Bool("deltas")
	return client.StreamSessionWithOptions(command.Context(), command.Arg(0), nvoken.StreamOptions{
		Deltas:       &deltas,
		Cursor:       optionalString(command.String("cursor")),
		InvocationID: optionalString(command.String("invocation-id")),
	}, func(event nvoken.StreamEvent, snapshot nvoken.ReducedSnapshot) error {
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
		if _, err := fmt.Fprintf(command.Stdout(), "%s\t%s\n", event.Type, snapshot.Cursor); err != nil {
			return err
		}
		// The stream is a subscription and never ends on its own. This command
		// is a tail, so it leaves when the server says the Session went quiet.
		// Run it again to pick up where this cursor left off.
		if idleClose(event) {
			return nvoken.ErrStopStream
		}
		return nil
	})
}

func idleClose(event nvoken.StreamEvent) bool {
	if event.Type != "connection.closing" {
		return false
	}
	var end struct {
		Reason string `json:"reason"`
	}
	return json.Unmarshal(event.Data, &end) == nil && end.Reason == "idle"
}

func runToolResultSubmit(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	var results []nvoken.ToolResult
	if path := command.String("file"); path != "" {
		if command.Arg(1) != "" || command.String("tool-call-id") != "" || command.IsSet("error") {
			return errors.New("--file cannot be combined with content, --tool-call-id, or --error")
		}
		payload, err := readRequestFile(command, path, 1<<20)
		if err != nil {
			return err
		}
		var fileResults []struct {
			ToolCallID string `json:"tool_call_id"`
			Content    any    `json:"content"`
			IsError    bool   `json:"is_error,omitempty"`
		}
		if err := json.Unmarshal(payload, &fileResults); err != nil {
			return fmt.Errorf("parse result file %s as a JSON array: %w", path, err)
		}
		if len(fileResults) == 0 || len(fileResults) > 32 {
			return errors.New("result file must contain between 1 and 32 results")
		}
		results = make([]nvoken.ToolResult, 0, len(fileResults))
		for index, item := range fileResults {
			if strings.TrimSpace(item.ToolCallID) == "" {
				return fmt.Errorf("result %d is missing tool_call_id", index+1)
			}
			results = append(results, nvoken.ToolResult{
				ToolCallID: item.ToolCallID,
				Content:    item.Content,
				IsError:    item.IsError,
			})
		}
	} else {
		if strings.TrimSpace(command.String("tool-call-id")) == "" {
			return errors.New("--tool-call-id is required without --file")
		}
		if command.Arg(1) == "" {
			return errors.New("content is required without --file")
		}
		var content any
		if err := json.Unmarshal([]byte(command.Arg(1)), &content); err != nil {
			return fmt.Errorf("parse result content as JSON: %w", err)
		}
		results = []nvoken.ToolResult{{
			ToolCallID: command.String("tool-call-id"),
			Content:    content,
			IsError:    command.Bool("error"),
		}}
	}
	result, err := client.SubmitToolResults(command.Context(), command.Arg(0), results)
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
	scope := nvoken.Scope{
		TenantKey: strings.TrimSpace(command.String("scope-tenant-key")),
		UserKey:   strings.TrimSpace(command.String("scope-user-key")),
	}
	if scope == (nvoken.Scope{}) {
		return nvoken.NewClient(auth.BaseURL, auth.APIKey)
	}
	return nvoken.NewClient(auth.BaseURL, auth.APIKey, nvoken.WithScope(scope))
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

func writeMutationReceipt(command *cli.Context, action, resource, id string) error {
	receipt := map[string]string{
		"action": action,
		resource: id,
	}
	return writeOutput(command, receipt, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", action, id)
		return err
	})
}

// outputTextDelta pulls the assistant prose out of a preview frame. Reasoning
// and tool arguments arrive on the same frame under a different kind, and this
// command prints neither.
func outputTextDelta(event nvoken.StreamEvent) (string, bool, error) {
	if event.Type != "message.delta" {
		return "", false, nil
	}
	var delta struct {
		Kind  string `json:"kind"`
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(event.Data, &delta); err != nil {
		return "", false, fmt.Errorf("decode message delta: %w", err)
	}
	if delta.Kind != "text" {
		return "", false, nil
	}
	return delta.Delta, true, nil
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
	cursor string,
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
	_, err := fmt.Fprintf(writer, "cursor\t%s\n", cursor)
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

func optionalListOrder(value string) *nvoken.ListOrder {
	if value == "" {
		return nil
	}
	order := nvoken.ListOrder(value)
	return &order
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
