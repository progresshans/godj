package input

import (
	"context"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
)

// NewResource additionally checks JSON exposure against the resource's
// explicit writable selection at startup. A surface may expose a subset or
// include read-only/computed output, but cannot widen the CRUD input policy.
// It retains the same Body, parser, schemas and complete model-source check.
func NewResource[M any](resource crud.Resource[M], spec serializers.Spec, config api.ParserConfig) (Body[orm.ModelInput[M]], error) {
	allowed := make(map[string]bool)
	for _, name := range resource.Fields() {
		allowed[name] = true
	}
	for _, field := range spec.Fields() {
		if !field.ReadOnly() && !allowed[field.Name()] {
			return Body[orm.ModelInput[M]]{}, configError("fields."+field.Name(), "JSON input exceeds the resource writable selection")
		}
	}
	return NewModel(resource.Manager(), spec, config)
}

// NewModel prepares the existing Body path for a typed generated model without
// DTO setters. Spec must originate from FromModel with this manager's complete
// metadata. Matching names or a generic NewSpec cannot establish that source.
// Full/partial validation, parser limits and schemas remain owned by Body and
// Spec, including read-only computed fields. Collections never become scalars.
func NewModel[M any](manager orm.Manager[M], spec serializers.Spec, config api.ParserConfig) (Body[orm.ModelInput[M]], error) {
	metadata, err := manager.Metadata()
	if err != nil {
		return Body[orm.ModelInput[M]]{}, err
	}
	source, bound := spec.Model()
	if !bound || !metadata.Equal(source) {
		return Body[orm.ModelInput[M]]{}, configError("model", "serializer requires the same complete model source")
	}
	if _, err := orm.InitialModelValues(metadata); err != nil {
		return Body[orm.ModelInput[M]]{}, err
	}
	var zero M
	if _, err := manager.ApplyValues(zero, nil); err != nil {
		return Body[orm.ModelInput[M]]{}, err
	}
	collections := make(map[string]bool, len(metadata.ManyToMany))
	for _, field := range metadata.ManyToMany {
		collections[field.Name] = true
	}
	return newBody(spec, config, func(ctx context.Context, values serializers.Values) (orm.ModelInput[M], error) {
		scalars := make(map[string]query.Value)
		membership := make(map[string][]int64)
		for _, entry := range values.All() {
			if err := ctx.Err(); err != nil {
				return orm.ModelInput[M]{}, err
			}
			if collections[entry.Name()] {
				keys, ok := entry.Value().AsIntegers()
				if !ok {
					return orm.ModelInput[M]{}, configError("fields."+entry.Name(), "validated collection has an incompatible representation")
				}
				membership[entry.Name()] = keys
				continue
			}
			scalar, ok := modelScalar(entry.Value())
			if !ok {
				return orm.ModelInput[M]{}, configError("fields."+entry.Name(), "validated scalar has an incompatible representation")
			}
			scalars[entry.Name()] = scalar
		}
		result, err := manager.Input(scalars)
		if err != nil {
			return orm.ModelInput[M]{}, err
		}
		return result.WithCollections(membership)
	})
}

func modelScalar(value serializers.Value) (query.Value, bool) {
	switch value.Kind() {
	case serializers.ValueNull:
		return query.Null(), true
	case serializers.ValueString:
		v, ok := value.AsString()
		return query.String(v), ok
	case serializers.ValueBoolean:
		v, ok := value.AsBoolean()
		return query.Boolean(v), ok
	case serializers.ValueInteger:
		v, ok := value.AsInteger()
		return query.Integer(v), ok
	case serializers.ValueFloat:
		v, ok := value.AsFloat()
		return query.Float(v), ok
	case serializers.ValueDateTime:
		v, ok := value.AsDateTime()
		return query.DateTime(v), ok
	case serializers.ValueDate:
		v, ok := value.AsDate()
		return query.Date(v), ok
	case serializers.ValueTime:
		v, ok := value.AsTime()
		return query.Time(v), ok
	case serializers.ValueDuration:
		v, ok := value.AsDuration()
		return query.Duration(v), ok
	case serializers.ValueDecimal:
		v, ok := value.AsDecimal()
		return query.Decimal(v), ok
	case serializers.ValueUUID:
		v, ok := value.AsUUID()
		return query.UUID(v), ok
	case serializers.ValueBinary:
		v, ok := value.AsBinary()
		return query.Binary(v), ok
	case serializers.ValueJSON:
		v, ok := value.AsJSON()
		return query.JSON(v), ok
	default:
		return query.Value{}, false
	}
}
