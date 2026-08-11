package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerClientKeyCommands(app *cli.App) {
	keys := app.Group("client-key").Description("Manage App browser-client verification keys")
	keys.Command("list").Args("app-id").Run(runClientKeyList)
	keys.Command("create").Args("app-id").Flags(
		cli.String("name").Required().Help("Operator-facing key name"),
		cli.String("public-key").Required().Help("Base64-encoded 32-byte Ed25519 public key"),
	).Run(runClientKeyCreate)
	keys.Command("revoke").Args("app-id", "key-id").Run(runClientKeyRevoke)
}

func runClientKeyList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	list, err := client.ListAppClientKeys(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, list, func(writer io.Writer) error {
		for _, key := range list.Items {
			if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", key.ID, key.Name, key.CreatedAt.Format("2006-01-02T15:04:05Z07:00")); err != nil {
				return err
			}
		}
		return nil
	})
}

func runClientKeyCreate(command *cli.Context) error {
	publicKey, err := base64.StdEncoding.DecodeString(command.String("public-key"))
	if err != nil || len(publicKey) != 32 {
		return errors.New("--public-key must be padded base64 for exactly 32 bytes")
	}
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	key, err := client.CreateAppClientKey(command.Context(), command.Arg(0), nvoken.CreateAppClientKeyInput{
		Name:      command.String("name"),
		PublicKey: publicKey,
	})
	if err != nil {
		return err
	}
	return writeOutput(command, key, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%s\n", key.ID, key.Name)
		return err
	})
}

func runClientKeyRevoke(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	if err := client.RevokeAppClientKey(command.Context(), command.Arg(0), command.Arg(1)); err != nil {
		return err
	}
	return writeOutput(command, map[string]string{"revoked": command.Arg(1)}, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "revoked\t%s\n", command.Arg(1))
		return err
	})
}
