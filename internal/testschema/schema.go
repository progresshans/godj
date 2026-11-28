package testschema

import "github.com/progresshans/godj/schema/ir"

func Relation() (ir.Schema, ir.Schema) {
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

func QueryRelation() (ir.Schema, ir.Schema) {
	authors, blog := Relation()
	blog.Models[0].Fields = append(
		blog.Models[0].Fields[:1],
		append(
			[]ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}},
			blog.Models[0].Fields[1:]...,
		)...,
	)
	return authors, blog
}

func Bundle() (ir.Schema, ir.Schema) {
	authors := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "authors",
		Models: []ir.Model{{
			Name:   "author",
			GoName: "Author",
			Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 100}},
		}},
	}
	blog := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:   "blog_post",
			GoName: "BlogPost",
			Fields: []ir.Field{
				{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
				{
					Name: "author", GoName: "AuthorID", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "blog_posts"},
						OnDelete:    ir.DeleteProtect,
					},
				},
				{
					Name: "reviewer", GoName: "ReviewerID", Kind: ir.FieldForeignKey, Nullable: true,
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
