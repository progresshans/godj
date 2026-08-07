package godj

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
)

func withEmptyArticleDatabase(ctx context.Context, contractID string, scenario databaseScenario) (protocol.Observation, error) {
	backend, err := openEmptyArticleDatabase(ctx, contractID)
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

func modelCreateAutoPrimaryKey(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		created, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Created").WithPublished(true).WithSummary("Written"),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		if _, present := (models.ArticleDescriptor{}).PrimaryKey(created); !present {
			return protocol.Observation{}, errors.New("created Article has no primary-key presence state")
		}
		result := protocol.Object(map[string]protocol.Value{
			"pk":  primaryKeyValue(created.ID),
			"row": articleValue(created),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, nil)
	})
}

func modelCreateNullableVariants(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		omitted, err := models.ArticleObjects.Create(ctx, backend, models.NewArticleCreate("Omitted"))
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("create omitted nullable value: %w", err)
		}
		explicitNull, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Explicit NULL").WithSummaryNull(),
		)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("create explicit NULL: %w", err)
		}
		empty, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Empty").WithSummary(""),
		)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("create empty string: %w", err)
		}
		result := protocol.Object(map[string]protocol.Value{
			"empty":         articleValue(empty),
			"explicit_null": articleValue(explicitNull),
			"omitted":       articleValue(omitted),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, nil)
	})
}

func modelPartialUpdateOmitted(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		created, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Before").WithSummary("Persisted"),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		memoryOnlySummary := "Memory only"
		created.Published = true
		created.Summary = &memoryOnlySummary
		if _, err := models.ArticleObjects.Update(ctx, backend, created, models.ArticlePatch{}.WithTitle("After")); err != nil {
			return protocol.Observation{}, err
		}
		persisted, err := models.ArticleObjects.Using(backend).
			Filter(models.ArticleFields.ID.Exact(created.ID)).
			All(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		if len(persisted) != 1 {
			return protocol.Observation{}, fmt.Errorf("partial update persisted rows = %d, want 1", len(persisted))
		}
		result := protocol.Object(map[string]protocol.Value{"persisted": articleValue(persisted[0])})
		return resultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, nil)
	})
}

func modelPartialUpdateExplicitNull(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		created, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Nullable").WithSummary("Before NULL"),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		updated, err := models.ArticleObjects.Update(ctx, backend, created, models.ArticlePatch{}.WithSummaryNull())
		if err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{"persisted": articleValue(updated)})
		return resultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, nil)
	})
}

func modelInstanceDelete(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		if _, err := models.ArticleObjects.Create(ctx, backend, models.NewArticleCreate("Keep me")); err != nil {
			return protocol.Observation{}, fmt.Errorf("create retained row: %w", err)
		}
		removed, err := models.ArticleObjects.Create(ctx, backend, models.NewArticleCreate("Delete me"))
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("create deleted row: %w", err)
		}
		key, present := (models.ArticleDescriptor{}).PrimaryKey(removed)
		if !present {
			return protocol.Observation{}, errors.New("deleted Article has no primary-key presence before delete")
		}
		keyInteger, ok := key.Integer()
		if !ok {
			return protocol.Observation{}, errors.New("deleted Article primary key is not an integer")
		}
		deletedCount, err := models.ArticleObjects.Delete(ctx, backend, &removed)
		if err != nil {
			return protocol.Observation{}, err
		}
		if removed.ID != 0 {
			return protocol.Observation{}, fmt.Errorf("deleted Article ID = %d, want zero", removed.ID)
		}
		if _, stillPresent := (models.ArticleDescriptor{}).PrimaryKey(removed); stillPresent {
			return protocol.Observation{}, errors.New("deleted Article primary-key presence was not cleared")
		}
		result := protocol.Object(map[string]protocol.Value{
			"deleted_count": protocol.Integer(strconv.FormatInt(deletedCount, 10)),
			"pk_after":      protocol.Null(),
			"pk_before":     primaryKeyValue(keyInteger),
		})
		return resultObservation(ctx, backend, contractID, protocol.PhaseCommit, result, nil)
	})
}

func transactionAtomicCommit(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		if _, err := models.ArticleObjects.Create(ctx, backend, models.NewArticleCreate("Before transaction")); err != nil {
			return protocol.Observation{}, err
		}
		var (
			created     models.Article
			countInside int
		)
		if err := backend.Atomic(ctx, func(session db.Session) error {
			var err error
			created, err = models.ArticleObjects.Create(ctx, session, models.NewArticleCreate("Committed"))
			if err != nil {
				return err
			}
			articles, err := models.ArticleObjects.Using(session).OrderBy(models.ArticleFields.ID.Asc()).All(ctx)
			if err != nil {
				return err
			}
			countInside = len(articles)
			return nil
		}); err != nil {
			return protocol.Observation{}, err
		}
		articles, err := models.ArticleObjects.Using(backend).OrderBy(models.ArticleFields.ID.Asc()).All(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{
			"count_after":  protocol.Integer(strconv.Itoa(len(articles))),
			"count_inside": protocol.Integer(strconv.Itoa(countInside)),
			"pk":           primaryKeyValue(created.ID),
		})
		return protocol.Observation{
			ID:      contractID,
			Status:  protocol.StatusObserved,
			Phase:   protocol.PhaseCommit,
			Result:  valuePointer(result),
			DBState: valuePointer(databaseState(articles)),
		}, nil
	})
}

var errForcedRollback = errors.New("forced rollback")

func transactionAtomicRollback(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withEmptyArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		sentinel, err := models.ArticleObjects.Create(
			ctx,
			backend,
			models.NewArticleCreate("Rollback sentinel").WithSummary("Preserved before transaction"),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		atomicErr := backend.Atomic(ctx, func(session db.Session) error {
			if _, err := models.ArticleObjects.Update(
				ctx,
				session,
				sentinel,
				models.ArticlePatch{}.WithTitle("Mutated").WithSummary("Mutated in transaction"),
			); err != nil {
				return err
			}
			if _, err := models.ArticleObjects.Create(ctx, session, models.NewArticleCreate("Transient")); err != nil {
				return err
			}
			return errForcedRollback
		})
		if atomicErr != errForcedRollback {
			return protocol.Observation{}, fmt.Errorf("atomic rollback error = %v, want exact forced rollback", atomicErr)
		}
		state, err := readDatabaseState(ctx, backend)
		if err != nil {
			return protocol.Observation{}, err
		}
		return protocol.Observation{
			ID:     contractID,
			Status: protocol.StatusObserved,
			Phase:  protocol.PhaseRollback,
			Error: &protocol.ObservedError{
				Category:          "application_error",
				Code:              "forced_rollback",
				Message:           atomicErr.Error(),
				MessageIsContract: boolPointer(false),
			},
			DBState: valuePointer(state),
		}, nil
	})
}

func primaryKeyValue(value int64) protocol.Value {
	return protocol.PrimaryKey(protocol.Integer(strconv.FormatInt(value, 10)))
}
