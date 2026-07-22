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
		"gcloud services enable storage.googleapis.com",
		"gcloud storage buckets describe gs://nvoken-test-state",
		"gcloud storage buckets update gs://nvoken-test-state",
		"terraform -chdir=", "init -input=false -reconfigure",
		"terraform -chdir=", "apply -auto-approve -target=terraform_data.build_ready",
		"gcloud builds submit", "--gcs-source-staging-dir=gs://example-project-nvoken-test-build-source/source",
		"apply -auto-approve -target=google_cloud_run_v2_job.migrate",
		"gcloud run jobs execute nvoken-test-migrate",
		"plan -out=",
		"apply ",
	)
	if strings.Contains(log, "output -raw region") {
		t.Fatalf("release queried an output that is absent after a first targeted apply:\n%s", log)
	}
	if !strings.Contains(log, "_BUILD_VERSION=immutable-test-tag") {
		t.Fatalf("release did not inject the immutable build version:\n%s", log)
	}
}

func TestBootstrapStateCreatesAndHardensMissingBucket(t *testing.T) {
	fakeBin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "commands.log")
	writeExecutable(t, fakeBin, "gcloud", `#!/usr/bin/env bash
printf 'gcloud %s\n' "$*" >>"${NVOKEN_TEST_COMMAND_LOG}"
if [[ "$*" == storage\ buckets\ describe* ]]; then exit 1; fi
exit 0
`)

	command := exec.Command("bash", filepath.Join(repoRoot(t), "deploy/google-cloud/bootstrap-state.sh"))
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+":/usr/bin:/bin",
		"NVOKEN_TEST_COMMAND_LOG="+logPath,
		"TF_VAR_project_id=example-project",
		"TF_VAR_region=us-east1",
		"NVOKEN_TF_STATE_BUCKET=nvoken-test-state",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap failed: %v\n%s", err, output)
	}
	log, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read command log: %v", readErr)
	}
	commands := string(log)
	assertOrdered(t, commands,
		"gcloud services enable storage.googleapis.com --project=example-project",
		"gcloud storage buckets describe gs://nvoken-test-state --project=example-project",
		"gcloud storage buckets create gs://nvoken-test-state --project=example-project --location=us-east1 --uniform-bucket-level-access --public-access-prevention",
		"gcloud storage buckets update gs://nvoken-test-state --project=example-project --uniform-bucket-level-access --public-access-prevention --versioning --update-labels=application=nvoken,usage=terraform-state",
	)
}

func TestBootstrapStateDoesNotRecreateExistingBucket(t *testing.T) {
	fakeBin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "commands.log")
	writeExecutable(t, fakeBin, "gcloud", `#!/usr/bin/env bash
printf 'gcloud %s\n' "$*" >>"${NVOKEN_TEST_COMMAND_LOG}"
exit 0
`)

	command := exec.Command("bash", filepath.Join(repoRoot(t), "deploy/google-cloud/bootstrap-state.sh"))
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+":/usr/bin:/bin",
		"NVOKEN_TEST_COMMAND_LOG="+logPath,
		"TF_VAR_project_id=example-project",
		"NVOKEN_TF_STATE_BUCKET=nvoken-test-state",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap failed: %v\n%s", err, output)
	}
	log, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read command log: %v", readErr)
	}
	commands := string(log)
	if strings.Contains(commands, "storage buckets create") {
		t.Fatalf("existing bucket was recreated:\n%s", commands)
	}
	if !strings.Contains(commands, "storage buckets update gs://nvoken-test-state") {
		t.Fatalf("existing bucket was not hardened:\n%s", commands)
	}
}

func TestPavedTerraformRequiresDatabaseTLSAndDedicatedMigrationIdentity(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/google-cloud/main.tf"))
	if err != nil {
		t.Fatalf("read Terraform root: %v", err)
	}
	configuration := string(contents)
	for _, required := range []string{
		`ssl_mode        = "ENCRYPTED_ONLY"`,
		`?sslmode=require`,
		`service_account = google_service_account.migrate.email`,
		`member    = "serviceAccount:${google_service_account.migrate.email}"`,
	} {
		if !strings.Contains(configuration, required) {
			t.Errorf("Terraform root does not contain %q", required)
		}
	}
}

func TestCloudBuildPinsLinuxAMD64WithoutBuildKitOnlyDockerfileArgs(t *testing.T) {
	buildConfig, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/google-cloud/cloudbuild.yaml"))
	if err != nil {
		t.Fatalf("read Cloud Build config: %v", err)
	}
	if !strings.Contains(string(buildConfig), `"--platform", "linux/amd64"`) {
		t.Fatal("Cloud Build must explicitly produce the paved linux/amd64 image")
	}
	if !strings.Contains(string(buildConfig), `NVOKEN_BUILD_VERSION=${_BUILD_VERSION}`) {
		t.Fatal("Cloud Build must pass the release identifier to the Docker build")
	}

	dockerfile, err := os.ReadFile(filepath.Join(repoRoot(t), "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if strings.Contains(string(dockerfile), "$BUILDPLATFORM") {
		t.Fatal("Dockerfile must parse without BuildKit-only automatic platform arguments")
	}
	if !strings.Contains(string(dockerfile), `-X main.buildVersion=${NVOKEN_BUILD_VERSION}`) {
		t.Fatal("Dockerfile must inject the build identifier into nvokend")
	}
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

func TestReleasePassesServingRevisionToMigrationPreflight(t *testing.T) {
	_, log := runReleaseWithCurrent(
		t,
		false,
		false,
		"us-central1-docker.pkg.dev/example-project/nvoken-test/nvokend:build-13",
		"13",
		"transition",
	)
	if !strings.Contains(log, "migration-vars current_image=us-central1-docker.pkg.dev/example-project/nvoken-test/nvokend:build-13 current_schema=13 target_schema=16 mode=transition") {
		t.Fatalf("migration preflight did not receive the release pair:\n%s", log)
	}
}

func TestReleaseLeavesPriorRevisionAfterSuccessfulMigrationAndFailedDeploy(t *testing.T) {
	_, log := runReleaseWithCurrent(
		t,
		false,
		true,
		"us-central1-docker.pkg.dev/example-project/nvoken-test/nvokend:build-14",
		"14",
		"ordinary",
	)
	assertOrdered(t, log,
		"gcloud run jobs execute nvoken-test-migrate",
		"plan -out=",
	)
	if strings.Contains(log, "apply "+filepath.Join(repoRoot(t), "deploy/google-cloud/release.tfplan")) {
		t.Fatalf("service apply ran after failed deploy plan:\n%s", log)
	}
}

func runRelease(t *testing.T, failMigration bool) (string, string) {
	t.Helper()
	return runReleaseWithCurrent(t, failMigration, false, "", "", "ordinary")
}

func runReleaseWithCurrent(
	t *testing.T,
	failMigration bool,
	failDeploy bool,
	currentImage string,
	currentSchema string,
	migrationMode string,
) (string, string) {
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
if [[ "$*" == *"apply -auto-approve -target=google_cloud_run_v2_job.migrate"* ]]; then
  printf 'migration-vars current_image=%s current_schema=%s target_schema=%s mode=%s\n' \
    "${TF_VAR_previous_build_version}" "${TF_VAR_previous_schema_version}" \
    "${TF_VAR_schema_version}" "${TF_VAR_migration_mode}" >>"${NVOKEN_TEST_COMMAND_LOG}"
fi
if [[ "${NVOKEN_TEST_FAIL_DEPLOY:-0}" == "1" && "$*" == *"plan -out="* ]]; then
  exit 8
fi
case "$*" in
  *"output -raw artifact_repository"*) echo 'us-central1-docker.pkg.dev/example-project/nvoken-test' ;;
  *"output -raw build_service_account_name"*) echo 'projects/example-project/serviceAccounts/nvoken-test-build@example-project.iam.gserviceaccount.com' ;;
  *"output -raw build_source_bucket"*) echo 'example-project-nvoken-test-build-source' ;;
  *"output -raw migration_job_name"*) echo 'nvoken-test-migrate' ;;
  *"output -raw region"*) echo 'region output must not be queried before the full apply' >&2; exit 7 ;;
  *"output -raw service_url"*) echo 'https://nvoken-test.example.run.app' ;;
esac
exit 0
`)
	writeExecutable(t, fakeBin, "gcloud", `#!/usr/bin/env bash
printf 'gcloud %s\n' "$*" >>"${NVOKEN_TEST_COMMAND_LOG}"
if [[ "$*" == run\ services\ describe*"containers[0].image"* ]]; then
  printf '%s\n' "${NVOKEN_TEST_CURRENT_IMAGE}"
  exit 0
fi
if [[ "$*" == run\ services\ describe*"nvoken_schema_version"* ]]; then
  printf '%s\n' "${NVOKEN_TEST_CURRENT_SCHEMA}"
  exit 0
fi
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
		"NVOKEN_TEST_FAIL_DEPLOY="+boolString(failDeploy),
		"NVOKEN_TEST_CURRENT_IMAGE="+currentImage,
		"NVOKEN_TEST_CURRENT_SCHEMA="+currentSchema,
		"NVOKEN_MIGRATION_MODE="+migrationMode,
		"TF_VAR_project_id=example-project",
		"TF_VAR_environment=test",
		"TF_VAR_image_tag=immutable-test-tag",
		"TF_VAR_openai_api_key_secret_id=nvoken-test-openai",
		"NVOKEN_TF_STATE_BUCKET=nvoken-test-state",
		"NVOKEN_DEPLOY_AUTO_APPROVE=1",
	)
	output, err := command.CombinedOutput()
	if failMigration || failDeploy {
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
