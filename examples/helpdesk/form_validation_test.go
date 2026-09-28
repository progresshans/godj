package helpdesk

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

type modelValidationSnapshot struct {
	*sqlite.Backend
	mode                        string
	finish                      error
	scopes, tupleQueries        int
	labelQueries, reportQueries int
}

func (backend *modelValidationSnapshot) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	backend.scopes++
	if backend.mode == "missing_callback" {
		return nil
	}
	if backend.mode == "nil_reader" {
		return callback(nil)
	}
	if backend.mode == "typed_nil_reader" {
		var reader *modelValidationReader
		return callback(reader)
	}
	err := backend.Backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		wrapped := modelValidationReader{Queryer: reader, owner: backend}
		if err := callback(wrapped); err != nil {
			return err
		}
		if backend.mode == "double_callback" {
			return callback(wrapped)
		}
		return nil
	})
	return errors.Join(err, backend.finish)
}

type modelValidationReader struct {
	db.Queryer
	owner *modelValidationSnapshot
}

func (reader modelValidationReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if plan.Table() == "helpdesk_ticket_label" && plan.ResultShape().Kind() == query.ResultProjection {
		reader.owner.tupleQueries++
	}
	if plan.Table() == "helpdesk_label" && plan.ResultShape().Kind() == query.ResultProjection {
		reader.owner.labelQueries++
	}
	if plan.Table() == "helpdesk_service_report" && plan.ResultShape().Kind() == query.ResultProjection {
		reader.owner.reportQueries++
	}
	return reader.Queryer.Query(ctx, plan)
}

func TestHelpdeskModelValidationReadScope(t *testing.T) {
	for _, mode := range []string{"duplicate", "same_row", "stale_choices", "cleanup_failure", "missing_callback", "double_callback", "nil_reader", "typed_nil_reader", "denied", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "scope.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := backend.Close(); err != nil {
					t.Error(err)
				}
			})
			loaded, _, err := definition.Load(MigrationSources()...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			category, err := models.CategoryObjects.Create(t.Context(), backend, models.NewCategoryCreate("Owned"))
			if err != nil {
				t.Fatal(err)
			}
			other, err := models.CategoryObjects.Create(t.Context(), backend, models.NewCategoryCreate("Other"))
			if err != nil {
				t.Fatal(err)
			}
			ownerID := category.ID
			if mode == "stale_choices" {
				ownerID = other.ID
			}
			ticket, err := models.TicketObjects.Create(t.Context(), backend, models.NewTicketCreate("Known ticket", ownerID))
			if err != nil {
				t.Fatal(err)
			}
			label, err := models.LabelObjects.Create(t.Context(), backend, models.NewLabelCreate("Known label", ownerID))
			if err != nil {
				t.Fatal(err)
			}
			link, err := models.TicketLabelObjects.Create(t.Context(), backend, models.NewTicketLabelCreate(ticket.ID, label.ID))
			if err != nil {
				t.Fatal(err)
			}
			wrapper := &modelValidationSnapshot{Backend: backend, mode: mode}
			secret := errors.New("private-snapshot-cleanup-failure")
			if mode == "cleanup_failure" {
				wrapper.finish = secret
			}
			app, err := New(wrapper, category.ID)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := formmodel.NewSpec(models.TicketLabelDescriptor{}.Metadata())
			if err != nil {
				t.Fatal(err)
			}
			spec, err = spec.WithModelChoices("ticket", forms.Choice{Value: forms.Integer(ticket.ID), Label: "Previously visible"})
			if err != nil {
				t.Fatal(err)
			}
			spec, err = spec.WithModelChoices("label", forms.Choice{Value: forms.Integer(label.ID), Label: "Previously visible"})
			if err != nil {
				t.Fatal(err)
			}
			bound, err := formmodel.Bind(models.TicketLabelDescriptor{}.Metadata(), spec, forms.NewData(map[string][]string{"ticket": {strconv.FormatInt(ticket.ID, 10)}, "label": {strconv.FormatInt(label.ID, 10)}}), nil, formmodel.PostClean{})
			if err != nil || !bound.Form().Valid() {
				t.Fatal("valid prior choice projection", err)
			}
			permissions := []auth.Permission{AddTicketLabel, ChangeTicketLabel, ViewTicket, ViewLabel}
			if mode == "denied" {
				permissions = []auth.Permission{AddTicketLabel, ViewTicket}
			}
			actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "editor", Active: true, Permissions: permissions})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			id := int64(0)
			if mode == "same_row" {
				id = link.ID
			}
			err = app.checkTicketLabelForm(ctx, actor, id, bound)
			failures, rejected := validation.Rejected(err)
			switch mode {
			case "duplicate":
				if !rejected || failures.ByField(validation.NonField).Len() != 1 || wrapper.tupleQueries != 1 {
					t.Fatal("owned tuple duplicate absent", err)
				}
			case "same_row":
				if err != nil || wrapper.tupleQueries != 1 {
					t.Fatal("same row conflicted", err)
				}
			case "stale_choices":
				if !rejected || failures.ByField("ticket").Len() != 1 || failures.ByField("label").Len() != 1 || wrapper.tupleQueries != 0 {
					t.Fatal("stale endpoint choices exposed a foreign tuple", err)
				}
			case "cleanup_failure":
				if !errors.Is(err, secret) || rejected {
					t.Fatal("cleanup failure published input diagnostics", err)
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) || wrapper.scopes != 0 {
					t.Fatal("canceled request entered scope", err)
				}
			case "denied":
				if err == nil || rejected || wrapper.scopes != 0 {
					t.Fatal("partial admission entered scope", err)
				}
			default:
				if err == nil || rejected {
					t.Fatal("invalid read scope contract accepted", err)
				}
			}
			if count, err := models.TicketLabelObjects.Using(backend).Count(t.Context()); err != nil || count != 1 {
				t.Fatal("read validation wrote link data", err)
			}
		})
	}
}

type modelFormScopeFixture struct {
	backend        *sqlite.Backend
	scope          *modelValidationSnapshot
	app            *Application
	owned, foreign int64
	actor          auth.Principal
}

func newModelFormScopeFixture(t *testing.T) modelFormScopeFixture {
	t.Helper()
	backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "model-forms.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	loaded, _, err := definition.Load(MigrationSources()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	owned, err := models.CategoryObjects.Create(t.Context(), backend, models.NewCategoryCreate("Owned"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := models.CategoryObjects.Create(t.Context(), backend, models.NewCategoryCreate("Foreign"))
	if err != nil {
		t.Fatal(err)
	}
	scope := &modelValidationSnapshot{Backend: backend}
	app, err := New(scope, owned.ID)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "checker", Active: true, Superuser: true})
	if err != nil {
		t.Fatal(err)
	}
	return modelFormScopeFixture{backend: backend, scope: scope, app: app, owned: owned.ID, foreign: foreign.ID, actor: actor}
}

func TestHelpdeskLabelModelValidationUsesServerCategory(t *testing.T) {
	for _, mode := range []string{"duplicate", "other_category_only", "invalid_name", "same_row", "foreign_current", "cleanup_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newModelFormScopeFixture(t)
			foreign, err := models.LabelObjects.Create(t.Context(), f.backend, models.NewLabelCreate("Shared", f.foreign))
			if err != nil {
				t.Fatal(err)
			}
			var owned models.Label
			if mode != "other_category_only" {
				owned, err = models.LabelObjects.Create(t.Context(), f.backend, models.NewLabelCreate("Shared", f.owned))
				if err != nil {
					t.Fatal(err)
				}
			}
			name := "Shared"
			if mode == "invalid_name" {
				name = ""
			}
			// An excluded initial value cannot replace the application's scope.
			bound, err := (formmodel.Definition{Fields: []string{"name"}}).Bind(models.LabelDescriptor{}.Metadata(), forms.NewData(map[string][]string{"name": {name}}), map[string]forms.Value{"category": forms.Integer(f.foreign)})
			if err != nil {
				t.Fatal(err)
			}
			before, err := models.LabelObjects.Using(f.backend).OrderBy(models.LabelFields.ID.Asc()).All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			secret := errors.New("private-label-snapshot-failure")
			if mode == "cleanup_failure" {
				f.scope.finish = secret
			}
			id := int64(0)
			if mode == "same_row" {
				id = owned.ID
			}
			if mode == "foreign_current" {
				id = foreign.ID
			}
			err = f.app.checkLabelForm(t.Context(), f.actor, id, bound)
			failures, rejected := validation.Rejected(err)
			switch mode {
			case "duplicate":
				if !rejected || failures.Len() != 1 || failures.ByField(validation.NonField).Len() != 1 || f.scope.labelQueries != 1 {
					t.Fatal("server category did not own the label tuple check", err)
				}
			case "invalid_name":
				if err != nil || f.scope.labelQueries != 0 || bound.Form().Valid() {
					t.Fatal("invalid label name entered tuple validation", err)
				}
			case "other_category_only", "same_row":
				if err != nil || f.scope.labelQueries != 1 {
					t.Fatal("foreign category or current row conflicted", err)
				}
			case "foreign_current":
				if err != admin.ErrObjectNotFound || f.scope.labelQueries != 0 {
					t.Fatal("foreign label row reached uniqueness", err)
				}
			case "cleanup_failure":
				if !errors.Is(err, secret) || rejected {
					t.Fatal("label cleanup failure published tuple diagnostics", err)
				}
			}
			if bound.Form().Valid() {
				input, err := bound.Input()
				if err != nil {
					t.Fatal(err)
				}
				if _, present := input.Get("category"); present {
					t.Fatal("server category entered editable input")
				}
			}
			after, err := models.LabelObjects.Using(f.backend).OrderBy(models.LabelFields.ID.Asc()).All(t.Context())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("label check changed storage", err)
			}
		})
	}
}

func TestHelpdeskReportModelValidationRechecksTicketBeforeUniqueness(t *testing.T) {
	for _, mode := range []string{"duplicate", "field_error", "same_row", "stale_choice", "foreign_current", "cleanup_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newModelFormScopeFixture(t)
			owned, err := models.TicketObjects.Create(t.Context(), f.backend, models.NewTicketCreate("Owned", f.owned))
			if err != nil {
				t.Fatal(err)
			}
			foreign, err := models.TicketObjects.Create(t.Context(), f.backend, models.NewTicketCreate("Foreign", f.foreign))
			if err != nil {
				t.Fatal(err)
			}
			ownedReport, err := models.ServiceReportObjects.Create(t.Context(), f.backend, models.NewServiceReportCreate(owned.ID, "Owned report"))
			if err != nil {
				t.Fatal(err)
			}
			foreignReport, err := models.ServiceReportObjects.Create(t.Context(), f.backend, models.NewServiceReportCreate(foreign.ID, "Private report"))
			if err != nil {
				t.Fatal(err)
			}
			selected := owned.ID
			if mode == "stale_choice" {
				selected = foreign.ID
			}
			spec, err := formmodel.NewSpecForFields(models.ServiceReportDescriptor{}.Metadata(), []string{"ticket", "summary", "completed"})
			if err != nil {
				t.Fatal(err)
			}
			spec, err = spec.WithModelChoices("ticket", forms.Choice{Value: forms.Integer(selected), Label: "Previously visible"})
			if err != nil {
				t.Fatal(err)
			}
			summary := "Candidate"
			if mode == "field_error" {
				summary = ""
			}
			bound, err := formmodel.Bind(models.ServiceReportDescriptor{}.Metadata(), spec, forms.NewData(map[string][]string{"ticket": {strconv.FormatInt(selected, 10)}, "summary": {summary}}), nil, formmodel.PostClean{})
			if err != nil {
				t.Fatal(err)
			}
			before, err := models.ServiceReportObjects.Using(f.backend).OrderBy(models.ServiceReportFields.ID.Asc()).All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			secret := errors.New("private-report-snapshot-failure")
			if mode == "cleanup_failure" {
				f.scope.finish = secret
			}
			id := int64(0)
			if mode == "same_row" {
				id = ownedReport.ID
			}
			if mode == "foreign_current" {
				id = foreignReport.ID
			}
			err = f.app.checkReportForm(t.Context(), f.actor, id, bound)
			failures, rejected := validation.Rejected(err)
			switch mode {
			case "duplicate", "field_error":
				if !rejected || failures.Len() != 1 || failures.ByField("ticket").Len() != 1 || failures.ByField("ticket").All()[0].Code() != validation.CodeUnique || f.scope.reportQueries != 1 {
					t.Fatal("valid ticket uniqueness was suppressed", err)
				}
				checked, err := bound.WithErrors(failures)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "field_error" && checked.Form().Errors().Len() != 2 {
					t.Fatal("report field and model errors did not combine")
				}
			case "same_row":
				if err != nil || f.scope.reportQueries != 1 {
					t.Fatal("current report conflicted with itself", err)
				}
			case "stale_choice":
				if !rejected || failures.Len() != 1 || failures.ByField("ticket").Len() != 1 || failures.ByField("ticket").All()[0].Code() != "invalid_choice" || f.scope.reportQueries != 0 {
					t.Fatal("stale ticket exposed a foreign report", err)
				}
			case "foreign_current":
				if err != admin.ErrObjectNotFound || f.scope.reportQueries != 0 {
					t.Fatal("foreign report row reached uniqueness", err)
				}
			case "cleanup_failure":
				if !errors.Is(err, secret) || rejected {
					t.Fatal("report cleanup failure published diagnostics", err)
				}
			}
			after, err := models.ServiceReportObjects.Using(f.backend).OrderBy(models.ServiceReportFields.ID.Asc()).All(t.Context())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("report check changed storage", err)
			}
		})
	}
}
