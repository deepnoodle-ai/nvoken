package main

import (
	"fmt"
	"io"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerTenantCommands(app *cli.App) {
	tenants := app.Group("tenant").Description("Inspect interned tenant partitions")
	tenants.Command("list").Description("List interned tenant partitions and their credit position").Flags(
		cli.String("tenant-key").Help("Limit to one exact tenant key"),
		cli.String("cursor").Help("Opaque continuation cursor"),
		cli.Int("limit").Help("Maximum page size"),
	).Run(runTenantList)
	tenants.Command("delete").
		Description("Delete a tenant that has never run work").
		AddArg(requiredArg("tenant-id", "Opaque non-default tenant ID")).
		Run(runTenantDelete)
}

func runTenantList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListTenants(command.Context(), &nvoken.ListTenantsParams{
		TenantKey: optionalString(command.String("tenant-key")),
		Cursor:    optionalString(command.String("cursor")),
		Limit:     optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, tenant := range page.Items {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s %s\t%s %s\n",
				tenant.ID,
				tenant.TenantKey,
				tenant.Credits.Available.Amount,
				tenant.Credits.Available.Currency,
				tenant.Credits.Used.Amount,
				tenant.Credits.Used.Currency,
			); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, page.NextCursor)
	})
}

func runTenantDelete(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	tenantID := command.Arg(0)
	if err := client.DeleteTenant(command.Context(), tenantID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "deleted", "tenant_id", tenantID)
}
