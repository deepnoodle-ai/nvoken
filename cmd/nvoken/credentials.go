package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deepnoodle-ai/wonton/cli"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

func registerCredentialCommands(app *cli.App) {
	group := app.Group("credentials").Description("Machine credential lifecycle")
	group.Command("list").Description("List installation credentials").Use(requireAuth()).Flags(
		cli.String("status", "").Enum("active", "revoked").Help("Filter by credential status"),
		cli.String("cursor", "").Help("Opaque continuation cursor"),
		cli.Int("limit", "").Help("Maximum page size"),
	).Run(runCredentialList)
	group.Command("create").Description("Create a machine credential").Use(requireAuth()).Flags(
		cli.String("name", "").Required().Help("Human-facing credential name"),
		cli.String("type", "").Required().Enum("installation_admin", "app", "app_read_only").Help("Credential trust boundary"),
		cli.String("app-id", "").Help("Target App; required for App credential types"),
		cli.String("expires-at", "").Help("Optional RFC3339 expiry"),
		cli.String("idempotency-key", "").Help("Stable retry identity; generated when omitted"),
	).Run(runCredentialCreate)
	group.Command("get").Description("Read credential metadata").Use(requireAuth()).AddArg(requiredArg("id", "Opaque credential ID")).Run(runCredentialGet)
	group.Command("rotate").Description("Rotate a machine credential").Use(requireAuth()).Flags(
		cli.Duration("overlap", "").Default(0).Help("Bounded predecessor overlap; maximum 24h"),
		cli.String("idempotency-key", "").Help("Stable retry identity; generated when omitted"),
	).AddArg(requiredArg("id", "Opaque credential ID")).Run(runCredentialRotate)
	group.Command("revoke").Description("Revoke a credential").Use(requireAuth()).AddArg(requiredArg("id", "Opaque credential ID")).Run(runCredentialRevoke)
}

func credentialClient(ctx *cli.Context) (*generated.ClientWithResponses, error) {
	return apiClient(authFor(ctx), true)
}

func runCredentialList(ctx *cli.Context) error {
	client, err := credentialClient(ctx)
	if err != nil {
		return err
	}
	var status *generated.CredentialStatus
	if value := optionalString(ctx.String("status")); value != nil {
		typed := generated.CredentialStatus(*value)
		status = &typed
	}
	response, err := client.ListCredentialsWithResponse(ctx.Context(), &generated.ListCredentialsParams{
		Status: status,
		Cursor: optionalString(ctx.String("cursor")),
		Limit:  optionalInt(ctx.Int("limit")),
	})
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
		ctx.Printf("%s\t%s\t%s\t%s\n", credential.ID, credential.Type, credential.Status, credential.Name)
	}
	return writeNextCursor(ctx.Stdout(), response.JSON200.NextCursor)
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
	body := generated.CreateCredentialRequest{Name: name, Type: generated.CredentialType(ctx.String("type"))}
	if value := strings.TrimSpace(ctx.String("app-id")); value != "" {
		body.AppID = &value
	}
	if value := strings.TrimSpace(ctx.String("expires-at")); value != "" {
		expiresAt, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return fmt.Errorf("parse --expires-at: %w", err)
		}
		body.ExpiresAt = &expiresAt
	}
	key := strings.TrimSpace(ctx.String("idempotency-key"))
	if key == "" {
		key, err = newIdempotencyKey()
		if err != nil {
			return err
		}
	}
	response, err := client.CreateCredentialWithResponse(ctx.Context(), &generated.CreateCredentialParams{IdempotencyKey: key}, body)
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
	ctx.Printf("ID: %s\nType: %s\nName: %s\nPrefix: %s\nStatus: %s\n", response.JSON200.ID, response.JSON200.Type, response.JSON200.Name, response.JSON200.Prefix, response.JSON200.Status)
	return nil
}

func runCredentialRotate(ctx *cli.Context) error {
	client, err := credentialClient(ctx)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(ctx.String("idempotency-key"))
	if key == "" {
		key, err = newIdempotencyKey()
		if err != nil {
			return err
		}
	}
	overlap := ctx.Duration("overlap")
	if overlap < 0 || overlap > 24*time.Hour {
		return errors.New("--overlap must be between zero and 24h")
	}
	response, err := client.RotateCredentialWithResponse(ctx.Context(), ctx.Args()[0], &generated.RotateCredentialParams{IdempotencyKey: key}, generated.RotateCredentialJSONRequestBody{OverlapSeconds: int(overlap.Seconds())})
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
	ctx.Success("Revoked credential %s", response.JSON200.ID)
	return nil
}

func renderCredentialIssuance(ctx *cli.Context, issuance *generated.CredentialIssuance) error {
	if jsonOutput(ctx) {
		return renderJSON(ctx, issuance)
	}
	ctx.Printf("Credential: %s\n", issuance.Credential.ID)
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
