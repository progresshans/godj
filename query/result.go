package query

import (
	"slices"
	"strconv"
	"strings"
)

// ResultKind identifies the row shape returned by a query plan. The source
// field universe remains independent so predicates and ordering do not become
// invalid merely because a projection selects fewer fields.
type ResultKind string

const (
	ResultModel      ResultKind = "model"
	ResultPrefetch   ResultKind = "prefetch"
	ResultProjection ResultKind = "projection"
	ResultAggregate  ResultKind = "aggregate"
)

// ResultExpressionKind identifies one ordered projection or aggregate cell.
type ResultExpressionKind string

const (
	ResultField    ResultExpressionKind = "field"
	ResultJSONPath ResultExpressionKind = "json_path"
	ResultCountAll ResultExpressionKind = "count_all"
	ResultMax      ResultExpressionKind = "max"
	ResultMin      ResultExpressionKind = "min"
)

// ResultExpression is an immutable, backend-independent selected value.
// COUNT(*) has no field; other expressions retain the exact source field
// identity. A related or JSON path result can be NULL even when that source is
// required.
type ResultExpression struct {
	kind     ResultExpressionKind
	field    FieldRef
	path     JSONPath
	relation *RelationPath
}

func FieldResult(field FieldRef) ResultExpression {
	return ResultExpression{kind: ResultField, field: field}
}

func JSONPathResult(field FieldRef, path JSONPath) (ResultExpression, error) {
	if !validResultField(field) || field.Kind() != FieldJSON || !path.Valid() {
		return ResultExpression{}, invalidPlanError("JSON path result requires a JSON source field and a valid path")
	}
	return ResultExpression{kind: ResultJSONPath, field: field, path: path}, nil
}

// RelatedJSONPathResult selects a nullable JSON value at a finite forward
// target. Source-field identity remains the target's exact metadata; optional
// ancestors never rewrite that metadata to manufacture a root field.
func RelatedJSONPathResult(relation RelationPath, path JSONPath) (ResultExpression, error) {
	expression, err := RelatedFieldResult(relation)
	if err != nil {
		return ResultExpression{}, err
	}
	if expression.field.Kind() != FieldJSON || !path.Valid() {
		return ResultExpression{}, invalidPlanError("JSON path result requires a JSON source field and a valid path")
	}
	expression.kind, expression.path = ResultJSONPath, path
	return expression, nil
}

// RelatedFieldResult preserves the selected target's field and route without
// adding it to the root model metadata or changing declared nullability.
func RelatedFieldResult(relation RelationPath) (ResultExpression, error) {
	if err := relation.Validate(); err != nil {
		return ResultExpression{}, err
	}
	if relation.hops[0].direction != RelationForward {
		return ResultExpression{}, &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "scalar results require a forward relation"}
	}
	if err := relation.validateForwardSelection(); err != nil {
		return ResultExpression{}, err
	}
	return ResultExpression{kind: ResultField, field: relation.Terminal(), relation: &relation}, nil
}

func (e ResultExpression) RelationPath() (RelationPath, bool) {
	if e.relation == nil {
		return RelationPath{}, false
	}
	return *e.relation, true
}

func (e ResultExpression) JSONPath() (JSONPath, bool) {
	return e.path, e.kind == ResultJSONPath && e.path.Valid()
}

func CountAllResult() ResultExpression {
	return ResultExpression{kind: ResultCountAll}
}

func MaxResult(field FieldRef) ResultExpression {
	return ResultExpression{kind: ResultMax, field: field}
}

func MinResult(field FieldRef) ResultExpression {
	return ResultExpression{kind: ResultMin, field: field}
}

func (e ResultExpression) Kind() ResultExpressionKind { return e.kind }

func (e ResultExpression) Field() (FieldRef, bool) {
	switch e.kind {
	case ResultField, ResultJSONPath, ResultMax, ResultMin:
		return e.field, true
	default:
		return FieldRef{}, false
	}
}

func (e ResultExpression) Equal(other ResultExpression) bool {
	if e.kind != other.kind || !e.field.Equal(other.field) || !e.path.Equal(other.path) || (e.relation == nil) != (other.relation == nil) {
		return false
	}
	return e.relation == nil || e.relation.Equal(*other.relation)
}

// ResultShape is sealed by constructors and shares immutable private storage.
// A model result derives its exact ordered cells from Plan.SourceFields.
type ResultShape struct {
	kind        ResultKind
	expressions []ResultExpression
}

// MaxProjectionExpressions bounds the selected cells independently of the
// model's source fields, since many JSON paths may use the same source column.
const MaxProjectionExpressions = 2048

func NewProjectionResult(expressions ...ResultExpression) (ResultShape, error) {
	if len(expressions) == 0 || len(expressions) > MaxProjectionExpressions {
		return ResultShape{}, invalidPlanError("projection requires between one and 2048 expressions")
	}
	shape := ResultShape{kind: ResultProjection, expressions: slices.Clone(expressions)}
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

// IsCountAll reports the single COUNT(*) aggregate supported over relation filters.
func (s ResultShape) IsCountAll() bool {
	return s.kind == ResultAggregate && len(s.expressions) == 1 && s.expressions[0].kind == ResultCountAll
}

// HasRelations reports selection routes independently of predicate routes.
func (s ResultShape) HasRelations() bool {
	for _, expression := range s.expressions {
		if expression.relation != nil {
			return true
		}
	}
	return false
}

func (s ResultShape) Expressions() []ResultExpression {
	return append([]ResultExpression(nil), s.expressions...)
}

func (s ResultShape) Equal(other ResultShape) bool {
	return s.kind == other.kind && slices.EqualFunc(s.expressions, other.expressions, ResultExpression.Equal)
}

func (s ResultShape) validate() error {
	switch s.kind {
	case ResultPrefetch:
		return s.validatePrefetch()
	case ResultModel:
		if len(s.expressions) != 0 {
			return invalidPlanError("model result cannot contain explicit expressions")
		}
		return nil
	case ResultProjection:
		if len(s.expressions) == 0 || len(s.expressions) > MaxProjectionExpressions {
			return invalidPlanError("projection requires between one and 2048 expressions")
		}
		type selectionKey struct {
			field    FieldRef
			path     string
			relation string
		}
		seen := make(map[selectionKey]struct{}, len(s.expressions))
		for _, expression := range s.expressions {
			field, ok := expression.Field()
			if !ok || !validResultField(field) {
				return invalidPlanError("projection result contains an invalid field expression")
			}
			key := selectionKey{field: field}
			if expression.relation != nil {
				if (expression.kind != ResultField && expression.kind != ResultJSONPath) || !expression.relation.Terminal().Equal(field) {
					return invalidPlanError("related result requires its exact terminal field")
				}
				if err := expression.relation.validateForwardSelection(); err != nil {
					return err
				}
				key.relation = projectionRouteKey(expression.relation.hops)
			}
			switch expression.kind {
			case ResultField:
				if expression.path.Valid() {
					return invalidPlanError("field result contains an unexpected JSON path")
				}
			case ResultJSONPath:
				if field.Kind() != FieldJSON || !expression.path.Valid() {
					return invalidPlanError("JSON path result is invalid")
				}
				// Typed, length-prefixed tokens distinguish numeric keys, indices,
				// embedded delimiters and an empty key without pointer identity.
				var identity strings.Builder
				for _, segment := range expression.path.data.segments {
					if segment.kind == 1 {
						identity.WriteString("k" + strconv.Itoa(len(segment.key)) + ":" + segment.key)
					} else {
						identity.WriteString("i" + strconv.Itoa(segment.index) + ";")
					}
				}
				key.path = identity.String()
			default:
				return invalidPlanError("projection result contains an unsupported expression")
			}
			if _, duplicate := seen[key]; duplicate {
				return invalidPlanError("projection result contains a duplicate expression")
			}
			seen[key] = struct{}{}
		}
		return nil
	case ResultAggregate:
		if len(s.expressions) == 0 || len(s.expressions) > 4 {
			return invalidPlanError("aggregate result requires between one and four expressions")
		}
		for _, expression := range s.expressions {
			if expression.path.Valid() || expression.relation != nil {
				return invalidPlanError("aggregate result cannot contain a JSON path or related value")
			}
			switch expression.Kind() {
			case ResultCountAll:
				if _, hasField := expression.Field(); hasField {
					return invalidPlanError("COUNT(*) result cannot contain a field")
				}
			case ResultMax, ResultMin:
				field, ok := expression.Field()
				if !ok || !validResultField(field) || (field.Kind() != FieldInteger && field.Kind() != FieldFloat && field.Kind() != FieldDecimal && field.Kind() != FieldUUID && field.Kind() != FieldString && field.Kind() != FieldDateTime && field.Kind() != FieldDate && (field.Kind() != FieldTime && field.Kind() != FieldDuration)) {
					return invalidPlanError("MIN/MAX result requires an ordered scalar field")
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
	if !field.ValidType() {
		return false
	}
	if field.Name() == "" || field.Column() == "" {
		return false
	}
	switch field.Kind() {
	case FieldInteger, FieldFloat, FieldDecimal, FieldUUID, FieldJSON, FieldString, FieldBoolean, FieldDateTime, FieldDate, FieldTime, FieldDuration:
		return true
	default:
		return false
	}
}
