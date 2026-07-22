#!/usr/bin/env bash
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly OAPI_CODEGEN_VERSION="v2.8.0"
readonly OPENAPI_GENERATOR_VERSION="7.22.0"
readonly OPENAPI_GENERATOR_SHA256="3f1e6ce5c6ad4f15242c6170ab43aad4bad771622617eeece4a7d4f72ffaf329"
readonly WORK="$(mktemp -d "${TMPDIR:-/tmp}/nvoken-sdk-generate.XXXXXX")"
readonly JAR="$WORK/openapi-generator-cli.jar"

cleanup() {
  rm -rf "$WORK"
}
trap cleanup EXIT

cd "$ROOT"

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
  --o "$WORK/runtime.gen.go" \
  openapi/runtime.yaml

java -jar "$JAR" generate \
  --generator-name typescript-fetch \
  --input-spec openapi/runtime.yaml \
  --output "$WORK/typescript" \
  --additional-properties 'npmName=@deepnoodle/nvoken,npmVersion=0.1.0,supportsES6=true,useSingleRequestParameter=true,importFileExtension=.js,hideGenerationTimestamp=true,disallowAdditionalPropertiesIfNotPresent=false' \
  --global-property 'apiDocs=false,modelDocs=false,apiTests=false,modelTests=false'

java -jar "$JAR" generate \
  --generator-name python \
  --input-spec openapi/runtime.yaml \
  --output "$WORK/python" \
  --additional-properties 'packageName=nvoken_generated,projectName=nvoken,packageVersion=0.1.0,library=httpx,supportHttpxSync=true,generateSourceCodeOnly=true,hideGenerationTimestamp=true,disallowAdditionalPropertiesIfNotPresent=false' \
  --global-property 'apiDocs=false,modelDocs=false,apiTests=false,modelTests=false'

java -jar "$JAR" generate \
  --generator-name rust \
  --input-spec openapi/runtime.yaml \
  --output "$WORK/rust" \
  --additional-properties 'packageName=nvoken,packageVersion=0.1.0,library=reqwest,supportAsync=true,supportMiddleware=true,hideGenerationTimestamp=true,disallowAdditionalPropertiesIfNotPresent=false,preferUnsignedInt=true' \
  --global-property 'apiDocs=false,modelDocs=false,apiTests=false,modelTests=false'

rm -rf sdk/go/generated
mkdir -p sdk/go/generated
cp "$WORK/runtime.gen.go" sdk/go/generated/runtime.gen.go

rm -rf sdk/typescript/src/generated
mkdir -p sdk/typescript/src/generated
cp -R "$WORK/typescript/src/." sdk/typescript/src/generated/

rm -rf sdk/python/src/nvoken_generated
mkdir -p sdk/python/src
cp -R "$WORK/python/nvoken_generated" sdk/python/src/nvoken_generated

rm -rf sdk/rust/src/apis sdk/rust/src/models
mkdir -p sdk/rust/src
cp -R "$WORK/rust/src/apis" sdk/rust/src/apis
cp -R "$WORK/rust/src/models" sdk/rust/src/models

find \
  sdk/typescript/src/generated \
  sdk/python/src/nvoken_generated \
  -type f \
  -exec perl -0pi -e 's/[ \t]+$//mg; s/\n+\z/\n/' {} +

go run ./sdk/internal/genmanifest
gofmt -w sdk/go/generated/runtime.gen.go
cargo fmt --manifest-path sdk/rust/Cargo.toml
