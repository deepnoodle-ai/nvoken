package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/deepnoodle-ai/wonton/cli"

	"github.com/deepnoodle-ai/nvoken/internal/authstore"
	identityclient "github.com/deepnoodle-ai/nvoken/internal/gen/identity"
)

const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

func registerAuthCommands(app *cli.App) {
	group := app.Group("auth").Description("CLI authentication and local profiles")
	group.Command("login").Description("Authenticate through a browser").Flags(
		cli.String("label", "").Help("device label shown during approval"),
		cli.String("role-cap", "").Default("Operator").Enum("Operator", "Viewer").Help("maximum user credential role"),
		cli.String("tenant-ref", "").Help("optional tenant constraint"),
		cli.String("session-id", "").Help("optional Session constraint"),
		cli.Bool("default", "").Default(false).Help("make this profile the default"),
		cli.Bool("no-browser", "").Default(false).Help("do not open the browser automatically"),
	).Run(runAuthLogin)
	group.Command("status").Description("Verify and show active authentication").Use(requireAuth()).Run(runAuthStatus)
	group.Command("list").Description("List local profiles").Run(runAuthList)
	group.Command("use").Description("Select the default profile").AddArg(&cli.Arg{Name: "name", Required: true}).Run(runAuthUse)
	group.Command("logout").Description("Remove the selected local profile without remote revocation").Run(runAuthLogout)
	group.Command("revoke").Description("Revoke the selected credential and remove its local profile").Use(requireAuth()).Run(runAuthRevoke)
}

func runAuthLogin(ctx *cli.Context) error {
	auth := authFor(ctx)
	client, err := identityClient(auth, false)
	if err != nil {
		return err
	}
	label := strings.TrimSpace(ctx.String("label"))
	if label == "" {
		label = defaultDeviceLabel()
	}
	roleCap := identityclient.DeviceCodeRequestRoleCap(ctx.String("role-cap"))
	body := identityclient.DeviceCodeRequest{DeviceLabel: label, RoleCap: &roleCap}
	if value := strings.TrimSpace(ctx.String("tenant-ref")); value != "" {
		body.TenantKey = &value
	}
	if value := strings.TrimSpace(ctx.String("session-id")); value != "" {
		body.SessionId = &value
	}
	response, err := client.CreateDeviceCodeWithResponse(ctx.Context(), body)
	if err != nil {
		return fmt.Errorf("request device code: %w", err)
	}
	if response.JSON200 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	challenge := response.JSON200
	ctx.Printf("Verification code: %s\n", challenge.UserCode)
	ctx.Printf("Open: %s\n", challenge.VerificationUriComplete)
	if !ctx.Bool("no-browser") {
		if err := openBrowser(challenge.VerificationUriComplete); err != nil {
			ctx.Warn("could not open browser automatically: %s", err)
		}
	}
	ctx.Println("Waiting for approval...")
	deadline := time.Now().Add(time.Duration(challenge.ExpiresIn) * time.Second)
	interval := time.Duration(challenge.Interval) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	var token *identityclient.DeviceTokenResponse
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Context().Done():
			return ctx.Context().Err()
		case <-time.After(interval):
		}
		poll, err := client.ExchangeDeviceCodeWithFormdataBodyWithResponse(ctx.Context(), identityclient.ExchangeDeviceCodeFormdataRequestBody{
			DeviceCode: challenge.DeviceCode, GrantType: deviceGrantType,
		})
		if err != nil {
			return fmt.Errorf("poll device authorization: %w", err)
		}
		if poll.JSON200 != nil {
			token = poll.JSON200
			break
		}
		if poll.JSON400 == nil {
			return responseError(poll.StatusCode(), poll.Body)
		}
		switch poll.JSON400.Error {
		case identityclient.AuthorizationPending:
		case identityclient.SlowDown:
			interval += 5 * time.Second
		case identityclient.AccessDenied:
			return errors.New("device authorization was denied")
		case identityclient.ExpiredToken:
			return errors.New("device authorization expired; run `nvoken auth login` again")
		default:
			return fmt.Errorf("device authorization failed: %s", poll.JSON400.Error)
		}
	}
	if token == nil || token.AccessToken == nil {
		return errors.New("device authorization expired; run `nvoken auth login` again")
	}
	verifiedAuth := &resolvedAuth{BaseURL: auth.BaseURL, APIKey: *token.AccessToken}
	verifiedClient, err := identityClient(verifiedAuth, true)
	if err != nil {
		return err
	}
	verified, err := verifiedClient.GetCurrentAccountWithResponse(ctx.Context())
	if err != nil {
		return fmt.Errorf("verify issued credential: %w", err)
	}
	if verified.JSON200 == nil {
		return responseError(verified.StatusCode(), verified.Body)
	}
	current := verified.JSON200
	profile := authstore.Profile{
		Endpoint: auth.BaseURL, Token: *token.AccessToken, CredentialID: current.Authentication.CredentialId,
		AccountID: current.Id, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if current.Subject != nil {
		profile.SubjectID = current.Subject.Id
		profile.Subject = current.Subject.Subject
	}
	name := profileName(ctx)
	if err := authstore.PutProfile(name, profile, ctx.Bool("default")); err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	path, _ := authstore.Path()
	ctx.Success("Logged in. Profile %q saved to %s", name, path)
	return nil
}

func runAuthStatus(ctx *cli.Context) error {
	auth := authFor(ctx)
	client, err := identityClient(auth, true)
	if err != nil {
		return err
	}
	response, err := client.GetCurrentAccountWithResponse(ctx.Context())
	if err != nil {
		return err
	}
	if response.JSON200 == nil {
		return responseError(response.StatusCode(), response.Body)
	}
	if jsonOutput(ctx) {
		return renderJSON(ctx, response.JSON200)
	}
	account := response.JSON200
	ctx.Printf("Account: %s\n", account.Id)
	ctx.Printf("Credential: %s (%s)\n", account.Authentication.CredentialId, account.Authentication.CredentialKind)
	ctx.Printf("Effective profile: %s\n", account.Authentication.EffectiveProfile)
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
			profiles = append(profiles, map[string]any{"name": name, "default": profile.Default, "endpoint": profile.Endpoint, "credential_id": profile.CredentialID, "account_id": profile.AccountID, "created_at": profile.CreatedAt, "last_used_at": profile.LastUsedAt})
		}
		return renderJSON(ctx, map[string]any{"profiles": profiles})
	}
	for _, name := range names {
		profile := store.Profiles[name]
		marker := " "
		if profile.Default {
			marker = "*"
		}
		ctx.Printf("%s %s\t%s\t%s\n", marker, name, profile.Endpoint, profile.AccountID)
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
	client, err := identityClient(auth, true)
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

func defaultDeviceLabel() string {
	hostname, _ := os.Hostname()
	current, _ := user.Current()
	if current != nil && current.Username != "" && hostname != "" {
		return current.Username + "@" + hostname
	}
	if hostname != "" {
		return hostname
	}
	return runtime.GOOS + "-device"
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return command.Start()
}

func newIdempotencyKey() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "nvoken-cli-" + base64.RawURLEncoding.EncodeToString(raw), nil
}
