package query

import "github.com/progresshans/godj/schema/ir"

// NewRelationChain constructs a connected traversal with the primary key of
// every visited model, including the root. Collections retain row identity for
// correlated existence checks; a backend must never guess an intermediary key.
// Model/field authority remains with the caller's sealed Schema IR binding.
func NewRelationChain(hops []RelationHop, primaryKeys []FieldRef, terminal FieldRef, scope RelationTerminalScope) (RelationPath, error) {
	p := RelationPath{hops: hops, keys: primaryKeys, terminal: terminal, scope: scope}
	if err := p.validateKeyedRoute(); err != nil {
		return RelationPath{}, err
	}
	p.hops = append([]RelationHop(nil), hops...)
	p.keys = append([]FieldRef(nil), primaryKeys...)
	return p, nil
}

// PrimaryKeys returns detached metadata in traversal order (root, then each
// hop's destination). Older paths without identity metadata return nil.
func (p RelationPath) PrimaryKeys() []FieldRef { return append([]FieldRef(nil), p.keys...) }

// ReuseNextCollectionFilter marks a manager's core membership filter. The
// immediately following Filter may constrain that same intermediary row. Any
// intervening query refinement consumes this one-use scope, including an empty
// Filter; later filters always own independent collection joins.
func (p Plan) ReuseNextCollectionFilter() (Plan, error) {
	if p.collectionFilters == 0 {
		return Plan{}, invalidPlanError("collection filter reuse requires a membership anchor")
	}
	p.reuseCollectionFilter = true
	return p, nil
}

func (p Plan) WithoutCollectionFilterReuse() Plan {
	p.reuseCollectionFilter = false
	return p
}

func (p RelationPath) validateKeyedRoute() error {
	if len(p.hops) == 0 || len(p.hops) > MaximumRelationHops || len(p.keys) != len(p.hops)+1 || !validFieldRef(p.terminal) {
		return invalidPlanError("relation route requires connected hops, model keys, and a valid terminal")
	}
	if p.scope != RelationTerminalRelatedField && p.scope != RelationTerminalSourceKey {
		return invalidPlanError("relation route terminal scope is invalid")
	}
	for _, key := range p.keys {
		if key.Kind() != FieldInteger || key.Nullable() || !canonicalIdentifier(key.Name()) || !canonicalIdentifier(key.Column()) {
			return invalidPlanError("relation route model key must be a canonical non-null integer field")
		}
	}
	type model struct {
		table string
		key   FieldRef
	}
	models := make(map[ir.ModelIdentity]model, len(p.keys))
	addModel := func(identity ir.ModelIdentity, table string, key FieldRef) error {
		value := model{table: table, key: key}
		if previous, exists := models[identity]; exists && previous != value {
			return invalidPlanError("relation route repeats a model with conflicting table or primary key metadata")
		}
		models[identity] = value
		return nil
	}
	collection := false
	var filterScope uint32
	for index, hop := range p.hops {
		if !canonicalModelIdentity(hop.source) || !canonicalModelIdentity(hop.target) ||
			!canonicalIdentifier(hop.sourceTable) || !canonicalIdentifier(hop.targetTable) ||
			!canonicalIdentifier(hop.field) || !canonicalIdentifier(hop.sourceColumn) || !canonicalIdentifier(hop.targetPrimaryKeyColumn) {
			return invalidPlanError("relation route contains non-canonical declaration metadata")
		}
		switch hop.direction {
		case RelationForward:
			if !hop.cardinality.SingleValued() || hop.reverseName != "" || hop.targetPrimaryKeyColumn != p.keys[index+1].Column() {
				return invalidPlanError("forward route declaration disagrees with its destination key or cardinality")
			}
		case RelationReverse:
			if (hop.cardinality != ir.RelationOneToMany && hop.cardinality != ir.RelationOneToOne) ||
				!canonicalIdentifier(hop.reverseName) || hop.targetPrimaryKeyColumn != p.keys[index].Column() {
				return invalidPlanError("reverse route declaration disagrees with its owner key or cardinality")
			}
		default:
			return invalidPlanError("relation route direction is invalid")
		}
		if !collection && !hop.cardinality.SingleValued() {
			collection = true
			filterScope = hop.filterScope
		}
		if (!collection && hop.filterScope != 0) || (collection && hop.filterScope != filterScope) || hop.filterScope >= maximumExpressionNodes {
			return invalidPlanError("relation filter scope must begin at its first collection and remain consistent")
		}
		from, fromTable := hop.From()
		to, toTable := hop.To()
		if index > 0 {
			previous, table := p.hops[index-1].To()
			if previous != from || table != fromTable {
				return invalidPlanError("relation route is disconnected")
			}
		}
		if err := addModel(from, fromTable, p.keys[index]); err != nil {
			return err
		}
		if err := addModel(to, toTable, p.keys[index+1]); err != nil {
			return err
		}
	}
	if p.scope == RelationTerminalSourceKey {
		hop := p.hops[len(p.hops)-1]
		if hop.direction != RelationForward || !p.terminal.Equal(NewFieldRef(hop.field, hop.sourceColumn, FieldInteger, hop.nullable)) {
			return invalidPlanError("source-key terminal disagrees with its final forward declaration")
		}
	}
	return nil
}

// Each Filter/WithWhere call has a fresh collection join scope. Single-valued
// prefixes remain reusable, while the first collection and all descendants
// belong to the current call. Predicates and already-derived plans stay immutable.
func bindCollectionFilter(expression Expression, scope uint32) (Expression, bool) {
	if expression.node.kind == ExpressionLeaf {
		path := expression.node.condition.relationPath
		if path == nil || path.SingleValued() {
			return expression, false
		}
		copyPath := *path
		copyPath.hops = append([]RelationHop(nil), path.hops...)
		collection := false
		for index := range copyPath.hops {
			collection = collection || !copyPath.hops[index].cardinality.SingleValued()
			if collection {
				copyPath.hops[index].filterScope = scope
			}
		}
		node := *expression.node
		node.condition.relationPath = &copyPath
		return Expression{node: &node}, true
	}
	if !expression.HasRelations() {
		return expression, false
	}
	node := *expression.node
	node.children = append([]Expression(nil), expression.node.children...)
	collection := false
	for index, child := range node.children {
		bound, found := bindCollectionFilter(child, scope)
		node.children[index] = bound
		collection = collection || found
	}
	if !collection {
		return expression, false
	}
	return Expression{node: &node}, true
}
