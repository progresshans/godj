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

// queryCallRecorder observes Queryer calls at the generic ORM boundary. It is
// intentionally independent from sqlite.Backend.QueryCount: setup DDL/DML and
// the final database-state read use the undecorated backend, while every
// terminal under test uses recordingQueryer inside an explicit capture window.
type queryCallRecorder struct {
	mu    sync.Mutex
	kinds []string
}

func (recorder *queryCallRecorder) checkpoint() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.kinds)
}

func (recorder *queryCallRecorder) recordQueryAttempt() {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	// db.Queryer accepts only immutable SELECT plans. Record before delegating
	// so a backend failure such as a missing table remains an observed attempt.
	recorder.kinds = append(recorder.kinds, "SELECT")
}

func (recorder *queryCallRecorder) kindsSince(checkpoint int) ([]string, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if checkpoint < 0 || checkpoint > len(recorder.kinds) {
		return nil, fmt.Errorf("query recorder checkpoint %d outside [0,%d]", checkpoint, len(recorder.kinds))
	}
	return append([]string(nil), recorder.kinds[checkpoint:]...), nil
}

type recordingQueryer struct {
	delegate db.Queryer
	recorder *queryCallRecorder
}

var _ db.Queryer = (*recordingQueryer)(nil)

func (backend *recordingQueryer) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.recorder.recordQueryAttempt()
	return backend.delegate.Query(ctx, plan)
}

func observedQueryer(delegate db.Queryer, recorder *queryCallRecorder) db.Queryer {
	return &recordingQueryer{delegate: delegate, recorder: recorder}
}

func queryCacheRepeatedFullEvaluation(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		querySet := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "first_full_evaluation", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "second_full_evaluation", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheEmptyFullEvaluation(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		querySet := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			Filter(models.ArticleFields.Title.Exact("Later")).
			OrderBy(models.ArticleFields.ID.Asc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "empty_evaluation", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := insertQueryCacheArticle(ctx, backend, 5, "Later", false, nil); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "same_queryset_after_matching_insert", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheStaleSnapshotAndFreshQuerySet(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		observed := observedQueryer(backend, recorder)
		source := models.ArticleObjects.Using(observed).OrderBy(models.ArticleFields.ID.Asc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "source_before_insert", source.All); err != nil {
			return protocol.Observation{}, err
		}
		summary := "fresh"
		if err := insertQueryCacheArticle(ctx, backend, 5, "New", true, &summary); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "source_after_insert", source.All); err != nil {
			return protocol.Observation{}, err
		}
		fresh := models.ArticleObjects.Using(observed).OrderBy(models.ArticleFields.ID.Asc())
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "fresh_queryset_after_insert", fresh.All); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheChainedQuerySetIndependence(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		source := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			Filter(models.ArticleFields.Published.Exact(true)).
			OrderBy(models.ArticleFields.ID.Asc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "source_before_insert", source.All); err != nil {
			return protocol.Observation{}, err
		}
		summary := "chain"
		if err := insertQueryCacheArticle(ctx, backend, 5, "New Django", true, &summary); err != nil {
			return protocol.Observation{}, err
		}
		derived := source.Filter(models.ArticleFields.Title.IContains("django"))
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "derived_after_insert", derived.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "source_after_insert", source.All); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheCountColdAndWarm(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		querySet := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureQueryCacheStep(recorder, &resultSteps, &metricSteps, "count_before_insert", func() (protocol.Value, error) {
			count, err := querySet.Count(ctx)
			return protocol.Integer(strconv.FormatInt(count, 10)), err
		}); err != nil {
			return protocol.Observation{}, err
		}
		if err := insertQueryCacheArticle(ctx, backend, 5, "Counted", false, nil); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "full_evaluation_after_insert", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureQueryCacheStep(recorder, &resultSteps, &metricSteps, "count_from_full_cache", func() (protocol.Value, error) {
			count, err := querySet.Count(ctx)
			return protocol.Integer(strconv.FormatInt(count, 10)), err
		}); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheExistsColdAndWarm(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		querySet := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			Filter(models.ArticleFields.Title.Exact("Later")).
			OrderBy(models.ArticleFields.ID.Asc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureQueryCacheStep(recorder, &resultSteps, &metricSteps, "exists_before_insert", func() (protocol.Value, error) {
			exists, err := querySet.Exists(ctx)
			return protocol.Boolean(exists), err
		}); err != nil {
			return protocol.Observation{}, err
		}
		if err := insertQueryCacheArticle(ctx, backend, 5, "Later", true, nil); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "full_evaluation_after_insert", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureQueryCacheStep(recorder, &resultSteps, &metricSteps, "exists_from_full_cache", func() (protocol.Value, error) {
			exists, err := querySet.Exists(ctx)
			return protocol.Boolean(exists), err
		}); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheIteratorBypass(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		querySet := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "cached_before_insert", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := insertQueryCacheArticle(ctx, backend, 5, "Iterator", true, nil); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureQueryCacheStep(recorder, &resultSteps, &metricSteps, "iterator_after_insert", func() (protocol.Value, error) {
			articles := make([]models.Article, 0)
			err := querySet.Iterate(ctx, func(article models.Article) error {
				articles = append(articles, article)
				return nil
			})
			return articleList(articles), err
		}); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "source_after_iterator", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheIndexPartialEvaluation(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		querySet := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Desc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureArticleTerminalStep(ctx, recorder, &resultSteps, &metricSteps, "index_before_insert", func(ctx context.Context) (models.Article, bool, error) {
			return querySet.At(ctx, 0)
		}); err != nil {
			return protocol.Observation{}, err
		}
		if err := insertQueryCacheArticle(ctx, backend, 5, "Index five", false, nil); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleTerminalStep(ctx, recorder, &resultSteps, &metricSteps, "index_after_first_insert", func(ctx context.Context) (models.Article, bool, error) {
			return querySet.At(ctx, 0)
		}); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "full_evaluation_after_first_insert", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := insertQueryCacheArticle(ctx, backend, 6, "Index six", true, nil); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleTerminalStep(ctx, recorder, &resultSteps, &metricSteps, "index_from_full_cache_after_second_insert", func(ctx context.Context) (models.Article, bool, error) {
			return querySet.At(ctx, 0)
		}); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheFailedEvaluationRetry(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		if _, err := backend.ExecContext(ctx, `DROP TABLE "godj_conformance_article"`); err != nil {
			return protocol.Observation{}, fmt.Errorf("drop Article table before failed evaluation: %w", err)
		}
		recorder := &queryCallRecorder{}
		querySet := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureQueryCacheErrorStep(recorder, &resultSteps, &metricSteps, "failed_evaluation", func() error {
			_, err := querySet.All(ctx)
			return err
		}); err != nil {
			return protocol.Observation{}, err
		}
		if err := createAndSeedQueryCacheArticleTable(ctx, backend); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "retry_after_schema_repair", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "repeat_after_success", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheFreshClone(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		source := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "source_before_insert", source.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := insertQueryCacheArticle(ctx, backend, 5, "Clone", true, nil); err != nil {
			return protocol.Observation{}, err
		}
		var fresh = source
		if err := captureQueryCacheStep(recorder, &resultSteps, &metricSteps, "fresh_copy_request", func() (protocol.Value, error) {
			fresh = source.Fresh()
			return protocol.Object(map[string]protocol.Value{"completed": protocol.Boolean(true)}), nil
		}); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "clone_after_insert", fresh.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "source_after_insert", source.All); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func queryCacheFirstColdAndWarm(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryCallRecorder{}
		querySet := models.ArticleObjects.Using(observedQueryer(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Desc())
		resultSteps, metricSteps := newQueryCacheSteps()
		if err := captureArticleTerminalStep(ctx, recorder, &resultSteps, &metricSteps, "first_before_insert", querySet.First); err != nil {
			return protocol.Observation{}, err
		}
		if err := insertQueryCacheArticle(ctx, backend, 5, "First five", false, nil); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleTerminalStep(ctx, recorder, &resultSteps, &metricSteps, "first_after_first_insert", querySet.First); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleListStep(ctx, recorder, &resultSteps, &metricSteps, "full_evaluation_after_first_insert", querySet.All); err != nil {
			return protocol.Observation{}, err
		}
		if err := insertQueryCacheArticle(ctx, backend, 6, "First six", true, nil); err != nil {
			return protocol.Observation{}, err
		}
		if err := captureArticleTerminalStep(ctx, recorder, &resultSteps, &metricSteps, "first_from_full_cache_after_second_insert", querySet.First); err != nil {
			return protocol.Observation{}, err
		}
		return queryCacheObservation(ctx, backend, contractID, resultSteps, metricSteps)
	})
}

func newQueryCacheSteps() ([]protocol.Value, []protocol.Value) {
	return make([]protocol.Value, 0, 4), make([]protocol.Value, 0, 4)
}

func captureArticleListStep(
	ctx context.Context,
	recorder *queryCallRecorder,
	resultSteps, metricSteps *[]protocol.Value,
	name string,
	operation func(context.Context) ([]models.Article, error),
) error {
	return captureQueryCacheStep(recorder, resultSteps, metricSteps, name, func() (protocol.Value, error) {
		articles, err := operation(ctx)
		return articleList(articles), err
	})
}

func captureArticleTerminalStep(
	ctx context.Context,
	recorder *queryCallRecorder,
	resultSteps, metricSteps *[]protocol.Value,
	name string,
	operation func(context.Context) (models.Article, bool, error),
) error {
	return captureQueryCacheStep(recorder, resultSteps, metricSteps, name, func() (protocol.Value, error) {
		article, found, err := operation(ctx)
		if err != nil {
			return protocol.Value{}, err
		}
		if !found {
			return protocol.Value{}, fmt.Errorf("%s returned no Article row", name)
		}
		return articleValue(article), nil
	})
}

func captureQueryCacheStep(
	recorder *queryCallRecorder,
	resultSteps, metricSteps *[]protocol.Value,
	name string,
	operation func() (protocol.Value, error),
) error {
	checkpoint := recorder.checkpoint()
	value, operationErr := operation()
	metric, metricsErr := queryCacheMetricStep(recorder, checkpoint, name)
	if operationErr != nil {
		return fmt.Errorf("capture query-cache step %s: %w", name, operationErr)
	}
	if metricsErr != nil {
		return metricsErr
	}
	*resultSteps = append(*resultSteps, protocol.Object(map[string]protocol.Value{
		"name":  protocol.String(name),
		"value": value,
	}))
	*metricSteps = append(*metricSteps, metric)
	return nil
}

func captureQueryCacheErrorStep(
	recorder *queryCallRecorder,
	resultSteps, metricSteps *[]protocol.Value,
	name string,
	operation func() error,
) error {
	checkpoint := recorder.checkpoint()
	operationErr := operation()
	metric, metricsErr := queryCacheMetricStep(recorder, checkpoint, name)
	if metricsErr != nil {
		return metricsErr
	}
	if operationErr == nil {
		return fmt.Errorf("capture query-cache step %s: operation unexpectedly succeeded", name)
	}
	var queryErr *query.Error
	if !errors.As(operationErr, &queryErr) {
		return fmt.Errorf("capture query-cache step %s: error = %T, want *query.Error: %w", name, operationErr, operationErr)
	}
	*resultSteps = append(*resultSteps, protocol.Object(map[string]protocol.Value{
		"error": protocol.Object(map[string]protocol.Value{
			"category": protocol.String(queryErr.Category),
			"code":     protocol.String(queryErr.Code),
		}),
		"name": protocol.String(name),
	}))
	*metricSteps = append(*metricSteps, metric)
	return nil
}

func queryCacheMetricStep(recorder *queryCallRecorder, checkpoint int, name string) (protocol.Value, error) {
	kinds, err := recorder.kindsSince(checkpoint)
	if err != nil {
		return protocol.Value{}, err
	}
	statementKinds := make([]protocol.Value, len(kinds))
	for index, kind := range kinds {
		statementKinds[index] = protocol.String(kind)
	}
	return protocol.Object(map[string]protocol.Value{
		"name":            protocol.String(name),
		"query_count":     protocol.Integer(strconv.Itoa(len(kinds))),
		"statement_kinds": protocol.List(statementKinds...),
	}), nil
}

func queryCacheObservation(
	ctx context.Context,
	backend *sqlite.Backend,
	contractID string,
	resultSteps, metricSteps []protocol.Value,
) (protocol.Observation, error) {
	if len(resultSteps) != len(metricSteps) {
		return protocol.Observation{}, fmt.Errorf("query-cache result step count %d does not match metric step count %d", len(resultSteps), len(metricSteps))
	}
	state, err := readDatabaseState(ctx, backend)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{"steps": protocol.List(resultSteps...)})
	metrics := protocol.Object(map[string]protocol.Value{"steps": protocol.List(metricSteps...)})
	return protocol.Observation{
		ID:      contractID,
		Status:  protocol.StatusObserved,
		Phase:   protocol.PhaseEvaluation,
		Result:  valuePointer(result),
		DBState: valuePointer(state),
		Metrics: valuePointer(metrics),
	}, nil
}

func insertQueryCacheArticle(
	ctx context.Context,
	backend *sqlite.Backend,
	id int64,
	title string,
	published bool,
	summary *string,
) error {
	if _, err := backend.ExecContext(
		ctx,
		`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES (?, ?, ?, ?)`,
		id,
		title,
		published,
		summary,
	); err != nil {
		return fmt.Errorf("insert query-cache Article %d: %w", id, err)
	}
	return nil
}

func createAndSeedQueryCacheArticleTable(ctx context.Context, backend *sqlite.Backend) error {
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`); err != nil {
		return fmt.Errorf("repair query-cache Article table: %w", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES
  (1, 'Alpine Guide', TRUE, NULL),
  (2, 'django Tips', FALSE, 'ORM'),
  (3, 'Django Deep Dive', TRUE, ''),
  (4, 'Other', TRUE, NULL)`); err != nil {
		return fmt.Errorf("seed repaired query-cache Article table: %w", err)
	}
	return nil
}
