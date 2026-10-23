package projectwire

import (
	"testing"

	"github.com/progresshans/godj/internal/projectspec"
)

// Near-limit arithmetic is checked without allocating a maximum-size schema.
func TestAggregateSchemaBudgetBoundaries(t *testing.T) {
	budget := specBudget{fields: projectspec.MaxAggregateFields - 1, nodes: projectspec.MaxAggregateNodes - 1}
	if err := budget.consumeFields(1); err != nil {
		t.Fatalf("maximum fields rejected: %v", err)
	}
	if err := budget.consumeNodes(1); err != nil {
		t.Fatalf("maximum nodes rejected: %v", err)
	}
	if err := budget.consumeFields(1); err == nil {
		t.Fatal("maximum+1 fields accepted")
	}
	if err := budget.consumeNodes(1); err == nil {
		t.Fatal("maximum+1 nodes accepted")
	}
}
