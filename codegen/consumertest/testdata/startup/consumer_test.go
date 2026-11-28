package consumer

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/notes"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
)

func TestInstalledAppsOwnLiveResourcesAndPreserveData(t *testing.T) {
	configured, err := settings.New(settings.Definition{ProjectName: "startup-consumer", InstalledApps: project.InstalledApps()})
	if err != nil {
		t.Fatal(err)
	}
	if got := configured.Apps().All(); len(got) != 2 || got[0].Label != "notes" || got[1].Label != "_storage" {
		t.Fatal("unexpected generated app inventory")
	}
	selected, err := database.SQLite(filepath.Join(t.TempDir(), "project.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	// Migration is an explicit step before app startup. Neither app callback
	// creates or repairs schema, and a later startup reuses the stored data.
	prepareDatabase(t, selected)
	var backend database.Backend
	var opened, closed, workers atomic.Int32
	var cleanupOrder []string
	failure := errors.New("dependent-app-configuration")
	start := func(fail bool) (*apps.Lifecycle, error) {
		return apps.Start(t.Context(), configured.Apps(), apps.StartupConfig{Hooks: []apps.Startup{
			{Label: "notes", DependsOn: []string{"_storage"}, Start: func(ctx context.Context, _ apps.Config) (apps.Stop, error) {
				ownedBackend := backend
				if _, err := notes.NoteObjects.Using(ownedBackend).Count(ctx); err != nil {
					return nil, err
				}
				finished := make(chan struct{})
				workers.Add(1)
				go func() { defer close(finished); defer workers.Add(-1); <-ctx.Done() }()
				stop := func(cleanup context.Context) error {
					select {
					case <-finished:
					case <-cleanup.Done():
						return cleanup.Err()
					}
					// The dependency must still be usable while this app stops.
					if _, err := notes.NoteObjects.Using(ownedBackend).Count(cleanup); err != nil {
						return err
					}
					cleanupOrder = append(cleanupOrder, "notes")
					return nil
				}
				if fail {
					return stop, failure
				}
				return stop, nil
			}},
			{Label: "_storage", Start: func(ctx context.Context, _ apps.Config) (apps.Stop, error) {
				ownedBackend, err := database.Open(ctx, selected)
				if err != nil {
					return nil, err
				}
				backend = ownedBackend
				opened.Add(1)
				return func(context.Context) error {
					cleanupOrder = append(cleanupOrder, "_storage")
					closed.Add(1)
					return ownedBackend.Close()
				}, nil
			}},
		}})
	}
	lifetime, err := start(false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lifetime.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if order := lifetime.Order(); len(order) != 2 || order[0].Label != "_storage" || order[1].Label != "notes" {
		t.Fatal("dependency order did not override generated inventory order")
	}
	created, err := notes.NoteObjects.Create(t.Context(), backend, notes.NewNoteCreate("survives restart"))
	if err != nil || created.ID <= 0 {
		t.Fatal("generated model write", err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := lifetime.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	if workers.Load() != 0 || opened.Load() != 1 || closed.Load() != 1 || !reflect.DeepEqual(cleanupOrder, []string{"notes", "_storage"}) {
		t.Fatal("resource ownership or reverse cleanup differs", cleanupOrder)
	}
	if _, err := notes.NoteObjects.Using(backend).Count(t.Context()); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatal("shutdown retained an open database", err)
	}
	cleanupOrder = nil
	if failed, err := start(true); failed != nil || !errors.Is(err, failure) {
		t.Fatal("failed dependent app published a lifecycle", err)
	}
	if workers.Load() != 0 || opened.Load() != 2 || closed.Load() != 2 || !reflect.DeepEqual(cleanupOrder, []string{"notes", "_storage"}) {
		t.Fatal("partial startup leaked resources or changed cleanup order", cleanupOrder)
	}
	if _, err := notes.NoteObjects.Using(backend).Count(t.Context()); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatal("rollback retained an open database", err)
	}
	reopened, err := database.Open(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows, err := notes.NoteObjects.Using(reopened).All(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != created.ID || rows[0].Title != "survives restart" {
		t.Fatal("startup/cleanup changed existing model data", err)
	}
}

func prepareDatabase(t *testing.T, selected database.Config) {
	t.Helper()
	backend, err := database.Open(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	}()
	encoded, err := definition.Encode(definition.Producer{Name: "startup-consumer", Version: "1"}, migrations.Migration{
		App: "notes", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "notes", Model: notes.NoteDescriptor{}.Metadata()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "startup-consumer", Document: encoded})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
}
