package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/deepnoodle-ai/wonton/cli"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

func registerUsageCommands(app *cli.App) {
	usage := app.Group("usage").Description("Read retained token, cost, activity, and reliability analytics")
	usage.Command("timeseries").
		Description("Read calendar-aligned usage buckets and optional top series").
		Flags(
			cli.String("start-at").Required().Help("Inclusive UTC RFC3339 reporting start"),
			cli.String("end-at").Required().Help("Exclusive UTC RFC3339 reporting end"),
			cli.String("interval").Required().Enum("day", "week", "month").Help("Calendar bucket interval"),
			cli.String("timezone").Help("IANA timezone used for bucket boundaries"),
			cli.String("app-id").Help("Filter to one App in the caller's reporting scope"),
			cli.String("tenant-key").Help("Filter to one host tenant"),
			cli.String("user-key").Help("Filter to one host end user"),
			cli.String("agent-id").Help("Filter to one Agent"),
			cli.String("provider").Help("Filter to one model provider"),
			cli.String("model").Help("Filter to one requested model"),
			cli.String("provider-key-source").Enum("caller_ephemeral", "app_byok", "tenant_byok", "platform", "config_byok").Help("Filter by the account source that paid for the model call"),
			cli.String("provider-key-id").Help("Filter by one reusable provider-key ID"),
			cli.String("credential-family-id").Help("Filter across all rotations of one credential family"),
			cli.String("authentication-method").Enum("api_key", "issuer_token", "client_token", "anonymous_token", "unknown").Help("Filter by caller authentication method"),
			cli.String("call-kind").Enum("generation", "compaction", "direct").Help("Filter by model-call kind"),
			cli.String("tool-name").Help("Filter by projected tool name"),
			cli.String("tool-mode").Enum("builtin", "host", "callback", "mcp").Help("Filter by tool execution mode"),
			cli.String("group-by").Enum("tenant_key", "agent_id", "model", "tool_name", "authentication_method").Help("Split each bucket into series"),
			cli.Int("top").Help("Keep the top series over the complete window"),
			cli.String("keys").Help("Comma-separated explicit series keys; mutually exclusive with --top"),
		).
		Run(runUsageTimeseries)
	usage.Command("breakdown").
		Description("Rank one usage dimension over a reporting window").
		Flags(
			cli.String("start-at").Required().Help("Inclusive UTC RFC3339 reporting start"),
			cli.String("end-at").Required().Help("Exclusive UTC RFC3339 reporting end"),
			cli.String("group-by").Required().Enum("app_id", "tenant_key", "user_key", "agent_id", "provider", "model", "provider_key_source", "provider_key_id", "credential_family_id", "authentication_method", "failure_class", "tool_name").Help("Dimension to rank"),
			cli.String("sort").Enum("model_cost", "model_calls", "invocations", "tool_calls").Help("Metric used to rank rows"),
			cli.String("app-id").Help("Filter to one App in the caller's reporting scope"),
			cli.String("tenant-key").Help("Filter to one host tenant"),
			cli.String("user-key").Help("Filter to one host end user"),
			cli.String("agent-id").Help("Filter to one Agent"),
			cli.String("provider").Help("Filter to one model provider"),
			cli.String("model").Help("Filter to one requested model"),
			cli.String("provider-key-source").Enum("caller_ephemeral", "app_byok", "tenant_byok", "platform", "config_byok").Help("Filter by the account source that paid for the model call"),
			cli.String("provider-key-id").Help("Filter by one reusable provider-key ID"),
			cli.String("credential-family-id").Help("Filter across all rotations of one credential family"),
			cli.String("authentication-method").Enum("api_key", "issuer_token", "client_token", "anonymous_token", "unknown").Help("Filter by caller authentication method"),
			cli.String("call-kind").Enum("generation", "compaction", "direct").Help("Filter by model-call kind"),
			cli.String("tool-name").Help("Filter by projected tool name"),
			cli.String("tool-mode").Enum("builtin", "host", "callback", "mcp").Help("Filter by tool execution mode"),
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runUsageBreakdown)
	usage.Command("records").
		Description("List retained content-free model call facts").
		Flags(
			cli.String("start-at").Required().Help("Inclusive UTC RFC3339 reporting start"),
			cli.String("end-at").Required().Help("Exclusive UTC RFC3339 reporting end"),
			cli.String("app-id").Help("Filter to one App in the caller's reporting scope"),
			cli.String("tenant-key").Help("Filter to one host tenant"),
			cli.String("user-key").Help("Filter to one host end user"),
			cli.String("agent-id").Help("Filter to one Agent"),
			cli.String("provider").Help("Filter to one model provider"),
			cli.String("model").Help("Filter to one requested model"),
			cli.String("provider-key-source").Enum("caller_ephemeral", "app_byok", "tenant_byok", "platform", "config_byok").Help("Filter by the account source that paid for the model call"),
			cli.String("provider-key-id").Help("Filter by one reusable provider-key ID"),
			cli.String("credential-family-id").Help("Filter across all rotations of one credential family"),
			cli.String("authentication-method").Enum("api_key", "issuer_token", "client_token", "anonymous_token", "unknown").Help("Filter by caller authentication method"),
			cli.String("call-kind").Enum("generation", "compaction", "direct").Help("Filter by model-call kind"),
			cli.String("tool-name").Help("Filter by projected tool name"),
			cli.String("tool-mode").Enum("builtin", "host", "callback", "mcp").Help("Filter by tool execution mode"),
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
			cli.String("format").Default("json").Enum("json", "csv").Help("Response format; CSV is written verbatim to stdout"),
		).
		Run(runUsageRecords)
}

func usageWindow(command *cli.Context) (time.Time, time.Time, error) {
	startAt, err := utcRFC3339(command.String("start-at"), "start-at")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	endAt, err := utcRFC3339(command.String("end-at"), "end-at")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return startAt, endAt, nil
}

func utcRFC3339(value, name string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be an RFC3339 timestamp", name)
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return time.Time{}, fmt.Errorf("%s must be UTC", name)
	}
	return parsed.UTC(), nil
}

type usageFilters struct {
	ProviderKeySource    *generated.ProviderKeySource
	ProviderKeyID        *string
	CredentialFamilyID   *string
	AuthenticationMethod *generated.AuthenticationMethod
	CallKind             *generated.ModelCallKind
	ToolName             *string
	ToolMode             *generated.ToolCallMode
}

func readUsageFilters(command *cli.Context) usageFilters {
	filters := usageFilters{
		ProviderKeyID:      optionalString(command.String("provider-key-id")),
		CredentialFamilyID: optionalString(command.String("credential-family-id")),
		ToolName:           optionalString(command.String("tool-name")),
	}
	if value := command.String("provider-key-source"); value != "" {
		typed := generated.ProviderKeySource(value)
		filters.ProviderKeySource = &typed
	}
	if value := command.String("authentication-method"); value != "" {
		typed := generated.AuthenticationMethod(value)
		filters.AuthenticationMethod = &typed
	}
	if value := command.String("call-kind"); value != "" {
		typed := generated.ModelCallKind(value)
		filters.CallKind = &typed
	}
	if value := command.String("tool-mode"); value != "" {
		typed := generated.ToolCallMode(value)
		filters.ToolMode = &typed
	}
	return filters
}

func runUsageTimeseries(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	startAt, endAt, err := usageWindow(command)
	if err != nil {
		return err
	}
	params := &nvoken.GetUsageTimeseriesParams{
		StartAt:   startAt,
		EndAt:     endAt,
		Interval:  nvoken.UsageInterval(command.String("interval")),
		Timezone:  optionalString(command.String("timezone")),
		AppID:     optionalString(command.String("app-id")),
		TenantKey: optionalString(command.String("tenant-key")),
		UserKey:   optionalString(command.String("user-key")),
		AgentID:   optionalString(command.String("agent-id")),
		Provider:  optionalString(command.String("provider")),
		Model:     optionalString(command.String("model")),
		Top:       optionalInt(command.Int("top")),
		Keys:      optionalString(command.String("keys")),
	}
	filters := readUsageFilters(command)
	params.ProviderKeySource = filters.ProviderKeySource
	params.ProviderKeyID = filters.ProviderKeyID
	params.CredentialFamilyID = filters.CredentialFamilyID
	params.AuthenticationMethod = filters.AuthenticationMethod
	params.CallKind = filters.CallKind
	params.ToolName = filters.ToolName
	params.ToolMode = filters.ToolMode
	if value := command.String("group-by"); value != "" {
		groupBy := nvoken.GetUsageTimeseriesParamsGroupBy(value)
		params.GroupBy = &groupBy
	}
	series, err := client.GetUsageTimeseries(command.Context(), params)
	if err != nil {
		return err
	}
	return writeOutput(command, series, func(writer io.Writer) error {
		for _, bucket := range series.Buckets {
			label := fmt.Sprintf("%s..%s", bucket.StartAt.Format(time.RFC3339), bucket.EndAt.Format(time.RFC3339))
			if bucket.SeriesKey != nil {
				label += "\t" + *bucket.SeriesKey
			}
			if err := writeUsageMetrics(writer, label, bucket.Metrics); err != nil {
				return err
			}
		}
		return nil
	})
}

func runUsageBreakdown(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	startAt, endAt, err := usageWindow(command)
	if err != nil {
		return err
	}
	params := &nvoken.GetUsageBreakdownParams{
		StartAt:   startAt,
		EndAt:     endAt,
		GroupBy:   nvoken.GetUsageBreakdownParamsGroupBy(command.String("group-by")),
		AppID:     optionalString(command.String("app-id")),
		TenantKey: optionalString(command.String("tenant-key")),
		UserKey:   optionalString(command.String("user-key")),
		AgentID:   optionalString(command.String("agent-id")),
		Provider:  optionalString(command.String("provider")),
		Model:     optionalString(command.String("model")),
		Cursor:    optionalString(command.String("cursor")),
		Limit:     optionalInt(command.Int("limit")),
	}
	filters := readUsageFilters(command)
	params.ProviderKeySource = filters.ProviderKeySource
	params.ProviderKeyID = filters.ProviderKeyID
	params.CredentialFamilyID = filters.CredentialFamilyID
	params.AuthenticationMethod = filters.AuthenticationMethod
	params.CallKind = filters.CallKind
	params.ToolName = filters.ToolName
	params.ToolMode = filters.ToolMode
	if value := command.String("sort"); value != "" {
		sortBy := nvoken.GetUsageBreakdownParamsSort(value)
		params.Sort = &sortBy
	}
	breakdown, err := client.GetUsageBreakdown(command.Context(), params)
	if err != nil {
		return err
	}
	return writeOutput(command, breakdown, func(writer io.Writer) error {
		for _, item := range breakdown.Items {
			if err := writeUsageMetrics(writer, item.Key, item.Metrics); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, breakdown.NextCursor)
	})
}

func runUsageRecords(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	startAt, endAt, err := usageWindow(command)
	if err != nil {
		return err
	}
	params := &nvoken.ListUsageRecordsParams{
		StartAt:   startAt,
		EndAt:     endAt,
		AppID:     optionalString(command.String("app-id")),
		TenantKey: optionalString(command.String("tenant-key")),
		UserKey:   optionalString(command.String("user-key")),
		AgentID:   optionalString(command.String("agent-id")),
		Provider:  optionalString(command.String("provider")),
		Model:     optionalString(command.String("model")),
		Cursor:    optionalString(command.String("cursor")),
		Limit:     optionalInt(command.Int("limit")),
	}
	filters := readUsageFilters(command)
	params.ProviderKeySource = filters.ProviderKeySource
	params.ProviderKeyID = filters.ProviderKeyID
	params.CredentialFamilyID = filters.CredentialFamilyID
	params.AuthenticationMethod = filters.AuthenticationMethod
	params.CallKind = filters.CallKind
	params.ToolName = filters.ToolName
	params.ToolMode = filters.ToolMode
	if command.String("format") == "csv" {
		if jsonOutput(command) {
			return fmt.Errorf("--format csv cannot be combined with --json or --output json")
		}
		format := nvoken.ListUsageRecordsParamsFormat("csv")
		params.Format = &format
		response, err := client.Raw().ListUsageRecordsWithResponse(command.Context(), params)
		if err != nil {
			return err
		}
		if response.StatusCode() != 200 {
			return responseError(response.StatusCode(), response.Body)
		}
		if _, err := command.Stdout().Write(response.Body); err != nil {
			return err
		}
		if cursor := strings.TrimSpace(response.HTTPResponse.Header.Get("X-Nvoken-Next-Cursor")); cursor != "" {
			_, err = fmt.Fprintf(command.Stderr(), "next_cursor\t%s\n", cursor)
			return err
		}
		return nil
	}
	records, err := client.ListUsageRecords(command.Context(), params)
	if err != nil {
		return err
	}
	return writeOutput(command, records, func(writer io.Writer) error {
		for _, record := range records.Items {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\n",
				record.ID,
				record.RequestedProvider,
				record.RequestedModel,
				record.CallKind,
				record.Status,
			); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, records.NextCursor)
	})
}

func writeUsageMetrics(writer io.Writer, label string, metrics nvoken.UsageMetrics) error {
	_, err := fmt.Fprintf(
		writer,
		"%s\tinvocations=%d\tmodel_calls=%d\tinput_tokens=%d\toutput_tokens=%d\ttool_calls=%d\tmodel_cost=%s %s\n",
		label,
		metrics.Activity.Invocations,
		metrics.Model.ModelCalls,
		metrics.Model.InputTokens,
		metrics.Model.OutputTokens,
		metrics.Tools.ToolCalls,
		metrics.Cost.ModelCost.Amount,
		metrics.Cost.ModelCost.Currency,
	)
	return err
}
