package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
	"github.com/deepnoodle-ai/wonton/cli"
)

const localBaseURL = defaultBaseURL

var version = "devel"

func registerRuntimeCommands(app *cli.App) {
	apps := app.Group("app").Description("Register and configure host applications")
	apps.Command("init").Description("Register an App and print its one-time deployment environment").
		AddArg(requiredArg("name", "Unique App name")).
		Flags(
			cli.String("external-ref"),
			cli.String("display-name"),
			cli.String("org-id"),
			cli.String("credential-name"),
			cli.Int("callback-timeout"),
			cli.Bool("browser"),
			cli.Strings("origin"),
			cli.String("webhook-url"),
			cli.String("client-key-name").Default("browser"),
			cli.Int("max-concurrent-turns").Default(30),
			cli.Int("max-admissions-per-minute").Default(200),
			cli.Int("max-concurrent-turns-per-tenant").Default(12),
			cli.Int("max-concurrent-turns-per-user").Default(2),
			cli.Int("max-admissions-per-user-per-minute").Default(10),
		).Run(runAppInit)

	agents := app.Group("agent").Description("Manage and run reusable Agents")
	agents.Command("lookup").Description("Resolve an Agent key in one owner namespace").
		AddArg(requiredArg("agent-key", "Caller-owned Agent key")).
		Flags(ownerFlags()...).Run(runAgentLookup)
	agents.Command("get").Description("Read an Agent by opaque ID").
		AddArg(requiredArg("agent-id", "Opaque Agent ID")).Run(runAgentGet)
	agents.Command("list").Description("List Agents in one owner namespace").
		Flags(append(ownerFlags(), cli.Bool("include-archived"), cli.String("cursor"), cli.Int("limit"))...).Run(runAgentList)
	agents.Command("create").Description("Create an Agent and revision 1 from an exact JSON request").
		Flags(cli.String("file", "f").Required().Help("CreateAgentRequest JSON; - reads stdin"), cli.String("idempotency-key")).Run(runAgentCreate)
	agents.Command("publish").Description("Publish an immutable Agent revision from BehaviorInput JSON").
		AddArg(requiredArg("agent-id", "Opaque Agent ID")).
		Flags(cli.String("file", "f").Required().Help("BehaviorInput JSON; - reads stdin"), cli.String("idempotency-key")).Run(runAgentPublish)
	agents.Command("revisions").Description("List immutable revisions of one Agent").
		AddArg(requiredArg("agent-id", "Opaque Agent ID")).Flags(cli.String("cursor"), cli.Int("limit")).Run(runAgentRevisions)
	agents.Command("archive").Description("Archive an Agent").AddArg(requiredArg("agent-id", "Opaque Agent ID")).Run(runAgentArchive)
	agents.Command("restore").Description("Restore an archived Agent").AddArg(requiredArg("agent-id", "Opaque Agent ID")).Run(runAgentRestore)

	turns := app.Group("turn").Description("Start and inspect durable Turns")
	turns.Command("start").Description("Start one durable Turn").
		AddArg(requiredArg("input", "User text for the Turn")).
		Flags(
			cli.String("agent-id").Required().Help("Agent to run"),
			cli.String("tenant").Required().Help("Tenant in which the Turn runs"),
			cli.String("user").Help("Optional Turn actor"),
			cli.String("conversation-id").Help("Continue an existing Conversation"),
			cli.String("memory-scope").Enum("none", "tenant", "user").Help("Override memory selection"),
			cli.String("memory-namespace").Help("MemorySpace namespace"),
			cli.String("idempotency-key").Help("Stable retry key; generated when omitted"),
			cli.Bool("wait").Help("Wait for the result instead of returning the accepted Turn"),
		).Run(runTurnStart)
	turns.Command("get").Description("Read a Turn").AddArg(requiredArg("turn-id", "Opaque Turn ID")).Run(runTurnGet)
	turns.Command("result").Description("Read a Turn with its produced messages and final-answer text").AddArg(requiredArg("turn-id", "Opaque Turn ID")).Run(runTurnResult)
	turns.Command("list").Description("List Turns").Flags(
		cli.String("tenant"), cli.String("user"),
		cli.String("conversation-id"), cli.String("agent-id"), cli.Strings("status"),
		cli.String("cursor"), cli.Int("limit"),
	).Run(runTurnList)
	turns.Command("cancel").Description("Cancel a nonterminal Turn").AddArg(requiredArg("turn-id", "Opaque Turn ID")).Run(runTurnCancel)
	turns.Command("interrupt").Description("Stop a Turn at its next safe seam").AddArg(requiredArg("turn-id", "Opaque Turn ID")).Run(runTurnInterrupt)
	turns.Command("resume").Description("Resume a budget-held Turn with narrowed limits JSON").
		AddArg(requiredArg("turn-id", "Opaque Turn ID")).Flags(cli.String("file", "f").Required()).Run(runTurnResume)

	conversations := app.Group("conversation").Description("Manage durable Conversation continuity")
	conversations.Command("get").AddArg(requiredArg("conversation-id", "Opaque Conversation ID")).Run(runConversationGet)
	conversations.Command("list").Flags(cli.String("tenant"), cli.String("user"), cli.String("owner"), cli.String("key"), cli.String("cursor"), cli.Int("limit")).Run(runConversationList)
	conversations.Command("create").Description("Create a Conversation from exact JSON").Flags(cli.String("file", "f").Required()).Run(runConversationCreate)
	conversations.Command("delete").AddArg(requiredArg("conversation-id", "Opaque Conversation ID")).Run(runConversationDelete)
	conversations.Command("messages").AddArg(requiredArg("conversation-id", "Opaque Conversation ID")).Flags(cli.String("cursor"), cli.Int("limit")).Run(runConversationMessages)
	conversations.Command("transcript").AddArg(requiredArg("conversation-id", "Opaque Conversation ID")).Run(runConversationTranscript)

	memories := app.Group("memory-space").Description("Manage tenant- and user-scoped MemorySpaces")
	memories.Command("get").AddArg(requiredArg("memory-space-id", "Opaque MemorySpace ID")).Run(runMemorySpaceGet)
	memories.Command("list").Flags(cli.String("tenant"), cli.String("user"), cli.String("scope").Enum("tenant", "user"), cli.Bool("erased"), cli.String("cursor"), cli.Int("limit")).Run(runMemorySpaceList)
	memories.Command("resolve").Description("Resolve or create a MemorySpace from exact JSON").Flags(cli.String("file", "f").Required()).Run(runMemorySpaceResolve)
	memories.Command("delete").AddArg(requiredArg("memory-space-id", "Opaque MemorySpace ID")).Run(runMemorySpaceDelete)
}

func ownerFlags() []cli.Flag {
	return []cli.Flag{
		cli.String("owner").Default("app").Enum("app", "tenant", "user").Help("Agent owner namespace"),
		cli.String("tenant").Help("Tenant owner coordinate"),
		cli.String("user").Help("User owner coordinate"),
	}
}

func runAgentLookup(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	options := nvoken.AgentLookupOptions{}
	switch command.String("owner") {
	case "tenant":
		options.OwnedBy = nvoken.TenantOwned(command.String("tenant"))
	case "user":
		options.OwnedBy = nvoken.UserOwned(command.String("tenant"), command.String("user"))
	}
	agent, err := client.Agent(command.Context(), command.Arg(0), options)
	if err != nil {
		return err
	}
	return renderJSON(command, agent.Resource())
}

func runAgentGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	agent, err := client.Agents().GetByID(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return renderJSON(command, agent.Resource())
}

func runAgentList(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	kind := generated.AgentOwnerKind(command.String("owner"))
	params := &generated.ListAgentsParams{OwnerKind: kind, TenantKey: optionalString(command.String("tenant")), UserKey: optionalString(command.String("user")), IncludeArchived: optionalBool(command.Bool("include-archived")), Cursor: optionalString(command.String("cursor")), Limit: optionalInt(command.Int("limit"))}
	response, err := client.ListAgentsWithResponse(command.Context(), params)
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}

func runAgentCreate(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	var body generated.CreateAgentRequest
	if err := readJSONFile(command.String("file"), &body); err != nil {
		return err
	}
	key := command.String("idempotency-key")
	if key == "" {
		key = randomKey()
	}
	response, err := client.CreateAgentWithResponse(command.Context(), &generated.CreateAgentParams{IdempotencyKey: key}, body)
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON201)
}

func runAgentPublish(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	var body generated.BehaviorInput
	if err := readJSONFile(command.String("file"), &body); err != nil {
		return err
	}
	key := command.String("idempotency-key")
	if key == "" {
		key = randomKey()
	}
	response, err := client.PublishAgentRevisionWithResponse(command.Context(), command.Arg(0), &generated.PublishAgentRevisionParams{IdempotencyKey: key}, body)
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON201)
}

func runAgentRevisions(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	response, err := client.ListAgentRevisionsWithResponse(command.Context(), command.Arg(0), &generated.ListAgentRevisionsParams{Cursor: optionalString(command.String("cursor")), Limit: optionalInt(command.Int("limit"))})
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}

func runAgentArchive(command *cli.Context) error { return runAgentLifecycle(command, true) }
func runAgentRestore(command *cli.Context) error { return runAgentLifecycle(command, false) }
func runAgentLifecycle(command *cli.Context, archive bool) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	if archive {
		response, err := client.ArchiveAgentWithResponse(command.Context(), command.Arg(0))
		if err != nil {
			return err
		}
		return writeNoContent(command, response.StatusCode(), response.Body, "archived", "agent", command.Arg(0))
	}
	response, err := client.RestoreAgentWithResponse(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeNoContent(command, response.StatusCode(), response.Body, "restored", "agent", command.Arg(0))
}

func runTurnStart(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	agent, err := client.Agents().GetByID(command.Context(), command.String("agent-id"))
	if err != nil {
		return err
	}
	options := nvoken.TurnOptions{TenantKey: command.String("tenant"), UserKey: command.String("user"), IdempotencyKey: command.String("idempotency-key")}
	if id := command.String("conversation-id"); id != "" {
		options.Conversation = nvoken.ContinueConversation(id)
	}
	switch command.String("memory-scope") {
	case "none":
		options.Memory = nvoken.NoneMemory()
	case "tenant":
		options.Memory = nvoken.TenantMemory(command.String("memory-namespace"))
	case "user":
		options.Memory = nvoken.UserMemory(command.String("memory-namespace"))
	}
	if command.Bool("wait") {
		result, err := agent.Run(command.Context(), command.Arg(0), options)
		if err != nil {
			return err
		}
		return renderJSON(command, result)
	}
	turn, err := agent.Start(command.Context(), command.Arg(0), options)
	if err != nil {
		return err
	}
	snapshot, err := turn.Status(command.Context())
	if err != nil {
		return err
	}
	return renderJSON(command, snapshot.Resource)
}

func runTurnGet(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	response, err := client.GetTurnWithResponse(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}

func runTurnResult(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	response, err := client.GetTurnResultWithResponse(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}

func runTurnList(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	statuses := make([]generated.TurnStatus, len(command.Strings("status")))
	for i, value := range command.Strings("status") {
		statuses[i] = generated.TurnStatus(value)
	}
	params := &generated.ListTurnsParams{TenantKey: optionalString(command.String("tenant")), UserKey: optionalString(command.String("user")), ConversationID: optionalString(command.String("conversation-id")), AgentID: optionalString(command.String("agent-id")), Cursor: optionalString(command.String("cursor")), Limit: optionalInt(command.Int("limit"))}
	if len(statuses) != 0 {
		params.Status = &statuses
	}
	response, err := client.ListTurnsWithResponse(command.Context(), params)
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}

func runTurnCancel(command *cli.Context) error    { return runTurnControl(command, "cancel") }
func runTurnInterrupt(command *cli.Context) error { return runTurnControl(command, "interrupt") }
func runTurnControl(command *cli.Context, action string) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	if action == "cancel" {
		response, err := client.CancelTurnWithResponse(command.Context(), command.Arg(0))
		if err != nil {
			return err
		}
		return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
	}
	response, err := client.InterruptTurnWithResponse(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}

func runTurnResume(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	var body generated.ResumeTurnRequest
	if err := readJSONFile(command.String("file"), &body); err != nil {
		return err
	}
	response, err := client.ResumeTurnWithResponse(command.Context(), command.Arg(0), body)
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON202)
}

func runConversationGet(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	response, err := client.GetConversationWithResponse(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}
func runConversationCreate(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	var body generated.CreateConversationRequest
	if err := readJSONFile(command.String("file"), &body); err != nil {
		return err
	}
	response, err := client.CreateConversationWithResponse(command.Context(), body)
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON201)
}
func runConversationDelete(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	response, err := client.DeleteConversationWithResponse(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeNoContent(command, response.StatusCode(), response.Body, "deleted", "conversation", command.Arg(0))
}

func runConversationList(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	var owner *generated.ConversationOwnerKind
	if value := command.String("owner"); value != "" {
		typed := generated.ConversationOwnerKind(value)
		owner = &typed
	}
	params := &generated.ListConversationsParams{TenantKey: optionalString(command.String("tenant")), UserKey: optionalString(command.String("user")), OwnerKind: owner, ConversationKey: optionalString(command.String("key")), Cursor: optionalString(command.String("cursor")), Limit: optionalInt(command.Int("limit"))}
	response, err := client.ListConversationsWithResponse(command.Context(), params)
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}

func runConversationMessages(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	response, err := client.ListConversationMessagesWithResponse(command.Context(), command.Arg(0), &generated.ListConversationMessagesParams{Cursor: optionalString(command.String("cursor")), Limit: optionalInt(command.Int("limit"))})
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}
func runConversationTranscript(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	response, err := client.GetConversationTranscriptWithResponse(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}

func runMemorySpaceGet(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	response, err := client.GetMemorySpaceWithResponse(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}
func runMemorySpaceDelete(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	response, err := client.DeleteMemorySpaceWithResponse(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeNoContent(command, response.StatusCode(), response.Body, "deleted", "memory-space", command.Arg(0))
}
func runMemorySpaceResolve(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	var body generated.ResolveMemorySpaceRequest
	if err := readJSONFile(command.String("file"), &body); err != nil {
		return err
	}
	response, err := client.ResolveMemorySpaceWithResponse(command.Context(), body)
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}
func runMemorySpaceList(command *cli.Context) error {
	client, err := rawRuntimeClient(command)
	if err != nil {
		return err
	}
	var scope *generated.MemoryScope
	if value := command.String("scope"); value != "" {
		typed := generated.MemoryScope(value)
		scope = &typed
	}
	params := &generated.ListMemorySpacesParams{TenantKey: optionalString(command.String("tenant")), UserKey: optionalString(command.String("user")), Scope: scope, Erased: optionalBool(command.Bool("erased")), Cursor: optionalString(command.String("cursor")), Limit: optionalInt(command.Int("limit"))}
	response, err := client.ListMemorySpacesWithResponse(command.Context(), params)
	if err != nil {
		return err
	}
	return writeResponse(command, response.StatusCode(), response.Body, response.JSON200)
}

func runtimeClient(command *cli.Context) (*nvoken.Client, error) {
	auth := authFor(command)
	if auth.APIKey == "" {
		return nil, errors.New("not authenticated; run `nvoken auth login`, pass --api-key, or set NVOKEN_API_KEY")
	}
	return nvoken.NewClient(auth.BaseURL, auth.APIKey, nvoken.WithHTTPClient(&http.Client{}))
}

func rawRuntimeClient(command *cli.Context) (*generated.ClientWithResponses, error) {
	auth := authFor(command)
	if auth.APIKey == "" {
		return nil, errors.New("not authenticated; run `nvoken auth login`, pass --api-key, or set NVOKEN_API_KEY")
	}
	return apiClient(auth, true)
}

func resolveBaseURL(explicit, configPath string) (string, error) {
	if explicit != "" {
		return strings.TrimRight(explicit, "/"), nil
	}
	path, err := resolveConfigPath(configPath)
	if err != nil {
		return "", err
	}
	if path == "" {
		return defaultBaseURL, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var config struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", err
	}
	if config.BaseURL == "" {
		return "", fmt.Errorf("config %s has no base_url", path)
	}
	return strings.TrimRight(config.BaseURL, "/"), nil
}

func resolveConfigPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil
	}
	path := filepath.Join(home, ".config", "nvoken", "config.json")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return "", nil
}

func readJSONFile(path string, target any) error {
	var reader io.Reader
	if path == "-" {
		reader = os.Stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		reader = file
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeResponse(command *cli.Context, status int, body []byte, value any) error {
	if status < 200 || status >= 300 {
		return responseError(status, body)
	}
	return renderJSON(command, value)
}

func writeNoContent(command *cli.Context, status int, body []byte, action, resource, id string) error {
	if status != http.StatusNoContent {
		return responseError(status, body)
	}
	return writeMutationReceipt(command, action, resource, id)
}

func writeMutationReceipt(command *cli.Context, action, resource, id string) error {
	return writeOutput(command, map[string]string{"action": action, "resource": resource, "id": id}, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s %s %s\n", action, resource, id)
		return err
	})
}

func writeOutput(command *cli.Context, value any, text func(io.Writer) error) error {
	if jsonOutput(command) {
		return renderJSON(command, value)
	}
	return text(command.Stdout())
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func optionalInt(value int) *int {
	if value == 0 {
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
func optionalArchiveStatus(value string) *nvoken.ArchiveStatus {
	if value == "" {
		return nil
	}
	status := nvoken.ArchiveStatus(value)
	return &status
}

func writeNextCursor(writer io.Writer, cursor *string) error {
	if cursor == nil || *cursor == "" {
		return nil
	}
	_, err := fmt.Fprintf(writer, "next cursor: %s\n", *cursor)
	return err
}

func optionalCallbackTimeout(command *cli.Context) *int64 {
	value := command.Int("callback-timeout")
	if value == 0 {
		return nil
	}
	result := int64(value)
	return &result
}

func randomKey() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("cli-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return "cli-" + hex.EncodeToString(value[:])
}

func ownerKind(value string) generated.AgentOwnerKind { return generated.AgentOwnerKind(value) }

func withTurnAccess(tenant, user string) generated.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		if tenant != "" {
			request.Header.Set("X-Nvoken-Tenant-Key", tenant)
		}
		if user != "" {
			request.Header.Set("X-Nvoken-User-Key", user)
		}
		return nil
	}
}
