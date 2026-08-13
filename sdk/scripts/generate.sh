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

# Agent Definition exclusivity is a constraint-only oneOf. The generators do
# not model that shape consistently (the Rust generator aborts), so remove only
# the constraint from the generator copy. Each handwritten facade preserves the
# exact-one-of contract when it constructs a request.
perl -0pi -e '
  my $removed = s/^      oneOf:\n        - required: \[agent_definition\]\n          not:\n            required: \[agent_definition_id\]\n        - required: \[agent_definition_id\]\n          not:\n            required: \[agent_definition\]\n//m;
  die "CreateInvocationRequest exclusivity constraint not found; update sdk/scripts/generate.sh\n" unless $removed;
' "$SPEC"

# oapi-codegen reserves ClientInterface for its generated transport interface.
# The contract also has a ClientInterface schema for browser authorization, so
# give only the generator copy a distinct Go-safe model name. JSON and the
# committed public contract remain unchanged.
perl -0pi -e '
  s{#/components/schemas/ClientInterface}{#/components/schemas/BrowserClientInterface}g;
  s/^    ClientInterface:$/    BrowserClientInterface:/m;
' "$SPEC"

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

# OpenAPI Generator treats the contract's outbound webhook as a Runtime client
# operation and emits a broken DefaultApi sender. SDK users receive callbacks;
# they do not invoke the receiver. Keep the generated callback models, but drop
# this synthetic client surface.
rm -f sdk/typescript/src/generated/apis/DefaultApi.ts

# openapi-generator's typescript-fetch templates assume every oneOf array
# element type exports an `instanceOf*` discriminator guard, but a oneOf with
# no discriminator property (InputBlock, itself a 3-way oneOf) never gets one
# generated. InvocationInput.ts references the missing export and fails to
# compile. InputBlockFromJSONTyped/ToJSONTyped already dispatch structurally,
# so the guard is redundant; strip it.
perl -0pi -e '
  s/import \{\n\s*instanceOfInputBlock,\n/import {\n/;
  s/(\s*)if \(json\.every\(item => instanceOfInputBlock\(item\)\)\) \{\n\s*(return json\.map\(value => InputBlockFromJSONTyped\(value, true\)\);)\n\s*\}\n/$1$2\n/;
  s/(\s*)if \(value\.every\(item => instanceOfInputBlock\(item\)\)\) \{\n\s*(return value\.map\(value => InputBlockToJSON\(value as InputBlock\)\);)\n\s*\}\n/$1$2\n/;
' sdk/typescript/src/generated/models/InvocationInput.ts

# Wrapper unions between the machine and browser projections have no single
# discriminator guard: which arm arrives is decided by the caller's credential,
# not by anything in the payload. The handwritten SDK currently authenticates
# only as a machine client, so select that projection and remove imports the
# generator emits for guards that a discriminated union never exports.
for projection in \
  "CurrentIdentityResponse BrowserCurrentIdentity CurrentIdentity" \
  "InvocationListResponse BrowserInvocationList InvocationList" \
  "InvocationResponse BrowserInvocation Invocation" \
  "InvocationResultResponse BrowserInvocationResult InvocationResult" \
  "SessionListResponse BrowserSessionList SessionList" \
  "SessionMessageListResponse BrowserSessionMessageList SessionMessageList" \
  "SessionResponse BrowserSession Session" \
  "TranscriptSnapshotResponse BrowserTranscriptSnapshot TranscriptSnapshot"
do
  read -r wrapper browser_projection machine_projection <<<"$projection"
  perl -0pi -e "
    s/^\\s*instanceOf${browser_projection},\\n//m;
    s/^\\s*instanceOf${machine_projection},\\n//m;
    s/instanceOf${browser_projection}\\([^)]*\\)/false/g;
    s/instanceOf${machine_projection}\\([^)]*\\)/true/g;
  " "sdk/typescript/src/generated/models/${wrapper}.ts"
done
perl -0pi -e '
  s/^\s*instanceOfBrowserInvocationStreamEvent,\n//m;
  s/^\s*instanceOfInvocationStreamEvent,\n//m;
  s/instanceOfBrowserInvocationStreamEvent\([^)]*\)/false/g;
  s/instanceOfInvocationStreamEvent\([^)]*\)/true/g;
' sdk/typescript/src/generated/models/InvocationStreamResponse.ts
perl -0pi -e '
  s/^\s*instanceOfBrowserTranscriptStreamEvent,\n//m;
  s/^\s*instanceOfTranscriptStreamEvent,\n//m;
  s/instanceOfBrowserTranscriptStreamEvent\([^)]*\)/false/g;
  s/instanceOfTranscriptStreamEvent\([^)]*\)/true/g;
' sdk/typescript/src/generated/models/TranscriptStreamResponse.ts

rm -rf sdk/python/src/nvoken_generated
mkdir -p sdk/python/src
cp -R "$WORK/python/nvoken_generated" sdk/python/src/nvoken_generated
rm -f sdk/python/src/nvoken_generated/api/default_api.py
# The generator emits annotated code but no PEP 561 marker, and this directory is
# replaced wholesale on every run, so the marker is written here rather than
# committed by hand where the next generation would delete it.
touch sdk/python/src/nvoken_generated/py.typed

# Python's oneOf decoder treats the smaller browser projection as a second
# match for a machine response because Pydantic ignores extra fields. The
# contract orders the richer machine projection first, so return on that first
# successful parse and fall through to the browser projection only when it
# does not validate.
for wrapper in \
  current_identity_response \
  invocation_list_response \
  invocation_response \
  invocation_result_response \
  session_list_response \
  session_message_list_response \
  session_response \
  transcript_snapshot_response
do
  perl -0pi -e '
    my $seen = 0;
    s/match \+= 1/(++$seen == 3) ? "return instance" : $&/ge;
  ' "sdk/python/src/nvoken_generated/models/${wrapper}.py"
done

rm -rf sdk/rust/src/apis sdk/rust/src/models
mkdir -p sdk/rust/src
cp -R "$WORK/rust/src/apis" sdk/rust/src/apis
cp -R "$WORK/rust/src/models" sdk/rust/src/models
rm -f sdk/rust/src/apis/default_api.rs
perl -0pi -e 's/^pub mod default_api;\n//m' sdk/rust/src/apis/mod.rs

# The Rust generator makes discriminator unions internally tagged while also
# retaining the required discriminator field on each branch model. Serde
# consumes that field before decoding the branch, so otherwise every valid
# transcript block and citation fails with "missing field `type`". The exact
# literal field on each closed branch already discriminates these unions.
for model in session_content_block citation; do
  perl -0pi -e 's/#\[serde\(tag = "type"\)\]/#[serde(untagged)]/' \
    "sdk/rust/src/models/${model}.rs"
done

find \
  sdk/typescript/src/generated \
  sdk/python/src/nvoken_generated \
  -type f \
  -exec perl -0pi -e 's/[ \t]+$//mg; s/\n+\z/\n/' {} +

go run ./sdk/internal/genmanifest
gofmt -w sdk/go/generated/nvoken.gen.go
cargo fmt --manifest-path sdk/rust/Cargo.toml
