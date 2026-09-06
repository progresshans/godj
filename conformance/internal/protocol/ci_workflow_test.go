package protocol

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// These readers cover the checked-in workflow's block jobs, needs and include
// records. Unsupported or missing structures fail the caller's required set;
// they are not a general YAML parser or a replacement for Actions validation.
func ciJobs(t *testing.T) map[string]string {
	t.Helper()
	text := ciRead(t, ".github/workflows/ci.yml")
	start := strings.Index(text, "\njobs:\n")
	if start < 0 {
		t.Fatal("workflow jobs mapping is missing")
	}
	text = text[start+len("\njobs:\n"):]
	pattern := regexp.MustCompile(`(?m)^  ([a-z0-9-]+):[ \t]*(?:#.*)?$`)
	matches := pattern.FindAllStringSubmatchIndex(text, -1)
	jobs := make(map[string]string)
	for i, match := range matches {
		name := text[match[2]:match[3]]
		if _, exists := jobs[name]; exists {
			t.Fatalf("duplicate CI job %q", name)
		}
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		jobs[name] = text[match[0]:end]
	}
	if len(jobs) == 0 {
		t.Fatal("workflow jobs mapping could not be read")
	}
	return jobs
}

func ciJob(t *testing.T, jobs map[string]string, name string) string {
	t.Helper()
	job, ok := jobs[name]
	if !ok {
		t.Fatalf("required CI job %q is missing", name)
	}
	return job
}

func ciNeeds(t *testing.T, job string) []string {
	t.Helper()
	match := regexp.MustCompile(`(?m)^    needs:[ \t]*([^\n]*)\n((?:      - [^\n]+\n)*)`).FindStringSubmatch(job)
	if len(match) != 3 {
		t.Fatal("CI dependency mapping could not be read")
	}
	var values []string
	if inline := strings.TrimSpace(match[1]); inline != "" {
		values = strings.Split(strings.Trim(inline, "[]"), ",")
	} else {
		for _, line := range strings.Split(strings.TrimSpace(match[2]), "\n") {
			values = append(values, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- ")))
		}
	}
	for i := range values {
		values[i] = strings.Trim(strings.TrimSpace(values[i]), "\"'")
	}
	sort.Strings(values)
	return values
}

func ciMatrix(t *testing.T, job string) []map[string]string {
	t.Helper()
	start := strings.Index(job, "\n        include:\n")
	end := strings.Index(job, "\n    steps:")
	if start < 0 || end <= start {
		t.Fatal("workflow matrix include mapping could not be read")
	}
	var rows []map[string]string
	for _, line := range strings.Split(job[start+len("\n        include:\n"):end], "\n") {
		if strings.HasPrefix(line, "          - ") {
			rows = append(rows, make(map[string]string))
			line = "            " + strings.TrimPrefix(line, "          - ")
		}
		if !strings.HasPrefix(line, "            ") || len(rows) == 0 {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(value) == "" {
			t.Fatalf("unsupported matrix include property %q", line)
		}
		row := rows[len(rows)-1]
		if _, exists := row[key]; exists {
			t.Fatalf("duplicate matrix include key %q", key)
		}
		row[key] = strings.Trim(strings.TrimSpace(value), "\"'")
	}
	if len(rows) == 0 {
		t.Fatal("matrix include records could not be read")
	}
	return rows
}

func ciRead(t *testing.T, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(conformanceRepositoryRoot(t), filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func ciRequire(t *testing.T, scope, text string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Fatalf("%s lacks required behavior %q", scope, value)
		}
	}
}

func ciMakeTarget(t *testing.T, text, target string) string {
	t.Helper()
	start := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:[^\n]*\n`).FindStringIndex(text)
	if start == nil {
		t.Fatalf("Make target %q is missing", target)
	}
	rest := text[start[1]:]
	end := regexp.MustCompile(`(?m)^[A-Za-z0-9_.%-]+:[^=]`).FindStringIndex(rest)
	if end == nil {
		return text[start[0]:]
	}
	return text[start[0] : start[1]+end[0]]
}

func TestWorkflowSelectedOwnersReachAggregate(t *testing.T) {
	jobs := ciJobs(t)
	outputPath := filepath.Join(t.TempDir(), "plan.txt")
	command := exec.CommandContext(t.Context(), "python3", "scripts/ci/scopes.py", "plan")
	command.Dir = conformanceRepositoryRoot(t)
	command.Env = append(os.Environ(), "VALIDATION_SUITE=full", "GITHUB_OUTPUT="+outputPath, "PYTHONDONTWRITEBYTECODE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("read CI scope owner authority: %v\n%s", err, output)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var owners []string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "jobs=") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "jobs=")), &owners); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(owners) == 0 {
		t.Fatal("full validation plan has no owners")
	}
	sort.Strings(owners)
	var discovered []string
	for name := range jobs {
		if name != "validation-plan" && name != "required-ci" {
			discovered = append(discovered, name)
		}
	}
	sort.Strings(discovered)
	if !reflect.DeepEqual(discovered, owners) {
		t.Fatalf("workflow owners differ from scope authority: jobs=%v owners=%v", discovered, owners)
	}
	for _, owner := range owners {
		job := ciJob(t, jobs, owner)
		if !containsString(ciNeeds(t, job), "validation-plan") {
			t.Fatalf("owner %s lacks the validation plan dependency", owner)
		}
		ciRequire(t, owner, job, "needs.validation-plan.outputs.jobs", "'"+owner+"'")
		if regexp.MustCompile(`continue-on-error:\s*true`).MatchString(job) {
			t.Fatalf("owner %s can ignore failure", owner)
		}
	}
	aggregate := ciJob(t, jobs, "required-ci")
	want := append(append([]string(nil), owners...), "validation-plan")
	sort.Strings(want)
	if actual := ciNeeds(t, aggregate); !reflect.DeepEqual(actual, want) {
		t.Fatalf("aggregate needs=%v want all owners=%v", actual, want)
	}
	ciRequire(t, "aggregate", aggregate, "always()", "toJSON(needs)", "needs.validation-plan.outputs.suite", "scripts/ci/scopes.py verify")
	ciRequire(t, "plan", ciJob(t, jobs, "validation-plan"), "scripts/ci/scopes.py plan")
}

func TestWorkflowRetainsDeclaredCoordinatesAndModes(t *testing.T) {
	jobs := ciJobs(t)
	coordinates := []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}
	for _, name := range []string{"relation-product-matrix", "product-project-check-matrix", "project-operator-product-matrix", "targeted-migrate-product-matrix", "sqlite-matrix"} {
		job := ciJob(t, jobs, name)
		rows := ciMatrix(t, job)
		matrixModes := name == "relation-product-matrix" || name == "product-project-check-matrix" || name == "project-operator-product-matrix" || name == "targeted-migrate-product-matrix"
		seen := make(map[string]bool)
		for _, row := range rows {
			seen[row["expected_goos"]+"/"+row["expected_goarch"]+"/"+row["mode"]] = true
		}
		modes := []string{""}
		if matrixModes {
			modes = []string{"normal", "race", "cgo0"}
		}
		for _, coordinate := range coordinates {
			for _, mode := range modes {
				if !seen[coordinate+"/"+mode] {
					t.Fatalf("%s misses %s mode=%s", name, coordinate, mode)
				}
			}
		}
		ciRequire(t, name, job, "go env GOOS", "go env GOARCH", "matrix.expected_goos", "matrix.expected_goarch")
		if matrixModes {
			ciRequire(t, name, job, "matrix.mode", "-race", "CGO_ENABLED=0", "go test")
		} else {
			ciRequire(t, name, job, "go test -count=1", "go test -race", "CGO_ENABLED=0 go test")
		}
	}
	portable := ciMatrix(t, ciJob(t, jobs, "portable-go-matrix"))
	for mode, prefix := range map[string]string{"normal": "go-test", "race": "go-race", "cgo0": "cgo-zero-build"} {
		for _, group := range []string{"core", "integration", "conformance", "products"} {
			found := false
			for _, row := range portable {
				if row["mode"] == mode && containsString(strings.Fields(row["make_targets"]), prefix+"-"+group) {
					found = true
				}
			}
			if !found {
				t.Fatalf("portable Go matrix misses package group=%s mode=%s", group, mode)
			}
		}
	}
	seen := make(map[string]bool)
	for _, row := range ciMatrix(t, ciJob(t, jobs, "postgresql-product")) {
		seen[row["shard"]+"/"+row["mode"]] = true
	}
	for _, shard := range []string{"core", "operator-target"} {
		for _, mode := range []string{"normal", "race", "cgo0"} {
			if !seen[shard+"/"+mode] {
				t.Fatalf("PostgreSQL matrix misses shard=%s mode=%s", shard, mode)
			}
		}
	}

}

func TestWorkflowKeepsOracleIndependentAndCapturesCurrent(t *testing.T) {
	jobs := ciJobs(t)
	for _, name := range []string{"conformance-validation", "exact-darwin-validation"} {
		job := ciJob(t, jobs, name)
		ciRequire(t, name, job, "uv sync --frozen", "git diff --exit-code --", "conformance/contracts", "conformance/profiles", "conformance/oracles")
		if strings.Contains(job, "oracle-regenerate") {
			t.Fatalf("%s regenerates the reference authority", name)
		}
	}
	ciRequire(t, "exact oracle validation", ciJob(t, jobs, "exact-darwin-validation"), "python-test-exact", "oracle-check")
	postgres := ciJob(t, jobs, "postgresql-product")
	reference := ciJob(t, jobs, "conformance-validation")
	if !containsString(ciNeeds(t, reference), "postgresql-product") {
		t.Fatal("reference comparison does not wait for current product captures")
	}
	ciRequire(t, "PostgreSQL capture producer", postgres, "scripts/ci/capture_artifact.py pack", "actions/upload-artifact@", "if-no-files-found: error")
	ciRequire(t, "current capture consumer", reference, "actions/download-artifact@", "scripts/ci/capture_artifact.py verify", "ATTESTATION_DIR:", "godj-conformance")
	for _, artifact := range []string{"systemstate-postgres", "operator-postgres"} {
		ciRequire(t, "capture producer", postgres, "name: "+artifact)
		ciRequire(t, "capture consumer", reference, "name: "+artifact)
	}
	for _, file := range []string{"postgresql-17.10-two-process-v1.json", "postgresql-17.10-sqlite-external-operator-v1.json"} {
		ciRequire(t, "capture producer", postgres, file)
		ciRequire(t, "capture consumer", reference, file)
	}
	if strings.Index(reference, "scripts/ci/capture_artifact.py verify") > strings.Index(reference, "godj-conformance") {
		t.Fatal("product comparison runs before capture provenance verification")
	}
}

func TestRunserverKeepsNormalRaceAndCGOZeroProductCoverage(t *testing.T) {
	makefile := ciRead(t, "Makefile")
	for target, mode := range map[string]string{"go-test-products": "", "go-race-products": "-race", "cgo-zero-build-products": "CGO_ENABLED=0"} {
		body := ciMakeTarget(t, makefile, target)
		found := false
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "./conformance/runserverproduct") {
				ciRequire(t, target, line, "go test", mode)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s misses actual runserver product tests", target)
		}
	}
	ciRequire(t, "fast feedback", ciRead(t, ".github/workflows/feedback.yml"), "make quick", "scripts/ci", "not full platform validation")
	quick := ciMakeTarget(t, makefile, "quick")
	for _, heavy := range []string{"godj-conformance", "go-test-products", "go-test-platform"} {
		if strings.Contains(quick, heavy) {
			t.Fatalf("quick feedback unexpectedly owns %s", heavy)
		}
	}
}

func assertConformanceAdapter(t *testing.T, prefix string, deviation bool) {
	t.Helper()
	makefile := ciRead(t, "Makefile")
	manifest, oracle, baseline := "$("+prefix+"_MANIFEST)", "$("+prefix+"_ORACLE)", "$("+prefix+"_NOT_IMPLEMENTED)"
	ciRequire(t, "reference adapter "+prefix, ciMakeTarget(t, makefile, "conformance-check"), manifest, oracle, baseline)
	product := ciMakeTarget(t, makefile, "godj-conformance")
	ciRequire(t, "product adapter "+prefix, product, "go run ./conformance/cmd/godjcheck", manifest, oracle)
	if strings.Contains(product, baseline) {
		t.Fatalf("product adapter %s uses a not-implemented observation", prefix)
	}
	if deviation {
		ciRequire(t, "product deviation "+prefix, product, "$("+prefix+"_DEVIATION_EXPECTED)")
	}
	check := strings.ReplaceAll(ciMakeTarget(t, makefile, "oracle-check"), "\\\n", " ")
	for _, line := range strings.Split(check, "\n") {
		if strings.Contains(line, manifest) {
			ciRequire(t, "independent oracle check "+prefix, line, oracle, "--check")
			return
		}
	}
	t.Fatalf("oracle-check misses %s", prefix)
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func TestWorkflowRequiredProductSentinelsRemainInventoried(t *testing.T) {
	jobs := ciJobs(t)
	operatorRequiredSentinels := []string{
		"TestOperatorSanitizeEnvironmentDropsHostOnlyControls",
		"TestGlobalCreatesuperuserExternalSQLiteProduct",
		"TestOperatorCanonicalSchemaRowsSortsAndFramesWithoutAmbiguity",
		"TestOperatorSQLiteSchemaSnapshotDetectsCatalogMutation",
		"TestOperatorCountRawSecretOccurrencesDetectsAuditMarker",
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
	postgresCoreRequiredSentinels := []string{
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
		"github.com/progresshans/godj/conformance/projectsqlmigrateproduct|TestGlobalSQLMigrateExternalPhaseDProduct",
		"github.com/progresshans/godj/conformance/systemstate/restart|TestSystemStatePostgresDistinctProcessRestartSentinel",
		"github.com/progresshans/godj/conformance/systemstate/restart|TestSystemStatePostgresTwoProcessCoordinationRestartSentinel",
	}
	postgresOperatorTargetRequiredSentinels := []string{
		"github.com/progresshans/godj/conformance/projectmigratetargetproduct|TestGlobalTargetedMigratePostgresLifecycle",
		"github.com/progresshans/godj/conformance/projectoperatorproduct|TestOperatorPostgresSchemaSnapshotDetectsTriggerMutation",
		"github.com/progresshans/godj/conformance/projectoperatorproduct|TestGlobalCreatesuperuserExternalPostgresAndSQLiteProduct",
	}
	for _, inventory := range []struct {
		job, array string
		required   []string
	}{
		{"project-operator-product-matrix", "required_tests", operatorRequiredSentinels},
		{"targeted-migrate-product-matrix", "required_passes", targetRequiredSentinels},
		{"postgresql-product", "core_required_passes", postgresCoreRequiredSentinels},
		{"postgresql-product", "operator_target_required_passes", postgresOperatorTargetRequiredSentinels},
	} {
		job := ciJob(t, jobs, inventory.job)
		pattern := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(inventory.array) + `=\(\s*(.*?)\s*\)`)
		array := pattern.FindStringSubmatch(job)
		if len(array) != 2 {
			t.Fatalf("%s has no required test inventory %s", inventory.job, inventory.array)
		}
		actual := make(map[string]bool)
		for _, value := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(array[1], -1) {
			actual[value[1]] = true
		}
		for _, sentinel := range inventory.required {
			if !actual[sentinel] {
				t.Fatalf("%s required inventory omits %s", inventory.job, sentinel)
			}
		}
		ciRequire(t, inventory.job, job, "${"+inventory.array+"[@]}", "scripts/ci/go_test_events.py", "--required", "--no-skips")
	}
}
