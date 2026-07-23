package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerProviderCredentialCommands(app *cli.App) {
	credentials := app.Group("provider-credential").Description("Manage reusable model provider credentials")
	credentials.Command("list").
		Flags(
			cli.String("provider").Enum("anthropic", "openai").Help("Filter by model provider"),
			cli.String("scope").Enum("account", "tenant").Help("Filter by credential scope"),
			cli.String("status").Enum("active", "revoked").Help("Filter by root status"),
			cli.String("tenant").Help("Filter by tenant partition"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runProviderCredentialList)
	credentials.Command("create").
		Flags(
			cli.String("provider").Required().Enum("anthropic", "openai").Help("Model provider"),
			cli.String("scope").Required().Enum("account", "tenant").Help("Credential scope"),
			cli.String("tenant").Help("Tenant partition for tenant scope"),
			cli.String("idempotency-key").Required().Help("Stable lifecycle request identity"),
			cli.String("expires-at").Help("Optional RFC3339 expiry"),
			cli.String("api-key-env").Default("NVOKEN_PROVIDER_API_KEY").Help("Environment variable containing the provider API key; reads stdin when unset"),
		).
		Run(runProviderCredentialCreate)
	credentials.Command("get").Args("provider-credential-id").Run(runProviderCredentialGet)
	credentials.Command("rotate").
		Args("provider-credential-id").
		Flags(
			cli.String("idempotency-key").Required().Help("Stable lifecycle request identity"),
			cli.String("expires-at").Help("Optional RFC3339 expiry"),
			cli.Int("overlap-seconds").Help("Old-version overlap in seconds"),
			cli.String("api-key-env").Default("NVOKEN_PROVIDER_API_KEY").Help("Environment variable containing the provider API key; reads stdin when unset"),
		).
		Run(runProviderCredentialRotate)
	credentials.Command("revoke").Args("provider-credential-id").Run(runProviderCredentialRevoke)
}

func runProviderCredentialList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListProviderCredentials(command.Context(), nvoken.ListProviderCredentialsOptions{
		Provider:  optionalModelProvider(command.String("provider")),
		Scope:     optionalProviderCredentialScope(command.String("scope")),
		Status:    optionalProviderCredentialStatus(command.String("status")),
		TenantKey: optionalString(command.String("tenant")),
		Limit:     optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for index := range page.Items {
			if err := writeProviderCredentialText(writer, &page.Items[index]); err != nil {
				return err
			}
		}
		return nil
	})
}

func runProviderCredentialCreate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	apiKey, err := readProviderAPIKey(command)
	if err != nil {
		return err
	}
	expiresAt, err := optionalRFC3339(command.String("expires-at"))
	if err != nil {
		return err
	}
	credential, err := client.CreateProviderCredential(command.Context(), nvoken.CreateProviderCredentialInput{
		Provider:       nvoken.ModelProvider(command.String("provider")),
		Scope:          nvoken.ProviderCredentialScope(command.String("scope")),
		TenantKey:      optionalString(command.String("tenant")),
		APIKey:         apiKey,
		ExpiresAt:      expiresAt,
		IdempotencyKey: command.String("idempotency-key"),
	})
	if err != nil {
		return err
	}
	return writeProviderCredential(command, credential)
}

func runProviderCredentialGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	credential, err := client.GetProviderCredential(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeProviderCredential(command, credential)
}

func runProviderCredentialRotate(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	apiKey, err := readProviderAPIKey(command)
	if err != nil {
		return err
	}
	expiresAt, err := optionalRFC3339(command.String("expires-at"))
	if err != nil {
		return err
	}
	credential, err := client.RotateProviderCredential(command.Context(), command.Arg(0), nvoken.RotateProviderCredentialInput{
		APIKey:         apiKey,
		ExpiresAt:      expiresAt,
		OverlapSeconds: optionalInt(command.Int("overlap-seconds")),
		IdempotencyKey: command.String("idempotency-key"),
	})
	if err != nil {
		return err
	}
	return writeProviderCredential(command, credential)
}

func runProviderCredentialRevoke(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	credential, err := client.RevokeProviderCredential(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeProviderCredential(command, credential)
}

func writeProviderCredential(command *cli.Context, credential *nvoken.ProviderCredential) error {
	return writeOutput(command, credential, func(writer io.Writer) error {
		return writeProviderCredentialText(writer, credential)
	})
}

func writeProviderCredentialText(writer io.Writer, credential *nvoken.ProviderCredential) error {
	_, err := fmt.Fprintf(
		writer,
		"%s\t%s\t%s\t%s\t%d\t%s\n",
		credential.ID,
		credential.Provider,
		credential.Scope,
		credential.Status,
		credential.Version,
		credential.VersionStatus,
	)
	return err
}

func optionalModelProvider(value string) *nvoken.ModelProvider {
	if value == "" {
		return nil
	}
	provider := nvoken.ModelProvider(value)
	return &provider
}

func optionalProviderCredentialScope(value string) *nvoken.ProviderCredentialScope {
	if value == "" {
		return nil
	}
	scope := nvoken.ProviderCredentialScope(value)
	return &scope
}

func optionalProviderCredentialStatus(value string) *nvoken.ProviderCredentialStatus {
	if value == "" {
		return nil
	}
	status := nvoken.ProviderCredentialStatus(value)
	return &status
}

func optionalRFC3339(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("parse expires-at as RFC3339: %w", err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func readProviderAPIKey(command *cli.Context) (string, error) {
	environmentName := command.String("api-key-env")
	if apiKey := strings.TrimSpace(os.Getenv(environmentName)); apiKey != "" {
		return apiKey, nil
	}
	data, err := io.ReadAll(io.LimitReader(command.Stdin(), 1<<20))
	if err != nil {
		return "", fmt.Errorf("read provider API key from stdin: %w", err)
	}
	apiKey := strings.TrimSpace(string(data))
	if apiKey == "" {
		return "", fmt.Errorf("provider API key is required in %s or stdin", environmentName)
	}
	return apiKey, nil
}
