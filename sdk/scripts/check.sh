#!/usr/bin/env bash
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly BASE_URL="http://127.0.0.1:43109"
readonly SERVER_LOG="$(mktemp "${TMPDIR:-/tmp}/nvoken-conformance.XXXXXX.log")"

server_pid=""
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -f "$SERVER_LOG"
}
trap cleanup EXIT

cd "$ROOT"
sdk/scripts/check-generated.sh

go run ./sdk/conformance/server >"$SERVER_LOG" 2>&1 &
server_pid="$!"
for _ in {1..100}; do
  if curl --fail --silent "$BASE_URL/healthz" >/dev/null; then
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    cat "$SERVER_LOG" >&2
    exit 1
  fi
  sleep 0.1
done
curl --fail --silent "$BASE_URL/healthz" >/dev/null
export NVOKEN_CONFORMANCE_URL="$BASE_URL"

(
  cd sdk/go
  GOWORK=off go test ./... -count=1
)

npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
npm test --prefix sdk/typescript

python3 -m venv sdk/python/.venv
sdk/python/.venv/bin/python -m pip install --quiet --upgrade pip
sdk/python/.venv/bin/python -m pip install --quiet -e 'sdk/python[test]'
sdk/python/.venv/bin/python -m compileall -q sdk/python/src sdk/python/examples
sdk/python/.venv/bin/pytest -q sdk/python

cargo test --manifest-path sdk/rust/Cargo.toml --all-targets
go test ./cmd/nvoken -count=1
