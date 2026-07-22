#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

nvoken_deploy_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
nvoken_repo_root="$(cd "${nvoken_deploy_dir}/../.." && pwd)"
nvoken_release_plan="${nvoken_deploy_dir}/release.tfplan"
trap 'rm -f "${nvoken_release_plan}"' EXIT

nvoken_target_schema_version=0
for nvoken_migration in "${nvoken_repo_root}"/internal/adapters/postgres/migrations/*.up.sql; do
  nvoken_migration_name="${nvoken_migration##*/}"
  nvoken_migration_prefix="${nvoken_migration_name%%_*}"
  nvoken_migration_version=$((10#${nvoken_migration_prefix}))
  if ((nvoken_migration_version > nvoken_target_schema_version)); then
    nvoken_target_schema_version="${nvoken_migration_version}"
  fi
done
if ((nvoken_target_schema_version == 0)); then
  echo "no embedded database migrations found" >&2
  exit 1
fi
TF_VAR_schema_version="${nvoken_target_schema_version}"
export TF_VAR_schema_version

for nvoken_command in gcloud git terraform; do
  if ! command -v "${nvoken_command}" >/dev/null 2>&1; then
    echo "required command not found: ${nvoken_command}" >&2
    exit 1
  fi
done

: "${TF_VAR_project_id:?set TF_VAR_project_id to the deployment project}"
: "${NVOKEN_TF_STATE_BUCKET:?set NVOKEN_TF_STATE_BUCKET to the protected Terraform state bucket}"

if [[ -z "${TF_VAR_image_tag:-}" ]]; then
  TF_VAR_image_tag="$(git -C "${nvoken_repo_root}" rev-parse HEAD)"
  export TF_VAR_image_tag
fi
case "${TF_VAR_image_tag}" in
  [Ll][Aa][Tt][Ee][Ss][Tt])
    echo "TF_VAR_image_tag must be immutable and must not be latest" >&2
    exit 1
    ;;
esac
if [[ "${NVOKEN_ALLOW_DIRTY_BUILD:-0}" != "1" ]] &&
  [[ -n "$(git -C "${nvoken_repo_root}" status --porcelain --untracked-files=normal)" ]]; then
  echo "the source tree is dirty; commit it or set NVOKEN_ALLOW_DIRTY_BUILD=1 deliberately" >&2
  exit 1
fi

if [[ -z "${TF_VAR_anthropic_api_key_secret_id:-}" && -z "${TF_VAR_openai_api_key_secret_id:-}" ]]; then
  echo "set TF_VAR_anthropic_api_key_secret_id, TF_VAR_openai_api_key_secret_id, or both" >&2
  exit 1
fi

if [[ -n "${TF_VAR_provider_credential_encryption_keys_secret_id:-}" && -z "${TF_VAR_provider_credential_active_key_id:-}" ]] ||
   [[ -z "${TF_VAR_provider_credential_encryption_keys_secret_id:-}" && -n "${TF_VAR_provider_credential_active_key_id:-}" ]]; then
  echo "set TF_VAR_provider_credential_encryption_keys_secret_id and TF_VAR_provider_credential_active_key_id together" >&2
  exit 1
fi

for nvoken_secret_id in "${TF_VAR_anthropic_api_key_secret_id:-}" "${TF_VAR_openai_api_key_secret_id:-}" "${TF_VAR_provider_credential_encryption_keys_secret_id:-}"; do
  if [[ -n "${nvoken_secret_id}" ]]; then
    gcloud secrets versions describe latest \
      --secret="${nvoken_secret_id}" \
      --project="${TF_VAR_project_id}" \
      --format='value(name)' >/dev/null
  fi
done

nvoken_environment="${TF_VAR_environment:-dev}"
nvoken_init_args=(
  -backend-config="bucket=${NVOKEN_TF_STATE_BUCKET}"
  -backend-config="prefix=nvoken/${nvoken_environment}"
)
nvoken_apply_args=()
if [[ "${NVOKEN_DEPLOY_AUTO_APPROVE:-0}" == "1" ]]; then
  nvoken_apply_args=(-auto-approve)
fi

"${nvoken_deploy_dir}/bootstrap-state.sh"
terraform -chdir="${nvoken_deploy_dir}" init -input=false -reconfigure "${nvoken_init_args[@]}"
terraform -chdir="${nvoken_deploy_dir}" validate

# Bootstrap the registry and least-privilege build identity before Cloud Build
# needs to push the immutable image.
terraform -chdir="${nvoken_deploy_dir}" apply \
  "${nvoken_apply_args[@]}" \
  -target=terraform_data.build_ready

nvoken_repository="$(terraform -chdir="${nvoken_deploy_dir}" output -raw artifact_repository)"
nvoken_image="${nvoken_repository}/nvokend:${TF_VAR_image_tag}"
nvoken_build_service_account="$(terraform -chdir="${nvoken_deploy_dir}" output -raw build_service_account_name)"
nvoken_build_source_bucket="$(terraform -chdir="${nvoken_deploy_dir}" output -raw build_source_bucket)"
gcloud builds submit "${nvoken_repo_root}" \
  --project="${TF_VAR_project_id}" \
  --region="${TF_VAR_region:-us-central1}" \
  --config="${nvoken_deploy_dir}/cloudbuild.yaml" \
  --service-account="${nvoken_build_service_account}" \
  --gcs-source-staging-dir="gs://${nvoken_build_source_bucket}/source" \
  --substitutions="_IMAGE=${nvoken_image},_BUILD_VERSION=${TF_VAR_image_tag}"

# Read the currently serving revision before the migration Job is updated. The
# schema label is written by every post-transition service revision. The first
# transition from an unlabeled revision requires one explicit operator value.
nvoken_service_name="${TF_VAR_name:-nvoken}-${nvoken_environment}"
nvoken_previous_image=""
if ! nvoken_previous_image="$(gcloud run services describe "${nvoken_service_name}" \
  --project="${TF_VAR_project_id}" \
  --region="${TF_VAR_region:-us-central1}" \
  --format='value(spec.template.spec.containers[0].image)' 2>/dev/null)"; then
  nvoken_previous_image=""
fi
if [[ -n "${nvoken_previous_image}" ]]; then
  nvoken_previous_schema_version="$(gcloud run services describe "${nvoken_service_name}" \
    --project="${TF_VAR_project_id}" \
    --region="${TF_VAR_region:-us-central1}" \
    --format='value(spec.template.metadata.labels.nvoken_schema_version)' 2>/dev/null || true)"
  if [[ -z "${nvoken_previous_schema_version}" ]]; then
    : "${NVOKEN_CURRENT_SCHEMA_VERSION:?set NVOKEN_CURRENT_SCHEMA_VERSION for the one-time unlabeled compatibility transition}"
    nvoken_previous_schema_version="${NVOKEN_CURRENT_SCHEMA_VERSION}"
  fi
  if [[ ! "${nvoken_previous_schema_version}" =~ ^[0-9]+$ ]]; then
    echo "current schema version must be a nonnegative integer" >&2
    exit 1
  fi
  TF_VAR_previous_build_version="${nvoken_previous_image}"
  TF_VAR_previous_schema_version="${nvoken_previous_schema_version}"
else
  TF_VAR_previous_build_version="none"
  TF_VAR_previous_schema_version=0
fi
TF_VAR_migration_mode="${NVOKEN_MIGRATION_MODE:-ordinary}"
export TF_VAR_previous_build_version TF_VAR_previous_schema_version TF_VAR_migration_mode

# Update only the release job and its prerequisites. The serving revision still
# points at the prior image until this exact image has migrated successfully.
terraform -chdir="${nvoken_deploy_dir}" apply \
  "${nvoken_apply_args[@]}" \
  -target=google_cloud_run_v2_job.migrate

nvoken_migration_job="$(terraform -chdir="${nvoken_deploy_dir}" output -raw migration_job_name)"
nvoken_region="${TF_VAR_region:-us-central1}"
gcloud run jobs execute "${nvoken_migration_job}" \
  --project="${TF_VAR_project_id}" \
  --region="${nvoken_region}" \
  --wait

# Only a successful migration reaches the full apply that updates service
# traffic. A failed gcloud job exits the script under set -e.
terraform -chdir="${nvoken_deploy_dir}" plan -out="${nvoken_release_plan}"
terraform -chdir="${nvoken_deploy_dir}" apply "${nvoken_release_plan}"

echo "released ${nvoken_image}"
echo "service URL: $(terraform -chdir="${nvoken_deploy_dir}" output -raw service_url)"
