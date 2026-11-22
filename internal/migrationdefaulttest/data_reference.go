package migrationdefaulttest

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
)

//go:embed testdata/historical-data-django61.json
var historicalReference []byte

type historicalOracle map[string]any

func loadHistoricalOracle(t *testing.T) historicalOracle {
	t.Helper()
	var oracle historicalOracle
	if err := json.Unmarshal(historicalReference, &oracle); err != nil || len(oracle) != 15 {
		t.Fatal("missing pinned historical migration observations", err)
	}
	return oracle
}

func (o historicalOracle) check(t *testing.T, phase string, actual any) {
	t.Helper()
	want, found := o[phase]
	if !found {
		t.Fatal("unknown Django phase", phase)
	}
	compareObservation(t, phase, want, actual)
}

func compareObservation(t *testing.T, phase string, want, actual any) {
	t.Helper()
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(encoded, &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Django phase %s: got %s, want %v (%v)", phase, encoded, want, err)
	}
}

func (o historicalOracle) plan(t *testing.T, phase string, steps []migrations.PlanStep, sqlTarget string, comments int) {
	t.Helper()
	names := make([]string, len(steps))
	irreversible := false
	for i, step := range steps {
		names[i] = step.Key.Name
		irreversible = irreversible || step.Irreversible
	}
	if sqlTarget != "" {
		o.check(t, phase, map[string]any{"migrations": names, "irreversible": irreversible, "sql_target": sqlTarget, "non_sql_operations": comments})
		return
	}
	// The currently public SQL renderer only projects forward operations.
	// Its reverse SQL diagnostics are a separate M1 acceptance condition.
	want := o[phase].(map[string]any)
	compareObservation(t, phase+".migrations", want["migrations"], names)
	compareObservation(t, phase+".irreversible", want["irreversible"], irreversible)
}

func nativeHistoricalSnapshot(t *testing.T, f Fixture) map[string]any {
	t.Helper()
	ctx := t.Context()
	query := func(stmt string, args ...any) *sql.Row { return f.SQL.QueryRowContext(ctx, stmt, args...) }
	var names []string
	var err error
	for _, name := range []string{"legacy_code", "label"} {
		var exists bool
		if f.SQLite {
			err = query("SELECT EXISTS(SELECT 1 FROM pragma_table_info('m1_category') WHERE name=?)", name).Scan(&exists)
		} else {
			err = query("SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='m1_category' AND column_name=$2)", f.Namespace, name).Scan(&exists)
		}
		if err != nil {
			t.Fatal(err)
		}
		if exists {
			names = append(names, name)
		}
	}
	if len(names) != 1 {
		t.Fatal("historical category columns", names)
	}
	var nullable sql.NullBool
	var unique, fk bool
	if f.SQLite {
		err = query(`SELECT NOT "notnull" FROM pragma_table_info('m1_ticket') WHERE name='external_id'`).Scan(&nullable)
	} else {
		err = query("SELECT is_nullable='YES' FROM information_schema.columns WHERE table_schema=$1 AND table_name='m1_ticket' AND column_name='external_id'", f.Namespace).Scan(&nullable)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if f.SQLite {
		err = query(`SELECT EXISTS(SELECT 1 FROM pragma_index_list('m1_ticket') i WHERE i."unique" AND NOT i.partial
			AND (SELECT COUNT(*) FROM pragma_index_info(i.name))=1
			AND EXISTS(SELECT 1 FROM pragma_index_info(i.name) WHERE name='external_id')),
			EXISTS(SELECT 1 FROM pragma_foreign_key_list('m1_ticket') WHERE "table"='m1_category' AND "from"='category_id' AND "to"='id')`).Scan(&unique, &fk)
	} else {
		err = query(`SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indrelid
			JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum=i.indkey[0]
			WHERE n.nspname=$1 AND c.relname='m1_ticket' AND a.attname='external_id' AND i.indisunique AND i.indisvalid AND i.indnatts=1 AND i.indpred IS NULL),
			EXISTS(SELECT 1 FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid JOIN pg_catalog.pg_class p ON p.oid=k.confrelid
			JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.conkey[1]
			JOIN pg_catalog.pg_attribute b ON b.attrelid=p.oid AND b.attnum=k.confkey[1]
			WHERE k.contype='f' AND n.nspname=$1 AND p.relnamespace=c.relnamespace AND c.relname='m1_ticket' AND p.relname='m1_category'
			AND cardinality(k.conkey)=1 AND a.attname='category_id' AND b.attname='id')`, f.Namespace).Scan(&unique, &fk)
	}
	if err != nil {
		t.Fatal(err)
	}
	read := func(stmt string) [][]any {
		t.Helper()
		rows, err := f.SQL.QueryContext(ctx, stmt)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		values := [][]any{}
		for rows.Next() {
			row := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range row {
				dest[i] = &row[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			values = append(values, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return values
	}
	projected := "NULL"
	var externalNullable any
	if nullable.Valid {
		externalNullable = nullable.Bool
		projected = "external_id"
	}
	stored, err := f.Backend.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	applied := make([]string, 0, len(stored))
	for _, record := range stored {
		if record.App == dataApp {
			applied = append(applied, record.Name)
		}
	}
	return map[string]any{
		"applied": applied, "category_column": names[0], "external_nullable": externalNullable, "external_unique": unique, "category_fk": fk,
		"categories": read("SELECT id," + names[0] + " FROM " + f.table("m1_category") + " ORDER BY id"),
		"tickets":    read("SELECT id,category_id,note," + projected + " FROM " + f.table("m1_ticket") + " ORDER BY id"),
	}
}
