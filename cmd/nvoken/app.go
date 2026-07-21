package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/deepnoodle-ai/wonton/cli"

	"github.com/deepnoodle-ai/nvoken/internal/authstore"
	identityclient "github.com/deepnoodle-ai/nvoken/internal/gen/identity"
)

const defaultBaseURL = "http://localhost:8080"

type authSource int

const (
	authSourceNone authSource = iota
	authSourceOverride
	authSourceProfile
)

type resolvedAuth struct {
	Source     authSource
	BaseURL    string
	BaseURLErr error
	APIKey     string
	Profile    *authstore.Profile
	Err        error
}

var (
	activeAuthMu sync.Mutex
	activeAuth   *resolvedAuth
)

func newApp() *cli.App {
	app := cli.New("nvoken").
		Description("Client for the nvoken durable agent runtime").
		Version(version).
		ExpandGroups(true).
		AddCompletionCommand()
	app.GlobalFlags(
		cli.String("base-url", "").Env("NVOKEN_BASE_URL").Help("nvoken API base URL"),
		cli.String("api-key", "").Env("NVOKEN_API_KEY").Help("machine or user API credential"),
		cli.String("config", "").Env("NVOKEN_CONFIG").Help("path to JSON config file"),
		cli.String("profile", "").Env("NVOKEN_PROFILE").Help("local credential profile"),
		cli.String("credentials-file", "").Env("NVOKEN_CREDENTIALS_FILE").Help("credentials file path"),
		cli.String("output", "o").Env("NVOKEN_OUTPUT").Default("text").Enum("text", "json").Help("output format"),
		cli.Bool("json").Help("emit stable JSON output (alias for --output json)"),
	)
	app.Use(authMiddleware())
	registerRuntimeCommands(app)
	registerAuthCommands(app)
	registerCredentialCommands(app)
	return app
}

func authMiddleware() cli.Middleware {
	return cli.Before(func(ctx *cli.Context) error {
		if path := ctx.String("credentials-file"); path != "" {
			authstore.SetPathOverride(path)
		}
		auth := resolveAuth(ctx)
		activeAuthMu.Lock()
		activeAuth = auth
		activeAuthMu.Unlock()
		if warning, err := authstore.PermissionWarning(); err == nil && warning != "" {
			_, _ = fmt.Fprintf(ctx.Stderr(), "nvoken: warning: credentials file %s\n", warning)
		}
		if auth.Source == authSourceProfile && auth.Profile != nil {
			if err := authstore.TouchProfile(auth.Profile.Name, time.Now().UTC().Format(time.RFC3339)); err != nil {
				_, _ = fmt.Fprintf(ctx.Stderr(), "nvoken: warning: update profile last-used: %v\n", err)
			}
		}
		return nil
	})
}

func resolveAuth(ctx *cli.Context) *resolvedAuth {
	result := &resolvedAuth{BaseURL: defaultBaseURL}
	profileName := ctx.String("profile")
	profile, profileErr := authstore.ResolveProfile(profileName)
	if profileErr == nil {
		result.Profile = profile
	}
	if ctx.IsSet("api-key") && ctx.String("api-key") != "" {
		result.Source = authSourceOverride
		result.APIKey = ctx.String("api-key")
	} else if profile != nil && profile.Token != "" {
		result.Source = authSourceProfile
		result.APIKey = profile.Token
	} else {
		result.Err = profileErr
	}
	if ctx.IsSet("base-url") && ctx.String("base-url") != "" {
		result.BaseURL = strings.TrimRight(ctx.String("base-url"), "/")
	} else if profile != nil && profile.Endpoint != "" {
		result.BaseURL = strings.TrimRight(profile.Endpoint, "/")
	} else {
		result.BaseURL, result.BaseURLErr = resolveBaseURL("", ctx.String("config"))
	}
	return result
}

func authFor(ctx *cli.Context) *resolvedAuth {
	activeAuthMu.Lock()
	defer activeAuthMu.Unlock()
	if activeAuth != nil {
		return activeAuth
	}
	return resolveAuth(ctx)
}

func requireAuth() cli.Middleware {
	return func(next cli.Handler) cli.Handler {
		return func(ctx *cli.Context) error {
			auth := authFor(ctx)
			if auth.APIKey == "" {
				if auth.Err != nil && !errors.Is(auth.Err, authstore.ErrNoDefaultProfile) {
					return auth.Err
				}
				return errors.New("not authenticated; run `nvoken auth login`, pass --api-key, or set NVOKEN_API_KEY")
			}
			return next(ctx)
		}
	}
}

func identityClient(auth *resolvedAuth, authenticated bool) (*identityclient.ClientWithResponses, error) {
	if auth.BaseURLErr != nil {
		return nil, auth.BaseURLErr
	}
	options := []identityclient.ClientOption{
		identityclient.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
	}
	if authenticated {
		options = append(options, identityclient.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
			request.Header.Set("Authorization", "Bearer "+auth.APIKey)
			return nil
		}))
	}
	return identityclient.NewClientWithResponses(auth.BaseURL, options...)
}

func renderJSON(ctx *cli.Context, value any) error {
	encoder := json.NewEncoder(ctx.Stdout())
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func jsonOutput(ctx *cli.Context) bool {
	return ctx.Bool("json") || ctx.String("output") == "json"
}

func responseError(status int, body []byte) error {
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Message != "" {
		if payload.Code != "" {
			return fmt.Errorf("%s: %s", payload.Code, payload.Message)
		}
		return errors.New(payload.Message)
	}
	return fmt.Errorf("nvoken API returned HTTP %d", status)
}

func profileName(ctx *cli.Context) string {
	if name := strings.TrimSpace(ctx.String("profile")); name != "" {
		return name
	}
	return "default"
}

func resetActiveAuth() {
	activeAuthMu.Lock()
	activeAuth = nil
	activeAuthMu.Unlock()
	if _, ok := os.LookupEnv("NVOKEN_CREDENTIALS_FILE"); !ok {
		authstore.SetPathOverride("")
	}
}
