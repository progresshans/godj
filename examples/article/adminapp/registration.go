package adminapp

import (
	"context"

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
	initialSpec, err := formmodel.NewSpecForFields(metadata, articleapp.WritableFields())
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(metadata, descriptor.WriteFieldValue, "id", "title", "published", "summary", "slug")
	if err != nil {
		return err
	}
	return admin.RegisterModel(builder, admin.ModelConfig[articlemodels.Article]{
		AppLabel:   "godj_conformance",
		Slug:       "articles",
		Model:      metadata,
		FormFields: append([]string(nil), articleapp.WritableFields()...),
		FormOverrides: []formmodel.Override{
			formmodel.OverrideField("title", formmodel.WithValidators(articleDisplayTextValidator("title"))),
			formmodel.OverrideField("summary", formmodel.WithValidators(articleDisplayTextValidator("summary"))),
		},
		ListFields:   []string{"id", "title", "published", "summary", "slug"},
		SearchFields: []string{"title", "summary"},
		Permissions:  permissions,
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[articlemodels.Article], error) {
			page, err := service.List(ctx, articleapp.ListOptions{
				Search: request.Search,
				Offset: request.Offset,
				Limit:  request.Limit,
			})
			if err != nil {
				return admin.Page[articlemodels.Article]{}, err
			}
			return admin.Page[articlemodels.Article]{
				Items:  append([]articlemodels.Article(nil), page.Articles...),
				Total:  page.Total,
				Offset: page.Offset,
				Limit:  page.Limit,
			}, nil
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (articlemodels.Article, bool, error) {
			return service.Get(ctx, id)
		},
		Snapshot: func(article articlemodels.Article) (admin.Object, error) {
			return projector.Project(article, article.ID, article.Title)
		},
		Initial: func(article articlemodels.Article) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, initialSpec, article, descriptor.WriteFieldValue)
		},
		Create: func(ctx context.Context, principal auth.Principal, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, error) {
			input, err := formmodel.PrepareInput(articlemodels.ArticleObjects, bound)
			if err != nil {
				return admin.MutationResult{}, err
			}
			value, err := service.Create(ctx, principal.ID(), input)
			return admin.MutationResult{ID: value.ID}, err
		},
		Update: func(ctx context.Context, principal auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, []string, error) {
			input, err := formmodel.PrepareInput(articlemodels.ArticleObjects, bound)
			if err != nil {
				return admin.MutationResult{}, nil, err
			}
			article, changed, err := service.Update(ctx, principal.ID(), mutation.ID, input)
			return admin.MutationResult{ID: article.ID}, changed, adminMutationError(err)
		},
		Delete: func(ctx context.Context, principal auth.Principal, mutation admin.Mutation) (admin.MutationResult, error) {
			id := mutation.ID
			article, err := service.Delete(ctx, principal.ID(), id)
			return admin.MutationResult{ID: article.ID}, adminMutationError(err)
		},
		History: func(ctx context.Context, _ auth.Principal, id int64, request admin.HistoryRequest) ([]admin.AuditEntry, error) {
			return service.HistoryLimited(ctx, id, request.Limit)
		},
		Actions: []admin.ActionConfig{{
			Name:          "publish",
			Label:         "Publish selected articles",
			Permission:    ArticleChangePermission,
			SuccessNotice: admin.ActionNotice{Tag: "published", Text: "{count} object(s) published."},
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

// Only a confirmed application miss becomes the Admin marker. Wrapped/joined
// execution failures retain their causes and cannot masquerade as a 404.
func adminMutationError(err error) error {
	if err == articleapp.ErrNotFound {
		return admin.ErrObjectNotFound
	}
	if missing, ok := err.(*articleapp.Error); ok && missing != nil && missing.Code == articleapp.CodeNotFound && missing.Cause == nil {
		return admin.ErrObjectNotFound
	}
	return err
}
