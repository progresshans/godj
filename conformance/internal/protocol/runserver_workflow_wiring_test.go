package protocol

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRunserverProductWorkflowWiringIsLocked(t *testing.T) {
	t.Parallel()

	root := runserverWorkflowRepositoryRoot(t)
	makefile := runserverWorkflowReadFile(t, filepath.Join(root, "Makefile"))
	for _, fragment := range []string{
		"PROJECT_MIGRATE_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectmigrateproduct",
		"PROJECT_MIGRATE_TARGET_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectmigratetargetproduct",
		"PROJECT_SHOWMIGRATIONS_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectshowmigrationsproduct",
		"RUNSERVER_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/runserverproduct",
		"MIGRATION_WRITER_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/migrationwriterproduct",
		"GODJ_RUNNER_IMPORT := github.com/progresshans/godj/conformance/runners/godj",
		"GODJCHECK_IMPORT := github.com/progresshans/godj/conformance/cmd/godjcheck",
		"MULTIRUNTIME_WORKER_IMPORT := github.com/progresshans/godj/conformance/systemstate/multiruntimeworker",
		"PORTABLE_HEAVY_PACKAGES := ./conformance/runners/godj ./conformance/cmd/godjcheck ./conformance/systemstate/multiruntimeworker",
		"PORTABLE_CGO0_HEAVY_PACKAGES := ./conformance/runners/godj ./conformance/cmd/godjcheck",
		"ci: format-check generate-check go-test go-vet go-race cgo-zero-build python-test conformance-check godj-conformance",
	} {
		runserverWorkflowRequireCount(t, "Makefile", makefile, fragment, 1)
	}
	selector := runserverWorkflowMakeDefinition(t, makefile, "select_core_go_packages")
	for _, fragment := range []string{
		`all_packages="$$(go list ./...)"`,
		`project_migrate_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(PROJECT_MIGRATE_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`project_migrate_target_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(PROJECT_MIGRATE_TARGET_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`project_showmigrations_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(PROJECT_SHOWMIGRATIONS_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`runserver_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(RUNSERVER_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`migration_writer_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(MIGRATION_WRITER_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`godj_runner_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(GODJ_RUNNER_IMPORT)" { count++ } END { print count + 0 }')"`,
		`godjcheck_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(GODJCHECK_IMPORT)" { count++ } END { print count + 0 }')"`,
		`multiruntime_worker_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(MULTIRUNTIME_WORKER_IMPORT)" { count++ } END { print count + 0 }')"`,
		`test "$$project_migrate_count" -eq 1`,
		`test "$$project_migrate_target_count" -eq 1`,
		`test "$$project_showmigrations_count" -eq 1`,
		`test "$$runserver_count" -eq 1`,
		`test "$$migration_writer_count" -eq 1`,
		`test "$$godj_runner_count" -eq 1`,
		`test "$$godjcheck_count" -eq 1`,
		`test "$$multiruntime_worker_count" -eq 1`,
		`core_packages="$$(printf '%s\n' "$$all_packages" | awk '$$0 != "$(PROJECT_MIGRATE_PRODUCT_IMPORT)" && $$0 != "$(PROJECT_MIGRATE_TARGET_PRODUCT_IMPORT)" && $$0 != "$(PROJECT_SHOWMIGRATIONS_PRODUCT_IMPORT)" && $$0 != "$(RUNSERVER_PRODUCT_IMPORT)" && $$0 != "$(MIGRATION_WRITER_PRODUCT_IMPORT)" && $$0 != "$(GODJ_RUNNER_IMPORT)" && $$0 != "$(GODJCHECK_IMPORT)" && $$0 != "$(MULTIRUNTIME_WORKER_IMPORT)"')"`,
		`test -n "$$core_packages"`,
		`all_count="$$(printf '%s\n' "$$all_packages" | awk 'NF { count++ } END { print count + 0 }')"`,
		`core_count="$$(printf '%s\n' "$$core_packages" | awk 'NF { count++ } END { print count + 0 }')"`,
		`test "$$all_count" -eq "$$((core_count + 8))"`,
	} {
		runserverWorkflowRequireCount(t, "Makefile core package selector", selector, fragment, 1)
	}
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(PROJECT_MIGRATE_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(PROJECT_MIGRATE_TARGET_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(PROJECT_SHOWMIGRATIONS_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(RUNSERVER_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(MIGRATION_WRITER_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(GODJ_RUNNER_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(GODJCHECK_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(MULTIRUNTIME_WORKER_IMPORT)", 2)

	selectionCheck := runserverWorkflowMakeTarget(t, makefile, "core-package-selection-check", "go-test")
	runserverWorkflowRequireRecipeLine(t, "Makefile core package selection check", selectionCheck, `$(select_core_go_packages); \`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile core package selection check", selectionCheck, `printf '%s\n' "$$core_packages"`, 1)

	normal := runserverWorkflowMakeTarget(t, makefile, "go-test", "go-vet")
	runserverWorkflowRequireRecipeLine(t, "Makefile normal core gate", normal, `$(select_core_go_packages); \`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal core gate", normal, `go test $$core_packages`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal isolated conformance gate", normal, "go test -timeout=20m -p=1 -count=1 $(PORTABLE_HEAVY_PACKAGES)", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal project-migrate gate", normal, "go test -timeout=15m -count=1 ./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal targeted-migrate gate", normal, "go test -timeout=15m -count=1 ./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal project-showmigrations gate", normal, "go test -timeout=15m -count=1 ./conformance/projectshowmigrationsproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal runserver gate", normal, "go test -timeout=15m -count=1 ./conformance/runserverproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal migration-writer gate", normal, "go test -timeout=15m -count=1 ./conformance/migrationwriterproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile normal gate", normal, "./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile normal gate", normal, "./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile normal gate", normal, "./conformance/projectshowmigrationsproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile normal gate", normal, "./conformance/runserverproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile normal gate", normal, "./conformance/migrationwriterproduct", 1)
	runserverWorkflowRequireSerialOrder(t, "Makefile normal heavy product gates", normal, "./conformance/projectmigrateproduct", "./conformance/projectmigratetargetproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile normal heavy product gates", normal, "./conformance/projectmigratetargetproduct", "./conformance/projectshowmigrationsproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile normal heavy product gates", normal, "./conformance/projectshowmigrationsproduct", "./conformance/runserverproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile normal heavy product gates", normal, "./conformance/runserverproduct", "./conformance/migrationwriterproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile normal isolated conformance gate", normal, "$(PORTABLE_HEAVY_PACKAGES)", "./conformance/projectmigrateproduct")

	race := runserverWorkflowMakeTarget(t, makefile, "go-race", "cgo-zero-build")
	runserverWorkflowRequireRecipeLine(t, "Makefile race core gate", race, `$(select_core_go_packages); \`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race core gate", race, `go test -race $$core_packages`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race isolated conformance gate", race, "go test -timeout=20m -p=1 -race -count=1 $(PORTABLE_HEAVY_PACKAGES)", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race project-migrate gate", race, "go test -timeout=15m -race -count=1 ./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race targeted-migrate gate", race, "go test -timeout=15m -race -count=1 ./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race project-showmigrations gate", race, "go test -timeout=15m -race -count=1 ./conformance/projectshowmigrationsproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race runserver gate", race, "go test -timeout=15m -race -count=1 ./conformance/runserverproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race migration-writer gate", race, "go test -timeout=15m -race -count=1 ./conformance/migrationwriterproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile race gate", race, "./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile race gate", race, "./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile race gate", race, "./conformance/projectshowmigrationsproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile race gate", race, "./conformance/runserverproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile race gate", race, "./conformance/migrationwriterproduct", 1)
	runserverWorkflowRequireSerialOrder(t, "Makefile race heavy product gates", race, "./conformance/projectmigrateproduct", "./conformance/projectmigratetargetproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile race heavy product gates", race, "./conformance/projectmigratetargetproduct", "./conformance/projectshowmigrationsproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile race heavy product gates", race, "./conformance/projectshowmigrationsproduct", "./conformance/runserverproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile race heavy product gates", race, "./conformance/runserverproduct", "./conformance/migrationwriterproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile race isolated conformance gate", race, "$(PORTABLE_HEAVY_PACKAGES)", "./conformance/projectmigrateproduct")

	cgoZero := runserverWorkflowMakeTarget(t, makefile, "cgo-zero-build", "python-test")
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "CGO_ENABLED=0 go test \\\n", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled isolated conformance gate", cgoZero, "CGO_ENABLED=0 go test -timeout=20m -p=1 -count=1 $(PORTABLE_CGO0_HEAVY_PACKAGES)", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled project-migrate gate", cgoZero, "CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled targeted-migrate gate", cgoZero, "CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled project-showmigrations gate", cgoZero, "CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/projectshowmigrationsproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled runserver gate", cgoZero, "CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/runserverproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled migration-writer gate", cgoZero, "CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/migrationwriterproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "./conformance/projectmigratetargetproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "./conformance/projectshowmigrationsproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "./conformance/runserverproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "./conformance/migrationwriterproduct", 1)
	for _, packagePattern := range []string{
		"./db/postgres",
		"./examples/article",
		"./conformance/postgresproduct/...",
		"./conformance/systemstate/restart",
	} {
		runserverWorkflowRequireCount(t, "Makefile PostgreSQL CGO-disabled coverage", cgoZero, packagePattern, 1)
	}
	runserverWorkflowRequireSerialOrder(t, "Makefile CGO-disabled heavy product gates", cgoZero, "./conformance/projectmigrateproduct", "./conformance/projectmigratetargetproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile CGO-disabled heavy product gates", cgoZero, "./conformance/projectmigratetargetproduct", "./conformance/projectshowmigrationsproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile CGO-disabled heavy product gates", cgoZero, "./conformance/projectshowmigrationsproduct", "./conformance/runserverproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile CGO-disabled heavy product gates", cgoZero, "./conformance/runserverproduct", "./conformance/migrationwriterproduct")
	runserverWorkflowRequireSerialOrder(t, "Makefile CGO-disabled isolated conformance gate", cgoZero, "$(PORTABLE_CGO0_HEAVY_PACKAGES)", "./conformance/projectmigrateproduct")
	for scope, target := range map[string]string{
		"normal":       normal,
		"race":         race,
		"CGO-disabled": cgoZero,
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
	portableGo := runserverWorkflowJob(t, jobs, "portable-go-matrix")
	for _, fragment := range []string{
		"name: Portable Go (${{ matrix.mode }})",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"- mode: normal-vet\n            make_targets: \"go-test go-vet\"\n            timeout_minutes: 40",
		"- mode: race\n            make_targets: \"go-race\"\n            timeout_minutes: 55",
		"- mode: cgo0\n            make_targets: \"cgo-zero-build\"\n            timeout_minutes: 50",
		"run: make ${{ matrix.make_targets }}",
	} {
		runserverWorkflowRequireCount(t, "portable-go-matrix job", portableGo, fragment, 1)
	}
	if strings.Contains(portableGo, "continue-on-error:") || strings.Contains(portableGo, "|| true") {
		t.Fatal("portable Go matrix gates must remain required")
	}

	portable := runserverWorkflowJob(t, jobs, "product-project-check-matrix")
	runserverWorkflowRequireCount(t, "product-project-check-matrix job", portable, "timeout-minutes: ${{ matrix.timeout_minutes }}", 1)
	runserverWorkflowRequireCount(t, "product-project-check-matrix job", portable, "timeout_minutes:", 4)
	for _, coordinate := range []string{
		"- runs_on: ubuntu-22.04\n            expected_goos: linux\n            expected_goarch: amd64\n            timeout_minutes: 30",
		"- runs_on: ubuntu-24.04-arm\n            expected_goos: linux\n            expected_goarch: arm64\n            timeout_minutes: 30",
		"- runs_on: macos-15-intel\n            expected_goos: darwin\n            expected_goarch: amd64\n            timeout_minutes: 45",
		"- runs_on: macos-26\n            expected_goos: darwin\n            expected_goarch: arm64\n            timeout_minutes: 30",
	} {
		runserverWorkflowRequireCount(t, "product-project-check-matrix job", portable, coordinate, 1)
	}
	portableNormal := runserverWorkflowStep(
		t,
		portable,
		"Run and inventory runserver product tests",
		"Run product project-check race tests",
	)
	for _, fragment := range []string{
		"set -euo pipefail",
		`log="$RUNNER_TEMP/runserver-product-tests.json"`,
		"status=0",
		`go test -timeout=15m -json -count=1 ./conformance/runserverproduct > "$log" || status=$?`,
		`if [ "$status" -ne 0 ]; then`,
		`exit "$status"`,
		`package="github.com/progresshans/godj/conformance/runserverproduct"`,
		"required_passes=(",
		`for test_name in "${required_passes[@]}"; do`,
		`pass_fragment="\"Action\":\"pass\",\"Package\":\"$package\",\"Test\":\"$test_name\""`,
		`skip_fragment="\"Action\":\"skip\",\"Package\":\"$package\",\"Test\":\"$test_name\""`,
		`if ! grep -Fq "$pass_fragment" "$log"; then`,
		`if grep -Fq "$skip_fragment" "$log"; then`,
	} {
		runserverWorkflowRequireCount(t, "portable runserver inventory step", portableNormal, fragment, 1)
	}
	for _, sentinel := range []string{
		"TestGlobalRunserverArticleSQLiteDevelopmentLoop",
		"TestGlobalRunserverPublishesAuthenticatedArticleAdminAndAPI",
		"TestGlobalRunserverRejectsStaleCopiedArticleBeforeRuntime",
		"TestRunserverHarnessForcedCleanupIncludesSeparateDescendantGroup",
	} {
		runserverWorkflowRequireCount(t, "portable runserver inventory step", portableNormal, sentinel, 1)
	}

	portableRace := runserverWorkflowStep(
		t,
		portable,
		"Run runserver product race tests",
		"Run product project-check tests without CGO",
	)
	runserverWorkflowRequireCount(
		t,
		"portable runserver race step",
		portableRace,
		"run: go test -timeout=15m -race -count=1 ./conformance/runserverproduct",
		1,
	)
	portableCGOZero := runserverWorkflowStep(
		t,
		portable,
		"Run runserver product tests without CGO",
		"Vet product project-check packages",
	)
	runserverWorkflowRequireCount(
		t,
		"portable runserver CGO-disabled step",
		portableCGOZero,
		"run: CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/runserverproduct",
		1,
	)
	portableVet := runserverWorkflowStep(
		t,
		portable,
		"Vet runserver product package",
		"Require a clean worktree",
	)
	runserverWorkflowRequireCount(
		t,
		"portable runserver vet step",
		portableVet,
		"run: go vet ./conformance/runserverproduct",
		1,
	)
	runserverWorkflowRequireCount(t, "product-project-check-matrix job", portable, "./conformance/runserverproduct", 4)
	if strings.Contains(portable, "continue-on-error:") || strings.Contains(portable, "|| true") {
		t.Fatal("portable runserver product gates must remain required")
	}

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
			timeoutMinutes := 25
			if strings.Contains(coordinate, "runs_on: macos-15-intel") {
				timeoutMinutes = 30
			}
			entry := coordinate + "\n            mode: " + mode
			if timeoutMinutes == 25 {
				entry += "\n            timeout_minutes: 25"
			} else {
				entry += "\n            timeout_minutes: 30"
			}
			runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix coordinate/mode", targetedMigrate, entry, 1)
		}
	}
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "          - runs_on: ", 12)
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "timeout_minutes:", 12)
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "timeout_minutes: 25", 9)
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "timeout_minutes: 30", 3)
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
		"test_flags=(-timeout=20m -json -count=1)",
		`case "$mode" in`,
		"test_flags+=(-race)",
		"export CGO_ENABLED=0",
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
	runserverWorkflowRequireCount(t, "targeted-migrate-product-matrix job", targetedMigrate, "./conformance/projectmigratetargetproduct", 2)
	if strings.Contains(targetedMigrate, "continue-on-error:") || strings.Contains(targetedMigrate, "|| true") {
		t.Fatal("targeted migrate product matrix gates must remain required")
	}

	postgres := runserverWorkflowJob(t, jobs, "postgresql-product")
	for _, fragment := range []string{
		"name: PostgreSQL 17.10 actual product (${{ matrix.mode }})",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"- mode: normal\n            timeout_minutes: 25",
		"- mode: race\n            timeout_minutes: 25",
		"- mode: cgo0\n            timeout_minutes: 25",
		`GODJ_TEST_POSTGRES_URL: postgresql://postgres:godj-ci-pg-canary-8H2k7M4q9V6x3R@127.0.0.1:${{ job.services.postgres.ports[5432] }}/postgres?sslmode=disable`,
		`GODJ_REQUIRE_POSTGRES: "1"`,
		`mode="${{ matrix.mode }}"`,
		`test_flags=(-timeout=15m -json -count=1 -run "$required_regex")`,
		`test_flags+=(-race)`,
		`export CGO_ENABLED=0`,
		`go test "${test_flags[@]}" \`,
		`./conformance/runserverproduct > "$log" || status=$?`,
		`assert len(expected) == 22, sorted(expected)`,
		`assert runs == expected, (sorted(runs), sorted(expected))`,
		`assert passes == expected, (sorted(passes), sorted(expected))`,
		`assert skips == [], skips`,
		`if [ "$mode" != "normal" ]; then`,
		"go vet \\",
	} {
		runserverWorkflowRequireCount(t, "PostgreSQL runserver matrix job", postgres, fragment, 1)
	}
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
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, `GODJ_REQUIRE_POSTGRES: "1"`, 1)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/runserverproduct", 2)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./cmd/godj", 2)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/projectmigrateproduct", 2)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/projectshowmigrationsproduct", 2)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/systemstate/restart", 2)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "TestGlobalRunserverArticlePostgresDevelopmentLoop", 2)
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

	startMarker := target + ":\n"
	endMarker := "\n" + nextTarget + ":\n"
	if strings.Count(makefile, startMarker) != 1 || strings.Count(makefile, endMarker) != 1 {
		t.Fatalf("cannot isolate Makefile target %q before %q", target, nextTarget)
	}
	start := strings.Index(makefile, startMarker)
	end := strings.Index(makefile[start+len(startMarker):], endMarker)
	if end < 0 {
		t.Fatalf("Makefile target %q has no following %q target", target, nextTarget)
	}
	return makefile[start : start+len(startMarker)+end]
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
