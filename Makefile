PROFILE := conformance/profiles/django-6.1-sqlite-darwin-arm64.json
MANIFEST := conformance/contracts/manifest.json
ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
NOT_IMPLEMENTED := conformance/fixtures/godj-not-implemented.json
WRITE_MIGRATION_MANIFEST := conformance/contracts/write-migration-manifest.json
WRITE_MIGRATION_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json
WRITE_MIGRATION_NOT_IMPLEMENTED := conformance/fixtures/godj-write-migration-not-implemented.json
SAVE_LIFECYCLE_MANIFEST := conformance/contracts/save-lifecycle-manifest.json
SAVE_LIFECYCLE_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json
SAVE_LIFECYCLE_NOT_IMPLEMENTED := conformance/fixtures/godj-save-lifecycle-not-implemented.json
QUERY_CACHE_MANIFEST := conformance/contracts/query-cache-manifest.json
QUERY_CACHE_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json
QUERY_CACHE_NOT_IMPLEMENTED := conformance/fixtures/godj-query-cache-not-implemented.json
MIGRATION_PLANNING_MANIFEST := conformance/contracts/migration-planning-manifest.json
MIGRATION_PLANNING_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json
MIGRATION_PLANNING_NOT_IMPLEMENTED := conformance/fixtures/godj-migration-planning-not-implemented.json
MIGRATION_EXECUTION_MANIFEST := conformance/contracts/migration-execution-manifest.json
MIGRATION_EXECUTION_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json
MIGRATION_EXECUTION_NOT_IMPLEMENTED := conformance/fixtures/godj-migration-execution-not-implemented.json
MIGRATION_EXECUTION_DEVIATION_EXPECTED := conformance/fixtures/godj-migration-execution-deviation-expected.json
MIGRATION_RESTART_MANIFEST := conformance/contracts/migration-restart-manifest.json
MIGRATION_RESTART_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json
MIGRATION_RESTART_NOT_IMPLEMENTED := conformance/fixtures/godj-migration-restart-not-implemented.json
MIGRATION_STATE_RECONSTRUCTION_MANIFEST := conformance/contracts/migration-state-reconstruction-manifest.json
MIGRATION_STATE_RECONSTRUCTION_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json
MIGRATION_STATE_RECONSTRUCTION_NOT_IMPLEMENTED := conformance/fixtures/godj-migration-state-reconstruction-not-implemented.json
MIGRATION_LIFECYCLE_MANIFEST := conformance/contracts/migration-lifecycle-manifest.json
MIGRATION_LIFECYCLE_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json
MIGRATION_LIFECYCLE_NOT_IMPLEMENTED := conformance/fixtures/godj-migration-lifecycle-not-implemented.json
MIGRATION_LIFECYCLE_DEVIATION_EXPECTED := conformance/fixtures/godj-migration-lifecycle-deviation-expected.json
MIGRATION_DEFINITION_SOURCE_MANIFEST := conformance/contracts/migration-definition-source-manifest.json
MIGRATION_DEFINITION_SOURCE_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json
MIGRATION_DEFINITION_SOURCE_NOT_IMPLEMENTED := conformance/fixtures/godj-migration-definition-source-not-implemented.json
MIGRATION_PROJECT_CHECK_MANIFEST := conformance/contracts/migration-project-check-manifest.json
MIGRATION_PROJECT_CHECK_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json
MIGRATION_PROJECT_CHECK_NOT_IMPLEMENTED := conformance/fixtures/godj-migration-project-check-not-implemented.json
RELATION_MANIFEST := conformance/contracts/relation-manifest.json
RELATION_ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json
RELATION_NOT_IMPLEMENTED := conformance/fixtures/godj-relation-not-implemented.json

.PHONY: cgo-zero-build check ci conformance-check format-check generate-check godj-conformance go-race go-test go-vet oracle-check oracle-regenerate python-test python-test-exact

format-check:
	@unformatted="$$(git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 gofmt -l)"; \
	if [ -n "$$unformatted" ]; then \
		echo "unformatted Go files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

generate-check:
	go run ./internal/cmd/m1generate -check

go-test:
	go test ./...

go-vet:
	go vet ./...

go-race:
	go test -race ./...

cgo-zero-build:
	CGO_ENABLED=0 go test \
		./db/sqlite \
		./cmd/godj \
		./project \
		./query \
		./internal/projectcheck/... \
		./conformance/runners/godj \
		./conformance/relationproduct/... \
		./conformance/relationqueryproduct/... \
		-count=1

python-test:
	PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uv run --frozen python -m unittest discover \
		-s conformance/runners/django/tests -v

python-test-exact:
	GODJ_EXACT_PROFILE=1 PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uv run --frozen python -m unittest discover \
		-s conformance/runners/django/tests -v

conformance-check:
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MANIFEST) -suite $(ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MANIFEST) -suite $(NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(WRITE_MIGRATION_MANIFEST) -suite $(WRITE_MIGRATION_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(WRITE_MIGRATION_MANIFEST) -suite $(WRITE_MIGRATION_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(SAVE_LIFECYCLE_MANIFEST) -suite $(SAVE_LIFECYCLE_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(SAVE_LIFECYCLE_MANIFEST) -suite $(SAVE_LIFECYCLE_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(QUERY_CACHE_MANIFEST) -suite $(QUERY_CACHE_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(QUERY_CACHE_MANIFEST) -suite $(QUERY_CACHE_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_PLANNING_MANIFEST) -suite $(MIGRATION_PLANNING_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_PLANNING_MANIFEST) -suite $(MIGRATION_PLANNING_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_EXECUTION_MANIFEST) -suite $(MIGRATION_EXECUTION_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_EXECUTION_MANIFEST) -suite $(MIGRATION_EXECUTION_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_RESTART_MANIFEST) -suite $(MIGRATION_RESTART_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_RESTART_MANIFEST) -suite $(MIGRATION_RESTART_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_STATE_RECONSTRUCTION_MANIFEST) -suite $(MIGRATION_STATE_RECONSTRUCTION_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_STATE_RECONSTRUCTION_MANIFEST) -suite $(MIGRATION_STATE_RECONSTRUCTION_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_LIFECYCLE_MANIFEST) -suite $(MIGRATION_LIFECYCLE_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_LIFECYCLE_MANIFEST) -suite $(MIGRATION_LIFECYCLE_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_DEFINITION_SOURCE_MANIFEST) -suite $(MIGRATION_DEFINITION_SOURCE_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_DEFINITION_SOURCE_MANIFEST) -suite $(MIGRATION_DEFINITION_SOURCE_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_PROJECT_CHECK_MANIFEST) -suite $(MIGRATION_PROJECT_CHECK_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_PROJECT_CHECK_MANIFEST) -suite $(MIGRATION_PROJECT_CHECK_NOT_IMPLEMENTED)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(RELATION_MANIFEST) -suite $(RELATION_ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(RELATION_MANIFEST) -suite $(RELATION_NOT_IMPLEMENTED)

godj-conformance:
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MANIFEST) -expected $(ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(WRITE_MIGRATION_MANIFEST) \
		-expected $(WRITE_MIGRATION_ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(SAVE_LIFECYCLE_MANIFEST) \
		-expected $(SAVE_LIFECYCLE_ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(QUERY_CACHE_MANIFEST) \
		-expected $(QUERY_CACHE_ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_PLANNING_MANIFEST) \
		-expected $(MIGRATION_PLANNING_ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_EXECUTION_MANIFEST) \
		-expected $(MIGRATION_EXECUTION_ORACLE) \
		-deviation-expected $(MIGRATION_EXECUTION_DEVIATION_EXPECTED)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_RESTART_MANIFEST) \
		-expected $(MIGRATION_RESTART_ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_STATE_RECONSTRUCTION_MANIFEST) \
		-expected $(MIGRATION_STATE_RECONSTRUCTION_ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_LIFECYCLE_MANIFEST) \
		-expected $(MIGRATION_LIFECYCLE_ORACLE) \
		-deviation-expected $(MIGRATION_LIFECYCLE_DEVIATION_EXPECTED)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_DEFINITION_SOURCE_MANIFEST) \
		-expected $(MIGRATION_DEFINITION_SOURCE_ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MIGRATION_PROJECT_CHECK_MANIFEST) \
		-expected $(MIGRATION_PROJECT_CHECK_ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(RELATION_MANIFEST) \
		-expected $(RELATION_ORACLE)

oracle-check:
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MANIFEST) --output $(ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(WRITE_MIGRATION_MANIFEST) \
		--output $(WRITE_MIGRATION_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(SAVE_LIFECYCLE_MANIFEST) \
		--output $(SAVE_LIFECYCLE_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(QUERY_CACHE_MANIFEST) \
		--output $(QUERY_CACHE_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_PLANNING_MANIFEST) \
		--output $(MIGRATION_PLANNING_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_EXECUTION_MANIFEST) \
		--output $(MIGRATION_EXECUTION_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_RESTART_MANIFEST) \
		--output $(MIGRATION_RESTART_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_STATE_RECONSTRUCTION_MANIFEST) \
		--output $(MIGRATION_STATE_RECONSTRUCTION_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_LIFECYCLE_MANIFEST) \
		--output $(MIGRATION_LIFECYCLE_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_DEFINITION_SOURCE_MANIFEST) \
		--output $(MIGRATION_DEFINITION_SOURCE_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_PROJECT_CHECK_MANIFEST) \
		--output $(MIGRATION_PROJECT_CHECK_ORACLE) --check
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(RELATION_MANIFEST) \
		--output $(RELATION_ORACLE) --check

oracle-regenerate:
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MANIFEST) --output $(ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(WRITE_MIGRATION_MANIFEST) \
		--output $(WRITE_MIGRATION_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(SAVE_LIFECYCLE_MANIFEST) \
		--output $(SAVE_LIFECYCLE_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(QUERY_CACHE_MANIFEST) \
		--output $(QUERY_CACHE_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_PLANNING_MANIFEST) \
		--output $(MIGRATION_PLANNING_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_EXECUTION_MANIFEST) \
		--output $(MIGRATION_EXECUTION_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_RESTART_MANIFEST) \
		--output $(MIGRATION_RESTART_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_STATE_RECONSTRUCTION_MANIFEST) \
		--output $(MIGRATION_STATE_RECONSTRUCTION_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_LIFECYCLE_MANIFEST) \
		--output $(MIGRATION_LIFECYCLE_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_DEFINITION_SOURCE_MANIFEST) \
		--output $(MIGRATION_DEFINITION_SOURCE_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MIGRATION_PROJECT_CHECK_MANIFEST) \
		--output $(MIGRATION_PROJECT_CHECK_ORACLE)
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(RELATION_MANIFEST) \
		--output $(RELATION_ORACLE)

ci: format-check generate-check go-test go-vet go-race cgo-zero-build python-test conformance-check godj-conformance

check: ci python-test-exact oracle-check
