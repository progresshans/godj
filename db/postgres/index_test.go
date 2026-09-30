package postgres

import (
	"strings"
	"testing"

	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func postgresIndexTestModel(t *testing.T) ir.Model {
	t.Helper()
	s, err := schema.Build(schema.Definition{AppLabel: "indexref", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.SlugField("address", "Address", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	return s.Models[0]
}

func TestPostgresColumnIndexSQLAndNamesOwnExactTransitions(t *testing.T) {
	indexed := postgresIndexTestModel(t)
	plain, unique, unicode := indexed.Clone(), indexed.Clone(), indexed.Clone()
	plain.Fields[1].Kind, plain.Fields[1].DBIndex = ir.FieldChar, false
	unique.Fields[1].Unique, unicode.Fields[1].AllowUnicode = true, true
	for _, test := range []struct {
		before, after ir.Model
		prefixes      []string
	}{
		{plain, indexed, []string{`CREATE INDEX `}}, {indexed, plain, []string{`DROP INDEX "product_schema".`}},
		{indexed, unique, []string{`DROP INDEX "product_schema".`, `ALTER TABLE "product_schema".`}},
		{unique, indexed, []string{`ALTER TABLE "product_schema".`, `CREATE INDEX `}}, {indexed, unicode, nil},
	} {
		groups, err := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"}).RenderForwardMigrationSQL(t.Context(), mb.ForwardMigrationSQLRequest{App: "indexref", Name: "0002_change", Intent: mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationAlterField, Before: test.before, After: test.after}}}})
		if err != nil || len(groups) != 1 || len(groups[0]) != len(test.prefixes) {
			t.Fatal("index transition lost physical operation group", groups, err)
		}
		for i, prefix := range test.prefixes {
			if !strings.HasPrefix(groups[0][i], prefix) {
				t.Fatal("wrong index transition order", groups)
			}
			if strings.HasPrefix(prefix, "DROP INDEX") && !strings.HasSuffix(groups[0][i], " RESTRICT") {
				t.Fatal("index removal lost restrictive ownership")
			}
		}
	}
	for _, model := range []ir.Model{indexed, unique} {
		statements, err := compilePostgresMigrationCreateModel("product_schema", model, nil)
		want := 2
		if model.Fields[1].Unique {
			want = 1
		}
		if err != nil || len(statements) != want {
			t.Fatal("index omitted or redundant beside unique", statements, err)
		}
	}
	names := map[string]bool{}
	for _, pair := range [][2]string{{"a_b", "c"}, {"a", "b_c"}, {"a", "c"}, {"b", "c"}} {
		name, err := postgresColumnIndexName(pair[0], pair[1])
		uq, uqErr := postgresUniqueConstraintName(pair[0], pair[1])
		again, againErr := postgresColumnIndexName(pair[0], pair[1])
		if err != nil || uqErr != nil || againErr != nil || name != again || len(name) > 63 || name == uq || names[name] {
			t.Fatal("index identities collide", name, err)
		}
		names[name] = true
	}
	collision := plain.Clone()
	collision.Name, collision.GoName = "collision", "Collision"
	var err error
	collision.DBTable, err = postgresColumnIndexName(indexed.DBTable, indexed.Fields[1].Column)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"}).RenderForwardMigrationSQL(t.Context(), mb.ForwardMigrationSQLRequest{App: "indexref", Name: "0001_collision", Intent: mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationCreateModel, After: indexed}, {OperationIndex: 1, Kind: mb.MigrationCreateModel, After: collision}}}}); err == nil || result != nil {
		t.Fatal("table captured an index name")
	}
}

func TestPostgresColumnIndexCatalogAndSealRejectForgedPolicy(t *testing.T) {
	model := postgresIndexTestModel(t)
	exact := postgresMigrationTestCatalog(t, "product_schema", model, nil)
	if err := assertPostgresMigrationModelCatalog(exact, "product_schema", model, nil); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*postgresMigrationTableCatalog){
		"missing":            func(c *postgresMigrationTableCatalog) { c.indexes = c.indexes[:1] },
		"duplicate":          func(c *postgresMigrationTableCatalog) { c.indexes = append(c.indexes, c.indexes[1]) },
		"identity":           func(c *postgresMigrationTableCatalog) { c.indexes[1].oid = 0 },
		"name":               func(c *postgresMigrationTableCatalog) { c.indexes[1].name += "x" },
		"primary":            func(c *postgresMigrationTableCatalog) { c.indexes[1].primary = true },
		"unique":             func(c *postgresMigrationTableCatalog) { c.indexes[1].unique = true },
		"immediate":          func(c *postgresMigrationTableCatalog) { c.indexes[1].immediate = false },
		"invalid":            func(c *postgresMigrationTableCatalog) { c.indexes[1].valid = false },
		"not_ready":          func(c *postgresMigrationTableCatalog) { c.indexes[1].ready = false },
		"not_live":           func(c *postgresMigrationTableCatalog) { c.indexes[1].live = false },
		"vectors":            func(c *postgresMigrationTableCatalog) { c.indexes[1].vectorsExact = false },
		"compound":           func(c *postgresMigrationTableCatalog) { c.indexes[1].keyCount = 2 },
		"included":           func(c *postgresMigrationTableCatalog) { c.indexes[1].totalCount = 2 },
		"partial":            func(c *postgresMigrationTableCatalog) { c.indexes[1].hasPredicate = true },
		"expression":         func(c *postgresMigrationTableCatalog) { c.indexes[1].hasExpressions = true },
		"null_policy":        func(c *postgresMigrationTableCatalog) { c.indexes[1].nullsNotDistinct = true },
		"exclusion":          func(c *postgresMigrationTableCatalog) { c.indexes[1].exclusion = true },
		"hash":               func(c *postgresMigrationTableCatalog) { c.indexes[1].accessMethod = "hash" },
		"options":            func(c *postgresMigrationTableCatalog) { c.indexes[1].options = 1 },
		"other_column":       func(c *postgresMigrationTableCatalog) { c.indexes[1].keys[0].attributeNumber = 1 },
		"descending":         func(c *postgresMigrationTableCatalog) { c.indexes[1].keys[0].columnOptions = 1 },
		"collation":          func(c *postgresMigrationTableCatalog) { c.indexes[1].keys[0].columnCollation = false },
		"opclass_schema":     func(c *postgresMigrationTableCatalog) { c.indexes[1].keys[0].operatorClassSchema = "public" },
		"pattern_opclass":    func(c *postgresMigrationTableCatalog) { c.indexes[1].keys[0].operatorClassName = "text_pattern_ops" },
		"nondefault_opclass": func(c *postgresMigrationTableCatalog) { c.indexes[1].keys[0].operatorClassDefault = false },
		"opclass_method":     func(c *postgresMigrationTableCatalog) { c.indexes[1].keys[0].operatorClassMethod = false },
	} {
		t.Run(name, func(t *testing.T) {
			forged := clonePostgresMigrationTestCatalog(exact)
			mutate(&forged)
			if err := assertPostgresMigrationModelCatalog(forged, "product_schema", model, nil); !mb.IsCapabilityError(err) {
				t.Fatal("forged ordinary index accepted", err)
			}
		})
	}
	for _, mutate := range []func(*ir.Field){func(f *ir.Field) { f.DBIndex = false }, func(f *ir.Field) { f.AllowUnicode = true }} {
		sealed, err := newPostgresMigrationSchema(mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: "indexref", Name: "0001_initial"}}, mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationCreateModel, After: model}}})
		if err != nil {
			t.Fatal(err)
		}
		mutate(&sealed.intent.Operations[0].After.Fields[1])
		assertPostgresMigrationIntegrity(t, sealed.verifySeal())
	}
}
