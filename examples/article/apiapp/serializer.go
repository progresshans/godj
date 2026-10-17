package apiapp

import (
	"net/http"

	"github.com/progresshans/godj/api"
	bodyinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func articleSpec() (serializers.Spec, error) {
	return serializers.FromModel((articlemodels.ArticleDescriptor{}).Metadata(),
		serializers.ModelField{Name: "id"},
		serializers.ModelField{Name: "title"},
		serializers.ModelField{Name: "published"},
		serializers.ModelField{Name: "summary", Optional: true, AllowEmpty: true},
		serializers.ModelField{Name: "slug", Optional: true, AllowEmpty: true},
	)
}

func (a *Application) articleValue(article articleapp.Article) (serializers.Value, error) {
	return a.encoder.Encode(articleapp.ModelSnapshot(article))
}

type articleInput struct {
	title     bodyinput.Presence[string]
	published bodyinput.Presence[bool]
	summary   bodyinput.Presence[*string]
	slug      bodyinput.Presence[*string]
}

func prepareArticleInput(spec serializers.Spec, config api.ParserConfig) (bodyinput.Body[articleInput], error) {
	return bodyinput.New(spec, config,
		bodyinput.Field("title", bodyinput.String(), func(value *articleInput, field bodyinput.Presence[string]) { value.title = field }),
		bodyinput.Field("published", bodyinput.Boolean(), func(value *articleInput, field bodyinput.Presence[bool]) { value.published = field }),
		bodyinput.Field("summary", bodyinput.Nullable(bodyinput.String()), func(value *articleInput, field bodyinput.Presence[*string]) { value.summary = field }),
		bodyinput.Field("slug", bodyinput.Nullable(bodyinput.String()), func(value *articleInput, field bodyinput.Presence[*string]) { value.slug = field }),
	)
}

func (a *Application) bind(request *web.Request, mode serializers.Mode) (articleInput, web.Response, bool, error) {
	value, diagnostics, err := a.body.Parse(request, mode)
	if err != nil {
		response, handled, failure := api.RequestErrorResponse(err)
		if handled {
			return articleInput{}, response, true, failure
		}
		return articleInput{}, web.Response{}, false, err
	}
	if diagnostics.Empty() {
		diagnostics = value.textDiagnostics()
	}
	if !diagnostics.Empty() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
		return articleInput{}, response, true, err
	}
	return value, web.Response{}, false, nil
}

func (value articleInput) full(current *articleapp.Article) articleapp.Input {
	title, _ := value.title.Get()
	published, _ := value.published.Get()
	result := articleapp.Input{Title: title, Published: published}
	if summary, present := value.summary.Get(); present {
		result.Summary = summary
	} else if current != nil && current.Summary != nil {
		copy := *current.Summary
		result.Summary = &copy
	}
	if slug, present := value.slug.Get(); present {
		result.Slug = slug
	} else if current != nil && current.Slug != nil {
		copy := *current.Slug
		result.Slug = &copy
	}
	return result
}

func (value articleInput) patch() articleapp.Patch {
	patch := articleapp.Patch{}
	if title, present := value.title.Get(); present {
		patch = patch.WithTitle(title)
	}
	if published, present := value.published.Get(); present {
		patch = patch.WithPublished(published)
	}
	if summary, present := value.summary.Get(); present {
		if summary == nil {
			patch = patch.WithSummaryNull()
		} else {
			patch = patch.WithSummary(*summary)
		}
	}
	if slug, present := value.slug.Get(); present {
		if slug == nil {
			patch = patch.WithSlugNull()
		} else {
			patch = patch.WithSlug(*slug)
		}
	}
	return patch
}

// The repository's non-text-control policy remains a validation rule before
// writes, after the model Spec's trimming and scalar validation.
func (value articleInput) textDiagnostics() validation.Errors {
	diagnostics := validation.NewErrors()
	if title, present := value.title.Get(); present && !acceptedRepositoryText(title) {
		diagnostics = diagnostics.Append(validation.NewErrors(validation.New("title", codeInvalid)))
	}
	if summary, present := value.summary.Get(); present && summary != nil && !acceptedRepositoryText(*summary) {
		diagnostics = diagnostics.Append(validation.NewErrors(validation.New("summary", codeInvalid)))
	}
	return diagnostics
}

func acceptedRepositoryText(value string) bool {
	for _, character := range value {
		if character == '\t' || character == '\n' || character == '\r' || character >= 0x20 && character != 0x7f {
			continue
		}
		return false
	}
	return true
}
