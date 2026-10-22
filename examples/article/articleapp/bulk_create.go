package articleapp

import (
	"context"
	"errors"
	"strconv"

	"github.com/progresshans/godj/db"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/validation"
)

const BulkCreateMaximum = 40
const bulkCreateBatchSize = 20

// BulkCreateAndPrepare validates every candidate and unique slug before any
// INSERT. Native batches borrow the same transaction, then all final stored
// rows are reloaded in input order. prepare receives a detached snapshot before
// the mutation hook and commit. Failure returns no partial application result.
// This bounded, atomic bulk policy belongs to Article, not to Django's default
// list serializer or the lower ORM BulkCreate operation.
func BulkCreateAndPrepare[T any](ctx context.Context, repository Repository, inputs []orm.ModelInput[articlemodels.Article], prepare func(context.Context, []articlemodels.Article) (T, error)) (T, error) {
	var zero T
	if err := validateContext(ctx); err != nil {
		return zero, err
	}
	if interfaceNil(repository.backend) || prepare == nil {
		return zero, invalid("bulk_create", "a repository and result preparation are required")
	}
	if len(inputs) < 1 || len(inputs) > BulkCreateMaximum {
		return zero, validation.Reject(validation.NewErrors(validation.New(validation.NonField, "invalid_count", validation.NewParam("min", "1"), validation.NewParam("max", strconv.Itoa(BulkCreateMaximum)))), nil)
	}
	prepared := make([]orm.PreparedModel[articlemodels.Article], len(inputs))
	candidates := make([]articlemodels.Article, len(inputs))
	for index, input := range inputs {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		value, candidate, err := prepareCreate(input)
		if err != nil {
			return zero, err
		}
		prepared[index], candidates[index] = value, candidate
	}
	return preparedAtomic(ctx, repository, MutationBulkCreate, func(work context.Context, session db.Session) (T, error) {
		seen := make(map[string]bool)
		var diagnostics []validation.Violation
		for index, candidate := range candidates {
			if err := work.Err(); err != nil {
				return zero, err
			}
			failures, err := prepared[index].ValidateUnique(work, session, candidate)
			if err != nil {
				return zero, err
			}
			// Empty non-null slugs are values too; multiple SQL nulls remain distinct.
			if slug := candidate.Slug; slug != nil {
				if seen[*slug] {
					failures = validation.Join(failures, validation.NewErrors(validation.New("slug", validation.CodeUnique)))
				}
				seen[*slug] = true
			}
			for _, failure := range failures.All() {
				diagnostics = append(diagnostics, validation.New(failure.Field(), failure.Code(), append(failure.Params(), validation.NewParam("index", strconv.Itoa(index)))...))
			}
		}
		if len(diagnostics) != 0 {
			return zero, validation.Reject(validation.NewErrors(diagnostics...), nil)
		}
		created, err := articlemodels.ArticleObjects.BulkCreate(work, session, candidates, orm.BulkBatchSize[articlemodels.Article](bulkCreateBatchSize))
		if err != nil {
			return zero, writeRejection(err)
		}
		if !created.ReturnedKeys || created.RowsAffected != int64(len(candidates)) || len(created.Objects) != len(candidates) {
			return zero, errors.New("article: incomplete bulk creation result")
		}
		rows := make([]articlemodels.Article, len(candidates))
		keys := make(map[int64]bool, len(candidates))
		mutation := MutationResult{Operation: MutationBulkCreate, Items: make([]MutationItem, len(candidates))}
		for index, created := range created.Objects {
			if created.ID <= 0 || keys[created.ID] {
				return zero, errors.New("article: invalid or repeated bulk creation key")
			}
			keys[created.ID] = true
			stored, found, err := repository.getIn(work, session, created.ID)
			if err != nil {
				return zero, err
			}
			if !found {
				return zero, errors.New("article: newly created row is absent")
			}
			rows[index] = cloneArticle(stored)
			mutation.Items[index] = MutationItem{Article: cloneArticle(rows[index])}
		}
		result, err := prepare(work, rows)
		if err != nil {
			return zero, err
		}
		if err := work.Err(); err != nil {
			return zero, err
		}
		if repository.mutationHook != nil {
			if err := repository.mutationHook(work, session, mutation); err != nil {
				return zero, err
			}
		}
		return result, nil
	})
}
