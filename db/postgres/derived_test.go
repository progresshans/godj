package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestCompileDerivedResults(t *testing.T) {
	querytest.DerivedCompiler(t, func(plan query.Plan) (string, []any, error) { return compilePlan("derived_schema", plan) }, true)
}

func TestCompileResultPages(t *testing.T) {
	querytest.ResultPageCompiler(t, func(plan query.Plan) (string, []any, error) { return compilePlan("page_schema", plan) }, true)
}
