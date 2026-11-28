package postgres

import (
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/uniquetest"
	mb "github.com/progresshans/godj/migrations/backend"
)

func TestPostgresNamedConstraintCatalogChecksEveryMemberAndOperator(t *testing.T) {
	model, _, _ := uniquetest.CompositeModel(t, "char", true)
	exact := postgresMigrationTestCatalog(t, "product_schema", model, nil)
	if err := assertPostgresMigrationModelCatalog(exact, "product_schema", model, nil); err != nil {
		t.Fatal("valid composite fixture rejected", err)
	}
	for name, mutate := range map[string]func(*postgresMigrationTableCatalog){
		"second constraint member": func(c *postgresMigrationTableCatalog) { c.constraints[2].sourceAttributes[1] = 1 },
		"reordered constraint":     func(c *postgresMigrationTableCatalog) { slices.Reverse(c.constraints[2].sourceAttributes) },
		"missing constraint member": func(c *postgresMigrationTableCatalog) {
			c.constraints[2].sourceAttributes = c.constraints[2].sourceAttributes[:1]
		},
		"second index member":  func(c *postgresMigrationTableCatalog) { c.indexes[2].keys[1].attributeNumber = 1 },
		"missing index member": func(c *postgresMigrationTableCatalog) { c.indexes[2].keys = c.indexes[2].keys[:1] },
		"reordered index":      func(c *postgresMigrationTableCatalog) { slices.Reverse(c.indexes[2].keys) },
		"second key collation": func(c *postgresMigrationTableCatalog) { c.indexes[2].keys[1].columnCollation = false },
		"second key direction": func(c *postgresMigrationTableCatalog) { c.indexes[2].keys[1].columnOptions = 1 },
		"second key class":     func(c *postgresMigrationTableCatalog) { c.indexes[2].keys[1].operatorClassName = "text_pattern_ops" },
		"second key schema":    func(c *postgresMigrationTableCatalog) { c.indexes[2].keys[1].operatorClassSchema = "public" },
		"second key default":   func(c *postgresMigrationTableCatalog) { c.indexes[2].keys[1].operatorClassDefault = false },
		"second key method":    func(c *postgresMigrationTableCatalog) { c.indexes[2].keys[1].operatorClassMethod = false },
		"vector lengths":       func(c *postgresMigrationTableCatalog) { c.indexes[2].vectorsExact = false },
		"included key":         func(c *postgresMigrationTableCatalog) { c.indexes[2].totalCount++ },
		"nonunique":            func(c *postgresMigrationTableCatalog) { c.indexes[2].unique = false },
		"deferrable":           func(c *postgresMigrationTableCatalog) { c.constraints[2].deferrable = true },
		"nulls not distinct":   func(c *postgresMigrationTableCatalog) { c.indexes[2].nullsNotDistinct = true },
	} {
		t.Run(name, func(t *testing.T) {
			actual := clonePostgresMigrationTestCatalog(exact)
			mutate(&actual)
			if err := assertPostgresMigrationModelCatalog(actual, "product_schema", model, nil); !mb.IsCapabilityError(err) {
				t.Fatal("composite physical drift was accepted", err)
			}
		})
	}
}

func TestPostgresNamedConstraintNamespaceIsSeparateAndBoundToModel(t *testing.T) {
	column, err := postgresUniqueConstraintName("composite_entry", "value")
	if err != nil {
		t.Fatal(err)
	}
	model, err := postgresNamedUniqueConstraintName("composite_entry", "value")
	if err != nil {
		t.Fatal(err)
	}
	other, err := postgresNamedUniqueConstraintName("other_entry", "value")
	if err != nil {
		t.Fatal(err)
	}
	long, err := postgresNamedUniqueConstraintName("composite_entry", strings.Repeat("a", 1000))
	if err != nil {
		t.Fatal("logical name was treated as a truncated physical name", err)
	}
	if column == model || model == other || len(long) > 63 {
		t.Fatal("constraint naming lost namespace separation")
	}
}
