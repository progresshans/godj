package externalfake

import (
	"context"
	"errors"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

// This external-package fake deliberately implements only the existing public
// migration ports. The test-only fence candidate does not add methods to them.
type legacyBackendFake struct{}

var _ migrationbackend.AppliedMigrationReader = (*legacyBackendFake)(nil)
var _ migrationbackend.AtomicBackend = (*legacyBackendFake)(nil)

func (*legacyBackendFake) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	return []migrationbackend.AppliedMigration{{App: "legacy", Name: "0001"}}, nil
}

func (*legacyBackendFake) BeginMigration(context.Context) (migrationbackend.Transaction, error) {
	return nil, errors.New("compile-only legacy transaction")
}

func TestExistingExternalFakeRemainsSourceCompatible(t *testing.T) {
	fake := &legacyBackendFake{}
	records, err := fake.ReadAppliedMigrations(context.Background())
	if err != nil || len(records) != 1 {
		t.Fatalf("legacy reader: records=%v err=%v", records, err)
	}
	if _, err := fake.BeginMigration(context.Background()); err == nil {
		t.Fatal("legacy fake begin unexpectedly succeeded")
	}
}
