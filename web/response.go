package web

import (
	"net/http"
	"strings"
)

// Response is an immutable, fully buffered HTTP response.
type Response struct {
	status int
	header http.Header
	body   []byte
	valid  bool
}

// NewResponse validates and snapshots an HTTP response.
func NewResponse(status int, header http.Header, body []byte) (Response, error) {
	if status < 200 || status > 599 {
		return Response{}, &Error{Code: CodeInvalidResponse, Field: "status", Detail: "status must be between 200 and 599"}
	}
	if (status == http.StatusNoContent || status == http.StatusNotModified) && len(body) != 0 {
		return Response{}, &Error{Code: CodeInvalidResponse, Field: "body", Detail: "status does not permit a response body"}
	}
	for name, values := range header {
		if !validHeaderName(name) {
			return Response{}, &Error{Code: CodeInvalidResponse, Field: "header", Detail: "header name is not a valid ASCII HTTP token"}
		}
		for _, value := range values {
			if !validHeaderValue(value) {
				return Response{}, &Error{Code: CodeInvalidResponse, Field: "header", Detail: "header value contains a line break or invalid control byte"}
			}
		}
	}
	return Response{
		status: status,
		header: header.Clone(),
		body:   append([]byte(nil), body...),
		valid:  true,
	}, nil
}

// HTML creates a UTF-8 HTML response from already-rendered bytes.
func HTML(status int, body []byte) (Response, error) {
	return responseWithContentType(status, "text/html; charset=utf-8", body)
}

func responseWithContentType(status int, contentType string, body []byte) (Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", contentType)
	return NewResponse(status, header, body)
}

// Status returns the response status code.
func (r Response) Status() int {
	return r.status
}

// Header returns an independent header copy.
func (r Response) Header() http.Header {
	return r.header.Clone()
}

// Body returns an independent body copy.
func (r Response) Body() []byte {
	return append([]byte(nil), r.body...)
}

func (r Response) validate(maxBytes int64) error {
	if !r.valid {
		return &Error{Code: CodeInvalidResponse, Detail: "handler returned a zero or invalid response"}
	}
	if int64(len(r.body)) > maxBytes {
		return &Error{Code: CodeResponseTooLarge, Field: "body", Detail: "buffered response exceeds the configured limit"}
	}
	return nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\t' || character >= 0x20 && character != 0x7f {
			continue
		}
		return false
	}
	return true
}
