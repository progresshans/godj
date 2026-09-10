package api

import (
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const (
	MaximumAcceptBytes  = 4096
	MaximumAcceptRanges = 32
)

// JSONNegotiation rejects an API-subtree request before its handler when the
// bounded Accept header does not permit application/json. A missing Accept
// header and the HTTP wildcards remain valid; no HTML or implicit renderer
// fallback is available.
func JSONNegotiation(prefix string) (web.Middleware, error) {
	if !validAPIPrefix(prefix) {
		return nil, &Error{Code: FailureInvalidConfig, Field: "prefix", Detail: "API prefix must be a clean absolute path ending in slash"}
	}
	return func(next web.Handler) web.Handler {
		return func(request *web.Request) (web.Response, error) {
			if next == nil {
				return web.Response{}, &Error{Code: FailureInvalidConfig, Field: "handler", Detail: "JSON negotiation downstream is nil"}
			}
			if request == nil || !strings.HasPrefix(request.Path(), prefix) {
				return next(request)
			}
			if err := validateJSONAccept(request.HTTP()); err != nil {
				return ErrorResponse(http.StatusNotAcceptable, CodeNotAcceptable, validation.NewErrors())
			}
			return next(request)
		}
	}, nil
}

func validateJSONAccept(request *http.Request) error {
	if request == nil {
		return &Error{Code: FailureInvalidRequest, Field: "request", Detail: "request is nil or outside its borrowed lifetime"}
	}
	values := request.Header.Values("Accept")
	if len(values) == 0 {
		return nil
	}
	totalBytes := 0
	ranges := 0
	bestSpecificity := -1
	bestQuality := 0.0
	for _, value := range values {
		totalBytes += len(value)
		if totalBytes > MaximumAcceptBytes {
			return &Error{Code: FailureNotAcceptable, Field: "accept", Detail: "Accept header exceeds the supported limit"}
		}
		for _, part := range strings.Split(value, ",") {
			ranges++
			if ranges > MaximumAcceptRanges {
				return &Error{Code: FailureNotAcceptable, Field: "accept", Detail: "Accept header has too many media ranges"}
			}
			mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err != nil {
				return &Error{Code: FailureNotAcceptable, Field: "accept", Detail: "Accept header is malformed", Cause: err}
			}
			quality := 1.0
			if value, present := parameters["q"]; present {
				quality, err = strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(quality) || math.IsInf(quality, 0) || quality < 0 || quality > 1 {
					return &Error{Code: FailureNotAcceptable, Field: "accept", Detail: "Accept quality is invalid", Cause: err}
				}
			}
			mediaType = strings.ToLower(mediaType)
			specificity := -1
			switch mediaType {
			case "application/json":
				specificity = 2
			case "application/*":
				specificity = 1
			case "*/*":
				specificity = 0
			}
			if specificity > bestSpecificity || specificity == bestSpecificity && quality > bestQuality {
				bestSpecificity = specificity
				bestQuality = quality
			}
		}
	}
	if bestSpecificity < 0 || bestQuality == 0 {
		return &Error{Code: FailureNotAcceptable, Field: "accept", Detail: "application/json is not acceptable"}
	}
	return nil
}
