package input_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

func require[T any](t *testing.T) func(T, error) T {
	t.Helper()
	return func(value T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}

func object(t *testing.T, spec serializers.Spec, raw string) serializers.Object {
	t.Helper()
	return require[serializers.Object](t)(spec.DecodeObject([]byte(raw), serializers.Limits{}))
}

func codes(diagnostics validation.Errors) []string {
	result := make([]string, 0, diagnostics.Len())
	for _, violation := range diagnostics.All() {
		entry := string(violation.Field()) + "/" + string(violation.Code())
		for _, param := range violation.Params() {
			entry += "/" + param.Key() + "=" + param.Value()
		}
		result = append(result, entry)
	}
	return result
}

type patchDTO struct {
	Name    input.Presence[string]
	Enabled input.Presence[bool]
	Memo    input.Presence[*string]
	Tags    input.Presence[[]int64]
}

func TestFullPartialPresenceAndAtomicValidation(t *testing.T) {
	f := require[serializers.Field](t)
	fields := []serializers.Field{
		f(serializers.StringField("name", serializers.WithMaxLength(3))),
		f(serializers.BooleanField("enabled", serializers.WithDefault(serializers.Boolean(false)))),
		f(serializers.StringField("memo", serializers.WithNullable(), serializers.WithRequired(false))),
		f(serializers.IntegerListField("tags", serializers.WithDefault(serializers.Integers(1, 1, 3)))),
		f(serializers.IntegerField("id", serializers.WithReadOnly())),
	}
	spec := require[serializers.Spec](t)(serializers.NewSpec(fields))
	var calls []string
	properties := []input.Property[patchDTO]{
		input.Field("tags", input.Integers(), func(out *patchDTO, v input.Presence[[]int64]) { calls = append(calls, "tags"); out.Tags = v }),
		input.Field("memo", input.Nullable(input.String()), func(out *patchDTO, v input.Presence[*string]) { calls = append(calls, "memo"); out.Memo = v }),
		input.Field("name", input.String(), func(out *patchDTO, v input.Presence[string]) { calls = append(calls, "name"); out.Name = v }),
		input.Field("enabled", input.Boolean(), func(out *patchDTO, v input.Presence[bool]) { calls = append(calls, "enabled"); out.Enabled = v }),
	}
	body := require[input.Body[patchDTO]](t)(input.New(spec, api.ParserConfig{}, properties...))
	if len(calls) != 0 {
		t.Fatal("preparation called a setter")
	}
	fields[0], properties[0] = serializers.Field{}, input.Property[patchDTO]{}
	for _, test := range []struct {
		raw  string
		mode serializers.Mode
		want []string
	}{
		{`{}`, serializers.ModeFull, []string{"name/required"}},
		{`{"name":null}`, serializers.ModeFull, []string{"name/null"}},
		{`{"name":"long"}`, serializers.ModeFull, []string{"name/max_length/max_length=3"}},
		{`{"z":1,"memo":4,"name":" ","enabled":9,"tags":[1,null,"x"],"id":1,"a":2}`, serializers.ModeFull,
			[]string{"name/blank", "enabled/type", "memo/type", "tags/null/index=1", "tags/type/index=2", "id/read_only", "a/unknown", "z/unknown"}},
		{`{"name":"ok","memo":false}`, serializers.ModePartial, []string{"memo/type"}},
	} {
		t.Run(test.raw, func(t *testing.T) {
			value, diagnostics, err := body.Bind(t.Context(), object(t, spec, test.raw), test.mode)
			if err != nil || !reflect.DeepEqual(value, patchDTO{}) || len(calls) != 0 || !slices.Equal(codes(diagnostics), test.want) {
				t.Fatal("validation order, partial DTO or setter invocation", value, codes(diagnostics), calls, err)
			}
		})
	}
	value, diagnostics, err := body.Bind(t.Context(), object(t, spec, `{"name":"  한  "}`), serializers.ModeFull)
	name, hasName := value.Name.Get()
	enabled, hasEnabled := value.Enabled.Get()
	memo, hasMemo := value.Memo.Get()
	tags, hasTags := value.Tags.Get()
	if err != nil || !diagnostics.Empty() || name != "한" || !hasName || enabled || !hasEnabled || memo != nil || hasMemo || !hasTags || !slices.Equal(tags, []int64{1, 1, 3}) || !slices.Equal(calls, []string{"name", "enabled", "memo", "tags"}) {
		t.Fatal("full defaults, normalization and Spec setter order", value, diagnostics, calls, err)
	}
	calls = nil
	partial, diagnostics, err := body.Bind(t.Context(), object(t, spec, `{}`), serializers.ModePartial)
	if err != nil || !diagnostics.Empty() || !reflect.DeepEqual(partial, patchDTO{}) || len(calls) != 4 {
		t.Fatal("partial omission applied defaults", partial, err)
	}
	value, diagnostics, err = body.Bind(t.Context(), object(t, spec, `{"memo":null,"enabled":false,"tags":[]}`), serializers.ModePartial)
	memo, hasMemo = value.Memo.Get()
	enabled, hasEnabled = value.Enabled.Get()
	tags, hasTags = value.Tags.Get()
	if err != nil || !diagnostics.Empty() || memo != nil || !hasMemo || enabled || !hasEnabled || !hasTags || tags == nil || len(tags) != 0 {
		t.Fatal("null, false and empty lost presence", value, err)
	}
	for _, mode := range []serializers.Mode{serializers.ModeFull, serializers.ModePartial} {
		got := require[openapi.Schema](t)(body.Schema(mode))
		want := require[openapi.Schema](t)(openapi.RequestSchema(spec, mode))
		encoded := require[[]byte](t)(serializers.Encode(got.Value(), serializers.Limits{}))
		expected := require[[]byte](t)(serializers.Encode(want.Value(), serializers.Limits{}))
		if !bytes.Equal(encoded, expected) {
			t.Fatal("typed schema differs from its exact Spec", string(encoded), string(expected))
		}
	}
}

func TestDefaultsRetainLegacyAndJSONNullMeaning(t *testing.T) {
	f := require[serializers.Field](t)
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{
		f(serializers.StringField("state", serializers.WithChoices(serializers.Choice{Value: serializers.String("active"), Label: "Active"}), serializers.WithDefault(serializers.String("legacy")))),
		f(serializers.EmailField("email", serializers.WithDefault(serializers.String("not an email")))),
		f(serializers.JSONField("literal", serializers.WithNullable(), serializers.WithDefault(serializers.JSON(jsonvalue.Null())))),
		f(serializers.JSONField("model", serializers.WithNullable(), serializers.WithDefault(serializers.Null()))),
	}))
	type row struct {
		State, Email   input.Presence[string]
		Literal, Model input.Presence[*jsonvalue.Value]
	}
	body := require[input.Body[row]](t)(input.New(spec, api.ParserConfig{},
		input.Field("state", input.String(), func(r *row, v input.Presence[string]) { r.State = v }),
		input.Field("email", input.String(), func(r *row, v input.Presence[string]) { r.Email = v }),
		input.Field("literal", input.Nullable(input.JSON()), func(r *row, v input.Presence[*jsonvalue.Value]) { r.Literal = v }),
		input.Field("model", input.Nullable(input.JSON()), func(r *row, v input.Presence[*jsonvalue.Value]) { r.Model = v }),
	))
	value, violations, err := body.Bind(t.Context(), object(t, spec, `{}`), serializers.ModeFull)
	state, hs := value.State.Get()
	email, he := value.Email.Get()
	literal, hl := value.Literal.Get()
	model, hm := value.Model.Get()
	if err != nil || !violations.Empty() || !hs || state != "legacy" || !he || email != "not an email" || !hl || literal == nil || literal.Text != "null" || !hm || model != nil {
		t.Fatal("default revalidation or JSON null meaning changed", value, err)
	}
	value, violations, err = body.Bind(t.Context(), object(t, spec, `{"state":"legacy","email":"not an email"}`), serializers.ModeFull)
	if err != nil || !reflect.DeepEqual(value, row{}) || !slices.Equal(codes(violations), []string{"state/invalid_choice", "email/invalid"}) {
		t.Fatal("submitted values bypassed validation", codes(violations), err)
	}
	value, violations, err = body.Bind(t.Context(), object(t, spec, `{"literal":null}`), serializers.ModePartial)
	literal, hl = value.Literal.Get()
	_, hm = value.Model.Get()
	if err != nil || !violations.Empty() || literal != nil || !hl || hm {
		t.Fatal("submitted null did not use model-null policy", value, err)
	}
}

func TestPreparationRejectsInvalidBindingsBeforeUse(t *testing.T) {
	f := require[serializers.Field](t)
	text := f(serializers.StringField("name"))
	nullable := f(serializers.StringField("name", serializers.WithNullable()))
	readonly := f(serializers.StringField("name", serializers.WithReadOnly()))
	type row struct{}
	calls := 0
	stringProperty := input.Field("name", input.String(), func(*row, input.Presence[string]) { calls++ })
	for _, test := range []struct {
		name       string
		fields     []serializers.Field
		config     api.ParserConfig
		properties []input.Property[row]
	}{
		{name: "missing", fields: []serializers.Field{text}},
		{name: "duplicate", fields: []serializers.Field{text}, properties: []input.Property[row]{stringProperty, stringProperty}},
		{name: "unknown", fields: []serializers.Field{text}, properties: []input.Property[row]{input.Field("private\x00name", input.String(), func(*row, input.Presence[string]) {})}},
		{name: "wrong kind", fields: []serializers.Field{f(serializers.IntegerField("name"))}, properties: []input.Property[row]{stringProperty}},
		{name: "missing nullable", fields: []serializers.Field{nullable}, properties: []input.Property[row]{stringProperty}},
		{name: "extra nullable", fields: []serializers.Field{text}, properties: []input.Property[row]{input.Field("name", input.Nullable(input.String()), func(*row, input.Presence[*string]) {})}},
		{name: "read-only", fields: []serializers.Field{readonly}, properties: []input.Property[row]{stringProperty}},
		{name: "zero property", fields: []serializers.Field{text}, properties: []input.Property[row]{{}}},
		{name: "zero codec", fields: []serializers.Field{text}, properties: []input.Property[row]{input.Field("name", input.Codec[string]{}, func(*row, input.Presence[string]) {})}},
		{name: "nil setter", fields: []serializers.Field{text}, properties: []input.Property[row]{input.Field[row]("name", input.String(), nil)}},
		{name: "nested nullable", fields: []serializers.Field{nullable}, properties: []input.Property[row]{input.Field("name", input.Nullable(input.Nullable(input.String())), func(*row, input.Presence[**string]) {})}},
		{name: "nullable zero", fields: []serializers.Field{nullable}, properties: []input.Property[row]{input.Field("name", input.Nullable(input.Codec[string]{}), func(*row, input.Presence[*string]) {})}},
		{name: "body budget", fields: []serializers.Field{text}, config: api.ParserConfig{MaxBodyBytes: -1}, properties: []input.Property[row]{stringProperty}},
		{name: "JSON budget", fields: []serializers.Field{text}, config: api.ParserConfig{JSONLimits: serializers.Limits{MaxDepth: -1}}, properties: []input.Property[row]{stringProperty}},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := require[serializers.Spec](t)(serializers.NewSpec(test.fields))
			body, err := input.New(spec, test.config, test.properties...)
			if !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) || calls != 0 {
				t.Fatal("invalid preparation", err, calls)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("configuration leaked invalid member name", err)
			}
			if _, schemaErr := body.Schema(serializers.ModeFull); schemaErr == nil {
				t.Fatal("failed construction published a body")
			}
		})
	}
	if _, err := input.New[row](serializers.Spec{}, api.ParserConfig{}); err == nil {
		t.Fatal("zero spec accepted")
	}
	readSpec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{readonly}))
	readBody := require[input.Body[row]](t)(input.New[row](readSpec, api.ParserConfig{}))
	_, diagnostics, err := readBody.Bind(t.Context(), object(t, readSpec, `{"name":"x"}`), serializers.ModePartial)
	if err != nil || !slices.Equal(codes(diagnostics), []string{"name/read_only"}) {
		t.Fatal("read-only validation lost", diagnostics, err)
	}
}

func TestContextAndInvalidUseNeverPublishDTO(t *testing.T) {
	f := require[serializers.Field](t)
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{f(serializers.IntegerField("a")), f(serializers.IntegerField("b"))}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type row struct{ A, B input.Presence[int64] }
	calls := 0
	body := require[input.Body[row]](t)(input.New(spec, api.ParserConfig{},
		input.Field("a", input.Integer(), func(r *row, v input.Presence[int64]) { calls++; r.A = v; cancel() }),
		input.Field("b", input.Integer(), func(r *row, v input.Presence[int64]) { calls++; r.B = v }),
	))
	valid := object(t, spec, `{"a":1,"b":2}`)
	value, diagnostics, err := body.Bind(ctx, valid, serializers.ModeFull)
	if !errors.Is(err, context.Canceled) || !diagnostics.Empty() || calls != 1 || !reflect.DeepEqual(value, row{}) {
		t.Fatal("late cancellation exposed a DTO or called a later setter", value, err, calls)
	}
	_, _, err = body.Bind(ctx, valid, serializers.ModeFull)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("pre-cancelled binding invoked a setter", err)
	}
	for _, mode := range []serializers.Mode{0, 3} {
		if _, _, err := body.Bind(t.Context(), valid, mode); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
			t.Fatal("invalid mode", err)
		}
		if _, err := body.Schema(mode); err == nil {
			t.Fatal("invalid schema mode")
		}
	}
	if _, _, err := body.Bind(nil, valid, serializers.ModeFull); err == nil {
		t.Fatal("nil context")
	}
	if _, _, err := body.Bind(t.Context(), serializers.Object{}, serializers.ModeFull); err == nil {
		t.Fatal("zero object")
	}
	if _, _, err := body.Parse(nil, serializers.ModeFull); !errors.Is(err, &api.Error{Code: api.FailureInvalidRequest}) {
		t.Fatal("nil request", err)
	}
	var zero input.Body[row]
	if _, _, err := zero.Bind(t.Context(), valid, serializers.ModeFull); err == nil {
		t.Fatal("zero body")
	}
	if _, _, err := zero.Parse(nil, serializers.ModeFull); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
		t.Fatal("zero body read request", err)
	}
	if calls != 1 {
		t.Fatal("invalid use invoked a setter", calls)
	}
}

func TestConcurrentBindingsOwnPointersListsAndDefaults(t *testing.T) {
	f := require[serializers.Field](t)
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{
		f(serializers.StringField("memo", serializers.WithNullable(), serializers.WithDefault(serializers.String("shared")))),
		f(serializers.IntegerListField("tags", serializers.WithDefault(serializers.Integers(1, 2, 1)))),
	}))
	body := require[input.Body[patchDTO]](t)(input.New(spec, api.ParserConfig{},
		input.Field("memo", input.Nullable(input.String()), func(r *patchDTO, v input.Presence[*string]) { r.Memo = v }),
		input.Field("tags", input.Integers(), func(r *patchDTO, v input.Presence[[]int64]) { r.Tags = v }),
	))
	empty := object(t, spec, `{}`)
	provided := object(t, spec, `{"memo":"shared","tags":[1,2,1]}`)
	var workers sync.WaitGroup
	for range 24 {
		workers.Go(func() {
			for range 12 {
				for _, source := range []serializers.Object{empty, provided} {
					result, diagnostics, err := body.Bind(t.Context(), source, serializers.ModeFull)
					memo, hm := result.Memo.Get()
					tags, ht := result.Tags.Get()
					if err != nil || !diagnostics.Empty() || !hm || memo == nil || *memo != "shared" || !ht || !slices.Equal(tags, []int64{1, 2, 1}) {
						t.Error("cross-request state", result, err)
						return
					}
					*memo = "local"
					tags[0] = 9
					if _, err := body.Schema(serializers.ModePartial); err != nil {
						t.Error(err)
						return
					}
				}
			}
		})
	}
	workers.Wait()
}
