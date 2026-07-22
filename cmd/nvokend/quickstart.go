package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/deepnoodle-ai/nvoken/internal/daemon"
	"github.com/deepnoodle-ai/nvoken/internal/localrun"
)

func runQuickstart(ctx context.Context, arguments []string, output io.Writer) error {
	if len(arguments) == 1 && arguments[0] == "cleanup" {
		return localrun.Cleanup(ctx, nil, output)
	}
	flags := flag.NewFlagSet("nvokend quickstart", flag.ContinueOnError)
	flags.SetOutput(output)
	provider := flags.String("provider", "", "model provider: anthropic or openai")
	model := flags.String("model", "", "exact model ID available to the provider account")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(output, "usage: nvokend quickstart --provider <anthropic|openai> --model <model-id>")
		_, _ = fmt.Fprintln(output, "       nvokend quickstart cleanup")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: nvokend quickstart --provider <anthropic|openai> --model <model-id>")
	}

	result, err := localrun.Prepare(ctx, localrun.Options{
		Provider: *provider,
		Model:    *model,
		Output:   output,
	})
	if err != nil {
		return err
	}
	restore, err := applyQuickstartEnvironment(result.Environment)
	if err != nil {
		return err
	}
	defer restore()
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))
	defer slog.SetDefault(previousLogger)

	_, _ = fmt.Fprintln(output, "Applying database migrations...")
	migrationConfig, err := loadMigrationConfig()
	if err != nil {
		return err
	}
	migrationConfig.TargetBuildVersion = buildVersion
	if err := daemon.Migrate(ctx, migrationConfig); err != nil {
		return err
	}

	configuration, err := loadDaemonConfig()
	if err != nil {
		return err
	}
	configuration.BuildVersion = buildVersion
	_, _ = fmt.Fprintln(output, "Starting nvoken at http://localhost:8080. Press Ctrl-C to stop it.")
	writeQuickstartNextStep(output, buildVersion)
	return explainQuickstartDaemonError(daemon.Run(ctx, configuration))
}

func explainQuickstartDaemonError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "address already in use") {
		return errors.New("localhost port 8080 is already in use; stop the process using it and run nvokend quickstart again")
	}
	return err
}

func writeQuickstartNextStep(output io.Writer, version string) {
	if version == "devel" {
		_, _ = fmt.Fprintln(output, "Next: build and run the TypeScript SDK quickstart in another terminal.")
	} else {
		_, _ = fmt.Fprintln(output, "Next, open another terminal in this directory and run:")
		_, _ = fmt.Fprintf(output, "npx --yes --package \"@deepnoodle/nvoken@%s\" nvoken-quickstart\n", version)
	}
}

func applyQuickstartEnvironment(values map[string]string) (func(), error) {
	type previousValue struct {
		Value string
		Set   bool
	}
	previous := make(map[string]previousValue, len(values))
	restore := func() {
		for name, old := range previous {
			if old.Set {
				_ = os.Setenv(name, old.Value)
			} else {
				_ = os.Unsetenv(name)
			}
		}
	}
	for name, value := range values {
		old, set := os.LookupEnv(name)
		previous[name] = previousValue{
			Value: old,
			Set:   set,
		}
		if err := os.Setenv(name, value); err != nil {
			restore()
			return func() {}, fmt.Errorf("set quickstart environment %s: %w", name, err)
		}
	}
	return restore, nil
}
