package migrationdefaulttest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

const dataApp = "m1_data"

func dataOp(name string, forward, reverse migrations.DataAction) migrations.RunData {
	return migrations.RunData{
		Key: migrations.DataKey{App: dataApp, Name: name, Version: 1}, Forward: forward, Reverse: reverse,
		Models: []ir.ModelIdentity{{AppLabel: dataApp, ModelName: "ticket"}},
	}
}

func historicalChanges(t *testing.T) []migrations.Migration {
	t.Helper()
	s, err := schema.Build(schema.Definition{AppLabel: dataApp, Models: []schema.Model{
		{Name: "category", GoName: "Category", DBTable: "m1_category", Fields: []schema.Field{schema.CharField("legacy_code", "LegacyCode", 32)}},
		{Name: "ticket", GoName: "Ticket", DBTable: "m1_ticket", Fields: []schema.Field{
			schema.ForeignKey("category", "Category", schema.Target(dataApp, "category"), schema.RelatedName("tickets"), schema.Protect),
			schema.CharField("note", "Note", 32), schema.CharField("external_id", "ExternalID", 80, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	category, ticket := s.Models[0], s.Models[1].Clone()
	index := slices.IndexFunc(ticket.Fields, func(f ir.Field) bool { return f.Name == "external_id" })
	if index < 0 {
		t.Fatal("missing external_id in normalized model")
	}
	external := ticket.Fields[index].Clone()
	ticket.Fields = slices.Delete(ticket.Fields, index, index+1)
	required := external.Clone()
	required.Nullable, required.Unique = false, true
	changes := []migrations.Migration{
		{App: dataApp, Name: "0001_initial", Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: dataApp, Model: category}, migrations.CreateModel{AppLabel: dataApp, Model: ticket}}},
		{App: dataApp, Name: "0002_expand", Operations: []migrations.Operation{
			migrations.AddField{AppLabel: dataApp, ModelName: "ticket", Field: external}}},
		{App: dataApp, Name: "0003_backfill", Operations: []migrations.Operation{
			dataOp("backfill", migrations.DataCall, migrations.DataCall), dataOp("mark", migrations.DataCall, migrations.DataNoop)}},
		{App: dataApp, Name: "0004_contract", Operations: []migrations.Operation{
			migrations.AlterField{AppLabel: dataApp, ModelName: "ticket", Before: external, After: required}}},
		{App: dataApp, Name: "0005_freeze", Operations: []migrations.Operation{
			dataOp("freeze", migrations.DataCall, migrations.DataIrreversible), dataOp("later", migrations.DataNoop, migrations.DataCall)}},
		{App: dataApp, Name: "0006_tail", Operations: []migrations.Operation{
			dataOp("tail", migrations.DataCall, migrations.DataCall)}},
	}
	for i := 1; i < len(changes); i++ {
		changes[i].Dependencies = []migrations.MigrationKey{changes[i-1].Key()}
	}
	return changes
}

func dataRequest(change migrations.Migration) migrations.LifecycleRequest {
	return migrations.TargetedLifecycleRequest(migrations.NamedTarget(change.Key()))
}

func updateHistorical(ctx context.Context, table orm.RecordSet, id int64, name string, value query.Value) error {
	count, err := table.Update(ctx, id, orm.Record{name: value})
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("updated %d rows at key %d", count, id)
	}
	return nil
}

func dataState(t *testing.T, f Fixture) [][]string {
	t.Helper()
	var state [][]string
	for _, name := range []string{"m1_category", "m1_ticket", "godj_migrations", "godj_migration_revision"} {
		rows := snapshot(t, f, name)
		if name == "godj_migrations" {
			slices.Sort(rows)
		}
		state = append(state, rows)
	}
	return state
}

type ticketRow struct {
	id, category int64
	note         string
	external     sql.NullString
}

func historicalTickets(t *testing.T, f Fixture) []ticketRow {
	t.Helper()
	rows, err := f.SQL.QueryContext(t.Context(), "SELECT id, category_id, note, external_id FROM "+f.table("m1_ticket")+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []ticketRow
	for rows.Next() {
		var row ticketRow
		if err := rows.Scan(&row.id, &row.category, &row.note, &row.external); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func RunHistoricalData(t *testing.T, database func(*testing.T) func() Fixture, constraint func(error) string) {
	t.Run("mixed_transaction_and_scope", func(t *testing.T) { checkHistoricalTransaction(t, database(t)) })
	t.Run("backfill_restart_and_reverse", func(t *testing.T) {
		open := database(t)
		f := open()
		changes := historicalChanges(t)
		loaded := loadChanges(t, changes...)
		var retained orm.RecordSet
		var calls []string
		missingCode := errors.New("missing historical category code")
		transform := func(clear bool) migrations.DataCallback {
			return func(ctx context.Context, models migrations.HistoricalModels) error {
				tickets, err := models.Model(ctx, dataApp, "ticket")
				if err != nil {
					return err
				}
				retained = tickets
				categories, err := models.Model(ctx, dataApp, "category")
				if err != nil {
					return err
				}
				if _, err := models.Model(ctx, dataApp, "other"); err == nil {
					return errors.New("undeclared model visible")
				}
				var cursor *int64
				for {
					page, err := tickets.Page(ctx, cursor, 2)
					if err != nil {
						return err
					}
					if len(page) == 0 {
						return nil
					}
					if len(page) > 2 {
						return errors.New("unbounded historical page")
					}
					for _, row := range page {
						id, ok := row["id"].Integer()
						if !ok || (cursor != nil && id <= *cursor) {
							return errors.New("invalid page ordering or primary key")
						}
						categoryID, ok := row["category"].Integer()
						if !ok {
							return errors.New("missing category key")
						}
						category, err := categories.Get(ctx, categoryID)
						if err != nil {
							return err
						}
						code, ok := category["legacy_code"].String()
						if !ok || code == "" {
							return missingCode
						}
						value := query.String(fmt.Sprintf("%s/%d", code, id))
						if clear {
							value = query.Null()
						}
						if err := updateHistorical(ctx, tickets, id, "external_id", value); err != nil {
							return err
						}
						changed, err := tickets.Get(ctx, id)
						if err != nil || !reflect.DeepEqual(changed["external_id"], value) {
							return fmt.Errorf("own update not visible for %d: %v", id, err)
						}
						calls = append(calls, fmt.Sprintf("row/%t/%d", clear, id))
						cursor = &id
					}
				}
			}
		}
		note := func(id int64, value string) migrations.DataCallback {
			return func(ctx context.Context, models migrations.HistoricalModels) error {
				tickets, err := models.Model(ctx, dataApp, "ticket")
				if err != nil {
					return err
				}
				calls = append(calls, fmt.Sprintf("note/%d/%s", id, value))
				return updateHistorical(ctx, tickets, id, "note", query.String(value))
			}
		}
		reg := func(name string, forward, reverse migrations.DataCallback) migrations.DataRegistration {
			return migrations.DataRegistration{Key: migrations.DataKey{App: dataApp, Name: name, Version: 1}, Forward: forward, Reverse: reverse}
		}
		registry, err := migrations.NewDataRegistry(
			reg("backfill", transform(false), transform(true)),
			reg("mark", note(11, "reviewed"), nil),
			reg("freeze", note(17, "frozen"), nil),
			reg("later", nil, note(17, "reverse visited")),
			reg("tail", note(23, "tail"), note(23, "")),
		)
		if err != nil {
			t.Fatal(err)
		}
		executor := func() migrations.Executor { return migrations.Executor{Backend: f.Backend, Data: registry} }
		run := func(i int) error {
			_, err := executor().Migrate(t.Context(), loaded, dataRequest(changes[i]))
			return err
		}
		apply := func(i int) {
			t.Helper()
			if err := run(i); err != nil {
				t.Fatal(err)
			}
		}
		reopen := func() {
			t.Helper()
			if err := f.SQL.Close(); err != nil {
				t.Fatal(err)
			}
			f = open()
		}
		checkError := func(err error, code migrations.ErrorCode, mig int, index int) {
			t.Helper()
			var detail *migrations.Error
			if !errors.As(err, &detail) || detail.Code != code || detail.Migration != changes[mig].Name || detail.OperationIndex != index || detail.RollbackCause != nil {
				t.Fatalf("expected %s %s[%d], got %v", code, changes[mig].Name, index, err)
			}
		}
		execSQL := func(statement string) {
			t.Helper()
			if _, err := f.SQL.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
		apply(1)
		categories, tickets := f.table("m1_category"), f.table("m1_ticket")
		execSQL("INSERT INTO " + categories + " (id,legacy_code) VALUES (1,'한''글'),(2,'')")
		execSQL("INSERT INTO " + tickets + " (id,category_id,note) VALUES (11,1,''),(17,1,''),(23,2,'')")
		rowsBefore := dataState(t, f)
		steps, err := executor().Plan(t.Context(), loaded, dataRequest(changes[2]))
		if err != nil || len(steps) != 1 || len(calls) != 0 || !reflect.DeepEqual(rowsBefore, dataState(t, f)) {
			t.Fatal("plan side effects or wrong number of steps", steps, err)
		}
		statements, err := migrations.RenderMigrationSQL(t.Context(), loaded, changes[2].Key(), f.Renderer)
		if err != nil || strings.Count(strings.Join(statements, "\n"), mb.RunDataSQLComment) != 2 || len(calls) != 0 {
			t.Fatal("SQL projection of two callbacks", statements, err)
		}
		// A user trigger on a reachable history model can run unregistered
		// writes on this connection. Both native catalogs must reject it.
		dropHook := "DROP TRIGGER m1_data_hook ON " + categories
		if f.SQLite {
			execSQL("CREATE TRIGGER m1_data_hook BEFORE UPDATE ON m1_category BEGIN SELECT 1; END")
			dropHook = "DROP TRIGGER m1_data_hook"
		} else {
			execSQL("CREATE TRIGGER m1_data_hook BEFORE UPDATE ON " + categories + " FOR EACH ROW EXECUTE FUNCTION pg_catalog.suppress_redundant_updates_trigger()")
		}
		if err := run(2); err == nil || len(calls) != 0 || !reflect.DeepEqual(rowsBefore, dataState(t, f)) {
			t.Fatal("data callback ran with an unexpected native trigger", err, calls)
		}
		execSQL(dropHook)
		err = run(2)
		checkError(err, migrations.CodeOperationFailed, 2, 0)
		if !errors.Is(err, missingCode) || !slices.Equal(calls, []string{"row/false/11", "row/false/17"}) {
			t.Fatal("callback failure lost writes or cause", calls, err)
		}
		if _, err := retained.Get(t.Context(), 11); err == nil {
			t.Fatal("callback record set survived rollback")
		}
		reopen()
		if !reflect.DeepEqual(rowsBefore, dataState(t, f)) {
			t.Fatal("failed callback changed durable data, schema history or revision")
		}
		execSQL("UPDATE " + categories + " SET legacy_code='β' WHERE id=2")
		calls = nil
		apply(3)
		if !slices.Equal(calls, []string{"row/false/11", "row/false/17", "row/false/23", "note/11/reviewed"}) {
			t.Fatal("forward callback order", calls)
		}
		want := []ticketRow{{11, 1, "reviewed", sql.NullString{String: "한'글/11", Valid: true}}, {17, 1, "", sql.NullString{String: "한'글/17", Valid: true}}, {23, 2, "", sql.NullString{String: "β/23", Valid: true}}}
		if got := historicalTickets(t, f); !slices.Equal(got, want) {
			t.Fatalf("persisted rows = %v, want %v", got, want)
		}
		state := dataState(t, f)
		for statement, kind := range map[string]string{
			"UPDATE " + tickets + " SET external_id=NULL WHERE id=11":   "not_null",
			"UPDATE " + tickets + " SET external_id='β/23' WHERE id=11": "unique",
		} {
			if _, err := f.SQL.ExecContext(t.Context(), statement); constraint(err) != kind {
				t.Fatal("missing native constraint", kind, err)
			}
		}
		reopen()
		calls = nil
		apply(3)
		if len(calls) != 0 || !reflect.DeepEqual(state, dataState(t, f)) {
			t.Fatal("reapplying changed data or revision")
		}
		if _, err := retained.Update(t.Context(), 11, orm.Record{"note": query.String("leaked")}); err == nil {
			t.Fatal("committed record set remained writable")
		}
		apply(1)
		cleared := slices.Clone(want)
		for i := range cleared {
			cleared[i].external = sql.NullString{}
		}
		if got := historicalTickets(t, f); !slices.Equal(got, cleared) || !slices.Equal(calls, []string{"row/true/11", "row/true/17", "row/true/23"}) {
			t.Fatal("reverse noop or reverse backfill", got, calls)
		}
		apply(5)
		calls = nil
		beforePlan := dataState(t, f)
		steps, err = executor().Plan(t.Context(), loaded, dataRequest(changes[2]))
		if err != nil || len(steps) != 3 ||
			steps[0].Key != changes[5].Key() || steps[0].Irreversible ||
			steps[1].Key != changes[4].Key() || !steps[1].Irreversible || steps[1].IrreversibleOperation != 0 ||
			steps[2].Key != changes[3].Key() || steps[2].Irreversible ||
			len(calls) != 0 || !reflect.DeepEqual(beforePlan, dataState(t, f)) {
			t.Fatal("reverse plan", steps, err)
		}
		checkError(run(2), migrations.CodeIrreversibleData, 4, 0)
		if !slices.Equal(calls, []string{"note/23/"}) {
			t.Fatal("irreversible migration partly executed", calls)
		}
		reopen()
		recorded, err := f.Backend.ReadAppliedMigrations(t.Context())
		if err != nil || len(recorded) != 5 || recorded[4].Name != changes[4].Name {
			t.Fatal("durable prefix", recorded, err)
		}
		want[1].note = "frozen"
		if got := historicalTickets(t, f); !slices.Equal(got, want) {
			t.Fatal("durable reverse prefix rows", got)
		}
		state = dataState(t, f)
		calls = nil
		checkError(run(2), migrations.CodeIrreversibleData, 4, 0)
		if len(calls) != 0 || !reflect.DeepEqual(state, dataState(t, f)) {
			t.Fatal("irreversible retry changed state")
		}
		apply(5)
		state = dataState(t, f)
		_, err = (migrations.Executor{Backend: f.Backend}).Plan(t.Context(), loaded, migrations.LatestLifecycleRequest())
		checkError(err, migrations.CodeMissingDataCallback, 2, 0)
		altered := slices.Clone(changes)
		altered[2].Operations = slices.Clone(changes[2].Operations)
		op := altered[2].Operations[0].(migrations.RunData)
		op.Key.Version++
		altered[2].Operations[0] = op
		_, err = executor().Migrate(t.Context(), loadChanges(t, altered...), migrations.LatestLifecycleRequest())
		checkError(err, migrations.CodeDefinitionHistoryMismatch, 2, migrations.NoOperation)
		if !reflect.DeepEqual(state, dataState(t, f)) {
			t.Fatal("invalid definition or registry changed durable state")
		}
	})
}

func checkHistoricalTransaction(t *testing.T, open func() Fixture) {
	t.Helper()
	f := open()
	changes := historicalChanges(t)[:2]
	operation := dataOp("expanded", migrations.DataCall, migrations.DataCall)
	changes[1].Operations = append(changes[1].Operations, operation)
	loaded := loadChanges(t, changes...)
	var retained migrations.HistoricalModels
	var called, reverse int
	sentinel := errors.New("requested rollback")
	behavior := "fail"
	var cancel context.CancelFunc
	registry, err := migrations.NewDataRegistry(migrations.DataRegistration{
		Key: operation.Key,
		Forward: func(ctx context.Context, scope migrations.HistoricalModels) error {
			called++
			retained = scope
			rows, err := scope.Model(ctx, dataApp, "ticket")
			if err != nil {
				return err
			}
			for _, values := range []orm.Record{{"id": query.Integer(9)}, {"later_label": query.String("current")}, {"note": query.Null()}, {"external_id": query.Integer(4)}} {
				if _, err := rows.Update(ctx, 11, values); err == nil {
					return fmt.Errorf("historical record accepted invalid fields %v", values)
				}
			}
			if count, err := rows.Update(ctx, 99, orm.Record{"external_id": query.String("missing")}); err != nil || count != 0 {
				return fmt.Errorf("missing key update: %d, %v", count, err)
			}
			if err := updateHistorical(ctx, rows, 11, "external_id", query.String("in transaction")); err != nil {
				return err
			}
			switch behavior {
			case "fail":
				return sentinel
			case "cancel":
				cancel()
			case "panic":
				panic(sentinel)
			}
			return nil
		},
		Reverse: func(ctx context.Context, scope migrations.HistoricalModels) error {
			rows, err := scope.Model(ctx, dataApp, "ticket")
			if err != nil {
				return err
			}
			row, err := rows.Get(ctx, 11)
			if err != nil {
				return err
			}
			text, ok := row["external_id"].String()
			if !ok || text != "in transaction" {
				return errors.New("reverse callback lost expanded column")
			}
			reverse++
			return updateHistorical(ctx, rows, 11, "note", query.String("reversed"))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, i int) error {
		_, err := (migrations.Executor{Backend: f.Backend, Data: registry}).Migrate(ctx, loaded, dataRequest(changes[i]))
		return err
	}
	if err := run(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"INSERT INTO " + f.table("m1_category") + " (id,legacy_code) VALUES (1,'A')", "INSERT INTO " + f.table("m1_ticket") + " (id,category_id,note) VALUES (11,1,'')"} {
		if _, err := f.SQL.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	before := dataState(t, f)
	reopenAndCheck := func() {
		t.Helper()
		if _, err := retained.Model(t.Context(), dataApp, "ticket"); err == nil {
			t.Fatal("escaped historical model scope")
		}
		if err := f.SQL.Close(); err != nil {
			t.Fatal(err)
		}
		f = open()
		if !reflect.DeepEqual(before, dataState(t, f)) {
			t.Fatal("failed migration leaked a column, row, receipt or revision")
		}
	}
	if err := run(t.Context(), 1); !errors.Is(err, sentinel) {
		t.Fatal("lost callback cause", err)
	}
	reopenAndCheck()
	behavior = "cancel"
	ctx, cancelRun := context.WithCancel(t.Context())
	cancel = cancelRun
	err = run(ctx, 1)
	cancelRun()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
	reopenAndCheck()
	behavior = "panic"
	func() {
		defer func() {
			if got := recover(); got != sentinel {
				t.Fatalf("unexpected panic: %v", got)
			}
		}()
		_ = run(t.Context(), 1)
	}()
	reopenAndCheck()
	behavior = "ok"
	if err := run(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if called != 4 {
		t.Fatal("callback execution count", called)
	}
	if err := run(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	if reverse != 1 || !slices.Equal(snapshot(t, f, "m1_ticket"), []string{"[11 1 reversed]"}) {
		t.Fatal("reverse did not precede removal of the same migration's column")
	}

	// The physical table exists, but this other app declares no dependency.
	outside := operation
	outside.Key.App = "other_app"
	outside.Forward, outside.Reverse = migrations.DataNoop, migrations.DataNoop
	unrelated := migrations.Migration{App: outside.Key.App, Name: "0001_unrelated", Operations: []migrations.Operation{outside}}
	before = dataState(t, f)
	_, err = (migrations.Executor{Backend: f.Backend, Data: registry}).Migrate(t.Context(), loadChanges(t, changes[0], unrelated), dataRequest(unrelated))
	var detail *migrations.Error
	if !errors.As(err, &detail) || detail.Code != migrations.CodeInvalidState || detail.OperationIndex != 0 || !reflect.DeepEqual(before, dataState(t, f)) {
		t.Fatal("unrelated applied model supplied an undeclared dependency", err)
	}
	// Adding a data operation to a previously applied, unbound migration
	// would silently skip the callback if the recorder only compared keys.
	old := changes[0]
	old.Operations = append(slices.Clone(old.Operations), operation)
	_, err = (migrations.Executor{Backend: f.Backend, Data: registry}).Plan(t.Context(), loadChanges(t, old), migrations.LatestLifecycleRequest())
	if !errors.As(err, &detail) || detail.Code != migrations.CodeDefinitionHistoryMismatch || !reflect.DeepEqual(before, dataState(t, f)) {
		t.Fatal("unbound existing data operation", err)
	}

	// Adding the actual dependency admits the same cross-app model. Its
	// dependency stays applied when this app alone returns to zero.
	unrelated.Dependencies = []migrations.MigrationKey{changes[0].Key()}
	outside.Forward = migrations.DataCall
	unrelated.Operations = []migrations.Operation{outside}
	crossCalls := 0
	crossRegistry, err := migrations.NewDataRegistry(migrations.DataRegistration{Key: outside.Key, Forward: func(ctx context.Context, models migrations.HistoricalModels) error {
		tickets, err := models.Model(ctx, dataApp, "ticket")
		if err != nil {
			return err
		}
		row, err := tickets.Get(ctx, 11)
		if err != nil {
			return err
		}
		if _, present := row["external_id"]; present {
			return errors.New("future column leaked into cross-app history")
		}
		code, ok := row["note"].String()
		if !ok || code != "reversed" {
			return fmt.Errorf("historical note was %q", code)
		}
		crossCalls++
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	crossSet := loadChanges(t, changes[0], unrelated)
	cross := migrations.Executor{Backend: f.Backend, Data: crossRegistry}
	if _, err := cross.Migrate(t.Context(), crossSet, dataRequest(unrelated)); err != nil || crossCalls != 1 {
		t.Fatal("cross-app dependency scope", crossCalls, err)
	}
	if _, err := cross.Migrate(t.Context(), crossSet, migrations.TargetedLifecycleRequest(migrations.ZeroTarget(unrelated.App))); err != nil {
		t.Fatal(err)
	}
	keys, err := f.Backend.ReadAppliedMigrations(t.Context())
	if err != nil || len(keys) != 1 || keys[0].App != dataApp || keys[0].Name != changes[0].Name || crossCalls != 1 {
		t.Fatal("cross-app reverse changed dependency", keys, crossCalls, err)
	}
}
