#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

for nvoken_command in gcloud; do
  if ! command -v "${nvoken_command}" >/dev/null 2>&1; then
    echo "required command not found: ${nvoken_command}" >&2
    exit 1
  fi
done

: "${TF_VAR_project_id:?set TF_VAR_project_id to the deployment project}"
: "${NVOKEN_TF_STATE_BUCKET:?set NVOKEN_TF_STATE_BUCKET to the globally unique Terraform state bucket name}"

case "${NVOKEN_TF_STATE_BUCKET}" in
  gs://*)
    echo "NVOKEN_TF_STATE_BUCKET must be a bucket name without the gs:// prefix" >&2
    exit 1
    ;;
esac

nvoken_state_location="${NVOKEN_TF_STATE_LOCATION:-${TF_VAR_region:-us-central1}}"
nvoken_state_uri="gs://${NVOKEN_TF_STATE_BUCKET}"

gcloud services enable storage.googleapis.com --project="${TF_VAR_project_id}"

# The backend bucket cannot be managed by the Terraform state it stores. Keep
# this bootstrap idempotent and fail closed: if describe fails because the
# caller cannot see an existing globally named bucket, create also fails rather
# than silently selecting another state location.
if ! gcloud storage buckets describe "${nvoken_state_uri}" \
  --project="${TF_VAR_project_id}" >/dev/null 2>&1; then
  gcloud storage buckets create "${nvoken_state_uri}" \
    --project="${TF_VAR_project_id}" \
    --location="${nvoken_state_location}" \
    --uniform-bucket-level-access \
    --public-access-prevention
fi

gcloud storage buckets update "${nvoken_state_uri}" \
  --project="${TF_VAR_project_id}" \
  --uniform-bucket-level-access \
  --public-access-prevention \
  --versioning \
  --update-labels=application=nvoken,usage=terraform-state

echo "Terraform state bucket ready: ${nvoken_state_uri}"
