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
	Arguments []string  `json:"arguments"`
	StartedAt time.Time `json:"started_at"`
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
		{"list", "-mod=readonly", "-find", "-json", "github.com/progresshans/godj/identity/models"},
		{"build", "-buildvcs=false", "-mod=readonly", "-o", filepath.Join(t.TempDir(), secret), "./cmd/site"},
	} {
		line, err := json.Marshal(runserverGoAuditRecord{arguments, started.Add(time.Duration(index+3) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		document = append(append(document, line...), '\n')
	}
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	want := "elapsed=7s go_commands_started=3 last_go_phase=runtime_build last_go_started_ago=2s"
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
