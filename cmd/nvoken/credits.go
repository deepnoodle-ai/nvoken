package main

import (
	"fmt"
	"io"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerCreditCommands(app *cli.App) {
	credits := app.Group("credits").Description("View tenant credit accounts and add Credits")
	credits.Command("accounts").Description("List tenant credit accounts").Flags(
		cli.String("tenant-key").Help("Select one non-default tenant"),
		cli.Bool("default-tenant").Help("Select the App default tenant"),
		cli.String("cursor").Help("Opaque continuation cursor"),
		cli.Int("limit").Help("Maximum page size"),
	).Run(runCreditAccounts)
	credits.Command("allocations").Description("List append-only credit allocations").Flags(
		cli.String("tenant-key").Help("Select one non-default tenant"),
		cli.Bool("default-tenant").Help("Select the App default tenant"),
		cli.String("cursor").Help("Opaque continuation cursor"),
		cli.Int("limit").Help("Maximum page size"),
	).Run(runCreditAllocations)
	credits.Command("allocate").Description("Add non-expiring Credits to one tenant account").Flags(
		cli.String("amount").Required().Help("Exact positive USD amount with six fractional digits"),
		cli.String("tenant-key").Help("Select one non-default tenant"),
		cli.Bool("default-tenant").Help("Select the App default tenant"),
		cli.String("reference").Help("Host-owned correlation reference"),
		cli.String("idempotency-key").Required().Help("Stable allocation request identity"),
	).Run(runCreditAllocate)
}

func tenantSelector(command *cli.Context) (*string, *bool) {
	var defaultTenant *bool
	if command.Bool("default-tenant") {
		value := true
		defaultTenant = &value
	}
	return optionalString(command.String("tenant-key")), defaultTenant
}

func runCreditAccounts(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	tenantKey, defaultTenant := tenantSelector(command)
	list, err := client.ListCreditAccounts(command.Context(), &nvoken.ListCreditAccountsParams{
		TenantKey:     tenantKey,
		DefaultTenant: defaultTenant,
		Cursor:        optionalString(command.String("cursor")),
		Limit:         optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, list, func(writer io.Writer) error {
		for index := range list.Items {
			if err := writeCreditAccountText(writer, &list.Items[index]); err != nil {
				return err
			}
		}
		return nil
	})
}

func runCreditAllocations(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	tenantKey, defaultTenant := tenantSelector(command)
	list, err := client.ListCreditAllocations(command.Context(), &nvoken.ListCreditAllocationsParams{
		TenantKey:     tenantKey,
		DefaultTenant: defaultTenant,
		Cursor:        optionalString(command.String("cursor")),
		Limit:         optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, list, func(writer io.Writer) error {
		for _, allocation := range list.Items {
			tenant := "default"
			if allocation.TenantKey != nil {
				tenant = *allocation.TenantKey
			}
			reference := ""
			if allocation.Reference != nil {
				reference = *allocation.Reference
			}
			if _, err := fmt.Fprintf(writer, "%s\t%s\t%s %s\t%s\t%s\n", allocation.ID, tenant, allocation.Amount.Amount, allocation.Amount.Currency, allocation.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), reference); err != nil {
				return err
			}
		}
		return nil
	})
}

func runCreditAllocate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	tenantKey, defaultTenant := tenantSelector(command)
	result, err := client.AllocateCredits(command.Context(), nvoken.AllocateCreditsInput{
		Amount:         nvoken.Money{Amount: command.String("amount"), Currency: "USD"},
		TenantKey:      tenantKey,
		DefaultTenant:  defaultTenant,
		Reference:      optionalString(command.String("reference")),
		IdempotencyKey: command.String("idempotency-key"),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, result, func(writer io.Writer) error {
		if _, err := fmt.Fprintf(writer, "allocated\t%s\t%s %s\n", result.Allocation.ID, result.Allocation.Amount.Amount, result.Allocation.Amount.Currency); err != nil {
			return err
		}
		return writeCreditAccountText(writer, &result.Account)
	})
}

func writeCreditAccountText(writer io.Writer, account *nvoken.CreditAccount) error {
	tenant := "default"
	if account.TenantKey != nil {
		tenant = *account.TenantKey
	}
	_, err := fmt.Fprintf(
		writer,
		"%s\tavailable=%s\tallocated=%s\tused=%s\theld=%s\tpaused=%d\t%s\n",
		tenant,
		account.Available.Amount,
		account.Allocated.Amount,
		account.Used.Amount,
		account.Held.Amount,
		account.PausedInvocations,
		account.Available.Currency,
	)
	return err
}
