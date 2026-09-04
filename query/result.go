package query

import "slices"

// ResultKind identifies the row shape returned by a query plan. The source
// field universe remains independent so predicates and ordering do not become
// invalid merely because a projection selects fewer fields.
type ResultKind string

const (
	ResultModel      ResultKind = "model"
	ResultProjection ResultKind = "projection"
	ResultAggregate  ResultKind = "aggregate"
)

// ResultExpressionKind identifies one ordered projection or aggregate cell.
type ResultExpressionKind string

const (
	ResultField    ResultExpressionKind = "field"
	ResultCountAll ResultExpressionKind = "count_all"
	ResultMax      ResultExpressionKind = "max"
)

// ResultExpression is an immutable, backend-independent selected value.
// COUNT(*) has no field; field projection and MAX require one exact source
// field reference.
type ResultExpression struct {
	kind  ResultExpressionKind
	field FieldRef
}

func FieldResult(field FieldRef) ResultExpression {
	return ResultExpression{kind: ResultField, field: field}
}

func CountAllResult() ResultExpression {
	return ResultExpression{kind: ResultCountAll}
}

func MaxResult(field FieldRef) ResultExpression {
	return ResultExpression{kind: ResultMax, field: field}
}

func (e ResultExpression) Kind() ResultExpressionKind { return e.kind }

func (e ResultExpression) Field() (FieldRef, bool) {
	switch e.kind {
	case ResultField, ResultMax:
		return e.field, true
	default:
		return FieldRef{}, false
	}
}

func (e ResultExpression) Equal(other ResultExpression) bool {
	return e == other
}

// ResultShape is sealed by constructors and detached when read from a Plan.
// A model result derives its exact ordered cells from Plan.SourceFields.
type ResultShape struct {
	kind        ResultKind
	expressions []ResultExpression
}

func NewProjectionResult(fields ...FieldRef) (ResultShape, error) {
	if len(fields) == 0 {
		return ResultShape{}, invalidPlanError("projection result is empty")
	}
	expressions := make([]ResultExpression, len(fields))
	for index, field := range fields {
		expressions[index] = FieldResult(field)
	}
	shape := ResultShape{kind: ResultProjection, expressions: expressions}
	if err := shape.validate(); err != nil {
		return ResultShape{}, err
	}
	return shape, nil
}

func NewAggregateResult(expressions ...ResultExpression) (ResultShape, error) {
	shape := ResultShape{
		kind:        ResultAggregate,
		expressions: append([]ResultExpression(nil), expressions...),
	}
	if err := shape.validate(); err != nil {
		return ResultShape{}, err
	}
	return shape, nil
}

func modelResult() ResultShape { return ResultShape{kind: ResultModel} }

func (s ResultShape) Kind() ResultKind { return s.kind }

func (s ResultShape) Expressions() []ResultExpression {
	return append([]ResultExpression(nil), s.expressions...)
}

func (s ResultShape) Equal(other ResultShape) bool {
	return s.kind == other.kind && slices.Equal(s.expressions, other.expressions)
}

func (s ResultShape) clone() ResultShape {
	return ResultShape{
		kind:        s.kind,
		expressions: append([]ResultExpression(nil), s.expressions...),
	}
}

func (s ResultShape) validate() error {
	switch s.kind {
	case ResultModel:
		if len(s.expressions) != 0 {
			return invalidPlanError("model result cannot contain explicit expressions")
		}
		return nil
	case ResultProjection:
		if len(s.expressions) == 0 {
			return invalidPlanError("projection result is empty")
		}
		seen := make(map[FieldRef]struct{}, len(s.expressions))
		for _, expression := range s.expressions {
			field, ok := expression.Field()
			if expression.Kind() != ResultField || !ok || !validResultField(field) {
				return invalidPlanError("projection result contains an invalid field expression")
			}
			if _, duplicate := seen[field]; duplicate {
				return invalidPlanError("projection result contains a duplicate field")
			}
			seen[field] = struct{}{}
		}
		return nil
	case ResultAggregate:
		if len(s.expressions) == 0 || len(s.expressions) > 4 {
			return invalidPlanError("aggregate result requires between one and four expressions")
		}
		for _, expression := range s.expressions {
			switch expression.Kind() {
			case ResultCountAll:
				if _, hasField := expression.Field(); hasField {
					return invalidPlanError("COUNT(*) result cannot contain a field")
				}
			case ResultMax:
				field, ok := expression.Field()
				if !ok || !validResultField(field) || (field.Kind() != FieldInteger && field.Kind() != FieldString) {
					return invalidPlanError("MAX result requires an integer or string field")
				}
			default:
				return invalidPlanError("aggregate result contains an unsupported expression")
			}
		}
		return nil
	default:
		return invalidPlanError("query result kind is invalid")
	}
}

func validResultField(field FieldRef) bool {
	if field.Name() == "" || field.Column() == "" {
		return false
	}
	switch field.Kind() {
	case FieldInteger, FieldString, FieldBoolean:
		return true
	default:
		return false
	}
}
