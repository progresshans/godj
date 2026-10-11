package schema_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func namedUniqueDeclaration() schema.Definition {
	return schema.Definition{AppLabel: "labels", Models: []schema.Model{{
		Name: "label", GoName: "Label",
		Fields: []schema.Field{
			schema.ForeignKey("category", "Category", schema.Target("labels", "category"), schema.RelatedName("labels"), schema.Protect),
			schema.CharField("name", "Name", 64, schema.Column("label_name")),
		},
		UniqueConstraints: []schema.UniqueConstraint{
			{Name: "scope_name", Fields: []string{"category", "name"}},
			{Name: "named_single", Fields: []string{"name"}},
		},
	}}}
}

func TestNamedUniqueConstraintOwnsLogicalMembersAndCanonicalIdentity(t *testing.T) {
	declaration := namedUniqueDeclaration()
	built, err := schema.Build(declaration)
	if err != nil {
		t.Fatal(err)
	}
	model := built.Models[0]
	if model.UniqueConstraints[0].Name != "named_single" || model.UniqueConstraints[1].Name != "scope_name" {
		t.Fatal("constraint declarations are not normalized by logical name")
	}
	if declaration.Models[0].UniqueConstraints[0].Name != "scope_name" {
		t.Fatal("normalization reordered the caller's constraints")
	}
	if !reflect.DeepEqual(model.UniqueConstraints[1].Fields, []string{"category", "name"}) || model.Fields[1].Column != "category_id" || model.Fields[2].Column != "label_name" {
		t.Fatal("constraint identity lost logical field names or their order")
	}
	if model.Fields[1].Unique || model.Fields[2].Unique || model.Fields[1].Relation.Cardinality != ir.RelationManyToOne {
		t.Fatal("tuple uniqueness changed field uniqueness or relation cardinality")
	}
	baselineHash, err := ir.Hash(built)
	if err != nil {
		t.Fatal(err)
	}
	reordered := namedUniqueDeclaration()
	reordered.Models[0].UniqueConstraints[0], reordered.Models[0].UniqueConstraints[1] = reordered.Models[0].UniqueConstraints[1], reordered.Models[0].UniqueConstraints[0]
	other, err := schema.Build(reordered)
	if err != nil {
		t.Fatal(err)
	}
	otherHash, err := ir.Hash(other)
	if err != nil || baselineHash != otherHash || !model.Equal(other.Models[0]) {
		t.Fatal("constraint list reorder changed canonical identity", err)
	}
	for _, change := range []func(*ir.Model){
		func(m *ir.Model) { m.UniqueConstraints[1].Name = "renamed" },
		func(m *ir.Model) {
			m.UniqueConstraints[1].Fields[0], m.UniqueConstraints[1].Fields[1] = m.UniqueConstraints[1].Fields[1], m.UniqueConstraints[1].Fields[0]
		},
		func(m *ir.Model) { m.UniqueConstraints[1].Fields[0] = "id" },
		func(m *ir.Model) { m.UniqueConstraints = m.UniqueConstraints[:1] },
	} {
		changed := built.Clone()
		change(&changed.Models[0])
		changedHash, err := ir.Hash(changed)
		if err != nil || changedHash == baselineHash || model.Equal(changed.Models[0]) {
			t.Fatal("constraint edit was omitted from equality or digest", err)
		}
	}
	declaration.Models[0].UniqueConstraints[0].Fields[0] = "mutated"
	cloned := built.Clone()
	cloned.Models[0].UniqueConstraints[1].Fields[0] = "id"
	if model.UniqueConstraints[1].Fields[0] != "category" || built.Models[0].UniqueConstraints[1].Fields[0] != "category" {
		t.Fatal("nested members alias caller or clone")
	}
}

func TestNamedUniqueConstraintRejectsAmbiguousOrMissingMembers(t *testing.T) {
	cases := []struct {
		name        string
		constraints []schema.UniqueConstraint
		path, code  string
	}{
		{"empty name", []schema.UniqueConstraint{{Fields: []string{"name"}}}, "models[0].unique_constraints[0].name", "invalid_identifier"},
		{"invalid name", []schema.UniqueConstraint{{Name: "has space", Fields: []string{"name"}}}, "models[0].unique_constraints[0].name", "invalid_identifier"},
		{"duplicate name", []schema.UniqueConstraint{{Name: "same", Fields: []string{"name"}}, {Name: "same", Fields: []string{"category"}}}, "models[0].unique_constraints[1].name", "duplicate"},
		{"empty members", []schema.UniqueConstraint{{Name: "scope"}}, "models[0].unique_constraints[0].fields", "empty"},
		{"duplicate member", []schema.UniqueConstraint{{Name: "scope", Fields: []string{"name", "name"}}}, "models[0].unique_constraints[0].fields[1]", "duplicate"},
		{"unknown member", []schema.UniqueConstraint{{Name: "scope", Fields: []string{"category", "missing"}}}, "models[0].unique_constraints[0].fields[1]", "unknown_field"},
		{"storage member", []schema.UniqueConstraint{{Name: "scope", Fields: []string{"category_id", "name"}}}, "models[0].unique_constraints[0].fields[0]", "unknown_field"},
		{"relation path", []schema.UniqueConstraint{{Name: "scope", Fields: []string{"category__id", "name"}}}, "models[0].unique_constraints[0].fields[0]", "unknown_field"},
		{"invalid member", []schema.UniqueConstraint{{Name: "scope", Fields: []string{"Name"}}}, "models[0].unique_constraints[0].fields[0]", "invalid_identifier"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			input := namedUniqueDeclaration()
			input.Models[0].UniqueConstraints = test.constraints
			result, err := schema.Build(input)
			var diagnostic *ir.ValidationError
			if !errors.As(err, &diagnostic) || diagnostic.Path != test.path || diagnostic.Code != test.code || len(result.Models) != 0 {
				t.Fatalf("accepted or misdiagnosed invalid declaration: %v", err)
			}
		})
	}
}

func TestNamedUniqueConstraintOwnsNameWithinEachModelAndAllowsPrimaryKey(t *testing.T) {
	input := namedUniqueDeclaration()
	input.Models[0].UniqueConstraints = []schema.UniqueConstraint{{Name: "own_name", Fields: []string{"id", "name"}}}
	input.Models = append(input.Models, schema.Model{Name: "other", GoName: "Other", UniqueConstraints: []schema.UniqueConstraint{{Name: "own_name", Fields: []string{"id"}}}})
	built, err := schema.Build(input)
	if err != nil || len(built.Models) != 2 {
		t.Fatal("model-local named ownership or implicit key lost", err)
	}
	if built.Models[0].Fields[0].Unique || built.Models[1].Fields[0].Unique {
		t.Fatal("named constraint changed primary key metadata")
	}
	for _, value := range []ir.Model{{UniqueConstraints: []ir.UniqueConstraint{}}, {UniqueConstraints: []ir.UniqueConstraint{{Name: "empty", Fields: []string{}}}}} {
		if !reflect.DeepEqual(value, value.Clone()) {
			t.Fatal("clone did not preserve raw metadata shape")
		}
	}
}
