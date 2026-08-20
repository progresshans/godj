package orm_test

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func TestBindProjectMixedSchemasPublishesCanonicalFreshProjections(t *testing.T) {
	t.Parallel()

	authors, blog := relationSchemas()
	binding, err := orm.BindProject(blog, authors)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}

	wantForward := []orm.RelationMetadata{
		{
			Source:      ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			Field:       "author",
			Column:      "author_id",
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "posts"},
			OnDelete:    ir.DeleteProtect,
		},
		{
			Source:      ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			Field:       "reviewer",
			Column:      "reviewer_id",
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Nullable:    true,
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "reviewed_posts"},
			OnDelete:    ir.DeleteSetNull,
		},
	}
	wantReverse := []orm.ReverseRelationMetadata{
		{
			Owner:       ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Name:        "posts",
			Target:      ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			SourceField: "author",
			Cardinality: ir.RelationOneToMany,
		},
		{
			Owner:       ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Name:        "reviewed_posts",
			Target:      ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			SourceField: "reviewer",
			Cardinality: ir.RelationOneToMany,
		},
	}
	if got := binding.ForwardRelations(); !reflect.DeepEqual(got, wantForward) {
		t.Fatalf("ForwardRelations() = %#v, want %#v", got, wantForward)
	}
	if got := binding.ReverseRelations(); !reflect.DeepEqual(got, wantReverse) {
		t.Fatalf("ReverseRelations() = %#v, want %#v", got, wantReverse)
	}

	got, ok := binding.Relation(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, "reviewer")
	if !ok || !reflect.DeepEqual(got, wantForward[1]) {
		t.Fatalf("Relation(blog.post, reviewer) = (%#v, %v), want (%#v, true)", got, ok, wantForward[1])
	}
	if _, ok := binding.Relation(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, "missing"); ok {
		t.Fatal("Relation() found a missing field")
	}

	// A successful binding owns its normalized snapshot. Neither later input
	// mutation nor accessor-slice mutation can alter it.
	blog.Models[0].Fields[1].Relation.Target.AppLabel = "mutated"
	blog.Models[0].Fields[2].Relation.Reverse.Name = "mutated"
	forwardCopy := binding.ForwardRelations()
	forwardCopy[0].Field = "mutated"
	reverseCopy := binding.ReverseRelations()
	reverseCopy[0].Name = "mutated"
	if got := binding.ForwardRelations(); !reflect.DeepEqual(got, wantForward) {
		t.Fatalf("caller mutation changed ForwardRelations(): %#v", got)
	}
	if got := binding.ReverseRelations(); !reflect.DeepEqual(got, wantReverse) {
		t.Fatalf("caller mutation changed ReverseRelations(): %#v", got)
	}

	authorsAgain, blogAgain := relationSchemas()
	permuted, err := orm.BindProject(authorsAgain, blogAgain)
	if err != nil {
		t.Fatalf("BindProject() permuted error = %v", err)
	}
	if !reflect.DeepEqual(permuted.ForwardRelations(), wantForward) || !reflect.DeepEqual(permuted.ReverseRelations(), wantReverse) {
		t.Fatal("schema input order changed canonical binding output")
	}
}

func TestBindProjectNoReverseAndZeroProject(t *testing.T) {
	t.Parallel()

	empty, err := orm.BindProject()
	if err != nil {
		t.Fatalf("BindProject() zero error = %v", err)
	}
	if empty.ForwardRelations() != nil || empty.ReverseRelations() != nil {
		t.Fatalf("zero binding accessors = (%#v, %#v), want nil slices", empty.ForwardRelations(), empty.ReverseRelations())
	}

	authors, blog := relationSchemas()
	blog.Models[0].Fields[1].Relation.Reverse = ir.ReverseRelation{Disabled: true}
	binding, err := orm.BindProject(authors, blog)
	if err != nil {
		t.Fatalf("BindProject() disabled reverse error = %v", err)
	}
	if got, want := len(binding.ForwardRelations()), 2; got != want {
		t.Fatalf("forward count = %d, want %d", got, want)
	}
	if got, want := len(binding.ReverseRelations()), 1; got != want {
		t.Fatalf("reverse count = %d, want %d", got, want)
	}
}

func TestBindProjectErrorsAreTypedDeterministicAndPublishNothing(t *testing.T) {
	t.Parallel()

	t.Run("duplicate app precheck", func(t *testing.T) {
		authors, _ := relationSchemas()
		duplicate := authors.Clone()
		duplicate.Models = nil // Duplicate-app precheck must precede IR validation.
		binding, err := orm.BindProject(duplicate, authors)
		assertBindingError(t, err, &orm.RelationBindingError{
			Code:     orm.RelationBindingDuplicateApp,
			AppLabel: "authors",
		})
		assertEmptyBinding(t, binding)
	})

	t.Run("unresolved target", func(t *testing.T) {
		_, blog := relationSchemas()
		binding, err := orm.BindProject(blog)
		assertBindingError(t, err, &orm.RelationBindingError{
			Code:      orm.RelationBindingUnresolvedTarget,
			AppLabel:  "blog",
			ModelName: "post",
			FieldName: "author",
			Target:    ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		})
		assertEmptyBinding(t, binding)
	})

	t.Run("reverse target field collision", func(t *testing.T) {
		authors, blog := relationSchemas()
		blog.Models[0].Fields[1].Relation.Reverse.Name = "name"
		binding, err := orm.BindProject(blog, authors)
		assertBindingError(t, err, &orm.RelationBindingError{
			Code:        orm.RelationBindingReverseNameCollision,
			AppLabel:    "blog",
			ModelName:   "post",
			FieldName:   "author",
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			ReverseName: "name",
		})
		assertEmptyBinding(t, binding)
	})

	t.Run("reverse relation collision", func(t *testing.T) {
		authors, blog := relationSchemas()
		blog.Models[0].Fields[2].Relation.Reverse.Name = "posts"
		binding, err := orm.BindProject(authors, blog)
		assertBindingError(t, err, &orm.RelationBindingError{
			Code:        orm.RelationBindingReverseNameCollision,
			AppLabel:    "blog",
			ModelName:   "post",
			FieldName:   "reviewer",
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			ReverseName: "posts",
		})
		assertEmptyBinding(t, binding)
	})

	t.Run("IR validation remains IR owned", func(t *testing.T) {
		authors, blog := relationSchemas()
		blog.Models[0].Fields[1].Relation.OnDelete = ir.DeleteSetNull
		binding, err := orm.BindProject(authors, blog)
		var validationError *ir.ValidationError
		if !errors.As(err, &validationError) {
			t.Fatalf("error = %T %v, want *ir.ValidationError", err, err)
		}
		var bindingError *orm.RelationBindingError
		if errors.As(err, &bindingError) {
			t.Fatalf("IR validation was reclassified as %#v", bindingError)
		}
		assertEmptyBinding(t, binding)
	})

	// Multiple failures always select the first canonical source relation,
	// independent of input schema order.
	authors, blog := relationSchemas()
	blog.Models = append(blog.Models, ir.Model{
		Name:   "comment",
		GoName: "Comment",
		Fields: []ir.Field{{
			Name:   "post",
			GoName: "PostID",
			Kind:   ir.FieldForeignKey,
			Relation: &ir.ForeignKeyRelation{
				Target:      ir.ModelIdentity{AppLabel: "missing", ModelName: "post"},
				Cardinality: ir.RelationManyToOne,
				Reverse:     ir.ReverseRelation{Name: "comments"},
				OnDelete:    ir.DeleteProtect,
			},
		}},
	})
	blog.Models[0].Fields[2].Relation.Target = ir.ModelIdentity{AppLabel: "missing", ModelName: "reviewer"}
	for iteration := 0; iteration < 20; iteration++ {
		inputs := []ir.Schema{authors, blog}
		if iteration%2 == 1 {
			slices.Reverse(inputs)
		}
		_, err := orm.BindProject(inputs...)
		var bindingError *orm.RelationBindingError
		if !errors.As(err, &bindingError) {
			t.Fatalf("iteration %d error = %T %v, want *RelationBindingError", iteration, err, err)
		}
		if got, want := bindingError.ModelName+"."+bindingError.FieldName, "comment.post"; got != want {
			t.Fatalf("iteration %d canonical failure = %q, want %q", iteration, got, want)
		}
	}
}

func TestProjectBindingConcurrentReads(t *testing.T) {
	t.Parallel()

	authors, blog := relationSchemas()
	binding, err := orm.BindProject(authors, blog)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}
	const readers = 32
	const iterations = 200
	var wait sync.WaitGroup
	wait.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				if len(binding.ForwardRelations()) != 2 || len(binding.ReverseRelations()) != 2 {
					t.Errorf("reader observed incomplete relation projections")
					return
				}
				if _, ok := binding.Relation(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, "author"); !ok {
					t.Errorf("reader did not find canonical relation")
					return
				}
			}
		}()
	}
	wait.Wait()
}

func relationSchemas() (ir.Schema, ir.Schema) {
	authors := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "authors",
		Models: []ir.Model{{
			Name:   "author",
			GoName: "Author",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 100},
			},
		}},
	}
	blog := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:   "post",
			GoName: "Post",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name:   "author",
					GoName: "AuthorID",
					Kind:   ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "posts"},
						OnDelete:    ir.DeleteProtect,
					},
				},
				{
					Name:     "reviewer",
					GoName:   "ReviewerID",
					Kind:     ir.FieldForeignKey,
					Nullable: true,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "reviewed_posts"},
						OnDelete:    ir.DeleteSetNull,
					},
				},
			},
		}},
	}
	return authors, blog
}

func assertBindingError(t *testing.T, err error, want *orm.RelationBindingError) {
	t.Helper()
	var got *orm.RelationBindingError
	if !errors.As(err, &got) {
		t.Fatalf("error = %T %v, want *RelationBindingError", err, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RelationBindingError = %#v, want %#v", got, want)
	}
}

func assertEmptyBinding(t *testing.T, binding orm.ProjectBinding) {
	t.Helper()
	if len(binding.ForwardRelations()) != 0 || len(binding.ReverseRelations()) != 0 {
		t.Fatalf("failed binding partially published: forward=%#v reverse=%#v", binding.ForwardRelations(), binding.ReverseRelations())
	}
}
