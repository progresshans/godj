package admin

import (
	"context"
	"slices"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
)

func TestChoiceRedisplayPrototypeRetainsAdmittedRowPolicy(t *testing.T) {
	for _, kind := range []string{"collection", "inline"} {
		t.Run(kind, func(t *testing.T) {
			for _, policy := range []struct {
				name           string
				change, delete bool
			}{{"allowed", true, true}, {"order_denied", false, true}, {"delete_denied", true, false}, {"both_denied", false, false}} {
				t.Run(policy.name, func(t *testing.T) {
					config, actor, snapshot, input := collectionCurrentFixture(t)
					// Include a changed extra row with a missing required subject.
					// Both existing rows and all relationship keys remain admitted.
					input.Del("tickets-0-ORDER")
					input.Del("tickets-1-ORDER")
					input.Set("tickets-TOTAL_FORMS", "3")
					input.Set("tickets-2-labels", "9")
					check := func(empty *forms.SetForm, stage string) {
						t.Helper()
						if empty == nil || empty.Form().ReadOnly() {
							t.Fatal(stage, "lost the permitted editable extra row")
						}
						for _, control := range []struct {
							name    string
							allowed bool
						}{{"ORDER", policy.change}, {"DELETE", policy.delete}} {
							present := slices.ContainsFunc(empty.Fields(), func(field forms.Field) bool { return field.Name() == control.name })
							if present != control.allowed {
								t.Errorf("%s prototype %s present=%v, admitted=%v", stage, control.name, present, control.allowed)
							}
						}
					}
					if kind == "collection" {
						declaration, err := NewCollectionFormSet(config)
						if err != nil {
							t.Fatal(err)
						}
						access := CollectionSetAccess{View: true, Add: true, Change: policy.change, Delete: policy.delete}
						initial, err := declaration.View().bind(t.Context(), actor, access, nil)
						if err != nil {
							t.Fatal(err)
						}
						check(initial.empty, "initial")
						data := forms.NewData(input)
						bound, err := declaration.View().bind(t.Context(), actor, access, &data)
						if err != nil || bound.set.Valid() || bound.set.Forms()[2].Form().Errors().ByField("subject").Empty() {
							t.Fatal("missing subject did not reach ordinary field redisplay", err)
						}
						check(bound.empty, "invalid redisplay")
						return
					}
					for _, index := range []string{"0", "1", "2"} {
						input.Set("tickets-"+index+"-category", "7")
					}
					declaration, err := NewInline(InlineConfig[models.Category, models.Ticket]{
						Project: config.Project, Parent: models.CategoryObjects, Child: models.TicketObjects,
						ParentWithID: models.NewCategoryWithID, ForeignKey: "category", Label: "Tickets",
						Form: config.Form, Set: config.Set, Permissions: config.Permissions,
						Load: func(context.Context, auth.Principal, int64) (InlineSnapshot[models.Ticket], error) {
							return InlineSnapshot[models.Ticket]{Current: snapshot.Current, Related: snapshot.Related}, nil
						},
					})
					if err != nil {
						t.Fatal(err)
					}
					access := InlineAccess{View: true, Add: true, Change: policy.change, Delete: policy.delete}
					initial, err := declaration.View().bind(t.Context(), actor, 7, access, nil)
					if err != nil {
						t.Fatal(err)
					}
					check(initial.empty, "initial")
					data := forms.NewData(input)
					bound, err := declaration.View().bind(t.Context(), actor, 7, access, &data)
					if err != nil || bound.set.Valid() || bound.set.Forms()[2].Form().Errors().ByField("subject").Empty() {
						t.Fatal("missing subject did not reach ordinary field redisplay", err)
					}
					check(bound.empty, "invalid redisplay")
				})
			}
		})
	}
}
