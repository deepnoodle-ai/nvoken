#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <path-to-prd>" >&2
  exit 2
fi

claude_bin="${NVOKEN_CLAUDE_BIN:-}"
if [[ -z "$claude_bin" && -x "$HOME/.local/bin/claude" ]]; then
  claude_bin="$HOME/.local/bin/claude"
fi
if [[ -z "$claude_bin" ]]; then
  claude_bin="$(command -v claude || true)"
fi
if [[ -z "$claude_bin" ]]; then
  echo "claude: command not found" >&2
  exit 127
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd -- "$script_dir/../../../.." && pwd -P)"
mobius_candidate="$repo_root/../mobius-cloud"

if [[ ! -d "$mobius_candidate" ]]; then
  echo "Mobius Cloud sibling not found: $mobius_candidate" >&2
  exit 2
fi
mobius_root="$(cd -- "$mobius_candidate" && pwd -P)"

prd_arg="$1"
if [[ "$prd_arg" = /* ]]; then
  prd_path="$prd_arg"
else
  prd_path="$repo_root/$prd_arg"
fi
if [[ ! -f "$prd_path" ]]; then
  echo "PRD not found: $prd_path" >&2
  exit 2
fi
prd_dir="$(cd -- "$(dirname -- "$prd_path")" && pwd -P)"
prd_path="$prd_dir/$(basename -- "$prd_path")"

review_budget="${NVOKEN_PRD_REVIEW_BUDGET_USD:-5.00}"
system_prompt="You are an independent, read-only product requirements reviewer. Inspect the named repository files before answering. Report only evidence-backed conclusions. Do not modify files."
review_prompt="Review the PRD at $prd_path. Start by reading $repo_root/CLAUDE.md, $repo_root/docs/prds/README.md, and the relevant governing files under $repo_root/docs/design/. Inspect current nvoken code or contracts where relevant. If the PRD is marked complete or has checked acceptance criteria, inspect every delivered artifact it names, including OpenAPI, examples, decision-log changes, and documentation, and verify that the evidence actually satisfies those checks. Independently inspect corresponding functionality in $mobius_root, including the schemas and admission, transcript, invocation lifecycle, claim, lease, reaper, and recovery seams relevant to this PRD; do not rely on the PRD's characterization of Mobius Cloud. Evaluate sequence and scope, requirement precision, acceptance coverage, contradictions, durability claims, public-versus-internal boundaries, and unnecessary verbosity. Cite concrete file paths and line numbers in evidence. Return an empty findings array if no change is warranted. Do not modify files."
review_schema='{"type":"object","properties":{"verdict":{"type":"string","enum":["approve","revise"]},"summary":{"type":"string"},"findings":{"type":"array","items":{"type":"object","properties":{"severity":{"type":"string","enum":["blocking","important","suggestion"]},"title":{"type":"string"},"evidence":{"type":"string"},"recommendation":{"type":"string"},"requirement_refs":{"type":"array","items":{"type":"string"}}},"required":["severity","title","evidence","recommendation","requirement_refs"],"additionalProperties":false}},"open_questions":{"type":"array","items":{"type":"string"}}},"required":["verdict","summary","findings","open_questions"],"additionalProperties":false}'

cd -- "$repo_root"
exec env -u ANTHROPIC_API_KEY "$claude_bin" -p \
  --safe-mode \
  --system-prompt "$system_prompt" \
  --model fable \
  --effort high \
  --permission-mode dontAsk \
  --add-dir "$mobius_root" \
  --tools "Read,Glob,Grep" \
  --output-format json \
  --json-schema "$review_schema" \
  --no-session-persistence \
  --max-budget-usd "$review_budget" \
  "$review_prompt"
