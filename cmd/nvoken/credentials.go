package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deepnoodle-ai/wonton/cli"

	identityclient "github.com/deepnoodle-ai/nvoken/internal/gen/identity"
)

func registerCredentialCommands(app *cli.App) {
	group := app.Group("credentials").Description("Machine credential lifecycle")
	group.Command("list").Description("List Account credentials").Use(requireAuth()).Run(runCredentialList)
	group.Command("create").Description("Create a machine credential").Use(requireAuth()).Flags(
		cli.String("name", "").Help("credential name"),
		cli.String("credential-profile", "").Default("Runtime").Enum("Runtime", "Viewer", "Operator").Help("fixed authorization profile"),
		cli.String("tenant-ref", "").Help("optional tenant constraint"),
		cli.String("session-id", "").Help("optional Session constraint"),
		cli.Strings("operation", "").Help("repeatable operation constraint"),
		cli.String("expires-at", "").Help("optional RFC3339 expiry"),
	).Run(runCredentialCreate)
	group.Command("get").Description("Read credential metadata").Use(requireAuth()).AddArg(&cli.Arg{Name: "id", Required: true}).Run(runCredentialGet)
	group.Command("rotate").Description("Rotate a machine credential").Use(requireAuth()).Flags(
		cli.Duration("overlap", "").Default(0).Help("bounded predecessor overlap (maximum 24h)"),
	).AddArg(&cli.Arg{Name: "id", Required: true}).Run(runCredentialRotate)
	group.Command("revoke").Description("Revoke a credential").Use(requireAuth()).AddArg(&cli.Arg{Name: "id", Required: true}).Run(runCredentialRevoke)
}

func credentialClient(ctx *cli.Context) (*identityclient.ClientWithResponses, error) {
	return identityClient(authFor(ctx), true)
}

func runCredentialList(ctx *cli.Context) error {
	client, err := credentialClient(ctx)
	if err != nil {
		return err
	}
	response, err := client.ListCredentialsWithResponse(ctx.Context())
	if err != nil {
		return err
	}
	if response.JSON200 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	if jsonOutput(ctx) {
		return renderJSON(ctx, response.JSON200)
	}
	for _, credential := range response.JSON200.Items {
		profile := ""
		if credential.Profile != nil {
			profile = string(*credential.Profile)
		} else if credential.RoleCap != nil {
			profile = string(*credential.RoleCap) + " cap"
		}
		ctx.Printf("%s\t%s\t%s\t%s\t%s\n", credential.Id, credential.Kind, profile, credential.Status, credential.Name)
	}
	return nil
}

func runCredentialCreate(ctx *cli.Context) error {
	name := strings.TrimSpace(ctx.String("name"))
	if name == "" {
		return errors.New("--name is required")
	}
	client, err := credentialClient(ctx)
	if err != nil {
		return err
	}
	body := identityclient.CreateCredentialRequest{Name: name, Profile: identityclient.Profile(ctx.String("credential-profile"))}
	if value := strings.TrimSpace(ctx.String("tenant-ref")); value != "" {
		body.TenantRef = &value
	}
	if value := strings.TrimSpace(ctx.String("session-id")); value != "" {
		body.SessionId = &value
	}
	if values := ctx.Strings("operation"); len(values) > 0 {
		operations := make([]identityclient.Operation, len(values))
		for i, value := range values {
			operations[i] = identityclient.Operation(value)
		}
		body.Operations = &operations
	}
	if value := strings.TrimSpace(ctx.String("expires-at")); value != "" {
		expiresAt, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return fmt.Errorf("parse --expires-at: %w", err)
		}
		body.ExpiresAt = &expiresAt
	}
	key, err := newIdempotencyKey()
	if err != nil {
		return err
	}
	response, err := client.CreateCredentialWithResponse(ctx.Context(), &identityclient.CreateCredentialParams{IdempotencyKey: key}, body)
	if err != nil {
		return err
	}
	if response.JSON201 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	return renderCredentialIssuance(ctx, response.JSON201)
}

func runCredentialGet(ctx *cli.Context) error {
	client, err := credentialClient(ctx)
	if err != nil {
		return err
	}
	response, err := client.GetCredentialWithResponse(ctx.Context(), ctx.Args()[0])
	if err != nil {
		return err
	}
	if response.JSON200 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	if jsonOutput(ctx) {
		return renderJSON(ctx, response.JSON200)
	}
	ctx.Printf("ID: %s\nKind: %s\nName: %s\nPrefix: %s\nStatus: %s\n", response.JSON200.Id, response.JSON200.Kind, response.JSON200.Name, response.JSON200.Prefix, response.JSON200.Status)
	return nil
}

func runCredentialRotate(ctx *cli.Context) error {
	client, err := credentialClient(ctx)
	if err != nil {
		return err
	}
	key, err := newIdempotencyKey()
	if err != nil {
		return err
	}
	overlap := ctx.Duration("overlap")
	if overlap < 0 || overlap > 24*time.Hour {
		return errors.New("--overlap must be between zero and 24h")
	}
	response, err := client.RotateCredentialWithResponse(ctx.Context(), ctx.Args()[0], &identityclient.RotateCredentialParams{IdempotencyKey: key}, identityclient.RotateCredentialJSONRequestBody{OverlapSeconds: int(overlap.Seconds())})
	if err != nil {
		return err
	}
	if response.JSON201 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	return renderCredentialIssuance(ctx, response.JSON201)
}

func runCredentialRevoke(ctx *cli.Context) error {
	client, err := credentialClient(ctx)
	if err != nil {
		return err
	}
	response, err := client.RevokeCredentialWithResponse(ctx.Context(), ctx.Args()[0])
	if err != nil {
		return err
	}
	if response.JSON200 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	if jsonOutput(ctx) {
		return renderJSON(ctx, response.JSON200)
	}
	ctx.Success("Revoked credential %s", response.JSON200.Id)
	return nil
}

func renderCredentialIssuance(ctx *cli.Context, issuance *identityclient.CredentialIssuance) error {
	if jsonOutput(ctx) {
		return renderJSON(ctx, issuance)
	}
	ctx.Printf("Credential: %s\n", issuance.Credential.Id)
	ctx.Printf("Secret: %s\n", valueOrEmpty(issuance.Secret))
	ctx.Printf("Store this secret now; it cannot be read after %s.\n", issuance.DeliveryExpiresAt.Format(time.RFC3339))
	return nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
