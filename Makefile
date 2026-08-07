PROFILE := conformance/profiles/django-6.1-sqlite-darwin-arm64.json
MANIFEST := conformance/contracts/manifest.json
ORACLE := conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
NOT_IMPLEMENTED := conformance/fixtures/godj-not-implemented.json

.PHONY: check ci conformance-check go-test oracle-check oracle-regenerate python-test python-test-exact

go-test:
	go test ./...

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

oracle-check:
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MANIFEST) --output $(ORACLE) --check

oracle-regenerate:
	LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
		--profile $(PROFILE) --manifest $(MANIFEST) --output $(ORACLE)

ci: go-test python-test conformance-check

check: ci python-test-exact oracle-check
