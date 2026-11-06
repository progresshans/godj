package uniquetest

import (
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Record is a typed adapter for the independent scalar profiles. Its values
// are immutable Query scalars; no backend SQL or uniqueness policy lives here.
type Record struct {
	ID      int64
	Present bool
	Value   query.Value
	Bucket  query.Value
}

type Descriptor struct{ Model ir.Model }

func (d Descriptor) Metadata() ir.Model { return d.Model.Clone() }
func (Descriptor) Scan(db.Row) (Record, error) {
	return Record{}, errors.New("uniqueness validation must not decode model rows")
}
func (Descriptor) CloneModel(value Record) Record      { return value }
func (Descriptor) CloneWriteModel(value Record) Record { return value }
func (Descriptor) PrimaryKey(value Record) (query.Value, bool) {
	return query.Integer(value.ID), value.Present
}
func (Descriptor) SetPrimaryKey(value *Record, id int64) { value.ID, value.Present = id, true }
func (Descriptor) ClearPrimaryKey(value *Record)         { value.ID, value.Present = 0, false }
func (Descriptor) WriteFieldValue(value Record, field ir.Field) (query.Value, bool) {
	if field.PrimaryKey {
		return query.Integer(value.ID), true
	}
	if field.Name == "bucket" {
		return value.Bucket, true
	}
	return value.Value, field.Name == "value"
}

type Input struct {
	Model ir.Model
	Value query.Value
}

func (input Input) BuildCreate() orm.Mutation[Record] {
	return orm.NewCreateMutation(Record{Value: input.Value}, input.Model.DBTable, []query.Assignment{orm.NewAssignment(input.Model.Fields[1], input.Value)})
}

func (input Input) BuildPatch(current Record) orm.Mutation[Record] {
	current.Value = input.Value
	return orm.NewPatchMutation(current, input.Model.DBTable, []query.Assignment{orm.NewAssignment(input.Model.Fields[1], input.Value)})
}

// CompositeInput uses the same owned scalar adapter with explicit patch
// presence, so partial updates exercise omitted tuple members from current.
type CompositeInput struct {
	Model                   ir.Model
	Bucket, Value           query.Value
	PatchBucket, PatchValue bool
}

func (input CompositeInput) BuildCreate() orm.Mutation[Record] {
	return orm.NewCreateMutation(Record{Bucket: input.Bucket, Value: input.Value}, input.Model.DBTable, []query.Assignment{
		orm.NewAssignment(input.Model.Fields[1], input.Bucket), orm.NewAssignment(input.Model.Fields[2], input.Value),
	})
}

func (input CompositeInput) BuildPatch(current Record) orm.Mutation[Record] {
	var assignments []query.Assignment
	if input.PatchBucket {
		current.Bucket = input.Bucket
		assignments = append(assignments, orm.NewAssignment(input.Model.Fields[1], input.Bucket))
	}
	if input.PatchValue {
		current.Value = input.Value
		assignments = append(assignments, orm.NewAssignment(input.Model.Fields[2], input.Value))
	}
	return orm.NewPatchMutation(current, input.Model.DBTable, assignments)
}
