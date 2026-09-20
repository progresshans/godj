package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/internal/uniquetest"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgresUniqueCatalogRequiresExactConstraintAndIndex(t *testing.T) {
	for _, profile := range uniquetest.Profiles(t, "postgres") {
		model, _ := uniquetest.Model(t, profile.Name)
		catalog := postgresMigrationTestCatalog(t, "product_schema", model, nil)
		if err := assertPostgresMigrationModelCatalog(catalog, "product_schema", model, nil); err != nil {
			t.Fatal(profile.Name, err)
		}
	}
	model, _ := uniquetest.Model(t, "char")
	exact := postgresMigrationTestCatalog(t, "product_schema", model, nil)
	for name, mutate := range map[string]func(*postgresMigrationTableCatalog){
		"constraint missing":    func(c *postgresMigrationTableCatalog) { c.constraints = c.constraints[:1] },
		"standalone index":      func(c *postgresMigrationTableCatalog) { c.constraints[1].indexOID++ },
		"deferrable":            func(c *postgresMigrationTableCatalog) { c.constraints[1].deferrable = true },
		"deferred":              func(c *postgresMigrationTableCatalog) { c.constraints[1].deferred = true },
		"unvalidated":           func(c *postgresMigrationTableCatalog) { c.constraints[1].validated = false },
		"foreign target":        func(c *postgresMigrationTableCatalog) { c.constraints[1].targetOID = 1 },
		"constraint duplicate":  func(c *postgresMigrationTableCatalog) { c.constraints[1] = c.constraints[0] },
		"index duplicate":       func(c *postgresMigrationTableCatalog) { c.indexes[1] = c.indexes[0] },
		"undeclared index":      func(c *postgresMigrationTableCatalog) { c.indexes = append(c.indexes, c.indexes[1]) },
		"nonunique":             func(c *postgresMigrationTableCatalog) { c.indexes[1].unique = false },
		"not immediate":         func(c *postgresMigrationTableCatalog) { c.indexes[1].immediate = false },
		"invalid":               func(c *postgresMigrationTableCatalog) { c.indexes[1].valid = false },
		"not ready":             func(c *postgresMigrationTableCatalog) { c.indexes[1].ready = false },
		"not live":              func(c *postgresMigrationTableCatalog) { c.indexes[1].live = false },
		"other column":          func(c *postgresMigrationTableCatalog) { c.indexes[1].firstAttributeNumber = 1 },
		"compound":              func(c *postgresMigrationTableCatalog) { c.indexes[1].keyCount = 2 },
		"included":              func(c *postgresMigrationTableCatalog) { c.indexes[1].totalCount = 2 },
		"partial":               func(c *postgresMigrationTableCatalog) { c.indexes[1].hasPredicate = true },
		"expression":            func(c *postgresMigrationTableCatalog) { c.indexes[1].hasExpressions = true },
		"nulls not distinct":    func(c *postgresMigrationTableCatalog) { c.indexes[1].nullsNotDistinct = true },
		"exclusion":             func(c *postgresMigrationTableCatalog) { c.indexes[1].exclusion = true },
		"non btree":             func(c *postgresMigrationTableCatalog) { c.indexes[1].accessMethod = "hash" },
		"ordering":              func(c *postgresMigrationTableCatalog) { c.indexes[1].columnOptions = 1 },
		"collation":             func(c *postgresMigrationTableCatalog) { c.indexes[1].columnCollation = false },
		"custom opclass schema": func(c *postgresMigrationTableCatalog) { c.indexes[1].operatorClassSchema = "public" },
		"pattern opclass":       func(c *postgresMigrationTableCatalog) { c.indexes[1].operatorClassName = "text_pattern_ops" },
		"nondefault opclass":    func(c *postgresMigrationTableCatalog) { c.indexes[1].operatorClassDefault = false },
		"wrong opclass method":  func(c *postgresMigrationTableCatalog) { c.indexes[1].operatorClassMethod = false },
		"storage options":       func(c *postgresMigrationTableCatalog) { c.indexes[1].options = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			catalog := clonePostgresMigrationTestCatalog(exact)
			mutate(&catalog)
			if err := assertPostgresMigrationModelCatalog(catalog, "product_schema", model, nil); !migrationbackend.IsCapabilityError(err) {
				t.Fatalf("unique catalog drift accepted: %v", err)
			}
		})
	}
}

func TestPostgresUniqueErrorClassificationOwnsOperationAndCause(t *testing.T) {
	cause := &pgconn.PgError{Code: "23505", SchemaName: "product_schema", TableName: "entry", ConstraintName: "entry_external", Detail: "private duplicate value"}
	for _, operation := range []string{"insert", "update"} {
		err := classifyDatabaseError(t.Context(), operation, "product_schema", "entry", cause)
		assertPostgresUniqueError(t, err)
		if !errors.Is(err, cause) || strings.Contains(err.Error(), "private duplicate value") {
			t.Fatal("unique error lost cause or leaked raw details")
		}
	}
	if err := classifyDatabaseError(t.Context(), "query", "product_schema", "entry", cause); errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
		t.Fatal("non-write unique error overclassified")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := classifyDatabaseError(ctx, "insert", "product_schema", "entry", cause); !errors.Is(err, context.Canceled) {
		t.Fatal("unique error displaced cancellation")
	}
}

func TestPostgresUniqueSQLProjectionRequiresPhysicalChanges(t *testing.T) {
	if !(&Backend{}).MigrationCapabilities().UniqueConstraints {
		t.Fatal("uniqueness capability missing")
	}
	plain := postgresMigrationTestPostModel(false)
	unique := plain.Clone()
	unique.Fields[1].Unique = true
	field := ir.Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldUUID, Nullable: true, Unique: true}
	added := plain.Clone()
	added.Fields = append(added.Fields, field)
	for _, operation := range []migrationbackend.MigrationOperation{
		{Kind: migrationbackend.MigrationCreateModel, After: unique},
		{Kind: migrationbackend.MigrationAddField, Before: plain, After: added},
		{Kind: migrationbackend.MigrationAlterField, Before: plain, After: unique},
		{Kind: migrationbackend.MigrationAlterField, Before: unique, After: plain},
	} {
		request := migrationbackend.ForwardMigrationSQLRequest{App: "blog", Name: "0002_unique", Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{operation}}}
		statements, err := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "public"}).RenderForwardMigrationSQL(t.Context(), request)
		if err != nil || len(statements) != 1 || statements[0] == "" {
			t.Fatalf("unique produced incomplete SQL: %v %v", statements, err)
		}
		if operation.Kind == migrationbackend.MigrationAlterField && !operation.After.Fields[1].Unique {
			if !strings.Contains(statements[0], " DROP CONSTRAINT ") || !strings.HasSuffix(statements[0], " RESTRICT") {
				t.Fatal("unique removal lost restrictive DDL")
			}
		} else if !strings.Contains(statements[0], " UNIQUE (") {
			t.Fatal("unique addition lost its constraint")
		}
	}
	for _, compile := range []func() (string, error){
		func() (string, error) { return compilePostgresMigrationCreateModel("public", unique, nil) },
		func() (string, error) { return compilePostgresMigrationAddField("public", plain, field, nil) },
	} {
		statement, err := compile()
		if err != nil || !strings.Contains(statement, " UNIQUE (") {
			t.Fatalf("direct compiler discarded unique: %q %v", statement, err)
		}
	}
}

func TestPostgresIntentSealIncludesUnique(t *testing.T) {
	model := postgresMigrationTestPostModel(false)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{Kind: migrationbackend.MigrationCreateModel, After: model}}}
	schema, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), intent)
	if err != nil {
		t.Fatal(err)
	}
	schema.intent.Operations[0].After.Fields[1].Unique = true
	assertPostgresMigrationIntegrity(t, schema.verifySeal())
}
