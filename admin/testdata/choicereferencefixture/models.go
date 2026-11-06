// Package choicereference owns the small canonical models used by the independent
// Admin-choice comparison. These descriptors are handwritten test input, not
// generated output or a second source of expected Django results.
package choicereferencefixture

import (
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const App = "admin_choice_reference"

type Target struct {
	ID, Tenant int64
	Label      string
	Enabled    bool
	present    bool
}

type Source struct {
	ID, Tenant, ForeignKey, OneToOne int64
	present                          bool
}

type Link struct {
	ID, SourceID, TargetID int64
	present                bool
}

func NewTarget(id, tenant int64, label string, enabled bool) Target {
	return Target{ID: id, Tenant: tenant, Label: label, Enabled: enabled, present: true}
}
func NewSource(id, tenant, foreign, single int64) Source {
	return Source{ID: id, Tenant: tenant, ForeignKey: foreign, OneToOne: single, present: true}
}
func NewLink(id, source, target int64) Link {
	return Link{ID: id, SourceID: source, TargetID: target, present: true}
}

// Declaration uses the supported AutoField IR. Parameterized SQL seeds explicit
// signed target keys; typed reads and updates then exercise the public APIs.
// This fixture does not claim PostgreSQL explicit generated-key insertion support.
func Declaration() ir.Schema {
	key := func() ir.Field { return ir.Field{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true} }
	foreign := func(name, goName, model, reverse string, cardinality ir.RelationCardinality, policy ir.DeletePolicy) ir.Field {
		return ir.Field{Name: name, GoName: goName, Kind: ir.FieldForeignKey,
			Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: App, ModelName: model},
				Cardinality: cardinality, Reverse: ir.ReverseRelation{Name: reverse}, OnDelete: policy}}
	}
	return ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: App, Models: []ir.Model{
		{Name: "target", GoName: "Target", DBTable: "gdj_admin_choice_target", Fields: []ir.Field{
			key(), {Name: "tenant", GoName: "Tenant", Kind: ir.FieldInteger},
			{Name: "label", GoName: "Label", Kind: ir.FieldChar, MaxLength: 120},
			{Name: "enabled", GoName: "Enabled", Kind: ir.FieldBoolean, Default: &ir.Scalar{Kind: ir.ScalarBoolean, Boolean: true}},
		}},
		{Name: "source", GoName: "Source", DBTable: "gdj_admin_choice_source", Fields: []ir.Field{
			key(), {Name: "tenant", GoName: "Tenant", Kind: ir.FieldInteger},
			foreign("foreign_key", "ForeignKey", "target", "foreign_sources", ir.RelationManyToOne, ir.DeleteProtect),
			foreign("one_to_one", "OneToOne", "target", "single_source", ir.RelationOneToOne, ir.DeleteProtect),
		}, ManyToMany: []ir.ManyToManyField{{Name: "many", GoName: "Many", Blank: true,
			Target: ir.ModelIdentity{AppLabel: App, ModelName: "target"}, Reverse: ir.ReverseRelation{Name: "multiple_sources"},
			Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: App, ModelName: "link"}, SourceField: "source", TargetField: "target"}}}},
		{Name: "link", GoName: "Link", DBTable: "gdj_admin_choice_link", Fields: []ir.Field{
			key(), foreign("source", "SourceID", "source", "many_links", ir.RelationManyToOne, ir.DeleteCascade),
			foreign("target", "TargetID", "target", "source_links", ir.RelationManyToOne, ir.DeleteCascade),
		}, UniqueConstraints: []ir.UniqueConstraint{{Name: "admin_choice_unique_membership", Fields: []string{"source", "target"}}}},
	}}
}

type Fixture struct {
	Schema           ir.Schema
	Project          orm.ProjectBinding
	Targets          orm.Manager[Target]
	Sources          orm.Manager[Source]
	Links            orm.Manager[Link]
	TargetID         orm.AutoField[Target]
	Tenant           orm.IntegerField[Target]
	Label            orm.StringField[Target]
	Enabled          orm.BooleanField[Target]
	SourceID         orm.AutoField[Source]
	LinkID           orm.AutoField[Link]
	LinkSource       orm.ForeignKeyField[Link]
	LinkTarget       orm.ForeignKeyField[Link]
	SourceDescriptor SourceDescriptor
}

func New() (Fixture, error) {
	schema, err := ir.Normalize(Declaration())
	if err != nil {
		return Fixture{}, err
	}
	binding, err := orm.BindProject(schema)
	if err != nil {
		return Fixture{}, err
	}
	models := make(map[string]ir.Model)
	for _, model := range schema.Models {
		canonical, found := binding.Model(ir.ModelIdentity{AppLabel: App, ModelName: model.Name})
		if !found || !canonical.Equal(model) {
			return Fixture{}, fmt.Errorf("choice fixture canonical model mismatch: %s", model.Name)
		}
		models[model.Name] = canonical
	}
	target, source, link := targetDescriptor{models["target"]}, SourceDescriptor{models["source"]}, linkDescriptor{models["link"]}
	field := func(model ir.Model, name string) ir.Field {
		for _, candidate := range model.Fields {
			if candidate.Name == name {
				return candidate
			}
		}
		return ir.Field{}
	}
	return Fixture{Schema: schema, Project: binding, Targets: orm.NewManager[Target](target), Sources: orm.NewManager[Source](source), Links: orm.NewManager[Link](link),
		TargetID: orm.NewAutoField[Target](field(target.model, "id")), Tenant: orm.NewIntegerField[Target](field(target.model, "tenant")),
		Label: orm.NewStringField[Target](field(target.model, "label")), Enabled: orm.NewBooleanField[Target](field(target.model, "enabled")),
		SourceID: orm.NewAutoField[Source](field(source.model, "id")), LinkID: orm.NewAutoField[Link](field(link.model, "id")),
		LinkSource: orm.NewForeignKeyField[Link](field(link.model, "source")), LinkTarget: orm.NewForeignKeyField[Link](field(link.model, "target")), SourceDescriptor: source}, nil
}

type targetDescriptor struct{ model ir.Model }

func (d targetDescriptor) Metadata() ir.Model { return d.model.Clone() }
func (targetDescriptor) Scan(row db.Row) (Target, error) {
	var v Target
	err := row.Scan(&v.ID, &v.Tenant, &v.Label, &v.Enabled)
	v.present = err == nil
	return v, err
}
func (targetDescriptor) CloneModel(v Target) Target      { return v }
func (targetDescriptor) CloneWriteModel(v Target) Target { return v }
func (targetDescriptor) PrimaryKey(v Target) (query.Value, bool) {
	return query.Integer(v.ID), v.present
}
func (targetDescriptor) SetPrimaryKey(v *Target, k int64) { v.ID, v.present = k, true }
func (targetDescriptor) ClearPrimaryKey(v *Target)        { v.ID, v.present = 0, false }
func (targetDescriptor) WriteFieldValue(v Target, f ir.Field) (query.Value, bool) {
	switch f.Name {
	case "id":
		return query.Integer(v.ID), true
	case "tenant":
		return query.Integer(v.Tenant), true
	case "label":
		return query.String(v.Label), true
	case "enabled":
		return query.Boolean(v.Enabled), true
	}
	return query.Value{}, false
}
func (targetDescriptor) SetFieldValue(v *Target, f ir.Field, x query.Value) bool {
	if v == nil {
		return false
	}
	switch f.Name {
	case "tenant":
		n, ok := x.Integer()
		if ok {
			v.Tenant = n
		}
		return ok
	case "label":
		s, ok := x.String()
		if ok {
			v.Label = s
		}
		return ok
	case "enabled":
		b, ok := x.Boolean()
		if ok {
			v.Enabled = b
		}
		return ok
	}
	return false
}

type SourceDescriptor struct{ model ir.Model }

func (d SourceDescriptor) Metadata() ir.Model { return d.model.Clone() }
func (SourceDescriptor) Scan(row db.Row) (Source, error) {
	var v Source
	err := row.Scan(&v.ID, &v.Tenant, &v.ForeignKey, &v.OneToOne)
	v.present = err == nil
	return v, err
}
func (SourceDescriptor) CloneModel(v Source) Source      { return v }
func (SourceDescriptor) CloneWriteModel(v Source) Source { return v }
func (SourceDescriptor) PrimaryKey(v Source) (query.Value, bool) {
	return query.Integer(v.ID), v.present
}
func (SourceDescriptor) SetPrimaryKey(v *Source, k int64) { v.ID, v.present = k, true }
func (SourceDescriptor) ClearPrimaryKey(v *Source)        { v.ID, v.present = 0, false }
func (SourceDescriptor) WriteFieldValue(v Source, f ir.Field) (query.Value, bool) {
	switch f.Name {
	case "id":
		return query.Integer(v.ID), true
	case "tenant":
		return query.Integer(v.Tenant), true
	case "foreign_key":
		return query.Integer(v.ForeignKey), true
	case "one_to_one":
		return query.Integer(v.OneToOne), true
	}
	return query.Value{}, false
}
func (SourceDescriptor) SetFieldValue(v *Source, f ir.Field, x query.Value) bool {
	if v == nil {
		return false
	}
	n, ok := x.Integer()
	if !ok {
		return false
	}
	switch f.Name {
	case "tenant":
		v.Tenant = n
	case "foreign_key":
		v.ForeignKey = n
	case "one_to_one":
		v.OneToOne = n
	default:
		return false
	}
	return true
}

type linkDescriptor struct{ model ir.Model }

func (d linkDescriptor) Metadata() ir.Model { return d.model.Clone() }
func (linkDescriptor) Scan(row db.Row) (Link, error) {
	var v Link
	err := row.Scan(&v.ID, &v.SourceID, &v.TargetID)
	v.present = err == nil
	return v, err
}
func (linkDescriptor) CloneModel(v Link) Link                { return v }
func (linkDescriptor) CloneWriteModel(v Link) Link           { return v }
func (linkDescriptor) PrimaryKey(v Link) (query.Value, bool) { return query.Integer(v.ID), v.present }
func (linkDescriptor) SetPrimaryKey(v *Link, k int64)        { v.ID, v.present = k, true }
func (linkDescriptor) ClearPrimaryKey(v *Link)               { v.ID, v.present = 0, false }
func (linkDescriptor) WriteFieldValue(v Link, f ir.Field) (query.Value, bool) {
	switch f.Name {
	case "id":
		return query.Integer(v.ID), true
	case "source":
		return query.Integer(v.SourceID), true
	case "target":
		return query.Integer(v.TargetID), true
	}
	return query.Value{}, false
}
func (linkDescriptor) SetFieldValue(v *Link, f ir.Field, x query.Value) bool {
	if v == nil {
		return false
	}
	n, ok := x.Integer()
	if !ok {
		return false
	}
	switch f.Name {
	case "source":
		v.SourceID = n
	case "target":
		v.TargetID = n
	default:
		return false
	}
	return true
}
