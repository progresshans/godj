package articleapp

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/progresshans/godj/db"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
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
	var zero T
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var prepared T
	var callbackError error
	entries, completed, sealed := 0, false, false
	err := repository.backend.Atomic(ctx, func(session db.Session) error {
		mu.Lock()
		if sealed {
			mu.Unlock()
			return errors.New("article: update callback outlived its owner")
		}
		entries++
		if entries != 1 {
			mu.Unlock()
			return errors.New("article: repeated update callback")
		}
		mu.Unlock()
		value, failure := func() (T, error) {
			if interfaceNil(session) {
				return zero, errors.New("article: update session is absent")
			}
			if err := work.Err(); err != nil {
				return zero, err
			}
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
		}()
		mu.Lock()
		prepared, callbackError, completed = value, failure, true
		mu.Unlock()
		return failure
	})
	mu.Lock()
	sealed = true
	valid := entries <= 1 && (entries == 0 && err != nil || completed)
	result, callbackErr := prepared, callbackError
	mu.Unlock()
	if !valid || callbackErr != nil && (err == nil || !errors.Is(err, callbackErr)) {
		failure := &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown,
			Detail: "Article update transaction did not confirm one complete callback", Cause: errors.Join(err, callbackErr)}
		return zero, mutationError(ctx, string(operation), failure)
	}
	if err != nil {
		return zero, mutationError(ctx, string(operation), err)
	}
	return result, nil
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
