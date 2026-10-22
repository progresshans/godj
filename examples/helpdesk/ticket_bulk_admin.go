package helpdesk

import (
	"context"
	"github.com/progresshans/godj/query"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

func (a *Application) ticketBulkActions() []admin.ActionConfig {
	if a.adminAudit == nil {
		return nil
	}
	result := make([]admin.ActionConfig, 0, 3)
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
					input, err := models.TicketObjects.Input(map[string]query.Value{"closed": query.Boolean(action.closed)})
					if err != nil {
						return admin.ActionResult{}, err
					}
					changes[index] = ticketBulkChange{index: index, id: id, input: input}
				}
				updated, err := a.updateTickets(ctx, actor, changes, a.adminAudit)
				if err != nil {
					return admin.ActionResult{}, err
				}
				return admin.ActionResult{MatchedIDs: updated.changedIDs}, nil
			},
		})
	}
	return append(result, admin.ActionConfig{
		Name: "raise-priority", Label: "Raise selected ticket priorities", Permission: ChangeTicket,
		AdditionalPermissions: []auth.Permission{ViewTicket, ViewLabel},
		SuccessNotice:         admin.ActionNotice{Tag: "priority-raised", Text: "Priority raised for {count} ticket(s)."},
		Run: func(ctx context.Context, actor auth.Principal, ids []int64) (admin.ActionResult, error) {
			updated, err := a.raiseTicketPriority(ctx, actor, ids, a.adminAudit)
			if err != nil {
				return admin.ActionResult{}, err
			}
			return admin.ActionResult{MatchedIDs: updated.changedIDs}, nil
		},
	})
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
	labelChoices, err := a.labelChoiceSource("ticket", "labels")
	if err != nil {
		return nil, err
	}
	binding, err := project.Bind()
	if err != nil {
		return nil, err
	}
	choices, err := labelModelChoices("ticket", "labels")
	if err != nil {
		return nil, err
	}
	choices = choices.Filter(models.LabelFields.ID.GreaterThan(0), a.relations.ModelsLabel.Category.ID.Exact(a.categoryID))
	var definition admin.ModelCollectionFormSet[models.Ticket]
	definition, err = admin.NewCollectionFormSet(admin.CollectionFormSetConfig[models.Ticket]{
		Name: "create", Label: "Create multiple tickets", Manager: models.TicketObjects,
		Project: binding, Source: ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"},
		Permissions:           admin.Permissions{View: ViewTicket, Add: AddTicket, Change: ChangeTicket, Delete: DeleteTicket},
		AdditionalPermissions: []auth.Permission{AddTicket},
		Form: admin.FormConfig{Definition: formmodel.Definition{Fields: fields, Overrides: overrides,
			PostClean: formmodel.PostClean{Fields: []string{"category"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
				return forms.NewValues(map[string]forms.Value{"category": forms.Integer(a.categoryID)}), validation.NewErrors()
			}},
		}, RelatedChoices: []admin.RelatedChoices{labelChoices}},
		Set: policy,
		Load: func(context.Context, auth.Principal, admin.CollectionSetAccess) (admin.CollectionSetSnapshot[models.Ticket], error) {
			return admin.CollectionSetSnapshot[models.Ticket]{}, nil
		},
		Run: func(ctx context.Context, actor auth.Principal, submission admin.CollectionSetSubmission) (admin.CollectionSetResult, error) {
			result, err := runApplicationRelationAtomic(ctx, a.backend, func(ctx context.Context, session db.RelationSession) (admin.CollectionSetResult, error) {
				bound, err := definition.BindCurrent(ctx, actor, submission, func(context.Context, admin.CollectionSetAccess) (admin.CollectionSetCurrent[models.Ticket], error) {
					return admin.CollectionSetCurrent[models.Ticket]{ResolveChoices: func(work context.Context, input formmodel.SetPreflight[models.Ticket]) (forms.RowModelChoices, error) {
						selections := make(map[int]map[string][]forms.Choice)
						for index := 0; index < input.TotalForms(); index++ {
							keys, valid, err := input.ChoiceKeys(index, "labels")
							if err != nil {
								return forms.RowModelChoices{}, err
							}
							if !valid {
								continue
							}
							selected, err := choices.Resolve(work, session, keys)
							if err != nil {
								return forms.RowModelChoices{}, err
							}
							selections[index] = map[string][]forms.Choice{"labels": selected.Items()}
						}
						return forms.NewRowModelChoices(selections, nil), nil
					}}, nil
				})
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				if !bound.Set().Valid() {
					return admin.CollectionSetResult{}, rejectTicketCollectionSet(bound.Set().FormSet())
				}
				prepared, err := bound.Set().Prepare()
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				rows := prepared.Rows()
				candidates := make([]ticketBulkCandidate, len(rows))
				for position, row := range rows {
					value, err := row.Model()
					if err != nil {
						return admin.CollectionSetResult{}, err
					}
					labels, _ := row.Prepared().Collections().Integers("labels")
					candidates[position] = ticketBulkCandidate{index: row.Index(), value: value, labels: labels}
				}
				created, err := a.createTicketsInSession(ctx, session, actor, candidates, a.adminAudit)
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				writes := make([]admin.CollectionSetWrite, len(created.records))
				for index, value := range created.records {
					writes[index] = admin.CollectionSetWrite{Index: rows[index].Index(), Kind: "create", ID: value.ID}
				}
				return bound.Result(writes)
			})
			if err != nil {
				return admin.CollectionSetResult{}, ticketCollectionRejection(err)
			}
			return result, err
		},
	})
	if err != nil {
		return nil, err
	}
	edit, err := a.ticketEditFormSet(fields, overrides)
	if err != nil {
		return nil, err
	}
	return []admin.CollectionFormSet{definition.View(), edit}, nil
}

func rejectTicketCollectionSet(set forms.Set) error {
	rows := make(map[int]validation.Errors)
	for _, row := range set.Forms() {
		if !row.Form().Errors().Empty() {
			rows[row.Index()] = row.Form().Errors()
		}
	}
	whole := set.NonFormErrors()
	for _, failure := range set.Management().Errors().All() {
		// Management fields belong to the set, not to any row Form. Preserve
		// their code/parameters while naming the control as diagnostic context.
		params := append(failure.Params(), validation.NewParam("field", string(failure.Field())))
		whole = validation.Join(whole, validation.NewErrors(validation.New(validation.NonField, failure.Code(), params...)))
	}
	return admin.RejectCollectionFormSet(rows, whole, nil)
}
