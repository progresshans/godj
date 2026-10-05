package helpdesk

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

func (a *Application) ticketBulkActions() []admin.ActionConfig {
	if a.adminAudit == nil {
		return nil
	}
	result := make([]admin.ActionConfig, 0, 2)
	for _, action := range []struct {
		name, label, result string
		closed              bool
	}{
		{"close", "Close selected tickets", "closed", true}, {"reopen", "Reopen selected tickets", "reopened", false},
	} {
		result = append(result, admin.ActionConfig{
			Name: action.name, Label: action.label, Permission: ChangeTicket,
			AdditionalPermissions: []auth.Permission{ViewTicket, ViewLabel},
			SuccessNotice:         admin.ActionNotice{Tag: action.result, Text: "{count} ticket(s) " + action.result + "."},
			Run: func(ctx context.Context, actor auth.Principal, ids []int64) (admin.ActionResult, error) {
				changes := make([]ticketBulkChange, len(ids))
				for index, id := range ids {
					changes[index] = ticketBulkChange{index: index, id: id, patch: models.TicketPatch{}.WithClosed(action.closed)}
				}
				updated, err := a.updateTickets(ctx, actor, changes, a.adminAudit)
				if err != nil {
					return admin.ActionResult{}, err
				}
				return admin.ActionResult{MatchedIDs: updated.changedIDs}, nil
			},
		})
	}
	return result
}

// AdminRenderLimits covers forty rows with the bounded label chooser. The
// enclosing web.Application must use MaxResponseBytes with this byte limit.
func AdminRenderLimits() templates.Limits {
	return templates.Limits{MaxLoopItems: 40_000, MaxOutputBytes: 8 << 20}
}

func (a *Application) ticketBulkFormSets(fields []string, overrides []formmodel.Override) ([]admin.CollectionFormSet, error) {
	if a.adminAudit == nil {
		return nil, nil
	}
	policy := forms.DefaultSetConfig()
	policy.Prefix, policy.ExtraForms, policy.MinForms = "tickets", 1, 1
	policy.MaxForms, policy.AbsoluteMax = ticketBulkMaximum, ticketBulkMaximum
	policy.ValidateMin, policy.ValidateMax = true, true
	definition, err := admin.NewCollectionFormSet(admin.CollectionFormSetConfig[models.Ticket]{
		Name: "create", Label: "Create multiple tickets", Manager: models.TicketObjects, Permission: AddTicket,
		Form: admin.FormConfig{Definition: formmodel.Definition{Fields: fields, Overrides: overrides,
			PostClean: formmodel.PostClean{Fields: []string{"category"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
				return forms.NewValues(map[string]forms.Value{"category": forms.Integer(a.categoryID)}), validation.NewErrors()
			}},
		}, RelatedChoices: []admin.RelatedChoices{{Field: "labels", Permission: ViewLabel, Load: a.ticketLabelChoices}}},
		Set: policy,
		Run: func(ctx context.Context, actor auth.Principal, set formmodel.InstanceSet[models.Ticket]) ([]int64, error) {
			prepared, err := set.Prepare()
			if err != nil {
				return nil, err
			}
			rows := prepared.Rows()
			candidates := make([]ticketBulkCandidate, len(rows))
			for position, row := range rows {
				value, err := row.Model()
				if err != nil {
					return nil, err
				}
				labels, _ := row.Prepared().Collections().Integers("labels")
				candidates[position] = ticketBulkCandidate{index: row.Index(), value: value, labels: labels}
			}
			created, err := a.createTickets(ctx, actor, candidates, a.adminAudit)
			if failures, rejected := validation.Rejected(err); rejected {
				rowFailures := make(map[int]validation.Errors)
				var whole validation.Errors
				for _, failure := range failures.All() {
					if index, present := bulkFailureIndex(failure); present {
						rowFailures[index] = validation.Join(rowFailures[index], validation.NewErrors(failure))
					} else {
						whole = validation.Join(whole, validation.NewErrors(failure))
					}
				}
				return nil, admin.RejectCollectionFormSet(rowFailures, whole, err)
			}
			if err != nil {
				return nil, err
			}
			identities := make([]int64, len(created.records))
			for index, value := range created.records {
				identities[index] = value.ID
			}
			return identities, nil
		},
	})
	if err != nil {
		return nil, err
	}
	return []admin.CollectionFormSet{definition}, nil
}
