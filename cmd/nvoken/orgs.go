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
		Args("display-name").
		Flags(cli.String("external-ref").Help("Unique identity-provider Org identifier; makes registration idempotent")).
		Run(runOrgRegister)
	orgs.Command("get").Args("org-id").Run(runOrgGet)
	orgs.Command("list").
		Flags(cli.String("status").Enum("active", "archived", "all").Help("Filter by archive status; defaults to active")).
		Run(runOrgList)
	orgs.Command("update").Args("org-id", "display-name").Run(runOrgUpdate)
	orgs.Command("archive").Args("org-id").Run(runOrgArchive)
	orgs.Command("restore").Args("org-id").Run(runOrgRestore)
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
	return client.ArchiveOrg(command.Context(), command.Arg(0))
}

func runOrgRestore(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	return client.RestoreOrg(command.Context(), command.Arg(0))
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
