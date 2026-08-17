package main

import (
	"fmt"
	"io"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerProbeCommands(app *cli.App) {
	app.Command("health").
		Description("Ask a deployment whether its process is running").
		Long("Reads /health, which touches no dependency. Use it as a restart signal: a database being down is not a reason to kill the process. No credential is needed.").
		Run(runHealthProbe)
	app.Command("ready").
		Description("Ask a deployment whether it can serve requests").
		Long("Reads /ready, which answers only once Postgres has. Route traffic on this rather than on `nvoken health`. Exits non-zero when the deployment says no. No credential is needed.").
		Run(runReadinessProbe)
}

func runHealthProbe(command *cli.Context) error {
	return writeProbe(command, func(probe *nvoken.Probe) (nvoken.ProbeResult, error) {
		return probe.Health(command.Context())
	})
}

func runReadinessProbe(command *cli.Context) error {
	return writeProbe(command, func(probe *nvoken.Probe) (nvoken.ProbeResult, error) {
		return probe.Readiness(command.Context())
	})
}

// writeProbe runs one probe and reports it. A deployment that answers "not
// ready" is a successful probe of an unhealthy deployment, so it prints
// normally and exits non-zero — the shape a deploy script or a wait loop can
// branch on without parsing anything.
func writeProbe(command *cli.Context, ask func(*nvoken.Probe) (nvoken.ProbeResult, error)) error {
	auth := authFor(command)
	if auth.BaseURLErr != nil {
		return auth.BaseURLErr
	}
	probe, err := nvoken.NewProbe(auth.BaseURL)
	if err != nil {
		return err
	}
	result, err := ask(probe)
	if err != nil {
		return err
	}
	report := map[string]any{
		"base_url":   auth.BaseURL,
		"ready":      result.Ready,
		"status":     result.Status,
		"detail":     result.Detail,
		"latency_ms": result.Latency.Milliseconds(),
	}
	if err := writeOutput(command, report, func(writer io.Writer) error {
		answer := "not ready"
		if result.Ready {
			answer = "ready"
		}
		_, writeErr := fmt.Fprintf(
			writer,
			"%s\t%s\t%d\t%dms\n",
			auth.BaseURL,
			answer,
			result.Status,
			result.Latency.Milliseconds(),
		)
		return writeErr
	}); err != nil {
		return err
	}
	if !result.Ready {
		return &cli.ExitError{
			Code:    1,
			Message: fmt.Sprintf("%s answered %d", auth.BaseURL, result.Status),
		}
	}
	return nil
}
