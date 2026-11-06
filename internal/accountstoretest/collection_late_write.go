package accountstoretest

import (
	"context"
	"errors"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// Mutate a genuinely inserted row when the enclosing owner returns to its
// scalar/unique/final-policy reads. The initial collection Apply has already
// verified the row. Pair membership remains unchanged, so a logical-set-only
// final check cannot detect this mutation. QueryUpdate owns the explicit PK
// assignment; ordinary row Update intentionally does not support it.
type lifecycleLateCollectionRow struct {
	mode, rootTable, table string
	key                    query.FieldRef
	id, reference          int64
	captured, changed      bool
}

func (state *lifecycleLateCollectionRow) inserted(plan query.InsertPlan, key int64) {
	if state.captured || !slices.Contains([]string{"late_link_key", "late_payload"}, state.mode) {
		return
	}
	if !slices.Contains([]string{"links_membership", "links_grant_link", "people_member_grants"}, plan.Table()) || state.mode == "late_payload" && plan.Table() != "links_membership" {
		return
	}
	state.key, state.captured = plan.ReturningKey()
	state.table, state.id = plan.Table(), key
}

func (state *lifecycleLateCollectionRow) beforeRead(ctx context.Context, scope db.RelationSession, read query.Plan) error {
	if !state.captured || state.changed || read.Table() != state.rootTable {
		return nil
	}
	field, value := state.key, query.Integer(-900000000001)
	fields := []query.FieldRef{state.key}
	if state.mode == "late_payload" {
		field = query.NewFieldRef("anchor", "anchor_id", query.FieldInteger, true)
		value = query.Integer(state.reference)
		fields = append(fields, field)
	}
	literal, err := query.LiteralExpression(value)
	if err != nil {
		return err
	}
	assignment, err := query.NewScalarAssignment(field, literal)
	if err != nil {
		return err
	}
	source, err := query.NewPlan(state.table, fields).WithConditions(query.NewCondition(state.key, query.LookupExact, query.Integer(state.id)))
	if err != nil {
		return err
	}
	plan, err := query.NewQueryUpdatePlan(source, state.key, []query.ScalarAssignment{assignment})
	if err != nil {
		return err
	}
	writer, ok := scope.(db.QueryUpdater)
	if !ok {
		return errors.New("late collection fixture requires the native query-update capability")
	}
	count, err := writer.QueryUpdate(ctx, plan)
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("late collection fixture did not rewrite exactly one actual row")
	}
	state.changed = true
	return nil
}
