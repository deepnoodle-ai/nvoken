package googlecloud_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseOrdersMigrationBeforeServiceApply(t *testing.T) {
	output, log := runRelease(t, false)
	if !strings.Contains(output, "released us-central1-docker.pkg.dev/example-project/nvoken-test/nvokend:immutable-test-tag") {
		t.Fatalf("release output = %s", output)
	}
	assertOrdered(t, log,
		"terraform -chdir=", "apply -auto-approve -target=terraform_data.build_ready",
		"gcloud builds submit",
		"apply -auto-approve -target=google_cloud_run_v2_job.migrate",
		"gcloud run jobs execute nvoken-test-migrate",
		"plan -out=",
		"apply ",
	)
}

func TestReleaseStopsBeforeServiceApplyWhenMigrationFails(t *testing.T) {
	_, log := runRelease(t, true)
	if strings.Contains(log, "terraform -chdir="+repoRoot(t)+"/deploy/google-cloud plan -out=") {
		t.Fatalf("service plan ran after migration failure:\n%s", log)
	}
	if !strings.Contains(log, "gcloud run jobs execute nvoken-test-migrate") {
		t.Fatalf("migration execution missing:\n%s", log)
	}
}

func runRelease(t *testing.T, failMigration bool) (string, string) {
	t.Helper()
	fakeBin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "commands.log")
	writeExecutable(t, fakeBin, "git", `#!/usr/bin/env bash
if [[ "$*" == *"status --porcelain"* ]]; then exit 0; fi
if [[ "$*" == *"rev-parse HEAD"* ]]; then echo immutable-test-tag; exit 0; fi
exit 0
`)
	writeExecutable(t, fakeBin, "terraform", `#!/usr/bin/env bash
printf 'terraform %s\n' "$*" >>"${NVOKEN_TEST_COMMAND_LOG}"
case "$*" in
  *"output -raw artifact_repository"*) echo 'us-central1-docker.pkg.dev/example-project/nvoken-test' ;;
  *"output -raw build_service_account_name"*) echo 'projects/example-project/serviceAccounts/nvoken-test-build@example-project.iam.gserviceaccount.com' ;;
  *"output -raw migration_job_name"*) echo 'nvoken-test-migrate' ;;
  *"output -raw region"*) echo 'us-central1' ;;
  *"output -raw service_url"*) echo 'https://nvoken-test.example.run.app' ;;
esac
exit 0
`)
	writeExecutable(t, fakeBin, "gcloud", `#!/usr/bin/env bash
printf 'gcloud %s\n' "$*" >>"${NVOKEN_TEST_COMMAND_LOG}"
if [[ "${NVOKEN_TEST_FAIL_MIGRATION:-0}" == "1" && "$*" == run\ jobs\ execute* ]]; then
  exit 9
fi
exit 0
`)

	command := exec.Command("bash", filepath.Join(repoRoot(t), "deploy/google-cloud/release.sh"))
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+":/usr/bin:/bin",
		"NVOKEN_TEST_COMMAND_LOG="+logPath,
		"NVOKEN_TEST_FAIL_MIGRATION="+boolString(failMigration),
		"TF_VAR_project_id=example-project",
		"TF_VAR_environment=test",
		"TF_VAR_image_tag=immutable-test-tag",
		"TF_VAR_openai_api_key_secret_id=nvoken-test-openai",
		"NVOKEN_TF_STATE_BUCKET=nvoken-test-state",
		"NVOKEN_DEPLOY_AUTO_APPROVE=1",
	)
	output, err := command.CombinedOutput()
	if failMigration {
		if err == nil {
			t.Fatalf("release succeeded despite migration failure: %s", output)
		}
	} else if err != nil {
		t.Fatalf("release failed: %v\n%s", err, output)
	}
	log, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read command log: %v", readErr)
	}
	return string(output), string(log)
}

func writeExecutable(t *testing.T, directory, name, contents string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func assertOrdered(t *testing.T, value string, fragments ...string) {
	t.Helper()
	position := 0
	for _, fragment := range fragments {
		next := strings.Index(value[position:], fragment)
		if next < 0 {
			t.Fatalf("%q missing after byte %d:\n%s", fragment, position, value)
		}
		position += next + len(fragment)
	}
}
