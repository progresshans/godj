package helpdesk

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

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
	mode                 string
	finish               error
	scopes, tupleQueries int
}

func (backend *modelValidationSnapshot) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	backend.scopes++
	if backend.mode == "missing_callback" {
		return nil
	}
	if backend.mode == "nil_reader" {
		return callback(nil)
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
	return reader.Queryer.Query(ctx, plan)
}

func TestHelpdeskModelValidationReadScope(t *testing.T) {
	for _, mode := range []string{"duplicate", "same_row", "stale_choices", "cleanup_failure", "missing_callback", "double_callback", "nil_reader", "denied", "canceled"} {
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
			bound, err := formmodel.Bind(models.TicketLabelDescriptor{}.Metadata(), spec, forms.NewData(map[string][]string{"ticket": {strconv.FormatInt(ticket.ID, 10)}, "label": {strconv.FormatInt(label.ID, 10)}}), nil)
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
