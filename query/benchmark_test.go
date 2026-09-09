package query_test

import (
	"fmt"
	"testing"

	"github.com/progresshans/godj/query"
)

var benchmarkDerivedPlan query.Plan

// Keep the source wide and already ordered/projected: replacing one component
// must not make the cost proportional to every unchanged component.
func BenchmarkPlanDerivation(b *testing.B) {
	for _, width := range []int{8, 64, 256} {
		fields := make([]query.FieldRef, width)
		for index := range fields {
			name := fmt.Sprintf("field_%d", index)
			fields[index] = query.NewFieldRef(name, name, query.FieldInteger, false)
		}
		shape, err := query.NewProjectionResult(fields...)
		if err != nil {
			b.Fatal(err)
		}
		base, err := query.NewPlan("entry", fields).WithOrderings(query.NewOrdering(fields[0], query.Ascending)).WithResultShape(shape)
		if err != nil {
			b.Fatal(err)
		}
		predicate, err := query.NewExpression(query.NewCondition(fields[0], query.LookupExact, query.Integer(7)))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("order_%d", width), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkDerivedPlan = base.WithOrderings(query.NewOrdering(fields[0], query.Descending))
			}
		})
		b.Run(fmt.Sprintf("filter_%d", width), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkDerivedPlan, err = base.WithWhere(predicate)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
