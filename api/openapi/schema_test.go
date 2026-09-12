package openapi_test

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func TestSchemaPrimitivesComposeWithoutLosingNullEnumOrIntegerRange(t *testing.T) {
	integer := schemaTestObject(t, openapi.Integer())
	if schemaTestString(t, integer, "type") != "integer" || schemaTestString(t, integer, "format") != "int64" ||
		schemaTestInteger(t, integer, "minimum") != math.MinInt64 || schemaTestInteger(t, integer, "maximum") != math.MaxInt64 {
		t.Fatal("integer schema does not describe the serializer's int64 range")
	}
	if schemaTestString(t, schemaTestObject(t, openapi.String()), "type") != "string" ||
		schemaTestString(t, schemaTestObject(t, openapi.Boolean()), "type") != "boolean" {
		t.Fatal("scalar types changed")
	}
	status, err := openapi.EnumStrings("open", "closed")
	if err != nil {
		t.Fatal(err)
	}
	nullableStatus, err := openapi.Nullable(status)
	if err != nil {
		t.Fatal(err)
	}
	branches := schemaTestList(t, schemaTestObject(t, nullableStatus), "anyOf")
	if len(branches) != 2 {
		t.Fatalf("nullable alternatives = %d", len(branches))
	}
	if !bytes.Equal(schemaTestEncodeValue(t, branches[0]), schemaTestEncode(t, status)) ||
		schemaTestString(t, schemaTestValueObject(t, branches[1]), "type") != "null" {
		t.Fatal("nullable enum did not preserve the enum and independently admit null")
	}
	items, err := openapi.Array(nullableStatus)
	if err != nil {
		t.Fatal(err)
	}
	page, err := openapi.Object(
		openapi.Property{Name: "results", Schema: items, Required: true},
		openapi.Property{Name: "total", Schema: openapi.Integer(), Required: true},
		openapi.Property{Name: "optional", Schema: openapi.Boolean()},
	)
	if err != nil {
		t.Fatal(err)
	}
	pageObject := schemaTestObject(t, page)
	if schemaTestBoolean(t, pageObject, "additionalProperties") {
		t.Fatal("object permits undeclared fields")
	}
	if got := schemaTestRequired(t, page); !reflect.DeepEqual(got, []string{"results", "total"}) {
		t.Fatalf("required = %v", got)
	}
	properties := schemaTestProperties(t, page)
	if got := schemaTestMemberNames(properties); !reflect.DeepEqual(got, []string{"results", "total", "optional"}) {
		t.Fatalf("property order = %v", got)
	}
	arrayObject := schemaTestProperty(t, page, "results")
	if schemaTestString(t, arrayObject, "type") != "array" {
		t.Fatal("array type lost")
	}
	itemValue, _ := arrayObject.Get("items")
	if !bytes.Equal(schemaTestEncodeValue(t, itemValue), schemaTestEncode(t, nullableStatus)) {
		t.Fatal("array item schema changed")
	}
}

func TestSchemaConstructionRejectsInvalidValuesBeforePublication(t *testing.T) {
	tests := []struct {
		name  string
		build func() (openapi.Schema, error)
	}{
		{"nullable zero", func() (openapi.Schema, error) { return openapi.Nullable(openapi.Schema{}) }},
		{"array zero", func() (openapi.Schema, error) { return openapi.Array(openapi.Schema{}) }},
		{"property zero", func() (openapi.Schema, error) { return openapi.Object(openapi.Property{Name: "value"}) }},
		{"empty property", func() (openapi.Schema, error) {
			return openapi.Object(openapi.Property{Schema: openapi.String()})
		}},
		{"invalid property UTF-8", func() (openapi.Schema, error) {
			return openapi.Object(openapi.Property{Name: "bad\xff", Schema: openapi.String()})
		}},
		{"property NUL", func() (openapi.Schema, error) {
			return openapi.Object(openapi.Property{Name: "bad\x00", Schema: openapi.String()})
		}},
		{"duplicate property", func() (openapi.Schema, error) {
			return openapi.Object(openapi.Property{Name: "value", Schema: openapi.String()}, openapi.Property{Name: "value", Schema: openapi.Integer()})
		}},
		{"empty enum", func() (openapi.Schema, error) { return openapi.EnumStrings() }},
		{"duplicate enum", func() (openapi.Schema, error) { return openapi.EnumStrings("open", "open") }},
		{"invalid enum UTF-8", func() (openapi.Schema, error) { return openapi.EnumStrings("bad\xff") }},
		{"enum NUL", func() (openapi.Schema, error) { return openapi.EnumStrings("bad\x00") }},
		{"request zero spec", func() (openapi.Schema, error) {
			return openapi.RequestSchema(serializers.Spec{}, serializers.ModeFull)
		}},
		{"response zero spec", func() (openapi.Schema, error) { return openapi.ModelResponseSchema(serializers.Spec{}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema, err := test.build()
			if !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) || schema.Value().Kind() != 0 {
				t.Fatalf("schema = %#v, error = %v", schema, err)
			}
		})
	}
	empty, err := openapi.Object()
	if err != nil || schemaTestProperties(t, empty).Len() != 0 || len(schemaTestRequired(t, empty)) != 0 {
		t.Fatalf("empty closed object = %#v, %v", empty, err)
	}
	if schemaTestBoolean(t, schemaTestObject(t, empty), "additionalProperties") {
		t.Fatal("empty object accepts arbitrary properties")
	}
	if _, err := openapi.EnumStrings(""); err != nil {
		t.Fatalf("empty string is a valid enum choice: %v", err)
	}
	if (openapi.Schema{}).Value().Kind() != 0 {
		t.Fatal("zero schema has a usable value")
	}
}

func TestRequestSchemaMatchesFullPartialPresenceDefaultAndReadOnlySemantics(t *testing.T) {
	id := schemaTestField(t, serializers.FieldInteger, "id", serializers.WithReadOnly())
	title := schemaTestField(t, serializers.FieldString, "title")
	published := schemaTestField(t, serializers.FieldBoolean, "published", serializers.WithDefault(serializers.Boolean(false)))
	// Bind applies the default before the required check even in this valid
	// option order. A schema that uses Required() alone would reject valid input.
	count := schemaTestField(t, serializers.FieldInteger, "count", serializers.WithDefault(serializers.Integer(0)), serializers.WithRequired(true))
	note := schemaTestField(t, serializers.FieldString, "note", serializers.WithNullable(), serializers.WithDefault(serializers.Null()), serializers.WithAllowEmpty())
	spec := schemaTestSpec(t, id, title, published, count, note)
	full, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := openapi.RequestSchema(spec, serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	if got := schemaTestRequired(t, full); !reflect.DeepEqual(got, []string{"title"}) {
		t.Fatalf("full required = %v", got)
	}
	if got := schemaTestRequired(t, partial); len(got) != 0 {
		t.Fatalf("partial requires omitted fields: %v", got)
	}
	for _, schema := range []openapi.Schema{full, partial} {
		if _, present := schemaTestProperties(t, schema).Get("id"); present || schemaTestBoolean(t, schemaTestObject(t, schema), "additionalProperties") {
			t.Fatal("request schema admits read-only or unknown fields")
		}
	}
	for _, test := range []struct {
		name string
		want serializers.Value
	}{{"published", serializers.Boolean(false)}, {"count", serializers.Integer(0)}, {"note", serializers.Null()}} {
		actual, present := schemaTestProperty(t, full, test.name).Get("default")
		if !present || !bytes.Equal(schemaTestEncodeValue(t, actual), schemaTestEncodeValue(t, test.want)) {
			t.Fatalf("full default %q is missing or changed", test.name)
		}
		if _, present := schemaTestProperty(t, partial, test.name).Get("default"); present {
			t.Fatalf("partial default for %q could replace an omitted field", test.name)
		}
	}
	fullValues := schemaTestBind(t, spec, `{"title":"Go"}`, serializers.ModeFull)
	if !fullValues.Valid() || len(fullValues.Values().All()) != 4 {
		t.Fatalf("runtime full values = %v", fullValues.Values().All())
	}
	partialValues := schemaTestBind(t, spec, `{}`, serializers.ModePartial)
	if !partialValues.Valid() || len(partialValues.Values().All()) != 0 {
		t.Fatal("runtime partial applied absent fields")
	}
	for _, input := range []string{`{"title":"Go","id":1}`, `{"title":"Go","unknown":1}`, `{"title":null}`} {
		if schemaTestBind(t, spec, input, serializers.ModeFull).Valid() {
			t.Fatalf("runtime accepted %s", input)
		}
	}
	for _, input := range []string{`{"note":null}`, `{"note":""}`} {
		if !schemaTestBind(t, spec, input, serializers.ModePartial).Valid() {
			t.Fatalf("runtime rejected %s", input)
		}
	}
	if _, present := schemaTestProperty(t, full, "title").Get("anyOf"); present {
		t.Fatal("nonnullable title became nullable")
	}
	if len(schemaTestList(t, schemaTestProperty(t, full, "note"), "anyOf")) != 2 {
		t.Fatal("nullable note lost its null alternative")
	}
	if _, err := openapi.RequestSchema(spec, serializers.Mode(99)); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig, Field: "schema.mode"}) {
		t.Fatalf("invalid mode error = %v", err)
	}
	readOnly, err := openapi.RequestSchema(schemaTestSpec(t, id), serializers.ModeFull)
	if err != nil || schemaTestProperties(t, readOnly).Len() != 0 {
		t.Fatalf("read-only spec did not yield an empty writable object: %v", err)
	}
}

func TestRequestSchemaDistinguishesRawAndNormalizedStringConstraints(t *testing.T) {
	trimmed := schemaTestField(t, serializers.FieldString, "trimmed", serializers.WithMaxLength(2))
	raw := schemaTestField(t, serializers.FieldString, "raw", serializers.WithTrimWhitespace(false), serializers.WithMaxLength(2))
	spec := schemaTestSpec(t, trimmed, raw)
	for _, mode := range []serializers.Mode{serializers.ModeFull, serializers.ModePartial} {
		schema, err := openapi.RequestSchema(spec, mode)
		if err != nil {
			t.Fatal(err)
		}
		trimmedProperty := schemaTestProperty(t, schema, "trimmed")
		for _, name := range []string{"minLength", "maxLength"} {
			if _, present := trimmedProperty.Get(name); present {
				t.Fatalf("raw %s would misdescribe validation after TrimSpace", name)
			}
		}
		policyValue, found := trimmedProperty.Get("x-godj-normalization")
		if !found {
			t.Fatal("normalization policy is undocumented")
		}
		policy := schemaTestValueObject(t, policyValue)
		if !schemaTestBoolean(t, policy, "trimWhitespace") || schemaTestBoolean(t, policy, "allowEmptyAfterTrim") ||
			schemaTestInteger(t, policy, "maxLengthAfterTrim") != 2 {
			t.Fatal("post-normalization constraints changed")
		}
		rawProperty := schemaTestProperty(t, schema, "raw")
		if schemaTestInteger(t, rawProperty, "minLength") != 1 || schemaTestInteger(t, rawProperty, "maxLength") != 2 {
			t.Fatal("verbatim string constraints are not standard raw length bounds")
		}
		if _, present := rawProperty.Get("x-godj-normalization"); present {
			t.Fatal("verbatim string was marked as normalized")
		}
	}
	for _, input := range []string{`{"trimmed":"  Go  ","raw":"ok"}`, `{"trimmed":"  한글  ","raw":"한글"}`} {
		if !schemaTestBind(t, spec, input, serializers.ModeFull).Valid() {
			t.Fatalf("runtime must permit input whose raw length exceeds the cleaned limit: %s", input)
		}
	}
	for _, input := range []string{`{"trimmed":"   ","raw":"ok"}`, `{"trimmed":"Go!","raw":"ok"}`, `{"trimmed":"Go","raw":" ok "}`} {
		if schemaTestBind(t, spec, input, serializers.ModeFull).Valid() {
			t.Fatalf("runtime accepted a string-policy violation: %s", input)
		}
	}
}

func TestModelResponseSchemaUsesCompleteExposureWithoutInputPolicies(t *testing.T) {
	metadata := ir.Model{
		Name: "ticket",
		Fields: []ir.Field{
			{Name: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", Kind: ir.FieldChar, MaxLength: 2},
			{Name: "note", Kind: ir.FieldChar, MaxLength: 20, Nullable: true},
			{Name: "published", Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: true}},
			{Name: "secret", Kind: ir.FieldChar, MaxLength: 20},
		},
	}
	spec, err := serializers.FromModel(metadata,
		serializers.ModelField{Name: "id"},
		serializers.ModelField{Name: "title"},
		serializers.ModelField{Name: "note", Optional: true, AllowEmpty: true},
		serializers.ModelField{Name: "published"},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := schemaTestRequired(t, response); !reflect.DeepEqual(got, []string{"id", "title", "note", "published"}) {
		t.Fatalf("response presence = %v", got)
	}
	if _, present := schemaTestProperties(t, response).Get("secret"); present {
		t.Fatal("unselected model field was exposed")
	}
	if !schemaTestBoolean(t, schemaTestProperty(t, response, "id"), "readOnly") {
		t.Fatal("auto key lost its read-only annotation")
	}
	for _, member := range schemaTestProperties(t, response).Members() {
		property := schemaTestValueObject(t, member.Value())
		for _, name := range []string{"default", "minLength", "x-godj-normalization"} {
			if _, present := property.Get(name); present {
				t.Fatalf("response field %s inherited input policy %s", member.Name(), name)
			}
		}
	}
	if schemaTestInteger(t, schemaTestProperty(t, response, "title"), "maxLength") != 2 ||
		schemaTestInteger(t, schemaTestProperty(t, response, "note"), "maxLength") != 20 {
		t.Fatal("response string bounds do not match the encoder's character limits")
	}
	noteBranches := schemaTestList(t, schemaTestProperty(t, response, "note"), "anyOf")
	if len(noteBranches) != 2 || schemaTestString(t, schemaTestValueObject(t, noteBranches[0]), "type") != "string" ||
		schemaTestString(t, schemaTestValueObject(t, noteBranches[1]), "type") != "null" {
		t.Fatal("nullable response length bound lost its string/null alternatives")
	}
	type model struct {
		title string
		note  *string
	}
	encoder, err := serializers.NewModelEncoder(spec, metadata, func(value model, field ir.Field) (query.Value, bool) {
		switch field.Name {
		case "id":
			return query.Integer(1), true
		case "title":
			return query.String(value.title), true
		case "note":
			if value.note == nil {
				return query.Null(), true
			}
			return query.String(*value.note), true
		case "published":
			return query.Boolean(false), true
		default:
			return query.Value{}, false
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	unicodeNote := strings.Repeat("한", 20)
	for _, input := range []model{{title: ""}, {title: "  "}, {title: "한글", note: &unicodeNote}} {
		value, err := encoder.Encode(input)
		if err != nil {
			t.Fatalf("output must allow blank text and count Unicode characters without cleaning: %v", err)
		}
		object := schemaTestValueObject(t, value)
		if schemaTestString(t, object, "title") != input.title || schemaTestBoolean(t, object, "published") {
			t.Fatal("encoder applied input normalization or its default")
		}
		note, present := object.Get("note")
		if !present || !reflect.DeepEqual(schemaTestMemberNames(object), schemaTestRequired(t, response)) {
			t.Fatal("response schema does not match encoder field presence")
		}
		if input.note == nil {
			if !note.IsNull() {
				t.Fatal("nullable output does not retain null")
			}
		} else if actual, ok := note.AsString(); !ok || actual != *input.note {
			t.Fatal("nullable output string changed")
		}
	}
	oversizedNote := unicodeNote + "글"
	for _, invalid := range []struct {
		input model
		field string
	}{
		{input: model{title: "한글셋"}, field: "title"},
		{input: model{title: " Go "}, field: "title"},
		{input: model{title: "ok", note: &oversizedNote}, field: "note"},
	} {
		if _, err := encoder.Encode(invalid.input); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue, Field: invalid.field}) {
			t.Fatalf("encoder must enforce the documented untrimmed character bound on %s: %v", invalid.field, err)
		}
	}
}

func TestSchemaSnapshotsAndReturnedContainersCannotMutatePublishedSchema(t *testing.T) {
	choices := []string{"open", "closed"}
	status, err := openapi.EnumStrings(choices...)
	if err != nil {
		t.Fatal(err)
	}
	nullableStatus, err := openapi.Nullable(status)
	if err != nil {
		t.Fatal(err)
	}
	properties := []openapi.Property{{Name: "status", Schema: nullableStatus, Required: true}}
	published, err := openapi.Object(properties...)
	if err != nil {
		t.Fatal(err)
	}
	before := schemaTestEncode(t, published)
	choices[0] = "changed"
	properties[0] = openapi.Property{}
	rootMembers := schemaTestObject(t, published).Members()
	rootMembers[0] = serializers.Member{}
	propertyMembers := schemaTestProperties(t, published).Members()
	propertyMembers[0] = serializers.Member{}
	required := schemaTestList(t, schemaTestObject(t, published), "required")
	required[0] = serializers.String("changed")
	branches := schemaTestList(t, schemaTestProperty(t, published, "status"), "anyOf")
	enumChoices := schemaTestList(t, schemaTestValueObject(t, branches[0]), "enum")
	enumChoices[0] = serializers.String("changed")
	branches[0] = serializers.Value{}
	for range 2 {
		if after := schemaTestEncode(t, published); !bytes.Equal(before, after) {
			t.Fatalf("schema changed through a caller-owned container:\n%s\n%s", before, after)
		}
	}
	field := schemaTestField(t, serializers.FieldString, "title", serializers.WithDefault(serializers.String("  seed  ")))
	inputFields := []serializers.Field{field}
	spec := schemaTestSpec(t, inputFields...)
	projected, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	inputFields[0] = serializers.Field{}
	returnedFields := spec.Fields()
	returnedFields[0] = serializers.Field{}
	again, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil || !bytes.Equal(schemaTestEncode(t, projected), schemaTestEncode(t, again)) {
		t.Fatalf("projection retained mutable source metadata: %v", err)
	}
	if schemaTestString(t, schemaTestProperty(t, projected, "title"), "default") != "seed" {
		t.Fatal("default was not the already normalized runtime value")
	}
}

func schemaTestField(t *testing.T, kind serializers.FieldKind, name string, options ...serializers.FieldOption) serializers.Field {
	t.Helper()
	var field serializers.Field
	var err error
	switch kind {
	case serializers.FieldString:
		field, err = serializers.StringField(name, options...)
	case serializers.FieldBoolean:
		field, err = serializers.BooleanField(name, options...)
	case serializers.FieldInteger:
		field, err = serializers.IntegerField(name, options...)
	default:
		t.Fatalf("unsupported test field kind %d", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	return field
}

func schemaTestSpec(t *testing.T, fields ...serializers.Field) serializers.Spec {
	t.Helper()
	spec, err := serializers.NewSpec(fields)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func schemaTestBind(t *testing.T, spec serializers.Spec, document string, mode serializers.Mode) serializers.Result {
	t.Helper()
	object, err := serializers.DecodeObject([]byte(document), serializers.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	result, err := spec.Bind(object, mode)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func schemaTestObject(t *testing.T, schema openapi.Schema) serializers.Object {
	t.Helper()
	return schemaTestValueObject(t, schema.Value())
}

func schemaTestValueObject(t *testing.T, value serializers.Value) serializers.Object {
	t.Helper()
	object, ok := value.AsObject()
	if !ok {
		t.Fatal("expected an object")
	}
	return object
}

func schemaTestProperties(t *testing.T, schema openapi.Schema) serializers.Object {
	t.Helper()
	properties, present := schemaTestObject(t, schema).Get("properties")
	if !present {
		t.Fatal("properties are absent")
	}
	return schemaTestValueObject(t, properties)
}

func schemaTestProperty(t *testing.T, schema openapi.Schema, name string) serializers.Object {
	t.Helper()
	property, present := schemaTestProperties(t, schema).Get(name)
	if !present {
		t.Fatalf("property %q is absent", name)
	}
	return schemaTestValueObject(t, property)
}

func schemaTestRequired(t *testing.T, schema openapi.Schema) []string {
	t.Helper()
	object := schemaTestObject(t, schema)
	if _, present := object.Get("required"); !present {
		return nil
	}
	values := schemaTestList(t, object, "required")
	names := make([]string, len(values))
	for index, value := range values {
		name, ok := value.AsString()
		if !ok {
			t.Fatal("required contains a non-string name")
		}
		names[index] = name
	}
	return names
}

func schemaTestList(t *testing.T, object serializers.Object, name string) []serializers.Value {
	t.Helper()
	value, present := object.Get(name)
	list, ok := value.AsList()
	if !present || !ok {
		t.Fatalf("%q is not a list", name)
	}
	return list
}

func schemaTestString(t *testing.T, object serializers.Object, name string) string {
	t.Helper()
	value, present := object.Get(name)
	text, ok := value.AsString()
	if !present || !ok {
		t.Fatalf("%q is not a string", name)
	}
	return text
}

func schemaTestInteger(t *testing.T, object serializers.Object, name string) int64 {
	t.Helper()
	value, present := object.Get(name)
	number, ok := value.AsInteger()
	if !present || !ok {
		t.Fatalf("%q is not an integer", name)
	}
	return number
}

func schemaTestBoolean(t *testing.T, object serializers.Object, name string) bool {
	t.Helper()
	value, present := object.Get(name)
	boolean, ok := value.AsBoolean()
	if !present || !ok {
		t.Fatalf("%q is not a boolean", name)
	}
	return boolean
}

func schemaTestMemberNames(object serializers.Object) []string {
	names := make([]string, 0, object.Len())
	for _, member := range object.Members() {
		names = append(names, member.Name())
	}
	return names
}

func schemaTestEncode(t *testing.T, schema openapi.Schema) []byte {
	t.Helper()
	return schemaTestEncodeValue(t, schema.Value())
}

func schemaTestEncodeValue(t *testing.T, value serializers.Value) []byte {
	t.Helper()
	encoded, err := serializers.Encode(value, serializers.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
