// Package onetoonefixture declares independent ticket and report apps used by
// the generated one-to-one product checks. It never imports generated models.
package onetoonefixture

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, fmt.Errorf("one-to-one fixture: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	tickets, err := schema.Build(schema.Definition{AppLabel: "ototickets", Models: []schema.Model{
		{Name: "ticket", GoName: "Ticket", Fields: []schema.Field{schema.CharField("subject", "Subject", 80)}},
	}})
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	reports, err := schema.Build(schema.Definition{AppLabel: "otoreports", Models: []schema.Model{
		{Name: "report", GoName: "Report", Fields: []schema.Field{
			schema.OneToOne("ticket", "TicketID", schema.Target("ototickets", "ticket"), schema.ReverseRelation{}, schema.Protect),
			schema.CharField("note", "Note", 80),
		}},
		{Name: "optional_report", GoName: "OptionalReport", Fields: []schema.Field{
			schema.OneToOne("ticket", "TicketID", schema.Target("ototickets", "ticket"), schema.RelatedName("optional_report"), schema.SetNull, schema.Nullable()),
			schema.CharField("note", "Note", 80),
		}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{
			schema.ForeignKey("ticket", "TicketID", schema.Target("ototickets", "ticket"), schema.RelatedName("links"), schema.Protect, schema.Unique()),
			schema.CharField("label", "Label", 80),
		}},
		{Name: "review", GoName: "Review", Fields: []schema.Field{
			schema.OneToOne("ticket", "TicketID", schema.Target("ototickets", "ticket"), schema.RelatedName("review"), schema.Protect),
			schema.IntegerField("score", "Score", schema.Nullable()),
			schema.CharField("title", "Title", 80, schema.Nullable()),
			schema.TextField("body", "Body", schema.Nullable()),
			schema.BooleanField("approved", "Approved", schema.Nullable()),
			schema.FloatField("ratio", "Ratio", schema.Nullable()),
			schema.DecimalField("price", "Price", 8, 2, schema.Nullable()),
			schema.UUIDField("token", "Token", schema.Nullable()),
			schema.JSONField("payload", "Payload", schema.Nullable()),
			schema.DateField("day", "Day", schema.Nullable()),
			schema.DateTimeField("at", "At", schema.Nullable()),
			schema.TimeField("clock", "Clock", schema.Nullable()),
			schema.DurationField("elapsed", "Elapsed", schema.Nullable()),
		}},
	}})
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	const root = "github.com/progresshans/godj/conformance/onetoonefixture/"
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: root + "project", Directory: "project"},
		Apps: []codegen.AppSpec{
			{Alias: "tickets", Package: codegen.PackageSpec{PackageName: "tickets", ImportPath: root + "tickets", Directory: "tickets"}, Schema: tickets},
			{Alias: "reports", Package: codegen.PackageSpec{PackageName: "reports", ImportPath: root + "reports", Directory: "reports"}, Schema: reports},
		},
	}, nil
}
