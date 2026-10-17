package storage

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

const DefaultAlias = "default"

// URLResolver creates an explicitly configured public or authorized URL. It
// must honor context for I/O and be safe for concurrent calls. A URL may itself
// be a credential. Its creation is neither a file-existence nor an admission
// check; callers decide who may receive it before calling Registry.URL.
type URLResolver interface {
	URL(context.Context, string) (string, error)
}

type URLFunc func(context.Context, string) (string, error)

func (f URLFunc) URL(ctx context.Context, name string) (string, error) {
	if f == nil {
		return "", &Error{Code: "invalid_url_resolver"}
	}
	return f(ctx, name)
}
func (URLFunc) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.URLFunc{redacted}") }

// Registration borrows an already initialized backend and an optional URL
// capability. Registry never constructs, closes, or replaces those resources.
// Omitting URL keeps that alias unavailable for URL generation.
type Registration struct {
	Alias   string
	Backend Backend
	URL     URLResolver
}

// Registry is an immutable application-scoped alias snapshot. Copies share the
// explicitly registered backend capabilities; there is no global default,
// lazy factory, hidden filesystem open, or fallback for an unknown alias.
type Registry struct {
	entries map[string]Registration
	aliases []string
}

func NewRegistry(registrations ...Registration) (Registry, error) {
	result := Registry{entries: make(map[string]Registration, len(registrations))}
	for _, registration := range registrations {
		if !validAlias(registration.Alias) {
			return Registry{}, &Error{Code: "invalid_alias"}
		}
		if _, duplicate := result.entries[registration.Alias]; duplicate {
			return Registry{}, &Error{Code: "duplicate_alias"}
		}
		if nilValue(registration.Backend) {
			return Registry{}, &Error{Code: "invalid_backend"}
		}
		if registration.URL != nil && nilValue(registration.URL) {
			return Registry{}, &Error{Code: "invalid_url_resolver"}
		}
		result.entries[registration.Alias] = registration
		result.aliases = append(result.aliases, registration.Alias)
	}
	return result, nil
}

func validAlias(alias string) bool {
	if alias == "" || len(alias) > 64 {
		return false
	}
	for _, c := range alias {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.-", c)) {
			return false
		}
	}
	return alias != "." && alias != ".."
}

func (registry Registry) Aliases() []string { return slices.Clone(registry.aliases) }

func (registry Registry) Lookup(alias string) (Backend, error) {
	if !validAlias(alias) {
		return nil, &Error{Code: "invalid_alias"}
	}
	entry, found := registry.entries[alias]
	if !found {
		return nil, &Error{Code: "unknown_alias"}
	}
	return entry.Backend, nil
}

func (registry Registry) Default() (Backend, error) { return registry.Lookup(DefaultAlias) }

func (registry Registry) URL(ctx context.Context, alias, name string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if _, err := registry.Lookup(alias); err != nil {
		return "", err
	}
	if err := validateName(name); err != nil {
		return "", err
	}
	resolver := registry.entries[alias].URL
	if resolver == nil {
		return "", &Error{Code: "url_unavailable"}
	}
	value, err := resolver.URL(ctx, name)
	if err != nil {
		return "", err
	}
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if _, err := parseURL(value); err != nil {
		return "", err
	}
	return value, nil
}

func (Registration) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.Registration{redacted}") }
func (Registry) Format(s fmt.State, _ rune)     { fmt.Fprint(s, "storage.Registry{redacted}") }
