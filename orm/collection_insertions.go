package orm

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type collectionInsert struct {
	plan   query.InsertPlan
	values map[string]query.Value
}

// CollectionInsertedRow is the complete typed scalar row read back by the
// actual key returned by its insertion. A conflict/no-op produces no row here.
// Key can be zero or negative. Values includes the actual primary-key field.
type CollectionInsertedRow struct {
	Model  ir.ModelIdentity
	Key    int64
	Values map[string]query.Value
}

func (CollectionInsertedRow) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.CollectionInsertedRow{redacted}"))
}

// AppliedCollectionChanges is bound to the complete project source. It reports
// only successfully verified insertions from one Apply, never deletion effects
// or a guessed key for existing membership. It grants no application authority
// and remains provisional until the enclosing transaction owner confirms commit.
// A later companion write must preserve these rows or verify its own changes.
type AppliedCollectionChanges struct{ state *appliedCollectionChanges }

type appliedCollectionChanges struct {
	source   *projectBindingSnapshot
	inserted []CollectionInsertedRow
	session  db.RelationSession
	readers  []func(context.Context, db.RelationSession, int64) (map[string]query.Value, error)
}

func (AppliedCollectionChanges) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.AppliedCollectionChanges{redacted}"))
}

func (applied AppliedCollectionChanges) ValidateSource(source ProjectBinding) error {
	if applied.state == nil || !sameProjectSource(applied.state.source, source.snapshot) {
		return relationInvalidPlan("applied collection changes do not match the complete project source")
	}
	return nil
}

func (applied AppliedCollectionChanges) Insertions() []CollectionInsertedRow {
	if applied.state == nil {
		return nil
	}
	result := slices.Clone(applied.state.inserted)
	for index := range result {
		result[index].Values = maps.Clone(result[index].Values)
	}
	return result
}

// Verify rereads the complete inserted rows in the original borrowed session.
// An enclosing owner can call it after companion writes and policies, before
// commit. A zero result or expired session is rejected; this cannot reopen a
// transaction or establish whether an uncertain outer commit succeeded.
func (applied AppliedCollectionChanges) Verify(ctx context.Context) error {
	if applied.state == nil || len(applied.state.inserted) != len(applied.state.readers) {
		return relationInvalidPlan("applied collection changes are absent or invalid")
	}
	state := applied.state
	if err := validateQuerySession(ctx, state.session); err != nil {
		return err
	}
	for index, expected := range state.inserted {
		actual, err := state.readers[index](ctx, state.session, expected.Key)
		if err != nil {
			return err
		}
		if !maps.EqualFunc(expected.Values, actual, query.Value.Equal) {
			return relationBackendInvalidPlan("inserted collection row differs from its typed candidate")
		}
	}
	return validateQuerySession(ctx, state.session)
}
