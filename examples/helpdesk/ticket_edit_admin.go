package helpdesk

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// The collection editor admits the complete category cohort, bounded to forty
// rows. Its Load and write-time BindCurrent use the same declared form and set.
// ORDER is form order; no model position field is inferred or persisted.
func (a *Application) ticketEditFormSet(fields []string, overrides []formmodel.Override) (admin.CollectionFormSet, error) {
	binding, err := project.Bind()
	if err != nil {
		return admin.CollectionFormSet{}, err
	}
	labelChoices, err := a.labelChoiceSource("ticket", "labels")
	if err != nil {
		return admin.CollectionFormSet{}, err
	}
	choices, err := labelModelChoices("ticket", "labels")
	if err != nil {
		return admin.CollectionFormSet{}, err
	}
	choices = choices.Filter(models.LabelFields.ID.GreaterThan(0), a.relations.ModelsLabel.Category.ID.Exact(a.categoryID))
	policy := forms.DefaultSetConfig()
	policy.Prefix, policy.ExtraForms, policy.MaxForms, policy.AbsoluteMax = "tickets", 1, ticketBulkMaximum, ticketBulkMaximum
	policy.ValidateMin, policy.ValidateMax, policy.CanDelete, policy.CanOrder = true, true, true, true
	var declaration admin.ModelCollectionFormSet[models.Ticket]
	declaration, err = admin.NewCollectionFormSet(admin.CollectionFormSetConfig[models.Ticket]{
		Name: "edit", Label: "Edit multiple tickets", Project: binding, Source: ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"}, Manager: models.TicketObjects,
		Permissions: admin.Permissions{View: ViewTicket, Add: AddTicket, Change: ChangeTicket, Delete: DeleteTicket},
		Form: admin.FormConfig{
			Definition: formmodel.Definition{Fields: fields, Overrides: overrides,
				PostClean: formmodel.PostClean{Fields: []string{"category"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
					return forms.NewValues(map[string]forms.Value{"category": forms.Integer(a.categoryID)}), validation.Errors{}
				}},
			},
			RelatedChoices: []admin.RelatedChoices{labelChoices},
		},
		Set: policy,
		Load: func(ctx context.Context, actor auth.Principal, access admin.CollectionSetAccess) (admin.CollectionSetSnapshot[models.Ticket], error) {
			var snapshot admin.CollectionSetSnapshot[models.Ticket]
			var callbackErr error
			calls := 0
			err := a.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
				calls++
				if calls != 1 {
					return errors.New("helpdesk: repeated collection snapshot callback")
				}
				snapshot, callbackErr = a.ticketAdminCohort(ctx, reader)
				return callbackErr
			})
			if calls != 1 || callbackErr != nil && (err == nil || !errors.Is(err, callbackErr)) {
				err = errors.Join(errors.New("helpdesk: invalid collection snapshot owner"), err, callbackErr)
			}
			if err != nil {
				return admin.CollectionSetSnapshot[models.Ticket]{}, err
			}
			return snapshot, nil
		},
		Run: func(ctx context.Context, actor auth.Principal, submission admin.CollectionSetSubmission) (admin.CollectionSetResult, error) {
			result, err := runApplicationRelationAtomic(ctx, a.backend, func(ctx context.Context, session db.RelationSession) (admin.CollectionSetResult, error) {
				bound, err := declaration.BindCurrent(ctx, actor, submission, func(work context.Context, access admin.CollectionSetAccess) (admin.CollectionSetCurrent[models.Ticket], error) {
					rows, err := a.ticketAdminCohort(work, session)
					if err != nil {
						return admin.CollectionSetCurrent[models.Ticket]{}, err
					}
					return admin.CollectionSetCurrent[models.Ticket]{Rows: rows, ResolveChoices: func(work context.Context, input formmodel.SetPreflight[models.Ticket]) (forms.RowModelChoices, error) {
						selected := make(map[int]map[string][]forms.Choice)
						for index := 0; index < input.TotalForms(); index++ {
							keys, valid, err := input.ChoiceKeys(index, "labels")
							if err != nil {
								return forms.RowModelChoices{}, err
							}
							if !valid {
								continue
							}
							// A readonly row with a denied target permission keeps no labels.
							readonly, err := input.ReadOnly(index)
							if err != nil {
								return forms.RowModelChoices{}, err
							}
							if readonly && (!actor.Has(ViewLabel) || !access.ChoiceAllowed("labels")) {
								continue
							}
							resolved, err := choices.Resolve(work, session, keys)
							if err != nil {
								return forms.RowModelChoices{}, err
							}
							selected[index] = map[string][]forms.Choice{"labels": resolved.Items()}
						}
						return forms.NewRowModelChoices(selected, nil), nil
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
				plan, err := prepared.SavePlan()
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				var writes []admin.CollectionSetWrite
				_, err = plan.Save(ctx, session, formmodel.SetSaveOptions[models.Ticket]{
					Save: func(work context.Context, scope db.Session, row *formmodel.SetSaveRow[models.Ticket]) error {
						if row.Existing() {
							instance, present := bound.Set().Instance(row.Index())
							if !present {
								return errors.New("helpdesk: missing current collection instance")
							}
							form := instance.BoundForm()
							updated, err := a.updateTicketsInSession(work, session, actor, []ticketBulkChange{{index: row.Index(), id: row.Model().ID, form: &form}}, a.adminAudit)
							if err != nil {
								return err
							}
							if len(updated.records) != 1 {
								return errors.New("helpdesk: incomplete collection update")
							}
							*row.Model() = updated.records[0].Ticket
							writes = append(writes, admin.CollectionSetWrite{Index: row.Index(), Kind: "update", ID: row.Model().ID})
							return nil
						}
						labels, _ := row.Prepared().Collections().Integers("labels")
						created, err := a.createTicketsInSession(work, session, actor, []ticketBulkCandidate{{index: row.Index(), value: *row.Model(), labels: labels}}, a.adminAudit)
						if err != nil {
							return err
						}
						if len(created.records) != 1 {
							return errors.New("helpdesk: incomplete collection creation")
						}
						*row.Model() = created.records[0].Ticket
						writes = append(writes, admin.CollectionSetWrite{Index: row.Index(), Kind: "create", ID: row.Model().ID})
						return nil
					},
					Delete: func(work context.Context, scope db.Session, row formmodel.DeletedSetRow[models.Ticket]) error {
						if err := ticketWritePermission(work, actor, DeleteTicket); err != nil {
							return err
						}
						current, err := row.Model()
						if err != nil {
							return err
						}
						if current.CategoryID != a.categoryID {
							return admin.ErrObjectNotFound
						}
						if _, err = a.deleters.ModelsTicket.DeleteInSession(work, session, current); err != nil {
							if protected, ok := err.(*query.ProtectedForeignKeyError); ok && protected.ProtectedSourceRows() > 0 {
								return admin.RejectCollectionFormSet(nil, validation.NewErrors(validation.New(validation.NonField, "protected", validation.NewParam("index", strconv.Itoa(row.Index())))), protected)
							}
							return err
						}
						event, err := admin.PrepareEvent(actor.ID(), "helpdesk.ticket", current.ID, admin.ActionDelete, nil, current.Subject)
						if err != nil {
							return err
						}
						if err := a.adminAudit(work, session, event); err != nil {
							return err
						}
						writes = append(writes, admin.CollectionSetWrite{Index: row.Index(), Kind: "delete", ID: current.ID})
						return nil
					},
				})
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				return bound.Result(writes)
			})
			if err != nil {
				return admin.CollectionSetResult{}, ticketCollectionRejection(err)
			}
			return result, nil
		},
	})
	if err != nil {
		return admin.CollectionFormSet{}, err
	}
	return declaration.View(), nil
}

func (a *Application) ticketAdminCohort(ctx context.Context, reader db.Queryer) (admin.CollectionSetSnapshot[models.Ticket], error) {
	present, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Exists(ctx)
	if err != nil {
		return admin.CollectionSetSnapshot[models.Ticket]{}, err
	}
	if !present {
		return admin.CollectionSetSnapshot[models.Ticket]{}, admin.ErrObjectNotFound
	}
	query, err := models.TicketObjects.Using(reader).Filter(a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).OrderBy(models.TicketFields.ID.Asc()).Limit(ticketBulkMaximum + 1)
	if err != nil {
		return admin.CollectionSetSnapshot[models.Ticket]{}, err
	}
	current, err := query.All(ctx)
	if err != nil {
		return admin.CollectionSetSnapshot[models.Ticket]{}, err
	}
	if len(current) > ticketBulkMaximum {
		return admin.CollectionSetSnapshot[models.Ticket]{}, errors.New("helpdesk: collection cohort exceeds its allocation limit")
	}
	keys := make([]int64, len(current))
	members := make(map[int64][]int64, len(current))
	for index, row := range current {
		keys[index] = row.ID
		members[row.ID] = []int64{}
	}
	if len(keys) > 0 {
		links, err := models.TicketLabelObjects.Using(reader).Filter(a.relations.ModelsTicketLabel.Ticket.ID.In(keys...)).OrderBy(models.TicketLabelFields.ID.Asc()).Limit(admin.MaximumInputValues + 1)
		if err != nil {
			return admin.CollectionSetSnapshot[models.Ticket]{}, err
		}
		stored, err := links.All(ctx)
		if err != nil {
			return admin.CollectionSetSnapshot[models.Ticket]{}, err
		}
		if len(stored) > admin.MaximumInputValues {
			return admin.CollectionSetSnapshot[models.Ticket]{}, errors.New("helpdesk: collection memberships exceed their allocation limit")
		}
		for _, link := range stored {
			members[link.TicketID] = append(members[link.TicketID], link.LabelID)
		}
	}
	return admin.CollectionSetSnapshot[models.Ticket]{Current: current, Related: func(row models.Ticket, field ir.ManyToManyField) ([]int64, bool) {
		keys, present := members[row.ID]
		return slices.Clone(keys), present && field.Name == "labels"
	}}, nil
}

func ticketCollectionRejection(err error) error {
	failures, rejected := validation.Rejected(err)
	if !rejected {
		return err
	}
	rows := make(map[int]validation.Errors)
	var whole validation.Errors
	for _, failure := range failures.All() {
		if index, present := bulkFailureIndex(failure); present {
			rows[index] = validation.Join(rows[index], validation.NewErrors(failure))
		} else {
			whole = validation.Join(whole, validation.NewErrors(failure))
		}
	}
	return admin.RejectCollectionFormSet(rows, whole, err)
}
