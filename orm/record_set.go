package orm

import (
	"context"
	"maps"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Record holds the concrete fields of one model in their logical names.
// Foreign keys carry the stored key. Each read owns its map.
type Record map[string]query.Value

type recordDescriptor struct {
	model  ir.Model
	decode func() resultDecoder[[]query.Value]
}

func (d *recordDescriptor) Metadata() ir.Model           { return d.model.Clone() }
func (d *recordDescriptor) CloneModel(row Record) Record { return maps.Clone(row) }
func (d *recordDescriptor) Scan(row db.Row) (Record, error) {
	decoder := d.decode()
	if err := row.Scan(decoder.destinations...); err != nil {
		return nil, err
	}
	if err := decoder.validateScanned(); err != nil {
		return nil, err
	}
	values := decoder.decode()
	record := make(Record, len(values))
	for index, field := range d.model.Fields {
		record[field.Name] = values[index]
	}
	return record, nil
}

// RecordSet binds a normalized model snapshot to one borrowed session.
// Callers can address historical fields without a generated Go type or a
// live model registry. No API accepts table names, column names or raw SQL.
type RecordSet struct {
	manager Manager[Record]
	session db.Session
	key     query.FieldRef
}

func BindRecords(model ir.Model, session db.Session) (RecordSet, error) {
	if interfaceIsNil(session) {
		return RecordSet{}, invalidWritePlan("record set needs a session")
	}
	if _, ok := session.(db.SessionValidator); !ok {
		return RecordSet{}, invalidWritePlan("record set needs a borrowed session")
	}
	key, ok := autoPrimaryKey(model)
	if !ok {
		return RecordSet{}, invalidWritePlan("record set needs an AutoField primary key")
	}
	model = model.Clone()
	fields := modelFieldReferences(model)
	expressions := make([]query.ResultExpression, len(fields))
	for i, field := range fields {
		expressions[i] = query.FieldResult(field)
	}
	decoder, err := dynamicGroupDecoder(expressions)
	if err != nil {
		return RecordSet{}, err
	}
	descriptor := &recordDescriptor{model: model, decode: decoder}
	// Build the same field-name and mutation indexes used by typed managers.
	manager := Manager[Record]{descriptor: descriptor, prepared: prepareModel(model, true)}
	return RecordSet{manager: manager, session: session, key: fieldReference(key)}, nil
}

func (set RecordSet) filtered(lookup query.Lookup, key int64) QuerySet[Record] {
	expression, err := query.NewExpression(query.NewCondition(set.key, lookup, query.Integer(key)))
	source := set.manager.Using(set.session)
	return source.Filter(Predicate[Record]{expression: expression, err: err})
}

func (set RecordSet) Get(ctx context.Context, key int64) (Record, error) {
	return set.filtered(query.LookupExact, key).Get(ctx)
}

// Page returns at most size rows in ascending primary-key order. A nil
// cursor includes zero and negative keys. The transport is closed before
// the caller can issue another read or write on the migration connection.
func (set RecordSet) Page(ctx context.Context, after *int64, size int) ([]Record, error) {
	if size < 1 {
		return nil, invalidWritePlan("record page size must be positive")
	}
	source := set.manager.Using(set.session)
	if after != nil {
		source = set.filtered(query.LookupGreaterThan, *after)
	}
	source.plan = source.plan.WithOrderings(query.NewOrdering(set.key, query.Ascending))
	source, err := source.Limit(size)
	if err != nil {
		return nil, err
	}
	return source.All(ctx)
}

// Update changes the supplied fields of an existing key and returns the
// native affected-row count. A missing key never becomes an insert.
func (set RecordSet) Update(ctx context.Context, key int64, values Record) (int64, error) {
	if set.manager.prepared == nil {
		return 0, invalidWritePlan("record set is not initialized")
	}
	if err := validateQuerySession(ctx, set.session); err != nil {
		return 0, err
	}
	if len(values) == 0 || len(values) > len(set.manager.prepared.metadata.Fields) {
		return 0, invalidWritePlan("record update needs fields of its model")
	}
	owned, err := set.manager.inputValues(values)
	if err != nil {
		return 0, err
	}
	assignments := make([]query.Assignment, 0, len(owned))
	for _, field := range set.manager.prepared.metadata.Fields {
		if value, present := owned[field.Name]; present {
			assignments = append(assignments, NewAssignment(field, value))
		}
	}
	count, err := set.session.Update(ctx, query.NewUpdatePlan(set.manager.prepared.metadata.DBTable, assignments, set.key, query.Integer(key)))
	if err != nil {
		return 0, err
	}
	if count < 0 || count > 1 {
		return 0, invalidWritePlan("primary key update returned an invalid affected-row count")
	}
	if err := validateQuerySession(ctx, set.session); err != nil {
		return 0, err
	}
	return count, nil
}
