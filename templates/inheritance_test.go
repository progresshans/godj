package templates_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/progresshans/godj/templates"
)

func TestPreparedInheritancePreservesNestedOverridesAndIncludeScope(t *testing.T) {
	engine := newEngine(t, map[string]string{
		"base":          `<{% block body %}base{% block inner %}base-inner{% endblock %}{% endblock %}>[{% block side %}base-side{% endblock %}]`,
		"middle":        `{% extends "base" %}{% block body %}middle:{% block inner %}middle-inner{% endblock %}|{% include "included" %}{% endblock %}`,
		"leaf":          `{% extends "middle" %}{% block inner %}{{ title }}{% endblock %}{% block side %}leaf-side{% endblock %}`,
		"empty":         `{% extends "leaf" %}`,
		"included-base": `{% block inner %}included-default{% endblock %}`,
		"included":      `{% extends "included-base" %}{% block inner %}included{% endblock %}`,
	}, templates.Config{})
	values := contextOf(t, map[string]templates.Value{"title": templates.String("<&")})
	wants := map[string]string{
		"base":   "<basebase-inner>[base-side]",
		"middle": "<middle:middle-inner|included>[base-side]",
		"leaf":   "<middle:&lt;&amp;|included>[leaf-side]",
		"empty":  "<middle:&lt;&amp;|included>[leaf-side]",
	}
	var workers sync.WaitGroup
	for name, want := range wants {
		workers.Go(func() {
			for range 8 {
				output, err := engine.Render(context.Background(), name, values, templates.Capabilities{})
				if err != nil || string(output) != want {
					t.Errorf("%s render = %q, %v", name, output, err)
					return
				}
				output[0] = '!'
			}
		})
	}
	workers.Wait()
}

func TestPreparedInheritanceKeepsRenderDepthFailureLocation(t *testing.T) {
	files := map[string]string{
		"base":   `{% block body %}base{% endblock %}`,
		"middle": `{% extends "base" %}`,
		"leaf":   `{% extends "middle" %}{% block body %}leaf{% endblock %}`,
		"page":   `{% include "leaf" %}`,
	}
	for _, test := range []struct {
		name    string
		depth   int
		failure string
	}{
		{"leaf", 1, "middle"}, {"leaf", 2, "base"}, {"page", 2, "middle"}, {"page", 3, "base"},
	} {
		engine := newEngine(t, files, templates.Config{Limits: templates.Limits{MaxRenderDepth: test.depth}})
		output, err := engine.Render(context.Background(), test.name, contextOf(t, nil), templates.Capabilities{})
		var render *templates.Error
		if output != nil || !errors.As(err, &render) || render.Phase != "render" || render.Code != "render_depth_exceeded" || render.Template != test.failure {
			t.Fatalf("%s depth %d = %q, %#v", test.name, test.depth, output, err)
		}
	}
}
