#!/usr/bin/env bash
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly WORK="$(mktemp -d "${TMPDIR:-/tmp}/nvoken-sdk-check-generated.XXXXXX")"
readonly BEFORE="$WORK/before"
readonly AFTER="$WORK/after"
readonly DIFF="$WORK/diff"
readonly GENERATED_PATHS=(
  sdk/operations.json
  sdk/go/generated
  sdk/go/identitygenerated
  sdk/typescript/src/generated
  sdk/typescript/src/identity-generated
  sdk/python/src/nvoken_generated
  sdk/rust/src/apis
  sdk/rust/src/models
  sdk/rust/src/routes.rs
)

cleanup() {
  rm -rf "$WORK"
}
trap cleanup EXIT

snapshot_generated() {
  local destination="$1"
  local path

  for path in "${GENERATED_PATHS[@]}"; do
    if [[ ! -e "$path" ]]; then
      continue
    fi
    mkdir -p "$destination/$(dirname "$path")"
    cp -R "$path" "$destination/$path"
  done
  # Python writes __pycache__ inside the generated package the moment anything
  # imports it, and the generator wipes the tree before rewriting it, so those
  # caches exist on the "before" side of the comparison only. Comparing
  # filesystem trees rather than tracked content is what makes this check work
  # in an uncommitted worktree; it also means an untracked build artifact reads
  # as drift. Bytecode caches are not generated transports, so drop them from
  # both snapshots and let a real difference be the only thing that fails.
  find "$destination" -name '__pycache__' -type d -prune -exec rm -rf {} +
}

cd "$ROOT"

snapshot_generated "$BEFORE"
sdk/scripts/generate.sh
snapshot_generated "$AFTER"

if ! diff -qr "$BEFORE" "$AFTER" >"$DIFF"; then
  echo "generated SDK transports are stale; run make sdk-generate" >&2
  sed "s|$BEFORE/||g; s|$AFTER/||g" "$DIFF" >&2
  exit 1
fi
