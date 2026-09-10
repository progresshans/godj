package api

import (
	"net/http"
	"unicode/utf8"

	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const JSONContentType = "application/json"

// JSON creates one deterministic JSON response from a closed Value.
func JSON(status int, value serializers.Value) (web.Response, error) {
	body, err := serializers.Encode(value, serializers.Limits{})
	if err != nil {
		return web.Response{}, &Error{Code: FailureInvalidResponse, Field: "body", Detail: "JSON response value is invalid", Cause: err}
	}
	header := make(http.Header)
	header.Set("Content-Type", JSONContentType)
	response, err := web.NewResponse(status, header, body)
	if err != nil {
		return web.Response{}, &Error{Code: FailureInvalidResponse, Field: "response", Detail: "JSON response is invalid", Cause: err}
	}
	return response, nil
}

// NoContent returns a 204 response with no Content-Type and an empty body.
func NoContent() (web.Response, error) {
	response, err := web.NewResponse(http.StatusNoContent, make(http.Header), nil)
	if err != nil {
		return web.Response{}, &Error{Code: FailureInvalidResponse, Field: "response", Detail: "no-content response is invalid", Cause: err}
	}
	return response, nil
}

// ErrorResponse renders only stable codes, ordered field names, ordered
// diagnostic codes, and presentation-independent parameters.
func ErrorResponse(status int, code ResponseCode, diagnostics validation.Errors) (web.Response, error) {
	if status < 400 || status > 499 || !validResponseCode(code) {
		return web.Response{}, &Error{Code: FailureInvalidResponse, Field: "error", Detail: "error response status or code is invalid"}
	}
	items := diagnostics.All()
	values := make([]serializers.Value, len(items))
	for index := range items {
		item, err := diagnosticValue(items[index])
		if err != nil {
			return web.Response{}, err
		}
		values[index] = item
	}
	list, err := serializers.NewList(values...)
	if err != nil {
		return web.Response{}, &Error{Code: FailureInvalidResponse, Field: "errors", Detail: "diagnostic list is invalid", Cause: err}
	}
	object, err := serializers.NewObject(
		serializers.MemberOf("code", serializers.String(string(code))),
		serializers.MemberOf("errors", list),
	)
	if err != nil {
		return web.Response{}, &Error{Code: FailureInvalidResponse, Field: "error", Detail: "error envelope is invalid", Cause: err}
	}
	return JSON(status, object.Value())
}

func diagnosticValue(item validation.Violation) (serializers.Value, error) {
	parameters := item.Params()
	parameterValues := make([]serializers.Value, len(parameters))
	for index := range parameters {
		parameter, err := serializers.NewObject(
			serializers.MemberOf("key", serializers.String(parameters[index].Key())),
			serializers.MemberOf("value", serializers.String(parameters[index].Value())),
		)
		if err != nil {
			return serializers.Value{}, &Error{Code: FailureInvalidResponse, Field: "errors.params", Detail: "diagnostic parameter is invalid", Cause: err}
		}
		parameterValues[index] = parameter.Value()
	}
	parameterList, err := serializers.NewList(parameterValues...)
	if err != nil {
		return serializers.Value{}, &Error{Code: FailureInvalidResponse, Field: "errors.params", Detail: "diagnostic parameters are invalid", Cause: err}
	}
	object, err := serializers.NewObject(
		serializers.MemberOf("field", serializers.String(string(item.Field()))),
		serializers.MemberOf("code", serializers.String(string(item.Code()))),
		serializers.MemberOf("params", parameterList),
	)
	if err != nil {
		return serializers.Value{}, &Error{Code: FailureInvalidResponse, Field: "errors", Detail: "diagnostic is invalid", Cause: err}
	}
	return object.Value(), nil
}

func validResponseCode(code ResponseCode) bool {
	value := string(code)
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || index > 0 && (character == '_' || character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}
