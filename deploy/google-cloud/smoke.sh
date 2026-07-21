#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

nvoken_deploy_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for nvoken_command in curl gcloud jq terraform; do
  if ! command -v "${nvoken_command}" >/dev/null 2>&1; then
    echo "required command not found: ${nvoken_command}" >&2
    exit 1
  fi
done

: "${NVOKEN_SMOKE_PROVIDER:?set NVOKEN_SMOKE_PROVIDER to anthropic or openai}"
: "${NVOKEN_SMOKE_MODEL:?set NVOKEN_SMOKE_MODEL to a currently available model name}"

case "${NVOKEN_SMOKE_PROVIDER}" in
  anthropic | openai) ;;
  *)
    echo "NVOKEN_SMOKE_PROVIDER must be anthropic or openai" >&2
    exit 1
    ;;
esac

nvoken_project="$(terraform -chdir="${nvoken_deploy_dir}" output -raw project_id)"
nvoken_region="$(terraform -chdir="${nvoken_deploy_dir}" output -raw region)"
nvoken_service_name="$(terraform -chdir="${nvoken_deploy_dir}" output -raw service_name)"
nvoken_service_url="$(terraform -chdir="${nvoken_deploy_dir}" output -raw service_url)"
nvoken_runtime_secret="$(terraform -chdir="${nvoken_deploy_dir}" output -raw runtime_api_key_secret_id)"
nvoken_runtime_api_key="$(gcloud secrets versions access latest --secret="${nvoken_runtime_secret}" --project="${nvoken_project}")"

nvoken_response_file="$(mktemp "${TMPDIR:-/tmp}/nvoken-smoke-response.XXXXXX")"
nvoken_curl_config="$(mktemp "${TMPDIR:-/tmp}/nvoken-smoke-curl.XXXXXX")"
chmod 600 "${nvoken_curl_config}"
printf 'header = "Authorization: Bearer %s"\n' "${nvoken_runtime_api_key}" >"${nvoken_curl_config}"
unset nvoken_runtime_api_key
trap 'rm -f "${nvoken_response_file}" "${nvoken_curl_config}"' EXIT

if [[ ! "${NVOKEN_SMOKE_TIMEOUT_SECONDS:-300}" =~ ^[1-9][0-9]*$ ]]; then
  echo "NVOKEN_SMOKE_TIMEOUT_SECONDS must be a positive whole number" >&2
  exit 1
fi

curl --silent --show-error --fail-with-body "${nvoken_service_url}/healthz" >/dev/null

nvoken_smoke_id="$(date -u +%Y%m%dT%H%M%SZ)-${RANDOM}"
nvoken_request_body="$(jq -n \
  --arg provider "${NVOKEN_SMOKE_PROVIDER}" \
  --arg model "${NVOKEN_SMOKE_MODEL}" \
  --arg key "${nvoken_smoke_id}" \
  '{
    agent_ref: "cloud-run-smoke",
    session_key: ("cloud-run-smoke:" + $key),
    idempotency_key: ("cloud-run-smoke:" + $key),
    input: {content: [{type: "text", text: "Reply briefly to confirm the nvoken deployment is working."}]},
    spec: {
      instructions: "You are a concise deployment smoke-test agent.",
      model: {provider: $provider, name: $model}
    }
  }')"

nvoken_status_code="$(curl --silent --show-error \
  --output "${nvoken_response_file}" \
  --write-out '%{http_code}' \
  --request POST "${nvoken_service_url}/v1/invocations" \
  --config "${nvoken_curl_config}" \
  --header 'Content-Type: application/json' \
  --data "${nvoken_request_body}")"
if [[ "${nvoken_status_code}" != "202" ]]; then
  echo "admission returned HTTP ${nvoken_status_code}" >&2
  jq . "${nvoken_response_file}" >&2 || true
  exit 1
fi

nvoken_invocation_id="$(jq -er '.invocation_id' "${nvoken_response_file}")"
nvoken_deadline=$((SECONDS + ${NVOKEN_SMOKE_TIMEOUT_SECONDS:-300}))
nvoken_status=""

while ((SECONDS < nvoken_deadline)); do
  curl --silent --show-error --fail-with-body \
    --output "${nvoken_response_file}" \
    --config "${nvoken_curl_config}" \
    "${nvoken_service_url}/v1/invocations/${nvoken_invocation_id}"
  nvoken_status="$(jq -er '.status' "${nvoken_response_file}")"
  case "${nvoken_status}" in
    completed) break ;;
    failed | cancelled)
      echo "Invocation ${nvoken_invocation_id} settled as ${nvoken_status}" >&2
      jq . "${nvoken_response_file}" >&2
      exit 1
      ;;
  esac
  sleep 2
done

if [[ "${nvoken_status}" != "completed" ]]; then
  echo "Invocation ${nvoken_invocation_id} did not complete before the smoke timeout" >&2
  exit 1
fi

# A second authoritative read proves the smoke result is durable, not a local
# response object retained by the admission request.
curl --silent --show-error --fail-with-body \
  --output "${nvoken_response_file}" \
  --config "${nvoken_curl_config}" \
  "${nvoken_service_url}/v1/invocations/${nvoken_invocation_id}"
jq -e '.status == "completed"' "${nvoken_response_file}" >/dev/null

nvoken_log_filter="resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${nvoken_service_name}\" AND jsonPayload.invocation_id=\"${nvoken_invocation_id}\""
nvoken_log_deadline=$((SECONDS + 60))
nvoken_log_message=""
while ((SECONDS < nvoken_log_deadline)); do
  nvoken_log_message="$(gcloud logging read "${nvoken_log_filter}" \
    --project="${nvoken_project}" \
    --freshness=10m \
    --limit=1 \
    --format='value(jsonPayload.message)' 2>/dev/null || true)"
  [[ -n "${nvoken_log_message}" ]] && break
  sleep 2
done
if [[ -z "${nvoken_log_message}" ]]; then
  echo "no structured Invocation log became visible before the log timeout" >&2
  exit 1
fi

echo "Cloud Run smoke passed"
echo "project: ${nvoken_project}"
echo "region: ${nvoken_region}"
echo "invocation: ${nvoken_invocation_id}"
