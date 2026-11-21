package command

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Kind string

const (
	String  Kind = "string"
	Integer Kind = "integer"
	Boolean Kind = "boolean"
)

type Argument struct {
	Name, Help     string
	Kind           Kind
	Required, Many bool
}
type Option struct {
	Name, Help string
	Kind       Kind
	Required   bool
}

// Values owns one parsed invocation. Accessors return copies; options are
// absent unless provided. No implicit defaults are invented by the parser.
type Values struct{ entries map[string][]string }

func (v Values) Format(s fmt.State, _ rune) { fmt.Fprint(s, "command.Values{redacted}") }
func (v Values) All(name string) []string   { return append([]string(nil), v.entries[name]...) }
func (v Values) String(name string) (string, bool) {
	values := v.entries[name]
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}
func (v Values) Integer(name string) (int64, bool) {
	value, ok := v.String(name)
	if !ok {
		return 0, false
	}
	number, err := strconv.ParseInt(value, 10, 64)
	return number, err == nil
}
func (v Values) Boolean(name string) (bool, bool) {
	value, ok := v.String(name)
	if !ok {
		return false, false
	}
	switch value {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

func validKind(kind Kind) bool { return kind == String || kind == Integer || kind == Boolean }
func validParameters(d Definition) bool {
	if len(d.Arguments)+len(d.Options) > MaxArguments {
		return false
	}
	names := make(map[string]bool)
	optional := false
	for i, a := range d.Arguments {
		if !validName(a.Name) || !validText(a.Help, 2048) || !validKind(a.Kind) || names[a.Name] || a.Many && i != len(d.Arguments)-1 || optional && a.Required {
			return false
		}
		optional = optional || !a.Required
		names[a.Name] = true
	}
	for _, o := range d.Options {
		if !validName(o.Name) || o.Name == "help" || !validText(o.Help, 2048) || !validKind(o.Kind) || names[o.Name] {
			return false
		}
		names[o.Name] = true
	}
	return true
}

func parse(d Definition, argv []string) (Values, error) {
	bad := func() (Values, error) { return Values{}, failed("invalid_arguments") }
	if len(argv) > MaxArguments {
		return bad()
	}
	args := append([]string(nil), argv...)
	total := 0
	for _, arg := range args {
		total += len(arg)
		if len(arg) > MaxArgumentBytes || total > MaxInputBytes || !utf8.ValidString(arg) || strings.IndexByte(arg, 0) >= 0 {
			return bad()
		}
	}
	options := make(map[string]Option, len(d.Options))
	for _, o := range d.Options {
		options[o.Name] = o
	}
	result := Values{entries: make(map[string][]string)}
	var positionals []string
	terminated := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !terminated && arg == "--" {
			terminated = true
			continue
		}
		if !terminated && strings.HasPrefix(arg, "--") {
			name, value, equals := strings.Cut(arg[2:], "=")
			option, exists := options[name]
			if !exists || result.entries[name] != nil {
				return bad()
			}
			if !equals {
				if option.Kind == Boolean {
					value = "true"
				} else {
					i++
					if i == len(args) {
						return bad()
					}
					value = args[i]
					if strings.HasPrefix(value, "--") {
						return bad()
					}
				}
			}
			if !validValue(value, option.Kind) {
				return bad()
			}
			result.entries[name] = []string{value}
		} else {
			positionals = append(positionals, arg)
		}
	}
	for _, option := range d.Options {
		if option.Required && result.entries[option.Name] == nil {
			return bad()
		}
	}
	cursor := 0
	for _, a := range d.Arguments {
		if cursor == len(positionals) {
			if a.Required {
				return bad()
			}
			continue
		}
		end := cursor + 1
		if a.Many {
			end = len(positionals)
		}
		for _, value := range positionals[cursor:end] {
			if !validValue(value, a.Kind) {
				return bad()
			}
		}
		result.entries[a.Name] = append([]string(nil), positionals[cursor:end]...)
		cursor = end
	}
	if cursor != len(positionals) {
		return bad()
	}
	return result, nil
}

func validValue(value string, kind Kind) bool {
	switch kind {
	case String:
		return true
	case Boolean:
		return value == "true" || value == "false"
	case Integer:
		n, err := strconv.ParseInt(value, 10, 64)
		return err == nil && strconv.FormatInt(n, 10) == value
	}
	return false
}
