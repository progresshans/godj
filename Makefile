PROFILE := conformance/profiles/django-6.1-sqlite-darwin-arm64.json
MANIFEST := conformance/contracts/manifest.json
ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
NOT_IMPLEMENTED := conformance/fixtures/godj-not-implemented.json

.PHONY: cgo-zero-build check ci conformance-check generate-check godj-conformance go-race go-test go-vet oracle-check oracle-regenerate python-test python-test-exact

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
	LC_ALL=C TZ=UTC uv run --frozen python -m unittest discover \
		-s conformance/runners/django/tests -v

python-test-exact:
	GODJ_EXACT_PROFILE=1 LC_ALL=C TZ=UTC uv run --frozen python -m unittest discover \
		-s conformance/runners/django/tests -v

conformance-check:
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MANIFEST) -suite $(ORACLE)
	go run ./conformance/cmd/contractcheck \
		-profile $(PROFILE) -manifest $(MANIFEST) -suite $(NOT_IMPLEMENTED)

godj-conformance:
	go run ./conformance/cmd/godjcheck \
		-profile $(PROFILE) -manifest $(MANIFEST) -expected $(ORACLE)

oracle-check:
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MANIFEST) --output $(ORACLE) --check

oracle-regenerate:
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MANIFEST) --output $(ORACLE)

ci: generate-check go-test go-vet cgo-zero-build python-test conformance-check godj-conformance

check: ci go-race python-test-exact oracle-check
