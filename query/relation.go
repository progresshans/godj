package query

import (
	"strings"

	"github.com/progresshans/godj/schema/ir"
)

// RelationDirection identifies the direction in which a relation path
// traverses a symbolic project relation.
type RelationDirection string

const RelationForward RelationDirection = "forward"

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
func (h RelationHop) Direction() RelationDirection        { return h.direction }
func (h RelationHop) Cardinality() ir.RelationCardinality { return h.cardinality }
func (h RelationHop) Nullable() bool                      { return h.nullable }
func (h RelationHop) Equal(other RelationHop) bool        { return h == other }

// RelationPath is an immutable symbolic traversal ending at a scalar target
// field. GDJ-0025 constructs exactly one hop, while retaining a slice keeps
// unsupported shapes visible to compilers as structured values rather than
// encoding SQL aliases in the AST.
type RelationPath struct {
	hops     []RelationHop
	terminal FieldRef
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
	return RelationPath{hops: []RelationHop{hop}, terminal: terminal}, nil
}

func (p RelationPath) Hops() []RelationHop {
	return append([]RelationHop(nil), p.hops...)
}

func (p RelationPath) Terminal() FieldRef { return p.terminal }

func (p RelationPath) Equal(other RelationPath) bool {
	if !p.terminal.Equal(other.terminal) || len(p.hops) != len(other.hops) {
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
	}
}

func validModelIdentity(identity ir.ModelIdentity) bool {
	return !blank(identity.AppLabel) && !blank(identity.ModelName)
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
