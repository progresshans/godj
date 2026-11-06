package apiapp

import (
	"net/http"

	"github.com/progresshans/godj/api"
	bodyinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/crud"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
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

func (a *Application) articleValue(article articlemodels.Article) (serializers.Value, error) {
	return a.encoder.Encode(article)
}

func prepareArticleInput(resource crud.Resource[articlemodels.Article], spec serializers.Spec, config api.ParserConfig) (bodyinput.Body[orm.ModelInput[articlemodels.Article]], error) {
	return bodyinput.NewResource(resource, spec, config)
}

func (a *Application) bind(request *web.Request, mode serializers.Mode) (orm.ModelInput[articlemodels.Article], web.Response, bool, error) {
	value, diagnostics, err := a.body.Parse(request, mode)
	if err != nil {
		response, handled, failure := api.RequestErrorResponse(err)
		if handled {
			return orm.ModelInput[articlemodels.Article]{}, response, true, failure
		}
		return orm.ModelInput[articlemodels.Article]{}, web.Response{}, false, err
	}
	if diagnostics.Empty() {
		diagnostics = articleTextDiagnostics(value)
	}
	if !diagnostics.Empty() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
		return orm.ModelInput[articlemodels.Article]{}, response, true, err
	}
	return value, web.Response{}, false, nil
}

// The repository's non-text-control policy remains a validation rule before
// writes, after the model Spec's trimming and scalar validation.
func articleTextDiagnostics(value orm.ModelInput[articlemodels.Article]) validation.Errors {
	diagnostics := validation.NewErrors()
	for _, name := range []string{"title", "summary"} {
		if scalar, present := value.Get(name); present && !scalar.IsNull() {
			text, _ := scalar.String()
			if !acceptedRepositoryText(text) {
				diagnostics = diagnostics.Append(validation.NewErrors(validation.New(validation.Field(name), codeInvalid)))
			}
		}
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
