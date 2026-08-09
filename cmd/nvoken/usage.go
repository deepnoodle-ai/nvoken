package main

import (
	"fmt"
	"io"
	"time"

	"github.com/deepnoodle-ai/wonton/cli"
	openapi_types "github.com/oapi-codegen/runtime/types"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
)

func registerUsageCommands(app *cli.App) {
	usage := app.Group("usage").Description("Read-side token and cost analytics")
	usage.Command("daily").
		Description("Daily usage buckets by app, provider, and model").
		Flags(
			cli.String("start-date").Help("Inclusive first UTC day (YYYY-MM-DD); defaults to 29 days before end-date"),
			cli.String("end-date").Help("Inclusive last UTC day (YYYY-MM-DD); defaults to today"),
			cli.String("tenant-key").Help("Return only usage for one host customer"),
			cli.String("user-key").Help("Return only usage for one host end user"),
			cli.String("group-by").Enum("tenant_key").Help("Split buckets by host customer"),
		).
		Run(runUsageDaily)
}

func runUsageDaily(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	params := &nvoken.GetDailyUsageParams{}
	if value := command.String("start-date"); value != "" {
		parsed, err := time.ParseInLocation(time.DateOnly, value, time.UTC)
		if err != nil {
			return fmt.Errorf("start-date must be a YYYY-MM-DD date")
		}
		params.StartDate = &openapi_types.Date{Time: parsed}
	}
	if value := command.String("end-date"); value != "" {
		parsed, err := time.ParseInLocation(time.DateOnly, value, time.UTC)
		if err != nil {
			return fmt.Errorf("end-date must be a YYYY-MM-DD date")
		}
		params.EndDate = &openapi_types.Date{Time: parsed}
	}
	params.TenantKey = optionalString(command.String("tenant-key"))
	params.UserKey = optionalString(command.String("user-key"))
	if value := command.String("group-by"); value != "" {
		groupBy := nvoken.GetDailyUsageParamsGroupBy(value)
		params.GroupBy = &groupBy
	}
	usage, err := client.GetDailyUsage(command.Context(), params)
	if err != nil {
		return err
	}
	return writeOutput(command, usage, func(writer io.Writer) error {
		for _, bucket := range usage.Items {
			customer := ""
			if bucket.TenantKey != nil {
				customer = fmt.Sprintf("\ttenant=%s", *bucket.TenantKey)
			}
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s%s\tinvocations=%d\tcalls=%d\tinput_tokens=%d\toutput_tokens=%d\tcost=%.4f\n",
				bucket.Day.Format(time.DateOnly),
				bucket.AppID,
				bucket.Provider,
				bucket.Model,
				customer,
				bucket.Invocations,
				bucket.ModelCalls,
				bucket.InputTokens,
				bucket.OutputTokens,
				bucket.EstimatedCostUsd,
			); err != nil {
				return err
			}
		}
		return nil
	})
}
