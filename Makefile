REDOCLY_VERSION := 1.34.11

.PHONY: build check facade-check fmt fmt-check generate openapi-check release \
	scripts-check sdk-check sdk-generate sdk-generate-check test vet

build:
	mkdir -p bin
	go build -o bin/nvoken ./cmd/nvoken

fmt:
	gofmt -w cmd internal sdk/conformance sdk/go sdk/internal
	cargo fmt --manifest-path sdk/rust/Cargo.toml

fmt-check:
	@out="$$(gofmt -l cmd internal sdk/conformance sdk/go sdk/internal)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	cargo fmt --manifest-path sdk/rust/Cargo.toml --check

facade-check:
	python3 sdk/scripts/check_facade_parity.py

generate: sdk-generate

openapi-check:
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint openapi/nvoken.yaml

release:
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=x.y.z"; exit 1; fi
	python3 scripts/release.py "$(VERSION)"

scripts-check:
	bash -n sdk/scripts/generate.sh sdk/scripts/check-generated.sh sdk/scripts/check.sh
	python3 -m compileall -q scripts sdk/scripts/check_package_files.py sdk/scripts/check_facade_parity.py sdk/scripts/fix_typescript_turn_input.py
	python3 scripts/check_python_deps.py
	python3 scripts/check_versions.py
	python3 scripts/check_go_frame_keys.py
	python3 scripts/check_signing_vectors.py
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_*.py'

sdk-generate:
	sdk/scripts/generate.sh

sdk-generate-check:
	sdk/scripts/check-generated.sh

sdk-check:
	sdk/scripts/check.sh

test:
	go test ./...

vet:
	go vet ./...

check: build vet test fmt-check openapi-check scripts-check facade-check sdk-check
