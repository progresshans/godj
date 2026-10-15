package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestCollectionAggregateCompilerKeepsFilterOccurrencesAndParameters(t *testing.T) {
	querytest.CollectionAggregateCompiler(t, func(plan query.Plan) (string, []any, error) {
		return compilePlan("collection_owner", plan)
	}, true)
}

func TestCollectionComputedCompilerSharesValueCaseAndFilterRows(t *testing.T) {
	querytest.CollectionComputedCompiler(t, func(plan query.Plan) (string, []any, error) {
		return compilePlan("collection_owner", plan)
	})
}
