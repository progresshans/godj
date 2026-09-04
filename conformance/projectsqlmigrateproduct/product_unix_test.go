//go:build darwin || linux

package projectsqlmigrateproduct_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

const (
	sqlProductAuthorOutput  = "CREATE TABLE \"authors_author\" (\"id\" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, \"name\" VARCHAR(100) NOT NULL);\n"
	sqlProductArticleOutput = "CREATE TABLE \"blog_article\" (\"id\" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, \"title\" VARCHAR(200) NOT NULL);\n"
	sqlProductEnrichOutput  = "ALTER TABLE \"blog_article\" ADD COLUMN \"summary\" VARCHAR(120) NULL;\n" +
		"ALTER TABLE \"blog_article\" ADD COLUMN \"published\" BOOLEAN NOT NULL;\n"
)

func TestGlobalSQLMigrateExternalSQLiteProduct(t *testing.T) {
	project := newSQLProductProject(t)

	t.Run("exact implicit and explicit argv publish compiler SQL", func(t *testing.T) {
		implicitState := project.state(t, "exact-implicit")
		implicitEnvironment := project.environment(implicitState, sqlProductCatalogFull, sqlProductRendererSQLite)
		implicit := project.run(t, implicitState, implicitEnvironment, "sqlmigrate", "blog", "0002_enrich")
		sqlProductAssertSuccess(t, implicit, sqlProductEnrichOutput, project.sensitive(implicitState)...)
		sqlProductAssertMarker(t, implicitState.initMarker, "init")
		sqlProductAssertMarker(t, implicitState.rendererMarker, "render")

		explicitState := project.state(t, "exact-explicit")
		explicitEnvironment := project.environment(explicitState, sqlProductCatalogFull, sqlProductRendererSQLite)
		explicit := project.runExplicit(t, explicitState, explicitEnvironment, "blog", "0002_enrich")
		sqlProductAssertSuccess(t, explicit, sqlProductEnrichOutput, project.sensitive(explicitState)...)
		sqlProductAssertMarker(t, explicitState.initMarker, "init")
		sqlProductAssertMarker(t, explicitState.rendererMarker, "render")
		if implicit.stdout != explicit.stdout {
			t.Fatalf("implicit and explicit SQL differ: implicit=%q explicit=%q", implicit.stdout, explicit.stdout)
		}

		authorState := project.state(t, "create-author")
		authorEnvironment := project.environment(authorState, sqlProductCatalogFull, sqlProductRendererSQLite)
		author := project.runExplicit(t, authorState, authorEnvironment, "authors", "0001_author")
		sqlProductAssertSuccess(t, author, sqlProductAuthorOutput, project.sensitive(authorState)...)
		sqlProductAssertMarker(t, authorState.initMarker, "init")
		sqlProductAssertMarker(t, authorState.rendererMarker, "render")

		articleState := project.state(t, "create-article")
		articleEnvironment := project.environment(articleState, sqlProductCatalogFull, sqlProductRendererSQLite)
		article := project.runExplicit(t, articleState, articleEnvironment, "blog", "0001_article")
		sqlProductAssertSuccess(t, article, sqlProductArticleOutput, project.sensitive(articleState)...)
		sqlProductAssertMarker(t, articleState.initMarker, "init")
		sqlProductAssertMarker(t, articleState.rendererMarker, "render")
	})

	t.Run("literal zero is an exact empty migration", func(t *testing.T) {
		state := project.state(t, "literal-zero")
		environment := project.environment(state, sqlProductCatalogFull, sqlProductRendererSQLite)
		result := project.runExplicit(t, state, environment, "blog", "zero")
		sqlProductAssertSuccess(t, result, "", project.sensitive(state)...)
		if len(result.stdout) != 0 {
			t.Fatalf("literal zero migration stdout bytes = %d, want 0", len(result.stdout))
		}
		sqlProductAssertMarker(t, state.initMarker, "init")
		sqlProductAssertMarker(t, state.rendererMarker, "render")
	})

	t.Run("prefix-looking name is not an exact target", func(t *testing.T) {
		state := project.state(t, "prefix-miss")
		environment := project.environment(state, sqlProductCatalogFull, sqlProductRendererSQLite)
		result := project.runExplicit(t, state, environment, "blog", "0002")
		sqlProductAssertFailure(t, result, 1, "migration_plan_error/target_not_found\n", project.sensitive(state)...)
		sqlProductAssertMarker(t, state.initMarker, "init")
		sqlProductAssertMarkerAbsent(t, state.rendererMarker)
	})

	t.Run("complete catalog validation precedes target renderer", func(t *testing.T) {
		state := project.state(t, "invalid-unrelated-source")
		environment := project.environment(state, sqlProductCatalogInvalid, sqlProductRendererFail)
		result := project.runExplicit(t, state, environment, "authors", "0001_author")
		sqlProductAssertFailure(t, result, 1, "migration_definition_source_error/invalid_definition_document\n", project.sensitive(state)...)
		sqlProductAssertMarker(t, state.initMarker, "init")
		sqlProductAssertMarkerAbsent(t, state.rendererMarker)
	})

	t.Run("renderer failure and partial SQL are redacted", func(t *testing.T) {
		state := project.state(t, "renderer-failure")
		environment := project.environment(state, sqlProductCatalogFull, sqlProductRendererFail)
		result := project.runExplicit(t, state, environment, "authors", "0001_author")
		sqlProductAssertFailure(t, result, 3, "migration_sql_render_error/render_failed\n", project.sensitive(state)...)
		sqlProductAssertMarker(t, state.initMarker, "init")
		sqlProductAssertMarker(t, state.rendererMarker, "render")
	})

	t.Run("nil renderer fails after exact materialization", func(t *testing.T) {
		state := project.state(t, "nil-renderer")
		environment := project.environment(state, sqlProductCatalogFull, sqlProductRendererNil)
		result := project.runExplicit(t, state, environment, "authors", "0001_author")
		sqlProductAssertFailure(t, result, 3, "migration_sql_render_error/renderer_unavailable\n", project.sensitive(state)...)
		sqlProductAssertMarker(t, state.initMarker, "init")
		sqlProductAssertMarkerAbsent(t, state.rendererMarker)
	})

	t.Run("invalid argv precedes project build and init", func(t *testing.T) {
		invalid := [][]string{
			{"sqlmigrate", "blog"},
			{"sqlmigrate", "blog", "0002_enrich", "--plan"},
			{"sqlmigrate", "blog", "0002_enrich", "--project"},
			{"sqlmigrate", "blog", "0002_enrich", "--project", "-descriptor"},
			{"sqlmigrate", "blog", "latest", "--project", project.poisonDescriptor},
			{"sqlmigrate", "-blog", "0002_enrich", "--project", project.poisonDescriptor},
			{"sqlmigrate", "blog", "-0002_enrich", "--project", project.poisonDescriptor},
			{"sqlmigrate", "blog", "0002_enrich", "--reverse", "--project", project.poisonDescriptor},
			{"sqlmigrate", "blog", "0002_enrich", "--project", project.poisonDescriptor, "--plan"},
		}
		for index, arguments := range invalid {
			state := project.state(t, fmt.Sprintf("invalid-argv-%02d", index))
			environment := project.environment(state, sqlProductCatalogFull, sqlProductRendererSQLite)
			result := project.runAt(t, state, project.unselected, environment, arguments...)
			sqlProductAssertFailure(t, result, 2, "migration_project_command_error/invalid_arguments\n", project.sensitive(state)...)
			sqlProductAssertMarkerAbsent(t, state.initMarker)
			sqlProductAssertMarkerAbsent(t, state.rendererMarker)
		}
	})

	t.Run("fresh process repetitions are byte deterministic", func(t *testing.T) {
		const repetitions = 3
		pids := make(map[int]struct{}, repetitions)
		for index := 0; index < repetitions; index++ {
			state := project.state(t, fmt.Sprintf("repeat-%02d", index))
			environment := project.environment(state, sqlProductCatalogFull, sqlProductRendererSQLite)
			result := project.runExplicit(t, state, environment, "blog", "0002_enrich")
			sqlProductAssertSuccess(t, result, sqlProductEnrichOutput, project.sensitive(state)...)
			pids[sqlProductAssertMarker(t, state.initMarker, "init")] = struct{}{}
			sqlProductAssertMarker(t, state.rendererMarker, "render")
		}
		if len(pids) != repetitions {
			t.Fatalf("fresh process repetitions used %d distinct runner PIDs, want %d", len(pids), repetitions)
		}
	})

	t.Run("bounded parallel invocations are byte deterministic", func(t *testing.T) {
		const parallel = 4
		states := make([]sqlProductState, parallel)
		environments := make([][]string, parallel)
		results := make([]sqlProductResult, parallel)
		errors := make([]error, parallel)
		var group sync.WaitGroup
		for index := 0; index < parallel; index++ {
			states[index] = project.state(t, fmt.Sprintf("parallel-%02d", index))
			environments[index] = project.environment(states[index], sqlProductCatalogFull, sqlProductRendererSQLite)
			group.Add(1)
			go func(index int) {
				defer group.Done()
				results[index], errors[index] = sqlProductRun(
					project.unselected,
					environments[index],
					project.globalBinary,
					"sqlmigrate", "blog", "0002_enrich", "--project", project.descriptor,
				)
			}(index)
		}
		group.Wait()
		pids := make(map[int]struct{}, parallel)
		for index := 0; index < parallel; index++ {
			if errors[index] != nil {
				t.Fatalf("parallel sqlmigrate %d: %v", index, errors[index])
			}
			project.assertCommandBoundary(t, states[index], results[index])
			sqlProductAssertSuccess(t, results[index], sqlProductEnrichOutput, project.sensitive(states[index])...)
			pids[sqlProductAssertMarker(t, states[index].initMarker, "init")] = struct{}{}
			sqlProductAssertMarker(t, states[index].rendererMarker, "render")
		}
		if len(pids) != parallel {
			t.Fatalf("parallel invocations used %d distinct runner PIDs, want %d", len(pids), parallel)
		}
	})

	sqlProductAuditApplicationSources(t, project.repository, project.root)
	project.assertApplicationUnchanged(t)
	project.assertWorkspaceEmpty(t)
	if strings.Contains(sqlProductEnrichOutput, ";;") || !strings.HasSuffix(sqlProductEnrichOutput, ";\n") {
		t.Fatal("canonical expected output does not own exactly one final statement terminator")
	}
}
