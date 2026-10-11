package orm

import (
	"strings"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// DynamicAggregateInput binds names against the source's metadata snapshot.
// Kind is one of count_all, count, min, max, sum, avg. It is never rendered as SQL.
type DynamicAggregateInput struct {
	Kind       query.ResultExpressionKind
	Field      string
	Distinct   bool
	Filter     []LookupInput
	Expression DynamicExpression
}

// GroupRow owns both ordered containers; its scalar Values are immutable.
type GroupRow struct{ Keys, Aggregates []query.Value }

func GroupValues[M any](source QuerySet[M], keys []string, aggregates []DynamicAggregateInput) (GroupedQuery[M, GroupRow], error) {
	return groupValues(source, nil, keys, aggregates)
}

// GroupValuesIn resolves relation names within an explicit immutable project
// binding. The source descriptor and metadata must belong to that bound model;
// eager materialization state is neither required nor used as name authority.
func GroupValuesIn[M any](source QuerySet[M], binding BoundModel[M], keys []string, aggregates []DynamicAggregateInput) (GroupedQuery[M, GroupRow], error) {
	if source.configurationErr != nil {
		return GroupedQuery[M, GroupRow]{}, source.configurationErr
	}
	if err := validateMaterializationSource(source, binding); err != nil {
		return GroupedQuery[M, GroupRow]{}, err
	}
	return groupValues(source, &binding, keys, aggregates)
}

func groupValues[M any](source QuerySet[M], binding *BoundModel[M], keys []string, aggregates []DynamicAggregateInput) (GroupedQuery[M, GroupRow], error) {
	var zero GroupedQuery[M, GroupRow]
	if source.configurationErr != nil {
		return zero, source.configurationErr
	}
	if source.prepared == nil {
		return zero, invalidResultBuilder("dynamic grouping requires model metadata")
	}
	if len(keys) == 0 || len(keys) > query.MaxGroupKeys || len(aggregates) == 0 || len(aggregates) > query.MaxAggregateExpressions {
		return zero, invalidResultBuilder("dynamic group exceeds key or aggregate bounds")
	}
	selected := make([]query.ResultExpression, len(keys))
	for index, name := range keys {
		value, err := dynamicGroupField(source, binding, name)
		if err != nil {
			return zero, err
		}
		selected[index] = value
	}
	values := make([]query.ResultExpression, len(aggregates))
	for index, input := range aggregates {
		var value query.ResultExpression
		if input.Kind == query.ResultCountAll {
			if input.Field != "" || input.Expression.node != nil || input.Expression.err != nil {
				return zero, invalidResultBuilder("COUNT(*) cannot name a field")
			}
			value = query.CountAllResult()
		} else {
			var operand query.ResultExpression
			var err error
			if input.Expression.node != nil || input.Expression.err != nil {
				if input.Field != "" {
					return zero, invalidResultBuilder("dynamic aggregate cannot combine a field name with an expression")
				}
				value, bindErr := BindExpression(source, input.Expression)
				operand, err = value.expression, bindErr
			} else {
				operand, err = dynamicGroupField(source, binding, input.Field)
			}
			if err != nil {
				return zero, err
			}
			switch input.Kind {
			case query.ResultCount:
				value, err = query.CountResult(operand)
			case query.ResultSum:
				value, err = query.SumResult(operand)
			case query.ResultAvg:
				value, err = query.AvgResult(operand)
			case query.ResultMin, query.ResultMax:
				if _, related := operand.RelationPath(); related {
					return zero, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "dynamic related MIN/MAX are not implemented"}
				}
				value, err = query.AggregateResult(input.Kind, operand)
			default:
				return zero, invalidResultBuilder("dynamic aggregate kind is unsupported")
			}
			if err != nil {
				return zero, err
			}
		}
		if input.Distinct {
			var err error
			value, err = value.WithDistinct()
			if err != nil {
				return zero, err
			}
		}
		if len(input.Filter) > query.MaxGroupExpressionNodes {
			return zero, invalidResultBuilder("dynamic aggregate has too many filters")
		}
		for _, input := range input.Filter {
			predicate, err := dynamicGroupFilter(source, binding, input)
			if err != nil {
				return zero, err
			}
			value, err = value.WithFilter(predicate.expression)
			if err != nil {
				return zero, err
			}
		}
		values[index] = value
	}
	if _, err := query.NewGroupedResult(selected, values); err != nil {
		return zero, err
	}
	keyDecoder, err := dynamicGroupDecoder(selected)
	if err != nil {
		return zero, err
	}
	valueDecoder, err := dynamicGroupDecoder(values)
	if err != nil {
		return zero, err
	}
	return GroupBy(source, Projection[M, []query.Value]{expressions: selected, newDecoder: keyDecoder},
		Aggregate[M, []query.Value]{expressions: values, newDecoder: valueDecoder},
		func(keys, values []query.Value) GroupRow { return GroupRow{Keys: keys, Aggregates: values} })
}

func dynamicGroupField[M any](source QuerySet[M], binding *BoundModel[M], name string) (query.ResultExpression, error) {
	if strings.Contains(name, "__") {
		if binding == nil {
			return query.ResultExpression{}, invalidResultBuilder("dynamic relation group keys require a project-bound query")
		}
		predicate, err := parseRelationInput(*binding, nil, LookupInput{Key: name + "__isnull", Value: true})
		if err != nil {
			return query.ResultExpression{}, err
		}
		condition, leaf := predicate.expression.Condition()
		if !leaf {
			return query.ResultExpression{}, invalidResultBuilder("dynamic group key did not resolve to one field")
		}
		path, related := condition.RelationPath()
		if !related {
			return query.ResultExpression{}, invalidResultBuilder("dynamic group key has no bound route")
		}
		return query.RelatedFieldResult(path)
	}
	field, found := findField(source.prepared.metadata.Fields, name)
	if !found {
		return query.ResultExpression{}, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField, Field: name, Detail: "group field is not source metadata"}
	}
	return query.FieldResult(fieldReference(field)), nil
}

type groupDescriptor[M any] struct {
	ModelDescriptor[M]
	metadata ir.Model
}

func (d groupDescriptor[M]) Metadata() ir.Model { return d.metadata.Clone() }
func dynamicGroupFilter[M any](source QuerySet[M], binding *BoundModel[M], input LookupInput) (Predicate[M], error) {
	field, _ := splitLookup(input.Key)
	if metadata, found := findField(source.prepared.metadata.Fields, field); !found || metadata.Relation != nil {
		if binding == nil {
			return Predicate[M]{}, invalidResultBuilder("dynamic aggregate relation filters require a project-bound query")
		}
		return parseRelationInput(*binding, nil, input)
	}
	predicates, err := ParseDynamic[M](groupDescriptor[M]{source.descriptor, source.prepared.metadata}, nil, []LookupInput{input})
	if err != nil {
		return Predicate[M]{}, err
	}
	return predicates[0], nil
}

// Dynamic group references are positions in this selected result, with keys
// first. Positions resolve to the same immutable expressions as typed handles;
// they cannot introduce unselected columns or SQL aliases.
type GroupLookupInput struct {
	Column int
	Lookup query.Lookup
	Value  any
}
type GroupOrderInput struct {
	Column    int
	Direction query.Direction
	Nulls     query.NullOrder
}

func (q GroupedQuery[M, R]) HavingDynamic(inputs ...GroupLookupInput) GroupedQuery[M, R] {
	if q.err != nil {
		return q.Fresh()
	}
	if len(inputs) > query.MaxGroupExpressionNodes {
		q.err = invalidResultBuilder("too many dynamic HAVING inputs")
		return q.Fresh()
	}
	selected := q.plan.ResultShape().Expressions()
	values := make([]GroupPredicate[M], len(inputs))
	for index, input := range inputs {
		if input.Column < 0 || input.Column >= len(selected) {
			q.err = invalidResultBuilder("dynamic HAVING column is out of range")
			return q.Fresh()
		}
		values[index] = (GroupValue[M, any]{expression: selected[input.Column]}).comparison(input.Lookup, input.Value)
	}
	return q.Having(values...)
}
func (q GroupedQuery[M, R]) OrderByDynamic(inputs ...GroupOrderInput) GroupedQuery[M, R] {
	if q.err != nil {
		return q.Fresh()
	}
	if len(inputs) > query.MaxGroupKeys+query.MaxAggregateExpressions {
		q.err = invalidResultBuilder("too many dynamic group orderings")
		return q.Fresh()
	}
	selected := q.plan.ResultShape().Expressions()
	values := make([]GroupOrder[M], len(inputs))
	for index, input := range inputs {
		if input.Column < 0 || input.Column >= len(selected) {
			q.err = invalidResultBuilder("dynamic ordering column is out of range")
			return q.Fresh()
		}
		if input.Nulls == "" {
			input.Nulls = query.NullsNative
		}
		value, err := query.NewGroupOrdering(selected[input.Column], input.Direction, input.Nulls)
		values[index] = GroupOrdering[M]{ordering: value, err: err}
	}
	return q.OrderBy(values...)
}

func dynamicGroupDecoder(expressions []query.ResultExpression) (func() resultDecoder[[]query.Value], error) {
	factories := make([]func() scalarCell[query.Value], len(expressions))
	for index, expression := range expressions {
		field, _ := expression.Field()
		kind, nullable := expression.ResultValueKind(), expression.ResultNullable()
		switch kind {
		case query.FieldInteger:
			factories[index] = dynamicGroupCell(nullableIntegerResultCell, nullable)
		case query.FieldString:
			factories[index] = dynamicGroupCell(nullableStringResultCell, nullable)
		case query.FieldBoolean:
			factories[index] = dynamicGroupCell(nullableBooleanResultCell, nullable)
		case query.FieldFloat:
			factories[index] = dynamicGroupCell(nullableFloatResultCell, nullable)
		case query.FieldDecimal:
			if _, computed := expression.Scalar(); computed || expression.Kind() == query.ResultSum || expression.Kind() == query.ResultAvg {
				factories[index] = dynamicGroupCell(nullableAggregateDecimalCell, nullable)
			} else {
				factories[index] = dynamicGroupCell(func() scalarCell[*decimal.Decimal] { return nullableDecimalResultCell(field) }, nullable)
			}
		case query.FieldUUID:
			factories[index] = dynamicGroupCell(nullableUUIDResultCell, nullable)
		case query.FieldBinary:
			factories[index] = dynamicGroupCell(nullableBinaryResultCell, nullable)
		case query.FieldJSON:
			factories[index] = dynamicGroupCell(nullableJSONResultCell, nullable)
		case query.FieldDateTime:
			factories[index] = dynamicGroupCell(nullableDateTimeResultCell, nullable)
		case query.FieldDate:
			factories[index] = dynamicGroupCell(nullableDateResultCell, nullable)
		case query.FieldTime:
			factories[index] = dynamicGroupCell(nullableTimeResultCell, nullable)
		case query.FieldDuration:
			if expression.Kind() == query.ResultAvg {
				factories[index] = dynamicGroupCell(nullableAggregateDurationCell, nullable)
			} else {
				factories[index] = dynamicGroupCell(nullableDurationResultCell, nullable)
			}
		default:
			return nil, invalidResultBuilder("dynamic group result has no scalar decoder")
		}
	}
	return func() resultDecoder[[]query.Value] {
		cells := make([]scalarCell[query.Value], len(factories))
		targets := make([]any, len(factories))
		for index, factory := range factories {
			cells[index] = factory()
			targets[index] = cells[index].destination
		}
		return resultDecoder[[]query.Value]{destinations: targets, decode: func() []query.Value {
			values := make([]query.Value, len(cells))
			for index, cell := range cells {
				values[index] = cell.value()
			}
			return values
		}}
	}, nil
}

type groupValueScanner struct{ scan func(any) error }

func (s *groupValueScanner) Scan(raw any) error { return s.scan(raw) }
func dynamicGroupCell[V any](factory func() scalarCell[V], nullable bool) func() scalarCell[query.Value] {
	return func() scalarCell[query.Value] {
		cell := factory()
		var value query.Value
		scanner := &groupValueScanner{scan: func(raw any) error {
			value = query.Value{}
			if err := scanGroupValue(cell.destination, raw); err != nil {
				return err
			}
			var err error
			value, err = groupLiteral(cell.value())
			if err != nil {
				return err
			}
			if value.IsNull() && !nullable {
				value = query.Value{}
				return invalidResultBuilder("required dynamic group cell is NULL")
			}
			return nil
		}}
		return scalarCell[query.Value]{destination: scanner, value: func() query.Value { return value }}
	}
}
