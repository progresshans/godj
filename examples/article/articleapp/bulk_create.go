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
func BulkCreateAndPrepare[T any](ctx context.Context, repository Repository, inputs []Input, prepare func(context.Context, []Article) (T, error)) (T, error) {
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
	creates := make([]articlemodels.ArticleCreate, len(inputs))
	slugs := make([]*string, len(inputs))
	for index, input := range inputs {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if err := ValidateInput(input); err != nil {
			return zero, err
		}
		create := articlemodels.NewArticleCreate(input.Title).WithPublished(input.Published)
		if input.Summary == nil {
			create = create.WithSummaryNull()
		} else {
			create = create.WithSummary(*input.Summary)
		}
		if input.Slug == nil {
			create = create.WithSlugNull()
		} else {
			value := *input.Slug
			slugs[index] = &value
			create = create.WithSlug(value)
		}
		creates[index] = create
	}
	return preparedAtomic(ctx, repository, MutationBulkCreate, func(work context.Context, session db.Session) (T, error) {
		seen := make(map[string]bool)
		var diagnostics []validation.Violation
		for index, create := range creates {
			if err := work.Err(); err != nil {
				return zero, err
			}
			failures, err := articlemodels.ArticleObjects.ValidateUniqueCreate(work, session, create)
			if err != nil {
				return zero, err
			}
			// Empty non-null slugs are values too; multiple SQL nulls remain distinct.
			if slug := slugs[index]; slug != nil {
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
		created, err := articlemodels.ArticleObjects.BulkCreateInputs(work, session, orm.CreateInputs[articlemodels.Article](creates), orm.BulkBatchSize[articlemodels.Article](bulkCreateBatchSize))
		if err != nil {
			return zero, writeRejection(err)
		}
		if !created.ReturnedKeys || created.RowsAffected != int64(len(creates)) || len(created.Objects) != len(creates) {
			return zero, errors.New("article: incomplete bulk creation result")
		}
		rows := make([]Article, len(creates))
		keys := make(map[int64]bool, len(creates))
		mutation := MutationResult{Operation: MutationBulkCreate, Items: make([]MutationItem, len(creates))}
		for index, created := range created.Objects {
			if created.ID <= 0 || keys[created.ID] {
				return zero, errors.New("article: invalid or repeated bulk creation key")
			}
			keys[created.ID] = true
			stored, found, err := getModel(work, session, created.ID)
			if err != nil {
				return zero, err
			}
			if !found {
				return zero, errors.New("article: newly created row is absent")
			}
			rows[index] = snapshot(stored)
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
