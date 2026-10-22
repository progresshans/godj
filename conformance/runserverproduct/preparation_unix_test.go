//go:build darwin || linux

package runserverproduct_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/testenv"
)

// Readiness includes both readonly Go builds and generated-bundle validation.
// Give preparation its own bounded compiler budget. HTTP exercises, shutdown
// grace, cancellation, process-tree absence and output limits remain separate.
const runserverPreparationTimeout = 5 * time.Minute
const maximumRunserverGoAudit = 64 << 10

type runserverGoAuditRecord struct {
	Arguments          []string  `json:"arguments"`
	StartedAt          time.Time `json:"started_at"`
	CandidateWorkspace string    `json:"candidate_workspace,omitempty"`
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
	records := []runserverGoAuditRecord{
		{Arguments: []string{"build", "-buildvcs=false", "-mod=readonly", "-o", filepath.Join(root, "runner"), "./cmd/projectrunner"}, StartedAt: now},
		{Arguments: []string{"telemetry", "off"}, StartedAt: now, CandidateWorkspace: root},
		{Arguments: []string{"list", "-mod=readonly", "-find", "-json", "github.com/progresshans/godj/identity/models"}, StartedAt: now, CandidateWorkspace: root},
		{Arguments: []string{"build", "-buildvcs=false", "-mod=readonly", "-o", filepath.Join(root, "site"), "./cmd/site"}, StartedAt: now},
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
	}
	for index, mutate := range mutations {
		if err := validateRunserverGoBuildAudit(encode(mutate(slices.Clone(records))), want); err == nil {
			t.Fatalf("invalid command sequence %d accepted", index)
		}
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
		case reflect.DeepEqual(arguments, []string{"telemetry", "off"}) && record.CandidateWorkspace != "":
			lastPhase = "private_telemetry_init"
		case reflect.DeepEqual(arguments, []string{"list", "-mod=readonly", "-find", "-json", "github.com/progresshans/godj/identity/models"}):
			lastPhase = "identity_metadata"
		case len(arguments) == 6 && arguments[0] == "build" && arguments[1] == "-buildvcs=false" && arguments[2] == "-mod=readonly" && arguments[3] == "-o" && filepath.IsAbs(arguments[4]):
			switch arguments[5] {
			case "./cmd/projectrunner":
				lastPhase = "declaration_build"
			case "./cmd/site":
				lastPhase = "runtime_build"
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
