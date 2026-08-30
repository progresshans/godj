//go:build darwin || linux

package projectsqlmigrateproduct_test

const (
	sqlProductCatalogEnvironment        = "GODJ_SQLMIGRATE_PRODUCT_CATALOG"
	sqlProductRendererEnvironment       = "GODJ_SQLMIGRATE_PRODUCT_RENDERER"
	sqlProductInitMarkerEnvironment     = "GODJ_SQLMIGRATE_PRODUCT_INIT_MARKER"
	sqlProductRendererMarkerEnvironment = "GODJ_SQLMIGRATE_PRODUCT_RENDERER_MARKER"
	sqlProductOpenerMarkerEnvironment   = "GODJ_SQLMIGRATE_PRODUCT_OPENER_MARKER"
	sqlProductDatabaseEnvironment       = "GODJ_SQLMIGRATE_PRODUCT_DATABASE"
	sqlProductSecretEnvironment         = "GODJ_SQLMIGRATE_PRODUCT_SECRET"

	sqlProductCatalogFull    = "full"
	sqlProductCatalogInvalid = "invalid"

	sqlProductRendererSQLite = "sqlite"
	sqlProductRendererFail   = "fail"
	sqlProductRendererNil    = "nil"

	sqlProductPartialCanary = "PARTIAL_SQL_MUST_NOT_BE_PUBLISHED_1f9da742"
)

const sqlProductRunnerSource = `package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/project"
	"github.com/progresshans/godj/schema/ir"
)

const (
	catalogEnvironment = "GODJ_SQLMIGRATE_PRODUCT_CATALOG"
	rendererEnvironment = "GODJ_SQLMIGRATE_PRODUCT_RENDERER"
	initMarkerEnvironment = "GODJ_SQLMIGRATE_PRODUCT_INIT_MARKER"
	rendererMarkerEnvironment = "GODJ_SQLMIGRATE_PRODUCT_RENDERER_MARKER"
	openerMarkerEnvironment = "GODJ_SQLMIGRATE_PRODUCT_OPENER_MARKER"
	databaseEnvironment = "GODJ_SQLMIGRATE_PRODUCT_DATABASE"
	secretEnvironment = "GODJ_SQLMIGRATE_PRODUCT_SECRET"
	partialSQLCanary = "PARTIAL_SQL_" + "MUST_NOT_BE_PUBLISHED_1f9da742"
)

func init() {
	path := os.Getenv(initMarkerEnvironment)
	if path != "" {
		if err := appendMarker(path, "init"); err != nil {
			fatal()
		}
	}
}

func main() {
	sources, err := sourcesForCatalog(os.Getenv(catalogEnvironment))
	if err != nil {
		fatal()
	}
	err = project.Run(context.Background(), project.Config{
		MigrationDefinitionSources: sources,
		OpenMigrationBackend: poisonOpenMigrationBackend,
		MigrationSQLRenderer: rendererForMode(os.Getenv(rendererEnvironment)),
	}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		fatal()
	}
}

func poisonOpenMigrationBackend(context.Context) (project.MigrationBackend, error) {
	if err := appendMarker(os.Getenv(openerMarkerEnvironment), "opener"); err != nil {
		return nil, err
	}
	if err := os.WriteFile(os.Getenv(databaseEnvironment), []byte(os.Getenv(secretEnvironment)), 0600); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("poison migration opener called: %s %s", os.Getenv(secretEnvironment), os.Getenv(databaseEnvironment))
}

func rendererForMode(mode string) backend.MigrationSQLRenderer {
	switch mode {
	case "sqlite":
		return observedRenderer{delegate: sqlite.NewMigrationSQLRenderer()}
	case "fail":
		return failingRenderer{}
	case "nil":
		return nil
	default:
		return failingRenderer{}
	}
}

type observedRenderer struct {
	delegate backend.MigrationSQLRenderer
}

func (renderer observedRenderer) RenderForwardMigrationSQL(
	ctx context.Context,
	request backend.ForwardMigrationSQLRequest,
) ([]string, error) {
	if err := appendMarker(os.Getenv(rendererMarkerEnvironment), "render"); err != nil {
		return nil, err
	}
	return renderer.delegate.RenderForwardMigrationSQL(ctx, request)
}

type failingRenderer struct{}

func (failingRenderer) RenderForwardMigrationSQL(
	context.Context,
	backend.ForwardMigrationSQLRequest,
) ([]string, error) {
	if err := appendMarker(os.Getenv(rendererMarkerEnvironment), "render"); err != nil {
		return nil, err
	}
	partial := partialSQLCanary + " " + os.Getenv(secretEnvironment) + " " + os.Getenv(databaseEnvironment)
	return []string{partial}, fmt.Errorf("injected renderer failure: %s %s", os.Getenv(secretEnvironment), os.Getenv(databaseEnvironment))
}

func sourcesForCatalog(catalog string) ([]definition.Source, error) {
	definitions := fullCatalog()
	sources := make([]definition.Source, len(definitions), len(definitions)+1)
	for index, migration := range definitions {
		document, err := definition.Encode(definition.Producer{Name: "sqlmigrate-product", Version: "1"}, migration)
		if err != nil {
			return nil, err
		}
		sources[index] = definition.Source{
			SourceID: fmt.Sprintf("generated/%02d_%s_%s.godj.json", index, migration.App, migration.Name),
			Document: document,
		}
	}
	switch catalog {
	case "full":
		return sources, nil
	case "invalid":
		return append(sources, definition.Source{
			SourceID: "generated/99_unrelated_invalid.godj.json",
			Document: []byte("{"),
		}), nil
	default:
		return nil, errors.New("unknown SQL migration product catalog")
	}
}

func fullCatalog() []migrations.Migration {
	author := normalizedModel("authors", ir.Model{
		Name: "author", GoName: "Author", DBTable: "authors_author",
		Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 100}},
	})
	article := normalizedModel("blog", ir.Model{
		Name: "article", GoName: "Article", DBTable: "blog_article",
		Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}},
	})
	return []migrations.Migration{
		{
			App: "authors", Name: "0001_author",
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "authors", Model: author}},
		},
		{
			App: "blog", Name: "0001_article",
			Dependencies: []migrations.MigrationKey{{App: "authors", Name: "0001_author"}},
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "blog", Model: article}},
		},
		{
			App: "blog", Name: "0002_enrich",
			Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_article"}},
			Operations: []migrations.Operation{
				migrations.AddField{
					AppLabel: "blog", ModelName: "article",
					Field: ir.Field{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, MaxLength: 120, Nullable: true},
				},
				migrations.AddField{
					AppLabel: "blog", ModelName: "article",
					Field: ir.Field{
						Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean,
						Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean},
					},
				},
			},
		},
		{
			App: "blog", Name: "zero",
			Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0002_enrich"}},
			Operations: make([]migrations.Operation, 0),
		},
	}
}

func normalizedModel(app string, model ir.Model) ir.Model {
	schema, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel: app,
		Models: []ir.Model{model},
	})
	if err != nil {
		panic("invalid static SQL migration product model")
	}
	return schema.Models[0]
}

func appendMarker(path, event string) error {
	if path == "" {
		return errors.New("SQL migration product marker path is empty")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "%d\t%s\n", os.Getpid(), event)
	return errors.Join(writeErr, file.Close())
}

func fatal() {
	_, _ = fmt.Fprintln(os.Stderr, "external SQL migration project failed")
	os.Exit(1)
}
`
