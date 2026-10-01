package helpdesk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

type formSaveDatabase interface {
	Backend
	migrationbackend.RevisionFencedBackend
	Close() error
}

func TestHelpdeskTypedFormPersistence(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		b, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "form.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		verifyTypedTicketForm(t, b)
	})
	dsn := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if dsn == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), dsn)
		if err != nil {
			t.Fatal("connect typed form PostgreSQL")
		}
		name := fmt.Sprintf("godj_helpdesk_typed_form_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{name}.Sanitize()
		if _, err := connection.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
			_ = connection.Close(context.Background())
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				t.Error(err)
			}
			if err := connection.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		b, err := postgres.Open(t.Context(), postgres.Config{URL: dsn, Schema: name})
		if err != nil {
			t.Fatal("open typed form PostgreSQL")
		}
		verifyTypedTicketForm(t, b)
		t.Run("binary_concurrency", func(t *testing.T) { verifyPayloadDigestPostgresConcurrency(t, b, connection, name) })
	})
}

func verifyTypedTicketForm(t *testing.T, b formSaveDatabase) {
	t.Helper()
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	loaded, _, err := definition.Load(MigrationSources()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: b}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	category, err := models.CategoryObjects.Create(t.Context(), b, models.NewCategoryCreate("Owned"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := models.CategoryObjects.Create(t.Context(), b, models.NewCategoryCreate("Other"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := models.LabelObjects.Create(t.Context(), b, models.NewLabelCreate("First", category.ID))
	if err != nil {
		t.Fatal(err)
	}
	second, err := models.LabelObjects.Create(t.Context(), b, models.NewLabelCreate("Second", category.ID))
	if err != nil {
		t.Fatal(err)
	}
	traced := &formSaveTracedBackend{formSaveDatabase: b}
	app, err := New(traced, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "editor", Active: true, Permissions: []auth.Permission{AddTicket, ChangeTicket, ViewLabel}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := models.TicketDescriptor{}.Metadata()
	for _, mode := range []string{"new_clean_excluded", "change_clean_excluded", "collection_only", "excluded_collection", "clear_collection", "category_rejected", "new_category_rejected", "late_scope_change", "narrowed_initial_corrected", "narrowed_initial_invalid"} {
		t.Run(mode, func(t *testing.T) {
			creating := strings.HasPrefix(mode, "new_")
			current := models.Ticket{}
			if !creating {
				current, err = models.TicketObjects.Create(t.Context(), b, models.NewTicketCreate("old-"+mode, category.ID).WithResolution("keep"))
				if err != nil {
					t.Fatal(err)
				}
				collection, err := app.collections.ModelsTicketLabels.From(b, current)
				if err != nil {
					t.Fatal(err)
				}
				if err := collection.SetKeys(t.Context(), []int64{first.ID}); err != nil {
					t.Fatal(err)
				}
			}
			fields := []string{"subject", "labels"}
			if mode == "excluded_collection" {
				fields = []string{"subject"}
			}
			definition := formmodel.Definition{Fields: fields}
			if strings.HasPrefix(mode, "narrowed_initial_") {
				definition.Overrides = []formmodel.Override{formmodel.OverrideField("subject", formmodel.WithMaxLength(4))}
			}
			calls := 0
			if strings.Contains(mode, "clean_excluded") || strings.Contains(mode, "category_rejected") {
				definition.PostClean = formmodel.PostClean{Fields: []string{"resolution", "category"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
					calls++
					changes := map[string]forms.Value{"resolution": forms.String("derived")}
					if strings.Contains(mode, "category_rejected") {
						changes["category"] = forms.Integer(other.ID)
					}
					return forms.NewValues(changes), validation.Errors{}
				}}
			}
			spec, err := definition.Spec(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "excluded_collection" {
				spec, err = spec.WithModelChoices("labels", forms.Choice{Value: forms.Integer(first.ID), Label: "First"}, forms.Choice{Value: forms.Integer(second.ID), Label: "Second"})
				if err != nil {
					t.Fatal(err)
				}
			}
			subject := "saved-" + mode
			if mode == "narrowed_initial_corrected" {
				subject = "new"
			}
			if mode == "narrowed_initial_invalid" {
				subject = current.Subject
			}
			if mode == "collection_only" || mode == "clear_collection" {
				subject = current.Subject
			}
			data := map[string][]string{"subject": {subject}, "labels": {strconv.FormatInt(second.ID, 10)}}
			if mode == "clear_collection" {
				data["labels"] = nil
			}
			initial := map[string]forms.Value{"category": forms.Integer(category.ID)}
			if strings.HasPrefix(mode, "narrowed_initial_") {
				initial["subject"] = forms.String(current.Subject)
			}
			if !creating {
				initial["id"] = forms.Integer(current.ID)
				initial["resolution"] = forms.String("keep")
			}
			bound, err := formmodel.Bind(t.Context(), metadata, spec, forms.NewData(data), initial, definition.PostClean)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "narrowed_initial_invalid" && (bound.Form().Valid() || bound.Form().Errors().ByField("subject").Empty()) {
				t.Fatal("narrowed form accepted an unchanged invalid subject")
			}
			if mode == "late_scope_change" {
				moved := current
				moved.CategoryID = other.ID
				if err := models.TicketObjects.Save(t.Context(), b, &moved); err != nil {
					t.Fatal(err)
				}
			}
			beforeCount, err := models.TicketObjects.Using(b).Count(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			traced.ticketUpdates = nil
			saved, changed, err := app.saveTicketForm(t.Context(), actor, current.ID, bound, admin.InlineSubmission{})
			rejected := strings.Contains(mode, "category_rejected") || mode == "late_scope_change" || mode == "narrowed_initial_invalid"
			if rejected {
				if err == nil || saved.ID != 0 || len(changed) != 0 {
					t.Fatal("rejected form was writable", err, changed)
				}
				count, err := models.TicketObjects.Using(b).Count(t.Context())
				if err != nil || count != beforeCount {
					t.Fatal("rejected form inserted data", err)
				}
				if !creating {
					fresh, present, err := ticket(t.Context(), b, current.ID)
					if err != nil || !present || fresh.Subject != current.Subject || fresh.Resolution == nil || *fresh.Resolution != "keep" {
						t.Fatal("rejected form changed current scalar", err)
					}
					if mode == "narrowed_initial_invalid" {
						collection, err := app.collections.ModelsTicketLabels.From(b, current)
						if err != nil {
							t.Fatal(err)
						}
						labels, err := collection.All(t.Context())
						if err != nil || len(labels) != 1 || labels[0].ID != first.ID || len(traced.ticketUpdates) != 0 {
							t.Fatal("invalid input changed scalar or collection storage", err)
						}
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				expected := []int64{second.ID}
				if mode == "excluded_collection" {
					expected = []int64{first.ID}
				}
				if mode == "clear_collection" {
					expected = []int64{}
				}
				if saved.Subject != subject || saved.CategoryID != category.ID || !reflect.DeepEqual(saved.labels, expected) {
					t.Fatal("typed form lost scalar, category or selection", saved.Subject, saved.labels)
				}
				if strings.Contains(mode, "clean_excluded") && (saved.Resolution == nil || *saved.Resolution != "derived") {
					t.Fatal("excluded clean output did not reach storage")
				}
				if mode == "collection_only" || mode == "clear_collection" {
					if !reflect.DeepEqual(changed, []string{"labels"}) {
						t.Fatal("relation-only form changed scalar audit", changed)
					}
				}
				if !creating && current.Subject == saved.Subject && mode != "collection_only" && mode != "clear_collection" {
					t.Fatal("form did not update subject")
				}
			}
			for _, fields := range traced.ticketUpdates {
				for _, field := range fields {
					if field == "category" {
						t.Fatal("form update rewrote excluded server category")
					}
				}
			}
			if mode == "collection_only" || mode == "clear_collection" {
				if len(traced.ticketUpdates) != 0 {
					t.Fatal("collection-only save rewrote unchanged scalar columns")
				}
			}
			if !definition.PostClean.Empty() && calls != 1 {
				t.Fatal("typed write rebound model clean", calls)
			}
		})
	}
	t.Run("inline_parent", func(t *testing.T) { verifyInlineParentPersistence(t, b) })
	t.Run("inline_readonly", func(t *testing.T) { verifyReadOnlyInlinePersistence(t, b) })
}

// Trace real native writes while retaining borrowed lifetime and conflict
// insertion. Seeding and readback use the underlying backend directly.
type formSaveTracedBackend struct {
	formSaveDatabase
	ticketUpdates [][]string
}

func (b *formSaveTracedBackend) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	return b.formSaveDatabase.AtomicRelation(ctx, func(session db.RelationSession) error {
		lifetime, ok := session.(db.SessionValidator)
		if !ok {
			return fmt.Errorf("native session lacks lifetime")
		}
		conflict, ok := session.(db.ConflictInserter)
		if !ok {
			return fmt.Errorf("native session lacks conflict insertion")
		}
		return callback(&formSaveTracedSession{RelationSession: session, lifetime: lifetime, conflict: conflict, owner: b})
	})
}

type formSaveTracedSession struct {
	db.RelationSession
	lifetime db.SessionValidator
	conflict db.ConflictInserter
	owner    *formSaveTracedBackend
}

func (s *formSaveTracedSession) ValidateSession(ctx context.Context) error {
	return s.lifetime.ValidateSession(ctx)
}
func (s *formSaveTracedSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	return s.conflict.InsertOnConflict(ctx, plan)
}
func (s *formSaveTracedSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if plan.Table() == "helpdesk_ticket" {
		fields := []string{}
		for _, assignment := range plan.Assignments() {
			fields = append(fields, assignment.Field().Name())
		}
		s.owner.ticketUpdates = append(s.owner.ticketUpdates, fields)
	}
	return s.RelationSession.Update(ctx, plan)
}
