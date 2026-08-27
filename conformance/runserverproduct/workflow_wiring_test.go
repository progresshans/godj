package runserverproduct_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunserverProductWorkflowWiringIsLocked(t *testing.T) {
	t.Parallel()

	root := runserverWorkflowRepositoryRoot(t)
	makefile := runserverWorkflowReadFile(t, filepath.Join(root, "Makefile"))
	for _, fragment := range []string{
		"PROJECT_MIGRATE_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/projectmigrateproduct",
		"RUNSERVER_PRODUCT_IMPORT := github.com/progresshans/godj/conformance/runserverproduct",
		"ci: format-check generate-check go-test go-vet go-race cgo-zero-build python-test conformance-check godj-conformance",
	} {
		runserverWorkflowRequireCount(t, "Makefile", makefile, fragment, 1)
	}
	selector := runserverWorkflowMakeDefinition(t, makefile, "select_core_go_packages")
	for _, fragment := range []string{
		`all_packages="$$(go list ./...)"`,
		`project_migrate_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(PROJECT_MIGRATE_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`runserver_count="$$(printf '%s\n' "$$all_packages" | awk '$$0 == "$(RUNSERVER_PRODUCT_IMPORT)" { count++ } END { print count + 0 }')"`,
		`test "$$project_migrate_count" -eq 1`,
		`test "$$runserver_count" -eq 1`,
		`core_packages="$$(printf '%s\n' "$$all_packages" | awk '$$0 != "$(PROJECT_MIGRATE_PRODUCT_IMPORT)" && $$0 != "$(RUNSERVER_PRODUCT_IMPORT)"')"`,
		`test -n "$$core_packages"`,
		`all_count="$$(printf '%s\n' "$$all_packages" | awk 'NF { count++ } END { print count + 0 }')"`,
		`core_count="$$(printf '%s\n' "$$core_packages" | awk 'NF { count++ } END { print count + 0 }')"`,
		`test "$$all_count" -eq "$$((core_count + 2))"`,
	} {
		runserverWorkflowRequireCount(t, "Makefile core package selector", selector, fragment, 1)
	}
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(PROJECT_MIGRATE_PRODUCT_IMPORT)", 2)
	runserverWorkflowRequireCount(t, "Makefile core package selector", selector, "$(RUNSERVER_PRODUCT_IMPORT)", 2)

	selectionCheck := runserverWorkflowMakeTarget(t, makefile, "core-package-selection-check", "go-test")
	runserverWorkflowRequireRecipeLine(t, "Makefile core package selection check", selectionCheck, `$(select_core_go_packages); \`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile core package selection check", selectionCheck, `printf '%s\n' "$$core_packages"`, 1)

	normal := runserverWorkflowMakeTarget(t, makefile, "go-test", "go-vet")
	runserverWorkflowRequireRecipeLine(t, "Makefile normal core gate", normal, `$(select_core_go_packages); \`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal core gate", normal, `go test $$core_packages`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal project-migrate gate", normal, "go test -timeout=15m -count=1 ./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile normal runserver gate", normal, "go test -timeout=15m -count=1 ./conformance/runserverproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile normal gate", normal, "./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile normal gate", normal, "./conformance/runserverproduct", 1)
	runserverWorkflowRequireSerialOrder(t, "Makefile normal heavy product gates", normal, "./conformance/projectmigrateproduct", "./conformance/runserverproduct")

	race := runserverWorkflowMakeTarget(t, makefile, "go-race", "cgo-zero-build")
	runserverWorkflowRequireRecipeLine(t, "Makefile race core gate", race, `$(select_core_go_packages); \`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race core gate", race, `go test -race $$core_packages`, 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race project-migrate gate", race, "go test -timeout=15m -race -count=1 ./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile race runserver gate", race, "go test -timeout=15m -race -count=1 ./conformance/runserverproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile race gate", race, "./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile race gate", race, "./conformance/runserverproduct", 1)
	runserverWorkflowRequireSerialOrder(t, "Makefile race heavy product gates", race, "./conformance/projectmigrateproduct", "./conformance/runserverproduct")

	cgoZero := runserverWorkflowMakeTarget(t, makefile, "cgo-zero-build", "python-test")
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "CGO_ENABLED=0 go test \\\n", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled project-migrate gate", cgoZero, "CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireRecipeLine(t, "Makefile CGO-disabled runserver gate", cgoZero, "CGO_ENABLED=0 go test -timeout=15m -count=1 ./conformance/runserverproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "./conformance/projectmigrateproduct", 1)
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "./conformance/runserverproduct", 1)
	runserverWorkflowRequireSerialOrder(t, "Makefile CGO-disabled heavy product gates", cgoZero, "./conformance/projectmigrateproduct", "./conformance/runserverproduct")
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

	conformance := runserverWorkflowJob(t, jobs, "conformance-validation", "exact-darwin-validation")
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "timeout-minutes:", 1)
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "timeout-minutes: 45", 1)
	portableValidation := runserverWorkflowStep(
		t,
		conformance,
		"Run portable conformance validation",
		"Validate project-linked migration check contracts",
	)
	runserverWorkflowRequireCount(t, "portable conformance validation step", portableValidation, "run: make ci", 1)
	if strings.Contains(conformance, "continue-on-error:") || strings.Contains(conformance, "|| true") {
		t.Fatal("portable conformance validation gates must remain required")
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

	portable := runserverWorkflowJob(t, jobs, "product-project-check-matrix", "python-compatibility-matrix")
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

	postgres := runserverWorkflowJob(t, jobs, "postgresql-product", "sqlite-matrix")
	postgresNormal := runserverWorkflowStep(
		t,
		postgres,
		"Run and inventory PostgreSQL actual product tests",
		"Run PostgreSQL actual product race tests",
	)
	for _, fragment := range []string{
		`GODJ_TEST_POSTGRES_URL: postgresql://postgres:godj-ci-pg-canary-8H2k7M4q9V6x3R@127.0.0.1:${{ job.services.postgres.ports[5432] }}/postgres?sslmode=disable`,
		`GODJ_REQUIRE_POSTGRES: "1"`,
		`log="$RUNNER_TEMP/postgresql-product-tests.json"`,
		`go test -timeout=15m -json -count=1 -run '^TestGlobalRunserverArticlePostgresDevelopmentLoop$' \`,
		`./conformance/runserverproduct >> "$log" || status=$?`,
		"github.com/progresshans/godj/conformance/runserverproduct|TestGlobalRunserverArticlePostgresDevelopmentLoop",
		`go test -timeout=15m -json -count=1 ./db/postgres ./examples/article ./conformance/postgresproduct/... ./conformance/systemstate/restart > "$log" || status=$?`,
		"github.com/progresshans/godj/conformance/systemstate/restart|TestSystemStatePostgresDistinctProcessRestartSentinel",
		`pass_fragment="\"Action\":\"pass\",\"Package\":\"$package\",\"Test\":\"$test_name\""`,
		`skip_fragment="\"Action\":\"skip\",\"Package\":\"$package\",\"Test\":\"$test_name\""`,
		`if ! grep -Fq "$pass_fragment" "$log"; then`,
		`if grep -Fq "$skip_fragment" "$log"; then`,
	} {
		runserverWorkflowRequireCount(t, "PostgreSQL runserver inventory step", postgresNormal, fragment, 1)
	}

	postgresRace := runserverWorkflowStep(
		t,
		postgres,
		"Run PostgreSQL actual product race tests",
		"Run PostgreSQL actual product tests without CGO",
	)
	for _, fragment := range []string{
		`GODJ_REQUIRE_POSTGRES: "1"`,
		`go test -timeout=15m -race -count=1 ./db/postgres ./examples/article ./conformance/postgresproduct/... ./conformance/systemstate/restart`,
		`go test -timeout=15m -race -count=1 -run '^TestGlobalRunserverArticlePostgresDevelopmentLoop$' ./conformance/runserverproduct`,
	} {
		runserverWorkflowRequireCount(t, "PostgreSQL runserver race step", postgresRace, fragment, 1)
	}
	postgresCGOZero := runserverWorkflowStep(
		t,
		postgres,
		"Run PostgreSQL actual product tests without CGO",
		"Verify PostgreSQL durable resume across service restart",
	)
	for _, fragment := range []string{
		`GODJ_REQUIRE_POSTGRES: "1"`,
		`CGO_ENABLED=0 go test -timeout=15m -count=1 ./db/postgres ./examples/article ./conformance/postgresproduct/... ./conformance/systemstate/restart`,
		`CGO_ENABLED=0 go test -timeout=15m -count=1 -run '^TestGlobalRunserverArticlePostgresDevelopmentLoop$' ./conformance/runserverproduct`,
	} {
		runserverWorkflowRequireCount(t, "PostgreSQL runserver CGO-disabled step", postgresCGOZero, fragment, 1)
	}
	postgresVet := runserverWorkflowStep(
		t,
		postgres,
		"Vet PostgreSQL product packages",
		"Require a clean worktree",
	)
	runserverWorkflowRequireCount(
		t,
		"PostgreSQL runserver vet step",
		postgresVet,
		"go vet ./db/postgres ./examples/article ./conformance/postgresproduct/... ./conformance/systemstate/restart && go vet ./conformance/runserverproduct",
		1,
	)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, `GODJ_REQUIRE_POSTGRES: "1"`, 3)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/runserverproduct", 4)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/systemstate/restart", 4)
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
		"-run '^TestGlobalRunserverArticlePostgresDevelopmentLoop$'",
		3,
	)
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

func runserverWorkflowJob(t *testing.T, jobs, job, nextJob string) string {
	t.Helper()

	startMarker := "  " + job + ":\n"
	endMarker := "  " + nextJob + ":\n"
	if strings.Count(jobs, startMarker) != 1 || strings.Count(jobs, endMarker) != 1 {
		t.Fatalf("cannot isolate workflow job %q before %q", job, nextJob)
	}
	start := strings.Index(jobs, startMarker)
	end := strings.Index(jobs[start+len(startMarker):], endMarker)
	if end < 0 {
		t.Fatalf("workflow job %q has no following %q job", job, nextJob)
	}
	return jobs[start : start+len(startMarker)+end]
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

func runserverWorkflowRequireSerialOrder(t *testing.T, scope, text, first, second string) {
	t.Helper()

	firstIndex := strings.Index(text, first)
	secondIndex := strings.Index(text, second)
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("%s order invalid: first_index=%d second_index=%d", scope, firstIndex, secondIndex)
	}
}
