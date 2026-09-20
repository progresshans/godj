package query

import (
	"fmt"
	"slices"
	"strings"

	"github.com/progresshans/godj/schema/ir"
)

// RelationProjection describes one target occurrence on a finite forward route.
// Its path terminates at that target's primary key; columns retain descriptor
// order. Join kind follows route nullability and the complete query predicate.
type RelationProjection struct {
	path          RelationPath
	targetColumns []FieldRef
}

// NewForwardRelationProjection constructs a direct projection. Deeper targets
// use NewForwardChainProjection and the same immutable route as query filters.
func NewForwardRelationProjection(source ir.ModelIdentity, sourceTable string, sourceKey FieldRef, target ir.ModelIdentity, targetTable string, targetKey FieldRef, orderedTargetColumns []FieldRef) (RelationProjection, error) {
	if !validProjectionField(sourceKey) || sourceKey.Kind() != FieldInteger {
		return RelationProjection{}, invalidPlanError("forward relation projection contains an invalid source key")
	}
	hop := RelationHop{source: source, sourceTable: sourceTable, field: sourceKey.Name(), sourceColumn: sourceKey.Column(), target: target, targetTable: targetTable, targetPrimaryKeyColumn: targetKey.Column(), direction: RelationForward, cardinality: ir.RelationManyToOne, nullable: sourceKey.Nullable()}
	return NewForwardChainProjection([]RelationHop{hop}, targetKey, orderedTargetColumns)
}

// NewForwardChainProjection owns the route and ordered columns. Every physical
// identity is canonical, and the non-null integer target key occurs once.
func NewForwardChainProjection(hops []RelationHop, targetKey FieldRef, orderedTargetColumns []FieldRef) (RelationProjection, error) {
	path, err := NewForwardRelationChain(hops, targetKey, RelationTerminalRelatedField)
	if err != nil {
		return RelationProjection{}, err
	}
	projection := RelationProjection{path: path, targetColumns: orderedTargetColumns}
	if err := projection.Validate(); err != nil {
		return RelationProjection{}, err
	}
	projection.targetColumns = append([]FieldRef(nil), orderedTargetColumns...)
	return projection, nil
}

func (p RelationProjection) Path() RelationPath { return p.path }

// TerminalHop is the final physical FK declaration, not its route identity.
// A zero projection returns a zero hop and remains invalid for compilation.
func (p RelationProjection) TerminalHop() RelationHop {
	if len(p.path.hops) == 0 {
		return RelationHop{}
	}
	return p.path.hops[len(p.path.hops)-1]
}
func (p RelationProjection) TargetColumns() []FieldRef {
	return append([]FieldRef(nil), p.targetColumns...)
}
func (p RelationProjection) Equal(other RelationProjection) bool {
	return p.path.Equal(other.path) && slices.Equal(p.targetColumns, other.targetColumns)
}

// Validate is shared by plan construction and backend compilation, including
// empty-result queries that must fail before any SQL can be skipped.
func (p RelationProjection) Validate() error {
	if err := p.path.validateForward(); err != nil {
		return err
	}
	if p.path.scope != RelationTerminalRelatedField {
		return invalidPlanError("projection requires a target-field route")
	}
	for _, hop := range p.path.hops {
		if !validProjectionIdentity(hop.Source()) || !validProjectionIdentity(hop.Target()) || !canonicalIdentifier(hop.SourceTable()) || !canonicalIdentifier(hop.Field()) || !canonicalIdentifier(hop.SourceColumn()) || !canonicalIdentifier(hop.TargetTable()) || !canonicalIdentifier(hop.TargetPrimaryKeyColumn()) {
			return invalidPlanError("forward relation projection contains non-canonical route metadata")
		}
	}
	targetKey := p.path.terminal
	if !validProjectionField(targetKey) || targetKey.Kind() != FieldInteger || targetKey.Nullable() || targetKey.Column() != p.TerminalHop().TargetPrimaryKeyColumn() {
		return invalidPlanError("projection requires its target's non-null integer primary key")
	}
	if len(p.targetColumns) == 0 {
		return invalidPlanError("forward relation projection has no target columns")
	}
	keys := 0
	names := map[string]bool{}
	columns := map[string]bool{}
	for _, field := range p.targetColumns {
		if !validProjectionField(field) {
			return invalidPlanError("forward relation projection contains an invalid target column")
		}
		if names[field.Name()] || columns[field.Column()] {
			return invalidPlanError("forward relation projection contains duplicate target fields or columns")
		}
		names[field.Name()], columns[field.Column()] = true, true
		if field.Equal(targetKey) {
			keys++
		}
	}
	if keys != 1 {
		return invalidPlanError("forward relation projection must contain its non-null integer target key exactly once")
	}
	return nil
}

// Source-FK names identify an occurrence below one logical root. Framing keeps
// raw metadata unambiguous even if a field name contains a lookup separator.
func projectionRouteKey(hops []RelationHop) string {
	var result strings.Builder
	for _, hop := range hops {
		fmt.Fprintf(&result, "%d:%s", len(hop.field), hop.field)
	}
	return result.String()
}
func compareProjectionRoutes(left, right RelationProjection) int {
	for i := 0; i < len(left.path.hops) && i < len(right.path.hops); i++ {
		if order := strings.Compare(left.path.hops[i].field, right.path.hops[i].field); order != 0 {
			return order
		}
	}
	if len(left.path.hops) < len(right.path.hops) {
		return -1
	}
	if len(left.path.hops) > len(right.path.hops) {
		return 1
	}
	return 0
}
func validProjectionIdentity(identity ir.ModelIdentity) bool {
	return canonicalIdentifier(identity.AppLabel) && canonicalIdentifier(identity.ModelName)
}
func validProjectionField(field FieldRef) bool {
	if !field.ValidType() {
		return false
	}
	if !canonicalIdentifier(field.Name()) || !canonicalIdentifier(field.Column()) {
		return false
	}
	switch field.Kind() {
	case FieldInteger, FieldFloat, FieldDecimal, FieldUUID, FieldJSON, FieldString, FieldBoolean, FieldDateTime, FieldDate, FieldTime, FieldDuration:
		return true
	default:
		return false
	}
}
func invalidPlanError(detail string) *Error {
	return &Error{Category: CategoryQuery, Code: CodeInvalidPlan, Detail: detail}
}
