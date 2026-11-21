package forms

import "strings"

// URLField trims Unicode whitespace and supplies https when no scheme is
// present. Complete URL syntax is validated after that conversion. There is
// no implicit field max_length; the URL grammar bounds input to 2048 runes.
// Model projection supplies the canonical storage length (200 by default).
func URLField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: URLInput, trimWhitespace: true, assumeScheme: "https"}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	if config.assumeScheme == "" {
		config.assumeScheme = "https"
	}
	switch strings.ToLower(config.assumeScheme) {
	case "http", "https", "ftp", "ftps":
	default:
		return Field{}, &ConfigError{Path: "fields." + name + ".assume_scheme", Code: "invalid"}
	}
	return makeField(name, FieldURL, config)
}

// WithAssumeScheme sets the scheme supplied to URL form input when missing.
// An empty value selects https. It does not change explicit submitted schemes.
func WithAssumeScheme(scheme string) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.assumeScheme, config.hasAssumeScheme = scheme, true })
}

func (field Field) AssumeScheme() string { return field.assumeScheme }
