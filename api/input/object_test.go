package input_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	article "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
)

type envelopeCommand struct{ Title input.Presence[*string] }
type modelEnvelope struct {
	Fields     orm.ModelInput[article.Article]
	Credential envelopeCommand
}

func modelEnvelopeBody(t *testing.T, calls *atomic.Int64) (input.Body[modelEnvelope], input.Body[envelopeCommand]) {
	t.Helper()
	_, model := articleModelBody(t)
	field := require[serializers.Field](t)(serializers.StringField("title", serializers.WithNullable(), serializers.WithTrimWhitespace(false)))
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{field}))
	command := require[input.Body[envelopeCommand]](t)(input.New(spec, api.ParserConfig{}, input.Field("title", input.Nullable(input.String()), func(out *envelopeCommand, value input.Presence[*string]) { calls.Add(1); out.Title = value })))
	body := require[input.Body[modelEnvelope]](t)(input.Object(api.ParserConfig{},
		input.ObjectField("fields", model, func(out *modelEnvelope, value orm.ModelInput[article.Article]) { calls.Add(1); out.Fields = value }),
		input.ObjectField("credential", command, func(out *modelEnvelope, value envelopeCommand) { calls.Add(1); out.Credential = value }),
	))
	return body, command
}

func TestObjectBodyKeepsModelSourcePresenceAndCommandNamespace(t *testing.T) {
	var calls atomic.Int64
	body, command := modelEnvelopeBody(t, &calls)
	if calls.Load() != 0 {
		t.Fatal("startup invoked a setter")
	}
	raw := require[serializers.Object](t)(serializers.DecodeObject([]byte(`{"fields":{"title":"  stored  "},"credential":{"title":"  private  "}}`), serializers.Limits{}))
	value, diagnostics, err := body.Bind(t.Context(), raw, serializers.ModeFull)
	if err != nil || !diagnostics.Empty() || calls.Load() != 3 {
		t.Fatal("envelope binding", diagnostics, err, calls.Load())
	}
	prepared := require[orm.PreparedModel[article.Article]](t)(article.ArticleObjects.PrepareInput(value.Fields, nil))
	model := require[article.Article](t)(prepared.Model())
	secret, present := value.Credential.Title.Get()
	if model.Title != "stored" || secret == nil || *secret != "  private  " || !present {
		t.Fatal("model/command names or normalization collided")
	}
	if published, present := value.Fields.Get("published"); !present || !published.Equal(query.Boolean(false)) {
		t.Fatal("IR default lost")
	}
	*secret = "changed"
	fresh, _, err := body.Bind(t.Context(), raw, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := fresh.Credential.Title.Get()
	if other == nil || *other != "  private  " {
		t.Fatal("nested pointer retained across requests")
	}
	empty := require[serializers.Object](t)(serializers.DecodeObject([]byte(`{"fields":{},"credential":{}}`), serializers.Limits{}))
	partial, diagnostics, err := body.Bind(t.Context(), empty, serializers.ModePartial)
	if err != nil || !diagnostics.Empty() || len(partial.Fields.Values()) != 0 {
		t.Fatal("partial model input lost source or omission", err, diagnostics)
	}
	current := article.NewArticleWithID(7)
	current.Title = "retained"
	current.Published = true
	unchanged := require[orm.PreparedModel[article.Article]](t)(article.ArticleObjects.PrepareInput(partial.Fields, &current))
	if row := require[article.Article](t)(unchanged.Model()); row.Title != "retained" || !row.Published {
		t.Fatal("partial child applied full defaults")
	}
	if _, present := partial.Credential.Title.Get(); present {
		t.Fatal("partial child invented credential")
	}
	modelSpec, _ := articleModelBody(t)
	for _, mode := range []serializers.Mode{serializers.ModeFull, serializers.ModePartial} {
		expected := require[openapi.Schema](t)(openapi.Object(
			openapi.Property{Name: "fields", Schema: require[openapi.Schema](t)(openapi.RequestSchema(modelSpec, mode)), Required: true},
			openapi.Property{Name: "credential", Schema: require[openapi.Schema](t)(command.Schema(mode)), Required: true},
		))
		got := require[openapi.Schema](t)(body.Schema(mode))
		if !reflect.DeepEqual(expected, got) {
			t.Fatal("envelope schema lost exact child declarations")
		}
	}
}

func TestObjectBodyValidatesEveryChildBeforeAnySetter(t *testing.T) {
	var calls atomic.Int64
	body, _ := modelEnvelopeBody(t, &calls)
	for name, test := range map[string]struct {
		raw  string
		mode serializers.Mode
		want []string
	}{
		"missing_full":    {`{}`, serializers.ModeFull, []string{"/fields/required", "/credential/required"}},
		"missing_partial": {`{}`, serializers.ModePartial, []string{"/fields/required", "/credential/required"}},
		"first_invalid":   {`{"fields":{"id":8},"credential":{"title":"private"}}`, serializers.ModeFull, []string{"/fields/id/read_only", "/fields/title/required"}},
		"second_invalid":  {`{"fields":{"title":"valid"},"credential":{"title":4}}`, serializers.ModeFull, []string{"/credential/title/type"}},
		"null_object":     {`{"fields":null,"credential":{}}`, serializers.ModeFull, []string{"/fields/type", "/credential/title/required"}},
		"array_object":    {`{"fields":[],"credential":false}`, serializers.ModePartial, []string{"/fields/type", "/credential/type"}},
		"all_diagnostics": {`{"fields":{"title":null,"id":1,"hidden":true},"credential":{"title":3,"x/y":true},"z":1,"a~b":2}`, serializers.ModeFull, []string{"/fields/id/read_only", "/fields/title/null", "/fields/hidden/unknown", "/credential/title/type", "/credential/x~1y/unknown", "/a~0b/unknown", "/z/unknown"}},
	} {
		t.Run(name, func(t *testing.T) {
			object := require[serializers.Object](t)(serializers.DecodeObject([]byte(test.raw), serializers.Limits{}))
			value, diagnostics, err := body.Bind(t.Context(), object, test.mode)
			if err != nil || !reflect.DeepEqual(value, modelEnvelope{}) || !slices.Equal(codes(diagnostics), test.want) || calls.Load() != 0 {
				t.Fatal("partial DTO, setter or diagnostic order", codes(diagnostics), err, calls.Load())
			}
		})
	}
}

func TestObjectBodyPreservesUnsubmittedLegacyDefaultsAndNestedPaths(t *testing.T) {
	type child struct{ State input.Presence[string] }
	type outer struct{ Child child }
	type request struct{ Outer outer }
	f := require[serializers.Field](t)(serializers.StringField("state", serializers.WithChoices(serializers.Choice{Value: serializers.String("current"), Label: "Current"}), serializers.WithDefault(serializers.String("legacy"))))
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{f}))
	leaf := require[input.Body[child]](t)(input.New(spec, api.ParserConfig{}, input.Field("state", input.String(), func(value *child, state input.Presence[string]) { value.State = state })))
	middle := require[input.Body[outer]](t)(input.Object(api.ParserConfig{}, input.ObjectField("child", leaf, func(value *outer, child child) { value.Child = child })))
	body := require[input.Body[request]](t)(input.Object(api.ParserConfig{}, input.ObjectField("outer", middle, func(value *request, outer outer) { value.Outer = outer })))
	for _, mode := range []serializers.Mode{serializers.ModeFull, serializers.ModePartial} {
		value, diagnostics, err := body.Bind(t.Context(), require[serializers.Object](t)(serializers.DecodeObject([]byte(`{"outer":{"child":{}}}`), serializers.Limits{})), mode)
		state, present := value.Outer.Child.State.Get()
		if err != nil || !diagnostics.Empty() || present != (mode == serializers.ModeFull) || present && state != "legacy" {
			t.Fatal("default was rebound as submitted input", err, diagnostics)
		}
	}
	_, diagnostics, err := body.Bind(t.Context(), require[serializers.Object](t)(serializers.DecodeObject([]byte(`{"outer":{"child":{"state":"legacy","a/b":1,"a~b":2}}}`), serializers.Limits{})), serializers.ModeFull)
	if err != nil || !slices.Equal(codes(diagnostics), []string{"/outer/child/state/invalid_choice", "/outer/child/a~1b/unknown", "/outer/child/a~0b/unknown"}) {
		t.Fatal("nested location or submitted legacy policy", codes(diagnostics), err)
	}
}

func TestObjectBodyCancellationAndConcurrentOwnership(t *testing.T) {
	type child struct {
		Value input.Presence[*string]
		IDs   input.Presence[[]int64]
	}
	type outer struct{ Left, Right child }
	f := require[serializers.Field](t)
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{f(serializers.StringField("value", serializers.WithNullable(), serializers.WithDefault(serializers.String("seed")))), f(serializers.IntegerListField("ids", serializers.WithDefault(serializers.Integers(1, 2))))}))
	makeBody := func(after func()) input.Body[outer] {
		leaf := require[input.Body[child]](t)(input.New(spec, api.ParserConfig{}, input.Field("value", input.Nullable(input.String()), func(out *child, value input.Presence[*string]) { out.Value = value; after() }), input.Field("ids", input.Integers(), func(out *child, value input.Presence[[]int64]) { out.IDs = value; after() })))
		return require[input.Body[outer]](t)(input.Object(api.ParserConfig{}, input.ObjectField("left", leaf, func(out *outer, value child) { out.Left = value; after() }), input.ObjectField("right", leaf, func(out *outer, value child) { out.Right = value; after() })))
	}
	empty := require[serializers.Object](t)(serializers.DecodeObject([]byte(`{"left":{},"right":{}}`), serializers.Limits{}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	body := makeBody(func() { calls++; cancel() })
	value, diagnostics, err := body.Bind(ctx, empty, serializers.ModeFull)
	if !errors.Is(err, context.Canceled) || !diagnostics.Empty() || !reflect.DeepEqual(value, outer{}) || calls != 1 {
		t.Fatal("late cancellation published value or ran a later setter", err, calls)
	}
	if _, _, err := body.Bind(ctx, empty, serializers.ModeFull); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("pre-cancelled binding ran setter", err)
	}
	if _, _, err := body.Bind(nil, empty, serializers.ModeFull); err == nil {
		t.Fatal("nil context")
	}
	if _, _, err := body.Bind(t.Context(), serializers.Object{}, serializers.ModeFull); err == nil {
		t.Fatal("zero object")
	}
	if _, _, err := body.Bind(t.Context(), empty, 0); err == nil {
		t.Fatal("invalid mode")
	}
	stable := makeBody(func() {})
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			for range 8 {
				value, diagnostics, err := stable.Bind(t.Context(), empty, serializers.ModeFull)
				if err != nil || !diagnostics.Empty() {
					t.Error("concurrent binding", err)
					return
				}
				left, _ := value.Left.Value.Get()
				right, _ := value.Right.Value.Get()
				li, _ := value.Left.IDs.Get()
				ri, _ := value.Right.IDs.Get()
				if left == nil || right == nil || *left != "seed" || *right != "seed" || !slices.Equal(li, []int64{1, 2}) || !slices.Equal(ri, []int64{1, 2}) {
					t.Error("shared mutable default")
					return
				}
				*left = "changed"
				li[0] = 9
				if *right != "seed" || ri[0] != 1 {
					t.Error("same child declaration shared request values")
					return
				}
			}
		})
	}
	workers.Wait()
}

func TestObjectBodyRejectsInvalidConstructionWithoutCallbacks(t *testing.T) {
	var calls atomic.Int64
	_, leaf := modelEnvelopeBody(t, &calls)
	type outer struct{ Child envelopeCommand }
	property := input.ObjectField("child", leaf, func(out *outer, value envelopeCommand) { calls.Add(1); out.Child = value })
	for name, properties := range map[string][]input.ObjectProperty[outer]{
		"empty": nil, "zero": {{}}, "duplicate": {property, property},
		"invalid_name": {input.ObjectField("private/name", leaf, func(*outer, envelopeCommand) { calls.Add(1) })},
		"nil_setter":   {input.ObjectField[outer]("child", leaf, nil)},
		"zero_child":   {input.ObjectField("child", input.Body[envelopeCommand]{}, func(*outer, envelopeCommand) { calls.Add(1) })},
	} {
		t.Run(name, func(t *testing.T) {
			body, err := input.Object(api.ParserConfig{}, properties...)
			if err == nil || calls.Load() != 0 || strings.Contains(err.Error(), "private") {
				t.Fatal("invalid construction ran work or leaked", err)
			}
			if _, err := body.Schema(serializers.ModeFull); err == nil {
				t.Fatal("invalid body published")
			}
		})
	}
	if _, err := input.Object(api.ParserConfig{MaxBodyBytes: -1}, property); err == nil || calls.Load() != 0 {
		t.Fatal("invalid parser published")
	}
	properties := []input.ObjectProperty[outer]{property}
	body := require[input.Body[outer]](t)(input.Object(api.ParserConfig{}, properties...))
	properties[0] = input.ObjectProperty[outer]{}
	if _, _, err := body.Bind(t.Context(), require[serializers.Object](t)(serializers.DecodeObject([]byte(`{"child":{"title":null}}`), serializers.Limits{})), serializers.ModeFull); err != nil || calls.Load() != 2 {
		t.Fatal("caller property mutation changed body", err, calls.Load())
	}
}
