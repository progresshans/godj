package migrationrelationproduct

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestObserveCasesTwiceDeterministically(t *testing.T) {
	wantCases := []Case{
		CaseLegacyABI,
		CaseProfileDispatch,
		CaseMixedDigest,
		CaseStatePromotion,
		CaseStructuralPreflight,
		CaseCreateLifecycle,
		CaseAddRelation,
		CaseRemoveRemake,
		CasePhysicalFKPolicy,
		CaseFileRestart,
		CasePrecommitFaults,
		CaseCommitOutcomes,
	}
	if !reflect.DeepEqual(Cases(), wantCases) {
		t.Fatalf("Cases() = %#v, want stable exact order %#v", Cases(), wantCases)
	}
	databaseCases := map[Case]bool{
		CaseCreateLifecycle:  true,
		CaseAddRelation:      true,
		CaseRemoveRemake:     true,
		CasePhysicalFKPolicy: true,
		CaseFileRestart:      true,
		CasePrecommitFaults:  true,
	}
	for _, selected := range Cases() {
		t.Run(string(selected), func(t *testing.T) {
			first, err := Observe(context.Background(), selected)
			if err != nil {
				t.Fatalf("first Observe(%s): %v", selected, err)
			}
			second, err := Observe(context.Background(), selected)
			if err != nil {
				t.Fatalf("second Observe(%s): %v", selected, err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("Observe(%s) changed across independent databases:\nfirst=%#v\nsecond=%#v", selected, first, second)
			}
			if first.Case != selected || len(first.Outcomes) == 0 || first.Metrics.Loads == nil || first.Metrics.Trace == nil {
				t.Fatalf("Observe(%s) incomplete typed facts: %#v", selected, first)
			}
			if databaseCases[selected] != (first.Database != nil) {
				t.Fatalf("Observe(%s) database presence = %t, want %t", selected, first.Database != nil, databaseCases[selected])
			}
			if first.Database != nil {
				if first.Database.Snapshots == nil || len(first.Database.Snapshots) == 0 {
					t.Fatalf("Observe(%s) published no database snapshots", selected)
				}
				for _, snapshot := range first.Database.Snapshots {
					assertSnapshotSorted(t, selected, snapshot)
				}
			}
			text := strings.ToLower(strings.TrimSpace(formatObservation(first)))
			for _, forbidden := range []string{"/var/folders/", "/tmp/", "file:/", "sqlite3?mode="} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("Observe(%s) leaked disposable path fragment %q", selected, forbidden)
				}
			}
		})
	}
}

func TestObserverOwnedSourceMutationChangesTypedFactsWithoutChangingSemanticDigest(t *testing.T) {
	baseMetrics := Metrics{Loads: make([]LoadFact, 0), Trace: make([]TraceEvent, 0)}
	_, base := loadOutcome("load", &baseMetrics, sourceFor(sourceLegacyAuthor))
	changedSource := sourceFor(sourceLegacyAuthor)
	changedSource.SourceID = "owned-mutated-source"
	changedSource.Document = []byte(strings.Replace(string(changedSource.Document), `"version":"1"`, `"version":"2"`, 1))
	changedMetrics := Metrics{Loads: make([]LoadFact, 0), Trace: make([]TraceEvent, 0)}
	_, changed := loadOutcome("load", &changedMetrics, changedSource)
	if !base.Accepted || !changed.Accepted {
		t.Fatalf("owned mutation loads = base:%#v changed:%#v", base.Error, changed.Error)
	}
	if base.Digest != changed.Digest {
		t.Fatalf("non-semantic source mutation changed digest: %q != %q", base.Digest, changed.Digest)
	}
	if reflect.DeepEqual(base.Sources, changed.Sources) {
		t.Fatal("owned source mutation left source facts unchanged")
	}
	if !reflect.DeepEqual(base.Definitions, changed.Definitions) {
		t.Fatal("owned source mutation changed semantic definitions")
	}
	if !reflect.DeepEqual(baseMetrics.Loads, changedMetrics.Loads) {
		t.Fatal("owned source mutation changed public load counters")
	}
}

func TestObserverContextAndCaseBoundary(t *testing.T) {
	if _, err := Observe(nil, CaseLegacyABI); err == nil {
		t.Fatal("Observe(nil) succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Observe(canceled, CaseLegacyABI); err == nil {
		t.Fatal("Observe(canceled) succeeded")
	}
	if _, err := Observe(context.Background(), Case("unknown")); err == nil {
		t.Fatal("Observe(unknown) succeeded")
	}
}

func TestObserverUsesOnlyPublicProductDependenciesAndOwnedIO(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "observer.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	allowedImports := map[string]bool{
		"context": true, "database/sql": true, "errors": true, "fmt": true,
		"os": true, "path/filepath": true, "sort": true, "strconv": true, "strings": true,
		"github.com/progresshans/godj/db/sqlite":             true,
		"github.com/progresshans/godj/migrations":            true,
		"github.com/progresshans/godj/migrations/backend":    true,
		"github.com/progresshans/godj/migrations/definition": true,
		"github.com/progresshans/godj/schema/ir":             true,
	}
	for _, imported := range file.Imports {
		path := strings.Trim(imported.Path.Value, `"`)
		if !allowedImports[path] {
			t.Errorf("observer import %q is outside the public product boundary", path)
		}
		if (path == "os" || path == "path/filepath") && imported.Name != nil {
			t.Errorf("observer filesystem import %q must use its canonical local name", path)
		}
	}
	forbiddenCalls := map[string]bool{
		"ReadFile": true, "OpenFile": true, "ReadAll": true,
		"Unmarshal": true, "NewDecoder": true,
	}
	allowedFilesystemCalls := map[string]map[string]bool{
		"os": {
			"MkdirTemp": true,
			"RemoveAll": true,
		},
		"filepath": {
			"Join":    true,
			"ToSlash": true,
		},
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, selectorNode := node.(*ast.SelectorExpr)
		if selectorNode {
			identifier, packageSelector := selector.X.(*ast.Ident)
			if packageSelector {
				if allowed, filesystemPackage := allowedFilesystemCalls[identifier.Name]; filesystemPackage && !allowed[selector.Sel.Name] {
					t.Errorf("observer contains non-allowlisted %s.%s filesystem selector", identifier.Name, selector.Sel.Name)
				}
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		calledSelector, selectorCall := call.Fun.(*ast.SelectorExpr)
		if selectorCall && forbiddenCalls[calledSelector.Sel.Name] {
			t.Errorf("observer contains forbidden artifact/file decode call %s", calledSelector.Sel.Name)
		}
		return true
	})
	sourceBytes, err := os.ReadFile("observer.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	for _, forbidden := range []string{
		"conformance/internal/protocol",
		"migrations/internal/",
		"/oracles/",
		"not-implemented",
		"sha256sums",
		"migration-relation-oracle",
		"runners/django",
		"protocol.compare",
		"mig-",
	} {
		if strings.Contains(strings.ToLower(source), forbidden) {
			t.Errorf("observer source contains forbidden reference shortcut %q", forbidden)
		}
	}
}

func assertSnapshotSorted(t *testing.T, selected Case, snapshot DatabaseSnapshot) {
	t.Helper()
	tableNames := make([]string, len(snapshot.Tables))
	for index, table := range snapshot.Tables {
		tableNames[index] = table.Name
	}
	if !sort.StringsAreSorted(tableNames) {
		t.Fatalf("Observe(%s) snapshot %s tables are not sorted: %v", selected, snapshot.Stage, tableNames)
	}
	for index := 1; index < len(snapshot.History); index++ {
		previous, current := snapshot.History[index-1], snapshot.History[index]
		if previous.App > current.App || previous.App == current.App && previous.Name >= current.Name {
			t.Fatalf("Observe(%s) snapshot %s history is not strictly sorted: %v", selected, snapshot.Stage, snapshot.History)
		}
	}
}

func formatObservation(value Observation) string {
	return fmt.Sprintf("%#v", value)
}
