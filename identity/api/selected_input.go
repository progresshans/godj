package identityapi

import (
	"net/http"
	"strconv"

	"github.com/progresshans/godj/api"
	apiinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

type selectedModelInput[M any] struct{ Fields orm.ModelInput[M] }
type selectedCredentialInput struct{ Password apiinput.Presence[*string] }
type selectedCreationInput[M any] struct {
	Fields     orm.ModelInput[M]
	Credential selectedCredentialInput
}

func selectedModelFields(model ir.Model, names []string, readOnly bool) []serializers.ModelField {
	fields := make([]serializers.ModelField, len(names))
	for index, name := range names {
		field := serializers.ModelField{Name: name, ReadOnly: readOnly}
		for _, scalar := range model.Fields {
			if scalar.Name != name {
				continue
			}
			field.Optional = scalar.Blank || scalar.Nullable
			switch scalar.Kind {
			case ir.FieldChar, ir.FieldEmail, ir.FieldURL, ir.FieldSlug, ir.FieldText:
				field.AllowEmpty = readOnly || scalar.Blank
			}
		}
		for _, collection := range model.ManyToMany {
			if collection.Name == name {
				// Full omission does not invent a destructive empty collection.
				field.Optional = true
			}
		}
		fields[index] = field
	}
	return fields
}

func selectedModelBody[M any](manager orm.Manager[M], spec serializers.Spec) (apiinput.Body[selectedModelInput[M]], error) {
	model, err := apiinput.NewModel(manager, spec, selectedParserConfig())
	if err != nil {
		return apiinput.Body[selectedModelInput[M]]{}, err
	}
	return apiinput.Object(selectedParserConfig(), apiinput.ObjectField("fields", model,
		func(input *selectedModelInput[M], value orm.ModelInput[M]) { input.Fields = value }))
}

func selectedCredentialBody(maximum int) (apiinput.Body[selectedCredentialInput], error) {
	field, err := serializers.StringField("password", serializers.WithTrimWhitespace(false), serializers.WithNullable(), serializers.WithMaxInputBytes(maximum))
	if err != nil {
		return apiinput.Body[selectedCredentialInput]{}, err
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		return apiinput.Body[selectedCredentialInput]{}, err
	}
	return apiinput.New(spec, selectedParserConfig(), apiinput.Field("password", apiinput.Nullable(apiinput.String()),
		func(input *selectedCredentialInput, value apiinput.Presence[*string]) { input.Password = value }))
}

func selectedCreationBody[M any](manager orm.Manager[M], spec serializers.Spec, credential apiinput.Body[selectedCredentialInput]) (apiinput.Body[selectedCreationInput[M]], error) {
	model, err := apiinput.NewModel(manager, spec, selectedParserConfig())
	if err != nil {
		return apiinput.Body[selectedCreationInput[M]]{}, err
	}
	return apiinput.Object(selectedParserConfig(),
		apiinput.ObjectField("fields", model, func(input *selectedCreationInput[M], value orm.ModelInput[M]) { input.Fields = value }),
		apiinput.ObjectField("credential", credential, func(input *selectedCreationInput[M], value selectedCredentialInput) { input.Credential = value }))
}

func parseSelectedBody[T any](request *web.Request, body apiinput.Body[T], mode serializers.Mode) (T, web.Response, bool, error) {
	var zero T
	value, diagnostics, err := body.Parse(request, mode)
	if err != nil {
		response, handled, failure := api.RequestErrorResponse(err)
		if handled {
			return zero, response, true, failure
		}
		return zero, web.Response{}, false, err
	}
	if !diagnostics.Empty() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
		return zero, response, true, err
	}
	return value, web.Response{}, false, nil
}

func selectedID(request *web.Request, allowQuery bool) (int64, bool) {
	text, present := request.StringParameter("id")
	if !present || len(text) > 20 || !allowQuery && !selectedNoQuery(request) {
		return 0, false
	}
	id, err := strconv.ParseInt(text, 10, 64)
	return id, err == nil && strconv.FormatInt(id, 10) == text
}

func selectedNoQuery(request *web.Request) bool {
	return request.HTTP().URL.RawQuery == "" && !request.HTTP().URL.ForceQuery
}

func (builder *selectedBuilder) condition(request *web.Request) (int64, web.Response, bool, error) {
	value, diagnostics, err := builder.revision.Parse(request.HTTP().Header)
	if err != nil || diagnostics.Empty() {
		return value, web.Response{}, false, err
	}
	status := http.StatusBadRequest
	if first, _ := diagnostics.At(0); first.Code() == "required" {
		status = http.StatusPreconditionRequired
	}
	response, err := api.ErrorResponse(status, api.CodeValidationError, diagnostics)
	return 0, response, true, err
}
