package api

import (
	"net/url"
	pathpkg "path"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

const (
	MaximumPageResults = 100
	MaximumLinkBytes   = 4096
)

// Link is an optional canonical relative request URI. Its zero value means
// null; absolute origins and network-path references cannot be represented.
type Link struct {
	value string
	valid bool
}

func RelativeLink(value string) (Link, error) {
	if !validRelativeRequestURI(value) {
		return Link{}, &Error{Code: FailureInvalidConfig, Field: "link", Detail: "page link must be a canonical relative request URI"}
	}
	return Link{value: value, valid: true}, nil
}

func (l Link) Value() (string, bool) { return l.value, l.valid }

// Page is an immutable count/link/result envelope.
type Page struct {
	count    int64
	next     Link
	previous Link
	results  []serializers.Value
	valid    bool
}

func NewPage(count int64, next, previous Link, results []serializers.Value) (Page, error) {
	if count < 0 {
		return Page{}, &Error{Code: FailureInvalidConfig, Field: "count", Detail: "page count cannot be negative"}
	}
	if len(results) > MaximumPageResults {
		return Page{}, &Error{Code: FailureInvalidConfig, Field: "results", Detail: "page result count exceeds the supported limit"}
	}
	if next.value != "" && !next.valid || previous.value != "" && !previous.valid {
		return Page{}, &Error{Code: FailureInvalidConfig, Field: "links", Detail: "page contains an invalid optional link"}
	}
	cloned := make([]serializers.Value, len(results))
	for index := range results {
		object, ok := results[index].AsObject()
		if !ok {
			return Page{}, &Error{Code: FailureInvalidConfig, Field: "results", Detail: "page result must be a JSON object"}
		}
		cloned[index] = object.Value()
	}
	return Page{count: count, next: next, previous: previous, results: cloned, valid: true}, nil
}

func (p Page) Response() (web.Response, error) {
	if !p.valid {
		return web.Response{}, &Error{Code: FailureInvalidResponse, Field: "page", Detail: "page is zero or invalid"}
	}
	results, err := serializers.NewList(p.results...)
	if err != nil {
		return web.Response{}, &Error{Code: FailureInvalidResponse, Field: "results", Detail: "page results are invalid", Cause: err}
	}
	object, err := serializers.NewObject(
		serializers.MemberOf("count", serializers.Integer(p.count)),
		serializers.MemberOf("next", linkValue(p.next)),
		serializers.MemberOf("previous", linkValue(p.previous)),
		serializers.MemberOf("results", results),
	)
	if err != nil {
		return web.Response{}, &Error{Code: FailureInvalidResponse, Field: "page", Detail: "page envelope is invalid", Cause: err}
	}
	return JSON(200, object.Value())
}

func linkValue(link Link) serializers.Value {
	if !link.valid {
		return serializers.Null()
	}
	return serializers.String(link.value)
}

func validRelativeRequestURI(value string) bool {
	if value == "" || len(value) > MaximumLinkBytes || !utf8.ValidString(value) ||
		!strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\#") {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] == 0x7f {
			return false
		}
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	decodedPath := parsed.Path
	if !utf8.ValidString(decodedPath) {
		return false
	}
	for _, character := range decodedPath {
		if character < 0x20 || character == 0x7f || character == '\\' {
			return false
		}
	}
	cleanCandidate := decodedPath
	if decodedPath != "/" && strings.HasSuffix(decodedPath, "/") {
		cleanCandidate = strings.TrimSuffix(decodedPath, "/")
	}
	if strings.Contains(decodedPath, "//") || pathpkg.Clean(cleanCandidate) != cleanCandidate {
		return false
	}
	canonicalEscapedPath := (&url.URL{Path: decodedPath}).EscapedPath()
	return parsed.EscapedPath() == canonicalEscapedPath
}
