package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
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
			size:   5085,
			sha256: "e689b37098a4b26e4faddbd7c7e8a09d9145526f2b7bd1de7fb6cd5cb139c16b",
		},
		"conformance/fixtures/godj-migration-project-check-not-implemented.json": {
			size:   1729,
			sha256: "86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680",
		},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json": {
			size:   19971,
			sha256: "8bbf10c02950181a8753a11a40a6a81e816be33d1825a8a2469655d9f65bc0aa",
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
		wantReferences := []string{"ADR-0021"}
		if index < 4 || contract.ID == "MIG-073" {
			wantReferences = append(wantReferences, "ADR-0035")
		}
		if len(contract.Provenance) != len(wantReferences) {
			t.Fatalf("contract %s provenance count = %d, want %d", contract.ID, len(contract.Provenance), len(wantReferences))
		}
		for provenanceIndex, provenance := range contract.Provenance {
			if provenance.Kind != "decision" || provenance.Reference != wantReferences[provenanceIndex] || provenance.Derived == nil || *provenance.Derived || provenance.License != "" {
				t.Fatalf("contract %s provenance %d = %#v, want decision/%s/derived=false", contract.ID, provenanceIndex, provenance, wantReferences[provenanceIndex])
			}
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

func TestMigrationProjectCheckRemainsInCurrentTwentyTwoAdapterProductTarget(t *testing.T) {
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
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 24 {
		t.Fatalf("product adapter count = %d, want 24", got)
	}
	if got := strings.Count(oracleCheckTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-check project-check manifest count = %d, want 1", got)
	}
	if got := strings.Count(oracleRegenerateTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-regenerate project-check manifest count = %d, want 1", got)
	}
}

func TestMigrationProjectCheckWorkflowRequiresEveryDeclaredCoordinateAndMode(t *testing.T) {
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
	wantJobs := []string{"conformance-validation", "portable-go-matrix", "exact-darwin-validation", "project-check-matrix", "relation-binding-matrix", "relation-product-matrix", "product-project-check-matrix", "targeted-migrate-product-matrix", "python-compatibility-matrix", "postgresql-product", "sqlite-matrix", "required-ci"}
	jobCounts := make(map[string]int, len(matches))
	for _, match := range matches {
		jobCounts[match[1]]++
	}
	for _, want := range wantJobs {
		if jobCounts[want] != 1 {
			t.Fatalf("required workflow job %q definition count = %d, want 1", want, jobCounts[want])
		}
	}

	conformance := migrationProjectCheckWorkflowJob(t, jobsText, "conformance-validation")
	portableGo := migrationProjectCheckWorkflowJob(t, jobsText, "portable-go-matrix")
	darwin := migrationProjectCheckWorkflowJob(t, jobsText, "exact-darwin-validation")
	project := migrationProjectCheckWorkflowJob(t, jobsText, "project-check-matrix")
	relationBinding := migrationProjectCheckWorkflowJob(t, jobsText, "relation-binding-matrix")
	relationProduct := migrationProjectCheckWorkflowJob(t, jobsText, "relation-product-matrix")
	product := migrationProjectCheckWorkflowJob(t, jobsText, "product-project-check-matrix")
	targetedMigrate := migrationProjectCheckWorkflowJob(t, jobsText, "targeted-migrate-product-matrix")
	python := migrationProjectCheckWorkflowJob(t, jobsText, "python-compatibility-matrix")
	postgres := migrationProjectCheckWorkflowJob(t, jobsText, "postgresql-product")
	sqlite := migrationProjectCheckWorkflowJob(t, jobsText, "sqlite-matrix")
	requiredCI := migrationProjectCheckWorkflowJob(t, jobsText, "required-ci")
	discoveredDependencies := make(map[string]bool, len(jobCounts)-1)
	for job := range jobCounts {
		if job != "required-ci" {
			discoveredDependencies[job] = true
		}
	}
	needsStart := strings.Index(requiredCI, "    needs:\n")
	needsEnd := strings.Index(requiredCI, "    runs-on:")
	if needsStart < 0 || needsEnd < 0 || needsEnd <= needsStart {
		t.Fatal("required CI dependency block is missing or malformed")
	}
	needPattern := regexp.MustCompile(`(?m)^      - ([a-z0-9-]+)$`)
	declaredDependencies := make(map[string]bool)
	for _, match := range needPattern.FindAllStringSubmatch(requiredCI[needsStart:needsEnd], -1) {
		declaredDependencies[match[1]] = true
	}
	expectedPattern := regexp.MustCompile(`(?m)^              "([a-z0-9-]+)",$`)
	runtimeExpected := make(map[string]bool)
	for _, match := range expectedPattern.FindAllStringSubmatch(requiredCI, -1) {
		runtimeExpected[match[1]] = true
	}
	if !reflect.DeepEqual(declaredDependencies, discoveredDependencies) || !reflect.DeepEqual(runtimeExpected, discoveredDependencies) {
		t.Fatalf("required CI coverage mismatch: discovered=%v needs=%v runtime_expected=%v", discoveredDependencies, declaredDependencies, runtimeExpected)
	}
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
	if !strings.Contains(conformance, "run: make format-check generate-check python-test conformance-check godj-conformance") || !strings.Contains(conformance, "GOARCH: \"386\"") ||
		!strings.Contains(conformance, "./cmd/godj") || !strings.Contains(conformance, "./project") ||
		!strings.Contains(conformance, "./internal/projectcheck/...") || !strings.Contains(conformance, "./conformance/runners/godj") ||
		!strings.Contains(conformance, "./conformance/projectmigratetargetproduct") ||
		!strings.Contains(conformance, "./conformance/projectshowmigrationsproduct") ||
		!strings.Contains(conformance, "Run relation products on 32-bit Linux") ||
		!strings.Contains(conformance, "./conformance/relationproduct/...") ||
		!strings.Contains(conformance, "./conformance/relationqueryproduct/...") ||
		!strings.Contains(conformance, "./conformance/relationobjectproduct/...") ||
		!strings.Contains(conformance, "./conformance/relationreverseproduct/...") ||
		!strings.Contains(conformance, "./conformance/relationprefetchproduct/...") ||
		!strings.Contains(conformance, "./conformance/relationselectproduct/...") ||
		!strings.Contains(conformance, "./conformance/relationdeleteproduct/...") ||
		!strings.Contains(conformance, "./conformance/migrationrelationproduct") {
		t.Fatal("existing Ubuntu full/Linux-386 gates were not preserved")
	}
	for _, required := range []string{
		"name: Portable Go (${{ matrix.mode }})",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"- mode: normal-vet\n            make_targets: \"go-test go-vet\"\n            timeout_minutes: 40",
		"- mode: race\n            make_targets: \"go-race\"\n            timeout_minutes: 55",
		"- mode: cgo0\n            make_targets: \"cgo-zero-build\"\n            timeout_minutes: 50",
		"run: make ${{ matrix.make_targets }}",
		`test "$(go env GOOS)" = "linux"`,
		`test "$(go env GOARCH)" = "amd64"`,
		"git diff --exit-code",
		`test -z "$(git status --porcelain=v1)"`,
	} {
		if strings.Count(portableGo, required) != 1 {
			t.Fatalf("portable Go matrix fragment %q count = %d, want 1", required, strings.Count(portableGo, required))
		}
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
	for name, block := range map[string]string{"project-check": project, "relation-binding": relationBinding, "product-project-check": product, "sqlite": sqlite} {
		expectedTimeout := "timeout-minutes: 20"
		if name == "product-project-check" {
			expectedTimeout = "timeout-minutes: ${{ matrix.timeout_minutes }}"
		}
		if strings.Count(block, "          - runs_on: ") != 4 {
			t.Fatalf("%s matrix leg count is not 4", name)
		}
		if got := strings.Count(block, "timeout-minutes:"); got != 1 {
			t.Fatalf("%s matrix timeout key count = %d, want 1", name, got)
		}
		for _, coordinate := range wantCoordinates {
			if strings.Count(block, coordinate) != 1 {
				t.Fatalf("%s matrix coordinate %q is not pinned exactly once", name, coordinate)
			}
		}
		for _, required := range []string{
			"runs-on: ${{ matrix.runs_on }}",
			expectedTimeout,
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
	if got := strings.Count(relationProduct, "          - runs_on: "); got != 12 {
		t.Fatalf("relation-product matrix leg count = %d, want 4 coordinates x 3 modes", got)
	}
	for _, required := range []string{
		"name: Relation product (${{ matrix.runs_on }}, ${{ matrix.mode }})",
		"runs-on: ${{ matrix.runs_on }}",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"go-version: \"1.26.5\"",
		`test "$(go env GOOS)" = "${{ matrix.expected_goos }}"`,
		`test "$(go env GOARCH)" = "${{ matrix.expected_goarch }}"`,
		"git diff --exit-code",
		`test -z "$(git status --porcelain=v1)"`,
	} {
		if strings.Count(relationProduct, required) != 1 {
			t.Fatalf("relation-product matrix required fragment %q count = %d, want 1", required, strings.Count(relationProduct, required))
		}
	}
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
		if got := strings.Count(targetedMigrate, block); got != 1 {
			t.Fatalf("targeted-migrate %s block count = %d, want 1", label, got)
		}
	}
	for _, coordinate := range wantCoordinates {
		if got := strings.Count(relationProduct, coordinate); got != 3 {
			t.Fatalf("relation-product coordinate %q count = %d, want one per mode", coordinate, got)
		}
		for _, mode := range []string{"normal", "race", "cgo0"} {
			timeoutMinutes := 20
			if strings.Contains(coordinate, "runs_on: macos-15-intel") && mode == "race" {
				timeoutMinutes = 30
			}
			entry := coordinate + "\n            mode: " + mode + fmt.Sprintf("\n            timeout_minutes: %d", timeoutMinutes)
			if got := strings.Count(relationProduct, entry); got != 1 {
				t.Fatalf("relation-product coordinate/mode entry %q count = %d, want 1", entry, got)
			}
		}
	}
	if got := strings.Count(relationProduct, "timeout_minutes: 20"); got != 11 {
		t.Fatalf("relation-product 20-minute mode timeout count = %d, want 11", got)
	}
	if got := strings.Count(relationProduct, "timeout_minutes: 30"); got != 1 {
		t.Fatalf("relation-product Intel race timeout count = %d, want 1", got)
	}
	productTimeoutCoordinates := []string{
		"- runs_on: ubuntu-22.04\n            expected_goos: linux\n            expected_goarch: amd64\n            timeout_minutes: 30",
		"- runs_on: ubuntu-24.04-arm\n            expected_goos: linux\n            expected_goarch: arm64\n            timeout_minutes: 30",
		"- runs_on: macos-15-intel\n            expected_goos: darwin\n            expected_goarch: amd64\n            timeout_minutes: 45",
		"- runs_on: macos-26\n            expected_goos: darwin\n            expected_goarch: arm64\n            timeout_minutes: 30",
	}
	if got := strings.Count(product, "timeout_minutes:"); got != len(productTimeoutCoordinates) {
		t.Fatalf("product-project-check coordinate timeout count = %d, want %d", got, len(productTimeoutCoordinates))
	}
	for _, coordinate := range productTimeoutCoordinates {
		if strings.Count(product, coordinate) != 1 {
			t.Fatalf("product-project-check timeout coordinate %q is not pinned exactly once", coordinate)
		}
	}
	for _, required := range []string{
		"name: Targeted migrate product (${{ matrix.runs_on }}, ${{ matrix.mode }})",
		"runs-on: ${{ matrix.runs_on }}",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"go-version: \"1.26.5\"",
		`test "$(go env GOOS)" = "${{ matrix.expected_goos }}"`,
		`test "$(go env GOARCH)" = "${{ matrix.expected_goarch }}"`,
		`mode="${{ matrix.mode }}"`,
		"test_flags=(-timeout=20m -json -count=1)",
		`case "$mode" in`,
		"test_flags+=(-race)",
		"export CGO_ENABLED=0",
		`log="$RUNNER_TEMP/project-migrate-target-product-${mode}.json"`,
		`go test "${test_flags[@]}" ./conformance/projectmigratetargetproduct > "$log" || status=$?`,
		`package="github.com/progresshans/godj/conformance/projectmigratetargetproduct"`,
		`for test_name in "${required_passes[@]}"; do`,
		`pass_fragment="\"Action\":\"pass\",\"Package\":\"$package\",\"Test\":\"$test_name\""`,
		`package_skip_fragment="\"Action\":\"skip\",\"Package\":\"$package\""`,
		`if [ "$mode" = "normal" ]; then`,
		"go vet ./conformance/projectmigratetargetproduct",
		"git diff --exit-code",
		`test -z "$(git status --porcelain=v1)"`,
	} {
		if strings.Count(targetedMigrate, required) != 1 {
			t.Fatalf("targeted-migrate product matrix fragment %q count = %d, want 1", required, strings.Count(targetedMigrate, required))
		}
	}
	for _, coordinate := range wantCoordinates {
		for _, mode := range []string{"normal", "race", "cgo0"} {
			timeoutMinutes := 25
			if strings.Contains(coordinate, "runs_on: macos-15-intel") {
				timeoutMinutes = 30
			}
			entry := coordinate + "\n            mode: " + mode + fmt.Sprintf("\n            timeout_minutes: %d", timeoutMinutes)
			if strings.Count(targetedMigrate, entry) != 1 {
				t.Fatalf("targeted-migrate coordinate/mode entry %q is not pinned exactly once", entry)
			}
		}
	}
	if got := strings.Count(targetedMigrate, "          - runs_on: "); got != 12 {
		t.Fatalf("targeted-migrate matrix leg count = %d, want 4 coordinates x 3 modes", got)
	}
	if got := strings.Count(targetedMigrate, "timeout_minutes:"); got != 12 {
		t.Fatalf("targeted-migrate coordinate timeout count = %d, want 12", got)
	}
	if got := strings.Count(targetedMigrate, "timeout_minutes: 25"); got != 9 {
		t.Fatalf("targeted-migrate 25-minute timeout count = %d, want 9", got)
	}
	if got := strings.Count(targetedMigrate, "timeout_minutes: 30"); got != 3 {
		t.Fatalf("targeted-migrate Intel timeout count = %d, want 3", got)
	}
	if got := strings.Count(targetedMigrate, "./conformance/projectmigratetargetproduct"); got != 2 {
		t.Fatalf("targeted-migrate package gate count = %d, want test plus normal-mode vet", got)
	}
	targetRequiredSentinels := []string{
		"TestProjectLinkedTargetedMigrateSQLite",
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
	}
	targetRequiredBlockPattern := regexp.MustCompile(`(?ms)required_passes=\(\n(.*?)\n\s*\)\n\s*for test_name`)
	targetRequiredBlock := targetRequiredBlockPattern.FindStringSubmatch(targetedMigrate)
	if len(targetRequiredBlock) != 2 {
		t.Fatal("targeted-migrate required inventory block is missing or malformed")
	}
	targetRequiredLinePattern := regexp.MustCompile(`(?m)^\s*"([^"]+)"\s*$`)
	targetRequiredLines := targetRequiredLinePattern.FindAllStringSubmatch(targetRequiredBlock[1], -1)
	actualTargetRequiredSentinels := make([]string, len(targetRequiredLines))
	for index, line := range targetRequiredLines {
		actualTargetRequiredSentinels[index] = line[1]
	}
	if !reflect.DeepEqual(actualTargetRequiredSentinels, targetRequiredSentinels) {
		t.Fatalf("targeted-migrate required inventory sentinels = %q, want exact ordered %q", actualTargetRequiredSentinels, targetRequiredSentinels)
	}
	if strings.Contains(targetedMigrate, "continue-on-error:") || strings.Contains(targetedMigrate, "|| true") {
		t.Fatal("targeted-migrate matrix modes must remain required")
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
	for _, command := range []string{
		"go test -json -count=1 ./conformance/relationbinding | tee \"$log\"",
		"run: go test -race -count=1 ./conformance/relationbinding",
		"run: CGO_ENABLED=0 go test -count=1 ./conformance/relationbinding",
		"run: go vet ./conformance/relationbinding",
	} {
		if strings.Count(relationBinding, command) != 1 {
			t.Fatalf("relation-binding matrix command %q count = %d, want 1", command, strings.Count(relationBinding, command))
		}
	}
	for _, required := range []string{
		`log="$RUNNER_TEMP/relation-binding-tests.json"`,
		`if event.get("Action") == "run"`,
		`if event.get("Action") == "pass"`,
		`if event.get("Action") == "skip" and "Test" in event`,
		`assert len(top_level_runs) == 18`,
		`assert top_level_passes == top_level_runs`,
		`assert skipped == [], skipped`,
	} {
		if strings.Count(relationBinding, required) != 1 {
			t.Fatalf("relation-binding inventory fragment %q count = %d, want 1", required, strings.Count(relationBinding, required))
		}
	}
	for _, artifact := range []string{
		"conformance/contracts/relation-manifest.json",
		"conformance/fixtures/godj-relation-not-implemented.json",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json",
	} {
		if strings.Count(relationBinding, artifact) != 1 {
			t.Fatalf("relation-binding no-rewrite artifact %q count = %d, want 1", artifact, strings.Count(relationBinding, artifact))
		}
	}
	for _, required := range []string{
		"name: Run relation product mode",
		"set -euo pipefail",
		`mode="${{ matrix.mode }}"`,
		`if [ "$mode" = "race" ]; then`,
		`if [ "$mode" = "cgo0" ]; then`,
		`test "$mode" = "normal"`,
		`log="$RUNNER_TEMP/relation-product-tests.json"`,
		`status=0`,
		"go test -timeout=15m -json -count=1 \\",
		"./query",
		"./db/sqlite",
		"./conformance/relationproduct/...",
		"./conformance/relationqueryproduct/...",
		"./conformance/relationobjectproduct/...",
		"./conformance/relationreverseproduct/...",
		"./conformance/relationprefetchproduct/...",
		"./conformance/relationselectproduct/...",
		"./conformance/relationdeleteproduct/...",
		"./conformance/migrationrelationproduct",
		"./conformance/internal/protocol",
		"./conformance/runners/godj",
		"./conformance/cmd/godjcheck",
		"./internal/compiletest",
		`./internal/compiletest > "$log" || status=$?`,
		`relation_godj_runner='^(`,
		`relation_godjcheck='^(`,
		"go test -timeout=20m -p=1 -json -count=1 -run \"$relation_godj_runner\" \\",
		"go test -timeout=20m -p=1 -json -count=1 -run \"$relation_godjcheck\" \\",
		`./conformance/cmd/godjcheck >> "$log" || status=$?`,
		`if [ "$status" -ne 0 ]; then`,
		`formatter_status=0`,
		`diagnostic_log="$RUNNER_TEMP/relation-product-failure.txt"`,
		`|| formatter_status=$?`,
		`max_failure_diagnostic_bytes = 60000`,
		`failed_tests = {`,
		`failed_packages = {`,
		`package_only_failures = failed_packages - failed_test_packages`,
		`if remaining > 0:`,
		`diagnostic.extend(encoded_output[-remaining:])`,
		`relation-product failure diagnostics truncated at`,
		`preserving go test status $status`,
		`tail -c 60000 "$log"`,
		`tail -c 65536 "$diagnostic_log"`,
		`exit "$status"`,
		`if event.get("Action") == "run"`,
		`if event.get("Action") == "pass"`,
		`if event.get("Action") == "skip" and "Test" in event`,
		`payload = b"".join(`,
		`assert len(runs) == 942`,
		`assert len(payload) == 96114`,
		`420471e8972361fb1bcea29074fbb6ccacced4cdeeb7a3c35d5a8f273a66c910`,
		`assert passes == runs`,
		`assert skipped == [], skipped`,
		`"relation_product_run": [package, test]`,
		`"relation_product_inventory": {`,
		`"payload_sha256": payload_sha256`,
		"go test -timeout=15m -race -count=1",
		"go test -timeout=20m -p=1 -race -count=1",
		"CGO_ENABLED=0 go test -timeout=15m -count=1",
		"CGO_ENABLED=0 go test -timeout=20m -p=1 -count=1",
		"go vet",
		"conformance/fixtures/godj-relation-not-implemented.json",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json",
		"conformance/relationproduct",
		"conformance/relationqueryproduct",
		"conformance/relationobjectproduct",
		"conformance/relationreverseproduct",
		"conformance/relationprefetchproduct",
		"conformance/relationselectproduct",
		"conformance/relationdeleteproduct",
		"conformance/migrationrelationproduct",
	} {
		if !strings.Contains(relationProduct, required) {
			t.Fatalf("relation-product matrix is missing required fragment %q", required)
		}
	}
	if strings.Contains(relationProduct, `| tee "$log"`) {
		t.Fatal("relation-product inventory must not stream verbose JSON through tee")
	}
	if got := strings.Count(relationProduct, `-run "$relation_godj_runner"`); got != 3 {
		t.Fatalf("relation-product GoDj runner selector count = %d, want normal/race/CGO0", got)
	}
	if got := strings.Count(relationProduct, `-run "$relation_godjcheck"`); got != 3 {
		t.Fatalf("relation-product godjcheck selector count = %d, want normal/race/CGO0", got)
	}
	for _, testName := range []string{
		"TestRelationProductGeneratesTwelveObservedContractsMatchingLockedOracle",
		"TestRelationMetadataObservationChangesForEveryOwnedEdgeMutation",
		"TestRelationAdapterDoesNotImportDBOrReferenceArtifacts",
		"TestMigrationRelationDiagnosticCharacterizationRemainsLockedUnregisteredDeterministicAndDoesNotCompareOracle",
		"TestMigrationRelationCharacterizationRejectsStatusPhaseAndDimensionDrift",
		"TestMigrationRelationCharacterizationUsesScenarioNotContractIdentity",
		"TestMigrationRelationCharacterizationSourceHasNoExpectedArtifactShortcut",
		"TestRunMatchesTwelveContractRelationProductBeforePublishingActualOutput",
		"TestRunLeavesMigrationRelationReferenceNotImplementedWithoutProductHandlers",
		"TestRunRejectsMigrationRelationFalseGreenBeforePublishingActualOutput",
		"TestRunDoesNotPublishRelationActualBeforePayloadComparison",
		"TestRunRejectsRelationRegistryStatusFalseGreensBeforeActualOutput",
		"TestRunRejectsUnknownRelationStatusBeforeActualOutput",
	} {
		if got := strings.Count(relationProduct, testName); got != 1 {
			t.Fatalf("relation-product exact selector test %q count = %d, want 1", testName, got)
		}
	}
	for _, exact := range []string{
		"import hashlib",
		`package.encode("utf-8")`,
		`+ b"\0"`,
		`test.encode("utf-8")`,
		`for package, test in sorted(runs)`,
		`hashlib.sha256(payload).hexdigest()`,
	} {
		if count := strings.Count(relationProduct, exact); count != 1 {
			t.Fatalf("relation-product inventory fragment %q count = %d, want 1", exact, count)
		}
	}
	for _, packagePattern := range []string{
		"./schema/...",
		"./query",
		"./codegen",
		"./orm",
		"./db/sqlite",
		"./migrations",
		"./migrations/definition",
		"./conformance/relationproduct/...",
		"./conformance/relationqueryproduct/...",
		"./conformance/relationobjectproduct/...",
		"./conformance/relationreverseproduct/...",
		"./conformance/relationprefetchproduct/...",
		"./conformance/relationselectproduct/...",
		"./conformance/relationdeleteproduct/...",
		"./conformance/migrationrelationproduct",
		"./conformance/internal/protocol",
		"./conformance/runners/godj",
		"./conformance/cmd/godjcheck",
		"./internal/compiletest",
	} {
		linePattern := regexp.MustCompile(`(?m)^[ \t]+` + regexp.QuoteMeta(packagePattern) + `(?: \\| > "\$log" \|\| status=\$\?| >> "\$log" \|\| status=\$\?)?$`)
		if count := len(linePattern.FindAllString(relationProduct, -1)); count != 4 {
			t.Fatalf("relation-product package %q gate count = %d, want normal/race/CGO0/vet", packagePattern, count)
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
		"--with Django==6.1 --with djangorestframework==3.18.0 --with asgiref==3.12.1 --with sqlparse==0.5.5",
		"platform.python_version() == expected",
		"django.get_version() == \"6.1\"",
		"rest_framework.VERSION == \"3.18.0\"",
		"asgiref.__version__ == \"3.12.1\"",
		"sqlparse.__version__ == \"0.5.5\"",
		"python -m unittest discover -s conformance/runners/django/tests -v",
		"grep -c '^test_'",
		"-eq 305",
		"grep -c ' \\.\\.\\. skipped '",
		"-eq 21",
		"Ran 305 tests",
		"OK (skipped=21)",
		"len(SCENARIOS) == 291",
		"len(payload) == 1015687",
		"b3918c9d471cacd79ad9da0774618b0df085b6db71784a884c668703807790de",
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
		"run: go test -timeout=15m -count=1 ./cmd/godj ./project ./internal/projectcheck/... ./conformance/runners/godj",
		"run: go test -timeout=15m -race -count=1 ./cmd/godj ./project ./internal/projectcheck/... ./conformance/runners/godj",
		"run: CGO_ENABLED=0 go test -timeout=15m -count=1 ./cmd/godj ./project ./internal/projectcheck/... ./conformance/runners/godj",
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
	for _, required := range []string{
		"name: PostgreSQL 17.10 actual product (${{ matrix.mode }})",
		"runs-on: ubuntu-24.04",
		"timeout-minutes: ${{ matrix.timeout_minutes }}",
		"fail-fast: false",
		"services:\n      postgres:\n",
		"image: postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f",
		"POSTGRES_PASSWORD: godj-ci-pg-canary-8H2k7M4q9V6x3R",
		"POSTGRES_DB: postgres",
		`POSTGRES_INITDB_ARGS: "--encoding=UTF8 --locale=C --locale-provider=libc"`,
		`--health-cmd "pg_isready -U postgres -d postgres"`,
		"--health-interval 5s",
		"--health-timeout 5s",
		"--health-retries 12",
		"- 5432",
		`current_setting('session_replication_role')`,
		`current_setting('synchronous_commit')`,
		`current_setting('default_transaction_isolation')`,
		`current_setting('default_transaction_read_only')`,
		`current_setting('default_transaction_deferrable')`,
		`current_setting('fsync')`,
		`current_setting('full_page_writes')`,
		`test "$fingerprint" = "170010|UTF8|UTF8|c|<null>|C|C|UTC|on|on|read committed|off|off|on|on|origin"`,
		"name: Run and inventory PostgreSQL actual product tests",
		`mode="${{ matrix.mode }}"`,
		`test_flags=(-timeout=15m -json -count=1 -run "$required_regex")`,
		`test_flags+=(-race)`,
		`export CGO_ENABLED=0`,
		`log="$RUNNER_TEMP/postgresql-product-tests.json"`,
		"status=0",
		`go test "${test_flags[@]}" \`,
		`./conformance/runserverproduct > "$log" || status=$?`,
		`if [ "$status" -ne 0 ]; then`,
		`tail -c 60000 "$log"`,
		`required="$RUNNER_TEMP/postgresql-required-tests.txt"`,
		`printf '%s\n' "${required_passes[@]}" > "$required"`,
		`python3 - "$required" "$log" "$mode" <<'PY'`,
		`assert len(expected) == 22, sorted(expected)`,
		`assert runs == expected, (sorted(runs), sorted(expected))`,
		`assert passes == expected, (sorted(passes), sorted(expected))`,
		`assert skips == [], skips`,
		`"mode": sys.argv[3]`,
		`if [ "$mode" != "normal" ]; then`,
		`test -f "$GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE"`,
		`sha256sum --check SHA256SUMS`,
		`export GODJ_TEST_POSTGRES_SCHEMA="godj_postgresproduct_ci${{ github.run_id }}${{ github.run_attempt }}"`,
		`project_runner="$RUNNER_TEMP/postgres-projectrunner"`,
		`go build -o "$project_runner" ./conformance/postgresproduct/cmd/projectrunner`,
		`"$project_runner" prepare`,
		`docker restart "$POSTGRES_CONTAINER_ID" >/dev/null`,
		`docker inspect --format '{{with (index .NetworkSettings.Ports "5432/tcp")}}{{(index . 0).HostPort}}{{end}}'`,
		`case "$postgres_host_port" in`,
		`""|*[!0-9]*)`,
		`if [ "$postgres_host_port" -lt 1 ] || [ "$postgres_host_port" -gt 65535 ]; then`,
		`export GODJ_TEST_POSTGRES_URL="postgresql://postgres:godj-ci-pg-canary-8H2k7M4q9V6x3R@127.0.0.1:${postgres_host_port}/postgres?sslmode=disable"`,
		`timeout 3s "$project_runner" probe`,
		`"$project_runner" resume`,
		`"$project_runner" verify`,
		`"$project_runner" cleanup`,
		"go vet \\",
		"git diff --exit-code",
		`test -z "$(git status --porcelain=v1)"`,
	} {
		if strings.Count(postgres, required) != 1 {
			t.Fatalf("PostgreSQL product job fragment %q count = %d, want 1", required, strings.Count(postgres, required))
		}
	}
	for _, mode := range []string{"normal", "race", "cgo0"} {
		entry := "- mode: " + mode + "\n            timeout_minutes: 25"
		if got := strings.Count(postgres, entry); got != 1 {
			t.Fatalf("PostgreSQL mode entry %q count = %d, want 1", entry, got)
		}
	}
	if got := strings.Count(postgres, "timeout_minutes: 25"); got != 3 {
		t.Fatalf("PostgreSQL mode timeout count = %d, want 3", got)
	}
	postgresRequiredSentinels := []string{
		"github.com/progresshans/godj/db/postgres|TestPostgreSQLPhase1Integration",
		"github.com/progresshans/godj/db/postgres|TestPostgresRevisionFencedMigrationIntegration",
		"github.com/progresshans/godj/db/postgres|TestPostgresRevisionFenceCrossProcessIntegration",
		"github.com/progresshans/godj/db/postgres|TestPostgresMigrationCreateThenAddInOneDefinitionIntegration",
		"github.com/progresshans/godj/db/postgres|TestPostgresMigrationRejectsNullableDefaultAddOnPopulatedTableIntegration",
		"github.com/progresshans/godj/db/postgres|TestPostgresMigrationRecorderFailureRollsBackSchemaHistoryAndRevisionIntegration",
		"github.com/progresshans/godj/db/postgres|TestPostgresMigrationRejectsAddAfterDroppedAttributeSlotsAreExhaustedIntegration",
		"github.com/progresshans/godj/db/postgres|TestPostgresMigrationRejectsInitializedRevisionZeroIntegration",
		"github.com/progresshans/godj/db/postgres|TestPostgresMigrationRejectsInboundControlForeignKeyIntegration",
		"github.com/progresshans/godj/examples/article|TestArticlePostgresMigrationGeneratedCRUDAndHTTP",
		"github.com/progresshans/godj/examples/article|TestArticleAdminSitePostgresUserFlow",
		"github.com/progresshans/godj/examples/article|TestArticleAPIAdminSessionPostgresUserFlow",
		"github.com/progresshans/godj/examples/article|TestArticleAPIBearerPostgresUserFlow",
		"github.com/progresshans/godj/cmd/godj|TestActualGodjMakemigrationsPostgresGeneratedMigrateNoopRestart",
		"github.com/progresshans/godj/conformance/postgresproduct|TestGeneratedRelationPostgresE2E",
		"github.com/progresshans/godj/conformance/postgresproduct/cmd/projectrunner|TestProjectRunnerSameServerLifecycle",
		"github.com/progresshans/godj/conformance/runserverproduct|TestGlobalRunserverArticlePostgresDevelopmentLoop",
		"github.com/progresshans/godj/conformance/projectmigrateproduct|TestGlobalMigrateArticlePostgresProduct",
		"github.com/progresshans/godj/conformance/projectmigrateproduct|TestGlobalMigrateAuthenticatedArticlePostgresRestartDurability",
		"github.com/progresshans/godj/conformance/projectshowmigrationsproduct|TestGlobalShowMigrationsPostgresReadOnlyFreshPrefixRestart",
		"github.com/progresshans/godj/conformance/systemstate/restart|TestSystemStatePostgresDistinctProcessRestartSentinel",
		"github.com/progresshans/godj/conformance/systemstate/restart|TestSystemStatePostgresTwoProcessCoordinationRestartSentinel",
	}
	if len(postgresRequiredSentinels) != 22 {
		t.Fatalf("PostgreSQL required actual-test sentinel count = %d, want exact 22", len(postgresRequiredSentinels))
	}
	requiredBlockPattern := regexp.MustCompile(`(?ms)required_passes=\(\n(.*?)\n\s*\)\n\s*required=`)
	requiredBlock := requiredBlockPattern.FindStringSubmatch(postgres)
	if len(requiredBlock) != 2 {
		t.Fatal("PostgreSQL required actual-test sentinel block is missing or malformed")
	}
	requiredLinePattern := regexp.MustCompile(`(?m)^\s*"([^"]+)"\s*$`)
	requiredLines := requiredLinePattern.FindAllStringSubmatch(requiredBlock[1], -1)
	actualRequiredSentinels := make([]string, len(requiredLines))
	for index, line := range requiredLines {
		actualRequiredSentinels[index] = line[1]
	}
	if !reflect.DeepEqual(actualRequiredSentinels, postgresRequiredSentinels) {
		t.Fatalf("PostgreSQL required actual-test sentinels = %q, want exact ordered %q", actualRequiredSentinels, postgresRequiredSentinels)
	}
	for _, sentinel := range postgresRequiredSentinels {
		if strings.Count(postgres, sentinel) != 1 {
			t.Fatalf("PostgreSQL required actual-test sentinel %q count = %d, want 1", sentinel, strings.Count(postgres, sentinel))
		}
	}
	postgresRequiredTests := make([]string, len(postgresRequiredSentinels))
	for index, sentinel := range postgresRequiredSentinels {
		_, testName, ok := strings.Cut(sentinel, "|")
		if !ok || testName == "" {
			t.Fatalf("PostgreSQL required sentinel %q has no test name", sentinel)
		}
		postgresRequiredTests[index] = testName
	}
	wantRequiredRegex := "required_regex='^(" + strings.Join(postgresRequiredTests, "|") + ")$'"
	if got := strings.Count(postgres, wantRequiredRegex); got != 1 {
		t.Fatalf("PostgreSQL exact required-test regex count = %d, want 1", got)
	}
	postgresURL := "GODJ_TEST_POSTGRES_URL: postgresql://postgres:godj-ci-pg-canary-8H2k7M4q9V6x3R@127.0.0.1:${{ job.services.postgres.ports[5432] }}/postgres?sslmode=disable"
	if got := strings.Count(postgres, postgresURL); got != 1 {
		t.Fatalf("PostgreSQL product URL injection count = %d, want exact 1 shared matrix step", got)
	}
	if got := strings.Count(postgres, `GODJ_REQUIRE_POSTGRES: "1"`); got != 1 {
		t.Fatalf("PostgreSQL required-actual guard count = %d, want exact 1 shared matrix step", got)
	}
	if got := strings.Count(postgres, "POSTGRES_CONTAINER_ID: ${{ job.services.postgres.id }}"); got != 2 {
		t.Fatalf("PostgreSQL service container identity count = %d, want exact 2", got)
	}
	if got := strings.Count(postgres, "go-version: \"1.26.5\""); got != 1 {
		t.Fatalf("PostgreSQL product Go pin count = %d, want 1", got)
	}
	for _, packagePattern := range []string{
		"./cmd/godj",
		"./db/postgres",
		"./examples/article",
		"./conformance/postgresproduct/...",
		"./conformance/projectmigrateproduct",
		"./conformance/projectshowmigrationsproduct",
		"./conformance/systemstate/restart",
		"./conformance/runserverproduct",
	} {
		linePattern := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(packagePattern) + `(?: \\| > "\$log" \|\| status=\$\?)?$`)
		if got := len(linePattern.FindAllString(postgres, -1)); got != 2 {
			t.Fatalf("PostgreSQL package %q gate count = %d, want selected test plus vet", packagePattern, got)
		}
	}
	for _, required := range []string{
		"name: Required CI",
		"if: ${{ always() }}",
		"runs-on: ubuntu-24.04",
		"timeout-minutes: 5",
		"REQUIRED_RESULTS_JSON: ${{ toJSON(needs) }}",
		`results = json.loads(os.environ["REQUIRED_RESULTS_JSON"])`,
		`assert set(results) == expected, (sorted(results), sorted(expected))`,
		`result.get("result") != "success"`,
		`required CI lanes did not succeed:`,
	} {
		if got := strings.Count(requiredCI, required); got != 1 {
			t.Fatalf("required CI aggregate fragment %q count = %d, want 1", required, got)
		}
	}
	for _, job := range wantJobs[:len(wantJobs)-1] {
		if got := strings.Count(requiredCI, "      - "+job+"\n"); got != 1 {
			t.Fatalf("required CI dependency %q count = %d, want 1", job, got)
		}
		if got := strings.Count(requiredCI, `"`+job+`"`); got != 1 {
			t.Fatalf("required CI expected-set member %q count = %d, want 1", job, got)
		}
	}
	if strings.Contains(requiredCI, "actions/checkout@") || strings.Contains(requiredCI, "actions/setup-go@") {
		t.Fatal("required CI aggregate must remain a short dependency-only gate")
	}
	if strings.Contains(text, "continue-on-error:") {
		t.Fatal("required workflow must not contain continue-on-error")
	}
	if got := strings.Count(text, "    services:\n"); got != 1 {
		t.Fatalf("workflow service-container block count = %d, want exact PostgreSQL product service", got)
	}
	if got := strings.Count(text, "      postgres:\n"); got != 1 {
		t.Fatalf("workflow PostgreSQL service definition count = %d, want 1", got)
	}
	for _, forbidden := range []string{"mysql:", "mariadb:", "windows-", "windows:"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("workflow contains forbidden unsupported backend fragment %q", forbidden)
		}
	}
	migrationProjectCheckAssertActionPin(t, text, "actions/checkout", "3d3c42e5aac5ba805825da76410c181273ba90b1", 10)
	migrationProjectCheckAssertActionPin(t, text, "actions/setup-go", "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e", 9)
	migrationProjectCheckAssertActionPin(t, text, "actions/setup-python", "5fda3b95a4ea91299a34e894583c3862153e4b97", 1)
	migrationProjectCheckAssertActionPin(t, text, "astral-sh/setup-uv", "c771a70e6277c0a99b617c7a806ffedaca235ff9", 3)
	migrationProjectCheckAssertRelationProductFailureFormatter(t, text)
}

func migrationProjectCheckAssertActionPin(t *testing.T, workflow, action, wantSHA string, minimum int) {
	t.Helper()

	pattern := regexp.MustCompile(`(?m)^\s+uses: ` + regexp.QuoteMeta(action) + `@([^\s#]+)`)
	matches := pattern.FindAllStringSubmatch(workflow, -1)
	if len(matches) < minimum {
		t.Fatalf("workflow %s usage count = %d, want at least %d required jobs", action, len(matches), minimum)
	}
	for _, match := range matches {
		if match[1] != wantSHA {
			t.Fatalf("workflow %s ref = %q, want immutable %q", action, match[1], wantSHA)
		}
	}
}

func migrationProjectCheckAssertRelationProductFailureFormatter(t *testing.T, workflow string) {
	t.Helper()

	startMarker := "            python3 - \"$log\" > \"$diagnostic_log\" 2>&1 <<'PY' || formatter_status=$?\n"
	start := strings.Index(workflow, startMarker)
	if start < 0 {
		t.Fatal("relation-product failure formatter start is missing")
	}
	start += len(startMarker)
	end := strings.Index(workflow[start:], "\n          PY\n")
	if end < 0 {
		t.Fatal("relation-product failure formatter end is missing")
	}
	lines := strings.Split(workflow[start:start+end], "\n")
	for index, line := range lines {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "          ") {
			t.Fatalf("formatter line %d does not retain YAML indentation: %q", index+1, line)
		}
		lines[index] = strings.TrimPrefix(line, "          ")
	}
	formatter := strings.Join(lines, "\n") + "\n"

	events := []map[string]any{
		{"Action": "run", "Package": "example/pkg", "Test": "TestHung"},
		{"Action": "output", "Package": "example/pkg", "Test": "TestHung", "Output": strings.Repeat("old-output-", 7000)},
		{"Action": "output", "Package": "example/pkg", "Test": "TestHung", "Output": "panic: test timed out\nimportant-stack-tail\n"},
		{"Action": "output", "Package": "example/pkg", "Output": "FAIL\n"},
		{"Action": "fail", "Package": "example/pkg", "Elapsed": 0.1},
	}
	output := migrationProjectCheckRunRelationProductFailureFormatter(t, formatter, events)
	if len(output) > 65536 {
		t.Fatalf("failure formatter output bytes = %d, want <= 65536", len(output))
	}
	for _, wanted := range []string{"panic: test timed out", "important-stack-tail", "relation-product failure diagnostics truncated at 60000 bytes"} {
		if !bytes.Contains(output, []byte(wanted)) {
			t.Fatalf("failure formatter output is missing %q: %s", wanted, output)
		}
	}

	remainingZeroEvents := []map[string]any{
		{"Action": "output", "Package": "example/pkg", "Output": strings.Repeat("overflow", 1000)},
		{"Action": "fail", "Package": "example/pkg", "Padding": strings.Repeat("Z", 59940)},
	}
	remainingZeroOutput := migrationProjectCheckRunRelationProductFailureFormatter(t, formatter, remainingZeroEvents)
	if len(remainingZeroOutput) > 65536 {
		t.Fatalf("remaining-zero formatter output bytes = %d, want <= 65536", len(remainingZeroOutput))
	}
	if bytes.Contains(remainingZeroOutput, []byte("overflow")) {
		t.Fatal("remaining-zero formatter appended selected output beyond the diagnostic cap")
	}
	if !bytes.Contains(remainingZeroOutput, []byte("relation-product failure diagnostics truncated at 60000 bytes")) {
		t.Fatal("remaining-zero formatter omitted the truncation marker")
	}
}

func migrationProjectCheckRunRelationProductFailureFormatter(t *testing.T, formatter string, events []map[string]any) []byte {
	t.Helper()

	var input bytes.Buffer
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		input.Write(encoded)
		input.WriteByte('\n')
	}
	logPath := filepath.Join(t.TempDir(), "package-only-timeout.json")
	if err := os.WriteFile(logPath, input.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", "-", logPath)
	command.Stdin = strings.NewReader(formatter)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("failure formatter returned %v: %s", err, output)
	}
	return output
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

func migrationProjectCheckWorkflowJob(t *testing.T, jobsText, job string) string {
	t.Helper()
	startMarker := "  " + job + ":\n"
	start := strings.Index(jobsText, startMarker)
	if start < 0 {
		t.Fatalf("workflow job %q is missing", job)
	}
	remainder := jobsText[start+len(startMarker):]
	nextJobPattern := regexp.MustCompile(`(?m)^  [a-z0-9-]+:$`)
	end := nextJobPattern.FindStringIndex(remainder)
	if end == nil {
		return jobsText[start:]
	}
	return jobsText[start : start+len(startMarker)+end[0]]
}
