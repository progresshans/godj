package orm

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type ManyToManySetOptions[L any] struct {
	// Clear removes all selected links before adding the desired set. The
	// default retains the identity and payload of every existing desired link.
	Clear           bool
	ThroughDefaults ManyToManyInput[L]
}

func (c *ManyCollection[T, L]) Add(ctx context.Context, targets []T, defaults ...ManyToManyInput[L]) error {
	keys, err := c.keys(targets)
	return c.change(ctx, manyAdd, keys, defaults, false, err)
}

func (c *ManyCollection[T, L]) AddKeys(ctx context.Context, keys []int64, defaults ...ManyToManyInput[L]) error {
	return c.change(ctx, manyAdd, keys, defaults, false, nil)
}

func (c *ManyCollection[T, L]) Remove(ctx context.Context, targets ...T) error {
	keys, err := c.keys(targets)
	return c.change(ctx, manyRemove, keys, nil, false, err)
}

func (c *ManyCollection[T, L]) RemoveKeys(ctx context.Context, keys ...int64) error {
	return c.change(ctx, manyRemove, keys, nil, false, nil)
}

func (c *ManyCollection[T, L]) Clear(ctx context.Context) error {
	return c.change(ctx, manyClear, nil, nil, false, nil)
}

func (c *ManyCollection[T, L]) Set(ctx context.Context, targets []T, options ...ManyToManySetOptions[L]) error {
	keys, err := c.keys(targets)
	return c.set(ctx, keys, options, err)
}

func (c *ManyCollection[T, L]) SetKeys(ctx context.Context, keys []int64, options ...ManyToManySetOptions[L]) error {
	return c.set(ctx, keys, options, nil)
}

func (c *ManyCollection[T, L]) set(ctx context.Context, keys []int64, options []ManyToManySetOptions[L], err error) error {
	var defaults []ManyToManyInput[L]
	clear := false
	if len(options) > 1 {
		err = errors.Join(err, relationInvalidPlan("collection set accepts at most one options value"))
	}
	if len(options) == 1 {
		clear = options[0].Clear
		if options[0].ThroughDefaults != nil {
			defaults = append(defaults, options[0].ThroughDefaults)
		}
	}
	return c.change(ctx, manySet, keys, defaults, clear, err)
}

func (c *ManyCollection[T, L]) keys(targets []T) ([]int64, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	keys := make([]int64, len(targets))
	for index, target := range targets {
		key, err := manyObjectKey(c.target, target)
		if err != nil {
			return nil, err
		}
		keys[index] = key
	}
	return keys, nil
}

type manyChange uint8

const (
	manyAdd manyChange = iota
	manyRemove
	manyClear
	manySet
)

type manyPair struct{ source, target int64 }
type manyRow struct {
	key                          int64
	pair                         manyPair
	sourcePresent, targetPresent bool
}

func (c *ManyCollection[T, L]) change(ctx context.Context, operation manyChange, keys []int64, defaults []ManyToManyInput[L], clear bool, inputErr error) error {
	if err := c.validate(); err != nil {
		return err
	}
	c.invalidate()
	defer c.invalidate()
	if interfaceIsNil(ctx) {
		return relationInvalidPlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if inputErr != nil {
		return inputErr
	}
	if len(defaults) > 1 || len(defaults) == 1 && interfaceIsNil(defaults[0]) {
		return relationInvalidPlan("collection accepts at most one non-nil through input")
	}
	input := c.through.ManyToManyCreateInput()
	if len(defaults) == 1 {
		input = defaults[0]
	}
	if interfaceIsNil(input) {
		return relationInvalidPlan("collection default through input is nil")
	}
	keys = slices.Clone(keys)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	if (operation == manyAdd || operation == manyRemove) && len(keys) == 0 {
		return nil
	}
	backend, ok := c.backend.(db.RelationAtomic)
	if !ok || interfaceIsNil(backend) {
		return relationBackendInvalidPlan("collection mutation requires a relation atomic backend")
	}
	return runRelationAtomic(ctx, backend.AtomicRelation, func(session db.RelationSession) error {
		return c.executeChange(ctx, session, operation, keys, input, clear)
	})
}

func (c *ManyCollection[T, L]) executeChange(ctx context.Context, session db.RelationSession, operation manyChange, keys []int64, input ManyToManyInput[L], clear bool) error {
	if interfaceIsNil(session) {
		return relationBackendInvalidPlan("collection backend supplied a nil session")
	}
	var conflict db.ConflictInserter
	if len(c.state.unique) != 0 && (operation == manyAdd || operation == manySet) {
		var ok bool
		conflict, ok = session.(db.ConflictInserter)
		if !ok || interfaceIsNil(conflict) {
			return relationBackendInvalidPlan("collection unique pair requires native conflict insertion in its transaction session")
		}
	}
	rows, err := c.state.rows(ctx, session, c.ownerKey)
	if err != nil {
		return err
	}
	existing := make(map[manyPair]bool, len(rows))
	var roots []relationDeleteRow
	for _, row := range rows {
		partner, present := row.partner(c.ownerKey)
		selected := present && slices.Contains(keys, partner)
		remove := operation == manyClear || operation == manySet && (clear || present && !selected) || operation == manyRemove && selected
		if remove {
			roots = append(roots, relationDeleteRow{model: c.state.through, key: row.key})
		} else if row.sourcePresent && row.targetPresent {
			existing[row.pair] = true
		}
	}
	var desired []manyPair
	if operation == manyAdd || operation == manySet {
		seen := make(map[manyPair]bool)
		for _, key := range keys {
			pairs := []manyPair{{source: c.ownerKey, target: key}}
			if c.state.symmetrical {
				pairs = append(pairs, manyPair{source: key, target: c.ownerKey})
			}
			for _, pair := range pairs {
				if !seen[pair] {
					seen[pair] = true
					desired = append(desired, pair)
				}
			}
		}
	}
	manager := NewManager[L](c.through)
	var inserts []query.InsertPlan
	for _, pair := range desired {
		if existing[pair] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		mutation := input.BuildManyToManyCreate(c.state.source.Clone(), c.state.target.Clone(), pair.source, pair.target)
		if err := validateMutation(mutation, MutationCreate, manager.prepared, c.through, nil); err != nil {
			return err
		}
		if _, present := c.through.PrimaryKey(mutation.value); present {
			return relationInvalidPlan("collection create input supplied a primary key")
		}
		for _, endpoint := range []struct {
			name string
			key  int64
		}{{c.state.source.Name, pair.source}, {c.state.target.Name, pair.target}} {
			for _, assignment := range mutation.assignments {
				if assignment.Field().Name() == endpoint.name && !assignment.Value().Equal(query.Integer(endpoint.key)) {
					return relationInvalidPlan("collection create input changed a bound endpoint")
				}
			}
		}
		inserts = append(inserts, query.NewInsertPlanReturningKey(c.state.model.DBTable, mutation.Assignments(), fieldReference(c.state.key)))
	}
	// All inputs and all reached PROTECT edges are checked before the first
	// deletion. The existing collector owns declared incoming policy on links.
	if len(roots) != 0 {
		if _, err := executeRelationDeleteRoots(ctx, session, c.state.graph, roots); err != nil {
			return err
		}
	}
	for _, insert := range inserts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if conflict != nil {
			_, err = conflict.InsertOnConflict(ctx, query.NewConflictInsertPlan(insert.Table(), insert.Assignments(), c.state.unique))
		} else {
			_, err = session.Insert(ctx, insert)
		}
		if err != nil {
			return err
		}
	}
	// Native 0 affected rows are not proof of membership (a trigger may skip
	// insertion). Check the requested postcondition inside the same transaction.
	final, err := c.state.rows(ctx, session, c.ownerKey)
	if err != nil {
		return err
	}
	presentPairs := make(map[manyPair]bool, len(final))
	for _, row := range final {
		if row.sourcePresent && row.targetPresent {
			presentPairs[row.pair] = true
		}
		partner, present := row.partner(c.ownerKey)
		if operation == manyClear || present && (operation == manyRemove && slices.Contains(keys, partner) || operation == manySet && !slices.Contains(keys, partner)) {
			return relationBackendInvalidPlan("collection deletion postcondition was not preserved")
		}
	}
	for _, pair := range desired {
		if !presentPairs[pair] {
			return relationBackendInvalidPlan("collection insertion did not establish the requested membership")
		}
	}
	return ctx.Err()
}

func (row manyRow) partner(owner int64) (int64, bool) {
	if row.sourcePresent && row.pair.source == owner {
		return row.pair.target, row.targetPresent
	}
	return row.pair.source, row.sourcePresent
}

func (state *manyToManyState) rows(ctx context.Context, session db.RelationSession, owner int64) ([]manyRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expression, err := query.NewExpression(query.NewCondition(fieldReference(state.source), query.LookupExact, query.Integer(owner)))
	if err != nil {
		return nil, err
	}
	if state.symmetrical {
		other, err := query.NewExpression(query.NewCondition(fieldReference(state.target), query.LookupExact, query.Integer(owner)))
		if err != nil {
			return nil, err
		}
		expression, err = query.OrExpressions(expression, other)
		if err != nil {
			return nil, err
		}
	}
	plan, err := query.NewPlan(state.model.DBTable, []query.FieldRef{fieldReference(state.key), fieldReference(state.source), fieldReference(state.target)}).WithWhere(expression)
	if err != nil {
		return nil, err
	}
	rows, err := session.Query(ctx, plan)
	if err != nil {
		if !interfaceIsNil(rows) {
			err = errors.Join(err, rows.Close())
		}
		return nil, joinContextErr(err, ctx)
	}
	if interfaceIsNil(rows) {
		return nil, joinContextErr(relationBackendInvalidPlan("collection query returned nil rows without an error"), ctx)
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	var result []manyRow
	seen := make(map[int64]bool)
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			break
		}
		var keyValue, sourceValue, targetValue any
		if scanErr := rows.Scan(&keyValue, &sourceValue, &targetValue); scanErr != nil {
			err = fmt.Errorf("scan collection link: %w", scanErr)
			break
		}
		key, keyOK := keyValue.(int64)
		source, sourceOK := sourceValue.(int64)
		target, targetOK := targetValue.(int64)
		if !keyOK || seen[key] || !sourceOK && !(sourceValue == nil && state.source.Nullable) || !targetOK && !(targetValue == nil && state.target.Nullable) || !(sourceOK && source == owner || state.symmetrical && targetOK && target == owner) {
			err = relationBackendInvalidPlan("collection row has invalid keys or is outside the bound owner")
			break
		}
		seen[key] = true
		result = append(result, manyRow{key: key, pair: manyPair{source: source, target: target}, sourcePresent: sourceOK, targetPresent: targetOK})
	}
	if err := lifecycle.finish(ctx, err); err != nil {
		return nil, err
	}
	slices.SortFunc(result, func(a, b manyRow) int {
		if a.key < b.key {
			return -1
		}
		if a.key > b.key {
			return 1
		}
		return 0
	})
	return result, nil
}
