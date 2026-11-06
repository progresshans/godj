package model

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

// SetSaveRow owns one mutable model selected for persistence. Model returns its
// stable pointer so deferred saves and generated keys survive a later failure.
// A row, or the plan containing it, is not safe for concurrent mutation. Obtain
// another SavePlan for an independent copy of the immutable preparation.
type SetSaveRow[M any] struct {
	row   PreparedSetRow[M]
	value M
}

func (row *SetSaveRow[M]) Index() int                    { return row.row.Index() }
func (row *SetSaveRow[M]) Existing() bool                { return row.row.Existing() }
func (row *SetSaveRow[M]) Changed() []string             { return row.row.Changed() }
func (row *SetSaveRow[M]) Prepared() PreparedInstance[M] { return row.row.Prepared() }
func (row *SetSaveRow[M]) Model() *M                     { return &row.value }

// Save uses the ordinary model-form scalar/collection lifecycle. Custom set
// writers can call it after admission or use an application-specific writer.
func (row *SetSaveRow[M]) Save(ctx context.Context, backend db.Session, savers ...CollectionSaver[M]) error {
	if row == nil {
		return &Error{Path: "set.row", Code: "nil"}
	}
	return row.row.prepared.Save(ctx, backend, &row.value, savers...)
}

// SetSaveOptions selects the complete write policy for a plan. By default each
// changed/new row uses PreparedInstance.Save and Collections. Save overrides
// that entire row write (including collections); combining it with Collections
// is an error. A custom writer must retain every selected write intent and use
// the supplied scope. Delete is required whenever an existing row is deleted;
// choose the project's complete incoming relation policy, never a guessed
// scalar delete. These callbacks also own final admission and audit if needed.
type SetSaveOptions[M any] struct {
	Collections []CollectionSaver[M]
	Save        func(context.Context, db.Session, *SetSaveRow[M]) error
	Delete      func(context.Context, db.Session, DeletedSetRow[M]) error
}

// SetWrite describes an attempted operation, including the failing one. A nil
// Err is only a statement about that operation in its supplied scope, never a
// transaction commit receipt. Earlier writes can survive outside a transaction.
type SetWrite struct {
	index int
	kind  string
	err   error
}

func (write SetWrite) Index() int   { return write.index }
func (write SetWrite) Kind() string { return write.kind } // create, update, delete
func (write SetWrite) Err() error   { return write.err }

type setSaveOperation[M any] struct {
	index   int
	row     *SetSaveRow[M]
	deleted DeletedSetRow[M]
}

// SetSavePlan holds changed existing and changed new models plus existing
// deletion intents. It does not run validation, I/O or implicit transactions.
// Model pointers keep assigned keys after partial failure or outer rollback;
// neither keys nor operation results establish commit. Repeated Save calls are
// explicit new attempts and are not an automatic or idempotent retry policy.
type SetSavePlan[M any] struct {
	manager    orm.Manager[M]
	rows       []*SetSaveRow[M]
	deleted    []DeletedSetRow[M]
	operations []setSaveOperation[M]
}

func (plan *SetSavePlan[M]) Rows() []*SetSaveRow[M]      { return slices.Clone(plan.rows) }
func (plan *SetSavePlan[M]) Deleted() []DeletedSetRow[M] { return slices.Clone(plan.deleted) }

// SavePlan is the deferred (commit=False) selection: unchanged editable rows,
// unchanged extras, read-only rows and new rows marked DELETE do not become
// writes. ORDER does not reorder persistence. Existing changes/deletions follow
// submitted row order, followed by new rows. Pending model uploads must first
// pass explicit SaveFiles; no file publication is hidden in a database write.
func (set PreparedSet[M]) SavePlan() (*SetSavePlan[M], error) {
	if _, err := set.manager.Metadata(); err != nil {
		return nil, err
	}
	plan := &SetSavePlan[M]{manager: set.manager, deleted: slices.Clone(set.deleted)}
	for _, row := range set.rows {
		if len(row.changed) == 0 {
			continue
		}
		value, err := row.Model()
		if err != nil {
			return nil, err
		}
		owned := &SetSaveRow[M]{row: row, value: value}
		plan.rows = append(plan.rows, owned)
		plan.operations = append(plan.operations, setSaveOperation[M]{index: row.index, row: owned})
	}
	for _, row := range set.deleted {
		if _, err := row.Model(); err != nil {
			return nil, err
		}
		plan.operations = append(plan.operations, setSaveOperation[M]{index: row.index, deleted: row})
	}
	sort.Slice(plan.operations, func(i, j int) bool { return plan.operations[i].index < plan.operations[j].index })
	return plan, nil
}

func (plan *SetSavePlan[M]) validate(ctx context.Context, backend db.Session) error {
	if plan == nil {
		return &Error{Path: "set.plan", Code: "nil"}
	}
	if _, err := plan.manager.Metadata(); err != nil {
		return err
	}
	return validateSaveScope(ctx, backend)
}

// Save executes the selected plan, stopping at the first error. All default
// collection bindings and the deletion callback are checked before any write.
// Application callbacks receive the exact context/session, and returned errors
// are preserved without wrapping so callers retain rejection/outcome semantics.
// Use an authorized outer transaction for atomicity and propagate its terminal
// outcome; this method does not perform permission, revision or DB uniqueness
// checks, compensate, retry or commit on the caller's behalf.
func (plan *SetSavePlan[M]) Save(ctx context.Context, backend db.Session, options SetSaveOptions[M]) ([]SetWrite, error) {
	if err := plan.validate(ctx, backend); err != nil {
		return nil, err
	}
	if len(plan.deleted) != 0 && options.Delete == nil {
		return nil, &Error{Path: "set.delete", Code: "missing_deleter"}
	}
	if options.Save != nil && len(options.Collections) != 0 {
		return nil, &Error{Path: "set.save", Code: "conflicting_savers"}
	}
	collections := slices.Clone(options.Collections)
	if options.Save == nil {
		for _, row := range plan.rows {
			if _, err := row.row.prepared.collectionSaves(ctx, backend, collections); err != nil {
				return nil, err
			}
		}
	}
	writes := make([]SetWrite, 0, len(plan.operations))
	for _, operation := range plan.operations {
		if err := validateSaveScope(ctx, backend); err != nil {
			return writes, err
		}
		kind := "delete"
		var err error
		if row := operation.row; row != nil {
			kind = "create"
			if row.Existing() {
				kind = "update"
			}
			if options.Save != nil {
				err = options.Save(ctx, backend, row)
			} else {
				err = row.Save(ctx, backend, collections...)
			}
		} else {
			err = options.Delete(ctx, backend, operation.deleted)
		}
		if err == nil {
			err = validateSaveScope(ctx, backend)
		}
		writes = append(writes, SetWrite{index: operation.index, kind: kind, err: err})
		if err != nil {
			return writes, err
		}
	}
	return writes, nil
}

// SaveCollections is the deferred phase after callers save plan.Rows models.
// It never saves scalar models or executes deletions. All selected models must
// have explicit primary keys before the first collection write; excluded and
// unchanged rows are untouched. Selection and collection order match Save.
func (plan *SetSavePlan[M]) SaveCollections(ctx context.Context, backend db.Session, savers ...CollectionSaver[M]) error {
	if err := plan.validate(ctx, backend); err != nil {
		return err
	}
	operations := make([][]collectionSave[M], len(plan.rows))
	for i, row := range plan.rows {
		var err error
		operations[i], err = row.row.prepared.collectionSaves(ctx, backend, savers)
		if err != nil {
			return err
		}
		if len(operations[i]) != 0 {
			if _, err := row.row.prepared.collectionOwner(row.value); err != nil {
				return err
			}
		}
	}
	for i, row := range plan.rows {
		if err := row.row.prepared.saveCollections(ctx, backend, row.value, operations[i]); err != nil {
			return err
		}
	}
	return nil
}

func (*SetSaveRow[M]) Format(s fmt.State, _ rune)    { fmt.Fprint(s, "model.SetSaveRow{redacted}") }
func (*SetSavePlan[M]) Format(s fmt.State, _ rune)   { fmt.Fprint(s, "model.SetSavePlan{redacted}") }
func (SetSaveOptions[M]) Format(s fmt.State, _ rune) { fmt.Fprint(s, "model.SetSaveOptions{redacted}") }
func (SetWrite) Format(s fmt.State, _ rune)          { fmt.Fprint(s, "model.SetWrite{redacted}") }
