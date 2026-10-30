package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func collectionSubmission[M any](t *testing.T, config CollectionFormSetConfig[M], actor auth.Principal, access CollectionSetAccess, data forms.Data) (ModelCollectionFormSet[M], CollectionSetSubmission) {
	t.Helper()
	stop := errors.New("capture admitted writer input")
	var submission CollectionSetSubmission
	config.Run = func(_ context.Context, _ auth.Principal, input CollectionSetSubmission) (CollectionSetResult, error) {
		submission = input
		return CollectionSetResult{}, stop
	}
	declaration, err := NewCollectionFormSet(config)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := declaration.View().bind(t.Context(), actor, access, &data)
	if err != nil || !bound.set.Valid() {
		t.Fatal("initial collection admission", err, bound.set.NonFormErrors())
	}
	if _, err := bound.run(t.Context()); err != stop || submission.owner == nil {
		t.Fatal("writer input was not issued", err)
	}
	return declaration, submission
}

func collectionCurrentFixture(t *testing.T) (CollectionFormSetConfig[models.Ticket], auth.Principal, CollectionSetSnapshot[models.Ticket], url.Values) {
	t.Helper()
	config, _ := collectionSetFixture(t)
	actor := sitePrincipal(t, "writer", true, config.Permissions.View, config.Permissions.Add, config.Permissions.Change, config.Permissions.Delete, "helpdesk.view_label")
	config.Form.Definition.Fields = []string{"subject", "labels"}
	config.Form.Definition.PostClean = formmodel.PostClean{}
	config.Set.MinForms, config.Set.ExtraForms, config.Set.CanDelete, config.Set.CanOrder = 0, 0, true, true
	first, second := models.NewTicketWithID(11), models.NewTicketWithID(12)
	first.Subject, second.Subject, first.CategoryID, second.CategoryID = "First", "Second", 7, 7
	snapshot := CollectionSetSnapshot[models.Ticket]{Current: []models.Ticket{first, second}, Related: func(models.Ticket, ir.ManyToManyField) ([]int64, bool) { return []int64{9}, true }}
	config.Load = func(context.Context, auth.Principal, CollectionSetAccess) (CollectionSetSnapshot[models.Ticket], error) {
		return snapshot, nil
	}
	input := url.Values{"tickets-TOTAL_FORMS": {"2"}, "tickets-INITIAL_FORMS": {"2"}, "tickets-0-id": {"11"}, "tickets-0-subject": {"First"}, "tickets-0-labels": {"9"}, "tickets-0-ORDER": {"1"}, "tickets-1-id": {"12"}, "tickets-1-subject": {"Second"}, "tickets-1-labels": {"9"}, "tickets-1-ORDER": {"2"}}
	return config, actor, snapshot, input
}

func TestCollectionSetCurrentUsesDeclaredPolicyFreshValuesAndRowIdentity(t *testing.T) {
	config, actor, snapshot, input := collectionCurrentFixture(t)
	loads, validations, cleans := 0, 0, 0
	initial := config.Load
	config.Load = func(ctx context.Context, actor auth.Principal, access CollectionSetAccess) (CollectionSetSnapshot[models.Ticket], error) {
		loads++
		return initial(ctx, actor, access)
	}
	config.Validate = func(context.Context, auth.Principal, formmodel.InstanceSet[models.Ticket]) error {
		validations++
		return nil
	}
	config.Form.Definition.Validators = []forms.CrossValidator{forms.CrossValidatorFunc(func(forms.Values) validation.Errors { cleans++; return validation.Errors{} })}
	input.Set("tickets-0-id", "12")
	input.Set("tickets-0-subject", "Second changed")
	input.Set("tickets-1-id", "11")
	input.Set("tickets-1-subject", "First changed")
	declaration, submission := collectionSubmission(t, config, actor, collectionTestAccess(actor), forms.NewData(input))
	config.Form.Definition.Fields[0] = "closed"
	snapshot.Current[0].CategoryID, snapshot.Current[1].CategoryID = 17, 18
	calls, resolves := 0, 0
	ctx := t.Context()
	bound, err := declaration.BindCurrent(ctx, actor, submission, func(work context.Context, access CollectionSetAccess) (CollectionSetCurrent[models.Ticket], error) {
		calls++
		if work != ctx || access != submission.Access() {
			t.Fatal("borrowed context or admission changed")
		}
		return CollectionSetCurrent[models.Ticket]{Rows: snapshot, ResolveChoices: func(work context.Context, preflight formmodel.SetPreflight[models.Ticket]) (forms.RowModelChoices, error) {
			resolves++
			for index, key := range []int64{12, 11} {
				row, present, err := preflight.Current(index)
				if err != nil || !present || row.ID != key {
					t.Fatal("selection used unsubmitted cohort order", err)
				}
			}
			return collectionTestChoices(work, preflight)
		}}, nil
	})
	if err != nil || !bound.Set().Valid() || calls != 1 || resolves != 1 || loads != 1 || validations != 1 || cleans != 4 {
		t.Fatal("current binding repeated display work or lost current state", err, calls, resolves, loads, validations, cleans)
	}
	snapshot.Current[1].CategoryID = 999
	prepared, err := bound.Set().Prepare()
	if err != nil {
		t.Fatal(err)
	}
	value, err := prepared.Rows()[0].Model()
	if err != nil || value.ID != 12 || value.CategoryID != 18 || value.Subject != "Second changed" {
		t.Fatal("fresh excluded value or detached source lost", err)
	}
	if _, err := bound.Result([]CollectionSetWrite{{0, "update", 12}, {1, "update", 11}}); err != nil {
		t.Fatal(err)
	}
	if _, err := bound.Result([]CollectionSetWrite{{0, "update", 11}, {1, "update", 12}}); !errors.Is(err, ErrReconciliationRequired) {
		t.Fatal("result used unsorted cohort identities", err)
	}
}

func TestCollectionSetCurrentAdmissionPrecedesAllCurrentIO(t *testing.T) {
	config, actor, snapshot, input := collectionCurrentFixture(t)
	declaration, submission := collectionSubmission(t, config, actor, collectionTestAccess(actor), forms.NewData(input))
	other, err := NewCollectionFormSet(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name       string
		ctx        context.Context
		actor      auth.Principal
		definition ModelCollectionFormSet[models.Ticket]
		input      CollectionSetSubmission
	}{
		{"zero_submission", t.Context(), actor, declaration, CollectionSetSubmission{}},
		{"other_declaration", t.Context(), actor, other, submission},
		{"zero_declaration", t.Context(), actor, ModelCollectionFormSet[models.Ticket]{}, submission},
		{"other_actor", t.Context(), sitePrincipal(t, "other", true, config.Permissions.View, config.Permissions.Add, config.Permissions.Change, config.Permissions.Delete, "helpdesk.view_label"), declaration, submission},
		{"revoked_operation", t.Context(), sitePrincipal(t, actor.ID(), true, config.Permissions.View, config.Permissions.Add, config.Permissions.Delete, "helpdesk.view_label"), declaration, submission},
		{"revoked_target", t.Context(), sitePrincipal(t, actor.ID(), true, config.Permissions.View, config.Permissions.Add, config.Permissions.Change, config.Permissions.Delete), declaration, submission},
		{"canceled", ctx, actor, declaration, submission},
		{"nil_context", nil, actor, declaration, submission},
	} {
		t.Run(test.name, func(t *testing.T) {
			loads := 0
			_, err := test.definition.BindCurrent(test.ctx, test.actor, test.input, func(context.Context, CollectionSetAccess) (CollectionSetCurrent[models.Ticket], error) {
				loads++
				return CollectionSetCurrent[models.Ticket]{Rows: snapshot, ResolveChoices: collectionTestChoices}, nil
			})
			if err == nil || loads != 0 {
				t.Fatal("invalid writer admission reached I/O", err, loads)
			}
		})
	}
	if _, err := declaration.BindCurrent(t.Context(), actor, submission, nil); err == nil {
		t.Fatal("nil loader accepted")
	}
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		failure := errors.New("private scope failure")
		_, err := declaration.BindCurrent(ctx, actor, submission, func(context.Context, CollectionSetAccess) (CollectionSetCurrent[models.Ticket], error) {
			if canceled {
				cancel()
			}
			return CollectionSetCurrent[models.Ticket]{}, failure
		})
		cancel()
		if !errors.Is(err, failure) || canceled && !errors.Is(err, context.Canceled) || !canceled && err != failure {
			t.Fatal("scope failure was lost", err)
		}
	}
}

func TestCollectionSetCurrentRejectsCohortBeforeChoices(t *testing.T) {
	for _, mode := range []string{"gained", "removed", "duplicate", "foreign", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			config, actor, snapshot, input := collectionCurrentFixture(t)
			// Deleted rows still participate in the complete source admission.
			input.Set("tickets-0-DELETE", "on")
			declaration, submission := collectionSubmission(t, config, actor, collectionTestAccess(actor), forms.NewData(input))
			fresh := snapshot
			fresh.Current = slices.Clone(snapshot.Current)
			switch mode {
			case "gained":
				row := models.NewTicketWithID(13)
				row.Subject, row.CategoryID = "Third", 7
				fresh.Current = append(fresh.Current, row)
			case "removed":
				fresh.Current = fresh.Current[:1]
			case "duplicate":
				fresh.Current[1] = fresh.Current[0]
			case "foreign":
				fresh.Current[1] = models.NewTicketWithID(13)
			case "overflow":
				fresh.Current = append(fresh.Current, fresh.Current...)
				fresh.Current = append(fresh.Current, snapshot.Current[0])
			}
			resolves := 0
			bound, err := declaration.BindCurrent(t.Context(), actor, submission, func(context.Context, CollectionSetAccess) (CollectionSetCurrent[models.Ticket], error) {
				return CollectionSetCurrent[models.Ticket]{Rows: fresh, ResolveChoices: func(work context.Context, input formmodel.SetPreflight[models.Ticket]) (forms.RowModelChoices, error) {
					resolves++
					return collectionTestChoices(work, input)
				}}, nil
			})
			if err == nil && bound.Set().Valid() || resolves != 0 {
				t.Fatal("changed cohort reached choices/writes", err, resolves)
			}
		})
	}
}

func TestCollectionSetCurrentRevisionCoversReadOnlyAndDeletedRows(t *testing.T) {
	schema := models.GoDjRelationSchema()
	for index := range schema.Models {
		if schema.Models[index].Name == "service_report" {
			schema.Models[index] = revisionReportDescriptor{}.Metadata()
		}
	}
	project, err := orm.BindProject(schema)
	if err != nil {
		t.Fatal(err)
	}
	policy := forms.DefaultSetConfig()
	policy.Prefix, policy.ExtraForms, policy.MaxForms, policy.AbsoluteMax = "reports", 0, 2, 2
	policy.CanDelete, policy.ValidateMin, policy.ValidateMax = true, true, true
	permissions := Permissions{View: "helpdesk.view_servicereport", Add: "helpdesk.add_servicereport", Change: "helpdesk.change_servicereport", Delete: "helpdesk.delete_servicereport"}
	actor := sitePrincipal(t, "writer", true, permissions.View, permissions.Change, permissions.Delete)
	row := models.NewServiceReportWithID(11)
	row.TicketID, row.Summary, row.Completed = 7, "Before", false
	config := CollectionFormSetConfig[models.ServiceReport]{Name: "edit", Label: "Reports", Project: project, Source: ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "service_report"}, Manager: orm.NewManager[models.ServiceReport](revisionReportDescriptor{}), Permissions: permissions, Set: policy, RevisionField: "completed", Form: FormConfig{Definition: formmodel.Definition{Fields: []string{"summary"}}}, Load: func(context.Context, auth.Principal, CollectionSetAccess) (CollectionSetSnapshot[models.ServiceReport], error) {
		return CollectionSetSnapshot[models.ServiceReport]{Current: []models.ServiceReport{row}}, nil
	}}
	for _, readonly := range []bool{false, true} {
		for _, deleted := range []bool{false, true} {
			t.Run(fmt.Sprintf("readonly_%t_deleted_%t", readonly, deleted), func(t *testing.T) {
				data := url.Values{"reports-TOTAL_FORMS": {"1"}, "reports-INITIAL_FORMS": {"1"}, "reports-0-id": {"11"}, "reports-0-summary": {"Changed"}, "reports-0-expected_revision": {"1"}}
				if deleted {
					data.Set("reports-0-DELETE", "on")
				}
				declaration, submission := collectionSubmission(t, config, actor, CollectionSetAccess{View: true, Change: !readonly, Delete: true}, forms.NewData(data))
				fresh := row
				fresh.Completed = true
				resolves := 0
				_, err := declaration.BindCurrent(t.Context(), actor, submission, func(context.Context, CollectionSetAccess) (CollectionSetCurrent[models.ServiceReport], error) {
					return CollectionSetCurrent[models.ServiceReport]{Rows: CollectionSetSnapshot[models.ServiceReport]{Current: []models.ServiceReport{fresh}}, ResolveChoices: func(context.Context, formmodel.SetPreflight[models.ServiceReport]) (forms.RowModelChoices, error) {
						resolves++
						return forms.RowModelChoices{}, nil
					}}, nil
				})
				var operation *OperationError
				if !errors.As(err, &operation) || operation.Code != OperationConflict || resolves != 0 {
					t.Fatal("stale revision reached choices or preparation", err, resolves)
				}
			})
		}
	}
}

func TestCollectionSetCurrentReadOnlyAndExactWriteIntents(t *testing.T) {
	for _, mode := range []string{"noop", "delete", "order", "collection", "readonly_new"} {
		t.Run(mode, func(t *testing.T) {
			config, actor, snapshot, input := collectionCurrentFixture(t)
			access := collectionTestAccess(actor)
			want := []CollectionSetWrite{}
			switch mode {
			case "delete":
				input.Set("tickets-0-DELETE", "on")
				want = []CollectionSetWrite{{0, "delete", 11}}
			case "order":
				input.Set("tickets-0-ORDER", "2")
				input.Set("tickets-1-ORDER", "1")
				want = []CollectionSetWrite{{0, "update", 11}, {1, "update", 12}}
			case "collection":
				input.Del("tickets-0-labels")
				want = []CollectionSetWrite{{0, "update", 11}}
			case "readonly_new":
				access.Change = false
				config.Form.Definition.PostClean = formmodel.PostClean{Fields: []string{"category"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
					return forms.NewValues(map[string]forms.Value{"category": forms.Integer(7)}), validation.Errors{}
				}}
				input.Del("tickets-0-ORDER")
				input.Del("tickets-1-ORDER")
				input.Set("tickets-0-subject", "forged")
				input.Set("tickets-TOTAL_FORMS", "3")
				input.Set("tickets-2-subject", "New")
				input.Set("tickets-2-labels", "9")
				want = []CollectionSetWrite{{2, "create", 21}}
			}
			declaration, submission := collectionSubmission(t, config, actor, access, forms.NewData(input))
			bound, err := declaration.BindCurrent(t.Context(), actor, submission, func(context.Context, CollectionSetAccess) (CollectionSetCurrent[models.Ticket], error) {
				return CollectionSetCurrent[models.Ticket]{Rows: snapshot, ResolveChoices: collectionTestChoices}, nil
			})
			if err != nil || !bound.Set().Valid() {
				t.Fatal("current operation binding", err)
			}
			if mode == "readonly_new" {
				row := bound.Set().FormSet().Forms()[0].Form()
				value, _ := row.Cleaned().String("subject")
				if !row.ReadOnly() || value != "First" || bound.Access().Change {
					t.Fatal("readonly overlay widened")
				}
			}
			result, err := bound.Result(want)
			if err != nil || !slices.Equal(result.Writes(), want) {
				t.Fatal("exact write intents", err, result.Writes())
			}
			if mode == "order" {
				ordered, err := bound.Set().FormSet().OrderedForms()
				if err != nil || len(ordered) != 2 || ordered[0].Index() != 1 {
					t.Fatal("order lost", err)
				}
			}
			if _, err := bound.Result(append(slices.Clone(want), CollectionSetWrite{Index: 3, Kind: "create", ID: 88})); !errors.Is(err, ErrReconciliationRequired) {
				t.Fatal("extra operation accepted", err)
			}
			if mode == "readonly_new" {
				if _, err := bound.Result([]CollectionSetWrite{{2, "create", 11}}); !errors.Is(err, ErrReconciliationRequired) {
					t.Fatal("created ID collided with surviving readonly row", err)
				}
			}
		})
	}
}

func TestCollectionSetRunRejectsReplayedResultAndPreservesTerminalError(t *testing.T) {
	config, actor := collectionSetFixture(t)
	var previous CollectionSetResult
	terminal := errors.New("commit outcome unknown")
	for _, mode := range []string{"replay", "terminal"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			definition := collectionTestDeclaration(t, config, func(bound BoundCollectionFormSet[models.Ticket]) (CollectionSetResult, error) {
				calls++
				if calls > 1 {
					return previous, nil
				}
				var err error
				previous, err = bound.Result([]CollectionSetWrite{{0, "create", 21}, {1, "create", 22}})
				if err != nil {
					return CollectionSetResult{}, err
				}
				if mode == "terminal" {
					return previous, terminal
				}
				return previous, nil
			})
			data := collectionSetData()
			bound := collectionTestBind(t, definition, actor, &data)
			result, err := bound.run(t.Context())
			if mode == "terminal" {
				if err != terminal || len(result.Writes()) != 0 {
					t.Fatal("terminal error became success", err)
				}
				return
			}
			if err != nil || len(result.Writes()) != 2 {
				t.Fatal(err)
			}
			result, err = bound.run(t.Context())
			if !errors.Is(err, ErrReconciliationRequired) || len(result.Writes()) != 0 {
				t.Fatal("previous Run result replayed", err)
			}
		})
	}
}

func TestCollectionSetOperationControlsPrecedeChoiceIO(t *testing.T) {
	for _, mode := range []string{"denied_delete", "denied_order", "policy_delete", "policy_order"} {
		t.Run(mode, func(t *testing.T) {
			config, actor, _, input := collectionCurrentFixture(t)
			access := collectionTestAccess(actor)
			switch mode {
			case "denied_delete":
				access.Delete = false
				input.Set("tickets-0-DELETE", "on")
			case "denied_order":
				access.Change = false
			case "policy_delete":
				config.Set.CanDelete = false
				input.Set("tickets-0-DELETE", "on")
			case "policy_order":
				config.Set.CanOrder = false
			}
			reads, writes := 0, 0
			config.Form.RelatedChoices[0] = testChoicesWithRead(config.Form.RelatedChoices[0], func(context.Context, auth.Principal) ([]forms.Choice, error) {
				reads++
				return []forms.Choice{{Value: forms.Integer(9), Label: "Private label"}}, nil
			})
			config.Run = func(context.Context, auth.Principal, CollectionSetSubmission) (CollectionSetResult, error) {
				writes++
				return CollectionSetResult{}, nil
			}
			definition, err := NewCollectionFormSet(config)
			if err != nil {
				t.Fatal(err)
			}
			data := forms.NewData(input)
			_, err = definition.View().bind(t.Context(), actor, access, &data)
			var operation *OperationError
			if !errors.As(err, &operation) || operation.Code != OperationDenied || reads != 0 || writes != 0 {
				t.Fatal("forbidden control reached choices or writer", err, reads, writes)
			}
		})
	}
}

func TestCollectionSetChoiceOriginsAndCurrentSelectionsStayPerRow(t *testing.T) {
	config, actor, snapshot, input := collectionCurrentFixture(t)
	snapshot.Related = func(row models.Ticket, _ ir.ManyToManyField) ([]int64, bool) { return []int64{row.ID - 2}, true }
	config.Load = func(context.Context, auth.Principal, CollectionSetAccess) (CollectionSetSnapshot[models.Ticket], error) {
		return snapshot, nil
	}
	choiceConfig := config.Form.RelatedChoices[0].(relatedChoices[forms.Choice]).config
	seen := make(map[int64]int)
	choiceConfig.Scope = func(_ context.Context, principal auth.Principal, origin ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[forms.Choice]) error) error {
		key, existing := origin.ObjectID()
		rowKey, rowExisting := origin.RowIdentity()
		_, _, parent := origin.Parent()
		if principal.ID() != actor.ID() || origin.Source() != config.Source || origin.Operation() != ChoiceCollectionSet || origin.Name() != config.Name || key != rowKey || existing != rowExisting || parent || key != 0 && key != 11 && key != 12 {
			t.Fatal("choice scope inherited another row or a fabricated parent")
		}
		seen[key]++
		selected := key - 2
		if key == 0 {
			selected = 21
		}
		return visit(testChoiceQueryer{actor: principal, read: func(context.Context, auth.Principal) ([]forms.Choice, error) {
			return []forms.Choice{{Value: forms.Integer(selected), Label: fmt.Sprintf("Only for row %d", key)}}, nil
		}}, nil)
	}
	var err error
	config.Form.RelatedChoices[0], err = NewRelatedChoices(choiceConfig)
	if err != nil {
		t.Fatal(err)
	}
	// Reorder identities: a submitted index must not reuse the server's old
	// index or its neighbour's selected-key authority.
	input.Set("tickets-0-id", "12")
	input.Set("tickets-0-subject", "Second changed")
	input.Set("tickets-0-labels", "10")
	input.Set("tickets-1-id", "11")
	input.Set("tickets-1-subject", "First changed")
	declaration, submission := collectionSubmission(t, config, actor, collectionTestAccess(actor), forms.NewData(input))
	if seen[11] == 0 || seen[12] == 0 {
		t.Fatal("row-specific resolver was not exercised", seen)
	}
	bound, err := declaration.BindCurrent(t.Context(), actor, submission, func(context.Context, CollectionSetAccess) (CollectionSetCurrent[models.Ticket], error) {
		return CollectionSetCurrent[models.Ticket]{Rows: snapshot, ResolveChoices: func(context.Context, formmodel.SetPreflight[models.Ticket]) (forms.RowModelChoices, error) {
			// A valid prototype is deliberately unable to fill a missing existing
			// row. The first row remains valid; the second has no authority.
			return forms.NewRowModelChoices(map[int]map[string][]forms.Choice{0: {"labels": {{Value: forms.Integer(10), Label: "Current second"}}}}, map[string][]forms.Choice{"labels": {{Value: forms.Integer(9), Label: "Wrong prototype authority"}}}), nil
		}}, nil
	})
	if err != nil || bound.Set().Valid() {
		t.Fatal("missing current row inherited prototype choices", err)
	}
	rows := bound.Set().FormSet().Forms()
	if !rows[0].Form().Valid() || rows[1].Form().Errors().ByField("labels").Empty() {
		t.Fatal("row authority leaked between rows")
	}
	wrong := input
	wrong.Set("tickets-0-labels", "9")
	data := forms.NewData(wrong)
	initial, err := declaration.View().bind(t.Context(), actor, collectionTestAccess(actor), &data)
	if err != nil || initial.set.Valid() || initial.set.Forms()[0].Form().Errors().ByField("labels").Empty() {
		t.Fatal("other row selection became valid", err)
	}
	for _, test := range []struct {
		key     int64
		allowed bool
	}{{0, true}, {11, true}, {12, true}, {13, false}} {
		_, err := declaration.View().searchOrigin(t.Context(), actor, collectionTestAccess(actor), test.key)
		if (err == nil) != test.allowed {
			t.Fatal("choice search cohort admission", test.key, err)
		}
	}
	for _, access := range []CollectionSetAccess{{View: true, Change: true}, {View: true, Add: true}} {
		key := int64(0)
		if !access.Change {
			key = 11
		}
		if _, err := declaration.View().searchOrigin(t.Context(), actor, access, key); err == nil {
			t.Fatal("search widened admitted operation")
		}
	}
}
