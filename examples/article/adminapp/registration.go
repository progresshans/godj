package adminapp

import (
	"context"
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
	return admin.RegisterModel(builder, admin.ModelConfig[Article]{
		AppLabel:   "godj_conformance",
		Slug:       "articles",
		Model:      (articlemodels.ArticleDescriptor{}).Metadata(),
		FormFields: append([]string(nil), articleWritableFields...),
		FormOverrides: []formmodel.Override{
			formmodel.OverrideField("title", formmodel.WithValidators(articleDisplayTextValidator("title"))),
			formmodel.OverrideField("summary", formmodel.WithValidators(articleDisplayTextValidator("summary"))),
		},
		ListFields:   []string{"id", "title", "published", "summary"},
		SearchFields: []string{"title", "summary"},
		Permissions:  permissions,
		List: func(ctx context.Context, request admin.ListRequest) (admin.Page[Article], error) {
			page, err := service.List(ctx, ListOptions{
				Search: request.Search,
				Offset: request.Offset,
				Limit:  request.Limit,
			})
			if err != nil {
				return admin.Page[Article]{}, err
			}
			return admin.Page[Article]{
				Items:  append([]Article(nil), page.Articles...),
				Total:  page.Total,
				Offset: page.Offset,
				Limit:  page.Limit,
			}, nil
		},
		Get: service.Get,
		Snapshot: func(article Article) (admin.Object, error) {
			return articleObject(article)
		},
		Initial: func(article Article) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, initialSpec, articleapp.ModelSnapshot(article), descriptor.WriteFieldValue)
		},
		Create: func(ctx context.Context, principal auth.Principal, values forms.Values) (Article, error) {
			input, err := articleInput(values)
			if err != nil {
				return Article{}, err
			}
			return service.Create(ctx, principal.ID(), input)
		},
		Update: func(ctx context.Context, principal auth.Principal, id int64, values forms.Values) (Article, []string, error) {
			input, err := articleInput(values)
			if err != nil {
				return Article{}, nil, err
			}
			return service.Update(ctx, principal.ID(), id, input)
		},
		Delete: func(ctx context.Context, principal auth.Principal, id int64) (Article, error) {
			return service.Delete(ctx, principal.ID(), id)
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

func articleObject(article Article) (admin.Object, error) {
	descriptor := articlemodels.ArticleDescriptor{}
	return admin.ModelObject(descriptor.Metadata(), articleapp.ModelSnapshot(article), descriptor.WriteFieldValue, article.ID, article.Title, "id", "title", "published", "summary")
}

func articleInput(values forms.Values) (Input, error) {
	if len(values.All()) != len(articleWritableFields) {
		return Input{}, invalid("form", "cleaned Article field set is incomplete")
	}
	title, ok := values.String("title")
	if !ok {
		return Input{}, invalid("title", "cleaned title is unavailable")
	}
	published, ok := values.Boolean("published")
	if !ok {
		return Input{}, invalid("published", "cleaned published value is unavailable")
	}
	summaryValue, ok := values.Get("summary")
	if !ok {
		return Input{}, invalid("summary", "cleaned summary is unavailable")
	}
	var summary *string
	switch summaryValue.Kind() {
	case forms.ValueNull:
	case forms.ValueString:
		value, stringOK := summaryValue.AsString()
		if !stringOK {
			return Input{}, invalid("summary", "cleaned summary type is invalid")
		}
		summary = &value
	default:
		return Input{}, invalid("summary", "cleaned summary type is invalid")
	}
	input := Input{Title: title, Published: published, Summary: summary}
	if err := articleapp.ValidateInput(input); err != nil {
		return Input{}, fmt.Errorf("article admin form conversion: %w", err)
	}
	return input, nil
}
