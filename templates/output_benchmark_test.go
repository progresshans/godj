package templates_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/progresshans/godj/templates"
)

func BenchmarkTemplateRejectedEscape(b *testing.B) {
	engine, err := templates.New(fstest.MapFS{"page": &fstest.MapFile{Data: []byte(`{{ value }}`)}}, templates.Config{Limits: templates.Limits{MaxOutputBytes: 64}})
	if err != nil {
		b.Fatal(err)
	}
	values, err := templates.NewContext(map[string]templates.Value{"value": templates.String(strings.Repeat("&", 1<<20))})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if result, err := engine.Render(context.Background(), "page", values, templates.Capabilities{}); err == nil || result != nil {
			b.Fatal("oversized output was accepted")
		}
	}
}
