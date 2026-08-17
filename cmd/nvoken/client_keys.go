package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerClientKeyCommands(app *cli.App) {
	keys := app.Group("client-key").Description("Manage App browser-client verification keys")
	keys.Command("list").
		Description("List an App's browser-client verification keys").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		Run(runClientKeyList)
	keys.Command("generate").
		Description("Generate an Ed25519 keypair and register its public half").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		Flags(
			cli.String("name").Required().Help("Operator-facing key name"),
			cli.Bool("no-register").Help("Print the keypair without registering it"),
		).Run(runClientKeyGenerate)
	keys.Command("create").
		Description("Register one Ed25519 browser-client verification key you already hold").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		Flags(
			cli.String("name").Required().Help("Operator-facing key name"),
			cli.String("public-key").Required().Help("Base64-encoded 32-byte Ed25519 public key"),
		).Run(runClientKeyCreate)
	keys.Command("revoke").
		Description("Revoke one browser-client verification key").
		AddArg(requiredArg("app-id", "Opaque App ID")).
		AddArg(requiredArg("key-id", "Browser-client key ID")).
		Run(runClientKeyRevoke)
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

// runClientKeyGenerate closes the gap that kept browser-direct access
// unadopted: `client-key create` requires a base64 Ed25519 public key, and
// nothing here produced one. The first step of the go-live path was "obtain an
// Ed25519 keypair by some means we do not describe".
//
// The private half is printed once and never leaves this process otherwise. It
// is the App's browser authority — whoever holds it can mint a grant for any
// end user — so it belongs in backend configuration and never in a bundle.
func runClientKeyGenerate(command *cli.Context) error {
	publicKey, seed, err := generateClientKeypair()
	if err != nil {
		return err
	}
	encodedPublic := base64.StdEncoding.EncodeToString(publicKey)

	generated := map[string]string{
		"public_key":  encodedPublic,
		"private_key": seed,
	}
	if command.Bool("no-register") {
		return writeOutput(command, generated, func(writer io.Writer) error {
			_, err := fmt.Fprintf(writer,
				"public_key\t%s\nprivate_key\t%s\n\nRegister the public half:\n  nvoken client-key create %s --name %q --public-key %s\n",
				encodedPublic, seed, command.Arg(0), command.String("name"), encodedPublic)
			return err
		})
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
	generated["key_id"] = key.ID
	generated["name"] = key.Name
	return writeOutput(command, generated, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer,
			"Registered %s (%s). Put these in your backend environment; the private key is shown only now.\n\n"+
				"NVOKEN_CLIENT_KEY_ID=%s\nNVOKEN_CLIENT_PRIVATE_KEY=%s\n",
			key.ID, key.Name, key.ID, seed)
		return err
	})
}

func generateClientKeypair() (ed25519.PublicKey, string, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate client keypair: %w", err)
	}
	// The seed, not the 64-byte expanded private key: it is what every
	// language's Ed25519 implementation accepts, and what the SDK mint helpers
	// document taking.
	return publicKey, base64.StdEncoding.EncodeToString(privateKey.Seed()), nil
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
