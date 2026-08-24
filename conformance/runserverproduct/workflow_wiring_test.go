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
	cgoZero := runserverWorkflowMakeTarget(t, makefile, "cgo-zero-build", "python-test")
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "CGO_ENABLED=0 go test \\\n", 1)
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "\t\t./conformance/runserverproduct \\\n", 1)
	runserverWorkflowRequireCount(t, "Makefile cgo-zero-build", cgoZero, "./conformance/runserverproduct", 1)

	workflow := runserverWorkflowReadFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	jobsMarker := "jobs:\n"
	if strings.Count(workflow, jobsMarker) != 1 {
		t.Fatalf("workflow jobs marker count = %d, want 1", strings.Count(workflow, jobsMarker))
	}
	jobs := workflow[strings.Index(workflow, jobsMarker)+len(jobsMarker):]

	conformance := runserverWorkflowJob(t, jobs, "conformance-validation", "exact-darwin-validation")
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "timeout-minutes:", 1)
	runserverWorkflowRequireCount(t, "conformance-validation job", conformance, "timeout-minutes: 25", 1)
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
	runserverWorkflowRequireCount(t, "product-project-check-matrix job", portable, "timeout-minutes: 30", 1)
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
		`GODJ_TEST_POSTGRES_URL: postgresql://postgres:postgres@127.0.0.1:${{ job.services.postgres.ports[5432] }}/postgres?sslmode=disable`,
		`GODJ_REQUIRE_POSTGRES: "1"`,
		`log="$RUNNER_TEMP/postgresql-product-tests.json"`,
		`go test -timeout=15m -json -count=1 -run '^TestGlobalRunserverArticlePostgresDevelopmentLoop$' \`,
		`./conformance/runserverproduct >> "$log" || status=$?`,
		"github.com/progresshans/godj/conformance/runserverproduct|TestGlobalRunserverArticlePostgresDevelopmentLoop",
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
		"go vet ./conformance/runserverproduct",
		1,
	)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, `GODJ_REQUIRE_POSTGRES: "1"`, 3)
	runserverWorkflowRequireCount(t, "postgresql-product job", postgres, "./conformance/runserverproduct", 4)
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
