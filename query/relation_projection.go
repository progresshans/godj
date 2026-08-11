package query

import (
	"slices"

	"github.com/progresshans/godj/schema/ir"
)

// RelationProjection is the immutable description of exactly one direct
// forward many-to-one eager projection. It deliberately carries no caller-
// selected join kind: required versus nullable follows the source key.
type RelationProjection struct {
	hop           RelationHop
	targetColumns []FieldRef
}

// NewForwardRelationProjection constructs one canonical forward projection.
// The target columns retain descriptor order and are cloned before
// publication. targetKey must occur exactly once in that ordered projection.
func NewForwardRelationProjection(
	source ir.ModelIdentity,
	sourceTable string,
	sourceKey FieldRef,
	target ir.ModelIdentity,
	targetTable string,
	targetKey FieldRef,
	orderedTargetColumns []FieldRef,
) (RelationProjection, error) {
	projection := RelationProjection{
		hop: RelationHop{
			source:                 source,
			sourceTable:            sourceTable,
			field:                  sourceKey.Name(),
			sourceColumn:           sourceKey.Column(),
			target:                 target,
			targetTable:            targetTable,
			targetPrimaryKeyColumn: targetKey.Column(),
			direction:              RelationForward,
			cardinality:            ir.RelationManyToOne,
			nullable:               sourceKey.Nullable(),
		},
		targetColumns: append([]FieldRef(nil), orderedTargetColumns...),
	}
	if !validProjectionIdentity(source) || !validProjectionIdentity(target) ||
		!canonicalIdentifier(sourceTable) || !canonicalIdentifier(targetTable) ||
		!validProjectionField(sourceKey) || sourceKey.Kind() != FieldInteger ||
		!validProjectionField(targetKey) || targetKey.Kind() != FieldInteger || targetKey.Nullable() {
		return RelationProjection{}, invalidPlanError("forward relation projection contains non-canonical source or target metadata")
	}
	if len(orderedTargetColumns) == 0 {
		return RelationProjection{}, invalidPlanError("forward relation projection has no target columns")
	}

	primaryKeyCount := 0
	names := make(map[string]struct{}, len(orderedTargetColumns))
	columns := make(map[string]struct{}, len(orderedTargetColumns))
	for _, field := range orderedTargetColumns {
		if !validProjectionField(field) {
			return RelationProjection{}, invalidPlanError("forward relation projection contains an invalid target column")
		}
		if _, exists := names[field.Name()]; exists {
			return RelationProjection{}, invalidPlanError("forward relation projection contains a duplicate target field")
		}
		if _, exists := columns[field.Column()]; exists {
			return RelationProjection{}, invalidPlanError("forward relation projection contains a duplicate target column")
		}
		names[field.Name()] = struct{}{}
		columns[field.Column()] = struct{}{}
		if field.Equal(targetKey) {
			primaryKeyCount++
		}
	}
	if primaryKeyCount != 1 {
		return RelationProjection{}, invalidPlanError("forward relation projection must contain its non-null integer target key exactly once")
	}
	return projection, nil
}

func (p RelationProjection) Hop() RelationHop { return p.hop }

func (p RelationProjection) TargetColumns() []FieldRef {
	return append([]FieldRef(nil), p.targetColumns...)
}

func (p RelationProjection) Equal(other RelationProjection) bool {
	return p.hop.Equal(other.hop) && slices.Equal(p.targetColumns, other.targetColumns)
}

func (p RelationProjection) clone() RelationProjection {
	return RelationProjection{
		hop:           p.hop,
		targetColumns: append([]FieldRef(nil), p.targetColumns...),
	}
}

func (p RelationProjection) validate() error {
	targetKey := FieldRef{}
	for _, field := range p.targetColumns {
		if field.Column() == p.hop.TargetPrimaryKeyColumn() {
			targetKey = field
			break
		}
	}
	_, err := NewForwardRelationProjection(
		p.hop.Source(),
		p.hop.SourceTable(),
		NewFieldRef(p.hop.Field(), p.hop.SourceColumn(), FieldInteger, p.hop.Nullable()),
		p.hop.Target(),
		p.hop.TargetTable(),
		targetKey,
		p.targetColumns,
	)
	if err != nil || p.hop.Direction() != RelationForward || p.hop.Cardinality() != ir.RelationManyToOne ||
		p.hop.ReverseName() != "" {
		return invalidPlanError("relation projection is zero, corrupt, or not one forward many-to-one hop")
	}
	return nil
}

func validProjectionIdentity(identity ir.ModelIdentity) bool {
	return canonicalIdentifier(identity.AppLabel) && canonicalIdentifier(identity.ModelName)
}

func validProjectionField(field FieldRef) bool {
	if !canonicalIdentifier(field.Name()) || !canonicalIdentifier(field.Column()) {
		return false
	}
	switch field.Kind() {
	case FieldInteger, FieldString, FieldBoolean:
		return true
	default:
		return false
	}
}

func invalidPlanError(detail string) *Error {
	return &Error{Category: CategoryQuery, Code: CodeInvalidPlan, Detail: detail}
}
