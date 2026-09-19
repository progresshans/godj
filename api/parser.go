package api

import (
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const (
	DefaultMaxBodyBytes int64 = 1 << 20
	maximumBodyBytes    int64 = 8 << 20
)

// ParserConfig is immutable startup input for a JSON-only body parser.
type ParserConfig struct {
	MaxBodyBytes int64
	JSONLimits   serializers.Limits
}

// Parser consumes exactly one borrowed request body into a closed ordered
// JSON object. Its zero value is invalid.
type Parser struct {
	maxBodyBytes int64
	jsonLimits   serializers.Limits
	valid        bool
}

func NewParser(config ParserConfig) (Parser, error) {
	maximum := config.MaxBodyBytes
	if maximum == 0 {
		maximum = DefaultMaxBodyBytes
	}
	if maximum < 1 || maximum > maximumBodyBytes {
		return Parser{}, &Error{Code: FailureInvalidConfig, Field: "max_body_bytes", Detail: "body limit is outside the supported range"}
	}
	limits := config.JSONLimits
	if limits.MaxDocumentBytes == 0 {
		limits.MaxDocumentBytes = int(maximum)
	}
	// Validate every configured JSON limit without publishing an independent
	// normalization API. An empty object is the minimum valid probe.
	if _, err := serializers.DecodeObject([]byte(`{}`), limits); err != nil {
		return Parser{}, &Error{Code: FailureInvalidConfig, Field: "json_limits", Detail: "JSON limits are invalid", Cause: err}
	}
	return Parser{maxBodyBytes: maximum, jsonLimits: limits, valid: true}, nil
}

// ParseObject accepts only application/json with no parameter other than an
// optional UTF-8 charset. It bounds bytes before serializer allocation.
func (p Parser) ParseObject(request *web.Request) (serializers.Object, error) {
	if !p.valid {
		return serializers.Object{}, &Error{Code: FailureInvalidConfig, Field: "parser", Detail: "parser is zero or invalid"}
	}
	if request == nil || request.HTTP() == nil {
		return serializers.Object{}, &Error{Code: FailureInvalidRequest, Field: "request", Detail: "request is nil or outside its borrowed lifetime"}
	}
	raw := request.HTTP()
	if raw.Body == nil {
		return serializers.Object{}, &Error{Code: FailureInvalidRequest, Field: "body", Detail: "request body is unavailable"}
	}
	if err := validateJSONContentType(raw.Header.Values("Content-Type")); err != nil {
		return serializers.Object{}, err
	}
	if raw.ContentLength > p.maxBodyBytes {
		return serializers.Object{}, &Error{Code: FailureBodyTooLarge, Field: "body", Detail: "request body exceeds the configured limit"}
	}
	document, err := io.ReadAll(io.LimitReader(raw.Body, p.maxBodyBytes+1))
	if err != nil {
		return serializers.Object{}, &Error{Code: FailureBodyRead, Field: "body", Detail: "request body could not be read", Cause: err}
	}
	if int64(len(document)) > p.maxBodyBytes {
		return serializers.Object{}, &Error{Code: FailureBodyTooLarge, Field: "body", Detail: "request body exceeds the configured limit"}
	}
	object, err := serializers.DecodeObject(document, p.jsonLimits)
	if err != nil {
		return serializers.Object{}, &Error{Code: FailureInvalidRequest, Field: "body", Detail: "request body is not an accepted JSON object", Cause: err}
	}
	return object, nil
}

func validateJSONContentType(values []string) error {
	if len(values) != 1 {
		return &Error{Code: FailureUnsupportedMedia, Field: "content_type", Detail: "exactly one application/json Content-Type is required"}
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return &Error{Code: FailureUnsupportedMedia, Field: "content_type", Detail: "Content-Type must be application/json", Cause: err}
	}
	for name, value := range parameters {
		if !strings.EqualFold(name, "charset") || !strings.EqualFold(value, "utf-8") {
			return &Error{Code: FailureUnsupportedMedia, Field: "content_type", Detail: "application/json parameter is unsupported"}
		}
	}
	return nil
}

// RequestErrorResponse maps only expected client-input parser failures. Body
// read failures remain internal errors and are deliberately not rendered.
func RequestErrorResponse(err error) (web.Response, bool, error) {
	typed, ok := err.(*Error)
	if !ok || typed == nil {
		return web.Response{}, false, nil
	}
	switch typed.Code {
	case FailureUnsupportedMedia:
		response, responseErr := ErrorResponse(http.StatusUnsupportedMediaType, CodeUnsupportedMedia, validation.NewErrors())
		return response, true, responseErr
	case FailureNotAcceptable:
		response, responseErr := ErrorResponse(http.StatusNotAcceptable, CodeNotAcceptable, validation.NewErrors())
		return response, true, responseErr
	case FailureBodyTooLarge:
		response, responseErr := ErrorResponse(http.StatusRequestEntityTooLarge, CodeRequestTooLarge, validation.NewErrors())
		return response, true, responseErr
	case FailureInvalidRequest:
		response, responseErr := ErrorResponse(http.StatusBadRequest, CodeParseError, validation.NewErrors())
		return response, true, responseErr
	default:
		return web.Response{}, false, nil
	}
}
