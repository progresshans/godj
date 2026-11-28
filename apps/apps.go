// Package apps defines an immutable registry of installed application
// identities. Runtime code receives snapshots; callers cannot mutate registry
// state after construction.
package apps

import (
	"strings"

	"github.com/progresshans/godj/internal/identifiers"
)

// Config identifies one installed application. Name is its stable project
// identity (normally an import path), while Label is its short routing and
// runtime namespace.
type Config struct {
	Name  string
	Label string
}

// Registry is an immutable installed-application snapshot. Its zero value is
// an empty registry.
type Registry struct {
	configs []Config
	byLabel map[string]int
}

// New validates and snapshots installed applications in declaration order.
func New(configs []Config) (Registry, error) {
	result := Registry{
		configs: make([]Config, len(configs)),
		byLabel: make(map[string]int, len(configs)),
	}
	byName := make(map[string]struct{}, len(configs))
	for index, config := range configs {
		config.Name = strings.TrimSpace(config.Name)
		if config.Name == "" {
			return Registry{}, &Error{Code: CodeInvalidConfig, Field: "name", Detail: "name is empty"}
		}
		if !identifiers.SQL(config.Label) {
			return Registry{}, &Error{
				Code:   CodeInvalidConfig,
				Field:  "label",
				Detail: "label must use the canonical schema namespace: lowercase ASCII letters, digits and underscores, without a leading digit",
			}
		}
		if _, exists := byName[config.Name]; exists {
			return Registry{}, &Error{Code: CodeDuplicateName, Field: "name", Detail: "name is already installed"}
		}
		if _, exists := result.byLabel[config.Label]; exists {
			return Registry{}, &Error{Code: CodeDuplicateLabel, Field: "label", Detail: "label is already installed"}
		}
		result.configs[index] = config
		byName[config.Name] = struct{}{}
		result.byLabel[config.Label] = index
	}
	return result, nil
}

// All returns an independent copy in declaration order.
func (r Registry) All() []Config {
	return append([]Config(nil), r.configs...)
}

// Lookup returns an application by label.
func (r Registry) Lookup(label string) (Config, bool) {
	index, ok := r.byLabel[label]
	if !ok || index < 0 || index >= len(r.configs) {
		return Config{}, false
	}
	return r.configs[index], true
}
