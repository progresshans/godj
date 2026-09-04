package postgres

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

func TestPostgresMigrationHistoryFingerprintV1Goldens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		records []migrationbackend.AppliedMigration
		want    string
	}{
		{name: "empty", want: "af5570f5a1810b7af78caf4bc70a660f0df51e42baf91d4de5b2328de0e83dfc"},
		{
			name:    "alpha",
			records: []migrationbackend.AppliedMigration{{App: "alpha", Name: "0001"}},
			want:    "d082f0a0b67b8c2b5c7efc208270dd2e17c6a346d9b2fa0e572e6396dedff40e",
		},
		{
			name:    "utf8_byte_length",
			records: []migrationbackend.AppliedMigration{{App: "legacy", Name: "ä"}},
			want:    "35e542d7c4bce2ba60aa694f4301300cb1835e58ca14efe54a781ea7ae03e45c",
		},
		{
			name: "canonical_order",
			records: []migrationbackend.AppliedMigration{
				{App: "beta", Name: "0001"},
				{App: "alpha", Name: "0002"},
			},
			want: "10d5c73ea5b12735cec427f16e0b516ca9d8c235754ee9f8e6b8cab4f04e9f4a",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fingerprint := fingerprintPostgresMigrationHistory(test.records)
			if got := hex.EncodeToString(fingerprint[:]); got != test.want {
				t.Fatalf("fingerprint = %s, want %s", got, test.want)
			}
		})
	}
}

func TestPostgresMigrationHistorySuccessor(t *testing.T) {
	t.Parallel()
	before := []migrationbackend.AppliedMigration{
		{App: "beta", Name: "0001"},
		{App: "alpha", Name: "0001"},
	}
	apply := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "alpha", Name: "0002"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	applied, err := postgresMigrationHistorySuccessor(before, apply)
	if err != nil {
		t.Fatal(err)
	}
	wantApplied := []migrationbackend.AppliedMigration{
		{App: "alpha", Name: "0001"},
		{App: "alpha", Name: "0002"},
		{App: "beta", Name: "0001"},
	}
	if !reflect.DeepEqual(applied, wantApplied) {
		t.Fatalf("applied = %v, want %v", applied, wantApplied)
	}
	if !reflect.DeepEqual(before, []migrationbackend.AppliedMigration{
		{App: "beta", Name: "0001"},
		{App: "alpha", Name: "0001"},
	}) {
		t.Fatalf("input mutated: %v", before)
	}

	unapply := migrationbackend.HistoryTransition{Migration: apply.Migration, Kind: migrationbackend.HistoryTransitionUnapply}
	unapplied, err := postgresMigrationHistorySuccessor(applied, unapply)
	if err != nil {
		t.Fatal(err)
	}
	wantUnapplied := []migrationbackend.AppliedMigration{
		{App: "alpha", Name: "0001"},
		{App: "beta", Name: "0001"},
	}
	if !reflect.DeepEqual(unapplied, wantUnapplied) {
		t.Fatalf("unapplied = %v, want %v", unapplied, wantUnapplied)
	}
}

func TestPostgresMigrationHistorySuccessorRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()
	record := migrationbackend.AppliedMigration{App: "app", Name: "0001"}
	tests := []struct {
		name       string
		records    []migrationbackend.AppliedMigration
		transition migrationbackend.HistoryTransition
	}{
		{
			name:       "duplicate_apply",
			records:    []migrationbackend.AppliedMigration{record},
			transition: migrationbackend.HistoryTransition{Migration: record, Kind: migrationbackend.HistoryTransitionApply},
		},
		{
			name:       "missing_unapply",
			transition: migrationbackend.HistoryTransition{Migration: record, Kind: migrationbackend.HistoryTransitionUnapply},
		},
		{
			name:       "unknown_kind",
			transition: migrationbackend.HistoryTransition{Migration: record, Kind: 99},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := postgresMigrationHistorySuccessor(test.records, test.transition)
			var fenceError *migrationbackend.RevisionFenceError
			if !errors.As(err, &fenceError) || fenceError == nil || fenceError.Kind != migrationbackend.RevisionFenceFailureIntegrity {
				t.Fatalf("error = %v, want revision integrity", err)
			}
		})
	}
}

func TestInitializedPostgresMigrationRevisionMustBePositive(t *testing.T) {
	t.Parallel()
	for _, revision := range []int64{-1, 0} {
		revision := revision
		t.Run(fmt.Sprintf("revision_%d", revision), func(t *testing.T) {
			t.Parallel()
			assertPostgresRevisionIntegrity(t, validateInitializedPostgresMigrationRevision(revision))
		})
	}
	if err := validateInitializedPostgresMigrationRevision(1); err != nil {
		t.Fatalf("first initialized PostgreSQL migration revision: %v", err)
	}
}

func TestPostgresMigrationHistoryRecordLimitBoundary(t *testing.T) {
	t.Parallel()
	if err := validatePostgresMigrationHistoryRecordCount(postgresMigrationHistoryRecordLimit); err != nil {
		t.Fatalf("PostgreSQL migration history at current limit: %v", err)
	}
	assertPostgresRevisionIntegrity(
		t,
		validatePostgresMigrationHistoryRecordCount(postgresMigrationHistoryRecordLimit+1),
	)
	records := make([]migrationbackend.AppliedMigration, postgresMigrationHistoryRecordLimit)
	for index := range records {
		records[index] = migrationbackend.AppliedMigration{
			App:  fmt.Sprintf("app%04d", index),
			Name: "0001",
		}
	}
	_, err := postgresMigrationHistorySuccessor(records, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "overflow", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	})
	assertPostgresRevisionIntegrity(t, err)
}

func TestPostgresMigrationHistoryQueriesBoundTransport(t *testing.T) {
	t.Parallel()
	capture := &postgresMigrationHistoryQueryCapture{}
	_, _ = readPostgresMigrationRecorder(context.Background(), capture, "godj_test")
	if !strings.HasSuffix(capture.statement, `ORDER BY "app", "name" LIMIT $1`) ||
		len(capture.arguments) != 1 || capture.arguments[0] != postgresMigrationHistoryRecordLimit+1 {
		t.Fatalf("PostgreSQL recorder query = %q args=%v", capture.statement, capture.arguments)
	}

	capture = &postgresMigrationHistoryQueryCapture{}
	_, _ = readPostgresMigrationRevisionToken(context.Background(), capture, "godj_test")
	if !strings.Contains(capture.statement, `"substring"("epoch", 1, 17)`) ||
		!strings.Contains(capture.statement, `"substring"("history_fingerprint", 1, 33)`) ||
		!strings.HasSuffix(capture.statement, `ORDER BY "singleton" LIMIT 2`) || len(capture.arguments) != 0 {
		t.Fatalf("PostgreSQL revision-token query = %q args=%v", capture.statement, capture.arguments)
	}
}

type postgresMigrationHistoryQueryCapture struct {
	postgresMigrationRecordingExecutor
	statement string
	arguments []any
}

func (capture *postgresMigrationHistoryQueryCapture) QueryContext(
	_ context.Context,
	statement string,
	arguments ...any,
) (*sql.Rows, error) {
	capture.statement = statement
	capture.arguments = append([]any(nil), arguments...)
	return nil, errors.New("captured PostgreSQL migration history query")
}

func TestPostgresMigrationControlShapeDeclarations(t *testing.T) {
	t.Parallel()
	if got := postgresMigrationRecorderColumns(); !reflect.DeepEqual(got, []postgresMigrationControlColumn{
		{attributeNumber: 1, name: "app", typeName: "character varying(255)", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 2, name: "name", typeName: "character varying(255)", notNull: true, local: true, defaultCollation: true},
	}) {
		t.Fatalf("recorder columns = %+v", got)
	}
	if got := postgresMigrationRevisionColumns(); !reflect.DeepEqual(got, []postgresMigrationControlColumn{
		{attributeNumber: 1, name: "singleton", typeName: "smallint", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 2, name: "format_version", typeName: "integer", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 3, name: "epoch", typeName: "bytea", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 4, name: "revision", typeName: "bigint", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 5, name: "history_fingerprint", typeName: "bytea", notNull: true, local: true, defaultCollation: true},
	}) {
		t.Fatalf("revision columns = %+v", got)
	}
	if got := postgresMigrationRecorderIndex(); got != (postgresMigrationControlIndex{
		name:         postgresMigrationRecorderPrimaryKey,
		accessMethod: "btree",
		primary:      true,
		unique:       true,
		valid:        true,
		ready:        true,
		live:         true,
		keyCount:     2,
		totalCount:   2,
		key:          "1 2",
	}) {
		t.Fatalf("recorder index = %+v", got)
	}
	if got := postgresMigrationRevisionIndex(); got != (postgresMigrationControlIndex{
		name:         postgresMigrationRevisionPrimaryKey,
		accessMethod: "btree",
		primary:      true,
		unique:       true,
		valid:        true,
		ready:        true,
		live:         true,
		keyCount:     1,
		totalCount:   1,
		key:          "1",
	}) {
		t.Fatalf("revision index = %+v", got)
	}
}

func TestPostgresMigrationControlProfileRejectsPhysicalDrift(t *testing.T) {
	t.Parallel()
	exact := expectedPostgresMigrationControlTableProfile()
	if err := validatePostgresMigrationControlTableProfile(postgresMigrationRecorderTable, exact); err != nil {
		t.Fatalf("exact profile: %v", err)
	}
	mutations := []struct {
		name   string
		mutate func(*postgresMigrationControlTableProfile)
	}{
		{name: "view", mutate: func(profile *postgresMigrationControlTableProfile) { profile.kind = "v" }},
		{name: "unlogged", mutate: func(profile *postgresMigrationControlTableProfile) { profile.persistence = "u" }},
		{name: "non_heap", mutate: func(profile *postgresMigrationControlTableProfile) { profile.accessMethod = "other" }},
		{name: "partition", mutate: func(profile *postgresMigrationControlTableProfile) { profile.isPartition = true }},
		{name: "row_security", mutate: func(profile *postgresMigrationControlTableProfile) { profile.rowSecurity = true }},
		{name: "force_security", mutate: func(profile *postgresMigrationControlTableProfile) { profile.forceSecurity = true }},
		{name: "subclass", mutate: func(profile *postgresMigrationControlTableProfile) { profile.hasSubclass = true }},
		{name: "replica_identity", mutate: func(profile *postgresMigrationControlTableProfile) { profile.replicaIdentity = "f" }},
		{name: "reloptions", mutate: func(profile *postgresMigrationControlTableProfile) { profile.options = 1 }},
		{name: "parent", mutate: func(profile *postgresMigrationControlTableProfile) { profile.parentCount = 1 }},
		{name: "child", mutate: func(profile *postgresMigrationControlTableProfile) { profile.childCount = 1 }},
		{name: "trigger", mutate: func(profile *postgresMigrationControlTableProfile) { profile.triggers = 1 }},
		{name: "policy", mutate: func(profile *postgresMigrationControlTableProfile) { profile.policies = 1 }},
		{name: "rule", mutate: func(profile *postgresMigrationControlTableProfile) { profile.rules = 1 }},
	}
	for _, mutation := range mutations {
		mutation := mutation
		t.Run(mutation.name, func(t *testing.T) {
			t.Parallel()
			changed := exact
			mutation.mutate(&changed)
			assertPostgresRevisionIntegrity(
				t,
				validatePostgresMigrationControlTableProfile(postgresMigrationRecorderTable, changed),
			)
		})
	}
}

func TestPostgresMigrationControlShapeRejectsCatalogDrift(t *testing.T) {
	t.Parallel()
	columns := postgresMigrationRecorderColumns()
	constraint := postgresMigrationControlConstraint{
		name: postgresMigrationRecorderPrimaryKey, kind: "p", validated: true, key: "1,2",
	}
	index := postgresMigrationRecorderIndex()
	if err := validatePostgresMigrationControlShape(
		postgresMigrationRecorderTable,
		columns,
		columns,
		[]postgresMigrationControlConstraint{constraint},
		constraint,
		[]postgresMigrationControlIndex{index},
		index,
	); err != nil {
		t.Fatalf("exact shape: %v", err)
	}

	dropped := append([]postgresMigrationControlColumn(nil), columns...)
	dropped[1].dropped = true
	assertPostgresRevisionIntegrity(t, validatePostgresMigrationControlShape(
		postgresMigrationRecorderTable,
		dropped,
		columns,
		[]postgresMigrationControlConstraint{constraint},
		constraint,
		[]postgresMigrationControlIndex{index},
		index,
	))
	customCollation := append([]postgresMigrationControlColumn(nil), columns...)
	customCollation[0].defaultCollation = false
	assertPostgresRevisionIntegrity(t, validatePostgresMigrationControlShape(
		postgresMigrationRecorderTable,
		customCollation,
		columns,
		[]postgresMigrationControlConstraint{constraint},
		constraint,
		[]postgresMigrationControlIndex{index},
		index,
	))
	assertPostgresRevisionIntegrity(t, validatePostgresMigrationControlShape(
		postgresMigrationRecorderTable,
		append(append([]postgresMigrationControlColumn(nil), columns...), postgresMigrationControlColumn{}),
		columns,
		[]postgresMigrationControlConstraint{constraint},
		constraint,
		[]postgresMigrationControlIndex{index},
		index,
	))
	assertPostgresRevisionIntegrity(t, validatePostgresMigrationControlShape(
		postgresMigrationRecorderTable,
		columns,
		columns,
		[]postgresMigrationControlConstraint{constraint, constraint},
		constraint,
		[]postgresMigrationControlIndex{index},
		index,
	))
	assertPostgresRevisionIntegrity(t, validatePostgresMigrationControlShape(
		postgresMigrationRecorderTable,
		columns,
		columns,
		[]postgresMigrationControlConstraint{constraint},
		constraint,
		[]postgresMigrationControlIndex{index, index},
		index,
	))
	invalidIndex := index
	invalidIndex.hasPredicate = true
	assertPostgresRevisionIntegrity(t, validatePostgresMigrationControlShape(
		postgresMigrationRecorderTable,
		columns,
		columns,
		[]postgresMigrationControlConstraint{constraint},
		constraint,
		[]postgresMigrationControlIndex{invalidIndex},
		index,
	))
}

func assertPostgresRevisionIntegrity(t *testing.T, err error) {
	t.Helper()
	var fenceError *migrationbackend.RevisionFenceError
	if !errors.As(err, &fenceError) || fenceError == nil || fenceError.Kind != migrationbackend.RevisionFenceFailureIntegrity {
		t.Fatalf("error = %v, want revision integrity", err)
	}
}
