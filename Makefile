SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
# Pipeline recipes also set their flags explicitly for macOS GNU Make 3.81.

ATTESTATION_DIR ?=
SYSTEM_STATE_POSTGRES_ATTESTATION ?= $(ATTESTATION_DIR)/systemstate/postgresql-17.10-two-process-v1.json
PROJECT_OPERATOR_POSTGRES_ATTESTATION ?= $(ATTESTATION_DIR)/operator/postgresql-17.10-sqlite-external-operator-v1.json

PROJECT_MIGRATE_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectmigrateproduct
PROJECT_MIGRATE_TARGET_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectmigratetargetproduct
PROJECT_SHOWMIGRATIONS_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectshowmigrationsproduct
PROJECT_SQLMIGRATE_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectsqlmigrateproduct
PROJECT_OPERATOR_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectoperatorproduct
PROJECT_OPERATOR_PORTABLE_TEST_REGEX := ^(TestOperatorSanitizeEnvironmentDropsHostOnlyControls|TestGlobalCreatesuperuserExternalSQLiteProduct|TestOperatorCanonicalSchemaRowsSortsAndFramesWithoutAmbiguity|TestOperatorSQLiteSchemaSnapshotDetectsCatalogMutation|TestOperatorCountRawSecretOccurrencesDetectsAuditMarker)$$
RUNSERVER_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/runserverproduct
MIGRATION_WRITER_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/migrationwriterproduct
GODJ_RUNNER_IMPORT := github.com/progresshans/godj/conformance/runners/godj
GODJCHECK_IMPORT := github.com/progresshans/godj/conformance/cmd/godjcheck
MULTIRUNTIME_WORKER_IMPORT := github.com/progresshans/godj/conformance/systemstate/multiruntimeworker

.PHONY: cgo-zero-build cgo-zero-build-core cgo-zero-build-operator cgo-zero-build-products check ci conformance-check core-package-selection-check format-check generate-check godj-conformance go-race go-race-core go-race-operator go-race-products go-test go-test-core go-test-operator go-test-products go-vet oracle-check oracle-regenerate project-command-dependencies project-operator-product python-test python-test-exact targeted-migrate-product

define select_core_go_packages
core_packages="$$(go list ./... | python3 scripts/ci/packages.py core)"
endef

format-check:
	@set -euo pipefail; \
	format_manifest="$$(mktemp)"; \
	trap 'rm -f "$$format_manifest"' EXIT; \
	git ls-files -z --cached --others --exclude-standard -- '*.go' | \
		while IFS= read -r -d '' file; do \
			if [ -f "$$file" ]; then printf '%s\0' "$$file"; fi; \
		done > "$$format_manifest"; \
	unformatted=""; \
	if [ -s "$$format_manifest" ]; then \
		unformatted="$$(xargs -0 gofmt -l -- < "$$format_manifest")"; \
	fi; \
	if [ -n "$$unformatted" ]; then \
		echo "unformatted Go files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

generate-check:
	go run ./cmd/godj generate --check --project ./examples/helpdesk/godj.toml
	go run ./cmd/godj generate --check --project ./examples/article/godj.toml
	go run ./cmd/godj generate --check --project ./conformance/relationfixture/godj.toml
	go test -count=1 -run '^TestCheckedInGenerated' ./conformance/relationproduct

project-command-dependencies:
	go mod download all

core-package-selection-check:
	@set -euo pipefail; \
	$(select_core_go_packages); \
	printf '%s\n' "$$core_packages"

go-test-core:
	@set -euo pipefail; \
	$(select_core_go_packages); \
	go test -timeout=20m $$core_packages

go-test-products: project-command-dependencies
	@set -euo pipefail; \
	packages="$$(go list ./... | python3 scripts/ci/packages.py portable-products)"; \
	go test -timeout=15m -count=1 -p=1 $$packages

go-test-operator: project-command-dependencies
	go test -timeout=25m -count=1 -run '$(PROJECT_OPERATOR_PORTABLE_TEST_REGEX)' ./conformance/projectoperatorproduct

go-test: go-test-platform go-test-core go-test-integration go-test-conformance go-test-products go-test-operator targeted-migrate-product

go-vet:
	go vet ./...

go-race-core:
	@set -euo pipefail; \
	$(select_core_go_packages); \
	go test -timeout=20m -race $$core_packages

go-race-products: project-command-dependencies
	@set -euo pipefail; \
	packages="$$(go list ./... | python3 scripts/ci/packages.py portable-products)"; \
	go test -timeout=15m -count=1 -p=1 -race $$packages

go-race-operator: project-command-dependencies
	go test -timeout=25m -race -count=1 -run '$(PROJECT_OPERATOR_PORTABLE_TEST_REGEX)' ./conformance/projectoperatorproduct

go-race: go-race-platform go-race-core go-race-integration go-race-conformance go-race-products go-race-operator

cgo-zero-build-core:
	@set -euo pipefail; \
	packages="$$(CGO_ENABLED=0 go list ./... | python3 scripts/ci/packages.py core)"; \
	CGO_ENABLED=0 go test -count=1 -timeout=20m $$packages

cgo-zero-build-integration: project-command-dependencies
	@set -euo pipefail; \
	packages="$$(CGO_ENABLED=0 go list ./... | python3 scripts/ci/packages.py integration)"; \
	CGO_ENABLED=0 go test -count=1 -timeout=25m $$packages

cgo-zero-build-conformance:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/ci/conformance_tests.py cgo0

cgo-zero-build-products: project-command-dependencies
	@set -euo pipefail; \
	export CGO_ENABLED=0; \
	packages="$$(go list ./... | python3 scripts/ci/packages.py portable-products)"; \
	go test -timeout=15m -count=1 -p=1 $$packages

cgo-zero-build-operator: project-command-dependencies
	CGO_ENABLED=0 go test -timeout=25m -count=1 -run '$(PROJECT_OPERATOR_PORTABLE_TEST_REGEX)' ./conformance/projectoperatorproduct

cgo-zero-build: cgo-zero-build-platform cgo-zero-build-core cgo-zero-build-integration cgo-zero-build-conformance cgo-zero-build-products cgo-zero-build-operator

project-operator-product: go-test-operator go-race-operator cgo-zero-build-operator

targeted-migrate-product: project-command-dependencies
	go test -timeout=30m -count=1 ./conformance/projectmigratetargetproduct
	go test -timeout=30m -race -count=1 ./conformance/projectmigratetargetproduct
	CGO_ENABLED=0 go test -timeout=30m -count=1 ./conformance/projectmigratetargetproduct

python-test:
	PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uv run --frozen --project conformance/reference/drf \
		python -m scripts.ci.python_tests --profile normal

python-test-exact:
	PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uv run --frozen --project conformance/reference/drf \
		python -m scripts.ci.python_tests --profile exact

conformance-check:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/conformance.py reference

godj-conformance: require-live-evidence
	SYSTEM_STATE_POSTGRES_ATTESTATION="$(SYSTEM_STATE_POSTGRES_ATTESTATION)" \
		PROJECT_OPERATOR_POSTGRES_ATTESTATION="$(PROJECT_OPERATOR_POSTGRES_ATTESTATION)" \
		PYTHONDONTWRITEBYTECODE=1 python3 scripts/conformance.py product

oracle-check:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/conformance.py oracle-check

oracle-regenerate:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/conformance.py oracle-regenerate

ci: require-live-evidence docs-check format-check generate-check go-test go-vet go-race cgo-zero-build targeted-migrate-product python-test conformance-check godj-conformance

check: quick

# The quick loop excludes real command/process and conformance adapters.
.PHONY: quick ci-tools-test go-test-integration go-test-conformance go-race-integration go-race-conformance cgo-zero-build-integration cgo-zero-build-conformance
ci-tools-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/ci -p 'test_*.py'

quick: docs-check format-check ci-tools-test go-test-core

go-test-integration: project-command-dependencies
	@set -euo pipefail; \
	packages="$$(go list ./... | python3 scripts/ci/packages.py integration)"; \
	go test -count=1 -timeout=25m $$packages

go-test-conformance:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/ci/conformance_tests.py normal

go-race-integration: project-command-dependencies
	@set -euo pipefail; \
	packages="$$(go list ./... | python3 scripts/ci/packages.py integration)"; \
	go test -race -count=1 -timeout=25m $$packages

go-race-conformance:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/ci/conformance_tests.py race

.PHONY: require-live-evidence
require-live-evidence:
	@test -f "$(SYSTEM_STATE_POSTGRES_ATTESTATION)" && test -f "$(PROJECT_OPERATOR_POSTGRES_ATTESTATION)" || { echo "Live conformance requires current CI captures: set ATTESTATION_DIR (systemstate/ and operator/)." >&2; exit 1; }

.PHONY: go-test-platform go-race-platform cgo-zero-build-platform
go-test-platform:
	@set -euo pipefail; \
	packages="$$(go list ./... | python3 scripts/ci/packages.py platform)"; \
	go test -count=1 -p=1 -timeout=30m $$packages

go-race-platform:
	@set -euo pipefail; \
	packages="$$(go list ./... | python3 scripts/ci/packages.py platform)"; \
	go test -race -count=1 -p=1 -timeout=30m $$packages

cgo-zero-build-platform:
	@set -euo pipefail; \
	packages="$$(CGO_ENABLED=0 go list ./... | python3 scripts/ci/packages.py platform)"; \
	CGO_ENABLED=0 go test -count=1 -p=1 -timeout=30m $$packages

.PHONY: docs-check
docs-check:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/check_docs.py
