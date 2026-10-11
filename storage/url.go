package storage

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// URLPrefix maps portable storage names below an explicit URL directory. It
// performs no I/O and does not publish a server route or grant access. Only use
// this capability for a prefix whose serving/admission policy is intentional.
type URLPrefix struct{ base *url.URL }

func NewURLPrefix(base string) (URLPrefix, error) {
	u, err := parseURL(base)
	if err != nil {
		return URLPrefix{}, err
	}
	if u.RawQuery != "" || u.ForceQuery {
		return URLPrefix{}, &Error{Code: "invalid_url_prefix"}
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if u.Path != "/" && path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") {
		return URLPrefix{}, &Error{Code: "invalid_url_prefix"}
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
		if u.RawPath != "" {
			u.RawPath += "/"
		}
	}
	return URLPrefix{base: u}, nil
}

func (prefix URLPrefix) URL(ctx context.Context, name string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if prefix.base == nil {
		return "", &Error{Code: "url_unavailable"}
	}
	if err := validateName(name); err != nil {
		return "", err
	}
	u := *prefix.base
	u.RawPath = u.EscapedPath() + fileURI(name)
	u.Path += name
	return u.String(), nil
}

// Match Django filepath_to_uri's path escaping for the supported portable
// names, including literal percent signs. Names are never decoded as URLs.
func fileURI(name string) string {
	const hex = "0123456789ABCDEF"
	var result strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~/!*()'", rune(c)) {
			result.WriteByte(c)
		} else {
			result.WriteByte('%')
			result.WriteByte(hex[c>>4])
			result.WriteByte(hex[c&15])
		}
	}
	return result.String()
}

func parseURL(value string) (*url.URL, error) {
	if value == "" || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) || strings.Contains(value, `\`) {
		return nil, &Error{Code: "invalid_url"}
	}
	u, err := url.Parse(value)
	if err != nil || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.RawFragment != "" || strings.Contains(value, "#") {
		return nil, &Error{Code: "invalid_url"}
	}
	if u.Scheme == "" {
		if u.Host != "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
			return nil, &Error{Code: "invalid_url"}
		}
	} else if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.Hostname() == "" {
		return nil, &Error{Code: "invalid_url"}
	}
	if _, err := url.QueryUnescape(u.RawQuery); err != nil {
		return nil, &Error{Code: "invalid_url"}
	}
	return u, nil
}

func (URLPrefix) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.URLPrefix{redacted}") }
