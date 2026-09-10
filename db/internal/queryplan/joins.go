package queryplan

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/query"
)

// Join contains unquoted SQL-independent names. Compilers own qualification,
// quoting, placeholders and the final SQL text.
type Join struct {
	Table, RootColumn, Column, Alias string
	LeftOuter                        bool
}

type Joins struct {
	Keys          []RelationKey
	ByKey         map[RelationKey]Join
	ProjectionKey RelationKey
}

// PrepareJoins consumes the compiler's private edge inventory. Predicate
// capabilities and duplicate-hop equality must already have been checked.
// It validates projection provenance and owns deterministic alias allocation.
func PrepareJoins(plan query.Plan, edges map[RelationKey]query.RelationHop, sourceKeys []query.RelationHop, backendName string) (Joins, error) {
	projection, selected := plan.RelationProjection()
	var projectionKey RelationKey
	if selected {
		var err error
		projectionKey, err = RelationProjection(plan, projection, backendName)
		if err != nil {
			return Joins{}, err
		}
		hop := projection.Hop()
		for _, sourceKey := range sourceKeys {
			if SameSourceEdge(sourceKey, hop) && !sourceKey.Equal(hop) {
				return Joins{}, invalidPlan("relation projection source-key provenance does not match the selected edge")
			}
		}
		if previous, exists := edges[projectionKey]; exists && !previous.Equal(hop) {
			return Joins{}, invalidPlan(fmt.Sprintf("relation edge %s.%s.%s has inconsistent predicate and projection metadata", projectionKey.SourceApp, projectionKey.SourceModel, projectionKey.Field))
		}
		edges[projectionKey] = hop
		for key := range edges {
			if key != projectionKey {
				return Joins{}, invalidPlan(backendName + " relation projection cannot combine unrelated relation joins")
			}
		}
	}

	keys := make([]RelationKey, 0, len(edges))
	for key := range edges {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, CompareRelationKey)
	joins := make(map[RelationKey]Join, len(keys))
	for index, key := range keys {
		hop := edges[key]
		join := Join{
			Table:      hop.TargetTable(),
			RootColumn: hop.SourceColumn(),
			Column:     hop.TargetPrimaryKeyColumn(),
			Alias:      fmt.Sprintf("t%d", index+1),
			LeftOuter:  selected && key == projectionKey && hop.Nullable(),
		}
		if hop.Direction() == query.RelationReverse {
			join.Table = hop.SourceTable()
			join.RootColumn = hop.TargetPrimaryKeyColumn()
			join.Column = hop.SourceColumn()
		}
		joins[key] = join
	}
	return Joins{Keys: keys, ByKey: joins, ProjectionKey: projectionKey}, nil
}
