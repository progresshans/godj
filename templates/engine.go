package templates

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// Engine is an immutable, concurrent-safe collection of startup-compiled
// templates. It retains no filesystem handle or mutable loader cache.
type Engine struct {
	templates map[string]*compiledTemplate
	names     []string
	limits    Limits
}

// New loads every regular file under Config.Root. All files and cross-template
// references are validated before an Engine is returned; failures publish no
// partial engine.
func New(source fs.FS, config Config) (*Engine, error) {
	if source == nil {
		return nil, &Error{Phase: "load", Code: "nil_filesystem"}
	}
	limits, err := normalizeLimits(config.Limits)
	if err != nil {
		return nil, &Error{Phase: "load", Code: "invalid_limits", Cause: err}
	}
	root := config.Root
	if root == "" {
		root = "."
	}
	root = path.Clean(root)
	if !fs.ValidPath(root) || root == ".." || strings.HasPrefix(root, "../") {
		return nil, &Error{Phase: "load", Code: "invalid_root", Template: root}
	}
	rootFS := source
	if root != "." {
		rootFS, err = fs.Sub(source, root)
		if err != nil {
			return nil, &Error{Phase: "load", Code: "open_root", Template: root, Cause: err}
		}
	}

	compiled := make(map[string]*compiledTemplate)
	names := make([]string, 0)
	totalBytes, totalNodes := 0, 0
	err = fs.WalkDir(rootFS, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return &Error{Phase: "load", Code: "walk", Template: name, Cause: walkErr}
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return &Error{Phase: "load", Code: "unsupported_file", Template: name}
		}
		if !validTemplateOrRouteName(name, true) {
			return &Error{Phase: "load", Code: "invalid_template_name", Template: name}
		}
		if len(compiled) >= limits.MaxTemplates {
			return &Error{Phase: "load", Code: "template_count_exceeded", Template: name}
		}
		data, err := readBounded(rootFS, name, limits.MaxTemplateBytes)
		if err != nil {
			return err
		}
		totalBytes += len(data)
		if totalBytes > limits.MaxTotalBytes {
			return &Error{Phase: "load", Code: "total_source_exceeded", Template: name}
		}
		template, nodes, err := parseTemplate(name, string(data), limits)
		if err != nil {
			return err
		}
		totalNodes += nodes
		if totalNodes > limits.MaxParseNodes {
			return &Error{Phase: "parse", Code: "parse_nodes_exceeded", Template: name}
		}
		compiled[name] = template
		names = append(names, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(compiled) == 0 {
		return nil, &Error{Phase: "load", Code: "no_templates", Template: root}
	}
	for _, name := range names {
		for _, reference := range compiled[name].refs {
			if _, ok := compiled[reference]; !ok {
				return nil, &Error{Phase: "link", Code: "missing_template", Template: name,
					Cause: fmt.Errorf("reference %q", reference)}
			}
		}
	}
	if err := rejectReferenceCycles(compiled, names); err != nil {
		return nil, err
	}
	sort.Strings(names)
	return &Engine{templates: compiled, names: append([]string(nil), names...), limits: limits}, nil
}

func readBounded(source fs.FS, name string, limit int) ([]byte, error) {
	file, err := source.Open(name)
	if err != nil {
		return nil, &Error{Phase: "load", Code: "open", Template: name, Cause: err}
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, &Error{Phase: "load", Code: "read", Template: name, Cause: readErr}
	}
	if closeErr != nil {
		return nil, &Error{Phase: "load", Code: "close", Template: name, Cause: closeErr}
	}
	if len(data) > limit {
		return nil, &Error{Phase: "load", Code: "template_source_exceeded", Template: name}
	}
	return data, nil
}

func rejectReferenceCycles(templates map[string]*compiledTemplate, names []string) error {
	const (
		unseen uint8 = iota
		visiting
		visited
	)
	state := make(map[string]uint8, len(templates))
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case visiting:
			return &Error{Phase: "link", Code: "template_cycle", Template: name}
		case visited:
			return nil
		}
		state[name] = visiting
		for _, reference := range templates[name].refs {
			if err := visit(reference); err != nil {
				return err
			}
		}
		state[name] = visited
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

// Names returns compiled template names in deterministic lexical order.
func (e *Engine) Names() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.names...)
}

// Render evaluates one template into an internal bounded buffer. Errors and
// cancellation always return nil output, never partially rendered bytes.
func (e *Engine) Render(ctx context.Context, name string, values Context, capabilities Capabilities) ([]byte, error) {
	if e == nil {
		return nil, &Error{Phase: "render", Code: "nil_engine", Template: name}
	}
	if ctx == nil {
		return nil, &Error{Phase: "render", Code: "nil_context", Template: name}
	}
	if _, ok := e.templates[name]; !ok {
		return nil, &Error{Phase: "render", Code: "unknown_template", Template: name}
	}
	if err := validateContext(values, e.limits.MaxContextDepth); err != nil {
		return nil, &Error{Phase: "render", Code: "invalid_context", Template: name, Cause: err}
	}
	state := renderState{
		engine:       e,
		capabilities: capabilities,
		output:       boundedOutput{limit: e.limits.MaxOutputBytes},
	}
	if err := state.renderTemplate(ctx, name, values, nil, 1); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, renderError(name, 0, 0, "context_canceled", err)
	}
	return append([]byte(nil), state.output.bytes...), nil
}

func validateContext(context Context, maxDepth int) error {
	for name, value := range context.values {
		if !validIdentifier(name) {
			return &ValueError{Path: name, Code: "invalid_context_name"}
		}
		if err := validateValue(value, 1, maxDepth, name); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(value Value, depth, maxDepth int, path string) error {
	if depth > maxDepth {
		return &ValueError{Path: path, Code: "context_depth_exceeded"}
	}
	switch value.kind {
	case ValueNull, ValueBoolean, ValueInteger:
		return nil
	case ValueString, ValueSafeHTML:
		if !validTextValue(value.text) {
			return &ValueError{Path: path, Code: "invalid_text"}
		}
		return nil
	case ValueList:
		for index, item := range value.list {
			if err := validateValue(item, depth+1, maxDepth, fmt.Sprintf("%s.%d", path, index)); err != nil {
				return err
			}
		}
		return nil
	case ValueObject:
		for name, item := range value.object {
			if !validIdentifier(name) {
				return &ValueError{Path: path + "." + name, Code: "invalid_key"}
			}
			if err := validateValue(item, depth+1, maxDepth, path+"."+name); err != nil {
				return err
			}
		}
		return nil
	default:
		return &ValueError{Path: path, Code: "unknown_kind"}
	}
}

func validTextValue(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
