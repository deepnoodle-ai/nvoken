.PHONY: fmt build generate sqlc sqlc-check test test-postgres vet openapi-check check check-deploy run migrate

REDOCLY_VERSION := 1.34.11
SQLC_VERSION := v1.31.1
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

fmt:
	gofmt -w .

build:
	go build ./...

generate: sqlc

sqlc:
	$(SQLC) generate

sqlc-check:
	$(SQLC) diff

test:
	go test ./...

test-postgres:
	@if [ -z "$$NVOKEN_TEST_DATABASE_URL" ]; then echo "NVOKEN_TEST_DATABASE_URL is required"; exit 1; fi
	go test ./... -count=1

vet:
	go vet ./...

openapi-check:
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint openapi/runtime.yaml

check-deploy:
	terraform fmt -check -recursive deploy/google-cloud
	terraform -chdir=deploy/google-cloud init -backend=false -input=false
	terraform -chdir=deploy/google-cloud validate
	terraform -chdir=deploy/google-cloud test
	bash -n deploy/google-cloud/bootstrap-state.sh deploy/google-cloud/release.sh deploy/google-cloud/smoke.sh

run:
	go run ./cmd/nvokend

migrate:
	go run ./cmd/nvokend migrate

check: build vet test sqlc-check openapi-check
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
