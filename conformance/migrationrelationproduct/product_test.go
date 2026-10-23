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
		CaseCurrentABI,
		CaseCurrentFormat,
		CaseCurrentDigest,
		CaseCurrentState,
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

func TestCurrentFormatValidationUsesOneExactEnvelopeAndFailsClosed(t *testing.T) {
	observation, err := Observe(context.Background(), CaseCurrentFormat)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name      string
		accepted  bool
		code      string
		pointer   string
		reason    string
		stage     string
		published int
	}{
		{name: "exact_current", accepted: true, published: 1},
		{name: "missing_format_version", code: "invalid_definition_document", pointer: "/format_version", reason: "missing_field", stage: "document"},
		{name: "unknown_format_version", code: "definition_format_incompatible", pointer: "/format_version", reason: "format_version", stage: "format"},
		{name: "wrong_type_format_version", code: "invalid_definition_document", pointer: "/format_version", reason: "wrong_type", stage: "document"},
		{name: "overflow_format_version", code: "invalid_definition_document", pointer: "/format_version", reason: "out_of_range", stage: "document"},
		{name: "retired_compatibility_tuple", code: "invalid_definition_document", pointer: "/compatibility", reason: "unknown_field", stage: "document"},
	}
	if len(observation.Outcomes) != len(want)+1 {
		t.Fatalf("current format outcomes = %d, want %d cases plus constants", len(observation.Outcomes), len(want))
	}
	if len(observation.Metrics.Loads) != len(want) {
		t.Fatalf("current format load facts = %d, want %d", len(observation.Metrics.Loads), len(want))
	}
	for index, expected := range want {
		outcome := observation.Outcomes[index]
		load := observation.Metrics.Loads[index]
		if outcome.Name != expected.name || load.Name != expected.name || outcome.Accepted != expected.accepted {
			t.Fatalf("current format case %d identity/accepted = %q/%q/%t, want %q/%q/%t", index, outcome.Name, load.Name, outcome.Accepted, expected.name, expected.name, expected.accepted)
		}
		if outcome.Error.Code != expected.code || outcome.Error.JSONPointer != expected.pointer || outcome.Error.Reason != expected.reason || outcome.Error.Stage != expected.stage {
			t.Fatalf("current format case %s error = %#v, want %s %s %s %s", expected.name, outcome.Error, expected.code, expected.pointer, expected.reason, expected.stage)
		}
		if expected.accepted {
			if outcome.Error.Present || outcome.Error.Category != "" {
				t.Fatalf("current format accepted case %s published an error: %#v", expected.name, outcome.Error)
			}
		} else if !outcome.Error.Present || outcome.Error.Category != "migration_definition_source_error" {
			t.Fatalf("current format rejected case %s error presence/category = %#v", expected.name, outcome.Error)
		}
		if load.DefinitionsPublished != expected.published || load.DefinitionSetsPublished != expected.published {
			t.Fatalf("current format case %s publication = definitions:%d sets:%d, want %d/%d", expected.name, load.DefinitionsPublished, load.DefinitionSetsPublished, expected.published, expected.published)
		}
	}
	constants := observation.Outcomes[len(want)]
	if constants.Name != "public_constants" || !constants.Accepted || len(constants.Integers) != 3 {
		t.Fatalf("current public constants = %#v", constants)
	}
	for _, value := range constants.Integers {
		if value.Value != 1 {
			t.Fatalf("current public constant %s = %d, want 1", value.Name, value.Value)
		}
	}
	if reflect.DeepEqual(currentAuthorDocument(), withoutFormatVersion(currentAuthorDocument())) ||
		reflect.DeepEqual(currentAuthorDocument(), withFormatVersion(currentAuthorDocument(), "2")) ||
		reflect.DeepEqual(currentAuthorDocument(), withRetiredCompatibilityTuple(currentAuthorDocument())) {
		t.Fatal("current format negative fixture transformation left source bytes unchanged")
	}
}

func TestCurrentABIUsesOneFormatAcrossRelationLifecycle(t *testing.T) {
	observation, err := Observe(context.Background(), CaseCurrentABI)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Outcomes) != 3 {
		t.Fatalf("current ABI outcomes = %d, want load/latest/zero", len(observation.Outcomes))
	}
	load, latest, zero := observation.Outcomes[0], observation.Outcomes[1], observation.Outcomes[2]
	if load.Name != "current_load" || latest.Name != "latest" || zero.Name != "zero_blog" ||
		!load.Accepted || !latest.Accepted || !zero.Accepted {
		t.Fatalf("current ABI lifecycle = load:%#v latest:%#v zero:%#v", load, latest, zero)
	}
	for _, name := range []string{"definition_format", "schema_ir", "state_format"} {
		if value := namedInteger(load.Integers, name); value != 1 {
			t.Fatalf("current ABI %s = %d, want 1", name, value)
		}
	}
	retiredTuplePresent, retiredTupleFact := lookupNamedBoolean(load.Booleans, "retired_compatibility_tuple_present")
	if !retiredTupleFact || retiredTuplePresent ||
		!namedBoolean(load.Booleans, "scalar_and_relation_share_format") {
		t.Fatalf("current ABI compatibility facts = %#v", load.Booleans)
	}
	if !definitionsHaveRelation(load.Definitions) || !stateHasRelation(latest.State) {
		t.Fatalf("current ABI lost relation facts: load=%#v latest=%#v", load.Definitions, latest.State)
	}
	if latest.State.FormatVersion != 1 || zero.State.FormatVersion != 1 {
		t.Fatalf("current ABI state formats = latest:%d zero:%d, want 1/1", latest.State.FormatVersion, zero.State.FormatVersion)
	}
}

func TestCurrentDigestUsesOneDomainAndSemanticSetIdentity(t *testing.T) {
	observation, err := Observe(context.Background(), CaseCurrentDigest)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"scalar_only", "relation_only", "combined", "combined_permuted"}
	if len(observation.Outcomes) != len(wantNames) {
		t.Fatalf("current digest outcomes = %d, want %d", len(observation.Outcomes), len(wantNames))
	}
	for index, name := range wantNames {
		outcome := observation.Outcomes[index]
		if outcome.Name != name || !outcome.Accepted || outcome.Digest == "" {
			t.Fatalf("current digest outcome %d = %#v, want accepted %s with digest", index, outcome, name)
		}
	}
	scalar, relation := observation.Outcomes[0], observation.Outcomes[1]
	combined, permuted := observation.Outcomes[2], observation.Outcomes[3]
	if definitionsHaveRelation(scalar.Definitions) || !definitionsHaveRelation(relation.Definitions) ||
		!definitionsHaveRelation(combined.Definitions) || !definitionsHaveRelation(permuted.Definitions) {
		t.Fatalf("current digest relation membership = scalar:%t relation:%t combined:%t permuted:%t",
			definitionsHaveRelation(scalar.Definitions), definitionsHaveRelation(relation.Definitions),
			definitionsHaveRelation(combined.Definitions), definitionsHaveRelation(permuted.Definitions))
	}
	semanticDigests := map[string]struct{}{scalar.Digest: {}, relation.Digest: {}, combined.Digest: {}}
	if len(semanticDigests) != 3 {
		t.Fatalf("scalar/relation/combined digests are not distinct: %q %q %q", scalar.Digest, relation.Digest, combined.Digest)
	}
	if permuted.Digest != combined.Digest || !namedBoolean(permuted.Booleans, "equals_combined_digest") {
		t.Fatalf("combined permutation changed digest: combined=%q permuted=%#v", combined.Digest, permuted)
	}
}

func TestCurrentStateUsesOneFormatWithoutPromotionOrDemotion(t *testing.T) {
	observation, err := Observe(context.Background(), CaseCurrentState)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Outcomes) != 3 {
		t.Fatalf("current state outcomes = %d, want load/forward/backward", len(observation.Outcomes))
	}
	load, forward, backward := observation.Outcomes[0], observation.Outcomes[1], observation.Outcomes[2]
	if load.Name != "current_load" || forward.Name != "forward" || backward.Name != "backward" ||
		!load.Accepted || !forward.Accepted || !backward.Accepted {
		t.Fatalf("current state lifecycle = load:%#v forward:%#v backward:%#v", load, forward, backward)
	}
	if forward.State.FormatVersion != 1 || backward.State.FormatVersion != 1 {
		t.Fatalf("current state format transitioned: forward=%d backward=%d", forward.State.FormatVersion, backward.State.FormatVersion)
	}
	if !stateHasRelation(forward.State) || !namedBoolean(forward.Booleans, "state_accessor_isolated") {
		t.Fatalf("current forward relation/alias facts = state:%#v booleans:%#v", forward.State, forward.Booleans)
	}
	if !reflect.DeepEqual(backward.State.Apps, []string{"authors"}) || len(backward.State.Models) != 1 ||
		backward.State.Models[0].App != "authors" || backward.State.Models[0].Name != "author" || stateHasRelation(backward.State) {
		t.Fatalf("current backward state = %#v, want only scalar authors.author", backward.State)
	}
}

func TestCurrentOnlyCasesAndUnifiedMigrationTraceAreExplicit(t *testing.T) {
	for _, selected := range Cases() {
		observation, err := Observe(context.Background(), selected)
		if err != nil {
			t.Fatalf("Observe(%s): %v", selected, err)
		}
		for _, event := range observation.Metrics.Trace {
			if event.Name == "begin_legacy" || event.Name == "begin_fenced" || event.Name == "begin_relation" {
				t.Fatalf("Observe(%s) emitted retired begin trace %#v", selected, event)
			}
		}
		for _, outcome := range observation.Outcomes {
			for _, intent := range outcome.Intents {
				hasRelation := false
				for _, operation := range intent.Operations {
					hasRelation = hasRelation || len(operation.Targets) != 0 || operation.Before.Fields != nil && modelHasRelation(operation.Before) || operation.After.Fields != nil && modelHasRelation(operation.After)
				}
				if intent.HasRelation != hasRelation {
					t.Fatalf("Observe(%s) intent relation fact = %t, derived %t: %#v", selected, intent.HasRelation, hasRelation, intent)
				}
			}
		}
	}
}

func TestStructuralPreflightCharacterizesThreeStagedNoMutationLanes(t *testing.T) {
	observation, err := Observe(context.Background(), CaseStructuralPreflight)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Outcomes) != 3 {
		t.Fatalf("structural preflight outcomes = %d, want static/history/physical", len(observation.Outcomes))
	}
	static, history, physical := observation.Outcomes[0], observation.Outcomes[1], observation.Outcomes[2]
	if static.Name != "static_invalid" || static.Accepted || namedInteger(static.Integers, "backend_trace_events") != 0 || namedString(static.Strings, "trace") != "" {
		t.Fatalf("static lane crossed backend boundary: %#v", static)
	}
	if static.Error.Category != "migration_state_error" || static.Error.Code != "invalid_state" {
		t.Fatalf("static lane error = %#v, want migration_state_error/invalid_state", static.Error)
	}
	if namedString(static.Strings, "capability_unavailable_error") != "migration_capability_unavailable" {
		t.Fatalf("static lane did not retire optional relation editor wording: %#v", static.Strings)
	}
	if history.Name != "history_invalid" || history.Accepted || namedInteger(history.Integers, "history_read_events") != 1 ||
		namedInteger(history.Integers, "session_open_events") != 1 || namedInteger(history.Integers, "begin_migration_events") != 0 || namedInteger(history.Integers, "mutation_events") != 0 {
		t.Fatalf("history lane boundary = %#v", history)
	}
	if history.Error.Category != "migration_history_error" || history.Error.Code != "inconsistent_applied_history" {
		t.Fatalf("history lane error = %#v, want migration_history_error/inconsistent_applied_history", history.Error)
	}
	if physical.Name != "physical_invalid" || physical.Accepted || !namedBoolean(physical.Booleans, "durable_unchanged") ||
		namedInteger(physical.Integers, "history_read_events") != 1 || namedInteger(physical.Integers, "begin_migration_events") != 1 ||
		namedInteger(physical.Integers, "mutation_events") != 0 {
		t.Fatalf("physical lane boundary = %#v", physical)
	}
	if physical.Error.Category != "migration_capability_error" || physical.Error.Code != "unsupported_operation" {
		t.Fatalf("physical lane error = %#v, want migration_capability_error/unsupported_operation", physical.Error)
	}
}

func modelHasRelation(model ModelFact) bool {
	for _, field := range model.Fields {
		if field.HasRelation {
			return true
		}
	}
	return false
}

func definitionsHaveRelation(definitions []DefinitionFact) bool {
	for _, definition := range definitions {
		for _, operation := range definition.Operations {
			if operation.Field.HasRelation || modelHasRelation(operation.Model) {
				return true
			}
		}
	}
	return false
}

func stateHasRelation(state StateFact) bool {
	for _, model := range state.Models {
		if modelHasRelation(model) {
			return true
		}
	}
	return false
}

func namedBoolean(values []NamedBooleanFact, name string) bool {
	value, _ := lookupNamedBoolean(values, name)
	return value
}

func lookupNamedBoolean(values []NamedBooleanFact, name string) (bool, bool) {
	for _, value := range values {
		if value.Name == name {
			return value.Value, true
		}
	}
	return false, false
}

func namedInteger(values []NamedIntegerFact, name string) int64 {
	for _, value := range values {
		if value.Name == name {
			return value.Value
		}
	}
	return -1
}

func namedString(values []NamedStringFact, name string) string {
	for _, value := range values {
		if value.Name == name {
			return value.Value
		}
	}
	return ""
}

func TestObserverOwnedSourceMutationChangesTypedFactsWithoutChangingSemanticDigest(t *testing.T) {
	baseMetrics := Metrics{Loads: make([]LoadFact, 0), Trace: make([]TraceEvent, 0)}
	_, base := loadOutcome("load", &baseMetrics, sourceFor(sourceCurrentAuthor))
	changedSource := sourceFor(sourceCurrentAuthor)
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
	if _, err := Observe(nil, CaseCurrentABI); err == nil {
		t.Fatal("Observe(nil) succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Observe(canceled, CaseCurrentABI); err == nil {
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
