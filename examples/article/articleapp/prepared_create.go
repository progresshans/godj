package articleapp

import (
	"context"

	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
)

// CreateAndPrepare prepares an owned application result after insertion and
// before the mutation hook and commit. Output failure rolls back the write;
// an uncertain owner outcome publishes no result and never retries.
func CreateAndPrepare[T any](ctx context.Context, repository Repository, input orm.ModelInput[articlemodels.Article], prepare func(context.Context, articlemodels.Article) (T, error)) (T, error) {
	var zero T
	if prepare == nil {
		return zero, invalid("prepare", "result preparation is required")
	}
	if err := repository.validateWrite(ctx, input); err != nil {
		return zero, err
	}
	_, _, err := prepareCreate(input)
	if err != nil {
		return zero, err
	}
	return preparedAtomic(ctx, repository, MutationCreate, func(work context.Context, session db.Session) (T, error) {
		mutation, err := repository.resource.CreateInSession(work, session, input, crud.SaveOptions[articlemodels.Article]{})
		if err != nil {
			return zero, writeRejection(err)
		}
		value := mutation.Value
		result, err := prepare(work, cloneArticle(value))
		if err != nil {
			return zero, err
		}
		if err := work.Err(); err != nil {
			return zero, err
		}
		if repository.mutationHook != nil {
			if err := repository.mutationHook(work, session, mutationResult(MutationCreate, value, nil)); err != nil {
				return zero, err
			}
		}
		return result, nil
	})
}

func prepareCreate(input orm.ModelInput[articlemodels.Article]) (orm.PreparedModel[articlemodels.Article], articlemodels.Article, error) {
	var zero orm.PreparedModel[articlemodels.Article]
	if err := ValidateInput(input); err != nil {
		return zero, articlemodels.Article{}, err
	}
	prepared, err := articlemodels.ArticleObjects.PrepareInput(input, nil)
	if err != nil {
		return zero, articlemodels.Article{}, err
	}
	value, err := prepared.Model()
	if err != nil {
		return zero, articlemodels.Article{}, err
	}
	// A server-created input may omit title; the unsaved empty text state is
	// not a valid Article. Other supplied scalars were checked above.
	if err := validateModelText("title", value.Title, false); err != nil {
		return zero, articlemodels.Article{}, err
	}
	return prepared, value, nil
}
