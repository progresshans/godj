package sqlite_test

import (
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/internal/querytest"
)

func TestCollectionAggregateCompilerKeepsFilterOccurrencesAndParameters(t *testing.T) {
	querytest.CollectionAggregateCompiler(t, sqlite.Compile, false)
}

func TestCollectionComputedCompilerSharesValueCaseAndFilterRows(t *testing.T) {
	querytest.CollectionComputedCompiler(t, sqlite.Compile)
}
