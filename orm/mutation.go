package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type MutationKind string

const (
	MutationCreate MutationKind = "create"
	MutationPatch  MutationKind = "patch"
)

// Mutation binds a generated input to its model type while keeping the
// database-independent assignment plan immutable. Only generated builders and
// generic Manager methods need to know how the result model is constructed.
type Mutation[M any] struct {
	kind        MutationKind
	table       string
	assignments []query.Assignment
	value       M
	err         error
}

func NewCreateMutation[M any](value M, table string, assignments []query.Assignment) Mutation[M] {
	return newMutation(MutationCreate, value, table, assignments)
}

func NewPatchMutation[M any](value M, table string, assignments []query.Assignment) Mutation[M] {
	if len(assignments) == 0 {
		return InvalidMutation[M](&query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeEmptyPatch,
			Detail:   "patch has no explicit field changes",
		})
	}
	return newMutation(MutationPatch, value, table, assignments)
}

func InvalidMutation[M any](err error) Mutation[M] {
	return Mutation[M]{err: err}
}

func (m Mutation[M]) Kind() MutationKind { return m.kind }
func (m Mutation[M]) Table() string      { return m.table }
func (m Mutation[M]) Assignments() []query.Assignment {
	return append([]query.Assignment(nil), m.assignments...)
}
func (m Mutation[M]) Err() error { return m.err }

func newMutation[M any](kind MutationKind, value M, table string, assignments []query.Assignment) Mutation[M] {
	if table == "" {
		return InvalidMutation[M](&query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeInvalidPlan,
			Detail:   "mutation table is empty",
		})
	}
	return Mutation[M]{
		kind:        kind,
		table:       table,
		assignments: append([]query.Assignment(nil), assignments...),
		value:       value,
	}
}

// NewAssignment converts normalized field metadata into the single Query AST
// representation shared by generated typed writes and backend compilation.
func NewAssignment(field ir.Field, value query.Value) query.Assignment {
	return query.NewAssignment(fieldReference(field), value)
}

type CreateInput[M any] interface {
	BuildCreate() Mutation[M]
}

type PatchInput[M any] interface {
	BuildPatch(M) Mutation[M]
}
