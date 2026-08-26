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

func registerProviderKeyCommands(app *cli.App) {
	providerKeys := app.Group("provider-key").Description("Manage reusable model provider keys")
	providerKeys.Command("list").
		Description("List reusable provider-key metadata without secret material").
		Flags(
			cli.String("provider").Help("Filter by installed canonical model provider"),
			cli.String("scope").Enum("app", "tenant").Help("Filter by provider-key scope"),
			cli.String("status").Enum("active", "revoked").Help("Filter by root status"),
			cli.String("tenant").Help("Filter by tenant partition"),
			cli.String("cursor").Help("Opaque continuation cursor"),
			cli.Int("limit").Help("Maximum page size"),
		).
		Run(runProviderKeyList)
	providerKeys.Command("create").
		Description("Store one encrypted reusable provider key").
		Flags(
			cli.String("provider").Required().Help("Installed canonical model provider"),
			cli.String("scope").Required().Enum("app", "tenant").Help("Provider-key scope"),
			cli.String("tenant").Help("Tenant partition for tenant scope"),
			cli.String("idempotency-key").Required().Help("Stable lifecycle request identity"),
			cli.String("expires-at").Help("Optional RFC3339 expiry"),
			cli.String("api-key-env").Default("NVOKEN_PROVIDER_API_KEY").Help("Environment variable containing the provider API key; reads stdin when unset"),
		).
		Run(runProviderKeyCreate)
	providerKeys.Command("get").
		Description("Read reusable provider-key metadata without its secret").
		AddArg(requiredArg("provider-key-id", "Opaque provider-key ID")).
		Run(runProviderKeyGet)
	providerKeys.Command("usage").
		Description("Read retained token and cost usage attributed to one provider key").
		AddArg(requiredArg("provider-key-id", "Opaque provider-key ID")).
		Run(runProviderKeyUsage)
	providerKeys.Command("rotate").
		Description("Replace a provider key with optional bounded overlap").
		AddArg(requiredArg("provider-key-id", "Opaque provider-key ID")).
		Flags(
			cli.String("idempotency-key").Required().Help("Stable lifecycle request identity"),
			cli.String("expires-at").Help("Optional RFC3339 expiry"),
			cli.Int("overlap-seconds").Help("Old-version overlap in seconds"),
			cli.String("api-key-env").Default("NVOKEN_PROVIDER_API_KEY").Help("Environment variable containing the provider API key; reads stdin when unset"),
		).
		Run(runProviderKeyRotate)
	providerKeys.Command("revoke").
		Description("Revoke a provider key and destroy its live encrypted versions").
		AddArg(requiredArg("provider-key-id", "Opaque provider-key ID")).
		Run(runProviderKeyRevoke)
}

func runProviderKeyList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	page, err := client.ListProviderKeys(command.Context(), nvoken.ListProviderKeysOptions{
		Provider:  optionalModelProvider(command.String("provider")),
		Scope:     optionalProviderKeyScope(command.String("scope")),
		Status:    optionalProviderKeyStatus(command.String("status")),
		TenantKey: optionalString(command.String("tenant")),
		Cursor:    optionalString(command.String("cursor")),
		Limit:     optionalInt(command.Int("limit")),
	})
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for index := range page.Items {
			if err := writeProviderKeyText(writer, &page.Items[index]); err != nil {
				return err
			}
		}
		return writeNextCursor(writer, page.NextCursor)
	})
}

func runProviderKeyCreate(command *cli.Context) error {
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
	providerKey, err := client.CreateProviderKey(command.Context(), nvoken.CreateProviderKeyInput{
		Provider:       nvoken.ModelProvider(command.String("provider")),
		Scope:          nvoken.ProviderKeyScope(command.String("scope")),
		TenantKey:      optionalString(command.String("tenant")),
		APIKey:         apiKey,
		ExpiresAt:      expiresAt,
		IdempotencyKey: command.String("idempotency-key"),
	})
	if err != nil {
		return err
	}
	return writeProviderKey(command, providerKey)
}

func runProviderKeyGet(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	providerKey, err := client.GetProviderKey(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeProviderKey(command, providerKey)
}

func runProviderKeyUsage(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	usage, err := client.GetProviderKeyUsage(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeOutput(command, usage, func(writer io.Writer) error {
		lastUsed := "never"
		if usage.LastUsedAt != nil {
			lastUsed = usage.LastUsedAt.UTC().Format(time.RFC3339)
		}
		inputTokens := 0
		outputTokens := 0
		if usage.Usage != nil {
			inputTokens = usage.Usage.InputTokens
			outputTokens = usage.Usage.OutputTokens
		}
		_, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%s\tinvocations=%d\tinput_tokens=%d\toutput_tokens=%d\tlast_used=%s\n",
			usage.ID,
			usage.Provider,
			usage.Scope,
			usage.Turns,
			inputTokens,
			outputTokens,
			lastUsed,
		)
		return err
	})
}

func runProviderKeyRotate(command *cli.Context) error {
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
	providerKey, err := client.RotateProviderKey(command.Context(), command.Arg(0), nvoken.RotateProviderKeyInput{
		APIKey:         apiKey,
		ExpiresAt:      expiresAt,
		OverlapSeconds: optionalInt(command.Int("overlap-seconds")),
		IdempotencyKey: command.String("idempotency-key"),
	})
	if err != nil {
		return err
	}
	return writeProviderKey(command, providerKey)
}

func runProviderKeyRevoke(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	providerKey, err := client.RevokeProviderKey(command.Context(), command.Arg(0))
	if err != nil {
		return err
	}
	return writeProviderKey(command, providerKey)
}

func writeProviderKey(command *cli.Context, providerKey *nvoken.ProviderKey) error {
	return writeOutput(command, providerKey, func(writer io.Writer) error {
		return writeProviderKeyText(writer, providerKey)
	})
}

func writeProviderKeyText(writer io.Writer, providerKey *nvoken.ProviderKey) error {
	_, err := fmt.Fprintf(
		writer,
		"%s\t%s\t%s\t%s\t%d\t%s\n",
		providerKey.ID,
		providerKey.Provider,
		providerKey.Scope,
		providerKey.Status,
		providerKey.Version,
		providerKey.VersionStatus,
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

func optionalProviderKeyScope(value string) *nvoken.ProviderKeyScope {
	if value == "" {
		return nil
	}
	scope := nvoken.ProviderKeyScope(value)
	return &scope
}

func optionalProviderKeyStatus(value string) *nvoken.ProviderKeyStatus {
	if value == "" {
		return nil
	}
	status := nvoken.ProviderKeyStatus(value)
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
