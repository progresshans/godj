package articleapp

import (
	"context"
	"slices"

	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
)

// UpdateAndPrepare prepares the final detached row before the mutation hook
// and commit, including a no-op. Input presence comes from its transport's
// full-input policy; excluded fields come from the fresh row in this write.
func UpdateAndPrepare[T any](ctx context.Context, repository Repository, id int64, input orm.ModelInput[articlemodels.Article], prepare func(context.Context, articlemodels.Article, []string) (T, error)) (T, error) {
	return updateAndPrepare(ctx, repository, id, input, MutationUpdate, prepare)
}

// PatchAndPrepare uses the same preparation path with partial-input presence
// and the explicit patch audit action. An empty input still checks the target
// and prepares output, without manufacturing a mutation or invoking its hook.
func PatchAndPrepare[T any](ctx context.Context, repository Repository, id int64, input orm.ModelInput[articlemodels.Article], prepare func(context.Context, articlemodels.Article, []string) (T, error)) (T, error) {
	return updateAndPrepare(ctx, repository, id, input, MutationPatch, prepare)
}

func updateAndPrepare[T any](ctx context.Context, repository Repository, id int64, input orm.ModelInput[articlemodels.Article], operation MutationOperation, prepare func(context.Context, articlemodels.Article, []string) (T, error)) (T, error) {
	var zero T
	if prepare == nil {
		return zero, invalid("prepare", "result preparation is required")
	}
	if err := repository.validateWrite(ctx, input); err != nil {
		return zero, err
	}
	if id <= 0 {
		return zero, invalid("id", "id must be positive")
	}
	return preparedAtomic(ctx, repository, operation, func(work context.Context, session db.Session) (T, error) {
		article, changed, err := repository.updateIn(work, session, id, input)
		if err != nil {
			return zero, err
		}
		result, err := prepare(work, cloneArticle(article), slices.Clone(changed))
		if err != nil {
			return zero, err
		}
		if err := work.Err(); err != nil {
			return zero, err
		}
		if len(changed) != 0 && repository.mutationHook != nil {
			if err := repository.mutationHook(work, session, mutationResult(operation, article, changed)); err != nil {
				return zero, err
			}
		}
		return result, nil
	})
}

func (r Repository) updateIn(ctx context.Context, session db.Session, id int64, input orm.ModelInput[articlemodels.Article]) (articlemodels.Article, []string, error) {
	mutation, err := r.resource.UpdateInSession(ctx, session, id, input, crud.SaveOptions[articlemodels.Article]{})
	if err == crud.ErrNotFound {
		return articlemodels.Article{}, nil, notFound(id)
	}
	if err != nil {
		return articlemodels.Article{}, nil, writeRejection(err)
	}
	return mutation.Value, mutation.ChangedFields, nil
}
