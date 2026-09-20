package query

import (
	"strings"

	"github.com/progresshans/godj/internal/identifiers"
	"github.com/progresshans/godj/schema/ir"
)

// RelationDirection identifies the direction in which a relation path
// traverses a symbolic project relation.
type RelationDirection string

const (
	RelationForward RelationDirection = "forward"
	RelationReverse RelationDirection = "reverse"
)

// RelationTerminalScope identifies whether a relation condition ends on a
// scalar field of the related model or on the source model's local key. The
// latter retains relation provenance while allowing a backend to trim a
// isnull traversal to its owning row.
type RelationTerminalScope string

const (
	RelationTerminalRelatedField RelationTerminalScope = "related_field"
	RelationTerminalSourceKey    RelationTerminalScope = "source_key"
)

// RelationHop is the immutable, backend-independent description of one
// relation edge. All state is private so later relation work can extend the
// representation without exposing mutable compiler data.
type RelationHop struct {
	source                 ir.ModelIdentity
	sourceTable            string
	field                  string
	sourceColumn           string
	target                 ir.ModelIdentity
	targetTable            string
	targetPrimaryKeyColumn string
	reverseName            string
	direction              RelationDirection
	cardinality            ir.RelationCardinality
	nullable               bool
}

func (h RelationHop) Source() ir.ModelIdentity            { return h.source }
func (h RelationHop) SourceTable() string                 { return h.sourceTable }
func (h RelationHop) Field() string                       { return h.field }
func (h RelationHop) SourceColumn() string                { return h.sourceColumn }
func (h RelationHop) Target() ir.ModelIdentity            { return h.target }
func (h RelationHop) TargetTable() string                 { return h.targetTable }
func (h RelationHop) TargetPrimaryKeyColumn() string      { return h.targetPrimaryKeyColumn }
func (h RelationHop) ReverseName() string                 { return h.reverseName }
func (h RelationHop) Direction() RelationDirection        { return h.direction }
func (h RelationHop) Cardinality() ir.RelationCardinality { return h.cardinality }
func (h RelationHop) Nullable() bool                      { return h.nullable }
func (h RelationHop) Equal(other RelationHop) bool        { return h == other }

// RelationPath is an immutable symbolic traversal ending at either a scalar
// target field or the source model's local key. Retaining a slice keeps
// unsupported shapes visible to compilers as structured values rather than
// encoding SQL aliases in the AST.
type RelationPath struct {
	hops     []RelationHop
	terminal FieldRef
	scope    RelationTerminalScope
}

// NewReverseRelationPath constructs one declaration-centric reverse
// one-to-many path. Source remains the model that owns the physical
// ForeignKey declaration; Target is the model whose namespace owns the
// reverse name and whose table is the query root.
func NewReverseRelationPath(
	source ir.ModelIdentity,
	sourceTable, sourceField, sourceColumn string,
	target ir.ModelIdentity,
	targetTable, targetPKColumn, reverseName string,
	nullable bool,
	terminal FieldRef,
) (RelationPath, error) {
	if !canonicalModelIdentity(source) || !canonicalModelIdentity(target) ||
		!canonicalIdentifier(sourceTable) || !canonicalIdentifier(sourceField) ||
		!canonicalIdentifier(sourceColumn) || !canonicalIdentifier(targetTable) ||
		!canonicalIdentifier(targetPKColumn) || !canonicalIdentifier(reverseName) ||
		!validReverseTerminal(terminal) {
		return RelationPath{}, &Error{
			Category: CategoryQuery,
			Code:     CodeInvalidPlan,
			Field:    sourceField,
			Detail:   "reverse relation path contains non-canonical or unsupported metadata",
		}
	}

	hop := RelationHop{
		source:                 source,
		sourceTable:            sourceTable,
		field:                  sourceField,
		sourceColumn:           sourceColumn,
		target:                 target,
		targetTable:            targetTable,
		targetPrimaryKeyColumn: targetPKColumn,
		reverseName:            reverseName,
		direction:              RelationReverse,
		cardinality:            ir.RelationOneToMany,
		nullable:               nullable,
	}
	return RelationPath{
		hops:     []RelationHop{hop},
		terminal: terminal,
		scope:    RelationTerminalRelatedField,
	}, nil
}

// NewForwardRelationPath constructs one direct many-to-one path, retaining
// whether the source key is nullable. AutoField target validation remains in
// the ORM binder, which owns the complete normalized project snapshot.
func NewForwardRelationPath(
	source ir.ModelIdentity,
	sourceTable, field, sourceColumn string,
	target ir.ModelIdentity,
	targetTable, targetPKColumn string,
	nullable bool,
	terminal FieldRef,
) (RelationPath, error) {
	if !validModelIdentity(source) || !validModelIdentity(target) ||
		blank(sourceTable) || blank(field) || blank(sourceColumn) ||
		blank(targetTable) || blank(targetPKColumn) || !validFieldRef(terminal) {
		return RelationPath{}, &Error{
			Category: CategoryQuery,
			Code:     CodeInvalidPlan,
			Field:    field,
			Detail:   "forward relation path contains blank or invalid metadata",
		}
	}
	hop := RelationHop{
		source:                 source,
		sourceTable:            sourceTable,
		field:                  field,
		sourceColumn:           sourceColumn,
		target:                 target,
		targetTable:            targetTable,
		targetPrimaryKeyColumn: targetPKColumn,
		direction:              RelationForward,
		cardinality:            ir.RelationManyToOne,
		nullable:               nullable,
	}
	return RelationPath{
		hops:     []RelationHop{hop},
		terminal: terminal,
		scope:    RelationTerminalRelatedField,
	}, nil
}

// NewForwardRelationIsNullPath retains the local FK terminal and provenance.
// A required FK can still be NULL after an optional ancestor join in a chain.
func NewForwardRelationIsNullPath(
	source ir.ModelIdentity,
	sourceTable string,
	sourceKey FieldRef,
	target ir.ModelIdentity,
	targetTable, targetPKColumn string,
) (RelationPath, error) {
	if !validModelIdentity(source) || !validModelIdentity(target) ||
		blank(sourceTable) || !validFieldRef(sourceKey) ||
		blank(targetTable) || blank(targetPKColumn) {
		return RelationPath{}, &Error{
			Category: CategoryQuery,
			Code:     CodeInvalidPlan,
			Field:    sourceKey.Name(),
			Detail:   "forward relation source-key path contains blank or invalid metadata",
		}
	}
	if sourceKey.Kind() != FieldInteger {
		return RelationPath{}, &Error{
			Category: CategoryQuery,
			Code:     CodeInvalidPlan,
			Field:    sourceKey.Name(),
			Detail:   "forward relation source key must be an integer field",
		}
	}
	hop := RelationHop{
		source:                 source,
		sourceTable:            sourceTable,
		field:                  sourceKey.Name(),
		sourceColumn:           sourceKey.Column(),
		target:                 target,
		targetTable:            targetTable,
		targetPrimaryKeyColumn: targetPKColumn,
		direction:              RelationForward,
		cardinality:            ir.RelationManyToOne,
		nullable:               sourceKey.Nullable(),
	}
	return RelationPath{
		hops:     []RelationHop{hop},
		terminal: sourceKey,
		scope:    RelationTerminalSourceKey,
	}, nil
}

func (p RelationPath) Hops() []RelationHop {
	return append([]RelationHop(nil), p.hops...)
}

func (p RelationPath) Terminal() FieldRef { return p.terminal }

func (p RelationPath) TerminalScope() RelationTerminalScope { return p.scope }

func (p RelationPath) Equal(other RelationPath) bool {
	if p.scope != other.scope || !p.terminal.Equal(other.terminal) || len(p.hops) != len(other.hops) {
		return false
	}
	for index := range p.hops {
		if !p.hops[index].Equal(other.hops[index]) {
			return false
		}
	}
	return true
}

func validModelIdentity(identity ir.ModelIdentity) bool {
	return !blank(identity.AppLabel) && !blank(identity.ModelName)
}

func canonicalModelIdentity(identity ir.ModelIdentity) bool {
	return canonicalIdentifier(identity.AppLabel) && canonicalIdentifier(identity.ModelName)
}

func canonicalIdentifier(value string) bool {
	return identifiers.SQL(value)
}

func validReverseTerminal(field FieldRef) bool {
	if !canonicalIdentifier(field.Name()) || !canonicalIdentifier(field.Column()) || field.Nullable() {
		return false
	}
	return field.Kind() == FieldDate || (field.Kind() == FieldTime || field.Kind() == FieldDuration) || field.Kind() == FieldDateTime || field.Kind() == FieldInteger || field.Kind() == FieldFloat || field.Kind() == FieldString
}

func validFieldRef(field FieldRef) bool {
	if blank(field.Name()) || blank(field.Column()) {
		return false
	}
	switch field.Kind() {
	case FieldInteger, FieldFloat, FieldString, FieldBoolean, FieldDateTime, FieldDate, FieldTime, FieldDuration:
		return true
	default:
		return false
	}
}

func blank(value string) bool {
	return strings.TrimSpace(value) == ""
}

// MaximumRelationHops bounds path construction, including self-references.
// Backend join limits may impose a smaller effective query-wide bound.
const MaximumRelationHops = 64

// NewForwardRelationChain copies an ordered route. Repeated declarations in a
// self-reference remain distinct occurrences, while adjacent models must agree.
func NewForwardRelationChain(hops []RelationHop, terminal FieldRef, scope RelationTerminalScope) (RelationPath, error) {
	path := RelationPath{hops: hops, terminal: terminal, scope: scope}
	if err := path.validateForward(); err != nil {
		return RelationPath{}, err
	}
	path.hops = append([]RelationHop(nil), hops...)
	return path, nil
}

// Validate checks a complete route independently of a backend or query root.
func (p RelationPath) Validate() error {
	if len(p.hops) == 1 && p.hops[0].direction == RelationReverse {
		hop := p.hops[0]
		if p.scope != RelationTerminalRelatedField || hop.cardinality != ir.RelationOneToMany {
			return invalidPlanError("reverse relation path has an invalid scope or cardinality")
		}
		_, err := NewReverseRelationPath(hop.source, hop.sourceTable, hop.field, hop.sourceColumn, hop.target, hop.targetTable, hop.targetPrimaryKeyColumn, hop.reverseName, hop.nullable, p.terminal)
		return err
	}
	return p.validateForward()
}
func (p RelationPath) validateForward() error {
	if len(p.hops) == 0 || len(p.hops) > MaximumRelationHops {
		return invalidPlanError("forward relation path requires between 1 and 64 hops")
	}
	if !validFieldRef(p.terminal) {
		return invalidPlanError("forward relation terminal is invalid")
	}
	if p.scope != RelationTerminalRelatedField && p.scope != RelationTerminalSourceKey {
		return invalidPlanError("forward relation terminal scope is invalid")
	}
	for index, hop := range p.hops {
		if hop.direction != RelationForward || hop.cardinality != ir.RelationManyToOne || hop.reverseName != "" || !validModelIdentity(hop.source) || !validModelIdentity(hop.target) || blank(hop.sourceTable) || blank(hop.field) || blank(hop.sourceColumn) || blank(hop.targetTable) || blank(hop.targetPrimaryKeyColumn) {
			return invalidPlanError("forward relation path has an invalid hop")
		}
		if index > 0 {
			previous := p.hops[index-1]
			if previous.target != hop.source || previous.targetTable != hop.sourceTable {
				return invalidPlanError("forward relation path has disconnected model or table metadata")
			}
		}
	}
	if p.scope == RelationTerminalSourceKey {
		hop := p.hops[len(p.hops)-1]
		if !p.terminal.Equal(NewFieldRef(hop.field, hop.sourceColumn, FieldInteger, hop.nullable)) {
			return invalidPlanError("forward relation source-key terminal disagrees with its final declaration")
		}
	}
	return nil
}
