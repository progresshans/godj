// Package queryplan owns the shared semantic checks used by SQL compilers.
// It does not own backend identifier quoting, physical schema rules or I/O.
package queryplan

import (
	"fmt"
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// RelationKey is the deterministic semantic edge identity used to prepare a
// join inventory. Full hop equality remains necessary before combining edges.
type RelationKey struct {
	SourceApp, SourceModel, Field, TargetApp, TargetModel string
	Direction                                             query.RelationDirection
}

func KeyForRelation(hop query.RelationHop) RelationKey {
	return RelationKey{
		SourceApp: hop.Source().AppLabel, SourceModel: hop.Source().ModelName, Field: hop.Field(),
		TargetApp: hop.Target().AppLabel, TargetModel: hop.Target().ModelName, Direction: hop.Direction(),
	}
}

func RelationProjection(plan query.Plan, projection query.RelationProjection, backendName string) (RelationKey, error) {
	hop := projection.Hop()
	if hop.Direction() != query.RelationForward || hop.Cardinality() != ir.RelationManyToOne || hop.ReverseName() != "" {
		return RelationKey{}, invalidPlan(backendName + " relation projection requires one direct forward many-to-one hop")
	}
	if hop.SourceTable() != plan.Table() {
		return RelationKey{}, invalidPlan(fmt.Sprintf(
			"relation projection source table %q does not match plan root table %q",
			hop.SourceTable(),
			plan.Table(),
		))
	}
	if !canonicalIdentity(hop.Source()) || !canonicalIdentity(hop.Target()) ||
		!CanonicalIdentifier(hop.SourceTable()) || !CanonicalIdentifier(hop.Field()) ||
		!CanonicalIdentifier(hop.SourceColumn()) || !CanonicalIdentifier(hop.TargetTable()) ||
		!CanonicalIdentifier(hop.TargetPrimaryKeyColumn()) {
		return RelationKey{}, invalidPlan("relation projection contains non-canonical metadata")
	}
	sourceKey := query.NewFieldRef(hop.Field(), hop.SourceColumn(), query.FieldInteger, hop.Nullable())
	if !ContainsField(plan.SourceFields(), sourceKey) {
		return RelationKey{}, invalidPlan(fmt.Sprintf(
			"relation projection source key %q is not selected model metadata",
			hop.Field(),
		))
	}
	targetColumns := projection.TargetColumns()
	if len(targetColumns) == 0 {
		return RelationKey{}, invalidPlan("relation projection target columns are empty")
	}
	primaryKeyCount := 0
	names := make(map[string]struct{}, len(targetColumns))
	columns := make(map[string]struct{}, len(targetColumns))
	for _, field := range targetColumns {
		if !CanonicalIdentifier(field.Name()) || !CanonicalIdentifier(field.Column()) ||
			(field.Kind() != query.FieldInteger && field.Kind() != query.FieldString && field.Kind() != query.FieldBoolean) {
			return RelationKey{}, invalidPlan("relation projection contains an unsupported target field")
		}
		if _, exists := names[field.Name()]; exists {
			return RelationKey{}, invalidPlan("relation projection contains a duplicate target field")
		}
		if _, exists := columns[field.Column()]; exists {
			return RelationKey{}, invalidPlan("relation projection contains a duplicate target column")
		}
		names[field.Name()] = struct{}{}
		columns[field.Column()] = struct{}{}
		if field.Column() == hop.TargetPrimaryKeyColumn() {
			if field.Kind() != query.FieldInteger || field.Nullable() {
				return RelationKey{}, invalidPlan("relation projection target primary key must be a non-null integer")
			}
			primaryKeyCount++
		}
	}
	if primaryKeyCount != 1 {
		return RelationKey{}, invalidPlan("relation projection must contain its target primary key exactly once")
	}
	return KeyForRelation(hop), nil
}

func SameSourceEdge(left, right query.RelationHop) bool {
	return left.Field() == right.Field() &&
		left.SourceColumn() == right.SourceColumn()
}

func NullableSourceKey(
	columns []query.FieldRef,
	condition query.Condition,
	hop query.RelationHop,
	backendName string,
) error {
	field := condition.Field()
	if hop.Direction() != query.RelationForward || hop.Cardinality() != ir.RelationManyToOne || !hop.Nullable() {
		return unsupportedRelatedCondition(condition, backendName+" source-key isnull requires a nullable forward many-to-one path")
	}
	if !canonicalIdentity(hop.Source()) || !canonicalIdentity(hop.Target()) ||
		!CanonicalIdentifier(hop.SourceTable()) || !CanonicalIdentifier(hop.Field()) ||
		!CanonicalIdentifier(hop.SourceColumn()) || !CanonicalIdentifier(hop.TargetTable()) ||
		!CanonicalIdentifier(hop.TargetPrimaryKeyColumn()) {
		return invalidPlan("nullable source-key relation path contains non-canonical metadata")
	}
	if field.Kind() != query.FieldInteger || !field.Nullable() ||
		field.Name() != hop.Field() || field.Column() != hop.SourceColumn() {
		return invalidPlan("nullable source-key relation terminal does not match the hop source key")
	}
	if !ContainsField(columns, field) {
		return invalidPlan(fmt.Sprintf("relation source key %q is not selected model metadata", field.Name()))
	}
	if condition.Lookup() != query.LookupIsNull {
		return unsupportedRelatedCondition(condition, backendName+" source-key relation paths support isnull only")
	}
	if _, ok := condition.Value().Boolean(); !ok {
		return invalidPlan(backendName + " source-key isnull requires a Boolean value")
	}
	return nil
}

func ReverseCondition(condition query.Condition, hop query.RelationHop, backendName string) error {
	if hop.Cardinality() != ir.RelationOneToMany {
		return unsupportedRelatedCondition(condition, backendName+" reverse related-field paths require one-to-many traversal")
	}
	if !canonicalIdentity(hop.Source()) || !canonicalIdentity(hop.Target()) ||
		!CanonicalIdentifier(hop.SourceTable()) || !CanonicalIdentifier(hop.Field()) ||
		!CanonicalIdentifier(hop.SourceColumn()) || !CanonicalIdentifier(hop.TargetTable()) ||
		!CanonicalIdentifier(hop.TargetPrimaryKeyColumn()) || !CanonicalIdentifier(hop.ReverseName()) {
		return invalidPlan("reverse relation path contains non-canonical metadata")
	}
	field := condition.Field()
	if !CanonicalIdentifier(field.Name()) || !CanonicalIdentifier(field.Column()) || field.Nullable() ||
		(field.Kind() != query.FieldInteger && field.Kind() != query.FieldString) {
		return invalidPlan("reverse relation terminal is non-canonical or unsupported")
	}
	return nil
}

func canonicalIdentity(identity ir.ModelIdentity) bool {
	return CanonicalIdentifier(identity.AppLabel) && CanonicalIdentifier(identity.ModelName)
}

func CanonicalIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character == '_' || character >= 'a' && character <= 'z' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func CompareRelationKey(left, right RelationKey) int {
	leftParts := [...]string{left.SourceApp, left.SourceModel, left.Field, left.TargetApp, left.TargetModel, string(left.Direction)}
	rightParts := [...]string{right.SourceApp, right.SourceModel, right.Field, right.TargetApp, right.TargetModel, string(right.Direction)}
	for index := range leftParts {
		if comparison := strings.Compare(leftParts[index], rightParts[index]); comparison != 0 {
			return comparison
		}
	}
	return 0
}

func ContainsField(columns []query.FieldRef, candidate query.FieldRef) bool {
	for _, column := range columns {
		if column.Equal(candidate) {
			return true
		}
	}
	return false
}

func ValueMatchesField(value query.ValueKind, field query.FieldKind) bool {
	return (value == query.ValueInteger && field == query.FieldInteger) ||
		(value == query.ValueString && field == query.FieldString) ||
		(value == query.ValueBoolean && field == query.FieldBoolean)
}

func OrderedValueMatchesField(value query.ValueKind, field query.FieldKind) bool {
	return (value == query.ValueInteger && field == query.FieldInteger) ||
		(value == query.ValueString && field == query.FieldString)
}

func ProjectionFields(result query.ResultShape, sourceFields []query.FieldRef) ([]query.FieldRef, error) {
	expressions := result.Expressions()
	if len(expressions) == 0 {
		return nil, invalidPlan("projection result is empty")
	}
	fields := make([]query.FieldRef, len(expressions))
	for index, expression := range expressions {
		field, ok := expression.Field()
		if expression.Kind() != query.ResultField || !ok || !ContainsField(sourceFields, field) {
			return nil, invalidPlan("projection result contains a field outside the plan source metadata")
		}
		fields[index] = field
	}
	return fields, nil
}

func OmittedOrderings(orderings []query.Ordering, sourceFields []query.FieldRef, quoteIdentifier func(string) (string, error)) error {
	for _, ordering := range orderings {
		if !ContainsField(sourceFields, ordering.Field()) {
			return invalidPlan(fmt.Sprintf("ordering field %q is not selected model metadata", ordering.Field().Name()))
		}
		if _, err := quoteIdentifier(ordering.Field().Column()); err != nil {
			return err
		}
		switch ordering.Direction() {
		case query.Ascending, query.Descending:
		default:
			return invalidPlan("unknown ordering direction")
		}
	}
	return nil
}

func invalidPlan(detail string) error {
	return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: detail}
}

func unsupportedRelatedCondition(condition query.Condition, detail string) error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnsupported,
		Field:    condition.Field().Name(),
		Lookup:   string(condition.Lookup()),
		Detail:   detail,
	}
}

// AppendAggregates validates the closed scalar aggregate grammar before each
// field reaches backend quoting. COUNT and MAX syntax is shared; quoting and
// schema/alias qualification are supplied by the actual compiler.
func AppendAggregates(sql *strings.Builder, expressions []query.ResultExpression, sourceFields []query.FieldRef, quoteField func(query.FieldRef) (string, error)) error {
	for index, expression := range expressions {
		if index > 0 {
			sql.WriteString(", ")
		}
		switch expression.Kind() {
		case query.ResultCountAll:
			if _, ok := expression.Field(); ok {
				return invalidPlan("COUNT(*) result contains a field")
			}
			sql.WriteString("COUNT(*)")
		case query.ResultMax:
			field, ok := expression.Field()
			if !ok || !ContainsField(sourceFields, field) ||
				(field.Kind() != query.FieldInteger && field.Kind() != query.FieldString) {
				return invalidPlan("MAX result requires an integer or string source field")
			}
			quoted, err := quoteField(field)
			if err != nil {
				return err
			}
			sql.WriteString("MAX(")
			sql.WriteString(quoted)
			sql.WriteByte(')')
		default:
			return invalidPlan("aggregate result contains an unsupported expression")
		}
	}
	return nil
}
