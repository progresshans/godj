package godj

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type databaseScenario func(context.Context, *sqlite.Backend) (protocol.Observation, error)

func withArticleDatabase(ctx context.Context, contractID string, scenario databaseScenario) (protocol.Observation, error) {
	backend, err := openArticleDatabase(ctx, contractID)
	if err != nil {
		return protocol.Observation{}, err
	}
	observation, scenarioErr := scenario(ctx, backend)
	closeErr := backend.Close()
	if scenarioErr != nil {
		return protocol.Observation{}, errors.Join(scenarioErr, closeErr)
	}
	if closeErr != nil {
		return protocol.Observation{}, closeErr
	}
	return observation, nil
}

func queryExact(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		articles, err := models.ArticleObjects.Using(backend).
			Filter(models.ArticleFields.Title.Exact("Alpine Guide")).
			OrderBy(models.ArticleFields.ID.Asc()).
			All(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, articleList(articles), nil)
	})
}

func queryASCIIInsensitiveContains(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		articles, err := models.ArticleObjects.Using(backend).
			Filter(models.ArticleFields.Title.IContains("django")).
			OrderBy(models.ArticleFields.ID.Asc()).
			All(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, articleList(articles), nil)
	})
}

func queryChainedAnd(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		articles, err := models.ArticleObjects.Using(backend).
			Filter(models.ArticleFields.Title.IContains("django")).
			Filter(models.ArticleFields.Published.Exact(true)).
			OrderBy(models.ArticleFields.ID.Asc()).
			All(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, articleList(articles), nil)
	})
}

func queryChainPreservesSource(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		base := models.ArticleObjects.Using(backend).
			Filter(models.ArticleFields.Published.Exact(true))
		derived := base.Filter(models.ArticleFields.Title.Exact("Django Deep Dive"))

		baseArticles, err := base.OrderBy(models.ArticleFields.ID.Asc()).All(ctx)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("evaluate base query: %w", err)
		}
		derivedArticles, err := derived.OrderBy(models.ArticleFields.ID.Asc()).All(ctx)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("evaluate derived query: %w", err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"base":    articleList(baseArticles),
			"derived": articleList(derivedArticles),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, nil)
	})
}

func queryOrderAndLimit(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		querySet := models.ArticleObjects.Using(backend).
			OrderBy(models.ArticleFields.Title.Desc(), models.ArticleFields.ID.Asc())
		querySet, err := querySet.Limit(2)
		if err != nil {
			return protocol.Observation{}, err
		}
		articles, err := querySet.All(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, articleList(articles), nil)
	})
}

func queryEmptyResult(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		articles, err := models.ArticleObjects.Using(backend).
			Filter(models.ArticleFields.Title.Exact("missing")).
			OrderBy(models.ArticleFields.ID.Asc()).
			All(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, articleList(articles), nil)
	})
}

func queryIsNull(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		base := models.ArticleObjects.Using(backend)
		nonNull, err := base.
			Filter(models.ArticleFields.Summary.IsNull(false)).
			OrderBy(models.ArticleFields.ID.Asc()).
			All(ctx)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("evaluate isnull=false query: %w", err)
		}
		null, err := base.
			Filter(models.ArticleFields.Summary.IsNull(true)).
			OrderBy(models.ArticleFields.ID.Asc()).
			All(ctx)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("evaluate isnull=true query: %w", err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"false": articleList(nonNull),
			"true":  articleList(null),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, result, nil)
	})
}

func queryUnknownField(ctx context.Context, contractID string) (protocol.Observation, error) {
	return dynamicErrorObservation(ctx, contractID, orm.LookupInput{Key: "unknown_field", Value: "value"}, query.CodeUnknownField)
}

func queryUnsupportedLookup(ctx context.Context, contractID string) (protocol.Observation, error) {
	return dynamicErrorObservation(ctx, contractID, orm.LookupInput{Key: "title__starts", Value: "Django"}, query.CodeUnsupportedLookup)
}

func dynamicErrorObservation(ctx context.Context, contractID string, input orm.LookupInput, expectedCode string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		before := backend.QueryCount()
		_, parseErr := orm.ParseDynamic(models.ArticleDescriptor{}, nil, []orm.LookupInput{input})
		after := backend.QueryCount()
		if after != before {
			return protocol.Observation{}, fmt.Errorf("dynamic lookup construction issued %d query calls", after-before)
		}
		var queryErr *query.Error
		if !errors.As(parseErr, &queryErr) {
			return protocol.Observation{}, fmt.Errorf("dynamic lookup error = %v, want *query.Error", parseErr)
		}
		if queryErr.Code != expectedCode {
			return protocol.Observation{}, fmt.Errorf("dynamic lookup error code = %q, want %q", queryErr.Code, expectedCode)
		}
		state, err := readDatabaseState(ctx, backend)
		if err != nil {
			return protocol.Observation{}, err
		}
		return protocol.Observation{
			ID:     contractID,
			Status: protocol.StatusObserved,
			Phase:  protocol.PhaseConstruction,
			Error: &protocol.ObservedError{
				Category:          queryErr.Category,
				Code:              queryErr.Code,
				Message:           queryErr.Error(),
				MessageIsContract: boolPointer(false),
			},
			DBState: valuePointer(state),
		}, nil
	})
}

func queryConstructionHasNoIO(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		before := backend.QueryCount()
		querySet := models.ArticleObjects.Using(backend).
			Filter(models.ArticleFields.Published.Exact(true)).
			OrderBy(models.ArticleFields.ID.Asc())
		querySet, err := querySet.Limit(2)
		if err != nil {
			return protocol.Observation{}, err
		}
		queriesDuringConstruction := backend.QueryCount() - before
		if queriesDuringConstruction != 0 {
			return protocol.Observation{}, fmt.Errorf("query construction issued %d query calls", queriesDuringConstruction)
		}
		// Keep the value live so this observation verifies construction rather
		// than a compiler-elided expression whose result is discarded.
		constructed := querySet.Plan().Table() != ""
		result := protocol.Object(map[string]protocol.Value{
			"queryset_constructed": protocol.Boolean(constructed),
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"queries_during_construction": protocol.Integer(fmt.Sprint(queriesDuringConstruction)),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseConstruction, result, &metrics)
	})
}

func schemaModelMetadata(contractID string) (protocol.Observation, error) {
	result, err := metadataValue((models.ArticleDescriptor{}).Metadata())
	if err != nil {
		return protocol.Observation{}, err
	}
	return protocol.Observation{
		ID:     contractID,
		Status: protocol.StatusObserved,
		Phase:  protocol.PhaseMetadata,
		Result: valuePointer(result),
	}, nil
}

func resultObservation(
	ctx context.Context,
	backend *sqlite.Backend,
	contractID string,
	phase protocol.Phase,
	result protocol.Value,
	metrics *protocol.Value,
) (protocol.Observation, error) {
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
		Metrics: metrics,
	}, nil
}

func readDatabaseState(ctx context.Context, backend *sqlite.Backend) (protocol.Value, error) {
	articles, err := models.ArticleObjects.Using(backend).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return protocol.Value{}, fmt.Errorf("read database state: %w", err)
	}
	return databaseState(articles), nil
}
