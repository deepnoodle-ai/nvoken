package main

import (
	"fmt"
	"io"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerOrgCommands(app *cli.App) {
	orgs := app.Group("org").Description("Manage customer ownership boundaries containing Apps")
	orgs.Command("register").
		Description("Register one customer Organization").
		AddArg(requiredArg("display-name", "Human-facing Organization name")).
		Flags(cli.String("external-ref").Help("Unique identity-provider Org identifier; makes registration idempotent")).
		Run(runOrgRegister)
	orgs.Command("get").
		Description("Read one Organization").
		AddArg(requiredArg("org-id", "Opaque Organization ID")).
		Run(runOrgGet)
	orgs.Command("list").
		Description("List Organizations visible to the current credential").
		Flags(cli.String("status").Enum("active", "archived", "all").Help("Filter by archive status; defaults to active")).
		Run(runOrgList)
	orgs.Command("update").
		Description("Replace an Organization's human-facing name").
		AddArg(requiredArg("org-id", "Opaque Organization ID")).
		AddArg(requiredArg("display-name", "Replacement human-facing name")).
		Run(runOrgUpdate)
	orgs.Command("archive").
		Description("Archive an Organization and refuse new Apps and Org credentials").
		AddArg(requiredArg("org-id", "Opaque Organization ID")).
		Run(runOrgArchive)
	orgs.Command("restore").
		Description("Restore an archived Organization").
		AddArg(requiredArg("org-id", "Opaque Organization ID")).
		Run(runOrgRestore)
}

func runOrgRegister(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	org, err := client.RegisterOrg(command.Context(), command.Arg(0), nvoken.RegisterOrgOptions{
		ExternalRef: optionalString(command.String("external-ref")),
	})
	if err != nil {
		return err
	}
	return writeOrg(command, org)
}

func runOrgGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	org, err := client.GetOrg(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOrg(command, org)
}

func runOrgList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	orgs, err := client.ListOrgs(command.Context(), nvoken.ListOrgsOptions{
		Status: optionalArchiveStatus(command.String("status")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, orgs, func(writer io.Writer) error {
		for index := range orgs.Items {
			if err := writeOrgText(writer, &orgs.Items[index]); err != nil {
				return err
			}
		}
		return nil
	})
}

func runOrgArchive(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	orgID := command.Arg(0)
	if err := client.ArchiveOrg(command.Context(), orgID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "archived", "org_id", orgID)
}

func runOrgRestore(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	orgID := command.Arg(0)
	if err := client.RestoreOrg(command.Context(), orgID); err != nil {
		return err
	}
	return writeMutationReceipt(command, "restored", "org_id", orgID)
}

func runOrgUpdate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	org, err := client.UpdateOrg(command.Context(), command.Arg(0), command.Arg(1))
	if err != nil {
		return err
	}
	return writeOrg(command, org)
}

func writeOrg(command *cli.Context, org *nvoken.Org) error {
	return writeOutput(command, org, func(writer io.Writer) error {
		return writeOrgText(writer, org)
	})
}

func writeOrgText(writer io.Writer, org *nvoken.Org) error {
	_, err := fmt.Fprintf(writer, "%s\t%s\n", org.ID, org.DisplayName)
	return err
}
