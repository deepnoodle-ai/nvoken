#!/usr/bin/env bash
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly OAPI_CODEGEN_VERSION="v2.8.0"
readonly OPENAPI_GENERATOR_VERSION="7.22.0"
readonly OPENAPI_GENERATOR_SHA256="3f1e6ce5c6ad4f15242c6170ab43aad4bad771622617eeece4a7d4f72ffaf329"
readonly WORK="$(mktemp -d "${TMPDIR:-/tmp}/nvoken-sdk-generate.XXXXXX")"
readonly JAR="$WORK/openapi-generator-cli.jar"
readonly TYPESCRIPT_VERSION="$(python3 -c 'import json; print(json.load(open("sdk/typescript/package.json"))["version"])')"
readonly PYTHON_VERSION="$(python3 -c 'import pathlib, tomllib; print(tomllib.loads(pathlib.Path("sdk/python/pyproject.toml").read_text())["project"]["version"])')"
readonly RUST_VERSION="$(python3 -c 'import pathlib, tomllib; print(tomllib.loads(pathlib.Path("sdk/rust/Cargo.toml").read_text())["package"]["version"])')"

cleanup() {
  rm -rf "$WORK"
}
trap cleanup EXIT

cd "$ROOT"

# Generate from an isolated copy so generator-specific normalization never
# mutates the committed OpenAPI snapshot.
readonly SPEC="$WORK/nvoken.yaml"
cp openapi/nvoken.yaml "$SPEC"

# Agent identity exclusivity used to be a constraint-only oneOf that the
# generators did not model consistently — the Rust generator aborted on it — so
# it was stripped from the generator copy here, and each handwritten facade
# restated the exact-one-of rule when constructing a request.
#
# The contract now says at most one instead, as an allOf of `not` clauses,
# because a browser token names the Agent and sends neither field. That shape
# needs no stripping, and every generator already tolerates it.

# oapi-codegen reserves ClientInterface for its generated transport interface.
# The contract also has a ClientInterface schema for browser authorization, so
# give only the generator copy a distinct Go-safe model name. JSON and the
# committed public contract remain unchanged.
perl -0pi -e '
  s{#/components/schemas/ClientInterface}{#/components/schemas/BrowserClientInterface}g;
  s/^    ClientInterface:$/    BrowserClientInterface:/m;
' "$SPEC"

# The two nullable policy fields on UpdateConversationRequest need three Go
# states: omitted, explicit null, and a replacement value. Enabling oapi-codegen's
# nullable type globally would change every nullable response projection, so add
# exact generator-only annotations to these fields instead.
python3 sdk/scripts/annotate_go_nullable_updates.py "$SPEC"

curl --fail --silent --show-error --location \
  "https://repo1.maven.org/maven2/org/openapitools/openapi-generator-cli/${OPENAPI_GENERATOR_VERSION}/openapi-generator-cli-${OPENAPI_GENERATOR_VERSION}.jar" \
  --output "$JAR"

actual_sha="$(shasum -a 256 "$JAR" | awk '{print $1}')"
if [[ "$actual_sha" != "$OPENAPI_GENERATOR_SHA256" ]]; then
  echo "OpenAPI Generator checksum mismatch: got $actual_sha" >&2
  exit 1
fi

go run "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@${OAPI_CODEGEN_VERSION}" \
  --config sdk/go/oapi-codegen.yaml \
  --o "$WORK/nvoken.gen.go" \
  "$SPEC"

java -jar "$JAR" generate \
  --generator-name typescript-fetch \
  --input-spec "$SPEC" \
  --output "$WORK/typescript" \
  --additional-properties "npmName=@deepnoodle/nvoken,npmVersion=${TYPESCRIPT_VERSION},supportsES6=true,useSingleRequestParameter=true,importFileExtension=.js,hideGenerationTimestamp=true,disallowAdditionalPropertiesIfNotPresent=false" \
  --global-property 'apiDocs=false,modelDocs=false,apiTests=false,modelTests=false'

java -jar "$JAR" generate \
  --generator-name python \
  --input-spec "$SPEC" \
  --output "$WORK/python" \
  --additional-properties "packageName=nvoken_generated,projectName=nvoken,packageVersion=${PYTHON_VERSION},library=httpx,supportHttpxSync=true,generateSourceCodeOnly=true,hideGenerationTimestamp=true,disallowAdditionalPropertiesIfNotPresent=false" \
  --global-property 'apiDocs=false,modelDocs=false,apiTests=false,modelTests=false'

java -jar "$JAR" generate \
  --generator-name rust \
  --input-spec "$SPEC" \
  --output "$WORK/rust" \
  --additional-properties "packageName=nvoken,packageVersion=${RUST_VERSION},library=reqwest,supportAsync=true,supportMiddleware=true,hideGenerationTimestamp=true,disallowAdditionalPropertiesIfNotPresent=false,preferUnsignedInt=true" \
  --global-property 'apiDocs=false,modelDocs=false,apiTests=false,modelTests=false'

rm -rf sdk/go/generated
mkdir -p sdk/go/generated
cp "$WORK/nvoken.gen.go" sdk/go/generated/nvoken.gen.go

rm -rf sdk/typescript/src/generated
mkdir -p sdk/typescript/src/generated
cp -R "$WORK/typescript/src/." sdk/typescript/src/generated/

# OpenAPI Generator emits TurnInput's array arm with a call to
# `instanceOfInputBlock`, but composed union models do not get that predicate.
# Decode each array member through the generated union decoder instead.
python3 sdk/scripts/fix_typescript_turn_input.py \
  sdk/typescript/src/generated/models/TurnInput.ts

# OpenAPI Generator treats the contract's outbound webhook as a Runtime client
# operation and emits a broken DefaultApi sender. SDK users receive callbacks;
# they do not invoke the receiver. Keep the generated callback models, but drop
# this synthetic client surface.
rm -f sdk/typescript/src/generated/apis/DefaultApi.ts

rm -rf sdk/python/src/nvoken_generated
mkdir -p sdk/python/src
cp -R "$WORK/python/nvoken_generated" sdk/python/src/nvoken_generated
rm -f sdk/python/src/nvoken_generated/api/default_api.py
# The generator emits annotated code but no PEP 561 marker, and this directory is
# replaced wholesale on every run, so the marker is written here rather than
# committed by hand where the next generation would delete it.
touch sdk/python/src/nvoken_generated/py.typed

rm -rf sdk/rust/src/apis sdk/rust/src/models
mkdir -p sdk/rust/src
cp -R "$WORK/rust/src/apis" sdk/rust/src/apis
cp -R "$WORK/rust/src/models" sdk/rust/src/models
rm -f sdk/rust/src/apis/default_api.rs
perl -0pi -e 's/^pub mod default_api;\n//m' sdk/rust/src/apis/mod.rs

# The Rust generator makes discriminator unions internally tagged while also
# retaining the required discriminator field on each branch model. Serde
# consumes that field before decoding the branch, so otherwise every valid
# transcript block, citation, tool declaration, stream frame, Agent owner,
# Conversation owner, and memory selection fails with "missing field `type`",
# "missing field `kind`", or "missing field `scope`", and every one it encodes
# carries the discriminator twice. The exact literal field on each closed
# branch already discriminates these unions. `Agent.owner` and
# `Conversation.owner` are required, so with the tag left in place no Agent
# or Conversation response decodes at all.
#
# A new discriminator union that is not listed here ships broken. The
# guard below fails generation when one appears, so add it to this list.
for model in \
  conversation_content_block citation tool_declaration stream_event turn_conversation \
  agent_owner conversation_owner default_memory_policy memory_space_selector \
  turn_behavior_selection turn_behavior_source turn_memory_selection \
  delivery_behavior_source browser_conversation_access browser_memory_access; do
  perl -0pi -e '
    die "no internal tag in '"${model}"'; update sdk/scripts/generate.sh\n"
      unless s/#\[serde\(tag = "(?:type|mode|kind|scope|default_scope)"\)\]/#[serde(untagged)]/;
  ' "sdk/rust/src/models/${model}.rs"
done
if grep -rl 'serde(tag = ' sdk/rust/src/models >/dev/null; then
  echo "internally tagged Rust unions remain; add them to the untagged list in sdk/scripts/generate.sh:" >&2
  grep -rl 'serde(tag = ' sdk/rust/src/models >&2
  exit 1
fi

find \
  sdk/typescript/src/generated \
  sdk/python/src/nvoken_generated \
  -type f \
  -exec perl -0pi -e 's/[ \t]+$//mg; s/\n+\z/\n/' {} +

go run ./sdk/internal/genmanifest
gofmt -w sdk/go/generated/nvoken.gen.go
cargo fmt --manifest-path sdk/rust/Cargo.toml
