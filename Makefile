.PHONY: fmt build release generate generate-identity identity-generate-check sqlc sqlc-check test test-postgres test-restore vet openapi-check scripts-check sdk-generate sdk-generate-check sdk-check onboarding-check check check-deploy readiness run upgrade-preflight migrate

REDOCLY_VERSION := 1.34.11
SQLC_VERSION := v1.31.1
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

fmt:
	gofmt -w .

build:
	go build ./...

release:
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=0.1.1"; exit 1; fi
	python3 scripts/release.py "$(VERSION)"

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

test-restore:
	@python3 scripts/test_restore.py

vet:
	go vet ./...

openapi-check:
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint openapi/runtime.yaml
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint openapi/identity.yaml

scripts-check:
	bash -n scripts/test-postgres.sh
	python3 -c 'import ast, pathlib; [ast.parse(pathlib.Path(path).read_text(), filename=path) for path in ("deploy/local/configure.py", "deploy/local/configure_test.py", "deploy/single-daemon/smoke.py", "deploy/single-daemon/smoke_test.py", "deploy/single-daemon/load.py", "scripts/release.py", "scripts/release_test.py", "scripts/test_typescript_onboarding.py")]'
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy/local -p '*_test.py'
	python3 deploy/single-daemon/smoke.py --help >/dev/null
	PYTHONDONTWRITEBYTECODE=1 python3 deploy/single-daemon/smoke_test.py >/dev/null
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p '*_test.py'
	python3 scripts/test_restore.py --check

sdk-generate:
	sdk/scripts/generate.sh

sdk-generate-check:
	sdk/scripts/check-generated.sh

sdk-check:
	sdk/scripts/check.sh

onboarding-check:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/test_typescript_onboarding.py

check-deploy:
	terraform fmt -check -recursive deploy/google-cloud
	terraform -chdir=deploy/google-cloud init -backend=false -input=false
	terraform -chdir=deploy/google-cloud validate
	terraform -chdir=deploy/google-cloud test
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy/google-cloud -p 'test_*.py'
	bash -n deploy/google-cloud/bootstrap-state.sh deploy/google-cloud/release.sh

readiness:
	@if [ -z "$(PROFILE)" ]; then echo "set PROFILE=single_daemon or PROFILE=google_cloud" >&2; exit 2; fi
	@python3 scripts/readiness.py --profile "$(PROFILE)" \
		$(if $(OUTPUT),--output "$(OUTPUT)") \
		$(if $(filter 1 true yes,$(LIVE)),--live) \
		$(if $(QUALIFY_ARGS),-- $(QUALIFY_ARGS))

run:
	go run ./cmd/nvokend

upgrade-preflight:
	go run ./cmd/nvokend upgrade-preflight

migrate:
	go run ./cmd/nvokend migrate

check: build vet test sqlc-check identity-generate-check openapi-check scripts-check
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
