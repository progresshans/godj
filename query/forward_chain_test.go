package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestForwardChainOwnsHopsAndNullableAncestors(t *testing.T) {
	node := ir.ModelIdentity{AppLabel: "tree", ModelName: "node"}
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	optional, err := query.NewForwardRelationPath(node, "tree_node", "parent", "parent_id", node, "tree_node", "id", true, name, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	required, err := query.NewForwardRelationPath(node, "tree_node", "owner", "owner_id", node, "tree_node", "id", false, name, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	hops := append(optional.Hops(), required.Hops()...)
	path, err := query.NewForwardRelationChain(hops, name, query.RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	condition := query.NewRelatedCondition(path, query.LookupExact, query.String("root"))
	if !condition.OperandNullable() || condition.Field().Nullable() {
		t.Fatal("required scalar lost nullable ancestor")
	}
	hops[0] = query.RelationHop{}
	copy := path.Hops()
	copy[1] = query.RelationHop{}
	if err := path.Validate(); err != nil {
		t.Fatal("caller mutation changed path", err)
	}
	if !path.Equal(path) || path.Equal(optional) {
		t.Fatal("route equality ignores path depth")
	}
	key := query.NewFieldRef("owner", "owner_id", query.FieldInteger, false)
	trimmed, err := query.NewForwardRelationChain(path.Hops(), key, query.RelationTerminalSourceKey)
	if err != nil {
		t.Fatal(err)
	}
	if !query.NewRelatedCondition(trimmed, query.LookupIsNull, query.Boolean(true)).OperandNullable() {
		t.Fatal("source key lost ancestor nullability")
	}
	if trimmed.Equal(path) {
		t.Fatal("terminal scope lost")
	}
	if _, err := query.NewForwardRelationIsNullPath(node, "tree_node", key, node, "tree_node", "id", ir.RelationManyToOne); err != nil {
		t.Fatal("required presence rejected", err)
	}
	for name, input := range map[string]struct {
		hops     []query.RelationHop
		terminal query.FieldRef
		scope    query.RelationTerminalScope
	}{
		"empty":                 {nil, name, query.RelationTerminalRelatedField},
		"zero hop":              {hops, name, query.RelationTerminalRelatedField},
		"invalid scope":         {path.Hops(), name, "unknown"},
		"mismatched source key": {path.Hops(), query.NewFieldRef("owner", "wrong", query.FieldInteger, false), query.RelationTerminalSourceKey},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := query.NewForwardRelationChain(input.hops, input.terminal, input.scope); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatalf("invalid chain: %v", err)
			}
		})
	}
	other, err := query.NewForwardRelationPath(ir.ModelIdentity{AppLabel: "other", ModelName: "node"}, "tree_node", "parent", "parent_id", node, "tree_node", "id", true, name, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewForwardRelationChain(append(optional.Hops(), other.Hops()...), name, query.RelationTerminalRelatedField); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("disconnected identities: %v", err)
	}
	hops = make([]query.RelationHop, query.MaximumRelationHops)
	for i := range hops {
		hops[i] = optional.Hops()[0]
	}
	if _, err := query.NewForwardRelationChain(hops, name, query.RelationTerminalRelatedField); err != nil {
		t.Fatal("bounded cycle rejected", err)
	}
	if _, err := query.NewForwardRelationChain(append(hops, hops[0]), name, query.RelationTerminalRelatedField); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("unbounded route accepted: %v", err)
	}
}
