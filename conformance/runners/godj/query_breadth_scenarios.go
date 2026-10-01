package godj

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type queryBreadthScenario func(context.Context, string) (protocol.Observation, error)

var queryBreadthScenarioRegistry = map[string]queryBreadthScenario{
	"django.query.breadth.ordered_projection":               queryBreadthOrderedProjection,
	"django.query.breadth.source_fields_outside_projection": queryBreadthSourceFieldsOutsideProjection,
	"django.query.breadth.projection_cache_independence":    queryBreadthProjectionCacheIndependence,
	"django.query.breadth.distinct_projection":              queryBreadthDistinctProjection,
	"django.query.breadth.stable_offset_limit":              queryBreadthStableOffsetLimit,
	"django.query.breadth.invalid_offset_pre_io":            queryBreadthInvalidOffsetPreIO,
	"django.query.breadth.cold_count_and_warm_cache":        queryBreadthColdCountAndWarmCache,
	"django.query.breadth.sliced_distinct_count":            queryBreadthSlicedDistinctCount,
	"django.query.breadth.empty_count_and_nullable_max":     queryBreadthEmptyCountAndNullableMax,
	"django.query.breadth.filtered_count_and_max":           queryBreadthFilteredCountAndMax,
	"django.query.breadth.terminal_failure_ownership":       queryBreadthTerminalFailureOwnership,
	"django.query.breadth.backend_parity_reference":         queryBreadthBackendParityReference,
}

// queryBreadthScenarioHandler is the only registry boundary consumed by the
// shared runner. Unknown scenario names remain unregistered and therefore
// fail closed through the runner's manifest/status checks.
func queryBreadthScenarioHandler(scenario string) (scenarioHandler, bool) {
	run, ok := queryBreadthScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		return run(ctx, contract.ID)
	}, true
}

func queryBreadthOrderedProjection(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		projection := orm.Project4(
			models.ArticleFields.Title,
			models.ArticleFields.ID,
			models.ArticleFields.Summary,
			models.ArticleFields.Published,
			func(title string, id int64, summary *string, published bool) queryBreadthOrderedRow {
				return queryBreadthOrderedRow{title: title, id: id, summary: summary, published: published}
			},
		)
		rows, metrics, err := queryBreadthCapture(recorder, func() ([]queryBreadthOrderedRow, error) {
			return orm.SelectInto(ctx, source, projection)
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("ordered projection: %w", err)
		}
		resultRows := make([]protocol.Value, len(rows))
		for index, row := range rows {
			resultRows[index] = protocol.List(
				protocol.String(row.title),
				queryBreadthPrimaryKey(row.id),
				queryBreadthNullableString(row.summary),
				protocol.Boolean(row.published),
			)
		}
		result := protocol.Object(map[string]protocol.Value{
			"fields": queryBreadthStrings("title", "id", "summary", "published"),
			"rows":   protocol.List(resultRows...),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

type queryBreadthOrderedRow struct {
	title     string
	id        int64
	summary   *string
	published bool
}

func queryBreadthSourceFieldsOutsideProjection(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			Filter(models.ArticleFields.Published.Exact(true)).
			OrderBy(models.ArticleFields.Title.Asc(), models.ArticleFields.ID.Asc())
		rows, metrics, err := queryBreadthCapture(recorder, func() ([]int64, error) {
			return orm.SelectInto(ctx, source, orm.Project1(models.ArticleFields.ID, func(id int64) int64 { return id }))
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("source fields outside projection: %w", err)
		}
		projected := make([]protocol.Value, len(rows))
		for index, id := range rows {
			projected[index] = protocol.List(queryBreadthPrimaryKey(id))
		}
		result := protocol.Object(map[string]protocol.Value{
			"filter_fields":     queryBreadthStrings("published"),
			"order_fields":      queryBreadthStrings("title", "id"),
			"projection_fields": queryBreadthStrings("id"),
			"rows":              protocol.List(projected...),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

func queryBreadthProjectionCacheIndependence(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		projection := orm.Project1(models.ArticleFields.Title, func(title string) string { return title })
		resultSteps := make([]protocol.Value, 0, 5)
		metricSteps := make([]protocol.Value, 0, 5)

		empty, metric, err := queryBreadthCaptureStep(recorder, "empty_projection", func() ([]string, error) {
			return orm.SelectInto(ctx, source.Filter(models.ArticleFields.ID.Exact(999)), projection)
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		queryBreadthAppendStep(&resultSteps, &metricSteps, "empty_projection", queryBreadthStringValues(empty), metric)

		nonempty, metric, err := queryBreadthCaptureStep(recorder, "nonempty_projection", func() ([]string, error) {
			return orm.SelectInto(ctx, source, projection)
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		queryBreadthAppendStep(&resultSteps, &metricSteps, "nonempty_projection", queryBreadthStringValues(nonempty), metric)

		if err := queryBreadthInsertArticle(ctx, backend, 5, "Projection five", true, nil); err != nil {
			return protocol.Observation{}, err
		}
		modelsAfterFirstInsert, metric, err := queryBreadthCaptureStep(recorder, "model_evaluation_after_first_insert", func() ([]models.Article, error) {
			return source.All(ctx)
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		queryBreadthAppendStep(&resultSteps, &metricSteps, "model_evaluation_after_first_insert", queryBreadthArticlePrimaryKeys(modelsAfterFirstInsert), metric)

		summary := "fresh projection"
		if err := queryBreadthInsertArticle(ctx, backend, 6, "Projection six", false, &summary); err != nil {
			return protocol.Observation{}, err
		}
		freshProjection, metric, err := queryBreadthCaptureStep(recorder, "projection_after_second_insert", func() ([]string, error) {
			return orm.SelectInto(ctx, source, projection)
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		queryBreadthAppendStep(&resultSteps, &metricSteps, "projection_after_second_insert", queryBreadthStringValues(freshProjection), metric)

		cachedModels, metric, err := queryBreadthCaptureStep(recorder, "model_cache_after_second_insert", func() ([]models.Article, error) {
			return source.All(ctx)
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		queryBreadthAppendStep(&resultSteps, &metricSteps, "model_cache_after_second_insert", queryBreadthArticlePrimaryKeys(cachedModels), metric)
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, metricSteps)
	})
}

func queryBreadthDistinctProjection(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			OrderBy(models.ArticleFields.Published.Asc()).
			Distinct()
		rows, metrics, err := queryBreadthCapture(recorder, func() ([]bool, error) {
			return orm.SelectInto(ctx, source, orm.Project1(models.ArticleFields.Published, func(value bool) bool { return value }))
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("distinct projection: %w", err)
		}
		values := make([]protocol.Value, len(rows))
		for index, value := range rows {
			values[index] = protocol.List(protocol.Boolean(value))
		}
		result := protocol.Object(map[string]protocol.Value{
			"fields": queryBreadthStrings("published"),
			"rows":   protocol.List(values...),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

func queryBreadthStableOffsetLimit(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		page, err := queryBreadthPaginate(source, 1, 2)
		if err != nil {
			return protocol.Observation{}, err
		}
		pageRows, pageMetric, err := queryBreadthCaptureStep(recorder, "offset_one_limit_two", func() ([]queryBreadthPageRow, error) {
			return orm.SelectInto(ctx, page, orm.Project2(
				models.ArticleFields.ID,
				models.ArticleFields.Title,
				func(id int64, title string) queryBreadthPageRow { return queryBreadthPageRow{id: id, title: title} },
			))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		pageValues := make([]protocol.Value, len(pageRows))
		for index, row := range pageRows {
			pageValues[index] = protocol.List(queryBreadthPrimaryKey(row.id), protocol.String(row.title))
		}

		outOfRange, err := queryBreadthPaginate(source, 20, 2)
		if err != nil {
			return protocol.Observation{}, err
		}
		outOfRangeRows, outOfRangeMetric, err := queryBreadthCaptureStep(recorder, "out_of_range", func() ([]int64, error) {
			return orm.SelectInto(ctx, outOfRange, orm.Project1(models.ArticleFields.ID, func(id int64) int64 { return id }))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		resultSteps := []protocol.Value{
			queryBreadthResultStep("offset_one_limit_two", protocol.List(pageValues...)),
			queryBreadthResultStep("out_of_range", queryBreadthPrimaryKeys(outOfRangeRows)),
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, []protocol.Value{pageMetric, outOfRangeMetric})
	})
}

type queryBreadthPageRow struct {
	id    int64
	title string
}

func queryBreadthInvalidOffsetPreIO(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		overflowOffset := int64(math.MaxInt32) + 1
		cases := []struct {
			name  string
			value int
		}{
			{name: "negative", value: -1},
			{name: "maximum", value: math.MaxInt32},
			// Offset accepts int. On 32-bit targets this runtime conversion
			// wraps to a negative int, which still exercises the same
			// fail-closed invalid_offset/no-I/O boundary.
			{name: "overflow", value: int(overflowOffset)},
		}
		resultSteps := make([]protocol.Value, 0, len(cases))
		metricSteps := make([]protocol.Value, 0, len(cases))
		for _, test := range cases {
			checkpoint := recorder.checkpoint()
			_, offsetErr := source.Offset(test.value)
			var value protocol.Value
			if offsetErr == nil {
				if test.value != math.MaxInt32 {
					return protocol.Observation{}, fmt.Errorf("offset %s value %d unexpectedly succeeded", test.name, test.value)
				}
				value = protocol.Object(map[string]protocol.Value{
					"accepted": protocol.Integer(strconv.Itoa(test.value)),
				})
			} else {
				var queryErr *query.Error
				if !errors.As(offsetErr, &queryErr) || queryErr.Category != query.CategoryQuery || queryErr.Code != query.CodeInvalidOffset {
					return protocol.Observation{}, fmt.Errorf("offset %s error = %v, want %s/%s", test.name, offsetErr, query.CategoryQuery, query.CodeInvalidOffset)
				}
				value = protocol.Object(map[string]protocol.Value{
					"error": protocol.Object(map[string]protocol.Value{
						"category": protocol.String(queryErr.Category),
						"code":     protocol.String(queryErr.Code),
					}),
				})
			}
			metric, err := recorder.metricSince(checkpoint, map[string]protocol.Value{"name": protocol.String(test.name)})
			if err != nil {
				return protocol.Observation{}, err
			}
			resultSteps = append(resultSteps, queryBreadthResultStep(test.name, value))
			metricSteps = append(metricSteps, metric)
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseConstruction, resultSteps, metricSteps)
	})
}

func queryBreadthColdCountAndWarmCache(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Asc())
		resultSteps := make([]protocol.Value, 0, 3)
		metricSteps := make([]protocol.Value, 0, 3)

		coldCount, metric, err := queryBreadthCaptureStep(recorder, "cold_count", func() (int64, error) { return source.Count(ctx) })
		if err != nil {
			return protocol.Observation{}, err
		}
		queryBreadthAppendStep(&resultSteps, &metricSteps, "cold_count", queryBreadthInteger(coldCount), metric)
		if err := queryBreadthInsertArticle(ctx, backend, 5, "Count five", true, nil); err != nil {
			return protocol.Observation{}, err
		}
		articles, metric, err := queryBreadthCaptureStep(recorder, "model_evaluation_after_insert", func() ([]models.Article, error) { return source.All(ctx) })
		if err != nil {
			return protocol.Observation{}, err
		}
		queryBreadthAppendStep(&resultSteps, &metricSteps, "model_evaluation_after_insert", queryBreadthArticlePrimaryKeys(articles), metric)
		warmCount, metric, err := queryBreadthCaptureStep(recorder, "warm_count", func() (int64, error) { return source.Count(ctx) })
		if err != nil {
			return protocol.Observation{}, err
		}
		queryBreadthAppendStep(&resultSteps, &metricSteps, "warm_count", queryBreadthInteger(warmCount), metric)
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, metricSteps)
	})
}

func queryBreadthSlicedDistinctCount(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			Filter(models.ArticleFields.Published.Exact(true)).
			OrderBy(models.ArticleFields.ID.Asc()).
			Distinct()
		source, err := queryBreadthPaginate(source, 1, 2)
		if err != nil {
			return protocol.Observation{}, err
		}
		rows, rowMetric, err := queryBreadthCaptureStep(recorder, "logical_source_rows", func() ([]int64, error) {
			return orm.SelectInto(ctx, source, orm.Project1(models.ArticleFields.ID, func(id int64) int64 { return id }))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		count, countMetric, err := queryBreadthCaptureStep(recorder, "logical_source_count", func() (int64, error) { return source.Count(ctx) })
		if err != nil {
			return protocol.Observation{}, err
		}
		resultSteps := []protocol.Value{
			queryBreadthResultStep("logical_source_rows", queryBreadthPrimaryKeys(rows)),
			queryBreadthResultStep("logical_source_count", queryBreadthInteger(count)),
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, []protocol.Value{rowMetric, countMetric})
	})
}

func queryBreadthEmptyCountAndNullableMax(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			Filter(models.ArticleFields.Title.Exact("missing"))
		count, countMetric, err := queryBreadthCaptureStep(recorder, "empty_count", func() (int64, error) { return source.Count(ctx) })
		if err != nil {
			return protocol.Observation{}, err
		}
		maximum, maximumMetric, err := queryBreadthCaptureStep(recorder, "empty_nullable_max", func() (orm.Optional[string], error) {
			return orm.AggregateInto(ctx, source, orm.Aggregate1(
				orm.Max(models.ArticleFields.Summary),
				func(value orm.Optional[string]) orm.Optional[string] { return value },
			))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		resultSteps := []protocol.Value{
			queryBreadthResultStep("empty_count", queryBreadthInteger(count)),
			queryBreadthResultStep("empty_nullable_max", queryBreadthOptionalString(maximum)),
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, []protocol.Value{countMetric, maximumMetric})
	})
}

func queryBreadthFilteredCountAndMax(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			Filter(models.ArticleFields.Published.Exact(true))
		value, metrics, err := queryBreadthCapture(recorder, func() (queryBreadthAggregate3, error) {
			return orm.AggregateInto(ctx, source, orm.Aggregate3(
				orm.CountRows[models.Article](),
				orm.Max(models.ArticleFields.ID),
				orm.Max(models.ArticleFields.Summary),
				func(count int64, latestID orm.Optional[int64], maximumSummary orm.Optional[string]) queryBreadthAggregate3 {
					return queryBreadthAggregate3{count: count, latestID: latestID, maximumSummary: maximumSummary}
				},
			))
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("filtered count and max: %w", err)
		}
		latestID, present := value.latestID.Get()
		if !present {
			return protocol.Observation{}, errors.New("filtered count and max returned no latest ID")
		}
		result := protocol.Object(map[string]protocol.Value{
			"fields": queryBreadthStrings("row_count", "latest_id", "max_summary"),
			"values": protocol.List(
				queryBreadthInteger(value.count),
				queryBreadthPrimaryKey(latestID),
				queryBreadthOptionalString(value.maximumSummary),
			),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

type queryBreadthAggregate3 struct {
	count          int64
	latestID       orm.Optional[int64]
	maximumSummary orm.Optional[string]
}

func queryBreadthTerminalFailureOwnership(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		cases := []queryBreadthFaultCase{
			{name: "consumer_stop", mode: queryBreadthFaultNone},
			{name: "decode_failure", mode: queryBreadthFaultDecode},
			{name: "iteration_failure", mode: queryBreadthFaultIteration},
			{name: "close_failure", mode: queryBreadthFaultClose},
		}
		resultSteps := make([]protocol.Value, 0, len(cases))
		metricSteps := make([]protocol.Value, 0, len(cases))
		for _, test := range cases {
			result, metric, err := queryBreadthRunFaultCase(ctx, backend, test)
			if err != nil {
				return protocol.Observation{}, fmt.Errorf("terminal failure case %s: %w", test.name, err)
			}
			resultSteps = append(resultSteps, queryBreadthResultStep(test.name, result))
			metricSteps = append(metricSteps, metric)
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, metricSteps)
	})
}

func queryBreadthBackendParityReference(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryBreadthSQLRecorder{}
		source := models.ArticleObjects.Using(queryBreadthObserved(backend, recorder)).
			Filter(models.ArticleFields.Published.Exact(true)).
			OrderBy(models.ArticleFields.ID.Asc())
		source, err := queryBreadthPaginate(source, 1, 2)
		if err != nil {
			return protocol.Observation{}, err
		}
		rows, rowMetric, err := queryBreadthCaptureStep(recorder, "sqlite_reference_projection", func() ([]queryBreadthParityRow, error) {
			return orm.SelectInto(ctx, source, orm.Project3(
				models.ArticleFields.ID,
				models.ArticleFields.Title,
				models.ArticleFields.Published,
				func(id int64, title string, published bool) queryBreadthParityRow {
					return queryBreadthParityRow{id: id, title: title, published: published}
				},
			))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		projected := make([]protocol.Value, len(rows))
		for index, row := range rows {
			projected[index] = protocol.List(queryBreadthPrimaryKey(row.id), protocol.String(row.title), protocol.Boolean(row.published))
		}
		aggregate, aggregateMetric, err := queryBreadthCaptureStep(recorder, "sqlite_reference_aggregate", func() (queryBreadthAggregate2, error) {
			return orm.AggregateInto(ctx, source, orm.Aggregate2(
				orm.CountRows[models.Article](),
				orm.Max(models.ArticleFields.ID),
				func(count int64, latestID orm.Optional[int64]) queryBreadthAggregate2 {
					return queryBreadthAggregate2{count: count, latestID: latestID}
				},
			))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		latestID, present := aggregate.latestID.Get()
		if !present {
			return protocol.Observation{}, errors.New("SQLite parity aggregate returned no latest ID")
		}
		aggregateValue := protocol.Object(map[string]protocol.Value{
			"fields": queryBreadthStrings("row_count", "latest_id"),
			"values": protocol.List(queryBreadthInteger(aggregate.count), queryBreadthPrimaryKey(latestID)),
		})
		resultSteps := []protocol.Value{
			queryBreadthResultStep("sqlite_reference_projection", protocol.List(projected...)),
			queryBreadthResultStep("sqlite_reference_aggregate", aggregateValue),
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, []protocol.Value{rowMetric, aggregateMetric})
	})
}

type queryBreadthParityRow struct {
	id        int64
	title     string
	published bool
}

type queryBreadthAggregate2 struct {
	count    int64
	latestID orm.Optional[int64]
}

func queryBreadthPaginate(source orm.QuerySet[models.Article], offset, limit int) (orm.QuerySet[models.Article], error) {
	paged, err := source.Offset(offset)
	if err != nil {
		return orm.QuerySet[models.Article]{}, err
	}
	return paged.Limit(limit)
}

func queryBreadthInsertArticle(ctx context.Context, backend *sqlite.Backend, id int64, title string, published bool, summary *string) error {
	if _, err := backend.ExecContext(
		ctx,
		`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES (?, ?, ?, ?)`,
		id,
		title,
		published,
		summary,
	); err != nil {
		return fmt.Errorf("insert query-breadth Article %d: %w", id, err)
	}
	return nil
}

func queryBreadthStepsObservation(
	ctx context.Context,
	backend *sqlite.Backend,
	contractID string,
	phase protocol.Phase,
	resultSteps, metricSteps []protocol.Value,
) (protocol.Observation, error) {
	if len(resultSteps) != len(metricSteps) {
		return protocol.Observation{}, fmt.Errorf("query-breadth result step count %d does not match metric step count %d", len(resultSteps), len(metricSteps))
	}
	result := protocol.Object(map[string]protocol.Value{"steps": protocol.List(resultSteps...)})
	metrics := protocol.Object(map[string]protocol.Value{"steps": protocol.List(metricSteps...)})
	return resultObservation(ctx, backend, contractID, phase, result, &metrics)
}

func queryBreadthAppendStep(resultSteps, metricSteps *[]protocol.Value, name string, value, metric protocol.Value) {
	*resultSteps = append(*resultSteps, queryBreadthResultStep(name, value))
	*metricSteps = append(*metricSteps, metric)
}

func queryBreadthResultStep(name string, value protocol.Value) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"name":  protocol.String(name),
		"value": value,
	})
}

func queryBreadthStrings(values ...string) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = protocol.String(value)
	}
	return protocol.List(items...)
}

func queryBreadthStringValues(values []string) protocol.Value {
	return queryBreadthStrings(values...)
}

func queryBreadthInteger(value int64) protocol.Value {
	return protocol.Integer(strconv.FormatInt(value, 10))
}

func queryBreadthPrimaryKey(value int64) protocol.Value {
	return protocol.PrimaryKey(queryBreadthInteger(value))
}

func queryBreadthPrimaryKeys(values []int64) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = queryBreadthPrimaryKey(value)
	}
	return protocol.List(items...)
}

func queryBreadthArticlePrimaryKeys(values []models.Article) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = queryBreadthPrimaryKey(value.ID)
	}
	return protocol.List(items...)
}

func queryBreadthNullableString(value *string) protocol.Value {
	if value == nil {
		return protocol.Null()
	}
	return protocol.String(*value)
}

func queryBreadthOptionalString(value orm.Optional[string]) protocol.Value {
	actual, present := value.Get()
	if !present {
		return protocol.Null()
	}
	return protocol.String(actual)
}

type queryBreadthSQLRecorder struct {
	mu         sync.Mutex
	statements []protocol.Value
}

func (recorder *queryBreadthSQLRecorder) checkpoint() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.statements)
}

func (recorder *queryBreadthSQLRecorder) record(statement string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.statements = append(recorder.statements, queryBreadthStatementShape(statement))
}

func (recorder *queryBreadthSQLRecorder) metricSince(checkpoint int, extra map[string]protocol.Value) (protocol.Value, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if checkpoint < 0 || checkpoint > len(recorder.statements) {
		return protocol.Value{}, fmt.Errorf("query-breadth recorder checkpoint %d outside [0,%d]", checkpoint, len(recorder.statements))
	}
	statements := append([]protocol.Value(nil), recorder.statements[checkpoint:]...)
	fields := make(map[string]protocol.Value, len(extra)+2)
	for name, value := range extra {
		fields[name] = value
	}
	fields["query_count"] = protocol.Integer(strconv.Itoa(len(statements)))
	fields["statements"] = protocol.List(statements...)
	return protocol.Object(fields), nil
}

type queryBreadthObservedQueryer struct {
	delegate  db.Queryer
	recorder  *queryBreadthSQLRecorder
	fault     queryBreadthFaultMode
	probeRows bool

	mu     sync.Mutex
	probes []*queryBreadthRowsProbe
}

var _ db.Queryer = (*queryBreadthObservedQueryer)(nil)

func queryBreadthObserved(delegate db.Queryer, recorder *queryBreadthSQLRecorder) db.Queryer {
	return &queryBreadthObservedQueryer{delegate: delegate, recorder: recorder}
}

func (observed *queryBreadthObservedQueryer) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	statement, _, err := sqlite.Compile(plan)
	if err != nil {
		return nil, err
	}
	observed.recorder.record(statement)
	rows, err := observed.delegate.Query(ctx, plan)
	if err != nil || rows == nil || (!observed.probeRows && observed.fault == queryBreadthFaultNone) {
		return rows, err
	}
	probe := &queryBreadthRowsProbe{delegate: rows, fault: observed.fault}
	observed.mu.Lock()
	observed.probes = append(observed.probes, probe)
	observed.mu.Unlock()
	return probe, nil
}

func (observed *queryBreadthObservedQueryer) singleProbe() (*queryBreadthRowsProbe, error) {
	observed.mu.Lock()
	defer observed.mu.Unlock()
	if len(observed.probes) != 1 {
		return nil, fmt.Errorf("query-breadth terminal opened %d row streams, want exactly one", len(observed.probes))
	}
	return observed.probes[0], nil
}

var (
	queryBreadthAggregatePattern = regexp.MustCompile(`\b(COUNT|MAX)\s*\(`)
	queryBreadthDerivedPattern   = regexp.MustCompile(`\bFROM\s*\(`)
	queryBreadthDistinctPattern  = regexp.MustCompile(`\bSELECT\s+DISTINCT\b`)
	queryBreadthLimitPattern     = regexp.MustCompile(`\bLIMIT\b`)
	queryBreadthOffsetPattern    = regexp.MustCompile(`\bOFFSET\b`)
)

func queryBreadthStatementShape(statement string) protocol.Value {
	rendered := strings.ToUpper(strings.Join(strings.Fields(statement), " "))
	kind := "EMPTY"
	if fields := strings.Fields(rendered); len(fields) != 0 {
		kind = fields[0]
	}
	present := make(map[string]bool, 2)
	for _, match := range queryBreadthAggregatePattern.FindAllStringSubmatch(rendered, -1) {
		present[match[1]] = true
	}
	aggregates := make([]protocol.Value, 0, 2)
	for _, function := range []string{"COUNT", "MAX"} {
		if present[function] {
			aggregates = append(aggregates, protocol.String(function))
		}
	}
	return protocol.Object(map[string]protocol.Value{
		"aggregate_functions": protocol.List(aggregates...),
		"derived_table":       protocol.Boolean(queryBreadthDerivedPattern.MatchString(rendered)),
		"distinct":            protocol.Boolean(queryBreadthDistinctPattern.MatchString(rendered)),
		"has_limit":           protocol.Boolean(queryBreadthLimitPattern.MatchString(rendered)),
		"has_offset":          protocol.Boolean(queryBreadthOffsetPattern.MatchString(rendered)),
		"statement_kind":      protocol.String(kind),
	})
}

func queryBreadthCapture[T any](recorder *queryBreadthSQLRecorder, operation func() (T, error)) (T, protocol.Value, error) {
	checkpoint := recorder.checkpoint()
	value, err := operation()
	if err != nil {
		var zero T
		return zero, protocol.Value{}, err
	}
	metric, err := recorder.metricSince(checkpoint, nil)
	return value, metric, err
}

func queryBreadthCaptureStep[T any](recorder *queryBreadthSQLRecorder, name string, operation func() (T, error)) (T, protocol.Value, error) {
	checkpoint := recorder.checkpoint()
	value, err := operation()
	if err != nil {
		var zero T
		return zero, protocol.Value{}, fmt.Errorf("capture query-breadth step %s: %w", name, err)
	}
	metric, err := recorder.metricSince(checkpoint, map[string]protocol.Value{"name": protocol.String(name)})
	return value, metric, err
}

type queryBreadthFaultMode uint8

const (
	queryBreadthFaultNone queryBreadthFaultMode = iota
	queryBreadthFaultDecode
	queryBreadthFaultIteration
	queryBreadthFaultClose
)

type queryBreadthFaultCase struct {
	name string
	mode queryBreadthFaultMode
}

var (
	errQueryBreadthConsumerStop = errors.New("query-breadth consumer stopped")
	errQueryBreadthDecode       = errors.New("query-breadth forced decode failure")
	errQueryBreadthIteration    = errors.New("query-breadth forced iteration failure")
	errQueryBreadthClose        = errors.New("query-breadth forced close failure")
)

type queryBreadthRowsProbe struct {
	delegate db.Rows
	fault    queryBreadthFaultMode

	nextCalls     int
	closeAttempts int
	iterationErr  error
}

var _ db.Rows = (*queryBreadthRowsProbe)(nil)

func (probe *queryBreadthRowsProbe) Next() bool {
	probe.nextCalls++
	if probe.fault == queryBreadthFaultIteration && probe.nextCalls == 2 {
		probe.iterationErr = errQueryBreadthIteration
		return false
	}
	return probe.delegate.Next()
}

func (probe *queryBreadthRowsProbe) Scan(destinations ...any) error {
	if probe.fault == queryBreadthFaultDecode {
		return errQueryBreadthDecode
	}
	return probe.delegate.Scan(destinations...)
}

func (probe *queryBreadthRowsProbe) Err() error {
	return errors.Join(probe.delegate.Err(), probe.iterationErr)
}

func (probe *queryBreadthRowsProbe) Close() error {
	probe.closeAttempts++
	err := probe.delegate.Close()
	if probe.fault == queryBreadthFaultClose {
		return errors.Join(err, errQueryBreadthClose)
	}
	return err
}

func queryBreadthRunFaultCase(
	ctx context.Context,
	backend *sqlite.Backend,
	test queryBreadthFaultCase,
) (protocol.Value, protocol.Value, error) {
	recorder := &queryBreadthSQLRecorder{}
	observed := &queryBreadthObservedQueryer{
		delegate:  backend,
		recorder:  recorder,
		fault:     test.mode,
		probeRows: true,
	}
	source := models.ArticleObjects.Using(observed).OrderBy(models.ArticleFields.ID.Asc())
	checkpoint := recorder.checkpoint()
	var result protocol.Value
	switch test.name {
	case "consumer_stop":
		var first int64
		err := source.Iterate(ctx, func(article models.Article) error {
			first = article.ID
			return errQueryBreadthConsumerStop
		})
		if !errors.Is(err, errQueryBreadthConsumerStop) {
			return protocol.Value{}, protocol.Value{}, fmt.Errorf("consumer stop error = %v", err)
		}
		result = protocol.Object(map[string]protocol.Value{
			"first":   queryBreadthPrimaryKey(first),
			"outcome": protocol.String("consumer_stopped"),
		})
	case "decode_failure":
		_, err := orm.SelectInto(ctx, source, orm.Project1(models.ArticleFields.ID, func(id int64) int64 { return id }))
		if !errors.Is(err, errQueryBreadthDecode) {
			return protocol.Value{}, protocol.Value{}, fmt.Errorf("decode failure error = %v", err)
		}
		result = queryBreadthTerminalError("decode_error", "conversion")
	case "iteration_failure":
		_, err := orm.SelectInto(ctx, source, orm.Project1(models.ArticleFields.ID, func(id int64) int64 { return id }))
		if !errors.Is(err, errQueryBreadthIteration) {
			return protocol.Value{}, protocol.Value{}, fmt.Errorf("iteration failure error = %v", err)
		}
		result = queryBreadthTerminalError("backend_error", "iteration")
	case "close_failure":
		_, err := orm.SelectInto(ctx, source, orm.Project1(models.ArticleFields.ID, func(id int64) int64 { return id }))
		if !errors.Is(err, errQueryBreadthClose) {
			return protocol.Value{}, protocol.Value{}, fmt.Errorf("close failure error = %v", err)
		}
		result = queryBreadthTerminalError("backend_error", "close")
	default:
		return protocol.Value{}, protocol.Value{}, fmt.Errorf("unknown terminal failure case %q", test.name)
	}
	probe, err := observed.singleProbe()
	if err != nil {
		return protocol.Value{}, protocol.Value{}, err
	}
	metric, err := recorder.metricSince(checkpoint, map[string]protocol.Value{
		"close_attempts": protocol.Integer(strconv.Itoa(probe.closeAttempts)),
		"name":           protocol.String(test.name),
	})
	if err != nil {
		return protocol.Value{}, protocol.Value{}, err
	}
	return result, metric, nil
}

func queryBreadthTerminalError(category, code string) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"error": protocol.Object(map[string]protocol.Value{
			"category": protocol.String(category),
			"code":     protocol.String(code),
		}),
	})
}
