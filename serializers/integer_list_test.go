package serializers_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func TestIntegerListTransportPresenceTypesAndExactKeys(t *testing.T) {
	field, err := serializers.IntegerListField("labels", serializers.WithRequired(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		text    string
		present bool
		keys    []int64
		codes   []string
		indices []string
	}{
		{`{}`, false, nil, nil, nil},
		{`{"labels":[]}`, true, []int64{}, nil, nil},
		{`{"labels":[9,7,9,1152921504606846977]}`, true, []int64{9, 7, 9, 1152921504606846977}, nil, nil},
		{`{"labels":null}`, false, nil, []string{"null"}, nil},
		{`{"labels":"7"}`, false, nil, []string{"type"}, nil},
		{`{"labels":[7,"9",null,true,9.0,[7]]}`, false, nil, []string{"type", "null", "type", "type", "type"}, []string{"1", "2", "3", "4", "5"}},
	} {
		object, err := serializers.DecodeObject([]byte(test.text), serializers.Limits{})
		if err != nil {
			t.Fatal(test.text, err)
		}
		for _, mode := range []serializers.Mode{serializers.ModeFull, serializers.ModePartial} {
			result, err := spec.Bind(object, mode)
			if err != nil {
				t.Fatal(err)
			}
			var codes, indices []string
			for _, failure := range result.Errors().All() {
				if failure.Field() != "labels" {
					t.Fatal(failure)
				}
				codes = append(codes, string(failure.Code()))
				for _, param := range failure.Params() {
					if param.Key() == "index" {
						indices = append(indices, param.Value())
					}
				}
			}
			value, present := result.Values().Get("labels")
			keys, typed := value.AsIntegers()
			if present != test.present || present && (!typed || !slices.Equal(keys, test.keys) || keys == nil) || !slices.Equal(codes, test.codes) || !slices.Equal(indices, test.indices) {
				t.Fatalf("%s: present=%v keys=%v errors=%v indices=%v", test.text, present, keys, codes, indices)
			}
			if present {
				encoded, err := serializers.Encode(value, serializers.Limits{})
				if err != nil {
					t.Fatal(err)
				}
				var raw []int64
				if err := json.Unmarshal(encoded, &raw); err != nil || !slices.Equal(raw, test.keys) {
					t.Fatal(string(encoded), err)
				}
			}
		}
	}
}

func TestIntegerListDefaultsSnapshotsAndInvalidConfiguration(t *testing.T) {
	keys := []int64{7, 9}
	value := serializers.Integers(keys...)
	keys[0] = 99
	read, ok := value.AsIntegers()
	if !ok || !slices.Equal(read, []int64{7, 9}) {
		t.Fatal(read)
	}
	read[0] = 100
	field, err := serializers.IntegerListField("labels", serializers.WithDefault(value))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	empty, _ := serializers.NewObject()
	full, err := spec.Bind(empty, serializers.ModeFull)
	if err != nil || !full.Valid() {
		t.Fatal(err, full.Errors())
	}
	defaultValue, _ := full.Values().Get("labels")
	actual, _ := defaultValue.AsIntegers()
	if !slices.Equal(actual, []int64{7, 9}) {
		t.Fatal(actual)
	}
	partial, err := spec.Bind(empty, serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := partial.Values().Get("labels"); present {
		t.Fatal("PATCH acquired default replacement")
	}
	invalidList, _ := serializers.NewList(serializers.Integer(7), serializers.Null())
	for _, options := range [][]serializers.FieldOption{{serializers.WithDefault(invalidList)}, {serializers.WithMaxLength(2)}, {serializers.WithAllowEmpty()}, {serializers.WithTrimWhitespace(false)}, {serializers.WithChoices(serializers.Choice{Value: serializers.Integer(7), Label: "seven"})}, {nil}} {
		if _, err := serializers.IntegerListField("labels", options...); err == nil {
			t.Fatal("invalid list configuration accepted", options)
		}
	}
	if _, ok := invalidList.AsIntegers(); ok {
		t.Fatal("partial typed list returned")
	}
	required, _ := serializers.IntegerListField("labels")
	requiredSpec, _ := serializers.NewSpec([]serializers.Field{required})
	missing, _ := requiredSpec.Bind(empty, serializers.ModeFull)
	if missing.Valid() || missing.Errors().All()[0].Code() != "required" {
		t.Fatal(missing.Errors())
	}
	emptyArray, _ := serializers.NewObject(serializers.MemberOf("labels", serializers.Integers()))
	accepted, _ := requiredSpec.Bind(emptyArray, serializers.ModeFull)
	if !accepted.Valid() {
		t.Fatal("required presence forbids empty array", accepted.Errors())
	}
	readOnly, _ := serializers.IntegerListField("labels", serializers.WithReadOnly())
	readOnlySpec, _ := serializers.NewSpec([]serializers.Field{readOnly})
	denied, _ := readOnlySpec.Bind(emptyArray, serializers.ModePartial)
	if denied.Valid() || denied.Errors().All()[0].Code() != "read_only" {
		t.Fatal(denied.Errors())
	}
}

func TestModelCollectionEncoderRequiresExplicitLoadedDataAndOwnsMetadata(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "posts", Models: []schema.Model{{Name: "post", GoName: "Post", Fields: []schema.Field{schema.CharField("title", "Title", 40)}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("labels", "Labels", schema.Target("labels", "label"), schema.RelatedName("posts"))}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := definition.Models[0]
	spec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "title"}, serializers.ModelField{Name: "labels", Optional: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Fields()) != 2 || spec.Fields()[1].Kind() != serializers.FieldIntegerList || spec.Fields()[1].Required() || spec.Fields()[1].Nullable() {
		t.Fatal(spec.Fields())
	}
	type record struct {
		title  string
		labels []int64
		loaded bool
	}
	read := func(v record, f ir.Field) (query.Value, bool) { return query.String(v.title), f.Name == "title" }
	many := func(v record, f ir.ManyToManyField) ([]int64, bool) {
		if f.Name != "labels" || f.Target.ModelName != "label" {
			t.Error("aliased metadata", f)
		}
		f.Target.ModelName = "mutated"
		return v.labels, v.loaded
	}
	if _, err := serializers.NewModelEncoder(spec, metadata, read); err == nil {
		t.Fatal("missing collection reader accepted")
	}
	encoder, err := serializers.NewModelEncoder(spec, metadata, read, many)
	if err != nil {
		t.Fatal(err)
	}
	metadata.ManyToMany[0].Target.ModelName = "changed"
	keys := []int64{9, 7, 9}
	out, err := encoder.Encode(record{"Title", keys, true})
	if err != nil {
		t.Fatal(err)
	}
	keys[0] = 99
	object, _ := out.AsObject()
	labels, _ := object.Get("labels")
	got, _ := labels.AsIntegers()
	if !slices.Equal(got, []int64{9, 7, 9}) {
		t.Fatal("encoder altered collection order/multiplicity", got)
	}
	if _, err := encoder.Encode(record{"Title", nil, false}); err == nil {
		t.Fatal("unloaded relation published")
	}
	if out, err := encoder.Encode(record{"Title", nil, true}); err != nil {
		t.Fatal(err)
	} else {
		obj, _ := out.AsObject()
		value, _ := obj.Get("labels")
		keys, _ := value.AsIntegers()
		if keys == nil || len(keys) != 0 {
			t.Fatal(keys)
		}
	}
	metadata = definition.Models[0]
	metadata.ManyToMany[0].Name = "title"
	if _, err := serializers.FromModel(metadata, serializers.ModelField{Name: "title"}); err == nil {
		t.Fatal("scalar/collection collision accepted")
	}
}
