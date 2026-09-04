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
	loaded := testLoadedDefinitionSet([]Migration{
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

// testLoadedDefinitionSet is deliberately test-only. Production callers can
// obtain an executable lifecycle authority only through definition.Load.
func testLoadedDefinitionSet(definitions []Migration) LoadedDefinitionSet {
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
		cloneMigrationDefinitions,
		func(values []DefinitionSourceInfo) []DefinitionSourceInfo {
			return append([]DefinitionSourceInfo(nil), values...)
		},
	)
	return LoadedDefinitionSet(publication)
}
