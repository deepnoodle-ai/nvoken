package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

type appInitResult struct {
	BaseURL          string                       `json:"base_url"`
	App              *nvoken.App                  `json:"app,omitempty"`
	Credential       *nvoken.Credential           `json:"credential,omitempty"`
	CredentialSecret string                       `json:"credential_secret,omitempty"`
	SigningKeys      []nvoken.AppSigningKeySecret `json:"signing_keys,omitempty"`
	ClientKey        *nvoken.ClientKey            `json:"client_key,omitempty"`
	ClientPublicKey  string                       `json:"client_public_key,omitempty"`
	ClientPrivateKey string                       `json:"client_private_key,omitempty"`
	Environment      string                       `json:"environment"`
	Complete         bool                         `json:"complete"`
	FailedStage      string                       `json:"failed_stage,omitempty"`
}

type appInitBrowserOptions struct {
	Access        nvoken.BrowserAccess
	DefaultLimits nvoken.AppDefaultRateLimits
	ClientKeyName string
}

func runAppInit(command *cli.Context) error {
	browser, err := appInitBrowserOptionsFrom(command)
	if err != nil {
		return err
	}
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}

	name := strings.TrimSpace(command.Arg(0))
	registered, err := client.RegisterApp(command.Context(), name, nvoken.RegisterAppOptions{
		ExternalRef:            optionalString(command.String("external-ref")),
		DisplayName:            optionalString(command.String("display-name")),
		OrgID:                  optionalString(command.String("org-id")),
		CallbackTimeoutSeconds: optionalCallbackTimeout(command),
	})
	if err != nil {
		return fmt.Errorf("register App: %w", err)
	}
	result := &appInitResult{
		BaseURL:     authFor(command).BaseURL,
		App:         &registered.App,
		SigningKeys: append([]nvoken.AppSigningKeySecret(nil), registered.SigningKeys...),
	}
	if err := validateAppInitRegistration(registered); err != nil {
		return appInitFailed(command, result, "App registration response", err)
	}

	credentialName := strings.TrimSpace(command.String("credential-name"))
	if credentialName == "" {
		credentialName = name + " app"
	}
	appID := registered.App.ID
	issued, err := client.CreateCredential(command.Context(), nvoken.CreateCredentialInput{
		Name:  credentialName,
		Type:  nvoken.CredentialTypeApp,
		AppID: &appID,
	})
	if err != nil {
		return appInitFailed(command, result, "App credential issuance", err)
	}
	result.Credential = &issued.Credential
	result.CredentialSecret = issued.Secret

	if browser != nil {
		if _, err := client.UpdateApp(command.Context(), appID, nvoken.UpdateAppOptions{
			BrowserAccess:     &browser.Access,
			DefaultRateLimits: &browser.DefaultLimits,
		}); err != nil {
			return appInitFailed(command, result, "browser access configuration", err)
		}

		publicKey, seed, err := generateClientKeypair()
		if err != nil {
			return appInitFailed(command, result, "browser client-key generation", err)
		}
		result.ClientPublicKey = base64.StdEncoding.EncodeToString(publicKey)
		result.ClientPrivateKey = seed
		clientKey, err := client.CreateAppClientKey(command.Context(), appID, nvoken.CreateAppClientKeyInput{
			Name:      browser.ClientKeyName,
			PublicKey: publicKey,
		})
		if err != nil {
			return appInitFailed(command, result, "browser client-key registration", err)
		}
		result.ClientKey = clientKey
	}

	result.Complete = true
	result.Environment = result.envBlock()
	return writeAppInitResult(command, result)
}

func appInitBrowserOptionsFrom(command *cli.Context) (*appInitBrowserOptions, error) {
	if !command.Bool("browser") {
		for _, flag := range []string{
			"origin",
			"webhook-url",
			"client-key-name",
			"max-concurrent-invocations",
			"max-admissions-per-minute",
			"max-concurrent-invocations-per-tenant",
			"max-concurrent-invocations-per-user",
			"max-admissions-per-user-per-minute",
		} {
			if command.IsSet(flag) {
				return nil, fmt.Errorf("--%s requires --browser", flag)
			}
		}
		return nil, nil
	}

	origins := command.Strings("origin")
	for index := range origins {
		origins[index] = strings.TrimSpace(origins[index])
		if origins[index] == "" {
			return nil, errors.New("--origin cannot be empty")
		}
	}
	if len(origins) == 0 {
		return nil, errors.New("--browser requires at least one --origin")
	}
	webhookURL := strings.TrimSpace(command.String("webhook-url"))
	if webhookURL == "" {
		return nil, errors.New("--browser requires --webhook-url")
	}
	clientKeyName := strings.TrimSpace(command.String("client-key-name"))
	if clientKeyName == "" {
		return nil, errors.New("--client-key-name cannot be empty")
	}
	limits := []struct {
		name  string
		value int
	}{
		{"max-concurrent-invocations", command.Int("max-concurrent-invocations")},
		{"max-admissions-per-minute", command.Int("max-admissions-per-minute")},
		{"max-concurrent-invocations-per-tenant", command.Int("max-concurrent-invocations-per-tenant")},
		{"max-concurrent-invocations-per-user", command.Int("max-concurrent-invocations-per-user")},
		{"max-admissions-per-user-per-minute", command.Int("max-admissions-per-user-per-minute")},
	}
	for _, limit := range limits {
		if limit.value <= 0 {
			return nil, fmt.Errorf("--%s must be greater than zero", limit.name)
		}
	}

	return &appInitBrowserOptions{
		Access: nvoken.BrowserAccess{
			AllowedOrigins: origins,
			InvocationWebhook: nvoken.BrowserInvocationWebhook{
				URL: webhookURL,
			},
			Limits: nvoken.BrowserRateLimits{
				MaxAdmissionsPerUserPerMinute:     int64(command.Int("max-admissions-per-user-per-minute")),
				MaxConcurrentInvocationsPerTenant: int64(command.Int("max-concurrent-invocations-per-tenant")),
				MaxConcurrentInvocationsPerUser:   int64(command.Int("max-concurrent-invocations-per-user")),
			},
		},
		DefaultLimits: nvoken.AppDefaultRateLimits{
			MaxAdmissionsPerMinute:   int64(command.Int("max-admissions-per-minute")),
			MaxConcurrentInvocations: int64(command.Int("max-concurrent-invocations")),
		},
		ClientKeyName: clientKeyName,
	}, nil
}

func validateAppInitRegistration(registered *nvoken.AppRegistration) error {
	if registered == nil || strings.TrimSpace(registered.App.ID) == "" {
		return errors.New("nvoken returned App registration without an App ID")
	}
	seen := make(map[nvoken.AppSigningKeyPurpose]bool, len(registered.SigningKeys))
	for _, key := range registered.SigningKeys {
		if key.Purpose != nvoken.AppSigningKeyPurposeCallback && key.Purpose != nvoken.AppSigningKeyPurposeWebhook {
			return fmt.Errorf("nvoken returned unknown signing-key purpose %q", key.Purpose)
		}
		if seen[key.Purpose] {
			return fmt.Errorf("nvoken returned more than one %s signing key", key.Purpose)
		}
		if strings.TrimSpace(key.KeyID) == "" || key.Version < 1 || strings.TrimSpace(key.Secret) == "" {
			return fmt.Errorf("nvoken returned incomplete %s signing key", key.Purpose)
		}
		seen[key.Purpose] = true
	}
	for _, purpose := range []nvoken.AppSigningKeyPurpose{
		nvoken.AppSigningKeyPurposeCallback,
		nvoken.AppSigningKeyPurposeWebhook,
	} {
		if !seen[purpose] {
			return fmt.Errorf("nvoken returned no %s signing key", purpose)
		}
	}
	return nil
}

func appInitFailed(command *cli.Context, result *appInitResult, stage string, cause error) error {
	result.FailedStage = stage
	result.Environment = result.envBlock()
	if err := writeAppInitResult(command, result); err != nil {
		return errors.Join(fmt.Errorf("%s: %w", stage, cause), fmt.Errorf("write recovery environment: %w", err))
	}
	return fmt.Errorf("%s: %w; preserve the one-time values printed to stdout", stage, cause)
}

func writeAppInitResult(command *cli.Context, result *appInitResult) error {
	return writeOutput(command, result, func(writer io.Writer) error {
		if !result.Complete {
			if _, err := fmt.Fprintf(writer,
				"# nvoken app init stopped during %s.\n# Preserve these one-time values and inspect the App before continuing.\n",
				result.FailedStage,
			); err != nil {
				return err
			}
		}
		_, err := io.WriteString(writer, result.Environment)
		return err
	})
}

func (result *appInitResult) envBlock() string {
	var block strings.Builder
	if result.App != nil {
		fmt.Fprintf(&block, "# nvoken App %s\n", result.App.ID)
		writeEnvAssignment(&block, "NVOKEN_BASE_URL", result.BaseURL)
		writeEnvAssignment(&block, "NVOKEN_APP_ID", result.App.ID)
	}
	if result.CredentialSecret != "" {
		writeEnvAssignment(&block, "NVOKEN_API_KEY", result.CredentialSecret)
	}
	for _, key := range result.SigningKeys {
		prefix := "NVOKEN_" + strings.ToUpper(string(key.Purpose))
		writeEnvAssignment(&block, prefix+"_KEY_ID", key.KeyID)
		writeEnvAssignment(&block, prefix+"_KEY_VERSION", fmt.Sprintf("%d", key.Version))
		writeEnvAssignment(&block, prefix+"_SECRET", key.Secret)
	}
	if result.ClientKey != nil {
		writeEnvAssignment(&block, "NVOKEN_CLIENT_KEY_ID", result.ClientKey.ID)
	}
	if result.ClientPublicKey != "" && result.ClientKey == nil {
		writeEnvAssignment(&block, "NVOKEN_CLIENT_PUBLIC_KEY", result.ClientPublicKey)
	}
	if result.ClientPrivateKey != "" {
		writeEnvAssignment(&block, "NVOKEN_CLIENT_PRIVATE_KEY", result.ClientPrivateKey)
	}
	return block.String()
}

func writeEnvAssignment(writer io.Writer, name, value string) {
	_, _ = fmt.Fprintf(writer, "%s='%s'\n", name, strings.ReplaceAll(value, "'", "'\"'\"'"))
}
