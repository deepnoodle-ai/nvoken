#!/usr/bin/env bash
set -Eeuo pipefail

nvoken_deploy_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for nvoken_command in gcloud terraform; do
  if ! command -v "${nvoken_command}" >/dev/null 2>&1; then
    echo "required command not found: ${nvoken_command}" >&2
    exit 1
  fi
done

if [[ ! "${NVOKEN_DISPATCH_SMOKE_TIMEOUT_SECONDS:-180}" =~ ^[1-9][0-9]*$ ]]; then
  echo "NVOKEN_DISPATCH_SMOKE_TIMEOUT_SECONDS must be a positive whole number" >&2
  exit 1
fi

nvoken_project="$(terraform -chdir="${nvoken_deploy_dir}" output -raw project_id)"
nvoken_region="$(terraform -chdir="${nvoken_deploy_dir}" output -raw region)"
nvoken_job="$(terraform -chdir="${nvoken_deploy_dir}" output -raw dispatch_smoke_job_name)"
nvoken_executor="$(terraform -chdir="${nvoken_deploy_dir}" output -raw executor_service_name)"

gcloud run jobs execute "${nvoken_job}" \
  --project="${nvoken_project}" \
  --region="${nvoken_region}" \
  --wait >/dev/null

nvoken_deadline=$((SECONDS + ${NVOKEN_DISPATCH_SMOKE_TIMEOUT_SECONDS:-180}))
nvoken_dispatch_id=""
while ((SECONDS < nvoken_deadline)); do
  nvoken_dispatch_id="$(gcloud logging read \
    "resource.type=\"cloud_run_job\" AND resource.labels.job_name=\"${nvoken_job}\" AND jsonPayload.message=\"created synthetic execution dispatch\"" \
    --project="${nvoken_project}" \
    --freshness=10m \
    --limit=1 \
    --order=desc \
    --format='value(jsonPayload.dispatch_id)' 2>/dev/null || true)"
  [[ -n "${nvoken_dispatch_id}" ]] && break
  sleep 2
done
if [[ -z "${nvoken_dispatch_id}" ]]; then
  echo "synthetic dispatch creation log did not become visible" >&2
  exit 1
fi

nvoken_outcome=""
while ((SECONDS < nvoken_deadline)); do
  nvoken_outcome="$(gcloud logging read \
    "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${nvoken_executor}\" AND jsonPayload.dispatch_id=\"${nvoken_dispatch_id}\" AND jsonPayload.message=\"execution dispatch attempt decided\"" \
    --project="${nvoken_project}" \
    --freshness=10m \
    --limit=1 \
    --order=desc \
    --format='value(jsonPayload.handler_outcome)' 2>/dev/null || true)"
  [[ -n "${nvoken_outcome}" ]] && break
  sleep 2
done
if [[ "${nvoken_outcome}" != "settled" ]]; then
  echo "dispatch ${nvoken_dispatch_id} did not reach durable synthetic settlement" >&2
  exit 1
fi

echo "Cloud Tasks dispatch smoke passed"
echo "project: ${nvoken_project}"
echo "region: ${nvoken_region}"
echo "dispatch: ${nvoken_dispatch_id}"
