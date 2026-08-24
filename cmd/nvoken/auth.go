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
	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

func registerAuthCommands(app *cli.App) {
	group := app.Group("auth").Description("CLI authentication and local profiles")
	group.Command("login").Description("Authorize through the nvoken console and save a local profile").Flags(
		cli.Bool("default", "").Default(false).Help("Make this profile the default"),
		cli.String("console-url", "").Env("NVOKEN_CONSOLE_URL").Default(defaultConsoleURL).Help("nvoken console base URL"),
		cli.String("label", "").Help("Device name shown on the approval page; defaults to the hostname"),
		cli.Bool("no-browser", "").Default(false).Help("Print the approval URL without opening a browser"),
	).Run(runAuthLogin)
	group.Command("status").Description("Verify and show active authentication").Use(requireAuth()).Run(runAuthStatus)
	group.Command("list").Description("List local profiles").Run(runAuthList)
	group.Command("use").Description("Select the default profile").AddArg(requiredArg("name", "Local profile name")).Run(runAuthUse)
	group.Command("logout").Description("Remove the selected local profile without remote revocation").Run(runAuthLogout)
	group.Command("revoke").Description("Revoke the selected credential and remove its local profile").Use(requireAuth()).Run(runAuthRevoke)
}

// runAuthLogin is interactive unless this invocation explicitly received an
// API key by flag or environment. A saved default profile never silently turns
// a request to log into another Org into a verification of the old profile.
func runAuthLogin(ctx *cli.Context) error {
	auth := authFor(ctx)
	if auth.Source != authSourceOverride {
		return runDeviceAuthLogin(ctx)
	}
	return runAPIKeyAuthLogin(ctx, auth)
}

func runAPIKeyAuthLogin(ctx *cli.Context, auth *resolvedAuth) error {
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
	identity := *verified.JSON200
	profile := authstore.Profile{
		Endpoint:       auth.BaseURL,
		Token:          auth.APIKey,
		CredentialID:   credentialIDOrEmpty(identity.Authentication.CredentialID),
		AppID:          appIDOrEmpty(identity.Authentication.AppID),
		CredentialType: credentialTypeOrEmpty(identity.Authentication.Type),
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	name := profileName(ctx)
	if err := authstore.PutProfile(name, profile, ctx.Bool("default")); err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	path, _ := authstore.Path()
	if jsonOutput(ctx) {
		return renderJSON(ctx, map[string]any{
			"action":           "saved",
			"profile":          name,
			"endpoint":         profile.Endpoint,
			"credential_id":    profile.CredentialID,
			"app_id":           profile.AppID,
			"credential_type":  profile.CredentialType,
			"credentials_file": path,
		})
	}
	ctx.Success("Verified API key. Profile %q saved to %s", name, path)
	return nil
}

func appIDOrEmpty(value *generated.AppID) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func credentialTypeOrEmpty(value *generated.CredentialType) string {
	if value == nil {
		return ""
	}
	return string(*value)
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
	identity := *response.JSON200
	// One identity schema serves every caller kind, so `method` is what says
	// which fields to expect. Print it, then only the ones this caller has.
	ctx.Printf("Method: %s\n", identity.Authentication.Method)
	if id := credentialIDOrEmpty(identity.Authentication.CredentialID); id != "" {
		ctx.Printf("Credential: %s\n", id)
	}
	if credentialType := identity.Authentication.Type; credentialType != nil {
		ctx.Printf("Credential type: %s\n", *credentialType)
	}
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
			profiles = append(profiles, map[string]any{"name": name, "default": profile.Default, "endpoint": profile.Endpoint, "credential_id": profile.CredentialID, "app_id": profile.AppID, "app_name": profile.AppName, "credential_type": profile.CredentialType, "label": profile.Label, "created_at": profile.CreatedAt, "last_used_at": profile.LastUsedAt})
		}
		return renderJSON(ctx, map[string]any{"profiles": profiles})
	}
	for _, name := range names {
		profile := store.Profiles[name]
		marker := " "
		if profile.Default {
			marker = "*"
		}
		appName := profile.AppName
		if appName == "" {
			appName = "-"
		}
		ctx.Printf("%s %s\t%s\t%s\t%s\t%s\n", marker, name, appName, profile.CredentialType, profile.Endpoint, profile.CredentialID)
	}
	return nil
}

func runAuthUse(ctx *cli.Context) error {
	name := ctx.Args()[0]
	if err := authstore.SetDefault(name); err != nil {
		return err
	}
	if jsonOutput(ctx) {
		return renderJSON(ctx, map[string]string{"action": "selected", "profile": name})
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
	if jsonOutput(ctx) {
		return renderJSON(ctx, map[string]any{"action": "removed", "profile": name, "credential_revoked": false})
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
	if jsonOutput(ctx) {
		return renderJSON(ctx, map[string]string{
			"action":        "revoked",
			"credential_id": auth.Profile.CredentialID,
			"profile":       auth.Profile.Name,
		})
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

// credentialIDOrEmpty reads the credential this caller authenticated with. It
// is audience-restricted on the one identity schema: a machine credential
// carries it and a browser grant does not. The CLI is always a machine client,
// so absence means an endpoint that does not identify us, not a normal case.
func credentialIDOrEmpty(value *generated.CredentialID) string {
	if value == nil {
		return ""
	}
	return string(*value)
}
