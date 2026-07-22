package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestQuickstartHelpIsSuccessful(t *testing.T) {
	var output bytes.Buffer
	if err := runQuickstart(context.Background(), []string{"--help"}, &output); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"--provider", "--model", "quickstart cleanup"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("help output is missing %q: %s", text, output.String())
		}
	}
}

func TestReleasedQuickstartPrintsMatchingPackage(t *testing.T) {
	var output bytes.Buffer
	writeQuickstartNextStep(&output, "0.1.1")
	if !strings.Contains(output.String(), `--package "@deepnoodle/nvoken@0.1.1" nvoken-quickstart`) {
		t.Fatalf("next step = %q", output.String())
	}
}

func TestExplainQuickstartDaemonErrorNamesBusyPort(t *testing.T) {
	err := explainQuickstartDaemonError(errors.New("listen tcp :8080: bind: address already in use"))
	if err == nil || err.Error() != "localhost port 8080 is already in use; stop the process using it and run nvokend quickstart again" {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyQuickstartEnvironmentRestoresProcess(t *testing.T) {
	t.Setenv("NVOKEN_QUICKSTART_EXISTING", "before")
	_ = os.Unsetenv("NVOKEN_QUICKSTART_NEW")
	restore, err := applyQuickstartEnvironment(map[string]string{
		"NVOKEN_QUICKSTART_EXISTING": "during",
		"NVOKEN_QUICKSTART_NEW":      "created",
	})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("NVOKEN_QUICKSTART_EXISTING") != "during" || os.Getenv("NVOKEN_QUICKSTART_NEW") != "created" {
		t.Fatal("quickstart environment was not applied")
	}
	restore()
	if os.Getenv("NVOKEN_QUICKSTART_EXISTING") != "before" {
		t.Fatal("existing environment was not restored")
	}
	if _, ok := os.LookupEnv("NVOKEN_QUICKSTART_NEW"); ok {
		t.Fatal("new environment was not removed")
	}
}
