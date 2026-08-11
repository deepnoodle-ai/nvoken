package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/deepnoodle-ai/wonton/cli"

	"github.com/deepnoodle-ai/nvoken/internal/authstore"
)

func registerAuthCommands(app *cli.App) {
	group := app.Group("auth").Description("CLI authentication and local profiles")
	group.Command("login").Description("Verify an API key and save it as a local profile").Flags(
		cli.Bool("default", "").Default(false).Help("make this profile the default"),
	).Run(runAuthLogin)
	group.Command("status").Description("Verify and show active authentication").Use(requireAuth()).Run(runAuthStatus)
	group.Command("list").Description("List local profiles").Run(runAuthList)
	group.Command("use").Description("Select the default profile").AddArg(&cli.Arg{Name: "name", Required: true}).Run(runAuthUse)
	group.Command("logout").Description("Remove the selected local profile without remote revocation").Run(runAuthLogout)
	group.Command("revoke").Description("Revoke the selected credential and remove its local profile").Use(requireAuth()).Run(runAuthRevoke)
}

// runAuthLogin verifies the API key already resolved for this invocation
// (--api-key, NVOKEN_API_KEY, or the selected profile) against GET /v1/identity
// and records it as a named local profile. nvoken issues API keys through
// `nvoken credentials create`; there is no interactive login.
func runAuthLogin(ctx *cli.Context) error {
	auth := authFor(ctx)
	client, err := apiClient(auth, true)
	if err != nil {
		return err
	}
	verified, err := client.GetCurrentIdentityWithResponse(ctx.Context())
	if err != nil {
		return fmt.Errorf("verify API key: %w", err)
	}
	if verified.JSON200 == nil {
		return responseError(verified.StatusCode(), verified.Body)
	}
	identity, err := verified.JSON200.AsCurrentIdentity()
	if err != nil {
		return fmt.Errorf("decode identity: %w", err)
	}
	profile := authstore.Profile{
		Endpoint:     auth.BaseURL,
		Token:        auth.APIKey,
		CredentialID: identity.Authentication.CredentialID,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	name := profileName(ctx)
	if err := authstore.PutProfile(name, profile, ctx.Bool("default")); err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	path, _ := authstore.Path()
	ctx.Success("Verified API key. Profile %q saved to %s", name, path)
	return nil
}

func runAuthStatus(ctx *cli.Context) error {
	auth := authFor(ctx)
	client, err := apiClient(auth, true)
	if err != nil {
		return err
	}
	response, err := client.GetCurrentIdentityWithResponse(ctx.Context())
	if err != nil {
		return err
	}
	if response.JSON200 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	if jsonOutput(ctx) {
		return renderJSON(ctx, response.JSON200)
	}
	identity, err := response.JSON200.AsCurrentIdentity()
	if err != nil {
		return fmt.Errorf("decode identity: %w", err)
	}
	ctx.Printf("Credential: %s\n", identity.Authentication.CredentialID)
	ctx.Printf("Effective profile: %s\n", identity.Authentication.EffectiveProfile)
	ctx.Printf("Endpoint: %s\n", auth.BaseURL)
	if auth.Profile != nil {
		ctx.Printf("Local profile: %s\n", auth.Profile.Name)
	}
	return nil
}

func runAuthList(ctx *cli.Context) error {
	store, err := authstore.LoadStore()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(store.Profiles))
	for name := range store.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	if jsonOutput(ctx) {
		profiles := make([]map[string]any, 0, len(names))
		for _, name := range names {
			profile := store.Profiles[name]
			profiles = append(profiles, map[string]any{"name": name, "default": profile.Default, "endpoint": profile.Endpoint, "credential_id": profile.CredentialID, "created_at": profile.CreatedAt, "last_used_at": profile.LastUsedAt})
		}
		return renderJSON(ctx, map[string]any{"profiles": profiles})
	}
	for _, name := range names {
		profile := store.Profiles[name]
		marker := " "
		if profile.Default {
			marker = "*"
		}
		ctx.Printf("%s %s\t%s\t%s\n", marker, name, profile.Endpoint, profile.CredentialID)
	}
	return nil
}

func runAuthUse(ctx *cli.Context) error {
	name := ctx.Args()[0]
	if err := authstore.SetDefault(name); err != nil {
		return err
	}
	ctx.Success("Profile %q is now the default", name)
	return nil
}

func runAuthLogout(ctx *cli.Context) error {
	auth := authFor(ctx)
	name := profileName(ctx)
	if auth.Profile != nil {
		name = auth.Profile.Name
	}
	if err := authstore.DeleteProfile(name); err != nil {
		return err
	}
	ctx.Success("Removed local profile %q; the server credential was not revoked", name)
	return nil
}

func runAuthRevoke(ctx *cli.Context) error {
	auth := authFor(ctx)
	if auth.Profile == nil || auth.Profile.CredentialID == "" {
		return errors.New("auth revoke requires a saved profile; use `nvoken credentials revoke <id>` for an environment-backed credential")
	}
	client, err := apiClient(auth, true)
	if err != nil {
		return err
	}
	response, err := client.RevokeCredentialWithResponse(ctx.Context(), auth.Profile.CredentialID)
	if err != nil {
		return err
	}
	if response.JSON200 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	if err := authstore.DeleteProfile(auth.Profile.Name); err != nil {
		return fmt.Errorf("credential revoked, but remove local profile: %w", err)
	}
	ctx.Success("Revoked credential %s and removed profile %q", auth.Profile.CredentialID, auth.Profile.Name)
	return nil
}

func newIdempotencyKey() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "nvoken-cli-" + base64.RawURLEncoding.EncodeToString(raw), nil
}
