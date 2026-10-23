//go:build darwin || linux

package runserverproduct_test

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testprocess"
	"github.com/progresshans/godj/internal/gobuild"
	"github.com/progresshans/godj/internal/testenv"
)

// Readiness includes both readonly Go builds and generated-bundle validation.
// Give preparation its own bounded compiler budget. HTTP exercises, shutdown
// grace, cancellation, process-tree absence and output limits remain separate.
const runserverPreparationTimeout = 5 * time.Minute

// A new project resolves its module, generates and checks its bundle, and
// compiles both server and command runtimes before publication. Those cold
// builds share one CLI invocation, so they need a separate aggregate budget.
const runserverScaffoldTimeout = 10 * time.Minute
const maximumRunserverGoAudit = 64 << 10

type runserverGoAuditRecord struct {
	Arguments          []string  `json:"arguments"`
	StartedAt          time.Time `json:"started_at"`
	CandidateWorkspace string    `json:"candidate_workspace,omitempty"`
	BuildWorkspace     string    `json:"build_workspace,omitempty"`
}

// Retain the last actual Go invocation when scaffold setup fails before the
// first HTTP runtime exists. Other commands retain their original budget.
func runProjectCLI(t *testing.T, directory string, environment []string, global string, args ...string) testprocess.CommandResult {
	t.Helper()
	limits := testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}
	if len(args) > 0 && args[0] == "startproject" {
		limits.Timeout = runserverScaffoldTimeout
		environment, _ = runserverGoAuditEnvironment(t, environment)
		started := time.Now()
		defer func() {
			t.Logf("startproject preparation: %s", runserverPreparationProgress(environment, started, time.Now()))
		}()
	}
	return testprocess.Run(t, limits, directory, environment, global, args...)
}

func ensureRunserverGoAuditEnvironment(t *testing.T, environment []string) []string {
	t.Helper()
	values := testenv.Map(environment)
	if values[runserverGoAuditModeEnv] == "1" && values[runserverGoAuditLogEnv] != "" && values[runserverGoAuditRealEnv] != "" {
		return append([]string(nil), environment...)
	}
	environment, _ = runserverGoAuditEnvironment(t, environment)
	return environment
}

// Inspect the real output, not just an intended compiler argument. A process
// test built with -race must not silently exercise an uninstrumented child.
func assertRunserverBuildCoordinate(t *testing.T, binary string) *debug.BuildInfo {
	t.Helper()
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal("native executable has no readable Go build identity")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	race := settings["-race"] == "true"
	if settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH || race != gobuild.RaceEnabled {
		t.Fatal("native executable lost the parent OS, architecture or race coordinate")
	}
	if expected := os.Getenv("CGO_ENABLED"); expected != "" && settings["CGO_ENABLED"] != expected {
		t.Fatal("native executable lost the explicit CGO coordinate")
	}
	t.Logf("native executable coordinate: goos=%s goarch=%s cgo=%s race=%t", settings["GOOS"], settings["GOARCH"], settings["CGO_ENABLED"], race)
	return info
}

// Initial readiness has one live declaration/runtime workspace. Earlier
// invocations may appear in the same audit, but their removed outputs cannot
// stand in for the two executables that currently serve this project.
func assertRunserverLinkedBuildCoordinates(t *testing.T, environment []string) {
	t.Helper()
	file, err := os.Open(testenv.Map(environment)[runserverGoAuditLogEnv])
	if err != nil {
		t.Fatal("ready runtime has no Go build audit")
	}
	document, readErr := io.ReadAll(io.LimitReader(file, maximumRunserverGoAudit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(document) > maximumRunserverGoAudit {
		t.Fatal("ready runtime Go build audit is unreadable or oversized")
	}
	found := map[string]int{"./cmd/projectrunner": 0, "./cmd/site": 0}
	for _, line := range bytes.Split(bytes.TrimSpace(document), []byte{'\n'}) {
		var record runserverGoAuditRecord
		if json.Unmarshal(line, &record) != nil {
			t.Fatal("ready runtime Go build audit is invalid")
		}
		arguments := record.Arguments
		if len(arguments) != 6 || arguments[0] != "build" || arguments[3] != "-o" {
			continue
		}
		if _, wanted := found[arguments[5]]; !wanted {
			continue
		}
		info, err := os.Stat(arguments[4])
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			t.Fatal("ready runtime build output is not a regular executable")
		}
		assertRunserverBuildCoordinate(t, arguments[4])
		found[arguments[5]]++
	}
	if found["./cmd/projectrunner"] != 1 || found["./cmd/site"] != 1 {
		t.Fatal("ready runtime did not retain exactly one declaration and server executable")
	}
}

func TestRunserverGoAuditConfinesTelemetryAndRequiresPairedMetadata(t *testing.T) {
	root, err := os.MkdirTemp(t.TempDir(), "godj-candidate-")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"GOENV": "off"}
	for key, suffix := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "xdg-config", "XDG_CACHE_HOME": "xdg-cache", "TMPDIR": "tmp", "GOTMPDIR": "gotmp"} {
		values[key] = filepath.Join(root, suffix)
	}
	if got := runserverCandidateWorkspace(testenv.Sorted(values)); got != root {
		t.Fatal("owned private environment was not recognized")
	}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TMPDIR", "GOTMPDIR", "GOENV", "TEST_TELEMETRY_DIR", "GO_TELEMETRY_CHILD", "GO_TELEMETRY_CHILD_UPLOAD", "GOTELEMETRY", "GOTELEMETRYDIR"} {
		t.Run(key, func(t *testing.T) {
			copy := testenv.Map(testenv.Sorted(values))
			copy[key] = "foreign"
			if got := runserverCandidateWorkspace(testenv.Sorted(copy)); got != "" {
				t.Fatal("ambient telemetry authority accepted")
			}
		})
	}
	now := time.Now().UTC()
	buildRoot, err := os.MkdirTemp(t.TempDir(), "godj-projectcheck-")
	if err != nil {
		t.Fatal(err)
	}
	buildEnvironment := map[string]string{"GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local"}
	for key, suffix := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "xdg-config", "XDG_CACHE_HOME": "xdg-cache", "TMPDIR": "tmp", "GOTMPDIR": "gotmp", "TEST_TELEMETRY_DIR": "telemetry"} {
		buildEnvironment[key] = filepath.Join(buildRoot, suffix)
	}
	if runserverBuildWorkspace(testenv.Sorted(buildEnvironment)) != buildRoot || runserverCandidateWorkspace(testenv.Sorted(buildEnvironment)) != "" || runserverBuildWorkspace(testenv.Sorted(values)) != "" {
		t.Fatal("declaration/runtime build ownership and candidate metadata ownership were mixed")
	}
	for key := range buildEnvironment {
		changed := testenv.With(testenv.Sorted(buildEnvironment), map[string]string{key: "foreign"})
		if runserverBuildWorkspace(changed) != "" {
			t.Fatal("unowned build environment accepted", key)
		}
	}
	records := []runserverGoAuditRecord{
		{Arguments: []string{"build", "-buildvcs=false", "-mod=readonly", "-o", filepath.Join(buildRoot, "runner"), "./cmd/projectrunner"}, StartedAt: now, BuildWorkspace: buildRoot},
		{Arguments: []string{"telemetry", "off"}, StartedAt: now, CandidateWorkspace: root},
		{Arguments: []string{"list", "-mod=readonly", "-find", "-json", "github.com/progresshans/godj/identity/models"}, StartedAt: now, CandidateWorkspace: root},
		{Arguments: []string{"build", "-buildvcs=false", "-mod=readonly", "-o", filepath.Join(buildRoot, "site"), "./cmd/site"}, StartedAt: now, BuildWorkspace: buildRoot},
	}
	encode := func(records []runserverGoAuditRecord) []byte {
		var out []byte
		for _, record := range records {
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			out = append(append(out, raw...), '\n')
		}
		return out
	}
	want := []string{"./cmd/projectrunner", "./cmd/site"}
	if err := validateRunserverGoBuildAudit(encode(records), want); err != nil {
		t.Fatal(err)
	}
	mutations := []func([]runserverGoAuditRecord) []runserverGoAuditRecord{
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord { v[1].CandidateWorkspace = ""; return v },
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord {
			v[2].CandidateWorkspace = root + "-other"
			return v
		},
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord {
			v[1].Arguments = []string{"telemetry", "on"}
			return v
		},
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord {
			v[1].Arguments = []string{"telemetry", "off", "extra"}
			return v
		},
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord { return append(v[:1], v[2:]...) },
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord { return append(v[:2], v[3:]...) },
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord {
			return append(append(v[:3:3], v[1], v[2]), v[3])
		},
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord { v[1], v[0] = v[0], v[1]; return v },
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord {
			v[3].Arguments = []string{"test", "./..."}
			return v
		},
		func(v []runserverGoAuditRecord) []runserverGoAuditRecord { v[0].BuildWorkspace = ""; return v },
	}
	for index, mutate := range mutations {
		if err := validateRunserverGoBuildAudit(encode(mutate(slices.Clone(records))), want); err == nil {
			t.Fatalf("invalid command sequence %d accepted", index)
		}
	}
	inventory := runserverGoAuditRecord{Arguments: []string{"list", "-deps", "-e", "-json", "-mod=readonly", "./cmd/projectrunner", "./cmd/site"}, StartedAt: now, BuildWorkspace: buildRoot}
	watch := append(append([]runserverGoAuditRecord{inventory}, records...), inventory)
	if err := validateRunserverGoBuildAudit(encode(watch), want); err != nil {
		t.Fatal("watch source fences rejected", err)
	}
	for name, mutate := range map[string]func([]runserverGoAuditRecord) []runserverGoAuditRecord{
		"missing-before": func(v []runserverGoAuditRecord) []runserverGoAuditRecord { return v[1:] },
		"missing-after":  func(v []runserverGoAuditRecord) []runserverGoAuditRecord { return v[:len(v)-1] },
		"different-owner": func(v []runserverGoAuditRecord) []runserverGoAuditRecord {
			v[0].BuildWorkspace += "-foreign"
			return v
		},
		"wrong-input": func(v []runserverGoAuditRecord) []runserverGoAuditRecord {
			v[0].Arguments = []string{"list", "./..."}
			return v
		},
		"late-before": func(v []runserverGoAuditRecord) []runserverGoAuditRecord { v[0], v[1] = v[1], v[0]; return v },
		"extra-fence": func(v []runserverGoAuditRecord) []runserverGoAuditRecord { return append(v, inventory) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateRunserverGoBuildAudit(encode(mutate(slices.Clone(watch))), want); err == nil {
				t.Fatal("unowned or incomplete source fence accepted")
			}
		})
	}
}

// Record only a workspace whose current environment confines telemetry mode
// writes. The command audit must not accept telemetry off against the user's
// HOME or an injected telemetry directory.
func runserverCandidateWorkspace(environment []string) string {
	values := testenv.Map(environment)
	root := filepath.Dir(values["HOME"])
	if !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), "godj-candidate-") {
		return ""
	}
	for key, suffix := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "xdg-config", "XDG_CACHE_HOME": "xdg-cache", "TMPDIR": "tmp", "GOTMPDIR": "gotmp"} {
		if values[key] != filepath.Join(root, suffix) {
			return ""
		}
	}
	for _, key := range []string{"TEST_TELEMETRY_DIR", "GO_TELEMETRY_CHILD", "GO_TELEMETRY_CHILD_UPLOAD", "GOTELEMETRY", "GOTELEMETRYDIR"} {
		if _, exists := values[key]; exists {
			return ""
		}
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || values["GOENV"] != "off" {
		return ""
	}
	return root
}

// Source inventory and executable builds use the retained project's workspace,
// not the separate generation candidate used by external-model metadata queries.
func runserverBuildWorkspace(environment []string) string {
	values := testenv.Map(environment)
	root := filepath.Dir(values["HOME"])
	if !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), "godj-projectcheck-") || values["GOENV"] != "off" || values["GOWORK"] != "off" || values["GOTOOLCHAIN"] != "local" {
		return ""
	}
	for key, suffix := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "xdg-config", "XDG_CACHE_HOME": "xdg-cache", "TMPDIR": "tmp", "GOTMPDIR": "gotmp", "TEST_TELEMETRY_DIR": "telemetry"} {
		if values[key] != filepath.Join(root, suffix) {
			return ""
		}
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return ""
	}
	return root
}

// Report only fixed phase names and elapsed time. Audit arguments include
// private workspace paths; neither those values nor arbitrary child output
// belong in a readiness failure diagnostic. A start record is not completion.
func runserverPreparationProgress(environment []string, started, now time.Time) string {
	prefix := fmt.Sprintf("elapsed=%s", max(time.Duration(0), now.Sub(started)).Round(time.Millisecond))
	path := testenv.Map(environment)[runserverGoAuditLogEnv]
	file, err := os.Open(path)
	if err != nil {
		return prefix + " go_audit=unavailable"
	}
	document, readErr := io.ReadAll(io.LimitReader(file, maximumRunserverGoAudit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(document) > maximumRunserverGoAudit || len(bytes.TrimSpace(document)) == 0 {
		return prefix + " go_audit=unavailable"
	}
	lines := bytes.Split(bytes.TrimSpace(document), []byte{'\n'})
	lastPhase := ""
	var lastStart time.Time
	for _, line := range lines {
		var record runserverGoAuditRecord
		if err := json.Unmarshal(line, &record); err != nil || record.StartedAt.IsZero() {
			return prefix + " go_audit=invalid"
		}
		arguments := record.Arguments
		switch {
		case reflect.DeepEqual(arguments, []string{"list", "-deps", "-e", "-json", "-mod=readonly", "./cmd/projectrunner", "./cmd/site"}) && record.BuildWorkspace != "":
			lastPhase = "source_inventory"
		case reflect.DeepEqual(arguments, []string{"telemetry", "off"}) && record.CandidateWorkspace != "":
			lastPhase = "private_telemetry_init"
		case reflect.DeepEqual(arguments, []string{"list", "-mod=readonly", "-find", "-json", "github.com/progresshans/godj/identity/models"}):
			lastPhase = "identity_metadata"
		case reflect.DeepEqual(arguments, []string{"list", "-mod=mod", "-deps", "./cmd/projectrunner"}) && record.BuildWorkspace != "":
			lastPhase = "scaffold_dependencies"
		case reflect.DeepEqual(arguments, []string{"mod", "tidy"}) && record.BuildWorkspace != "":
			lastPhase = "module_tidy"
		case len(arguments) > 1 && arguments[0] == "list" && record.CandidateWorkspace != "":
			lastPhase = "candidate_inventory"
		case len(arguments) > 1 && arguments[0] == "test" && arguments[1] == "-c" && record.CandidateWorkspace != "":
			lastPhase = "candidate_compile"
		case len(arguments) == 6 && arguments[0] == "build" && arguments[1] == "-buildvcs=false" && arguments[2] == "-mod=readonly" && arguments[3] == "-o" && filepath.IsAbs(arguments[4]):
			switch arguments[5] {
			case "./cmd/projectrunner":
				lastPhase = "declaration_build"
			case "./cmd/site":
				lastPhase = "runtime_build"
			case "./cmd/commands":
				lastPhase = "command_runtime_build"
			default:
				return prefix + " go_audit=invalid"
			}
		default:
			return prefix + " go_audit=invalid"
		}
		lastStart = record.StartedAt
	}
	return fmt.Sprintf("%s go_commands_started=%d last_go_phase=%s last_go_started_ago=%s", prefix, len(lines), lastPhase, max(time.Duration(0), now.Sub(lastStart)).Round(time.Millisecond))
}

func TestRunserverPreparationDiagnosticsExcludeArgumentsAndDistinguishUnavailableAudit(t *testing.T) {
	started := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	now := started.Add(7 * time.Second)
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	environment := []string{runserverGoAuditLogEnv + "=" + path}
	if got := runserverPreparationProgress(environment, started, now); got != "elapsed=7s go_audit=unavailable" {
		t.Fatal("missing audit was described as a running phase", got)
	}
	const secret = "never-publish-private-argument"
	var document []byte
	for index, arguments := range [][]string{
		{"build", "-buildvcs=false", "-mod=readonly", "-o", filepath.Join(t.TempDir(), secret), "./cmd/projectrunner"},
		{"telemetry", "off"},
		{"list", "-mod=readonly", "-find", "-json", "github.com/progresshans/godj/identity/models"},
		{"build", "-buildvcs=false", "-mod=readonly", "-o", filepath.Join(t.TempDir(), secret), "./cmd/site"},
	} {
		line, err := json.Marshal(runserverGoAuditRecord{Arguments: arguments, StartedAt: started.Add(time.Duration(index+2) * time.Second), CandidateWorkspace: filepath.Join(t.TempDir(), secret)})
		if err != nil {
			t.Fatal(err)
		}
		document = append(append(document, line...), '\n')
	}
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	want := "elapsed=7s go_commands_started=4 last_go_phase=runtime_build last_go_started_ago=2s"
	if got := runserverPreparationProgress(environment, started, now); got != want || strings.Contains(got, secret) {
		t.Fatal("diagnostic leaked arguments or invented phase completion", got)
	}
	var scaffold []byte
	for index, arguments := range [][]string{
		{"list", "-mod=mod", "-deps", "./cmd/projectrunner"},
		{"build", "-buildvcs=false", "-mod=readonly", "-o", filepath.Join(t.TempDir(), secret), "./cmd/projectrunner"},
		{"telemetry", "off"},
		{"list", "-m", "-json", "-buildvcs=false", "-mod=readonly"},
		{"list", "-json", "-buildvcs=false", "-mod=readonly", "-overlay=" + secret, "./..."},
		{"test", "-c", "-overlay=" + secret, "-o", filepath.Join(t.TempDir(), secret), "example.com/" + secret},
		{"mod", "tidy"},
		{"build", "-buildvcs=false", "-mod=readonly", "-o", filepath.Join(t.TempDir(), secret), "./cmd/commands"},
	} {
		record := runserverGoAuditRecord{Arguments: arguments, StartedAt: started.Add(time.Duration(index) * time.Second), CandidateWorkspace: secret, BuildWorkspace: secret}
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		scaffold = append(append(scaffold, line...), '\n')
	}
	if err := os.WriteFile(path, scaffold, 0o600); err != nil {
		t.Fatal(err)
	}
	want = "elapsed=20s go_commands_started=8 last_go_phase=command_runtime_build last_go_started_ago=13s"
	if got := runserverPreparationProgress(environment, started, started.Add(20*time.Second)); got != want || strings.Contains(got, secret) {
		t.Fatal("scaffold progress leaked private source or omitted an actual command", got)
	}
	for _, payload := range [][]byte{[]byte("{" + secret), []byte(`{"arguments":["` + secret + `"],"started_at":"2026-10-15T00:00:01Z"}`), []byte(`{"arguments":[],"started_at":"0001-01-01T00:00:00Z"}`)} {
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := runserverPreparationProgress(environment, started, now); got != "elapsed=7s go_audit=invalid" || strings.Contains(got, secret) {
			t.Fatal("invalid audit escaped the fixed diagnostic vocabulary", got)
		}
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maximumRunserverGoAudit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := runserverPreparationProgress(environment, started, now); got != "elapsed=7s go_audit=unavailable" {
		t.Fatal("oversized audit was accepted", got)
	}
}
