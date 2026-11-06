package settings

import (
	"fmt"
	"strings"
)

const (
	MaximumEnvironmentEntries = 4096
	MaximumEnvironmentBytes   = 1 << 20
)

// Environment is an immutable invocation-local environment snapshot. Its zero
// value is empty. Values may contain credentials: use Lookup only at the
// configuration boundary and do not put its results in diagnostics or logs.
type Environment struct {
	values map[string]string
}

// NewEnvironment snapshots caller-provided NAME=value entries, normally from
// one os.Environ call in a process entrypoint. It does not read or change the
// process environment. Empty values and bytes after the first '=' are kept
// exactly; duplicate names are rejected instead of depending on lookup order.
func NewEnvironment(entries []string) (Environment, error) {
	if len(entries) > MaximumEnvironmentEntries {
		return Environment{}, &Error{Field: "environment", Detail: "entry limit exceeded"}
	}
	values := make(map[string]string, len(entries))
	remaining := MaximumEnvironmentBytes
	for index, entry := range entries {
		field := fmt.Sprintf("environment[%d]", index)
		if len(entry) > remaining {
			return Environment{}, &Error{Field: field, Detail: "byte limit exceeded"}
		}
		remaining -= len(entry)
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || strings.ContainsRune(entry, '\x00') {
			return Environment{}, &Error{Field: field, Detail: "invalid environment entry"}
		}
		if _, exists := values[name]; exists {
			return Environment{}, &Error{Field: field, Detail: "duplicate environment name"}
		}
		values[name] = value
	}
	return Environment{values: values}, nil
}

// Lookup has os.LookupEnv's absent-versus-empty shape without consulting
// process-global state. The returned string must be treated as potentially
// secret. No writable map or slice is exposed, so concurrent reads are safe.
func (environment Environment) Lookup(name string) (string, bool) {
	value, present := environment.values[name]
	return value, present
}

func (Environment) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "settings.Environment{redacted}")
}
