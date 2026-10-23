package migrations

import (
	"fmt"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func benchmarkProjectState(b *testing.B) ProjectState {
	b.Helper()
	schemas := make([]ir.Schema, 16)
	for index := range schemas {
		schemas[index] = articleSchema()
		schemas[index].AppLabel = fmt.Sprintf("app_%d", index)
	}
	state, err := NewProjectState(schemas...)
	if err != nil {
		b.Fatal(err)
	}
	return state
}

func BenchmarkProjectStateEquality(b *testing.B) {
	left := benchmarkProjectState(b)
	right := left.Clone()
	b.ReportAllocs()
	for b.Loop() {
		if !left.Equal(right) {
			b.Fatal("equal state differs")
		}
	}
}

func BenchmarkProjectStateAppChange(b *testing.B) {
	state := benchmarkProjectState(b)
	schema, _ := state.Schema("app_0")
	schema.Models[0].DBTable = "changed_table"
	b.ReportAllocs()
	for b.Loop() {
		changed := state.withSchema(schema)
		if changed.apps["app_0"].Models[0].DBTable != "changed_table" {
			b.Fatal("state change missing")
		}
	}
}
