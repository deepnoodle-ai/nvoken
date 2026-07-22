.PHONY: fmt build generate generate-identity identity-generate-check sqlc sqlc-check test test-postgres vet openapi-check scripts-check sdk-generate sdk-generate-check sdk-check check check-deploy run migrate

REDOCLY_VERSION := 1.34.11
SQLC_VERSION := v1.31.1
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

fmt:
	gofmt -w .

build:
	go build ./...

generate: sqlc

generate-identity:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config openapi/oapi-codegen.identity.yaml openapi/identity.yaml

identity-generate-check:
	@tmp="$$(mktemp)"; cp internal/gen/identity/client.gen.go "$$tmp"; \
	$(MAKE) generate-identity >/dev/null; diff -u "$$tmp" internal/gen/identity/client.gen.go; \
	rm -f "$$tmp"

sqlc:
	$(SQLC) generate

sqlc-check:
	$(SQLC) diff

test:
	go test ./...

test-postgres:
	@scripts/test-postgres.sh

vet:
	go vet ./...

openapi-check:
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint openapi/runtime.yaml
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint openapi/identity.yaml

scripts-check:
	bash -n scripts/test-postgres.sh

sdk-generate:
	sdk/scripts/generate.sh

sdk-generate-check:
	sdk/scripts/check-generated.sh

sdk-check:
	sdk/scripts/check.sh

check-deploy:
	terraform fmt -check -recursive deploy/google-cloud
	terraform -chdir=deploy/google-cloud init -backend=false -input=false
	terraform -chdir=deploy/google-cloud validate
	terraform -chdir=deploy/google-cloud test
	bash -n deploy/google-cloud/bootstrap-state.sh deploy/google-cloud/release.sh deploy/google-cloud/smoke.sh deploy/google-cloud/dispatch-smoke.sh

run:
	go run ./cmd/nvokend

migrate:
	go run ./cmd/nvokend migrate

check: build vet test sqlc-check identity-generate-check openapi-check scripts-check
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
