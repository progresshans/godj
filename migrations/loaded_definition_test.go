package migrations

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations/internal/loadeddefinition"
)

func TestLoadedDefinitionSetStatusesPreservesLoaderAuthority(t *testing.T) {
	alpha1 := MigrationKey{App: "alpha", Name: "0001"}
	alpha2 := MigrationKey{App: "alpha", Name: "0002"}
	loaded := testLoadedDefinitionSet(t, []Migration{
		{App: alpha2.App, Name: alpha2.Name, Dependencies: []MigrationKey{alpha1}},
		{App: alpha1.App, Name: alpha1.Name},
	})
	applied, err := NewAppliedState(alpha1)
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := loaded.Statuses(applied)
	if err != nil {
		t.Fatal(err)
	}
	want := []MigrationStatusEntry{
		{Key: alpha1, Status: MigrationStatusApplied},
		{Key: alpha2, Status: MigrationStatusUnapplied},
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("Statuses() = %+v, want %+v", statuses, want)
	}
	_, err = (LoadedDefinitionSet{}).Statuses(AppliedState{})
	var migrationError *Error
	if !errors.As(err, &migrationError) || migrationError.Category != CategoryState || migrationError.Code != CodeInvalidState {
		t.Fatalf("zero LoadedDefinitionSet.Statuses() error = %#v, want state/invalid_state", err)
	}
}

func TestLoadedDefinitionSetDigestDoesNotAllocateOrExposeDefinitions(t *testing.T) {
	definitions := make([]Migration, 1_000)
	for index := range definitions {
		definitions[index] = Migration{App: fmt.Sprintf("app%d", index), Name: "0001"}
	}
	loaded := testLoadedDefinitionSet(t, definitions)
	var digest string
	if allocations := testing.AllocsPerRun(20, func() { digest = loaded.Digest() }); allocations != 0 {
		t.Fatalf("Digest allocated %.0f times for metadata-only access", allocations)
	}
	if digest == "" {
		t.Fatal("initialized publication lost its digest")
	}
	definitions[0].App = "changed input"
	copy := loaded.Definitions()
	copy[0].Name = "changed diagnostic copy"
	sources := loaded.Sources()
	sources[0].SourceID = "changed source copy"
	statuses, err := loaded.Statuses(AppliedState{})
	if err != nil || len(statuses) != len(definitions) || statuses[0].Key != (MigrationKey{App: "app0", Name: "0001"}) {
		t.Fatalf("prepared graph retained caller mutation: statuses=%d error=%v", len(statuses), err)
	}
	if loaded.Sources()[0].SourceID == "changed source copy" || loaded.Definitions()[0].Name != "0001" || loaded.Digest() != digest {
		t.Fatal("metadata inspection exposed publication storage")
	}
}

// testLoadedDefinitionSet is deliberately test-only. Production callers can
// obtain an executable lifecycle authority only through definition.Load.
func testLoadedDefinitionSet(t *testing.T, definitions []Migration) LoadedDefinitionSet {
	t.Helper()
	planner, err := NewPlanner(definitions...)
	if err != nil {
		t.Fatalf("invalid loaded fixture graph: %v", err)
	}
	sources := make([]DefinitionSourceInfo, len(definitions))
	for index := range definitions {
		sources[index] = DefinitionSourceInfo{
			SourceID:  fmt.Sprintf("test-definition-%04d", index),
			Producer:  DefinitionProducer{Name: "migrations-test", Version: "1"},
			Migration: definitions[index].Key(),
		}
	}
	publication := loadeddefinition.New(
		definitions,
		"sha256:test-loaded-definition-set",
		sources,
		planner,
		cloneMigrationDefinitions,
		func(values []DefinitionSourceInfo) []DefinitionSourceInfo {
			return append([]DefinitionSourceInfo(nil), values...)
		},
	)
	return LoadedDefinitionSet(publication)
}
