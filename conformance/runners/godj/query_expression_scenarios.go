package godj

import (
	"context"
	"fmt"
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

type queryExpressionScenario func(context.Context, string) (protocol.Observation, error)

var queryExpressionScenarioRegistry = map[string]queryExpressionScenario{
	"django.query.expression.scalar_exact_or":                                  queryExpressionScalarExactOr,
	"django.query.expression.escaped_ascii_icontains_or":                       queryExpressionEscapedASCIIInsensitiveContainsOr,
	"django.query.expression.grouped_or_and_reuse":                             queryExpressionGroupedOrAndReuse,
	"django.query.expression.nonnull_scalar_not":                               queryExpressionNonNullScalarNot,
	"django.query.expression.nullable_negation_truth_table":                    queryExpressionNullableNegationTruthTable,
	"django.query.expression.implicit_filter_and":                              queryExpressionImplicitFilterAnd,
	"django.query.expression.nested_connector_order_and_source_independence":   queryExpressionNestedConnectorOrderAndSourceIndependence,
	"django.query.expression.composite_distinct_stable_page":                   queryExpressionCompositeDistinctStablePage,
	"django.query.expression.projection_outside_predicate":                     queryExpressionProjectionOutsidePredicate,
	"django.query.expression.composite_count_max":                              queryExpressionCompositeCountMax,
	"django.query.expression.integer_gt_literal_boundary":                      queryExpressionIntegerGreaterThanLiteralBoundary,
	"django.query.expression.integer_gte_literal_boundary":                     queryExpressionIntegerGreaterThanOrEqualLiteralBoundary,
	"django.query.expression.integer_lt_literal_boundary":                      queryExpressionIntegerLessThanLiteralBoundary,
	"django.query.expression.integer_lte_literal_boundary":                     queryExpressionIntegerLessThanOrEqualLiteralBoundary,
	"django.query.expression.range_composition_negation_and_reuse":             queryExpressionRangeCompositionNegationAndReuse,
	"django.query.expression.same_field_reference_boundaries":                  queryExpressionSameFieldReferenceBoundaries,
	"django.query.expression.same_model_field_reference_and_nullable_negation": queryExpressionSameModelFieldReferenceAndNullableNegation,
	"django.query.expression.nullable_ordering_negation_truth_table":           queryExpressionNullableOrderingNegationTruthTable,
	"django.query.expression.field_reference_stable_projection":                queryExpressionFieldReferenceStableProjection,
	"django.query.expression.field_reference_count_max":                        queryExpressionFieldReferenceCountMax,
}

// queryExpressionScenarioHandler is the only query-expression registry
// boundary consumed by the shared runner. A name must match one of the twenty
// contract scenarios exactly; near misses remain unregistered and fail closed.
func queryExpressionScenarioHandler(scenario string) (scenarioHandler, bool) {
	run, ok := queryExpressionScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		return run(ctx, contract.ID)
	}, true
}

func queryExpressionScalarExactOr(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		source := models.ArticleObjects.Using(queryExpressionObserved(backend, recorder)).
			Filter(orm.Or(
				models.ArticleFields.Title.Exact("Alpine Guide"),
				models.ArticleFields.Title.Exact("Other"),
			)).
			OrderBy(models.ArticleFields.ID.Asc())
		rows, metrics, err := queryExpressionCapture(recorder, func() ([]int64, error) {
			return queryExpressionPrimaryKeyRows(ctx, source)
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("scalar exact OR: %w", err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"operator": protocol.String("or"),
			"rows":     queryBreadthPrimaryKeys(rows),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

func queryExpressionEscapedASCIIInsensitiveContainsOr(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		summary := "literal markers"
		if err := queryBreadthInsertArticle(ctx, backend, 5, "100%_Coverage", true, &summary); err != nil {
			return protocol.Observation{}, err
		}
		needle := "%_"
		before := needle
		recorder := &queryExpressionSQLRecorder{}
		source := models.ArticleObjects.Using(queryExpressionObserved(backend, recorder)).
			Filter(orm.Or(
				models.ArticleFields.Title.IContains(needle),
				models.ArticleFields.Summary.IContains("orm"),
			)).
			OrderBy(models.ArticleFields.ID.Asc())
		rows, metrics, err := queryExpressionCapture(recorder, func() ([]int64, error) {
			return queryExpressionPrimaryKeyRows(ctx, source)
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("escaped ASCII icontains OR: %w", err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"input_after":  protocol.String(needle),
			"input_before": protocol.String(before),
			"rows":         queryBreadthPrimaryKeys(rows),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

func queryExpressionGroupedOrAndReuse(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		predicate := orm.Or(
			models.ArticleFields.Title.IContains("django"),
			models.ArticleFields.Title.Exact("Other"),
		)
		source := models.ArticleObjects.Using(observed).Filter(predicate)

		published, publishedMetric, err := queryExpressionCaptureStep(recorder, "published", func() ([]int64, error) {
			return queryExpressionPrimaryKeyRows(ctx, source.
				Filter(models.ArticleFields.Published.Exact(true)).
				OrderBy(models.ArticleFields.ID.Asc()))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		unpublished, unpublishedMetric, err := queryExpressionCaptureStep(recorder, "unpublished", func() ([]int64, error) {
			return queryExpressionPrimaryKeyRows(ctx, source.
				Filter(models.ArticleFields.Published.Exact(false)).
				OrderBy(models.ArticleFields.ID.Asc()))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		return queryBreadthStepsObservation(
			ctx,
			backend,
			contractID,
			protocol.PhaseEvaluation,
			[]protocol.Value{
				queryBreadthResultStep("published", queryBreadthPrimaryKeys(published)),
				queryBreadthResultStep("unpublished", queryBreadthPrimaryKeys(unpublished)),
			},
			[]protocol.Value{publishedMetric, unpublishedMetric},
		)
	})
}

func queryExpressionNonNullScalarNot(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		source := models.ArticleObjects.Using(queryExpressionObserved(backend, recorder)).
			Filter(orm.Not(models.ArticleFields.Title.IContains("django"))).
			OrderBy(models.ArticleFields.ID.Asc())
		rows, metrics, err := queryExpressionCapture(recorder, func() ([]int64, error) {
			return queryExpressionPrimaryKeyRows(ctx, source)
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("non-null scalar NOT: %w", err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"operator": protocol.String("not"),
			"rows":     queryBreadthPrimaryKeys(rows),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

func queryExpressionNullableNegationTruthTable(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		cases := []struct {
			name      string
			predicate orm.Predicate[models.Article]
		}{
			{name: "not_exact_orm", predicate: orm.Not(models.ArticleFields.Summary.Exact("ORM"))},
			{name: "not_icontains_orm", predicate: orm.Not(models.ArticleFields.Summary.IContains("orm"))},
			{name: "not_isnull_true", predicate: orm.Not(models.ArticleFields.Summary.IsNull(true))},
			{name: "not_isnull_false", predicate: orm.Not(models.ArticleFields.Summary.IsNull(false))},
		}
		resultSteps := make([]protocol.Value, 0, len(cases))
		metricSteps := make([]protocol.Value, 0, len(cases))
		for _, test := range cases {
			rows, metric, err := queryExpressionCaptureStep(recorder, test.name, func() ([]int64, error) {
				return queryExpressionPrimaryKeyRows(ctx, models.ArticleObjects.Using(observed).
					Filter(test.predicate).
					OrderBy(models.ArticleFields.ID.Asc()))
			})
			if err != nil {
				return protocol.Observation{}, err
			}
			resultSteps = append(resultSteps, queryBreadthResultStep(test.name, queryBreadthPrimaryKeys(rows)))
			metricSteps = append(metricSteps, metric)
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, metricSteps)
	})
}

func queryExpressionImplicitFilterAnd(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		variadic, variadicMetric, err := queryExpressionCaptureStep(recorder, "variadic_filter", func() ([]int64, error) {
			return queryExpressionPrimaryKeyRows(ctx, models.ArticleObjects.Using(observed).
				Filter(
					models.ArticleFields.Title.IContains("django"),
					models.ArticleFields.Published.Exact(true),
				).
				OrderBy(models.ArticleFields.ID.Asc()))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		repeated, repeatedMetric, err := queryExpressionCaptureStep(recorder, "repeated_filter", func() ([]int64, error) {
			return queryExpressionPrimaryKeyRows(ctx, models.ArticleObjects.Using(observed).
				Filter(models.ArticleFields.Title.IContains("django")).
				Filter(models.ArticleFields.Published.Exact(true)).
				OrderBy(models.ArticleFields.ID.Asc()))
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		return queryBreadthStepsObservation(
			ctx,
			backend,
			contractID,
			protocol.PhaseEvaluation,
			[]protocol.Value{
				queryBreadthResultStep("variadic_filter", queryBreadthPrimaryKeys(variadic)),
				queryBreadthResultStep("repeated_filter", queryBreadthPrimaryKeys(repeated)),
			},
			[]protocol.Value{variadicMetric, repeatedMetric},
		)
	})
}

func queryExpressionNestedConnectorOrderAndSourceIndependence(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		predicate := orm.Or(
			models.ArticleFields.Title.IContains("django"),
			models.ArticleFields.Title.Exact("Other"),
		)
		base := models.ArticleObjects.Using(observed).Filter(models.ArticleFields.Published.Exact(true))
		first := base.Filter(predicate)
		second := base.Filter(orm.Or(
			models.ArticleFields.Title.Exact("Alpine Guide"),
			models.ArticleFields.Title.Exact("Other"),
		))

		steps := []struct {
			name   string
			source orm.QuerySet[models.Article]
		}{
			{name: "first_derived", source: first},
			{name: "second_derived", source: second},
			{name: "base_after_derivation", source: base},
			{name: "reused_predicate", source: models.ArticleObjects.Using(observed).Filter(
				predicate,
				models.ArticleFields.Published.Exact(false),
			)},
		}
		resultSteps := make([]protocol.Value, 0, len(steps))
		metricSteps := make([]protocol.Value, 0, len(steps))
		for _, step := range steps {
			rows, metric, err := queryExpressionCaptureStep(recorder, step.name, func() ([]int64, error) {
				return queryExpressionPrimaryKeyRows(ctx, step.source.OrderBy(models.ArticleFields.ID.Asc()))
			})
			if err != nil {
				return protocol.Observation{}, err
			}
			resultSteps = append(resultSteps, queryBreadthResultStep(step.name, queryBreadthPrimaryKeys(rows)))
			metricSteps = append(metricSteps, metric)
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, metricSteps)
	})
}

func queryExpressionCompositeDistinctStablePage(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		source := models.ArticleObjects.Using(queryExpressionObserved(backend, recorder)).
			Filter(orm.Or(
				models.ArticleFields.Title.IContains("django"),
				models.ArticleFields.Published.Exact(true),
			)).
			OrderBy(models.ArticleFields.ID.Asc()).
			Distinct()
		page, err := queryBreadthPaginate(source, 1, 2)
		if err != nil {
			return protocol.Observation{}, err
		}
		rows, metrics, err := queryExpressionCapture(recorder, func() ([]queryExpressionProjectedRow, error) {
			return queryExpressionProjectedRows(ctx, page)
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("composite distinct stable page: %w", err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"fields": queryBreadthStrings("id", "title"),
			"rows":   queryExpressionProjectedRowValues(rows),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

func queryExpressionProjectionOutsidePredicate(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		source := models.ArticleObjects.Using(queryExpressionObserved(backend, recorder)).
			Filter(orm.Or(
				models.ArticleFields.Summary.IsNull(true),
				models.ArticleFields.Published.Exact(false),
			)).
			OrderBy(models.ArticleFields.ID.Asc())
		rows, metrics, err := queryExpressionCapture(recorder, func() ([]queryExpressionProjectedRow, error) {
			return queryExpressionProjectedRows(ctx, source)
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("projection outside predicate: %w", err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"filter_fields":     queryBreadthStrings("summary", "published"),
			"projection_fields": queryBreadthStrings("id", "title"),
			"rows":              queryExpressionProjectedRowValues(rows),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

func queryExpressionCompositeCountMax(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		nonempty := models.ArticleObjects.Using(observed).Filter(orm.Or(
			models.ArticleFields.Title.IContains("django"),
			models.ArticleFields.Summary.IsNull(true),
		))
		empty := models.ArticleObjects.Using(observed).Filter(orm.Or(
			models.ArticleFields.Title.Exact("missing"),
			models.ArticleFields.Summary.Exact("missing"),
		))

		nonemptyValue, nonemptyMetric, err := queryExpressionCaptureStep(recorder, "nonempty", func() (queryExpressionAggregate, error) {
			return queryExpressionAggregateValues(ctx, nonempty)
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		emptyValue, emptyMetric, err := queryExpressionCaptureStep(recorder, "empty", func() (queryExpressionAggregate, error) {
			return queryExpressionAggregateValues(ctx, empty)
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		return queryBreadthStepsObservation(
			ctx,
			backend,
			contractID,
			protocol.PhaseEvaluation,
			[]protocol.Value{
				queryBreadthResultStep("nonempty", queryExpressionAggregateValue(nonemptyValue)),
				queryBreadthResultStep("empty", queryExpressionAggregateValue(emptyValue)),
			},
			[]protocol.Value{nonemptyMetric, emptyMetric},
		)
	})
}

func queryExpressionIntegerLiteralBoundary(
	ctx context.Context,
	contractID string,
	lookup string,
	rhs int64,
	predicate orm.Predicate[models.Article],
) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		source := models.ArticleObjects.Using(queryExpressionObserved(backend, recorder)).
			Filter(predicate).
			OrderBy(models.ArticleFields.ID.Asc())
		rows, metrics, err := queryExpressionCapture(recorder, func() ([]int64, error) {
			return queryExpressionPrimaryKeyRows(ctx, source)
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("integer %s literal boundary: %w", lookup, err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"lookup": protocol.String(lookup),
			"rhs":    queryBreadthInteger(rhs),
			"rows":   queryBreadthPrimaryKeys(rows),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

func queryExpressionIntegerGreaterThanLiteralBoundary(ctx context.Context, contractID string) (protocol.Observation, error) {
	return queryExpressionIntegerLiteralBoundary(
		ctx,
		contractID,
		"gt",
		2,
		models.ArticleFields.ID.GreaterThan(2),
	)
}

func queryExpressionIntegerGreaterThanOrEqualLiteralBoundary(ctx context.Context, contractID string) (protocol.Observation, error) {
	return queryExpressionIntegerLiteralBoundary(
		ctx,
		contractID,
		"gte",
		2,
		models.ArticleFields.ID.GreaterThanOrEqual(2),
	)
}

func queryExpressionIntegerLessThanLiteralBoundary(ctx context.Context, contractID string) (protocol.Observation, error) {
	return queryExpressionIntegerLiteralBoundary(
		ctx,
		contractID,
		"lt",
		3,
		models.ArticleFields.ID.LessThan(3),
	)
}

func queryExpressionIntegerLessThanOrEqualLiteralBoundary(ctx context.Context, contractID string) (protocol.Observation, error) {
	return queryExpressionIntegerLiteralBoundary(
		ctx,
		contractID,
		"lte",
		3,
		models.ArticleFields.ID.LessThanOrEqual(3),
	)
}

func queryExpressionRangeCompositionNegationAndReuse(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		predicate := orm.And(
			models.ArticleFields.ID.GreaterThan(1),
			models.ArticleFields.ID.LessThanOrEqual(3),
		)
		base := models.ArticleObjects.Using(observed)
		cases := []struct {
			name   string
			source orm.QuerySet[models.Article]
		}{
			{name: "explicit_q_range", source: base.Filter(predicate)},
			{name: "keyword_range", source: base.Filter(
				models.ArticleFields.ID.GreaterThan(1),
				models.ArticleFields.ID.LessThanOrEqual(3),
			)},
			{name: "negated_range", source: base.Filter(orm.Not(predicate))},
			{name: "reused_published", source: base.Filter(
				predicate,
				models.ArticleFields.Published.Exact(true),
			)},
		}
		resultSteps := make([]protocol.Value, 0, len(cases))
		metricSteps := make([]protocol.Value, 0, len(cases))
		for _, test := range cases {
			rows, metric, err := queryExpressionCaptureStep(recorder, test.name, func() ([]int64, error) {
				return queryExpressionPrimaryKeyRows(ctx, test.source.OrderBy(models.ArticleFields.ID.Asc()))
			})
			if err != nil {
				return protocol.Observation{}, err
			}
			queryBreadthAppendStep(
				&resultSteps,
				&metricSteps,
				test.name,
				queryBreadthPrimaryKeys(rows),
				metric,
			)
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, metricSteps)
	})
}

func queryExpressionSameFieldReferenceBoundaries(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		id := models.ArticleFields.ID
		idReference := orm.F(id)
		cases := []struct {
			name      string
			predicate orm.Predicate[models.Article]
		}{
			{name: "id_exact_id", predicate: id.ExactField(idReference)},
			{name: "id_gt_id", predicate: id.GreaterThanField(idReference)},
			{name: "id_gte_id", predicate: id.GreaterThanOrEqualField(idReference)},
			{name: "id_lt_id", predicate: id.LessThanField(idReference)},
			{name: "id_lte_id", predicate: id.LessThanOrEqualField(idReference)},
		}
		resultSteps := make([]protocol.Value, 0, len(cases))
		metricSteps := make([]protocol.Value, 0, len(cases))
		for _, test := range cases {
			rows, metric, err := queryExpressionCaptureStep(recorder, test.name, func() ([]int64, error) {
				return queryExpressionPrimaryKeyRows(ctx, models.ArticleObjects.Using(observed).
					Filter(test.predicate).
					OrderBy(models.ArticleFields.ID.Asc()))
			})
			if err != nil {
				return protocol.Observation{}, err
			}
			queryBreadthAppendStep(
				&resultSteps,
				&metricSteps,
				test.name,
				queryBreadthPrimaryKeys(rows),
				metric,
			)
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, metricSteps)
	})
}

func queryExpressionSameModelFieldReferenceAndNullableNegation(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		summary := "same"
		if err := queryBreadthInsertArticle(ctx, backend, 5, "same", false, &summary); err != nil {
			return protocol.Observation{}, err
		}
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		titleEqualsSummary := models.ArticleFields.Title.ExactField(orm.F(models.ArticleFields.Summary))
		cases := []struct {
			name      string
			predicate orm.Predicate[models.Article]
		}{
			{name: "cross_field_exact", predicate: titleEqualsSummary},
			{name: "cross_field_not_exact", predicate: orm.Not(titleEqualsSummary)},
			{name: "equal_row_gt", predicate: orm.And(
				models.ArticleFields.ID.Exact(5),
				models.ArticleFields.Title.GreaterThanField(orm.F(models.ArticleFields.Summary)),
			)},
			{name: "nullable_rhs_direct", predicate: orm.And(
				models.ArticleFields.ID.Exact(1),
				titleEqualsSummary,
			)},
		}
		resultSteps := make([]protocol.Value, 0, len(cases))
		metricSteps := make([]protocol.Value, 0, len(cases))
		for _, test := range cases {
			rows, metric, err := queryExpressionCaptureStep(recorder, test.name, func() ([]int64, error) {
				return queryExpressionPrimaryKeyRows(ctx, models.ArticleObjects.Using(observed).
					Filter(test.predicate).
					OrderBy(models.ArticleFields.ID.Asc()))
			})
			if err != nil {
				return protocol.Observation{}, err
			}
			queryBreadthAppendStep(
				&resultSteps,
				&metricSteps,
				test.name,
				queryBreadthPrimaryKeys(rows),
				metric,
			)
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, metricSteps)
	})
}

func queryExpressionNullableOrderingNegationTruthTable(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		cases := []struct {
			name      string
			predicate orm.Predicate[models.Article]
		}{
			{name: "not_gt_empty", predicate: orm.Not(models.ArticleFields.Summary.GreaterThan(""))},
			{name: "not_gte_empty", predicate: orm.Not(models.ArticleFields.Summary.GreaterThanOrEqual(""))},
			{name: "not_lt_orm", predicate: orm.Not(models.ArticleFields.Summary.LessThan("ORM"))},
			{name: "not_lte_orm", predicate: orm.Not(models.ArticleFields.Summary.LessThanOrEqual("ORM"))},
		}
		resultSteps := make([]protocol.Value, 0, len(cases))
		metricSteps := make([]protocol.Value, 0, len(cases))
		for _, test := range cases {
			rows, metric, err := queryExpressionCaptureStep(recorder, test.name, func() ([]int64, error) {
				return queryExpressionPrimaryKeyRows(ctx, models.ArticleObjects.Using(observed).
					Filter(test.predicate).
					OrderBy(models.ArticleFields.ID.Asc()))
			})
			if err != nil {
				return protocol.Observation{}, err
			}
			queryBreadthAppendStep(
				&resultSteps,
				&metricSteps,
				test.name,
				queryBreadthPrimaryKeys(rows),
				metric,
			)
		}
		return queryBreadthStepsObservation(ctx, backend, contractID, protocol.PhaseEvaluation, resultSteps, metricSteps)
	})
}

func queryExpressionFieldReferenceSource(queryer db.Queryer) orm.QuerySet[models.Article] {
	return models.ArticleObjects.Using(queryer).Filter(orm.And(
		models.ArticleFields.ID.GreaterThanOrEqual(2),
		orm.Or(
			models.ArticleFields.Summary.GreaterThanOrEqualField(orm.F(models.ArticleFields.Summary)),
			models.ArticleFields.Summary.IsNull(true),
		),
	))
}

func queryExpressionFieldReferenceStableProjection(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		source := queryExpressionFieldReferenceSource(queryExpressionObserved(backend, recorder)).
			OrderBy(models.ArticleFields.ID.Desc())
		rows, metrics, err := queryExpressionCapture(recorder, func() ([]queryExpressionProjectedRow, error) {
			return queryExpressionProjectedRows(ctx, source)
		})
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("field-reference stable projection: %w", err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"filter_fields":     queryBreadthStrings("id", "summary"),
			"order_fields":      queryBreadthStrings("-id"),
			"projection_fields": queryBreadthStrings("id", "title"),
			"rows":              queryExpressionProjectedRowValues(rows),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, &metrics)
	})
}

func queryExpressionFieldReferenceCountMax(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		recorder := &queryExpressionSQLRecorder{}
		observed := queryExpressionObserved(backend, recorder)
		nonempty := queryExpressionFieldReferenceSource(observed)
		empty := models.ArticleObjects.Using(observed).
			Filter(models.ArticleFields.ID.GreaterThanField(orm.F(models.ArticleFields.ID)))

		nonemptyValue, nonemptyMetric, err := queryExpressionCaptureStep(recorder, "nonempty", func() (queryExpressionAggregate, error) {
			return queryExpressionAggregateValues(ctx, nonempty)
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		emptyValue, emptyMetric, err := queryExpressionCaptureStep(recorder, "empty", func() (queryExpressionAggregate, error) {
			return queryExpressionAggregateValues(ctx, empty)
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		return queryBreadthStepsObservation(
			ctx,
			backend,
			contractID,
			protocol.PhaseEvaluation,
			[]protocol.Value{
				queryBreadthResultStep("nonempty", queryExpressionAggregateValue(nonemptyValue)),
				queryBreadthResultStep("empty", queryExpressionAggregateValue(emptyValue)),
			},
			[]protocol.Value{nonemptyMetric, emptyMetric},
		)
	})
}

type queryExpressionProjectedRow struct {
	id    int64
	title string
}

type queryExpressionAggregate struct {
	count    int64
	latestID orm.Optional[int64]
}

func queryExpressionPrimaryKeyRows(ctx context.Context, source orm.QuerySet[models.Article]) ([]int64, error) {
	return orm.SelectInto(ctx, source, orm.Project1(
		models.ArticleFields.ID,
		func(id int64) int64 { return id },
	))
}

func queryExpressionProjectedRows(ctx context.Context, source orm.QuerySet[models.Article]) ([]queryExpressionProjectedRow, error) {
	return orm.SelectInto(ctx, source, orm.Project2(
		models.ArticleFields.ID,
		models.ArticleFields.Title,
		func(id int64, title string) queryExpressionProjectedRow {
			return queryExpressionProjectedRow{id: id, title: title}
		},
	))
}

func queryExpressionProjectedRowValues(rows []queryExpressionProjectedRow) protocol.Value {
	values := make([]protocol.Value, len(rows))
	for index, row := range rows {
		values[index] = protocol.List(queryBreadthPrimaryKey(row.id), protocol.String(row.title))
	}
	return protocol.List(values...)
}

func queryExpressionAggregateValues(ctx context.Context, source orm.QuerySet[models.Article]) (queryExpressionAggregate, error) {
	return orm.AggregateInto(ctx, source, orm.Aggregate2(
		orm.CountRows[models.Article](),
		orm.Max(models.ArticleFields.ID),
		func(count int64, latestID orm.Optional[int64]) queryExpressionAggregate {
			return queryExpressionAggregate{count: count, latestID: latestID}
		},
	))
}

func queryExpressionAggregateValue(value queryExpressionAggregate) protocol.Value {
	latestID, present := value.latestID.Get()
	maximum := protocol.Null()
	if present {
		maximum = queryBreadthPrimaryKey(latestID)
	}
	return protocol.Object(map[string]protocol.Value{
		"fields": queryBreadthStrings("row_count", "latest_id"),
		"values": protocol.List(queryBreadthInteger(value.count), maximum),
	})
}

type queryExpressionSQLRecorder struct {
	mu         sync.Mutex
	statements []protocol.Value
}

func (recorder *queryExpressionSQLRecorder) checkpoint() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.statements)
}

func (recorder *queryExpressionSQLRecorder) record(statement protocol.Value) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.statements = append(recorder.statements, statement)
}

func (recorder *queryExpressionSQLRecorder) metricSince(checkpoint int, extra map[string]protocol.Value) (protocol.Value, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if checkpoint < 0 || checkpoint > len(recorder.statements) {
		return protocol.Value{}, fmt.Errorf("query-expression recorder checkpoint %d outside [0,%d]", checkpoint, len(recorder.statements))
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

type queryExpressionObservedQueryer struct {
	delegate db.Queryer
	recorder *queryExpressionSQLRecorder
}

var _ db.Queryer = (*queryExpressionObservedQueryer)(nil)

func queryExpressionObserved(delegate db.Queryer, recorder *queryExpressionSQLRecorder) db.Queryer {
	return &queryExpressionObservedQueryer{delegate: delegate, recorder: recorder}
}

func (observed *queryExpressionObservedQueryer) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		return nil, err
	}
	shape, err := queryExpressionStatementShape(statement, queryExpressionPredicateArguments(plan, arguments))
	if err != nil {
		return nil, err
	}
	observed.recorder.record(shape)
	return observed.delegate.Query(ctx, plan)
}

func queryExpressionPredicateArguments(plan query.Plan, arguments []any) []any {
	// Django's SQLite compiler renders LIMIT/OFFSET as SQL literals, while
	// GoDj binds them after every predicate argument. The comparison contract
	// observes predicate parameters, so remove only those known trailing
	// pagination bindings from the compiled argument stream.
	count := len(arguments)
	if _, ok := plan.Limit(); ok && count > 0 {
		count--
	}
	if _, ok := plan.Offset(); ok && count > 0 {
		count--
	}
	return append([]any(nil), arguments[:count]...)
}

var (
	queryExpressionAggregatePattern = regexp.MustCompile(`\b(COUNT|MAX)\s*\(`)
	queryExpressionDerivedPattern   = regexp.MustCompile(`\bFROM\s*\(`)
	queryExpressionDistinctPattern  = regexp.MustCompile(`\bSELECT\s+DISTINCT\b`)
	queryExpressionLimitPattern     = regexp.MustCompile(`\bLIMIT\b`)
	queryExpressionOffsetPattern    = regexp.MustCompile(`\bOFFSET\b`)
	queryExpressionAndPattern       = regexp.MustCompile(`\bAND\b`)
	queryExpressionNotPattern       = regexp.MustCompile(`\bNOT\s*\(`)
	queryExpressionOrPattern        = regexp.MustCompile(`\bOR\b`)
	queryExpressionIsNotNullPattern = regexp.MustCompile(`\bIS\s+NOT\s+NULL\b`)
	queryExpressionIsNullPattern    = regexp.MustCompile(`\bIS\s+NULL\b`)
)

func queryExpressionStatementShape(statement string, arguments []any) (protocol.Value, error) {
	rendered := strings.ToUpper(strings.Join(strings.Fields(statement), " "))
	kind := "EMPTY"
	if fields := strings.Fields(rendered); len(fields) != 0 {
		kind = fields[0]
	}
	present := make(map[string]bool, 2)
	for _, match := range queryExpressionAggregatePattern.FindAllStringSubmatch(rendered, -1) {
		present[match[1]] = true
	}
	aggregates := make([]protocol.Value, 0, 2)
	for _, function := range []string{"COUNT", "MAX"} {
		if present[function] {
			aggregates = append(aggregates, protocol.String(function))
		}
	}
	parameters := make([]protocol.Value, len(arguments))
	for index, argument := range arguments {
		value, err := queryExpressionParameterValue(argument)
		if err != nil {
			return protocol.Value{}, fmt.Errorf("normalize compiled query parameter %d: %w", index, err)
		}
		parameters[index] = value
	}
	return protocol.Object(map[string]protocol.Value{
		"aggregate_functions": protocol.List(aggregates...),
		"derived_table":       protocol.Boolean(queryExpressionDerivedPattern.MatchString(rendered)),
		"distinct":            protocol.Boolean(queryExpressionDistinctPattern.MatchString(rendered)),
		"has_limit":           protocol.Boolean(queryExpressionLimitPattern.MatchString(rendered)),
		"has_offset":          protocol.Boolean(queryExpressionOffsetPattern.MatchString(rendered)),
		"logical_operators": protocol.Object(map[string]protocol.Value{
			"and": protocol.Integer(strconv.Itoa(len(queryExpressionAndPattern.FindAllStringIndex(rendered, -1)))),
			"not": protocol.Integer(strconv.Itoa(len(queryExpressionNotPattern.FindAllStringIndex(rendered, -1)))),
			"or":  protocol.Integer(strconv.Itoa(len(queryExpressionOrPattern.FindAllStringIndex(rendered, -1)))),
		}),
		"null_predicates": protocol.Object(map[string]protocol.Value{
			"is_not_null": protocol.Integer(strconv.Itoa(len(queryExpressionIsNotNullPattern.FindAllStringIndex(rendered, -1)))),
			"is_null":     protocol.Integer(strconv.Itoa(len(queryExpressionIsNullPattern.FindAllStringIndex(rendered, -1)))),
		}),
		"parameters":     protocol.List(parameters...),
		"statement_kind": protocol.String(kind),
	}), nil
}

func queryExpressionParameterValue(value any) (protocol.Value, error) {
	switch value := value.(type) {
	case nil:
		return protocol.Null(), nil
	case bool:
		return protocol.Boolean(value), nil
	case string:
		return protocol.String(value), nil
	case int:
		return protocol.Integer(strconv.Itoa(value)), nil
	case int32:
		return protocol.Integer(strconv.FormatInt(int64(value), 10)), nil
	case int64:
		return protocol.Integer(strconv.FormatInt(value, 10)), nil
	default:
		return protocol.Value{}, fmt.Errorf("unsupported compiled query parameter type %T", value)
	}
}

func queryExpressionCapture[T any](recorder *queryExpressionSQLRecorder, operation func() (T, error)) (T, protocol.Value, error) {
	checkpoint := recorder.checkpoint()
	value, err := operation()
	if err != nil {
		var zero T
		return zero, protocol.Value{}, err
	}
	metric, err := recorder.metricSince(checkpoint, nil)
	return value, metric, err
}

func queryExpressionCaptureStep[T any](recorder *queryExpressionSQLRecorder, name string, operation func() (T, error)) (T, protocol.Value, error) {
	checkpoint := recorder.checkpoint()
	value, err := operation()
	if err != nil {
		var zero T
		return zero, protocol.Value{}, fmt.Errorf("capture query-expression step %s: %w", name, err)
	}
	metric, err := recorder.metricSince(checkpoint, map[string]protocol.Value{"name": protocol.String(name)})
	return value, metric, err
}
