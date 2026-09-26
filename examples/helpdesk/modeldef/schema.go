// Package modeldef declares the Helpdesk consumer independently of Article.
package modeldef

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func Schema() (ir.Schema, error) {
	return schema.Build(schema.Definition{AppLabel: "helpdesk", Models: []schema.Model{
		{Name: "category", GoName: "Category", Fields: []schema.Field{schema.CharField("name", "Name", 60)}},
		{Name: "ticket", GoName: "Ticket", Fields: []schema.Field{
			schema.CharField("subject", "Subject", 120),
			schema.CharField("details", "Details", 400, schema.Nullable()),
			schema.BooleanField("closed", "Closed", schema.Default(false)),
			schema.ForeignKey("category", "CategoryID", schema.Target("helpdesk", "category"), schema.RelatedName("tickets"), schema.Protect),
			schema.IntegerField("priority", "Priority", schema.Nullable(), schema.Choices(
				schema.Choice(int64(1), "Urgent"), schema.Choice(int64(0), "Normal"), schema.Choice(int64(-1), "Low"),
			)),
			schema.TextField("resolution", "Resolution", schema.Nullable()),
			schema.DateTimeField("due_at", "DueAt", schema.Nullable()),
			schema.BooleanField("reviewed", "Reviewed", schema.Nullable()),
			schema.DateField("service_on", "ServiceOn", schema.Nullable()),
			schema.TimeField("service_at", "ServiceAt", schema.Nullable()),
			schema.DurationField("elapsed", "Elapsed", schema.Nullable()),
			schema.FloatField("effort", "Effort", schema.Nullable()),
			schema.DecimalField("expected_cost", "ExpectedCost", 14, 2, schema.Nullable()),
			schema.UUIDField("external_reference", "ExternalReference", schema.Nullable(), schema.Unique()),
			schema.JSONField("external_payload", "ExternalPayload", schema.Nullable()),
		}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("labels", "Labels", schema.Target("helpdesk", "label"), schema.RelatedName("tickets"), schema.Through(schema.Target("helpdesk", "ticket_label"), "ticket", "label")),
		}},
		{Name: "service_report", GoName: "ServiceReport", Fields: []schema.Field{
			schema.OneToOne("ticket", "TicketID", schema.Target("helpdesk", "ticket"), schema.RelatedName("service_report"), schema.Protect),
			schema.TextField("summary", "Summary"),
			schema.BooleanField("completed", "Completed", schema.Default(false)),
		}},
		{Name: "label", GoName: "Label", Fields: []schema.Field{
			schema.CharField("name", "Name", 64),
			schema.ForeignKey("category", "CategoryID", schema.Target("helpdesk", "category"), schema.RelatedName("labels"), schema.Protect),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "category_name", Fields: []string{"category", "name"}}}},
		{Name: "ticket_label", GoName: "TicketLabel", Fields: []schema.Field{
			schema.ForeignKey("ticket", "TicketID", schema.Target("helpdesk", "ticket"), schema.RelatedName("label_links"), schema.Cascade),
			schema.ForeignKey("label", "LabelID", schema.Target("helpdesk", "label"), schema.RelatedName("ticket_links"), schema.Cascade),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "ticket_label", Fields: []string{"ticket", "label"}}}},
	}})
}

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, fmt.Errorf("helpdesk schema: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	definition, err := Schema()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	const root = "github.com/progresshans/godj/examples/helpdesk/"
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: root + "project", Directory: "project"},
		Apps:    []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: root + "models", Directory: "models"}, Schema: definition}},
	}, nil
}
