package main

import (
	"fmt"
	"io"
	"strconv"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerSigningKeyCommands(app *cli.App) {
	keys := app.Group("signing-key").Description("Rotate an App's callback and webhook signing keys")
	keys.Command("list").
		Description("List receiver-facing signing key versions without their secrets").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		Run(runSigningKeyList)
	keys.Command("mint").
		Description("Mint the next signing key version and print its one-time secret").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		Flags(
			cli.String("purpose").Required().Enum("callback", "webhook").Help("Delivery class this version signs"),
			cli.Bool("activate").Help("Sign with the new version immediately; only for recovering a lost secret"),
		).Run(runSigningKeyMint)
	keys.Command("activate").
		Description("Move signing to a receiver-ready key version").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		AddArg(requiredArg("purpose", "Signing purpose: callback or webhook")).
		AddArg(requiredArg("version", "Positive signing key version")).
		Run(runSigningKeyActivate)
	keys.Command("retire").
		Description("Delete a superseded signing key version").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		AddArg(requiredArg("purpose", "Signing purpose: callback or webhook")).
		AddArg(requiredArg("version", "Positive signing key version")).
		Run(runSigningKeyRetire)
}

func runSigningKeyList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	list, err := client.ListAppSigningKeys(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, list, func(writer io.Writer) error {
		for _, key := range list.Items {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%d\tactive=%t\t%s\t%s\n",
				key.Purpose,
				key.Version,
				key.Active,
				key.KeyID,
				key.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func runSigningKeyMint(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	key, err := client.MintAppSigningKey(command.Context(), command.Arg(0), nvoken.MintAppSigningKeyInput{
		Purpose:  nvoken.AppSigningKeyPurpose(command.String("purpose")),
		Activate: command.Bool("activate"),
	})
	if err != nil {
		return err
	}
	// The secret is printed because this response is the only place it ever
	// exists. Configure your receiver with it before activating the version.
	return writeOutput(command, key, func(writer io.Writer) error {
		_, err := fmt.Fprintf(
			writer,
			"%s\t%d\tactive=%t\t%s\t%s\n",
			key.Purpose,
			key.Version,
			key.Active,
			key.KeyID,
			key.Secret,
		)
		return err
	})
}

func runSigningKeyActivate(command *cli.Context) error {
	purpose, version, err := signingKeyVersion(command)
	if err != nil {
		return err
	}
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	key, err := client.ActivateAppSigningKey(command.Context(), command.Arg(0), purpose, version)
	if err != nil {
		return err
	}
	return writeOutput(command, key, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "%s\t%d\tactive=%t\t%s\n", key.Purpose, key.Version, key.Active, key.KeyID)
		return err
	})
}

func runSigningKeyRetire(command *cli.Context) error {
	purpose, version, err := signingKeyVersion(command)
	if err != nil {
		return err
	}
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	if err := client.RetireAppSigningKey(command.Context(), command.Arg(0), purpose, version); err != nil {
		return err
	}
	retired := map[string]any{"purpose": string(purpose), "version": version, "retired": true}
	return writeOutput(command, retired, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "retired\t%s\t%d\n", purpose, version)
		return err
	})
}

func signingKeyVersion(command *cli.Context) (nvoken.AppSigningKeyPurpose, int64, error) {
	purpose := nvoken.AppSigningKeyPurpose(command.Arg(1))
	if !purpose.Valid() {
		return "", 0, fmt.Errorf("purpose must be callback or webhook, not %q", command.Arg(1))
	}
	version, err := strconv.ParseInt(command.Arg(2), 10, 64)
	if err != nil || version < 1 {
		return "", 0, fmt.Errorf("version must be a positive integer, not %q", command.Arg(2))
	}
	return purpose, version, nil
}
