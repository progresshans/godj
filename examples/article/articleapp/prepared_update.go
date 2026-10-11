package articleapp

import (
	"context"
	"slices"

	"github.com/progresshans/godj/db"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/validation"
)

// UpdateAndPrepare prepares an application result from the final detached row
// before the existing mutation hook and commit. It also runs for a no-op, which
// still skips the mutation hook. prepare is request-local and must not retain
// borrowed resources. An encoding/validation failure rolls back this update.
// A result is returned only after one joined callback and confirmed commit;
// cancellation after that confirmation does not turn success into a retry.
func UpdateAndPrepare[T any](ctx context.Context, repository Repository, id int64, input Input, prepare func(context.Context, Article, []string) (T, error)) (T, error) {
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
	patch := (Patch{}).WithTitle(input.Title).WithPublished(input.Published)
	if input.Summary == nil {
		patch = patch.WithSummaryNull()
	} else {
		patch = patch.WithSummary(*input.Summary)
	}
	if input.Slug == nil {
		patch = patch.WithSlugNull()
	} else {
		patch = patch.WithSlug(*input.Slug)
	}
	return updateAndPrepare(ctx, repository, id, patch, MutationUpdate, prepare)
}

// PatchAndPrepare is the partial-input counterpart of UpdateAndPrepare. The
// preparation callback runs on the effective current row even for an empty
// patch, without manufacturing a mutation or invoking the mutation hook.
func PatchAndPrepare[T any](ctx context.Context, repository Repository, id int64, patch Patch, prepare func(context.Context, Article, []string) (T, error)) (T, error) {
	var zero T
	if prepare == nil {
		return zero, invalid("prepare", "result preparation is required")
	}
	if err := validateContext(ctx); err != nil {
		return zero, err
	}
	if interfaceNil(repository.backend) {
		return zero, invalid("backend", "repository is zero or invalid")
	}
	if id <= 0 {
		return zero, invalid("id", "id must be positive")
	}
	if err := validatePatch(patch); err != nil {
		return zero, err
	}
	return updateAndPrepare(ctx, repository, id, patch, MutationPatch, prepare)
}

func updateAndPrepare[T any](ctx context.Context, repository Repository, id int64, patch Patch, operation MutationOperation, prepare func(context.Context, Article, []string) (T, error)) (T, error) {
	return preparedAtomic(ctx, repository, operation, func(work context.Context, session db.Session) (T, error) {
		var zero T
		article, changed, err := repository.updateIn(work, session, id, patch, operation)
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
		if err := work.Err(); err != nil {
			return zero, err
		}
		return result, nil
	})
}

func (r Repository) updateIn(ctx context.Context, session db.Session, id int64, patch Patch, operation MutationOperation) (Article, []string, error) {
	current, found, err := getModel(ctx, session, id)
	if err != nil {
		return Article{}, nil, err
	}
	if !found {
		return Article{}, nil, notFound(id)
	}
	changed := patchChangedFields(current, patch)
	if len(changed) == 0 {
		return snapshot(current), nil, nil
	}
	modelPatch := articlemodels.ArticlePatch{}
	if operation == MutationUpdate || patch.title.supplied && current.Title != patch.title.value {
		modelPatch = modelPatch.WithTitle(patch.title.value)
	}
	if operation == MutationUpdate || patch.published.supplied && current.Published != patch.published.value {
		modelPatch = modelPatch.WithPublished(patch.published.value)
	}
	if operation == MutationUpdate || patch.summary.supplied && !patchSummaryEquals(current.Summary, patch.summary) {
		if patch.summary.null {
			modelPatch = modelPatch.WithSummaryNull()
		} else {
			modelPatch = modelPatch.WithSummary(patch.summary.value)
		}
	}
	if operation == MutationUpdate || patch.slug.supplied && !patchSummaryEquals(current.Slug, patch.slug) {
		if patch.slug.null {
			modelPatch = modelPatch.WithSlugNull()
		} else {
			modelPatch = modelPatch.WithSlug(patch.slug.value)
		}
	}
	violations, err := articlemodels.ArticleObjects.ValidateUniqueUpdate(ctx, session, current, modelPatch)
	if err != nil {
		return Article{}, nil, err
	}
	if !violations.Empty() {
		return Article{}, nil, validation.Reject(violations, nil)
	}
	updated, err := articlemodels.ArticleObjects.Patch(ctx, session, current, modelPatch)
	if err != nil {
		return Article{}, nil, writeRejection(err)
	}
	return snapshot(updated), changed, nil
}
