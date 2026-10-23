package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/serializers"
)

func TestRefPreservesBoundedComponentIdentity(t *testing.T) {
	for _, name := range []string{"Ticket", "0.api-1_response", strings.Repeat("a", maxSchemaNameBytes)} {
		schema, err := Ref(name)
		if err != nil {
			t.Fatalf("Ref(%q): %v", name, err)
		}
		got, err := rawSchema(schema)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"$ref":"#/components/schemas/` + name + `"}`
		if string(got) != want {
			t.Fatalf("Ref(%q) = %s, want %s", name, got, want)
		}
	}
	for _, name := range []string{"", " ", "two words", "a/b", "a~b", "a#b", "a%20b", "한글", "bad\x00", "bad\xff", "https://example.com/schema", "#/components/schemas/Ticket", strings.Repeat("a", maxSchemaNameBytes+1)} {
		schema, err := Ref(name)
		if !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) || schemaValid(schema) {
			t.Fatalf("invalid Ref(%q) = %#v, %v", name, schema, err)
		}
	}
}

func TestSchemaCatalogPreservesReferencesIdentityAndSnapshots(t *testing.T) {
	ticket := referenceTestObject(t, Property{Name: "id", Schema: Integer(), Required: true})
	ticketList := referenceTestArray(t, referenceTestRef(t, "Ticket"))
	declarations := []NamedSchema{
		{Name: "TicketList", Schema: ticketList},
		{Name: "Alias", Schema: referenceTestRef(t, "TicketList")},
		{Name: "Ticket", Schema: ticket},
		{Name: "DistinctTicket", Schema: ticket},
	}
	catalog, err := prepareSchemaCatalog(declarations)
	if err != nil {
		t.Fatal(err)
	}
	reversed := append([]NamedSchema(nil), declarations...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	other, err := prepareSchemaCatalog(reversed)
	if err != nil {
		t.Fatal(err)
	}
	components := referenceTestComponents(t, catalog)
	otherComponents := referenceTestComponents(t, other)
	firstJSON, err := json.Marshal(components)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(otherComponents)
	if err != nil || !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("declaration order changed component output: %s / %s (%v)", firstJSON, secondJSON, err)
	}
	if len(components) != 4 || !bytes.Equal(components["Ticket"], components["DistinctTicket"]) {
		t.Fatal("explicit identities were deduplicated or changed")
	}
	if string(components["Alias"]) != `{"$ref":"#/components/schemas/TicketList"}` ||
		!bytes.Contains(components["TicketList"], []byte(`"$ref":"#/components/schemas/Ticket"`)) {
		t.Fatal("component references were expanded or changed")
	}
	resolved, err := catalog.resolveRoot(referenceTestRef(t, "Alias"))
	if err != nil {
		t.Fatal(err)
	}
	resolvedJSON, err := rawSchema(resolved)
	if err != nil || !bytes.Equal(resolvedJSON, components["TicketList"]) {
		t.Fatalf("root aliases did not resolve to the unchanged array schema: %s (%v)", resolvedJSON, err)
	}
	// Both declarations and published encoding slices belong to their callers.
	declarations[0] = NamedSchema{Name: "Replaced", Schema: Boolean()}
	components["Ticket"][0] = '!'
	delete(components, "Alias")
	components["Added"] = json.RawMessage(`{}`)
	thirdJSON, err := json.Marshal(referenceTestComponents(t, catalog))
	if err != nil || !bytes.Equal(firstJSON, thirdJSON) {
		t.Fatalf("caller mutation changed the catalog: %s (%v)", thirdJSON, err)
	}
}

func TestSchemaCatalogVisitsOnlySchemaPositions(t *testing.T) {
	nullable, err := Nullable(referenceTestRef(t, "Text"))
	if err != nil {
		t.Fatal(err)
	}
	schema := referenceTestObject(t,
		Property{Name: "$ref", Schema: String(), Required: true},
		Property{Name: "properties", Schema: referenceTestArray(t, referenceTestRef(t, "Text"))},
		Property{Name: "anyOf", Schema: nullable},
		Property{Name: "items", Schema: Boolean()},
	)
	data, err := serializers.NewObject(
		serializers.MemberOf("$ref", serializers.String("https://example.com/not-a-schema")),
		serializers.MemberOf("items", serializers.Boolean(false)),
		serializers.MemberOf("anyOf", serializers.String("ordinary data")),
	)
	if err != nil {
		t.Fatal(err)
	}
	schema, err = schemaAnnotate(schema, serializers.MemberOf("default", data.Value()), serializers.MemberOf("x-example", data.Value()))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := prepareSchemaCatalog([]NamedSchema{{Name: "Text", Schema: String()}, {Name: "Payload", Schema: schema}})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.validate(schema); err != nil {
		t.Fatalf("ordinary member names or annotation data were treated as references: %v", err)
	}
	components := referenceTestComponents(t, catalog)
	if !bytes.Contains(components["Payload"], []byte(`"$ref":{"type":"string"}`)) ||
		!bytes.Contains(components["Payload"], []byte(`"$ref":"https://example.com/not-a-schema"`)) {
		t.Fatal("ordinary property or annotation contents changed")
	}
}

func TestSchemaCatalogRejectsUnresolvedAndRecursiveGraphs(t *testing.T) {
	missing := referenceTestRef(t, "Missing")
	nullableMissing, err := Nullable(missing)
	if err != nil {
		t.Fatal(err)
	}
	nullableA, err := Nullable(referenceTestRef(t, "A"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		declarations []NamedSchema
	}{
		{"invalid component name", []NamedSchema{{Name: "bad/name", Schema: String()}}},
		{"duplicate identity", []NamedSchema{{Name: "A", Schema: String()}, {Name: "A", Schema: String()}}},
		{"zero schema", []NamedSchema{{Name: "A"}}},
		{"unresolved root", []NamedSchema{{Name: "A", Schema: missing}}},
		{"unresolved property", []NamedSchema{{Name: "A", Schema: referenceTestObject(t, Property{Name: "value", Schema: missing})}}},
		{"unresolved items", []NamedSchema{{Name: "A", Schema: referenceTestArray(t, missing)}}},
		{"unresolved alternative", []NamedSchema{{Name: "A", Schema: nullableMissing}}},
		{"self alias", []NamedSchema{{Name: "A", Schema: referenceTestRef(t, "A")}}},
		{"alias cycle", []NamedSchema{{Name: "A", Schema: referenceTestRef(t, "B")}, {Name: "B", Schema: referenceTestRef(t, "A")}}},
		{"property cycle", []NamedSchema{{Name: "A", Schema: referenceTestObject(t, Property{Name: "parent", Schema: referenceTestRef(t, "A")})}}},
		{"array and nullable cycle", []NamedSchema{{Name: "A", Schema: referenceTestArray(t, referenceTestRef(t, "B"))}, {Name: "B", Schema: nullableA}}},
		{"unused cycle", []NamedSchema{{Name: "Root", Schema: String()}, {Name: "Unused", Schema: referenceTestRef(t, "Unused")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := prepareSchemaCatalog(test.declarations)
			if !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) || len(catalog.schemas) != 0 {
				t.Fatalf("invalid catalog was published: %#v, %v", catalog, err)
			}
		})
	}
	catalog, err := prepareSchemaCatalog(nil)
	if err != nil || catalog.validate(String()) != nil {
		t.Fatalf("empty catalog rejected an inline schema: %v", err)
	}
	for _, schema := range []Schema{{}, missing} {
		if err := catalog.validate(schema); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
			t.Fatalf("operation schema accepted without a valid target: %v", err)
		}
		if resolved, err := catalog.resolveRoot(schema); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) || schemaValid(resolved) {
			t.Fatalf("invalid root resolution = %#v, %v", resolved, err)
		}
	}
}

func TestSchemaCatalogRejectsUnsupportedReferenceSpellingsAndMalformedPositions(t *testing.T) {
	list, err := serializers.NewList()
	if err != nil {
		t.Fatal(err)
	}
	tests := [][]serializers.Member{
		{serializers.MemberOf("$ref", serializers.String("https://example.com/schema"))},
		{serializers.MemberOf("$ref", serializers.String("#/components/parameters/A"))},
		{serializers.MemberOf("$ref", serializers.String(schemaRefPrefix+"A/properties/id"))},
		{serializers.MemberOf("$ref", serializers.String(schemaRefPrefix+"A%2FB"))},
		{serializers.MemberOf("$ref", serializers.Integer(1))},
		{serializers.MemberOf("$ref", serializers.String(schemaRefPrefix+"A")), serializers.MemberOf("type", serializers.String("string"))},
		{serializers.MemberOf("properties", list)},
		{serializers.MemberOf("items", serializers.Boolean(true))},
		{serializers.MemberOf("anyOf", serializers.Boolean(true))},
		{serializers.MemberOf("anyOf", list)},
	}
	for index, members := range tests {
		schema, err := schemaObject(members...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepareSchemaCatalog([]NamedSchema{{Name: "A", Schema: String()}, {Name: "B", Schema: schema}}); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
			t.Fatalf("malformed schema %d accepted: %v", index, err)
		}
	}
}

func TestSchemaCatalogBoundsComponentsAndCombinedReferenceDepth(t *testing.T) {
	declarations := make([]NamedSchema, maxNamedSchemas+1)
	for index := range declarations {
		declarations[index] = NamedSchema{Name: fmt.Sprintf("Schema%03d", index), Schema: String()}
	}
	if _, err := prepareSchemaCatalog(declarations[:maxNamedSchemas]); err != nil {
		t.Fatalf("maximum component count rejected: %v", err)
	}
	if _, err := prepareSchemaCatalog(declarations); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
		t.Fatalf("component count overflow = %v", err)
	}
	chain := func(length int) []NamedSchema {
		result := make([]NamedSchema, length)
		for index := range result {
			schema := String()
			if index+1 < length {
				schema = referenceTestRef(t, fmt.Sprintf("Schema%03d", index+1))
			}
			result[index] = NamedSchema{Name: fmt.Sprintf("Schema%03d", index), Schema: schema}
		}
		return result
	}
	catalog, err := prepareSchemaCatalog(chain(maxSchemaDepth - 1))
	if err != nil {
		t.Fatal(err)
	}
	root := referenceTestRef(t, "Schema000")
	if err := catalog.validate(root); err != nil {
		t.Fatalf("64-node operation reference path rejected: %v", err)
	}
	if err := catalog.validate(referenceTestArray(t, root)); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig, Field: "schema.depth"}) {
		t.Fatalf("combined inline and reference depth overflow = %v", err)
	}
	if _, err := prepareSchemaCatalog(chain(maxSchemaDepth)); err != nil {
		t.Fatalf("64-node component path rejected: %v", err)
	}
	if _, err := prepareSchemaCatalog(chain(maxSchemaDepth + 1)); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig, Field: "schema.depth"}) {
		t.Fatalf("reference depth overflow = %v", err)
	}
}

func TestSchemaCatalogBoundsInlineTraversalAndComponentEncoding(t *testing.T) {
	catalog, err := prepareSchemaCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	deep := String()
	for range maxSchemaDepth - 1 {
		deep = referenceTestArray(t, deep)
	}
	if err := catalog.validate(deep); err != nil {
		t.Fatalf("maximum inline graph depth rejected: %v", err)
	}
	if err := catalog.validate(referenceTestArray(t, deep)); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig, Field: "schema.depth"}) {
		t.Fatalf("inline graph depth overflow = %v", err)
	}
	wide := make([]Property, maxSchemaNodes)
	for index := range wide {
		wide[index] = Property{Name: fmt.Sprintf("field%d", index), Schema: String()}
	}
	if err := catalog.validate(referenceTestObject(t, wide[:maxSchemaNodes-1]...)); err != nil {
		t.Fatalf("maximum inline node count rejected: %v", err)
	}
	if err := catalog.validate(referenceTestObject(t, wide...)); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig, Field: "schema.nodes"}) {
		t.Fatalf("inline node overflow = %v", err)
	}
	// Graph budgets do not waive the pre-existing JSON encoding depth budget.
	catalog, err = prepareSchemaCatalog([]NamedSchema{{Name: "Deep", Schema: deep}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.components(); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
		t.Fatalf("JSON serialization budget was bypassed: %v", err)
	}
	large, err := EnumStrings(strings.Repeat("a", 8192))
	if err != nil {
		t.Fatal(err)
	}
	declarations := make([]NamedSchema, maxNamedSchemas)
	for index := range declarations {
		declarations[index] = NamedSchema{Name: fmt.Sprintf("Large%03d", index), Schema: large}
	}
	catalog, err = prepareSchemaCatalog(declarations)
	if err != nil {
		t.Fatal(err)
	}
	if components, err := catalog.components(); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) || components != nil {
		t.Fatalf("oversized component encodings were published: count %d, %v", len(components), err)
	}
}

func referenceTestRef(t *testing.T, name string) Schema {
	t.Helper()
	schema, err := Ref(name)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func referenceTestObject(t *testing.T, properties ...Property) Schema {
	t.Helper()
	schema, err := Object(properties...)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func referenceTestArray(t *testing.T, items Schema) Schema {
	t.Helper()
	schema, err := Array(items)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func referenceTestComponents(t *testing.T, catalog schemaCatalog) map[string]json.RawMessage {
	t.Helper()
	components, err := catalog.components()
	if err != nil {
		t.Fatal(err)
	}
	return components
}
