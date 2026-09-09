package migrations

import (
	"fmt"
	"testing"
)

var benchmarkAncestors loadedAncestorIndex

func BenchmarkLoadedAncestorIndex(b *testing.B) {
	for _, count := range []int{128, 2048} {
		definitions := make([]Migration, count)
		for index := range definitions {
			definitions[index] = Migration{App: "bench", Name: fmt.Sprintf("migration_%04d", index)}
			if index > 0 {
				definitions[index].Dependencies = []MigrationKey{definitions[index-1].Key()}
			}
		}
		graph, err := newPlannerGraph(definitions)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("chain_%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkAncestors = newLoadedAncestorIndex(graph)
			}
		})
	}
}
