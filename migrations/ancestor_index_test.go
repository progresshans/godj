package migrations

import (
	"fmt"
	"math/rand"
	"testing"
)

// An independent DFS checks reachability, including word boundaries, shared
// ancestors, disconnected roots and names opposite to dependency order.
func TestLoadedAncestorIndexMatchesReachability(t *testing.T) {
	t.Parallel()
	for seed := int64(0); seed < 8; seed++ {
		random := rand.New(rand.NewSource(seed))
		names := random.Perm(129)
		definitions := make([]Migration, len(names))
		parents := make(map[MigrationKey][]MigrationKey, len(names))
		for index, name := range names {
			item := Migration{App: "graph", Name: fmt.Sprintf("node_%03d", name)}
			if index > 0 && index%4 != 0 {
				choices := random.Perm(index)
				for _, parent := range choices[:min(3, len(choices))] {
					item.Dependencies = append(item.Dependencies, definitions[parent].Key())
				}
			}
			definitions[index] = item
			parents[item.Key()] = item.Dependencies
		}
		graph, err := newPlannerGraph(definitions)
		if err != nil {
			t.Fatal(err)
		}
		reconstructor := loadedStateReconstructor{ancestors: newLoadedAncestorIndex(graph)}
		for _, item := range definitions {
			want := make(map[MigrationKey]bool)
			var visit func(MigrationKey)
			visit = func(key MigrationKey) {
				for _, parent := range parents[key] {
					if !want[parent] {
						want[parent] = true
						visit(parent)
					}
				}
			}
			visit(item.Key())
			for _, ancestor := range definitions {
				if got := reconstructor.isAncestor(ancestor.Key(), item.Key()); got != want[ancestor.Key()] {
					t.Fatalf("seed %d: %v ancestor of %v = %v, want %v", seed, ancestor.Key(), item.Key(), got, want[ancestor.Key()])
				}
			}
			if reconstructor.isAncestor(MigrationKey{App: "unknown", Name: "missing"}, item.Key()) {
				t.Fatal("unknown migration became an ancestor")
			}
		}
	}
}
