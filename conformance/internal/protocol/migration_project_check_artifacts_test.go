package protocol

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestMigrationProjectCheckArtifactHashesAreLocked(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	wanted := map[string]struct {
		size   int64
		sha256 string
	}{
		"conformance/contracts/migration-project-check-manifest.json": {
			size:   4520,
			sha256: "0bbf254e80fea17b52070d0589da5ddcd401ff67440062a89b4fcd3e8309c048",
		},
		"conformance/fixtures/godj-migration-project-check-not-implemented.json": {
			size:   1729,
			sha256: "86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680",
		},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json": {
			size:   19971,
			sha256: "49f50b97bfa1973cef6fe464296a7c973b87e4ad1f9aaefecee24ab64f04d4d2",
		},
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(contents)) != want.size {
			t.Fatalf("migration-project-check artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want.sha256 {
			t.Fatalf("migration-project-check artifact %s checksum = %q, want %q", name, got, want.sha256)
		}
	}
}

func TestMigrationProjectCheckArtifactBoundaryIsLocked(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadMigrationProjectCheckArtifacts(t)
	wantSlugs := []string{
		"godj.migration.project_check.nested_project_success",
		"godj.migration.project_check.explicit_project_override",
		"godj.migration.project_check.empty_catalog",
		"godj.migration.project_check.canonical_filesystem_order",
		"godj.migration.project_check.unsafe_source_entry",
		"godj.migration.project_check.project_not_found",
		"godj.migration.project_check.project_protocol_incompatible",
		"godj.migration.project_check.project_build_failure_atomic",
		"godj.migration.project_check.definition_load_failure",
		"godj.migration.project_check.invalid_runner_response",
	}
	wantPhases := []Phase{
		PhaseEnvironment,
		PhaseEnvironment,
		PhaseConstruction,
		PhaseConstruction,
		PhaseConstruction,
		PhaseEnvironment,
		PhaseEnvironment,
		PhaseEnvironment,
		PhaseConstruction,
		PhaseEnvironment,
	}
	if len(manifest.Contracts) != len(wantSlugs) {
		t.Fatalf("migration-project-check manifest has %d contracts, want %d", len(manifest.Contracts), len(wantSlugs))
	}
	if len(oracle.Contracts) != len(wantSlugs) || len(baseline.Contracts) != len(wantSlugs) {
		t.Fatalf("migration-project-check suite lengths = oracle %d/static %d, want %d", len(oracle.Contracts), len(baseline.Contracts), len(wantSlugs))
	}
	wantMetricNames := []string{
		"ancestor_directories_inspected",
		"build_calls",
		"command_dispatches",
		"definition_sets_published",
		"definitions_published",
		"descriptor_reads",
		"direct_planner_calls",
		"directory_entries_seen",
		"documents_received",
		"exit_code",
		"failure",
		"godj_db_calls",
		"headers_validated",
		"load_calls",
		"operations_decoded",
		"partial_stdout_writes",
		"planner_construction",
		"revision_lifecycle_calls",
		"roots_opened",
		"runner_calls",
		"runner_response_writes",
		"source_reads",
		"user_stderr_writes",
		"user_stdout_writes",
	}

	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+65)
		if contract.ID != wantID {
			t.Fatalf("contract %d ID = %q, want %q", index, contract.ID, wantID)
		}
		if contract.Scenario != wantSlugs[index] {
			t.Fatalf("contract %s scenario = %q, want %q", contract.ID, contract.Scenario, wantSlugs[index])
		}
		if contract.Status != ContractPassing {
			t.Fatalf("contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
		}
		if contract.Phase != wantPhases[index] {
			t.Fatalf("contract %s phase = %q, want %q", contract.ID, contract.Phase, wantPhases[index])
		}
		wantComparison := []ComparisonDimension{CompareError, CompareMetrics}
		if index < 4 {
			wantComparison = []ComparisonDimension{CompareResult, CompareMetrics}
		}
		if !reflect.DeepEqual(contract.Comparison, wantComparison) {
			t.Fatalf("contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantComparison)
		}
		if len(contract.Provenance) != 1 {
			t.Fatalf("contract %s provenance count = %d, want 1", contract.ID, len(contract.Provenance))
		}
		provenance := contract.Provenance[0]
		if provenance.Kind != "decision" || provenance.Reference != "ADR-0021" || provenance.Derived == nil || *provenance.Derived || provenance.License != "" {
			t.Fatalf("contract %s provenance = %#v, want decision/ADR-0021/derived=false", contract.ID, provenance)
		}
		if oracle.Contracts[index].ID != contract.ID || oracle.Contracts[index].Status != StatusObserved {
			t.Fatalf("oracle contract %d = %#v, want %s observed", index, oracle.Contracts[index], contract.ID)
		}
		if baseline.Contracts[index].ID != contract.ID || baseline.Contracts[index].Status != StatusNotImplemented {
			t.Fatalf("static contract %d = %#v, want %s not_implemented", index, baseline.Contracts[index], contract.ID)
		}
		if oracle.Contracts[index].DBState != nil {
			t.Fatalf("oracle contract %s db_state = %#v, want absent for DB-free reference", contract.ID, oracle.Contracts[index].DBState)
		}
		metrics := oracle.Contracts[index].Metrics
		if metrics == nil || metrics.Type != ValueObject {
			t.Fatalf("oracle contract %s metrics = %#v, want object", contract.ID, metrics)
		}
		gotMetricNames := make([]string, 0, len(metrics.Fields))
		for _, field := range metrics.Fields {
			gotMetricNames = append(gotMetricNames, field.Name)
		}
		if !reflect.DeepEqual(gotMetricNames, wantMetricNames) {
			t.Fatalf("oracle contract %s metric fields = %#v, want exact 24 fields %#v", contract.ID, gotMetricNames, wantMetricNames)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("migration-project-check oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("migration-project-check static fixture does not validate: %v", err)
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != len(wantSlugs) {
		t.Fatalf("oracle/static differences = %d, want %d: %#v", len(differences), len(wantSlugs), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != manifest.Contracts[index].ID || difference.Path != "status" {
			t.Fatalf("difference %d = %#v, want ordered status-only mismatch", index, difference)
		}
	}
}

func TestMigrationProjectCheckStaticFixtureExitsOneWithTenOrderedMismatches(t *testing.T) {
	root := conformanceRepositoryRoot(t)
	arguments := []string{
		"run", "./conformance/cmd/observationcmp",
		"-profile", "conformance/profiles/django-6.1-sqlite-darwin-arm64.json",
		"-manifest", "conformance/contracts/migration-project-check-manifest.json",
		"-expected", "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json",
		"-actual", "conformance/fixtures/godj-migration-project-check-not-implemented.json",
	}
	command := exec.Command("go", arguments...)
	command.Dir = root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 1 {
		t.Fatalf("observationcmp error = %v, want exit 1; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("observationcmp stdout = %q, want empty", stdout.String())
	}
	text := stderr.String()
	if !strings.Contains(text, "observationcmp: 10 difference(s)") {
		t.Fatalf("observationcmp stderr = %q, want 10 differences", text)
	}
	previous := -1
	for number := 65; number <= 74; number++ {
		needle := fmt.Sprintf("MIG-%03d status:", number)
		position := strings.Index(text, needle)
		if position <= previous {
			t.Fatalf("observationcmp stderr does not preserve contract order at %s: %q", needle, text)
		}
		previous = position
	}
}

func TestMigrationProjectCheckEntersElevenAdapterProductTarget(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	referenceTarget := migrationProjectCheckMakeTarget(t, text, "conformance-check:\n", "godj-conformance:\n")
	productTarget := migrationProjectCheckMakeTarget(t, text, "godj-conformance:\n", "oracle-check:\n")
	oracleCheckTarget := migrationProjectCheckMakeTarget(t, text, "oracle-check:\n", "oracle-regenerate:\n")
	oracleRegenerateTarget := migrationProjectCheckMakeTarget(t, text, "oracle-regenerate:\n", "ci:")
	if got := strings.Count(referenceTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 2 {
		t.Fatalf("reference target project-check manifest count = %d, want 2", got)
	}
	if got := strings.Count(productTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 1 {
		t.Fatalf("product target project-check manifest count = %d, want 1", got)
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 11 {
		t.Fatalf("product adapter count = %d, want 11", got)
	}
	if got := strings.Count(oracleCheckTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-check project-check manifest count = %d, want 1", got)
	}
	if got := strings.Count(oracleRegenerateTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-regenerate project-check manifest count = %d, want 1", got)
	}
}

func TestMigrationProjectCheckWorkflowExpandsToExactEighteenRequiredExecutions(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	jobsStart := strings.Index(text, "jobs:\n")
	if jobsStart < 0 {
		t.Fatal("workflow jobs block is missing")
	}
	jobsText := text[jobsStart+len("jobs:\n"):]
	jobPattern := regexp.MustCompile(`(?m)^  ([a-z0-9-]+):$`)
	matches := jobPattern.FindAllStringSubmatch(jobsText, -1)
	wantJobs := []string{"conformance-validation", "exact-darwin-validation", "project-check-matrix", "product-project-check-matrix", "python-compatibility-matrix", "sqlite-matrix"}
	if len(matches) != len(wantJobs) {
		t.Fatalf("workflow top-level job definitions = %d, want %d: %#v", len(matches), len(wantJobs), matches)
	}
	for index, want := range wantJobs {
		if matches[index][1] != want {
			t.Fatalf("workflow job %d = %q, want %q", index, matches[index][1], want)
		}
	}

	conformance := migrationProjectCheckWorkflowJob(t, jobsText, "conformance-validation", "exact-darwin-validation")
	darwin := migrationProjectCheckWorkflowJob(t, jobsText, "exact-darwin-validation", "project-check-matrix")
	project := migrationProjectCheckWorkflowJob(t, jobsText, "project-check-matrix", "product-project-check-matrix")
	product := migrationProjectCheckWorkflowJob(t, jobsText, "product-project-check-matrix", "python-compatibility-matrix")
	python := migrationProjectCheckWorkflowJob(t, jobsText, "python-compatibility-matrix", "sqlite-matrix")
	sqlite := migrationProjectCheckWorkflowJob(t, jobsText, "sqlite-matrix", "")
	for name, block := range map[string]string{"conformance-validation": conformance, "exact-darwin-validation": darwin} {
		if got := strings.Count(block, "run: go test -count=1 ./conformance/projectcheck"); got != 1 {
			t.Fatalf("%s focused project-check command count = %d, want 1", name, got)
		}
		if got := strings.Count(block, "python-version: \"3.14.3\""); got != 1 {
			t.Fatalf("%s locked oracle Python pin count = %d, want 1", name, got)
		}
		if got := strings.Count(block, "astral-sh/setup-uv@c771a70e6277c0a99b617c7a806ffedaca235ff9"); got != 1 {
			t.Fatalf("%s locked setup-uv action count = %d, want 1", name, got)
		}
	}
	if !strings.Contains(conformance, "version: \"0.12.3\"") {
		t.Fatal("portable Ubuntu job does not pin current uv 0.12.3")
	}
	if !strings.Contains(darwin, "version: \"0.10.12\"") {
		t.Fatal("exact darwin oracle job does not preserve profile uv 0.10.12")
	}
	if !strings.Contains(conformance, "run: make ci") || !strings.Contains(conformance, "GOARCH: \"386\"") ||
		!strings.Contains(conformance, "./cmd/godj") || !strings.Contains(conformance, "./project") ||
		!strings.Contains(conformance, "./internal/projectcheck/...") || !strings.Contains(conformance, "./conformance/runners/godj") {
		t.Fatal("existing Ubuntu full/Linux-386 gates were not preserved")
	}
	if !strings.Contains(darwin, "make python-test-exact oracle-check") || !strings.Contains(darwin, "./migrations") || !strings.Contains(darwin, "./db/sqlite") {
		t.Fatal("existing macOS exact/lifecycle gates were not preserved")
	}

	wantCoordinates := []string{
		"- runs_on: ubuntu-22.04\n            expected_goos: linux\n            expected_goarch: amd64",
		"- runs_on: ubuntu-24.04-arm\n            expected_goos: linux\n            expected_goarch: arm64",
		"- runs_on: macos-15-intel\n            expected_goos: darwin\n            expected_goarch: amd64",
		"- runs_on: macos-26\n            expected_goos: darwin\n            expected_goarch: arm64",
	}
	for name, block := range map[string]string{"project-check": project, "product-project-check": product, "sqlite": sqlite} {
		if strings.Count(block, "          - runs_on: ") != 4 {
			t.Fatalf("%s matrix leg count is not 4", name)
		}
		for _, coordinate := range wantCoordinates {
			if strings.Count(block, coordinate) != 1 {
				t.Fatalf("%s matrix coordinate %q is not pinned exactly once", name, coordinate)
			}
		}
		for _, required := range []string{
			"runs-on: ${{ matrix.runs_on }}",
			"timeout-minutes: 20",
			"fail-fast: false",
			"go-version: \"1.26.5\"",
			`test "$(go env GOOS)" = "${{ matrix.expected_goos }}"`,
			`test "$(go env GOARCH)" = "${{ matrix.expected_goarch }}"`,
			"git diff --exit-code",
			`test -z "$(git status --porcelain=v1)"`,
		} {
			if strings.Count(block, required) != 1 {
				t.Fatalf("%s matrix required fragment %q count = %d, want 1", name, required, strings.Count(block, required))
			}
		}
	}
	for _, command := range []string{
		"run: go test -count=1 ./conformance/projectcheck",
		"run: go test -race -count=1 ./conformance/projectcheck",
		"run: CGO_ENABLED=0 go test -count=1 ./conformance/projectcheck",
		"run: go vet ./conformance/projectcheck",
	} {
		if strings.Count(project, command) != 1 {
			t.Fatalf("project-check matrix command %q count = %d, want 1", command, strings.Count(project, command))
		}
	}
	for _, required := range []string{
		"runs-on: ubuntu-24.04",
		"timeout-minutes: 20",
		"fail-fast: false",
		"UV_PYTHON_DOWNLOADS: never",
		"- \"3.12.13\"",
		"- \"3.13.15\"",
		"- \"3.14.3\"",
		"- \"3.14.7\"",
		"id: python",
		"python-version: ${{ matrix.python_version }}",
		"check-latest: false",
		"COMPAT_PYTHON: ${{ steps.python.outputs.python-path }}",
		`uv --version | grep -Eq '^uv 0[.]12[.]3([[:space:]]|$)'`,
		"--no-project --isolated --python \"$COMPAT_PYTHON\"",
		"--with Django==6.1 --with asgiref==3.12.1 --with sqlparse==0.5.5",
		"platform.python_version() == expected",
		"django.get_version() == \"6.1\"",
		"asgiref.__version__ == \"3.12.1\"",
		"sqlparse.__version__ == \"0.5.5\"",
		"python -m unittest discover -s conformance/runners/django/tests -v",
		"grep -c '^test_'",
		"-eq 174",
		"grep -c ' \\.\\.\\. skipped '",
		"-eq 16",
		"len(SCENARIOS) == 115",
		"len(payload) == 464087",
		"aa2ed24d41434b9756e4a4669a04ea44f2a457a94a4bdd31dcab9ff3d6b7afe8",
		"git diff --exit-code",
		`test -z "$(git status --porcelain=v1)"`,
	} {
		if !strings.Contains(python, required) {
			t.Fatalf("Python compatibility matrix is missing required fragment %q", required)
		}
	}
	if got := strings.Count(python, "          - "); got != 4 {
		t.Fatalf("Python compatibility matrix leg count = %d, want exact 4", got)
	}
	if strings.Count(python, "--no-project --isolated --python \"$COMPAT_PYTHON\"") != 3 {
		t.Fatalf("Python compatibility isolated invocation count = %d, want 3", strings.Count(python, "--no-project --isolated --python \"$COMPAT_PYTHON\""))
	}
	for _, command := range []string{
		"run: go test -count=1 ./cmd/godj ./project ./internal/projectcheck/... ./conformance/runners/godj",
		"run: go test -race -count=1 ./cmd/godj ./project ./internal/projectcheck/... ./conformance/runners/godj",
		"run: CGO_ENABLED=0 go test -count=1 ./cmd/godj ./project ./internal/projectcheck/... ./conformance/runners/godj",
		"run: go vet ./cmd/godj ./project ./internal/projectcheck/... ./conformance/runners/godj",
	} {
		if strings.Count(product, command) != 1 {
			t.Fatalf("product project-check matrix command %q count = %d, want 1", command, strings.Count(product, command))
		}
	}
	for _, command := range []string{
		"run: go test -count=1 ./migrations ./db/sqlite",
		"run: go test -race -count=1 ./migrations ./db/sqlite",
		"run: CGO_ENABLED=0 go test -count=1 ./migrations ./db/sqlite",
		"run: go vet ./migrations ./db/sqlite",
	} {
		if strings.Count(sqlite, command) != 1 {
			t.Fatalf("SQLite matrix command %q count = %d, want 1", command, strings.Count(sqlite, command))
		}
	}
	expandedExecutions := 2 + strings.Count(project, "          - runs_on: ") + strings.Count(product, "          - runs_on: ") + strings.Count(sqlite, "          - runs_on: ") + strings.Count(python, "          - ")
	if expandedExecutions != 18 {
		t.Fatalf("expanded workflow executions = %d, want 18", expandedExecutions)
	}
	if strings.Contains(text, "continue-on-error:") {
		t.Fatal("required workflow must not contain continue-on-error")
	}
	for _, forbidden := range []string{"services:", "postgres:", "mysql:", "mariadb:", "windows-", "windows:"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("workflow contains forbidden service-only backend fragment %q", forbidden)
		}
	}
	if got := strings.Count(text, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"); got != 6 {
		t.Fatalf("pinned checkout action count = %d, want 6 job definitions", got)
	}
	if got := strings.Count(text, "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"); got != 5 {
		t.Fatalf("pinned setup-go action count = %d, want 5 job definitions", got)
	}
	if got := strings.Count(text, "actions/setup-python@a309ff8b426b58ec0e2a45f0f869d46889d02405"); got != 1 {
		t.Fatalf("pinned setup-python action count = %d, want 1 job definition", got)
	}
	if got := strings.Count(text, "astral-sh/setup-uv@c771a70e6277c0a99b617c7a806ffedaca235ff9"); got != 3 {
		t.Fatalf("pinned setup-uv action count = %d, want 3 job definitions", got)
	}
}

func loadMigrationProjectCheckArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-project-check-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-project-check-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-project-check-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func migrationProjectCheckMakeTarget(t *testing.T, text, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(text, startMarker)
	end := strings.Index(text, endMarker)
	if start < 0 || end <= start {
		t.Fatalf("cannot isolate Makefile target %q..%q", startMarker, endMarker)
	}
	return text[start:end]
}

func migrationProjectCheckWorkflowJob(t *testing.T, jobsText, job, nextJob string) string {
	t.Helper()
	startMarker := "  " + job + ":\n"
	start := strings.Index(jobsText, startMarker)
	if start < 0 {
		t.Fatalf("workflow job %q is missing", job)
	}
	if nextJob == "" {
		return jobsText[start:]
	}
	endMarker := "  " + nextJob + ":\n"
	end := strings.Index(jobsText[start+len(startMarker):], endMarker)
	if end < 0 {
		t.Fatalf("workflow job %q has no following %q job", job, nextJob)
	}
	return jobsText[start : start+len(startMarker)+end]
}
