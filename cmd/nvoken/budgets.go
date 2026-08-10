package main

import (
	"fmt"
	"io"
	"time"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerBudgetCommands(app *cli.App) {
	budgets := app.Group("budget").Description("Manage shared estimated-cost guardrails")
	budgets.Command("create").Flags(
		cli.String("scope").Required().Enum("app", "tenant", "user", "agent", "provider_key", "credential").Help("Budget scope"),
		cli.String("tenant-key").Help("Tenant key for tenant or user scope"),
		cli.Bool("default-tenant").Help("Select the App default tenant"),
		cli.String("user-key").Help("End-user key for user scope"),
		cli.String("agent-id").Help("Agent ID for agent scope"),
		cli.String("provider-key-id").Help("Stored provider key ID"),
		cli.String("credential-id").Help("API credential in the budgeted family"),
		cli.String("window-start").Required().Help("Inclusive UTC RFC3339 start"),
		cli.String("window-end").Required().Help("Exclusive UTC RFC3339 end"),
		cli.Float64("max-estimated-cost-usd").Required().Help("Maximum estimated list-price cost in USD"),
		cli.String("idempotency-key").Required().Help("Stable create request identity"),
	).Run(runBudgetCreate)
	budgets.Command("get").Args("budget-id").Run(runBudgetGet)
	budgets.Command("list").Flags(
		cli.String("scope").Enum("app", "tenant", "user", "agent", "provider_key", "credential").Help("Filter by Budget scope"),
		cli.String("status").Enum("upcoming", "active", "expired", "removed").Help("Filter by lifecycle status"),
		cli.String("tenant-key").Help("Filter by tenant key"),
		cli.String("user-key").Help("Filter by end-user key"),
		cli.String("agent-id").Help("Filter by Agent ID"),
		cli.String("provider-key-id").Help("Filter by stored provider key ID"),
		cli.String("credential-family-id").Help("Filter by credential rotation family"),
		cli.String("cursor").Help("Opaque continuation cursor"),
		cli.Int("limit").Help("Maximum page size"),
	).Run(runBudgetList)
	budgets.Command("update").Args("budget-id").Flags(
		cli.Float64("max-estimated-cost-usd").Required().Help("Replacement estimated cost cap in USD"),
	).Run(runBudgetUpdate)
	budgets.Command("delete").Args("budget-id").Run(runBudgetDelete)
}

func runBudgetCreate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	windowStart, err := utcRFC3339(command.String("window-start"), "window-start")
	if err != nil {
		return err
	}
	windowEnd, err := utcRFC3339(command.String("window-end"), "window-end")
	if err != nil {
		return err
	}
	var defaultTenant *bool
	if command.Bool("default-tenant") {
		value := true
		defaultTenant = &value
	}
	budget, err := client.CreateBudget(command.Context(), nvoken.CreateBudgetInput{
		Scope:               nvoken.BudgetScope(command.String("scope")),
		TenantKey:           optionalString(command.String("tenant-key")),
		DefaultTenant:       defaultTenant,
		UserKey:             optionalString(command.String("user-key")),
		AgentID:             optionalString(command.String("agent-id")),
		ProviderKeyID:       optionalString(command.String("provider-key-id")),
		CredentialID:        optionalString(command.String("credential-id")),
		WindowStart:         windowStart,
		WindowEnd:           windowEnd,
		MaxEstimatedCostUSD: command.Float64("max-estimated-cost-usd"),
		IdempotencyKey:      command.String("idempotency-key"),
	})
	if err != nil {
		return err
	}
	return writeBudget(command, budget)
}

func runBudgetGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	budget, err := client.GetBudget(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeBudget(command, budget)
}

func runBudgetList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	params := &nvoken.ListBudgetsParams{
		TenantKey:          optionalString(command.String("tenant-key")),
		UserKey:            optionalString(command.String("user-key")),
		AgentID:            optionalString(command.String("agent-id")),
		ProviderKeyID:      optionalString(command.String("provider-key-id")),
		CredentialFamilyID: optionalString(command.String("credential-family-id")),
		Cursor:             optionalString(command.String("cursor")),
		Limit:              optionalInt(command.Int("limit")),
	}
	if value := command.String("scope"); value != "" {
		scope := nvoken.BudgetScope(value)
		params.Scope = &scope
	}
	if value := command.String("status"); value != "" {
		status := nvoken.ListBudgetsParamsStatus(value)
		params.Status = &status
	}
	list, err := client.ListBudgets(command.Context(), params)
	if err != nil {
		return err
	}
	return writeOutput(command, list, func(writer io.Writer) error {
		for index := range list.Items {
			if err := writeBudgetText(writer, &list.Items[index]); err != nil {
				return err
			}
		}
		return nil
	})
}

func runBudgetUpdate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	budget, err := client.UpdateBudget(
		command.Context(),
		command.Arg(0),
		command.Float64("max-estimated-cost-usd"),
	)
	if err != nil {
		return err
	}
	return writeBudget(command, budget)
}

func runBudgetDelete(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	id := command.Arg(0)
	if err := client.DeleteBudget(command.Context(), id); err != nil {
		return err
	}
	return writeOutput(command, map[string]string{"deleted": id}, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "deleted\t%s\n", id)
		return err
	})
}

func writeBudget(command *cli.Context, budget *nvoken.Budget) error {
	return writeOutput(command, budget, func(writer io.Writer) error {
		return writeBudgetText(writer, budget)
	})
}

func writeBudgetText(writer io.Writer, budget *nvoken.Budget) error {
	_, err := fmt.Fprintf(
		writer,
		"%s\t%s\t%s..%s\tcap=%.6f\tspent=%.6f\treserved=%.6f\tavailable=%.6f\tpaused=%d\n",
		budget.ID,
		budget.Scope,
		budget.WindowStart.UTC().Format(time.RFC3339),
		budget.WindowEnd.UTC().Format(time.RFC3339),
		budget.MaxEstimatedCostUsd,
		budget.SpentEstimatedCostUsd,
		budget.ReservedEstimatedCostUsd,
		budget.AvailableEstimatedCostUsd,
		budget.PausedInvocations,
	)
	return err
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
