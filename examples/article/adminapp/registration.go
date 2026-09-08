package adminapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/validation"
)

const (
	ArticleViewPermission   = articleapp.ArticleViewPermission
	ArticleAddPermission    = articleapp.ArticleAddPermission
	ArticleChangePermission = articleapp.ArticleChangePermission
	ArticleDeletePermission = articleapp.ArticleDeletePermission
)

// RegisterArticle installs the generated Article model and its explicit typed
// persistence adapter into a startup Admin builder. Generated model values and
// methods never cross into templates or generic form mutation.
func RegisterArticle(builder *admin.Builder, service Service) error {
	if !service.validState() {
		return invalid("service", "service is zero or invalid")
	}
	permissions := admin.Permissions{
		View:   ArticleViewPermission,
		Add:    ArticleAddPermission,
		Change: ArticleChangePermission,
		Delete: ArticleDeletePermission,
	}
	descriptor := articlemodels.ArticleDescriptor{}
	metadata := descriptor.Metadata()
	initialSpec, err := formmodel.NewSpecForFields(metadata, articleWritableFields)
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(metadata, descriptor.WriteFieldValue, "id", "title", "published", "summary")
	if err != nil {
		return err
	}
	return admin.RegisterModel(builder, admin.ModelConfig[articleapp.Article]{
		AppLabel:   "godj_conformance",
		Slug:       "articles",
		Model:      metadata,
		FormFields: append([]string(nil), articleWritableFields...),
		FormOverrides: []formmodel.Override{
			formmodel.OverrideField("title", formmodel.WithValidators(articleDisplayTextValidator("title"))),
			formmodel.OverrideField("summary", formmodel.WithValidators(articleDisplayTextValidator("summary"))),
		},
		ListFields:   []string{"id", "title", "published", "summary"},
		SearchFields: []string{"title", "summary"},
		Permissions:  permissions,
		List: func(ctx context.Context, request admin.ListRequest) (admin.Page[articleapp.Article], error) {
			page, err := service.List(ctx, articleapp.ListOptions{
				Search: request.Search,
				Offset: request.Offset,
				Limit:  request.Limit,
			})
			if err != nil {
				return admin.Page[articleapp.Article]{}, err
			}
			return admin.Page[articleapp.Article]{
				Items:  append([]articleapp.Article(nil), page.Articles...),
				Total:  page.Total,
				Offset: page.Offset,
				Limit:  page.Limit,
			}, nil
		},
		Get: service.Get,
		Snapshot: func(article articleapp.Article) (admin.Object, error) {
			return projector.Project(articleapp.ModelSnapshot(article), article.ID, article.Title)
		},
		Initial: func(article articleapp.Article) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, initialSpec, articleapp.ModelSnapshot(article), descriptor.WriteFieldValue)
		},
		Create: func(ctx context.Context, principal auth.Principal, values forms.Values) (articleapp.Article, error) {
			input, err := articleInput(values)
			if err != nil {
				return articleapp.Article{}, err
			}
			return service.Create(ctx, principal.ID(), input)
		},
		Update: func(ctx context.Context, principal auth.Principal, id int64, values forms.Values) (articleapp.Article, []string, error) {
			input, err := articleInput(values)
			if err != nil {
				return articleapp.Article{}, nil, err
			}
			article, changed, err := service.Update(ctx, principal.ID(), id, input)
			return article, changed, adminMutationError(err)
		},
		Delete: func(ctx context.Context, principal auth.Principal, id int64) (articleapp.Article, error) {
			article, err := service.Delete(ctx, principal.ID(), id)
			return article, adminMutationError(err)
		},
		History: func(ctx context.Context, id int64, request admin.HistoryRequest) ([]admin.AuditEntry, error) {
			return service.HistoryLimited(ctx, id, request.Limit)
		},
		Actions: []admin.ActionConfig{{
			Name:       "publish",
			Label:      "Publish selected articles",
			Permission: ArticleChangePermission,
			Run: func(ctx context.Context, principal auth.Principal, ids []int64) (admin.ActionResult, error) {
				result, err := service.Publish(ctx, principal.ID(), ids)
				if err != nil {
					return admin.ActionResult{}, err
				}
				return admin.ActionResult{MatchedIDs: append([]int64(nil), result.MatchedIDs...)}, nil
			},
		}},
	})
}

func articleDisplayTextValidator(field validation.Field) forms.FieldValidator {
	return forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		if value.IsNull() {
			return validation.NewErrors()
		}
		text, ok := value.AsString()
		if !ok {
			return validation.NewErrors()
		}
		for _, character := range text {
			if character == 0 || character == '\t' || character == '\n' || character == '\r' {
				continue
			}
			if character < 0x20 || character == 0x7f {
				return validation.NewErrors(validation.New(field, "invalid_control_character"))
			}
		}
		return validation.NewErrors()
	})
}

// Add the framework marker at the Admin callback boundary while preserving
// the original error chain, including rollback and outcome-unknown failures.
func adminMutationError(err error) error {
	if errors.Is(err, articleapp.ErrNotFound) {
		return errors.Join(admin.ErrObjectNotFound, err)
	}
	return err
}

func articleInput(values forms.Values) (articleapp.Input, error) {
	if len(values.All()) != len(articleWritableFields) {
		return articleapp.Input{}, invalid("form", "cleaned Article field set is incomplete")
	}
	title, ok := values.String("title")
	if !ok {
		return articleapp.Input{}, invalid("title", "cleaned title is unavailable")
	}
	published, ok := values.Boolean("published")
	if !ok {
		return articleapp.Input{}, invalid("published", "cleaned published value is unavailable")
	}
	summaryValue, ok := values.Get("summary")
	if !ok {
		return articleapp.Input{}, invalid("summary", "cleaned summary is unavailable")
	}
	var summary *string
	switch summaryValue.Kind() {
	case forms.ValueNull:
	case forms.ValueString:
		value, stringOK := summaryValue.AsString()
		if !stringOK {
			return articleapp.Input{}, invalid("summary", "cleaned summary type is invalid")
		}
		summary = &value
	default:
		return articleapp.Input{}, invalid("summary", "cleaned summary type is invalid")
	}
	input := articleapp.Input{Title: title, Published: published, Summary: summary}
	if err := articleapp.ValidateInput(input); err != nil {
		return articleapp.Input{}, fmt.Errorf("article admin form conversion: %w", err)
	}
	return input, nil
}
