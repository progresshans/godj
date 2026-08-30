//go:build darwin || linux

package projectmigratetargetproduct_test

import "fmt"

const (
	targetDatabaseEnvironment         = "GODJ_TARGET_MIGRATE_SQLITE_DATABASE"
	targetCatalogEnvironment          = "GODJ_TARGET_MIGRATE_CATALOG"
	targetMarkerEnvironment           = "GODJ_TARGET_MIGRATE_MARKER"
	targetSecretEnvironment           = "GODJ_TARGET_MIGRATE_SECRET_CANARY"
	targetFailDeleteTableEnvironment  = "GODJ_TARGET_MIGRATE_FAIL_DELETE_TABLE"
	targetFailBackendOpenEnvironment  = "GODJ_TARGET_MIGRATE_FAIL_BACKEND_OPEN"
	targetFailBackendCloseEnvironment = "GODJ_TARGET_MIGRATE_FAIL_BACKEND_CLOSE"

	targetCatalogBranch         = "branch"
	targetCatalogZero           = "zero"
	targetCatalogBlog           = "blog"
	targetCatalogReverseFailure = "reverse_failure"
	targetCatalogInvalid        = "invalid"

	targetAlphaApp   = "alpha"
	targetBetaApp    = "beta"
	targetCharlieApp = "charlie"
	targetGammaApp   = "gamma"
	targetBlogApp    = "blog"
	targetFailureApp = "failure"

	targetAlpha1   = "0001_initial"
	targetAlpha2   = "0002_second"
	targetAlpha3   = "0003_third"
	targetBeta1    = "0001_direct_dependent"
	targetCharlie1 = "0001_descendant_dependent"
	targetGamma1   = "0001_unrelated"
	targetBlog1    = "0001_initial"
	targetBlog2    = "0002_editor"
	targetFailure1 = "0001_initial"
	targetFailure2 = "0002_second"
	targetFailure3 = "0003_middle"
	targetFailure4 = "0004_tail"

	targetAlpha1Table         = "target_alpha_initial"
	targetAlpha2Table         = "target_alpha_second"
	targetAlpha3Table         = "target_alpha_third"
	targetBeta1Table          = "target_beta_direct"
	targetCharlie1Table       = "target_charlie_descendant"
	targetGamma1Table         = "target_gamma_unrelated"
	targetBlog1Table          = "target_blog_initial"
	targetBlog2Table          = "target_blog_editor"
	targetFailure1Table       = "target_failure_initial"
	targetFailure2Table       = "target_failure_second"
	targetFailure3FirstTable  = "target_failure_middle_first"
	targetFailure3SecondTable = "target_failure_middle_second"
	targetFailure4Table       = "target_failure_tail"
)

const (
	targetEventBackendOpen  = "backend_open"
	targetEventSessionOpen  = "session_open"
	targetEventHistoryRead  = "history_read"
	targetEventSessionClose = "session_close"
	targetEventBackendClose = "backend_close"
)

func targetBeginEvent(app, name, direction string) string {
	return fmt.Sprintf("begin %s.%s %s", app, name, direction)
}

func targetCreateEvent(table string) string {
	return "create " + table
}

func targetDeleteEvent(table string) string {
	return "delete " + table
}

func targetRecordAppliedEvent(app, name string) string {
	return fmt.Sprintf("record_applied %s.%s", app, name)
}

func targetRecordUnappliedEvent(app, name string) string {
	return fmt.Sprintf("record_unapplied %s.%s", app, name)
}

func targetCommitEvent(app, name, direction string) string {
	return fmt.Sprintf("commit %s.%s %s", app, name, direction)
}

func targetRollbackEvent(app, name, direction string) string {
	return fmt.Sprintf("rollback %s.%s %s", app, name, direction)
}

const targetProjectRunnerSource = `package main

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
	databaseEnvironment = "GODJ_TARGET_MIGRATE_SQLITE_DATABASE"
	catalogEnvironment = "GODJ_TARGET_MIGRATE_CATALOG"
	markerEnvironment = "GODJ_TARGET_MIGRATE_MARKER"
	secretEnvironment = "GODJ_TARGET_MIGRATE_SECRET_CANARY"
	failDeleteTableEnvironment = "GODJ_TARGET_MIGRATE_FAIL_DELETE_TABLE"
	failBackendOpenEnvironment = "GODJ_TARGET_MIGRATE_FAIL_BACKEND_OPEN"
	failBackendCloseEnvironment = "GODJ_TARGET_MIGRATE_FAIL_BACKEND_CLOSE"
)

func main() {
	sources, err := sourcesForCatalog(os.Getenv(catalogEnvironment))
	if err != nil {
		fatal()
	}
	err = project.Run(context.Background(), project.Config{
		MigrationDefinitionSources: sources,
		OpenMigrationBackend: openObservedBackend,
	}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		fatal()
	}
}

func openObservedBackend(ctx context.Context) (project.MigrationBackend, error) {
	if err := appendMarker("backend_open"); err != nil {
		return nil, err
	}
	if os.Getenv(failBackendOpenEnvironment) == "1" {
		return nil, fmt.Errorf("injected backend open failure: %s %s", os.Getenv(secretEnvironment), os.Getenv(databaseEnvironment))
	}
	opened, err := sqlite.Open(ctx, os.Getenv(databaseEnvironment))
	if err != nil {
		return nil, err
	}
	return &observedBackend{MigrationBackend: opened}, nil
}

type observedBackend struct {
	project.MigrationBackend
}

func (observed *observedBackend) OpenRevisionFencedSession(ctx context.Context) (backend.RevisionFencedSession, error) {
	if err := appendMarker("session_open"); err != nil {
		return nil, err
	}
	session, err := observed.MigrationBackend.OpenRevisionFencedSession(ctx)
	if session == nil {
		return nil, err
	}
	return &observedSession{RevisionFencedSession: session}, err
}

func (observed *observedBackend) Close() error {
	markerErr := appendMarker("backend_close")
	closeErr := observed.MigrationBackend.Close()
	if os.Getenv(failBackendCloseEnvironment) == "1" {
		closeErr = errors.Join(closeErr, fmt.Errorf("injected backend close failure: %s %s", os.Getenv(secretEnvironment), os.Getenv(databaseEnvironment)))
	}
	return errors.Join(closeErr, markerErr)
}

type observedSession struct {
	backend.RevisionFencedSession
}

func (observed *observedSession) ReadAppliedMigrations(ctx context.Context) ([]backend.AppliedMigration, error) {
	if err := appendMarker("history_read"); err != nil {
		return nil, err
	}
	return observed.RevisionFencedSession.ReadAppliedMigrations(ctx)
}

func (observed *observedSession) BeginMigration(ctx context.Context, transition backend.HistoryTransition, intent backend.MigrationIntent) (backend.RevisionFencedTransaction, error) {
	direction := transitionDirection(transition.Kind)
	if err := appendMarker(fmt.Sprintf("begin %s.%s %s", transition.Migration.App, transition.Migration.Name, direction)); err != nil {
		return nil, err
	}
	transaction, err := observed.RevisionFencedSession.BeginMigration(ctx, transition, intent)
	if transaction == nil {
		return nil, err
	}
	return &observedTransaction{
		RevisionFencedTransaction: transaction,
		app: transition.Migration.App,
		name: transition.Migration.Name,
		direction: direction,
	}, err
}

func (observed *observedSession) Close(ctx context.Context) error {
	return errors.Join(observed.RevisionFencedSession.Close(ctx), appendMarker("session_close"))
}

type observedTransaction struct {
	backend.RevisionFencedTransaction
	app string
	name string
	direction string
}

func (observed *observedTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	if err := appendMarker("create " + model.DBTable); err != nil {
		return err
	}
	return observed.RevisionFencedTransaction.CreateModel(ctx, model)
}

func (observed *observedTransaction) DeleteModel(ctx context.Context, model ir.Model) error {
	if err := appendMarker("delete " + model.DBTable); err != nil {
		return err
	}
	if os.Getenv(failDeleteTableEnvironment) == model.DBTable {
		return fmt.Errorf("injected delete failure: %s %s", os.Getenv(secretEnvironment), os.Getenv(databaseEnvironment))
	}
	return observed.RevisionFencedTransaction.DeleteModel(ctx, model)
}

func (observed *observedTransaction) RecordApplied(ctx context.Context, app, name string) error {
	if err := appendMarker(fmt.Sprintf("record_applied %s.%s", app, name)); err != nil {
		return err
	}
	return observed.RevisionFencedTransaction.RecordApplied(ctx, app, name)
}

func (observed *observedTransaction) RecordUnapplied(ctx context.Context, app, name string) error {
	if err := appendMarker(fmt.Sprintf("record_unapplied %s.%s", app, name)); err != nil {
		return err
	}
	return observed.RevisionFencedTransaction.RecordUnapplied(ctx, app, name)
}

func (observed *observedTransaction) CommitFenced(ctx context.Context) (backend.CommitOutcome, error) {
	if err := appendMarker(fmt.Sprintf("commit %s.%s %s", observed.app, observed.name, observed.direction)); err != nil {
		return backend.CommitOutcome{}, err
	}
	return observed.RevisionFencedTransaction.CommitFenced(ctx)
}

func (observed *observedTransaction) Rollback(ctx context.Context) error {
	return errors.Join(
		appendMarker(fmt.Sprintf("rollback %s.%s %s", observed.app, observed.name, observed.direction)),
		observed.RevisionFencedTransaction.Rollback(ctx),
	)
}

func transitionDirection(kind backend.HistoryTransitionKind) string {
	switch kind {
	case backend.HistoryTransitionApply:
		return "forward"
	case backend.HistoryTransitionUnapply:
		return "backward"
	default:
		return "invalid"
	}
}

func appendMarker(event string) error {
	path := os.Getenv(markerEnvironment)
	if path == "" {
		return errors.New("migration marker path is empty")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "%d\t%s\n", os.Getpid(), event)
	return errors.Join(writeErr, file.Close())
}

func sourcesForCatalog(catalog string) ([]definition.Source, error) {
	if catalog == "invalid" {
		return []definition.Source{{SourceID: "invalid.godj.json", Document: []byte("{")}}, nil
	}
	var definitions []migrations.Migration
	switch catalog {
	case "branch":
		definitions = branchCatalog(true)
	case "zero":
		definitions = branchCatalog(false)
	case "blog":
		definitions = blogCatalog()
	case "reverse_failure":
		definitions = reverseFailureCatalog()
	default:
		return nil, errors.New("unknown target migration catalog")
	}
	sources := make([]definition.Source, len(definitions))
	for index, migration := range definitions {
		document, err := definition.Encode(definition.Producer{Name: "target-migrate-product", Version: "1"}, migration)
		if err != nil {
			return nil, err
		}
		sources[index] = definition.Source{
			SourceID: fmt.Sprintf("generated/%02d_%s_%s.godj.json", index, migration.App, migration.Name),
			Document: document,
		}
	}
	return sources, nil
}

func branchCatalog(includeDescendant bool) []migrations.Migration {
	alpha1 := migration("alpha", "0001_initial", nil, model("alpha", "initial", "Initial", "target_alpha_initial"))
	alpha2 := migration("alpha", "0002_second", []migrations.MigrationKey{alpha1.Key()}, model("alpha", "second", "Second", "target_alpha_second"))
	alpha3 := migration("alpha", "0003_third", []migrations.MigrationKey{alpha2.Key()}, model("alpha", "third", "Third", "target_alpha_third"))
	beta1 := migration("beta", "0001_direct_dependent", []migrations.MigrationKey{alpha1.Key()}, model("beta", "direct", "Direct", "target_beta_direct"))
	gamma1 := migration("gamma", "0001_unrelated", nil, model("gamma", "unrelated", "Unrelated", "target_gamma_unrelated"))
	result := []migrations.Migration{alpha1, alpha2, alpha3, beta1}
	if includeDescendant {
		result = append(result, migration("charlie", "0001_descendant_dependent", []migrations.MigrationKey{alpha3.Key()}, model("charlie", "descendant", "Descendant", "target_charlie_descendant")))
	}
	return append(result, gamma1)
}

func blogCatalog() []migrations.Migration {
	initial := migration("blog", "0001_initial", nil, model("blog", "initial", "Initial", "target_blog_initial"))
	editor := migration("blog", "0002_editor", []migrations.MigrationKey{initial.Key()}, model("blog", "editor", "Editor", "target_blog_editor"))
	return []migrations.Migration{initial, editor}
}

func reverseFailureCatalog() []migrations.Migration {
	initial := migration("failure", "0001_initial", nil, model("failure", "initial", "Initial", "target_failure_initial"))
	second := migration("failure", "0002_second", []migrations.MigrationKey{initial.Key()}, model("failure", "second", "Second", "target_failure_second"))
	middle := migrations.Migration{
		App: "failure", Name: "0003_middle",
		Dependencies: []migrations.MigrationKey{second.Key()},
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "failure", Model: model("failure", "middle_first", "MiddleFirst", "target_failure_middle_first")},
			migrations.CreateModel{AppLabel: "failure", Model: model("failure", "middle_second", "MiddleSecond", "target_failure_middle_second")},
		},
	}
	tail := migration("failure", "0004_tail", []migrations.MigrationKey{middle.Key()}, model("failure", "tail", "Tail", "target_failure_tail"))
	return []migrations.Migration{initial, second, middle, tail}
}

func migration(app, name string, dependencies []migrations.MigrationKey, value ir.Model) migrations.Migration {
	return migrations.Migration{
		App: app, Name: name,
		Dependencies: append([]migrations.MigrationKey(nil), dependencies...),
		Operations: []migrations.Operation{migrations.CreateModel{AppLabel: app, Model: value}},
	}
}

func model(app, name, goName, table string) ir.Model {
	schema, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel: app,
		Models: []ir.Model{{
			Name: name, GoName: goName, DBTable: table,
			Fields: []ir.Field{{Name: "value", GoName: "Value", Kind: ir.FieldChar, MaxLength: 128}},
		}},
	})
	if err != nil {
		panic("invalid static target migration model")
	}
	return schema.Models[0]
}

func fatal() {
	_, _ = fmt.Fprintln(os.Stderr, "external target migration project failed")
	os.Exit(1)
}
`
