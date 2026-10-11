package consumer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

type groupedBackend interface {
	db.Session
	db.Atomic
	db.QueryUpdater
	db.SnapshotReader
	migrationbackend.RevisionFencedBackend
	Close() error
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func withGroupedBackends(t *testing.T, run func(*testing.T, groupedBackend, func() (groupedBackend, error), bool)) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "grouped.sqlite3")) + "?mode=rwc&_pragma=busy_timeout(10000)"
		backend, err := sqlite.Open(t.Context(), dsn)
		check(t, err)
		t.Cleanup(func() { check(t, backend.Close()) })
		_, err = backend.ExecContext(t.Context(), "PRAGMA journal_mode=WAL")
		check(t, err)
		migrateGrouped(t, backend)
		seedGrouped(t, backend)
		run(t, backend, func() (groupedBackend, error) { return sqlite.Open(t.Context(), dsn) }, false)
	})
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), url)
		check(t, err)
		schema := fmt.Sprintf("godj_grouped_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		_, err = connection.Exec(t.Context(), "CREATE SCHEMA "+quoted)
		check(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
			check(t, err)
			check(t, connection.Close(ctx))
		})
		open := func() (groupedBackend, error) {
			return postgres.Open(t.Context(), postgres.Config{URL: url, Schema: schema})
		}
		backend, err := open()
		check(t, err)
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateGrouped(t, backend)
		seedGrouped(t, backend)
		run(t, backend, open, true)
	})
}

func migrateGrouped(t *testing.T, backend groupedBackend) {
	t.Helper()
	migration := migrations.Migration{App: "records", Name: "0001_initial"}
	var many []ir.ManyToManyField
	for _, model := range records.GoDjRelationSchema().Models {
		if model.Name == "item" {
			many = model.ManyToMany
			model.ManyToMany = nil
		}
		migration.Operations = append(migration.Operations, migrations.CreateModel{AppLabel: "records", Model: model})
	}
	for _, field := range many {
		migration.Operations = append(migration.Operations, migrations.AddManyToMany{AppLabel: "records", ModelName: "item", Field: field})
	}
	wire, err := definition.Encode(definition.Producer{Name: "grouped-consumer", Version: "1"}, migration)
	check(t, err)
	loaded, _, err := definition.Load(definition.Source{SourceID: "grouped", Document: wire})
	check(t, err)
	_, err = (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest())
	check(t, err)
}

func pointer[V any](value V) *V { return &value }
func seedGrouped(t *testing.T, backend groupedBackend) {
	t.Helper()
	ctx := t.Context()
	for index, name := range []string{"first", "second", "empty"} {
		input := records.NewGroupCreate(name)
		if index == 1 {
			input = input.WithRegion("east")
		}
		value, err := records.GroupObjects.Create(ctx, backend, input)
		check(t, err)
		if value.ID != int64(index+1) {
			t.Fatal("synthetic group identity", value.ID)
		}
	}
	inputs := []struct {
		rank    *int64
		note    *string
		enabled bool
		group   *int64
	}{
		{nil, nil, true, pointer(int64(1))}, {nil, pointer("first"), false, pointer(int64(1))}, {pointer(int64(0)), pointer("same"), true, pointer(int64(2))},
		{pointer(int64(0)), pointer("same"), true, nil}, {pointer(int64(1)), pointer(""), false, pointer(int64(2))}, {pointer(int64(1)), pointer(""), true, nil},
		{pointer(int64(7)), pointer("later"), true, pointer(int64(1))}, {nil, nil, false, nil},
	}
	for index, values := range inputs {
		id := index + 1
		choice := id % 2
		price, err := decimal.Parse(fmt.Sprintf("%d.25", choice))
		check(t, err)
		identity, err := uuid.Parse(fmt.Sprintf("00000000-0000-0000-0000-%012d", choice))
		check(t, err)
		binary, err := binaryvalue.FromBytes([]byte{byte(choice), 0, 255})
		check(t, err)
		date, err := calendar.New(2026, time.January, choice+1)
		check(t, err)
		clockTime, err := clock.New(12, 30, choice, 123456)
		check(t, err)
		document, err := jsonvalue.Parse([]byte(fmt.Sprintf(`{"key":%d}`, choice)))
		check(t, err)
		input := records.NewItemCreate(fmt.Sprint("item-", id), int64(id*10), values.enabled, float64(choice)+.5, price, identity, binary, date, clockTime, time.Date(2026, time.January, choice+1, 12, 30, 0, 0, time.UTC), duration.FromMicroseconds(int64(choice)*1000000), document)
		if values.rank != nil {
			input = input.WithRank(*values.rank)
		}
		if values.note != nil {
			input = input.WithNote(*values.note)
		}
		if values.group != nil {
			input = input.WithGroupID(*values.group)
		}
		value, err := records.ItemObjects.Create(ctx, backend, input)
		check(t, err)
		if value.ID != int64(id) {
			t.Fatal("synthetic item identity", value.ID)
		}
	}
	for _, pair := range [][2]int64{{1, 2}, {1, 3}, {3, 2}} {
		_, err := records.PeerObjects.Create(ctx, backend, records.NewPeerCreate(pair[0], pair[1]))
		check(t, err)
	}
}

type groupedProbe struct {
	groupedBackend
	plans         []query.Plan
	afterFirstRow func() error
}

func (probe *groupedProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	probe.plans = append(probe.plans, plan)
	rows, err := probe.groupedBackend.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	if probe.afterFirstRow != nil && plan.ResultShape().GroupMode() == query.GroupPage {
		return &groupedObservedRows{Rows: rows, after: probe.afterFirstRow}, nil
	}
	return rows, nil
}

type groupedObservedRows struct {
	db.Rows
	after func() error
	seen  bool
	err   error
}

func (rows *groupedObservedRows) Next() bool {
	if rows.err != nil {
		return false
	}
	next := rows.Rows.Next()
	if next && !rows.seen {
		rows.seen = true
		rows.err = rows.after()
		if rows.err != nil {
			return false
		}
	}
	return next
}
func (rows *groupedObservedRows) Err() error {
	if rows.err != nil {
		return rows.err
	}
	return rows.Rows.Err()
}
