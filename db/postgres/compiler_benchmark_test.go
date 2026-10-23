package postgres

import (
	"fmt"
	"testing"

	"github.com/progresshans/godj/query"
)

var benchmarkConditionSQL string
var benchmarkConditionArguments []any

// Measure the complete supported compiler, including its validation pass.
// The scalar control makes added preparation cost visible when no IN is used.
func BenchmarkPostgresConditionCompilation(b *testing.B) {
	for _, count := range []int{0, 8, 256, 999} {
		field := query.NewFieldRef("id", "id", query.FieldInteger, false)
		condition := query.NewCondition(field, query.LookupExact, query.Integer(1))
		name := "exact"
		if count > 0 {
			name = fmt.Sprintf("in_%d", count)
			values := make([]query.Value, count)
			for index := range values {
				values[index] = query.Integer(int64(index + 1))
			}
			var err error
			condition, err = query.NewInCondition(field, values)
			if err != nil {
				b.Fatal(err)
			}
		}
		plan, err := query.NewPlan("entry", []query.FieldRef{field}).WithConditions(condition)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkConditionSQL, benchmarkConditionArguments, err = compilePlan("godj_app", plan)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
