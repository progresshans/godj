package apiapp

import (
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func articleSpec() (serializers.Spec, error) {
	id, err := serializers.IntegerField("id", serializers.WithReadOnly())
	if err != nil {
		return serializers.Spec{}, err
	}
	title, err := serializers.StringField("title", serializers.WithMaxLength(200))
	if err != nil {
		return serializers.Spec{}, err
	}
	published, err := serializers.BooleanField("published", serializers.WithDefault(serializers.Boolean(false)))
	if err != nil {
		return serializers.Spec{}, err
	}
	summary, err := serializers.StringField(
		"summary",
		serializers.WithRequired(false),
		serializers.WithNullable(),
		serializers.WithAllowEmpty(),
		serializers.WithMaxLength(200),
	)
	if err != nil {
		return serializers.Spec{}, err
	}
	return serializers.NewSpec([]serializers.Field{id, title, published, summary})
}

func articleValue(article articleapp.Article) (serializers.Value, error) {
	summary := serializers.Null()
	if article.Summary != nil {
		summary = serializers.String(*article.Summary)
	}
	object, err := serializers.NewObject(
		serializers.MemberOf("id", serializers.Integer(article.ID)),
		serializers.MemberOf("title", serializers.String(article.Title)),
		serializers.MemberOf("published", serializers.Boolean(article.Published)),
		serializers.MemberOf("summary", summary),
	)
	if err != nil {
		return serializers.Value{}, err
	}
	return object.Value(), nil
}

func (a *Application) bind(request *web.Request, mode serializers.Mode) (serializers.Values, web.Response, bool, error) {
	object, err := a.parser.ParseObject(request)
	if err != nil {
		response, expected, responseErr := api.RequestErrorResponse(err)
		return serializers.Values{}, response, expected, responseErrOrCause(expected, responseErr, err)
	}
	result, err := a.spec.Bind(object, mode)
	if err != nil {
		return serializers.Values{}, web.Response{}, false, err
	}
	if !result.Valid() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, result.Errors())
		return serializers.Values{}, response, true, err
	}
	values := result.Values()
	if diagnostics := repositoryTextDiagnostics(values); !diagnostics.Empty() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
		return serializers.Values{}, response, true, err
	}
	return values, web.Response{}, false, nil
}

func responseErrOrCause(expected bool, responseErr, cause error) error {
	if responseErr != nil {
		return responseErr
	}
	if expected {
		return nil
	}
	return cause
}

func fullInput(values serializers.Values) articleapp.Input {
	title, _ := stringValue(values, "title")
	published, _ := booleanValue(values, "published")
	summary, supplied, null := nullableStringValue(values, "summary")
	input := articleapp.Input{Title: title, Published: published}
	if supplied && !null {
		input.Summary = &summary
	}
	return input
}

func fullUpdateInput(current articleapp.Article, values serializers.Values) articleapp.Input {
	input := fullInput(values)
	if _, supplied := values.Get("summary"); supplied {
		return input
	}
	if current.Summary != nil {
		summary := *current.Summary
		input.Summary = &summary
	}
	return input
}

func partialInput(values serializers.Values) articleapp.Patch {
	patch := articleapp.Patch{}
	if title, supplied := stringValue(values, "title"); supplied {
		patch = patch.WithTitle(title)
	}
	if published, supplied := booleanValue(values, "published"); supplied {
		patch = patch.WithPublished(published)
	}
	if summary, supplied, null := nullableStringValue(values, "summary"); supplied {
		if null {
			patch = patch.WithSummaryNull()
		} else {
			patch = patch.WithSummary(summary)
		}
	}
	return patch
}

func stringValue(values serializers.Values, name string) (string, bool) {
	value, supplied := values.Get(name)
	if !supplied {
		return "", false
	}
	result, valid := value.AsString()
	return result, valid
}

func booleanValue(values serializers.Values, name string) (bool, bool) {
	value, supplied := values.Get(name)
	if !supplied {
		return false, false
	}
	result, valid := value.AsBoolean()
	return result, valid
}

func nullableStringValue(values serializers.Values, name string) (string, bool, bool) {
	value, supplied := values.Get(name)
	if !supplied {
		return "", false, false
	}
	if value.IsNull() {
		return "", true, true
	}
	result, valid := value.AsString()
	return result, valid, false
}

// The neutral repository deliberately rejects non-text control bytes. Keep
// that policy on the validation side of the I/O boundary so a bounded client
// value cannot turn into an internal 500 after serializer validation.
func repositoryTextDiagnostics(values serializers.Values) validation.Errors {
	diagnostics := validation.NewErrors()
	for _, name := range []string{"title", "summary"} {
		value, supplied := values.Get(name)
		if !supplied || value.IsNull() {
			continue
		}
		text, ok := value.AsString()
		if !ok || acceptedRepositoryText(text) {
			continue
		}
		diagnostics = diagnostics.Append(validation.NewErrors(validation.New(validation.Field(name), codeInvalid)))
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
