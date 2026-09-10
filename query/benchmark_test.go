package query_test

import (
	"fmt"
	"testing"

	"github.com/progresshans/godj/query"
)

var benchmarkDerivedPlan query.Plan

func BenchmarkConditionBatching(b *testing.B) {
	for _, count := range []int{8, 64, 256, 1023} {
		field := query.NewFieldRef("id", "id", query.FieldInteger, false)
		conditions := make([]query.Condition, count)
		for index := range conditions {
			conditions[index] = query.NewCondition(field, query.LookupExact, query.Integer(int64(index)))
		}
		base := query.NewPlan("entry", []query.FieldRef{field})
		batch, err := base.WithConditions(conditions...)
		if err != nil {
			b.Fatal(err)
		}
		chain := base
		for _, condition := range conditions {
			chain, err = chain.WithConditions(condition)
			if err != nil {
				b.Fatal(err)
			}
		}
		if !batch.Equal(chain) {
			b.Fatal("valid bounded conditions differ between batch and chain")
		}
		for _, batched := range []bool{false, true} {
			b.Run(fmt.Sprintf("conditions_%d/batch_%t", count, batched), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					benchmarkDerivedPlan = base
					if batched {
						benchmarkDerivedPlan, err = base.WithConditions(conditions...)
						if err != nil {
							b.Fatal(err)
						}
						continue
					}
					for _, condition := range conditions {
						benchmarkDerivedPlan, err = benchmarkDerivedPlan.WithConditions(condition)
						if err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}

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
