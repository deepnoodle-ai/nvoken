#!/usr/bin/env bash
set -euo pipefail

nvoken_repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

run_tests() {
  (
    cd "${nvoken_repo_root}"
    go test ./... -count=1
  )
}

if [[ -n "${NVOKEN_TEST_DATABASE_URL:-}" ]]; then
  echo "Using NVOKEN_TEST_DATABASE_URL for Postgres integration tests."
  run_tests
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required when NVOKEN_TEST_DATABASE_URL is not set." >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "Docker is installed but its daemon is unavailable." >&2
  exit 1
fi

nvoken_postgres_image="${NVOKEN_TEST_POSTGRES_IMAGE:-postgres:17}"
nvoken_postgres_container="nvoken-test-postgres-${PPID}-${RANDOM}"
nvoken_postgres_user="nvoken"
nvoken_postgres_password="nvoken-test"
nvoken_postgres_database="nvoken_test"

cleanup() {
  docker rm --force "${nvoken_postgres_container}" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "Starting disposable ${nvoken_postgres_image} for Postgres integration tests."
docker run \
  --detach \
  --rm \
  --name "${nvoken_postgres_container}" \
  --env "POSTGRES_USER=${nvoken_postgres_user}" \
  --env "POSTGRES_PASSWORD=${nvoken_postgres_password}" \
  --env "POSTGRES_DB=${nvoken_postgres_database}" \
  --publish 127.0.0.1::5432 \
  "${nvoken_postgres_image}" >/dev/null

nvoken_postgres_ready=false
for ((nvoken_postgres_attempt = 0; nvoken_postgres_attempt < 60; nvoken_postgres_attempt++)); do
  if docker exec "${nvoken_postgres_container}" \
    pg_isready --username "${nvoken_postgres_user}" --dbname "${nvoken_postgres_database}" >/dev/null 2>&1; then
    nvoken_postgres_ready=true
    break
  fi
  nvoken_postgres_running="$(
    docker inspect --format '{{.State.Running}}' "${nvoken_postgres_container}" 2>/dev/null || true
  )"
  if [[ "${nvoken_postgres_running}" != "true" ]]; then
    echo "Disposable Postgres exited before becoming ready:" >&2
    docker logs "${nvoken_postgres_container}" >&2 || true
    exit 1
  fi
  sleep 1
done
if [[ "${nvoken_postgres_ready}" != "true" ]]; then
  echo "Disposable Postgres did not become ready within 60 seconds." >&2
  docker logs "${nvoken_postgres_container}" >&2 || true
  exit 1
fi

nvoken_postgres_binding="$(docker port "${nvoken_postgres_container}" 5432/tcp)"
nvoken_postgres_port="${nvoken_postgres_binding##*:}"
if [[ -z "${nvoken_postgres_port}" || "${nvoken_postgres_port}" == "${nvoken_postgres_binding}" ]]; then
  echo "Could not determine the disposable Postgres host port." >&2
  exit 1
fi

export NVOKEN_TEST_DATABASE_URL="postgres://${nvoken_postgres_user}:${nvoken_postgres_password}@127.0.0.1:${nvoken_postgres_port}/${nvoken_postgres_database}?sslmode=disable"
run_tests
