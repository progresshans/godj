package godj

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
)

// statementRecorder observes the product Mutator boundary instead of parsing
// backend SQL. Setup writes and the database-state read deliberately bypass
// this recorder, so metrics contain only the save operation under test.
type statementRecorder struct {
	mu    sync.Mutex
	kinds []string
}

func (recorder *statementRecorder) record(kind string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.kinds = append(recorder.kinds, kind)
}

func (recorder *statementRecorder) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.kinds...)
}

type recordingMutator struct {
	delegate db.Mutator
	recorder *statementRecorder
}

var _ db.Mutator = (*recordingMutator)(nil)

func (backend *recordingMutator) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	backend.recorder.record("INSERT")
	return backend.delegate.Insert(ctx, plan)
}

func (backend *recordingMutator) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	backend.recorder.record("UPDATE")
	return backend.delegate.Update(ctx, plan)
}

func (backend *recordingMutator) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	backend.recorder.record("DELETE")
	return backend.delegate.Delete(ctx, plan)
}

func modelSaveNewAutoPrimaryKey(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		summary := "Created"
		article := models.Article{Title: "New save", Published: true, Summary: &summary}
		before := saveArticleValue(article)
		recorder := &statementRecorder{}
		if err := models.ArticleObjects.Save(ctx, observedMutator(backend, recorder), &article); err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{
			"after":  saveArticleValue(article),
			"before": before,
		})
		return saveResultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, recorder)
	})
}

func modelSaveLoadedAllFields(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		created, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Before").WithSummary("Loaded summary"),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		loaded, err := loadArticle(ctx, backend, created.ID)
		if err != nil {
			return protocol.Observation{}, err
		}
		if _, err := models.ArticleObjects.Update(
			ctx,
			backend,
			created,
			models.ArticlePatch{}.WithPublished(true).WithSummary("Concurrent database value"),
		); err != nil {
			return protocol.Observation{}, err
		}
		loaded.Title = "After default save"
		recorder := &statementRecorder{}
		if err := models.ArticleObjects.Save(ctx, observedMutator(backend, recorder), &loaded); err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{"instance_after": saveArticleValue(loaded)})
		return saveResultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, recorder)
	})
}

func modelSaveUpdateFieldsNamed(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		created, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Before").WithSummary("Loaded summary"),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		loaded, err := loadArticle(ctx, backend, created.ID)
		if err != nil {
			return protocol.Observation{}, err
		}
		if _, err := models.ArticleObjects.Update(
			ctx,
			backend,
			created,
			models.ArticlePatch{}.WithSummary("Database preserved"),
		); err != nil {
			return protocol.Observation{}, err
		}
		memorySummary := "Memory only"
		loaded.Title = "Only title persists"
		loaded.Published = true
		loaded.Summary = &memorySummary
		recorder := &statementRecorder{}
		if err := models.ArticleObjects.Save(
			ctx,
			observedMutator(backend, recorder),
			&loaded,
			models.ArticleUpdateFields(models.ArticleFields.Title),
		); err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{"instance_after": saveArticleValue(loaded)})
		return saveResultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, recorder)
	})
}

func modelSaveUpdateFieldsEmpty(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		article, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Persisted").WithSummary("Database"),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		memorySummary := "Also memory only"
		article.Title = "Memory only"
		article.Published = true
		article.Summary = &memorySummary
		recorder := &statementRecorder{}
		if err := models.ArticleObjects.Save(
			ctx,
			observedMutator(backend, recorder),
			&article,
			models.ArticleUpdateFields(),
		); err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{"instance_after": saveArticleValue(article)})
		return saveResultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, recorder)
	})
}

func modelSaveUpdateFieldsPrimaryKey(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		article, err := models.ArticleObjects.Create(ctx, backend, models.NewArticleCreate("Unchanged"))
		if err != nil {
			return protocol.Observation{}, err
		}
		article.Title = "Rejected"
		recorder := &statementRecorder{}
		saveErr := models.ArticleObjects.Save(
			ctx,
			observedMutator(backend, recorder),
			&article,
			models.ArticleUpdateFieldNames("id"),
		)
		return saveErrorObservation(ctx, backend, contractID, protocol.PhaseEvaluation, saveErr, recorder)
	})
}

func modelSaveForceInsertConflict(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		inserted, err := models.ArticleObjects.Create(ctx, backend, models.NewArticleCreate("Existing"))
		if err != nil {
			return protocol.Observation{}, err
		}
		article := models.NewArticleWithID(inserted.ID)
		article.Title = "Force insert conflict"
		recorder := &statementRecorder{}
		saveErr := models.ArticleObjects.Save(
			ctx,
			observedMutator(backend, recorder),
			&article,
			models.ArticleForceInsert(),
		)
		return saveErrorObservation(ctx, backend, contractID, protocol.PhaseEvaluation, saveErr, recorder)
	})
}

func modelSaveForceUpdateWithoutPrimaryKey(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		article := models.Article{Title: "No primary key"}
		recorder := &statementRecorder{}
		saveErr := models.ArticleObjects.Save(
			ctx,
			observedMutator(backend, recorder),
			&article,
			models.ArticleForceUpdate(),
		)
		return saveErrorObservation(ctx, backend, contractID, protocol.PhaseEvaluation, saveErr, recorder)
	})
}

func modelSaveForceUpdateMissingRow(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		article := models.NewArticleWithID(999)
		article.Title = "Missing row"
		recorder := &statementRecorder{}
		saveErr := models.ArticleObjects.Save(
			ctx,
			observedMutator(backend, recorder),
			&article,
			models.ArticleForceUpdate(),
		)
		return saveErrorObservation(ctx, backend, contractID, protocol.PhaseEvaluation, saveErr, recorder)
	})
}

func modelSaveMutuallyExclusiveForceFlags(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		article := models.Article{Title: "Mutually exclusive"}
		recorder := &statementRecorder{}
		saveErr := models.ArticleObjects.Save(
			ctx,
			observedMutator(backend, recorder),
			&article,
			models.ArticleForceInsert(),
			models.ArticleForceUpdate(),
		)
		return saveErrorObservation(ctx, backend, contractID, protocol.PhaseEvaluation, saveErr, recorder)
	})
}

func modelSaveExplicitPrimaryKeyExisting(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		if _, err := backend.ExecContext(
			ctx,
			`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES (?, ?, ?, ?)`,
			int64(41),
			"Existing",
			false,
			nil,
		); err != nil {
			return protocol.Observation{}, fmt.Errorf("seed explicit primary-key row: %w", err)
		}
		article := models.NewArticleWithID(41)
		article.Title = "Updated existing"
		article.Published = true
		recorder := &statementRecorder{}
		if err := models.ArticleObjects.Save(ctx, observedMutator(backend, recorder), &article); err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{"instance_after": saveArticleValue(article)})
		return saveResultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, recorder)
	})
}

func modelSaveExplicitPrimaryKeyMissing(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		summary := "Fallback insert"
		article := models.NewArticleWithID(42)
		article.Title = "Inserted missing"
		article.Summary = &summary
		recorder := &statementRecorder{}
		if err := models.ArticleObjects.Save(ctx, observedMutator(backend, recorder), &article); err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{"instance_after": saveArticleValue(article)})
		return saveResultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, recorder)
	})
}

func modelSaveAtomicRollbackInstanceState(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		sentinel, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Persisted before transaction").WithSummary("Original"),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		created := models.Article{Title: "Rolled back new instance", Published: true}
		recorder := &statementRecorder{}
		atomicErr := backend.Atomic(ctx, func(session db.Session) error {
			recorder.record("BEGIN")
			memorySummary := "Memory summary after rollback"
			sentinel.Title = "Memory after rollback"
			sentinel.Summary = &memorySummary
			observedSession := observedMutator(session, recorder)
			if err := models.ArticleObjects.Save(
				ctx,
				observedSession,
				&sentinel,
				models.ArticleUpdateFields(models.ArticleFields.Title, models.ArticleFields.Summary),
			); err != nil {
				return err
			}
			if err := models.ArticleObjects.Save(ctx, observedSession, &created); err != nil {
				return err
			}
			return errForcedRollback
		})
		if atomicErr != errForcedRollback {
			return protocol.Observation{}, fmt.Errorf("atomic rollback error = %v, want exact forced rollback", atomicErr)
		}
		result := protocol.Object(map[string]protocol.Value{
			"created_instance_after":  saveArticleValue(created),
			"rollback_triggered":      protocol.Boolean(true),
			"sentinel_instance_after": saveArticleValue(sentinel),
		})
		return saveResultObservation(ctx, backend, contractID, protocol.PhaseRollback, result, recorder)
	})
}

func observedMutator(backend db.Mutator, recorder *statementRecorder) db.Mutator {
	return &recordingMutator{delegate: backend, recorder: recorder}
}

func loadArticle(ctx context.Context, backend *sqlite.Backend, id int64) (models.Article, error) {
	articles, err := models.ArticleObjects.Using(backend).
		Filter(models.ArticleFields.ID.Exact(id)).
		All(ctx)
	if err != nil {
		return models.Article{}, err
	}
	if len(articles) != 1 {
		return models.Article{}, fmt.Errorf("load Article %d returned %d rows, want 1", id, len(articles))
	}
	return articles[0], nil
}

func saveArticleValue(article models.Article) protocol.Value {
	primaryKey := protocol.Null()
	if _, present := (models.ArticleDescriptor{}).PrimaryKey(article); present {
		primaryKey = primaryKeyValue(article.ID)
	}
	summary := protocol.Null()
	if article.Summary != nil {
		summary = protocol.String(*article.Summary)
	}
	return protocol.Object(map[string]protocol.Value{
		"pk":        primaryKey,
		"published": protocol.Boolean(article.Published),
		"summary":   summary,
		"title":     protocol.String(article.Title),
	})
}

func saveMetrics(recorder *statementRecorder) protocol.Value {
	kinds := recorder.snapshot()
	values := make([]protocol.Value, len(kinds))
	for index, kind := range kinds {
		values[index] = protocol.String(kind)
	}
	return protocol.Object(map[string]protocol.Value{
		"query_count":     protocol.Integer(strconv.Itoa(len(kinds))),
		"statement_kinds": protocol.List(values...),
	})
}

func saveResultObservation(
	ctx context.Context,
	backend *sqlite.Backend,
	contractID string,
	phase protocol.Phase,
	result protocol.Value,
	recorder *statementRecorder,
) (protocol.Observation, error) {
	metrics := saveMetrics(recorder)
	state, err := readDatabaseState(ctx, backend)
	if err != nil {
		return protocol.Observation{}, err
	}
	return protocol.Observation{
		ID:      contractID,
		Status:  protocol.StatusObserved,
		Phase:   phase,
		Result:  valuePointer(result),
		DBState: valuePointer(state),
		Metrics: valuePointer(metrics),
	}, nil
}

func saveErrorObservation(
	ctx context.Context,
	backend *sqlite.Backend,
	contractID string,
	phase protocol.Phase,
	saveErr error,
	recorder *statementRecorder,
) (protocol.Observation, error) {
	if saveErr == nil {
		return protocol.Observation{}, errors.New("save operation unexpectedly succeeded")
	}
	var queryErr *query.Error
	if !errors.As(saveErr, &queryErr) {
		return protocol.Observation{}, fmt.Errorf("save error = %T, want *query.Error: %w", saveErr, saveErr)
	}
	metrics := saveMetrics(recorder)
	state, err := readDatabaseState(ctx, backend)
	if err != nil {
		return protocol.Observation{}, err
	}
	return protocol.Observation{
		ID:     contractID,
		Status: protocol.StatusObserved,
		Phase:  phase,
		Error: &protocol.ObservedError{
			Category:          queryErr.Category,
			Code:              queryErr.Code,
			Message:           queryErr.Error(),
			MessageIsContract: boolPointer(false),
		},
		DBState: valuePointer(state),
		Metrics: valuePointer(metrics),
	}, nil
}
