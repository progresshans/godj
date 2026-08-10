package query

import (
	"regexp"
	"strings"

	"github.com/progresshans/godj/schema/ir"
)

// RelationDirection identifies the direction in which a relation path
// traverses a symbolic project relation.
type RelationDirection string

const (
	RelationForward RelationDirection = "forward"
	RelationReverse RelationDirection = "reverse"
)

var canonicalRelationIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// RelationTerminalScope identifies whether a relation condition ends on a
// scalar field of the related model or on the source model's local key. The
// latter retains relation provenance while allowing a backend to trim a
// nullable isnull traversal to the root table.
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

// NewForwardRelationPath constructs the one required many-to-one path owned by
// GDJ-0025. AutoField target validation remains in the ORM binder, which owns
// the complete normalized project snapshot.
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
	if nullable {
		return RelationPath{}, &Error{
			Category: CategoryField,
			Code:     CodeUnsupportedLookup,
			Field:    field,
			Detail:   "nullable forward relation paths are not supported",
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
		nullable:               false,
	}
	return RelationPath{
		hops:     []RelationHop{hop},
		terminal: terminal,
		scope:    RelationTerminalRelatedField,
	}, nil
}

// NewNullableForwardRelationIsNullPath constructs the nullable one-hop
// source-key path used by relation-level isnull predicates. The terminal is
// deliberately the canonical local ForeignKey field so compilers can verify
// it against the selected root-model columns before trimming the join.
func NewNullableForwardRelationIsNullPath(
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
			Detail:   "nullable forward relation source-key path contains blank or invalid metadata",
		}
	}
	if sourceKey.Kind() != FieldInteger || !sourceKey.Nullable() {
		return RelationPath{}, &Error{
			Category: CategoryQuery,
			Code:     CodeInvalidPlan,
			Field:    sourceKey.Name(),
			Detail:   "nullable forward relation source key must be a nullable integer field",
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
		nullable:               true,
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

func (p RelationPath) clone() RelationPath {
	return RelationPath{
		hops:     append([]RelationHop(nil), p.hops...),
		terminal: p.terminal,
		scope:    p.scope,
	}
}

func validModelIdentity(identity ir.ModelIdentity) bool {
	return !blank(identity.AppLabel) && !blank(identity.ModelName)
}

func canonicalModelIdentity(identity ir.ModelIdentity) bool {
	return canonicalIdentifier(identity.AppLabel) && canonicalIdentifier(identity.ModelName)
}

func canonicalIdentifier(value string) bool {
	return canonicalRelationIdentifier.MatchString(value)
}

func validReverseTerminal(field FieldRef) bool {
	if !canonicalIdentifier(field.Name()) || !canonicalIdentifier(field.Column()) || field.Nullable() {
		return false
	}
	return field.Kind() == FieldInteger || field.Kind() == FieldString
}

func validFieldRef(field FieldRef) bool {
	if blank(field.Name()) || blank(field.Column()) {
		return false
	}
	switch field.Kind() {
	case FieldInteger, FieldString, FieldBoolean:
		return true
	default:
		return false
	}
}

func blank(value string) bool {
	return strings.TrimSpace(value) == ""
}
