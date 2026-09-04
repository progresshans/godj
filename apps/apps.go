// Package apps defines an immutable registry of installed application
// identities. Runtime code receives snapshots; callers cannot mutate registry
// state after construction.
package apps

import (
	"strings"
	"unicode"
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
	byName  map[string]int
	byLabel map[string]int
}

// New validates and snapshots installed applications in declaration order.
func New(configs []Config) (Registry, error) {
	result := Registry{
		configs: make([]Config, len(configs)),
		byName:  make(map[string]int, len(configs)),
		byLabel: make(map[string]int, len(configs)),
	}
	for index, config := range configs {
		config.Name = strings.TrimSpace(config.Name)
		config.Label = strings.TrimSpace(config.Label)
		if config.Name == "" {
			return Registry{}, &Error{Code: CodeInvalidConfig, Field: "name", Detail: "name is empty"}
		}
		if !validLabel(config.Label) {
			return Registry{}, &Error{
				Code:   CodeInvalidConfig,
				Field:  "label",
				Detail: "label must start with a letter and contain only letters, digits, or underscores",
			}
		}
		if _, exists := result.byName[config.Name]; exists {
			return Registry{}, &Error{Code: CodeDuplicateName, Field: "name", Detail: "name is already installed"}
		}
		if _, exists := result.byLabel[config.Label]; exists {
			return Registry{}, &Error{Code: CodeDuplicateLabel, Field: "label", Detail: "label is already installed"}
		}
		result.configs[index] = config
		result.byName[config.Name] = index
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

func validLabel(label string) bool {
	for index, character := range label {
		if index == 0 {
			if !unicode.IsLetter(character) {
				return false
			}
			continue
		}
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_' {
			return false
		}
	}
	return label != ""
}
