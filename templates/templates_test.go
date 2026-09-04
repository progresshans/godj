package templates_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/progresshans/godj/templates"
)

func newEngine(t *testing.T, files map[string]string, config templates.Config) *templates.Engine {
	t.Helper()
	source := make(fstest.MapFS, len(files))
	for name, body := range files {
		source[name] = &fstest.MapFile{Data: []byte(body), Mode: 0o444}
	}
	engine, err := templates.New(source, config)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func contextOf(t *testing.T, values map[string]templates.Value) templates.Context {
	t.Helper()
	context, err := templates.NewContext(values)
	if err != nil {
		t.Fatal(err)
	}
	return context
}

func objectOf(t *testing.T, values map[string]templates.Value) templates.Value {
	t.Helper()
	object, err := templates.Object(values)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func TestScalarMissingAndDottedLookup(t *testing.T) {
	engine := newEngine(t, map[string]string{
		"page.html": `{{ user.name }}|{{ missing }}|{{ rows.1.title }}|{{ rows.9.title }}`,
	}, templates.Config{})
	values := contextOf(t, map[string]templates.Value{
		"user": objectOf(t, map[string]templates.Value{"name": templates.String("Ada")}),
		"rows": templates.List(
			objectOf(t, map[string]templates.Value{"title": templates.String("first")}),
			objectOf(t, map[string]templates.Value{"title": templates.String("second")}),
		),
	})
	output, err := engine.Render(context.Background(), "page.html", values, templates.Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(output), "Ada||second|"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestAutoescapeAndExplicitTrustedHTML(t *testing.T) {
	engine := newEngine(t, map[string]string{"page.html": `{{ unsafe }}|{{ safe }}`}, templates.Config{})
	output, err := engine.Render(context.Background(), "page.html", contextOf(t, map[string]templates.Value{
		"unsafe": templates.String(`<b title="x">&</b>`),
		"safe":   templates.TrustedHTML(`<strong>trusted</strong>`),
	}), templates.Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(output), `&lt;b title=&#34;x&#34;&gt;&amp;&lt;/b&gt;|<strong>trusted</strong>`; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestIfForEmptyAndClosedFilters(t *testing.T) {
	engine := newEngine(t, map[string]string{
		"page.html": `{% if enabled %}{% for item in items %}[{{ forloop.counter }}:{{ item|lower }}]{% empty %}empty{% endfor %}{% else %}off{% endif %}|{{ missing|default:"Fallback"|lower }}|{{ items|length }}`,
	}, templates.Config{})
	values := contextOf(t, map[string]templates.Value{
		"enabled": templates.Bool(true),
		"items":   templates.List(templates.String("ONE"), templates.String("TWO")),
	})
	output, err := engine.Render(context.Background(), "page.html", values, templates.Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(output), "[1:one][2:two]|fallback|2"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	empty, err := engine.Render(context.Background(), "page.html", contextOf(t, map[string]templates.Value{
		"enabled": templates.Bool(true),
		"items":   templates.List(),
	}), templates.Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(empty), "empty|fallback|0"; got != want {
		t.Fatalf("empty output = %q, want %q", got, want)
	}
}

type URLResolver struct{}

func (URLResolver) Reverse(_ context.Context, name string) (string, error) {
	if name != "article:list" {
		return "", fmt.Errorf("unknown route")
	}
	return "/articles/?a=1&b=2", nil
}

type CSRFProvider struct{ token string }

func (provider CSRFProvider) Token(context.Context) (string, error) { return provider.token, nil }

func TestInheritanceIncludeURLAndCSRFCapabilities(t *testing.T) {
	files := map[string]string{
		"base.html":   `<main>{% block body %}base{% endblock %}</main>{% include "footer.html" %}`,
		"child.html":  `{% extends "base.html" %}{% block body %}<h1>{{ title }}</h1>{% endblock %}`,
		"footer.html": `<a href="{% url 'article:list' %}">list</a>{% csrf_token %}`,
	}
	engine := newEngine(t, files, templates.Config{})
	output, err := engine.Render(context.Background(), "child.html", contextOf(t, map[string]templates.Value{
		"title": templates.String("Admin & Forms"),
	}), templates.Capabilities{URL: URLResolver{}, CSRF: CSRFProvider{token: `a"&b`}})
	if err != nil {
		t.Fatal(err)
	}
	want := `<main><h1>Admin &amp; Forms</h1></main><a href="/articles/?a=1&amp;b=2">list</a>` +
		`<input type="hidden" name="csrfmiddlewaretoken" value="a&#34;&amp;b">`
	if got := string(output); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	missingCaps, err := engine.Render(context.Background(), "child.html", contextOf(t, map[string]templates.Value{}), templates.Capabilities{})
	if err == nil || missingCaps != nil {
		t.Fatalf("missing capabilities = %q, %v", missingCaps, err)
	}
}

func TestEngineDetachesFilesystemAndPublishedNames(t *testing.T) {
	source := fstest.MapFS{"page.html": &fstest.MapFile{Data: []byte("stable"), Mode: 0o444}}
	engine, err := templates.New(source, templates.Config{})
	if err != nil {
		t.Fatal(err)
	}
	source["page.html"].Data[0] = 'X'
	names := engine.Names()
	names[0] = "mutated"
	output, err := engine.Render(context.Background(), "page.html", contextOf(t, nil), templates.Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "stable" || engine.Names()[0] != "page.html" {
		t.Fatalf("output = %q, names = %#v", output, engine.Names())
	}
}

func TestConstructionRejectsUnknownPrivateUnclosedAndReferenceFailures(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		code  string
	}{
		{name: "unknown tag", files: map[string]string{"x": `{% execute value %}`}, code: "unknown_tag"},
		{name: "unknown filter", files: map[string]string{"x": `{{ value|escape }}`}, code: "invalid_expression"},
		{name: "private lookup", files: map[string]string{"x": `{{ value._private }}`}, code: "invalid_expression"},
		{name: "negative index", files: map[string]string{"x": `{{ value.-1 }}`}, code: "invalid_expression"},
		{name: "unclosed variable", files: map[string]string{"x": `{{ value`}, code: "unclosed_delimiter"},
		{name: "unclosed if", files: map[string]string{"x": `{% if value %}x`}, code: "unclosed_block"},
		{name: "missing include", files: map[string]string{"x": `{% include "missing" %}`}, code: "missing_template"},
		{name: "include cycle", files: map[string]string{"a": `{% include "b" %}`, "b": `{% include "a" %}`}, code: "template_cycle"},
		{name: "extends content", files: map[string]string{"base": `x`, "child": `bad{% extends "base" %}`}, code: "extends_not_first"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := make(fstest.MapFS, len(test.files))
			for name, body := range test.files {
				source[name] = &fstest.MapFile{Data: []byte(body), Mode: 0o444}
			}
			engine, err := templates.New(source, templates.Config{})
			if err == nil || engine != nil {
				t.Fatalf("New = %#v, %v", engine, err)
			}
			var templateError *templates.Error
			if !errors.As(err, &templateError) || templateError.Code != test.code {
				t.Fatalf("error = %#v, want code %q", err, test.code)
			}
		})
	}
	if _, err := templates.Object(map[string]templates.Value{"_private": templates.String("x")}); err == nil {
		t.Fatal("private object member accepted")
	}
	if _, err := templates.NewContext(map[string]templates.Value{"_private": templates.String("x")}); err == nil {
		t.Fatal("private context member accepted")
	}
}

func TestEveryResourceLimitAndCancellationFailsWithoutPartialOutput(t *testing.T) {
	t.Run("template count", func(t *testing.T) {
		source := fstest.MapFS{
			"a": &fstest.MapFile{Data: []byte("a"), Mode: 0o444},
			"b": &fstest.MapFile{Data: []byte("b"), Mode: 0o444},
		}
		engine, err := templates.New(source, templates.Config{Limits: templates.Limits{MaxTemplates: 1}})
		if err == nil || engine != nil {
			t.Fatalf("New = %#v, %v", engine, err)
		}
	})
	t.Run("source", func(t *testing.T) {
		source := fstest.MapFS{"x": &fstest.MapFile{Data: []byte("1234"), Mode: 0o444}}
		engine, err := templates.New(source, templates.Config{Limits: templates.Limits{MaxTemplateBytes: 3}})
		if err == nil || engine != nil {
			t.Fatalf("New = %#v, %v", engine, err)
		}
	})
	t.Run("total source", func(t *testing.T) {
		source := fstest.MapFS{
			"a": &fstest.MapFile{Data: []byte("123"), Mode: 0o444},
			"b": &fstest.MapFile{Data: []byte("456"), Mode: 0o444},
		}
		engine, err := templates.New(source, templates.Config{Limits: templates.Limits{MaxTotalBytes: 5}})
		if err == nil || engine != nil {
			t.Fatalf("New = %#v, %v", engine, err)
		}
	})
	t.Run("nodes", func(t *testing.T) {
		source := fstest.MapFS{"x": &fstest.MapFile{Data: []byte("a{{ x }}b"), Mode: 0o444}}
		engine, err := templates.New(source, templates.Config{Limits: templates.Limits{MaxParseNodes: 2}})
		if err == nil || engine != nil {
			t.Fatalf("New = %#v, %v", engine, err)
		}
	})
	t.Run("parse depth", func(t *testing.T) {
		source := fstest.MapFS{"x": &fstest.MapFile{Data: []byte(`{% if a %}{% if b %}x{% endif %}{% endif %}`), Mode: 0o444}}
		engine, err := templates.New(source, templates.Config{Limits: templates.Limits{MaxParseDepth: 2}})
		if err == nil || engine != nil {
			t.Fatalf("New = %#v, %v", engine, err)
		}
	})
	t.Run("output", func(t *testing.T) {
		engine := newEngine(t, map[string]string{"x": `ok{{ value }}`}, templates.Config{Limits: templates.Limits{MaxOutputBytes: 3}})
		output, err := engine.Render(context.Background(), "x", contextOf(t, map[string]templates.Value{
			"value": templates.String("too long"),
		}), templates.Capabilities{})
		if err == nil || output != nil {
			t.Fatalf("Render = %q, %v", output, err)
		}
	})
	t.Run("render depth", func(t *testing.T) {
		engine := newEngine(t, map[string]string{
			"a": `{% include "b" %}`,
			"b": `{% include "c" %}`,
			"c": `done`,
		}, templates.Config{Limits: templates.Limits{MaxRenderDepth: 2}})
		output, err := engine.Render(context.Background(), "a", contextOf(t, nil), templates.Capabilities{})
		if err == nil || output != nil {
			t.Fatalf("Render = %q, %v", output, err)
		}
	})
	t.Run("loop", func(t *testing.T) {
		engine := newEngine(t, map[string]string{"x": `{% for item in items %}{{ item }}{% endfor %}`}, templates.Config{Limits: templates.Limits{MaxLoopItems: 2}})
		output, err := engine.Render(context.Background(), "x", contextOf(t, map[string]templates.Value{
			"items": templates.List(templates.Integer(1), templates.Integer(2), templates.Integer(3)),
		}), templates.Capabilities{})
		if err == nil || output != nil {
			t.Fatalf("Render = %q, %v", output, err)
		}
	})
	t.Run("context depth", func(t *testing.T) {
		deep := objectOf(t, map[string]templates.Value{"third": templates.String("x")})
		deep = objectOf(t, map[string]templates.Value{"second": deep})
		engine := newEngine(t, map[string]string{"x": `prefix{{ value.second.third }}`}, templates.Config{Limits: templates.Limits{MaxContextDepth: 2}})
		output, err := engine.Render(context.Background(), "x", contextOf(t, map[string]templates.Value{"value": deep}), templates.Capabilities{})
		if err == nil || output != nil {
			t.Fatalf("Render = %q, %v", output, err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		engine := newEngine(t, map[string]string{"x": `prefix{{ value }}`}, templates.Config{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		output, err := engine.Render(ctx, "x", contextOf(t, map[string]templates.Value{"value": templates.String("secret")}), templates.Capabilities{})
		if !errors.Is(err, context.Canceled) || output != nil {
			t.Fatalf("Render = %q, %v", output, err)
		}
	})
}

func TestEngineIsConcurrentSafe(t *testing.T) {
	engine := newEngine(t, map[string]string{"x": `{% for item in items %}{{ item }}{% endfor %}`}, templates.Config{})
	values := contextOf(t, map[string]templates.Value{"items": templates.List(templates.Integer(1), templates.Integer(2))})
	const workers = 64
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			output, err := engine.Render(context.Background(), "x", values, templates.Capabilities{})
			if err != nil || string(output) != "12" {
				t.Errorf("Render = %q, %v", output, err)
			}
		}()
	}
	wait.Wait()
}

func FuzzEngineConstruction(f *testing.F) {
	f.Add("plain")
	f.Add("{{ value|lower }}")
	f.Add("{% if value %}yes{% else %}no{% endif %}")
	f.Fuzz(func(t *testing.T, source string) {
		engine, err := templates.New(fstest.MapFS{
			"fuzz.html": &fstest.MapFile{Data: []byte(source), Mode: 0o444},
		}, templates.Config{Limits: templates.Limits{
			MaxTemplateBytes: 4096,
			MaxTotalBytes:    4096,
			MaxParseNodes:    256,
			MaxParseDepth:    16,
		}})
		if err == nil {
			_, _ = engine.Render(context.Background(), "fuzz.html", contextOf(t, map[string]templates.Value{
				"value": templates.String("value"),
			}), templates.Capabilities{})
		}
	})
}
