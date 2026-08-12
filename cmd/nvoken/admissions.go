package main

import (
	"fmt"
	"io"
	"time"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerAdmissionCommands(app *cli.App) {
	admissions := app.Group("admission").Description("Inspect admitted and refused turn attempts")
	admissions.Command("list").Flags(
		cli.String("outcome").Enum("admitted", "refused").Help("Limit to one outcome"),
		cli.String("error-code").Help("Limit to one refusal code"),
		cli.String("tenant-key").Help("Limit to one tenant key"),
		cli.String("user-key").Help("Limit to one end-user key"),
		cli.String("start-at").Help("Inclusive UTC RFC3339 lower bound"),
		cli.String("end-at").Help("Exclusive UTC RFC3339 upper bound"),
		cli.String("cursor").Help("Opaque continuation cursor"),
		cli.Int("limit").Help("Maximum page size"),
	).Run(runAdmissionList)
	admissions.Command("summary").Flags(
		cli.String("start-at").Help("Inclusive UTC RFC3339 lower bound; default 24 hours ago"),
		cli.String("end-at").Help("Exclusive UTC RFC3339 upper bound; default now"),
	).Run(runAdmissionSummary)
}

func runAdmissionList(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	startAt, err := optionalUTCRFC3339(command.String("start-at"), "start-at")
	if err != nil {
		return err
	}
	endAt, err := optionalUTCRFC3339(command.String("end-at"), "end-at")
	if err != nil {
		return err
	}
	params := &nvoken.ListAdmissionsParams{
		ErrorCode: optionalString(command.String("error-code")),
		TenantKey: optionalString(command.String("tenant-key")),
		UserKey:   optionalString(command.String("user-key")),
		StartAt:   startAt,
		EndAt:     endAt,
		Cursor:    optionalString(command.String("cursor")),
		Limit:     optionalInt(command.Int("limit")),
	}
	if value := command.String("outcome"); value != "" {
		outcome := nvoken.AdmissionOutcome(value)
		params.Outcome = &outcome
	}
	page, err := client.ListAdmissions(command.Context(), params)
	if err != nil {
		return err
	}
	return writeOutput(command, page, func(writer io.Writer) error {
		for _, attempt := range page.Items {
			errorCode := "-"
			if attempt.ErrorCode != nil {
				errorCode = string(*attempt.ErrorCode)
			}
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%d\t%s\n",
				attempt.AttemptedAt.UTC().Format(time.RFC3339Nano),
				attempt.ID,
				attempt.Outcome,
				attempt.HTTPStatus,
				errorCode,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func runAdmissionSummary(command *cli.Context) error {
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	startAt, err := optionalUTCRFC3339(command.String("start-at"), "start-at")
	if err != nil {
		return err
	}
	endAt, err := optionalUTCRFC3339(command.String("end-at"), "end-at")
	if err != nil {
		return err
	}
	summary, err := client.SummarizeAdmissions(command.Context(), &nvoken.SummarizeAdmissionsParams{
		StartAt: startAt,
		EndAt:   endAt,
	})
	if err != nil {
		return err
	}
	return writeOutput(command, summary, func(writer io.Writer) error {
		_, err := fmt.Fprintf(writer, "admitted\t%d\nrefused\t%d\n", summary.Admitted, summary.Refused)
		return err
	})
}

func optionalUTCRFC3339(value string, name string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := utcRFC3339(value, name)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
