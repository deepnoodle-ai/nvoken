#!/usr/bin/env bash
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

sdk/scripts/generate.sh

if ! git diff --quiet -- sdk/operations.json sdk/go/generated sdk/typescript/src/generated sdk/python/src/nvoken_generated sdk/rust/src/apis sdk/rust/src/models sdk/rust/src/routes.rs; then
  echo "generated SDK transports are stale; run make sdk-generate" >&2
  git diff --stat -- sdk/operations.json sdk/go/generated sdk/typescript/src/generated sdk/python/src/nvoken_generated sdk/rust/src/apis sdk/rust/src/models sdk/rust/src/routes.rs >&2
  exit 1
fi
