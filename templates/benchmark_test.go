package templates_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/progresshans/godj/templates"
)

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
