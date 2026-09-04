package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRunserverProductWorkflowWiringIsLocked(t *testing.T) {
	t.Parallel()

	root := runserverWorkflowRepositoryRoot(t)
	operatorFixture := runserverWorkflowReadFile(t, filepath.Join(root, "conformance", "projectoperatorproduct", "external_fixture_unix_test.go"))
	for _, captureControl := range []string{
		`GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE`,
		`operatorPostgresAttestationCaptureEnvironment`,
	} {
		runserverWorkflowRequireCount(t, "operator external environment sanitizer and positive control", operatorFixture, captureControl, 2)
	}
	makefile := runserverWorkflowReadFile(t, filepath.Join(root, "Makefile"))
	for _, fragment := range []string{
		"PROJECT_MIGRATE_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectmigrateproduct",
		"PROJECT_MIGRATE_TARGET_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectmigratetargetproduct",
		"PROJECT_SHOWMIGRATIONS_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectshowmigrationsproduct",
		"PROJECT_SQLMIGRATE_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectsqlmigrateproduct",
		"PROJECT_OPERATOR_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectoperatorproduct",
		"PROJECT_OPERATOR_PORTABLE_TEST_REGEX := ^(TestOperatorSanitizeEnvironmentDropsHostOnlyControls|TestGlobalCreatesuperuserExternalSQLiteProduct|TestOperatorCanonicalSchemaRowsSortsAndFramesWithoutAmbiguity|TestOperatorSQLiteSchemaSnapshotDetectsCatalogMutation|TestOperatorCountRawSecretOccurrencesDetectsAuditMarker)$$",
		"RUNSERVER_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/runserverproduct",
		"MIGRATION_WRITER_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/migrationwriterproduct",
		"GODJ_RUNNER_IMPORT := github.com/progresshans/godj/conformance/runners/godj",
		"GODJCHECK_IMPORT := github.com/progresshans/godj/conformance/cmd/godjcheck",
		"MULTIRUNTIME_WORKER_IMPORT := github.com/progresshans/godj/conformance/systemstate/multiruntimeworker",
		"PORTABLE_HEAVY_PACKAGES := ./conformance/runners/godj ./conformance/cmd/godjcheck ./conformance/systemstate/multiruntimeworker",
		"PORTABLE_CGO0_HEAVY_PACKAGES := ./conformance/runners/godj ./conformance/cmd/godjcheck",
		".PHONY: cgo-zero-build cgo-zero-build-core cgo-zero-build-operator cgo-zero-build-products check ci conformance-check core-package-selection-check format-check generate-check godj-conformance go-race go-race-core go-race-operator go-race-products go-test go-test-core go-test-operator go-test-products go-vet oracle-check oracle-regenerate project-command-dependencies project-operator-product python-test python-test-exact targeted-migrate-product\n",
		"ci: format-check generate-check go-test go-vet go-race cgo-zero-build targeted-migrate-product python-test conformance-check godj-conformance",
	} {
		runserverWorkflowRequireCount(t, "Makefile", makefile, fragment, 1)
	}
	for _, header := range []string{
		"project-command-dependencies:",
		"core-package-selection-check:",
		"go-test-core:",
		"go-test-products: project-command-dependencies",
		"go-test-operator: project-command-dependencies",
		"go-test: go-test-core go-test-products go-test-operator",
		"go-vet:",
		"go-race-core:",
		"go-race-products: project-command-dependencies",
		"go-race-operator: project-command-dependencies",
		"go-race: go-race-core go-race-products go-race-operator",
		"cgo-zero-build-core:",
		"cgo-zero-build-products: project-command-dependencies",
		"cgo-zero-build-operator: project-command-dependencies",
		"cgo-zero-build: cgo-zero-build-core cgo-zero-build-products cgo-zero-build-operator",
		"project-operator-product: go-test-operator go-race-operator cgo-zero-build-operator",
		"targeted-migrate-product: project-command-dependencies",
		"python-test:",
	} {
		runserverWorkflowRequireCount(t, "Makefile target header", makefile, "\n"+header+"\n", 1)
	}
	projectCommandDependencies := runserverWorkflowMakeTarget(t, makefile, "project-command-dependencies", "core-package-selection-check")
	runserverWorkflowRequireRecipeLine(t, "Makefile project command dependency prewarm", projectCommandDependencies, "go mod download all", 1)
	for _, target := range []string{
		"go-test-products",
		"go-test-operator",
		"go-race-products",
		"go-race-operator",
		"cgo-zero-build-products",
		"cgo-zero-build-operator",
		"targeted-migrate-product",
	} {
		runserverWorkflowRequireCount(t, "Makefile project command dependency prewarm", makefile, target+": project-command-dependencies", 1)
	}
	selector := runserverWorkflowMakeDefinition(t, makefile, "select_core_go_packages")
	for _, fragment := range []string{
		`all_packages="$$(go list ./...)"`,
		`project_migrate_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(PROJECT_MIGRATE_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`project_migrate_target_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(PROJECT_MIGRATE_TARGET_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`project_showmigrations_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(PROJECT_SHOWMIGRATIONS_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`project_sqlmigrate_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(PROJECT_SQLMIGRATE_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`project_operator_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(PROJECT_OPERATOR_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`runserver_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(RUNSERVER_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`migration_writer_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(MIGRATION_WRITER_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`godj_runner_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(GODJ_RUNNER_IMPORT)" { count++ } END { print count + 0 }')"`,
		`godjcheck_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(GODJCHECK_IMPORT)" { count++ } END { print count + 0 }')"`,
		`multiruntime_worker_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(MULTIRUNTIME_WORKER_IMPORT)" { count++ } END { print count + 0 }')"`,
		`test "$$project_migrate_count" -eq 1`,
		`test "$$project_migrate_target_count" -eq 1`,
		`test "$$project_showmigrations_count" -eq 1`,
		`test "$$project_sqlmigrate_count" -eq 1`,
		`test "$$project_operator_count" -eq 1`,
		`test "$$runserver_count" -eq 1`,
		`test "$$migration_writer_count" -eq 1`,
		`test "$$godj_runner_count" -eq 1`,
		`test "$$godjcheck_count" -eq 1`,
		`test "$$multiruntime_worker_count" -eq 1`,
		`core_packages="$$(printf '%s\n' "$$all_packages" | awk '$$0 != "$(PROJECT_MIGRATE_PRODUCT_IMPORT)" && $$0 != "$(PROJECT_MIGRATE_TARGET_PRODUCT_IMPORT)" && $$0 != "$(PROJECT_SHOWMIGRATIONS_PRODUCT_IMPORT)" && $$0 != "$(PROJECT_SQLMIGRATE_PRODUCT_IMPORT)" && $$0 != "$(PROJECT_OPERATOR_PRODUCT_IMPORT)" && $$0 != "$(RUNSERVER_PRODUCT_IMPORT)" && $$0 != "$(MIGRATION_WRITER_PRODUCT_IMPORT)" && $$0 != "$(GODJ_RUNNER_IMPORT)" && $$0 != "$(GODJCHECK_IMPORT)" && $$0 != "$(MULTIRUNTIME_WORKER_IMPORT)"')"`,
		`test -n "$$core_packages"`,
		`all_count="$$(printf '%s\n' "$$all_packages" | awk 'NF { count++ } END { print count + 0 }')"`,
		`core_count="$$(printf '%s\n' "$$core_packages" | awk 'NF { count++ } END { print count + 0 }')"`,
		`test "$$all_count" -eq "$$((core_count + 10))"`,
	} {
		runserverWorkflowRequireCount(t, "Makefile core package selector", selector, fragment, 1)
	}
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(PROJECT_MIGRATE_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(PROJECT_MIGRATE_TARGET_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(PROJECT_SHOWMIGRATIONS_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(PROJECT_SQLMIGRATE_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(PROJECT_OPERATOR_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(RUNSERVER_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(MIGRATION_WRITER_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(GODJ_RUNNER_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(GODJCHECK_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(MULTIRUNTIME_WORKER_IMPORT)", 2)

	selectionCheck := runserverWorkflowMakeTarget(t, makefile, "core-package-selection-check", "go-test-core")
	runserverWorkflowRequireRecipeLine(t, "Makefile core package selection check", selectionCheck, `$(select_core_go_packages); \`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile core package selection check", selectionCheck, `printf '%s\n' "$$core_packages"`, 1)

	normalCore := runserverWorkflowMakeTarget(t, makefile, "go-test-core", "go-test-products")
	runserverWorkflowRequireRecipeLine(t, "Makefile normal core gate", normalCore, `$(select_core_go_packages); \`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal core gate", normalCore, `go test -timeout=20m $$core_packages`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal isolated conformance gate", normalCore, "go test -timeout=20m -p=1 -count=1 $(PORTABLE_HEAVY_PACKAGES)", 1)
	normalProducts := runserverWorkflowMakeTarget(t, makefile, "go-test-products", "go-test-operator")
	normalProductCommands := []string{
		"go test -timeout=15m -count=1 ./conformance/projectmigrateproduct",
		"go test -timeout=15m -count=1 ./conformance/projectshowmigrationsproduct",
		"go test -timeout=15m -count=1 ./conformance/projectsqlmigrateproduct",
		"go test -timeout=15m -count=1 ./conformance/runserverproduct",
		"go test -timeout=15m -count=1 ./conformance/migrationwriterproduct",
	}
	for _, command := range normalProductCommands {
		runserverWorkflowRequireRecipeLine(t, "Makefile normal products gate", normalProducts, command, 1)
	}
	normalOperator := runserverWorkflowMakeTarget(t, makefile, "go-test-operator", "go-vet")
	runserverWorkflowRequireRecipeLine(t, "Makefile normal operator product gate", normalOperator, "go test -timeout=25m -count=1 -run '$(PROJECT_OPERATOR_PORTABLE_TEST_REGEX)' ./conformance/projectoperatorproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile normal aggregate", makefile, "go-test: go-test-core go-test-products go-test-operator", 1)

	raceCore := runserverWorkflowMakeTarget(t, makefile, "go-race-core", "go-race-products")
	runserverWorkflowRequireRecipeLine(t, "Makefile race core gate", raceCore, `$(select_core_go_packages); \`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race core gate", raceCore, `go test -timeout=20m -race $$core_packages`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race isolated conformance gate", raceCore, "go test -timeout=20m -p=1 -race -count=1 $(PORTABLE_HEAVY_PACKAGES)", 1)
	raceProducts := runserverWorkflowMakeTarget(t, makefile, "go-race-products", "go-race-operator")
	for _, command := range []string{
		"go test -timeout=15m -race -count=1 ./conformance/projectmigrateproduct",
		"go test -timeout=15m -race -count=1 ./conformance/projectshowmigrationsproduct",
		"go test -timeout=15m -race -count=1 ./conformance/projectsqlmigrateproduct",
		"go test -timeout=15m -race -count=1 ./conformance/runserverproduct",
		"go test -timeout=15m -race -count=1 ./conformance/migrationwriterproduct",
	} {
		runserverWorkflowRequireRecipeLine(t, "Makefile race products gate", raceProducts, command, 1)
	}
	raceOperator := runserverWorkflowMakeTarget(t, makefile, "go-race-operator", "cgo-zero-build-core")
	runserverWorkflowRequireRecipeLine(t, "Makefile race operator product gate", raceOperator, "go test -timeout=25m -race -count=1 -run '$(PROJECT_OPERATOR_PORTABLE_TEST_REGEX)' ./conformance/projectoperatorproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile race aggregate", makefile, "go-race: go-race-core go-race-products go-race-operator", 1)

	cgoZeroCore := runserverWorkflowMakeTarget(t, makefile, "cgo-zero-build-core", "cgo-zero-build-products")
	runserverWorkflowRequireCount(t, "Makefile CGO-disabled core gate", cgoZeroCore, "CGO_ENABLED=0 go test -timeout=20m \\\n", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled isolated conformance gate", cgoZeroCore, "CGO_ENABLED=0 go test -timeout=20m -p=1 -count=1 $(PORTABLE_CGO0_HEAVY_PACKAGES)", 1)
	cgoZeroProducts := runserverWorkflowMakeTarget(t, makefile, "cgo-zero-build-products", "cgo-zero-build-operator")
	for _, command := range []string{
		"CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/projectmigrateproduct",
		"CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/projectshowmigrationsproduct",
		"CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/projectsqlmigrateproduct",
		"CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/runserverproduct",
		"CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/migrationwriterproduct",
	} {
		runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled products gate", cgoZeroProducts, command, 1)
	}
	cgoZeroOperator := runserverWorkflowMakeTarget(t, makefile, "cgo-zero-build-operator", "targeted-migrate-product")
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled operator product gate", cgoZeroOperator, "CGO_ENABLED=0 go test -timeout=25m -count=1 -run '$(PROJECT_OPERATOR_PORTABLE_TEST_REGEX)' ./conformance/projectoperatorproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile CGO-disabled aggregate", makefile, "cgo-zero-build: cgo-zero-build-core cgo-zero-build-products cgo-zero-build-operator", 1)
	runserverWorkflowRequireCount(t, "Makefile operator aggregate", makefile, "project-operator-product: go-test-operator go-race-operator cgo-zero-build-operator", 1)
	for _, packagePattern := range []string{
		"./db/postgres",
		"./examples/article",
		"./conformance/postgresproduct/...",
		"./conformance/systemstate/restart",
	} {
		runserverWorkflowRequireCount(t, "Makefile PostgreSQL CGO-disabled coverage", cgoZeroCore, packagePattern, 1)
	}
	for _, products := range []struct {
		name string
		text string
	}{
		{name: "normal", text: normalProducts},
		{name: "race", text: raceProducts},
		{name: "CGO-disabled", text: cgoZeroProducts},
	} {
		runserverWorkflowRequireCount(t, "Makefile "+products.name+" products gate", products.text, "./conformance/projectmigratetargetproduct", 0)
		runserverWorkflowRequireCount(t, "Makefile "+products.name+" products gate", products.text, "./conformance/projectoperatorproduct", 0)
		runserverWorkflowRequireSerialOrder(t, "Makefile "+products.name+" products gate", products.text, "./conformance/projectmigrateproduct", "./conformance/projectshowmigrationsproduct")
		runserverWorkflowRequireSerialOrder(t, "Makefile "+products.name+" products gate", products.text, "./conformance/projectshowmigrationsproduct", "./conformance/projectsqlmigrateproduct")
		runserverWorkflowRequireSerialOrder(t, "Makefile "+products.name+" products gate", products.text, "./conformance/projectsqlmigrateproduct", "./conformance/runserverproduct")
		runserverWorkflowRequireSerialOrder(t, "Makefile "+products.name+" products gate", products.text, "./conformance/runserverproduct", "./conformance/migrationwriterproduct")
	}
	targetedMigrateMake := runserverWorkflowMakeTarget(t, makefile, "targeted-migrate-product", "python-test")
	runserverWorkflowRequireRecipeLine(t, "Makefile targeted-migrate normal gate", targetedMigrateMake, "go test -timeout=30m -count=1 ./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile targeted-migrate race gate", targetedMigrateMake, "go test -timeout=30m -race -count=1 ./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile targeted-migrate CGO-disabled gate", targetedMigrateMake, "CGO_ENABLED=0 go test -timeout=30m -count=1 ./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile targeted-migrate gate", targetedMigrateMake, "./conformance/projectmigratetargetproduct", 3)
	runserverWorkflowRequireSerialOrder(t, "Makefile targeted-migrate modes", targetedMigrateMake, "go test -timeout=30m -count=1", "go test -timeout=30m -race -count=1")
	runserverWorkflowRequireSerialOrder(t, "Makefile targeted-migrate modes", targetedMigrateMake, "go test -timeout=30m -race -count=1", "CGO_ENABLED=0 go test -timeout=30m -count=1")
	for scope, target := range map[string]string{
		"normal core":           normalCore,
		"normal products":       normalProducts,
		"normal operator":       normalOperator,
		"race core":             raceCore,
		"race products":         raceProducts,
		"race operator":         raceOperator,
		"CGO-disabled core":     cgoZeroCore,
		"CGO-disabled products": cgoZeroProducts,
		"CGO-disabled operator": cgoZeroOperator,
		"targeted":              targetedMigrateMake,
	} {
		if strings.Contains(target, "|| true") || strings.Contains(target, "continue-on-error:") {
			t.Fatalf("Makefile %s product gates must remain required", scope)
		}
	}

	workflow := runserverWorkflowReadFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	jobsMarker := "jobs:\n"
	if strings.Count(workflow, jobsMarker) != 1 {
		t.Fatalf("workflow jobs marker count = %d, want 1", strings.Count(workflow, jobsMarker))
	}
	jobs := workflow[strings.Index(workflow, jobsMarker)+len(jobsMarker):]

	conformance := runserverWorkflowJob(t, jobs, "conformance-validation")
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "timeout-minutes:", 1)
	runserverWorkflowRequireExactLine(t, "conformance-validation job", conformance, "    timeout-minutes: 30", 1)
	portableValidation := runserverWorkflowStep(
		t,
		conformance,
		"Run portable artifact and contract validation",
		"Validate project-linked migration check contracts",
	)
	runserverWorkflowRequireCount(t, "portable conformance validation step", portableValidation, "run:", 1)
	runserverWorkflowRequireExactLine(t, "portable conformance validation step", portableValidation, "        run: make format-check generate-check python-test conformance-check godj-conformance", 1)
	if strings.Contains(conformance, "continue-on-error:") || strings.Contains(conformance, "|| true") {
		t.Fatal("portable conformance validation gates must remain required")
	}
	productCompile386 := runserverWorkflowStep(
		t,
		conformance,
		"Compile migration definition and project-check products on 32-bit Linux",
		"Compile runserver product on 32-bit Linux",
	)
	for _, fragment := range []string{
		`CGO_ENABLED: "0"`,
		`GOARCH: "386"`,
		`go test -run '^$' -count=1`,
		`./conformance/projectmigratetargetproduct`,
		`./conformance/projectsqlmigrateproduct`,
		`./conformance/projectoperatorproduct`,
	} {
		runserverWorkflowRequireCount(t, "targeted migrate 386 compile step", productCompile386, fragment, 1)
	}
	compile386 := runserverWorkflowStep(
		t,
		conformance,
		"Compile runserver product on 32-bit Linux",
		"Run relation products on 32-bit Linux",
	)
	for _, fragment := range []string{
		`CGO_ENABLED: "0"`,
		`GOARCH: "386"`,
		`run: go test -run '^$' -count=1 -exec=/usr/bin/true ./conformance/runserverproduct`,
	} {
		runserverWorkflowRequireCount(t, "runserver 386 compile step", compile386, fragment, 1)
	}
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "./conformance/runserverproduct", 1)
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "./conformance/projectshowmigrationsproduct", 1)
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "./conformance/projectsqlmigrateproduct", 1)
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "./conformance/projectoperatorproduct", 1)
	portableGo := runserverWorkflowJob(t, jobs, "portable-go-matrix")
	for _, fragment := range []string{
		"name: Portable Go (${{ matrix.mode }}, ${{ matrix.shard }})",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"- mode: normal\n            shard: core-vet\n            make_targets: \"go-test-core go-vet\"\n            timeout_minutes: 40",
		"- mode: normal\n            shard: products\n            make_targets: \"go-test-products\"\n            timeout_minutes: 55",
		"- mode: race\n            shard: core\n            make_targets: \"go-race-core\"\n            timeout_minutes: 40",
		"- mode: race\n            shard: products\n            make_targets: \"go-race-products\"\n            timeout_minutes: 55",
		"- mode: cgo0\n            shard: core\n            make_targets: \"cgo-zero-build-core\"\n            timeout_minutes: 30",
		"- mode: cgo0\n            shard: products\n            make_targets: \"cgo-zero-build-products\"\n            timeout_minutes: 50",
		"run: make ${{ matrix.make_targets }}",
	} {
		runserverWorkflowRequireCount(t, "portable-go-matrix job", portableGo, fragment, 1)
	}
	runserverWorkflowRequireCount(t, "portable-go-matrix job", portableGo, "          - mode: ", 6)
	runserverWorkflowRequireCount(t, "portable-go-matrix job", portableGo, "            shard: ", 6)
	runserverWorkflowRequireCount(t, "portable-go-matrix job", portableGo, "            make_targets: ", 6)
	runserverWorkflowRequireCount(t, "portable-go-matrix job", portableGo, "            timeout_minutes: ", 6)
	for _, excludedTarget := range []string{
		`make_targets: "go-test"`,
		`make_targets: "go-race"`,
		`make_targets: "cgo-zero-build"`,
		"go-test-operator",
		"go-race-operator",
		"cgo-zero-build-operator",
	} {
		runserverWorkflowRequireCount(t, "portable-go-matrix operator exclusion", portableGo, excludedTarget, 0)
	}
	if strings.Contains(portableGo, "continue-on-error:") || strings.Contains(portableGo, "|| true") {
		t.Fatal("portable Go matrix gates must remain required")
	}

	portable := runserverWorkflowJob(t, jobs, "product-project-check-matrix")
	for _, fragment := range []string{
		"name: Product project check (${{ matrix.runs_on }}, ${{ matrix.mode }})",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		`mode="${{ matrix.mode }}"`,
		`test_flags=(-timeout=20m -count=1)`,
		`json_flags=(-timeout=15m -json -count=1)`,
		`go test "${test_flags[@]}" ./cmd/godj ./project ./internal/projectcheck/...`,
		`go test "${test_flags[@]}" ./conformance/runners/godj`,
	} {
		runserverWorkflowRequireCount(t, "product-project-check-matrix job", portable, fragment, 1)
	}
	runserverWorkflowRequireCount(t, "product-project-check-matrix job", portable, "          - runs_on: ", 12)
	runserverWorkflowRequireCount(t, "product-project-check-matrix job", portable, "timeout_minutes:", 12)
	productCoordinates := []string{
		"- runs_on: ubuntu-22.04\n            expected_goos: linux\n            expected_goarch: amd64",
		"- runs_on: ubuntu-24.04-arm\n            expected_goos: linux\n            expected_goarch: arm64",
		"- runs_on: macos-15-intel\n            expected_goos: darwin\n            expected_goarch: amd64",
		"- runs_on: macos-26\n            expected_goos: darwin\n            expected_goarch: arm64",
	}
	for _, coordinate := range productCoordinates {
		for _, mode := range []string{"normal", "race", "cgo0"} {
			timeoutMinutes := 45
			switch {
			case strings.Contains(coordinate, "ubuntu-24.04-arm"):
				timeoutMinutes = 40
				if mode == "race" {
					timeoutMinutes = 50
				}
			case strings.Contains(coordinate, "macos-15-intel"):
				timeoutMinutes = 55
				if mode == "race" {
					timeoutMinutes = 65
				}
			default:
				if mode == "race" {
					timeoutMinutes = 55
				}
			}
			entry := coordinate + "\n            mode: " + mode + fmt.Sprintf("\n            timeout_minutes: %d", timeoutMinutes)
			runserverWorkflowRequireCount(t, "product-project-check coordinate/mode", portable, entry, 1)
		}
	}
	portableMode := runserverWorkflowStep(
		t,
		portable,
		"Run and inventory product project-check mode",
		"Require a clean worktree",
	)
	for _, fragment := range []string{
		"set -euo pipefail",
		`runserver_log="$RUNNER_TEMP/runserver-product-${mode}.json"`,
		`go test "${json_flags[@]}" ./conformance/runserverproduct > "$runserver_log" || status=$?`,
		`runserver_package="github.com/progresshans/godj/conformance/runserverproduct"`,
		"runserver_required=(",
		`for test_name in "${runserver_required[@]}"; do`,
		`pass_fragment="\"Action\":\"pass\",\"Package\":\"$runserver_package\",\"Test\":\"$test_name\""`,
		`skip_fragment="\"Action\":\"skip\",\"Package\":\"$runserver_package\",\"Test\":\"$test_name\""`,
		`if ! grep -Fq "$pass_fragment" "$runserver_log"; then`,
		`if grep -Fq "$skip_fragment" "$runserver_log"; then`,
	} {
		runserverWorkflowRequireCount(t, "portable runserver inventory", portableMode, fragment, 1)
	}
	runserverWorkflowRequireCount(t, "portable mode status ownership", portableMode, "status=0", 1)
	runserverWorkflowRequireCount(t, "portable mode failure guard", portableMode, `if [ "$status" -ne 0 ]; then`, 1)
	runserverWorkflowRequireCount(t, "portable mode failure exit", portableMode, `exit "$status"`, 1)
	for _, sentinel := range []string{
		"TestGlobalRunserverArticleSQLiteDevelopmentLoop",
		"TestGlobalRunserverPublishesAuthenticatedArticleAdminAndAPI",
		"TestGlobalRunserverRejectsStaleCopiedArticleBeforeRuntime",
		"TestRunserverHarnessForcedCleanupIncludesSeparateDescendantGroup",
	} {
		runserverWorkflowRequireCount(t, "portable runserver inventory", portableMode, sentinel, 1)
	}
	runserverWorkflowRequireCount(t, "product-project-check-matrix job", portable, "./conformance/runserverproduct", 2)
	runserverWorkflowRequireCount(t, "product-project-check-matrix operator exclusion", portable, "projectoperatorproduct", 0)
	if strings.Contains(portable, "for mode in normal race cgo0") {
		t.Fatal("product project-check mode job must not rerun all three modes internally")
	}
	if strings.Contains(portable, "continue-on-error:") || strings.Contains(portable, "|| true") {
		t.Fatal("portable runserver product gates must remain required")
	}

	operator := runserverWorkflowJob(t, jobs, "project-operator-product-matrix")
	for _, fragment := range []string{
		"name: Project operator product (${{ matrix.runs_on }}, ${{ matrix.mode }})",
		"runs-on: ${{ matrix.runs_on }}",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"go-version: \"1.26.5\"",
		`test "$(go env GOOS)" = "${{ matrix.expected_goos }}"`,
		`test "$(go env GOARCH)" = "${{ matrix.expected_goarch }}"`,
		"run: go mod download all",
		"git diff --exit-code",
		`test -z "$(git status --porcelain=v1)"`,
	} {
		runserverWorkflowRequireCount(t, "project-operator-product-matrix job", operator, fragment, 1)
	}
	operatorCoordinates := []struct {
		coordinate     string
		testTimeout    string
		timeoutMinutes int
	}{
		{coordinate: "- runs_on: ubuntu-22.04\n            expected_goos: linux\n            expected_goarch: amd64", testTimeout: "30m", timeoutMinutes: 40},
		{coordinate: "- runs_on: ubuntu-24.04-arm\n            expected_goos: linux\n            expected_goarch: arm64", testTimeout: "30m", timeoutMinutes: 40},
		{coordinate: "- runs_on: macos-15-intel\n            expected_goos: darwin\n            expected_goarch: amd64", testTimeout: "45m", timeoutMinutes: 55},
		{coordinate: "- runs_on: macos-26\n            expected_goos: darwin\n            expected_goarch: arm64", testTimeout: "35m", timeoutMinutes: 45},
	}
	for _, coordinate := range operatorCoordinates {
		for _, mode := range []string{"normal", "race", "cgo0"} {
			entry := coordinate.coordinate + "\n            mode: " + mode +
				"\n            test_timeout: " + coordinate.testTimeout +
				fmt.Sprintf("\n            timeout_minutes: %d", coordinate.timeoutMinutes)
			runserverWorkflowRequireCount(t, "project-operator-product coordinate/mode", operator, entry, 1)
		}
	}
	runserverWorkflowRequireCount(t, "project-operator-product-matrix job", operator, "          - runs_on: ", 12)
	runserverWorkflowRequireCount(t, "project-operator-product-matrix job", operator, "test_timeout:", 12)
	runserverWorkflowRequireCount(t, "project-operator-product-matrix job", operator, "timeout_minutes:", 12)
	operatorMode := runserverWorkflowStep(
		t,
		operator,
		"Run and inventory project operator product mode",
		"Require a clean worktree",
	)
	for label, block := range map[string]string{
		"mode switch": `          case "$mode" in
            normal)
              ;;
            race)
              test_flags+=(-race)
              ;;
            cgo0)
              export CGO_ENABLED=0
              ;;
            *)
              echo "unsupported project operator product mode: $mode" >&2
              exit 1
              ;;
          esac`,
		"top-level inventory": `          runs = {
              (event.get("Package"), event["Test"])
              for event in events
              if event.get("Action") == "run" and "Test" in event and "/" not in event["Test"]
          }
          passes = {
              (event.get("Package"), event["Test"])
              for event in events
              if event.get("Action") == "pass" and "Test" in event and "/" not in event["Test"]
          }`,
	} {
		runserverWorkflowRequireCount(t, "project operator product "+label, operatorMode, block, 1)
	}
	for _, fragment := range []string{
		"set -euo pipefail",
		`mode="${{ matrix.mode }}"`,
		`test_flags=(-timeout="${{ matrix.test_timeout }}" -json -count=1)`,
		`operator_package="github.com/progresshans/godj/conformance/projectoperatorproduct"`,
		`required_tests=(`,
		`required_regex="^($(IFS='|'; printf '%s' "${required_tests[*]}"))$"`,
		`required="$RUNNER_TEMP/project-operator-sqlite-required-tests.txt"`,
		`printf '%s\n' "${required_tests[@]}" > "$required"`,
		`operator_log="$RUNNER_TEMP/project-operator-sqlite-${mode}.json"`,
		`go test "${test_flags[@]}" -run "$required_regex" ./conformance/projectoperatorproduct > "$operator_log" || status=$?`,
		`python3 - "$required" "$operator_log" "$operator_package" "$mode" <<'PY'`,
		`assert len(expected) == 5, sorted(expected)`,
		`assert runs == expected, (sys.argv[4], sorted(runs), sorted(expected))`,
		`assert passes == expected, (sys.argv[4], sorted(passes), sorted(expected))`,
		`assert skips == [], (sys.argv[4], skips)`,
		`if [ "$mode" = "normal" ]; then`,
		"go vet ./conformance/projectoperatorproduct",
	} {
		runserverWorkflowRequireCount(t, "project operator product mode", operatorMode, fragment, 1)
	}
	for _, sentinel := range []string{
		"TestOperatorSanitizeEnvironmentDropsHostOnlyControls",
		"TestGlobalCreatesuperuserExternalSQLiteProduct",
		"TestOperatorCanonicalSchemaRowsSortsAndFramesWithoutAmbiguity",
		"TestOperatorSQLiteSchemaSnapshotDetectsCatalogMutation",
		"TestOperatorCountRawSecretOccurrencesDetectsAuditMarker",
	} {
		runserverWorkflowRequireCount(t, "project operator product required inventory", operatorMode, sentinel, 1)
	}
	for _, line := range []string{
		`          python3 - "$required" "$operator_log" "$operator_package" "$mode" <<'PY'`,
		`          import json`,
		`          PY`,
	} {
		runserverWorkflowRequireExactLine(t, "project operator inventory heredoc indentation", operatorMode, line, 1)
	}
	runserverWorkflowRequireCount(t, "project operator status ownership", operatorMode, "status=0", 1)
	runserverWorkflowRequireCount(t, "project operator failure guard", operatorMode, `if [ "$status" -ne 0 ]; then`, 1)
	runserverWorkflowRequireCount(t, "project operator failure exit", operatorMode, `exit "$status"`, 1)
	runserverWorkflowRequireCount(t, "project-operator-product-matrix job", operator, "./conformance/projectoperatorproduct", 2)
	if strings.Contains(operator, "for mode in normal race cgo0") {
		t.Fatal("project operator mode job must not rerun all three modes internally")
	}
	if strings.Contains(operator, "continue-on-error:") || strings.Contains(operator, "|| true") {
		t.Fatal("project operator product gates must remain required")
	}

	requiredCI := runserverWorkflowJob(t, jobs, "required-ci")
	runserverWorkflowRequireExactLine(t, "required-ci needs", requiredCI, "      - project-operator-product-matrix", 1)
	runserverWorkflowRequireExactLine(t, "required-ci expected lanes", requiredCI, `              "project-operator-product-matrix",`, 1)

	targetedMigrate := runserverWorkflowJob(t, jobs, "targeted-migrate-product-matrix")
	for _, fragment := range []string{
		"name: Targeted migrate product (${{ matrix.runs_on }}, ${{ matrix.mode }})",
		"runs-on: ${{ matrix.runs_on }}",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"go-version: \"1.26.5\"",
		`test "$(go env GOOS)" = "${{ matrix.expected_goos }}"`,
		`test "$(go env GOARCH)" = "${{ matrix.expected_goarch }}"`,
		"git diff --exit-code",
		`test -z "$(git status --porcelain=v1)"`,
	} {
		runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, fragment, 1)
	}
	coordinates := []string{
		"- runs_on: ubuntu-22.04\n            expected_goos: linux\n            expected_goarch: amd64",
		"- runs_on: ubuntu-24.04-arm\n            expected_goos: linux\n            expected_goarch: arm64",
		"- runs_on: macos-15-intel\n            expected_goos: darwin\n            expected_goarch: amd64",
		"- runs_on: macos-26\n            expected_goos: darwin\n            expected_goarch: arm64",
	}
	for _, coordinate := range coordinates {
		for _, mode := range []string{"normal", "race", "cgo0"} {
			timeoutMinutes := 40
			testTimeout := "30m"
			if strings.Contains(coordinate, "runs_on: macos-15-intel") {
				testTimeout = "45m"
				timeoutMinutes = 55
			}
			entry := coordinate + "\n            mode: " + mode +
				"\n            test_timeout: " + testTimeout +
				fmt.Sprintf("\n            timeout_minutes: %d", timeoutMinutes)
			runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix coordinate/mode", targetedMigrate, entry, 1)
		}
	}
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "          - runs_on: ", 12)
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "test_timeout:", 12)
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "test_timeout: 30m", 9)
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "test_timeout: 45m", 3)
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "timeout_minutes:", 12)
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "timeout_minutes: 40", 9)
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "timeout_minutes: 55", 3)
	targetedMode := runserverWorkflowStep(
		t,
		targetedMigrate,
		"Run and inventory targeted migrate product mode",
		"Require a clean worktree",
	)
	for label, block := range map[string]string{
		"mode switch": `          case "$mode" in
            normal)
              ;;
            race)
              test_flags+=(-race)
              ;;
            cgo0)
              export CGO_ENABLED=0
              ;;
            *)
              echo "unsupported targeted migrate product mode: $mode" >&2
              exit 1
              ;;
          esac`,
		"skip failure": `          package_skip_fragment="\"Action\":\"skip\",\"Package\":\"$package\""
          if grep -Fq "$package_skip_fragment" "$log"; then
            echo "targeted migrate product $mode inventory contains a skipped test" >&2
            exit 1
          fi`,
	} {
		runserverWorkflowRequireCount(t, "targeted migrate product "+label, targetedMode, block, 1)
	}
	for _, fragment := range []string{
		"set -euo pipefail",
		`mode="${{ matrix.mode }}"`,
		`test_flags=(-timeout="${{ matrix.test_timeout }}" -json -count=1 -run '^TestProjectLinkedTargetedMigrateSQLite$')`,
		`case "$mode" in`,
		"test_flags+=(-race)",
		"export CGO_ENABLED=0",
		"go mod download all",
		`log="$RUNNER_TEMP/project-migrate-target-product-${mode}.json"`,
		"status=0",
		`go test "${test_flags[@]}" ./conformance/projectmigratetargetproduct > "$log" || status=$?`,
		`if [ "$status" -ne 0 ]; then`,
		`tail -c 60000 "$log"`,
		`exit "$status"`,
		`package="github.com/progresshans/godj/conformance/projectmigratetargetproduct"`,
		"required_passes=(",
		`for test_name in "${required_passes[@]}"; do`,
		`pass_fragment="\"Action\":\"pass\",\"Package\":\"$package\",\"Test\":\"$test_name\""`,
		`if ! grep -Fq "$pass_fragment" "$log"; then`,
		`package_skip_fragment="\"Action\":\"skip\",\"Package\":\"$package\""`,
		`if grep -Fq "$package_skip_fragment" "$log"; then`,
		`if [ "$mode" = "normal" ]; then`,
		"go vet ./conformance/projectmigratetargetproduct",
	} {
		runserverWorkflowRequireCount(t, "targeted migrate product mode step", targetedMode, fragment, 1)
	}
	for _, modeLabel := range []string{"normal", "race", "cgo0"} {
		runserverWorkflowRequireExactLine(t, "targeted migrate product mode switch", targetedMode, "            "+modeLabel+")", 1)
	}
	runserverWorkflowRequireExactLine(t, "targeted migrate product inventory", targetedMode, `            "TestProjectLinkedTargetedMigrateSQLite"`, 1)
	for _, sentinel := range []string{
		"TestProjectLinkedTargetedMigrateSQLite/MIG-119_exact_argv_and_exact_name_miss",
		"TestProjectLinkedTargetedMigrateSQLite/MIG-120_named_forward_closure",
		"TestProjectLinkedTargetedMigrateSQLite/MIG-121_named_reverse_descendants",
		"TestProjectLinkedTargetedMigrateSQLite/MIG-122_app_zero_DEV-0002_order",
		"TestProjectLinkedTargetedMigrateSQLite/MIG-123_noop_and_known_zero_unknown",
		"TestProjectLinkedTargetedMigrateSQLite/MIG-124_plan_is_exact_and_read_only",
		"TestProjectLinkedTargetedMigrateSQLite/MIG-125_preview_drift_fresh_execute",
		"TestProjectLinkedTargetedMigrateSQLite/MIG-126_reverse_middle_failure_fresh_resume",
		"TestProjectLinkedTargetedMigrateSQLite/MIG-128_phase_C_public_ownership_subset",
		"TestProjectLinkedTargetedMigrateSQLite/exact_public_family_coverage",
	} {
		runserverWorkflowRequireCount(t, "targeted migrate product inventory", targetedMode, sentinel, 1)
	}
	if strings.Contains(targetedMode, "TestGlobalTargetedMigratePostgresLifecycle") {
		t.Fatal("portable targeted-migrate mode step must exclude the PostgreSQL-only lifecycle")
	}
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "./conformance/projectmigratetargetproduct", 2)
	if strings.Contains(targetedMigrate, "continue-on-error:") || strings.Contains(targetedMigrate, "|| true") {
		t.Fatal("targeted migrate product matrix gates must remain required")
	}

	postgres := runserverWorkflowJob(t, jobs, "postgresql-product")
	for _, fragment := range []string{
		"name: PostgreSQL 17.10 actual product (${{ matrix.mode }}, ${{ matrix.shard }})",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"- mode: normal\n            shard: core\n            timeout_minutes: 45",
		"- mode: normal\n            shard: operator-target\n            timeout_minutes: 35",
		"- mode: race\n            shard: core\n            timeout_minutes: 40",
		"- mode: race\n            shard: operator-target\n            timeout_minutes: 35",
		"- mode: cgo0\n            shard: core\n            timeout_minutes: 40",
		"- mode: cgo0\n            shard: operator-target\n            timeout_minutes: 35",
		`GODJ_TEST_POSTGRES_URL: postgresql://postgres:godj-ci-pg-canary-8H2k7M4q9V6x3R@127.0.0.1:${{ job.services.postgres.ports[5432] }}/postgres?sslmode=disable`,
		`GODJ_REQUIRE_POSTGRES: "1"`,
		`GODJ_PROJECT_OPERATOR_POSTGRES_ATTESTATION_CAPTURE: ${{ runner.temp }}/postgresql-17.10-sqlite-external-operator-v1.json`,
		`mode="${{ matrix.mode }}"`,
		`shard="${{ matrix.shard }}"`,
		`test_flags=(-p=1 -timeout=18m -json -count=1 -run "$required_regex")`,
		`test_flags+=(-race)`,
		`export CGO_ENABLED=0`,
		`go test "${test_flags[@]}" "${packages[@]}" > "$log" || status=$?`,
		`assert len(expected) == int(sys.argv[4]), sorted(expected)`,
		`assert runs == expected, (sorted(runs), sorted(expected))`,
		`assert passes == expected, (sorted(passes), sorted(expected))`,
		`assert skips == [], skips`,
		`if [ "$mode" != "normal" ]; then`,
		`if [ "$shard" = "core" ]; then`,
		`test "$shard" = "operator-target"`,
	} {
		runserverWorkflowRequireCount(t, "PostgreSQL runserver matrix job", postgres, fragment, 1)
	}
	runserverWorkflowRequireCount(t, "PostgreSQL shard-owned vet", postgres, `go vet "${packages[@]}"`, 2)
	runserverWorkflowRequireCount(
		t,
		"postgresql-product job",
		postgres,
		"github.com/progresshans/godj/cmd/godj|TestActualGodjMakemigrationsPostgresGeneratedMigrateNoopRestart",
		1,
	)
	runserverWorkflowRequireCount(
		t,
		"postgresql-product job",
		postgres,
		"github.com/progresshans/godj/conformance/projectoperatorproduct|TestOperatorPostgresSchemaSnapshotDetectsTriggerMutation",
		1,
	)
	runserverWorkflowRequireCount(
		t,
		"postgresql-product job",
		postgres,
		"github.com/progresshans/godj/conformance/projectoperatorproduct|TestGlobalCreatesuperuserExternalPostgresAndSQLiteProduct",
		1,
	)
	runserverWorkflowRequireCount(
		t,
		"postgresql-product job",
		postgres,
		"github.com/progresshans/godj/conformance/runserverproduct|TestGlobalRunserverArticlePostgresDevelopmentLoop",
		1,
	)
	runserverWorkflowRequireCount(
		t,
		"postgresql-product job",
		postgres,
		"github.com/progresshans/godj/conformance/systemstate/restart|TestSystemStatePostgresDistinctProcessRestartSentinel",
		1,
	)
	runserverWorkflowRequireCount(
		t,
		"postgresql-product job",
		postgres,
		"github.com/progresshans/godj/conformance/projectmigrateproduct|TestGlobalMigrateAuthenticatedArticlePostgresRestartDurability",
		1,
	)
	runserverWorkflowRequireCount(
		t,
		"postgresql-product job",
		postgres,
		"github.com/progresshans/godj/conformance/projectshowmigrationsproduct|TestGlobalShowMigrationsPostgresReadOnlyFreshPrefixRestart",
		1,
	)
	runserverWorkflowRequireCount(
		t,
		"postgresql-product job",
		postgres,
		"github.com/progresshans/godj/conformance/projectsqlmigrateproduct|TestGlobalSQLMigrateExternalPhaseDProduct",
		1,
	)
	runserverWorkflowRequireCount(
		t,
		"postgresql-product job",
		postgres,
		"github.com/progresshans/godj/conformance/projectmigratetargetproduct|TestGlobalTargetedMigratePostgresLifecycle",
		1,
	)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, `GODJ_REQUIRE_POSTGRES: "1"`, 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/runserverproduct", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "go mod download all", 1)
	runserverWorkflowRequireSerialOrder(t, "postgresql-product common module prewarm", postgres, "go mod download all", `case "$shard" in`)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./cmd/godj", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/projectshowmigrationsproduct", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/projectsqlmigrateproduct", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/systemstate/restart", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/projectoperatorproduct", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "TestGlobalRunserverArticlePostgresDevelopmentLoop", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "TestOperatorPostgresSchemaSnapshotDetectsTriggerMutation", 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "TestGlobalCreatesuperuserExternalPostgresAndSQLiteProduct", 1)
	if strings.Contains(postgres, "continue-on-error:") || strings.Contains(postgres, "|| true") {
		t.Fatal("PostgreSQL runserver product gates must remain required")
	}
}

func runserverWorkflowRepositoryRoot(t *testing.T) string {
	t.Helper()

	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for candidate := filepath.Clean(working); ; candidate = filepath.Dir(candidate) {
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			t.Fatalf("cannot resolve repository root from %s", working)
		}
	}
}

func runserverWorkflowReadFile(t *testing.T, path string) string {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func runserverWorkflowMakeTarget(t *testing.T, makefile, target, nextTarget string) string {
	t.Helper()

	startPattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:[^\n]*\n`)
	endPattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(nextTarget) + `:[^\n]*\n`)
	startMatches := startPattern.FindAllStringIndex(makefile, -1)
	endMatches := endPattern.FindAllStringIndex(makefile, -1)
	if len(startMatches) != 1 || len(endMatches) != 1 {
		t.Fatalf("cannot isolate Makefile target %q before %q", target, nextTarget)
	}
	start := startMatches[0][0]
	end := endMatches[0][0]
	if end <= start {
		t.Fatalf("Makefile target %q has no following %q target", target, nextTarget)
	}
	return makefile[start:end]
}

func runserverWorkflowMakeDefinition(t *testing.T, makefile, definition string) string {
	t.Helper()

	startMarker := "define " + definition + "\n"
	endMarker := "\nendef\n"
	if strings.Count(makefile, startMarker) != 1 {
		t.Fatalf("Makefile definition %q count = %d, want 1", definition, strings.Count(makefile, startMarker))
	}
	start := strings.Index(makefile, startMarker)
	end := strings.Index(makefile[start+len(startMarker):], endMarker)
	if end < 0 {
		t.Fatalf("Makefile definition %q has no endef", definition)
	}
	return makefile[start : start+len(startMarker)+end]
}

func runserverWorkflowJob(t *testing.T, jobs, job string) string {
	t.Helper()

	startMarker := "  " + job + ":\n"
	if strings.Count(jobs, startMarker) != 1 {
		t.Fatalf("cannot isolate workflow job %q", job)
	}
	start := strings.Index(jobs, startMarker)
	remainder := jobs[start+len(startMarker):]
	end := regexp.MustCompile(`(?m)^  [a-z0-9-]+:$`).FindStringIndex(remainder)
	if end == nil {
		return jobs[start:]
	}
	return jobs[start : start+len(startMarker)+end[0]]
}

func runserverWorkflowStep(t *testing.T, job, step, nextStep string) string {
	t.Helper()

	startMarker := "      - name: " + step + "\n"
	endMarker := "      - name: " + nextStep + "\n"
	if strings.Count(job, startMarker) != 1 || strings.Count(job, endMarker) != 1 {
		t.Fatalf("cannot isolate workflow step %q before %q", step, nextStep)
	}
	start := strings.Index(job, startMarker)
	end := strings.Index(job[start+len(startMarker):], endMarker)
	if end < 0 {
		t.Fatalf("workflow step %q has no following %q step", step, nextStep)
	}
	return job[start : start+len(startMarker)+end]
}

func runserverWorkflowRequireCount(t *testing.T, scope, text, fragment string, want int) {
	t.Helper()

	if got := strings.Count(text, fragment); got != want {
		t.Fatalf("%s fragment %q count = %d, want %d", scope, fragment, got, want)
	}
}

func runserverWorkflowRequireRecipeLine(t *testing.T, scope, target, recipe string, want int) {
	t.Helper()

	got := 0
	for _, line := range strings.Split(target, "\n") {
		if line == "\t"+recipe {
			got++
		}
	}
	if got != want {
		t.Fatalf("%s recipe line %q count = %d, want %d", scope, recipe, got, want)
	}
}

func runserverWorkflowRequireExactLine(t *testing.T, scope, text, line string, want int) {
	t.Helper()

	got := 0
	for _, candidate := range strings.Split(text, "\n") {
		if candidate == line {
			got++
		}
	}
	if got != want {
		t.Fatalf("%s exact line %q count = %d, want %d", scope, line, got, want)
	}
}

func runserverWorkflowRequireSerialOrder(t *testing.T, scope, text, first, second string) {
	t.Helper()

	firstIndex := strings.Index(text, first)
	secondIndex := strings.Index(text, second)
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("%s order invalid: first_index=%d second_index=%d", scope, firstIndex, secondIndex)
	}
}
