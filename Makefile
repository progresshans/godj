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
	CGO_ENABLED=0 go test ./db/sqlite ./conformance/runners/godj -count=1

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

godj-conformance:
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MANIFEST) -expected $(ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(WRITE_MIGRATION_MANIFEST) \
		-expected $(WRITE_MIGRATION_ORACLE)
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(SAVE_LIFECYCLE_MANIFEST) \
		-expected $(SAVE_LIFECYCLE_ORACLE)

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

ci: format-check generate-check go-test go-vet go-race cgo-zero-build python-test conformance-check godj-conformance

check: ci python-test-exact oracle-check
