// Package cascadefixture declares the authored inputs of the independent
// Django CASCADE comparison. It never imports generated model packages.
package cascadefixture

import (
	"context"
	"errors"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, errors.New("cascade fixture: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	fk := func(name, goName, app, target string, policy schema.DeletePolicy, nullable bool) schema.Field {
		field := schema.ForeignKey(name, goName, schema.Target(app, target), schema.NoReverse(), policy)
		field.Nullable = nullable
		return field
	}
	parents, err := schema.Build(schema.Definition{AppLabel: "cascadeparents", Models: []schema.Model{
		{Name: "label", GoName: "Label", DBTable: "cascade_label", Fields: []schema.Field{schema.CharField("name", "Name", 64)}},
		{Name: "root", GoName: "Root", DBTable: "cascade_root", Fields: []schema.Field{schema.CharField("name", "Name", 64)}},
		{Name: "node", GoName: "Node", DBTable: "cascade_node", Fields: []schema.Field{fk("parent", "ParentID", "cascadeparents", "node", schema.Cascade, true)}},
		{Name: "left", GoName: "Left", DBTable: "cascade_left", Fields: []schema.Field{fk("right", "RightID", "cascadedetails", "right", schema.Cascade, true)}},
		{Name: "required_left", GoName: "RequiredLeft", DBTable: "cascade_required_left", Fields: []schema.Field{fk("right", "RightID", "cascadedetails", "required_right", schema.Cascade, false)}},
		// An explicit association tests the ORM-owned cleanup and tuple
		// constraint. This is not a general ManyToMany declaration or manager.
		{Name: "root_labels", GoName: "RootLabels", DBTable: "cascade_root_labels", Fields: []schema.Field{
			fk("root", "RootID", "cascadeparents", "root", schema.Cascade, false),
			fk("label", "LabelID", "cascadeparents", "label", schema.Cascade, false),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "root_label", Fields: []string{"root", "label"}}}},
	}})
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	details, err := schema.Build(schema.Definition{AppLabel: "cascadedetails", Models: []schema.Model{
		{Name: "child", GoName: "Child", DBTable: "cascade_child", Fields: []schema.Field{schema.ForeignKey("root", "RootID", schema.Target("cascadeparents", "root"), schema.RelatedName("children"), schema.Cascade)}},
		{Name: "grandchild", GoName: "Grandchild", DBTable: "cascade_grandchild", Fields: []schema.Field{schema.ForeignKey("child", "ChildID", schema.Target("cascadedetails", "child"), schema.RelatedName("grandchildren"), schema.Cascade)}},
		{Name: "watcher", GoName: "Watcher", DBTable: "cascade_watcher", Fields: []schema.Field{fk("child", "ChildID", "cascadedetails", "child", schema.SetNull, true)}},
		{Name: "protected", GoName: "Protected", DBTable: "cascade_protected", Fields: []schema.Field{fk("grandchild", "GrandchildID", "cascadedetails", "grandchild", schema.Protect, false)}},
		{Name: "twin", GoName: "Twin", DBTable: "cascade_twin", Fields: []schema.Field{
			fk("first", "FirstID", "cascadeparents", "root", schema.Cascade, false), fk("second", "SecondID", "cascadeparents", "root", schema.Cascade, false),
		}},
		{Name: "hidden", GoName: "Hidden", DBTable: "cascade_hidden", Fields: []schema.Field{fk("root", "RootID", "cascadeparents", "root", schema.Cascade, false)}},
		{Name: "detail", GoName: "Detail", DBTable: "cascade_detail", Fields: []schema.Field{schema.OneToOne("root", "RootID", schema.Target("cascadeparents", "root"), schema.RelatedName("detail"), schema.Cascade)}},
		{Name: "overlap", GoName: "Overlap", DBTable: "cascade_overlap", Fields: []schema.Field{
			fk("cascade_root", "CascadeRootID", "cascadeparents", "root", schema.Cascade, false), fk("protected_root", "ProtectedRootID", "cascadeparents", "root", schema.Protect, false),
		}},
		{Name: "right", GoName: "Right", DBTable: "cascade_right", Fields: []schema.Field{fk("left", "LeftID", "cascadeparents", "left", schema.Cascade, false)}},
		{Name: "required_right", GoName: "RequiredRight", DBTable: "cascade_required_right", Fields: []schema.Field{fk("left", "LeftID", "cascadeparents", "required_left", schema.Cascade, false)}},
	}})
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	const root = "github.com/progresshans/godj/conformance/cascadefixture/"
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: root + "project", Directory: "project"}, Apps: []codegen.AppSpec{
		{Alias: "parents", Package: codegen.PackageSpec{PackageName: "parents", ImportPath: root + "parents", Directory: "parents"}, Schema: parents},
		{Alias: "details", Package: codegen.PackageSpec{PackageName: "details", ImportPath: root + "details", Directory: "details"}, Schema: details},
	}}, nil
}
