package templates_test

import (
	"context"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/progresshans/godj/templates"
)

func BenchmarkTemplateInheritance(b *testing.B) {
	for _, depth := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			source := fstest.MapFS{"page_0": &fstest.MapFile{Data: []byte(`<main>{% block body %}base{% endblock %}</main>`)}}
			for level := 1; level < depth; level++ {
				body := fmt.Sprintf(`{%% extends "page_%d" %%}{%% block body %%}{{ title }}{%% endblock %%}`, level-1)
				source[fmt.Sprintf("page_%d", level)] = &fstest.MapFile{Data: []byte(body)}
			}
			engine, err := templates.New(source, templates.Config{})
			if err != nil {
				b.Fatal(err)
			}
			values, err := templates.NewContext(map[string]templates.Value{"title": templates.String("Example")})
			if err != nil {
				b.Fatal(err)
			}
			name := fmt.Sprintf("page_%d", depth-1)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := engine.Render(context.Background(), name, values, templates.Capabilities{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkTemplateComposition(b *testing.B) {
	child, err := templates.Object(map[string]templates.Value{"title": templates.String("Example"), "enabled": templates.Bool(true)})
	if err != nil {
		b.Fatal(err)
	}
	items := make([]templates.Value, 1000)
	for index := range items {
		items[index] = child
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := templates.NewContext(map[string]templates.Value{"items": templates.List(items...)}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTemplateLoop(b *testing.B) {
	engine, err := templates.New(fstest.MapFS{"page": &fstest.MapFile{Data: []byte(`{% for item in items %}{{ item }}{% if forloop.last %}!{% endif %}{% endfor %}`)}}, templates.Config{})
	if err != nil {
		b.Fatal(err)
	}
	items := make([]templates.Value, 1000)
	for index := range items {
		items[index] = templates.String("item")
	}
	values, err := templates.NewContext(map[string]templates.Value{"items": templates.List(items...)})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := engine.Render(context.Background(), "page", values, templates.Capabilities{}); err != nil {
			b.Fatal(err)
		}
	}
}
