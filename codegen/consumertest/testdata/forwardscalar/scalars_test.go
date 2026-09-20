package consumer_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"testing"
	"time"

	"example.com/godj-forward-scalar/models"
	"example.com/godj-forward-scalar/project"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
)

func parsed[V any](t *testing.T, raw string, parse func(string) (V, error)) V {
	t.Helper()
	v, err := parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func integerValue(t *testing.T, s string) int64 {
	return parsed(t, s, func(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) })
}
func textValue(_ *testing.T, s string) string  { return s }
func booleanValue(t *testing.T, s string) bool { return parsed(t, s, strconv.ParseBool) }
func floatValue(t *testing.T, s string) float64 {
	return parsed(t, s, func(s string) (float64, error) { return strconv.ParseFloat(s, 64) })
}
func decimalValue(t *testing.T, s string) decimal.Decimal { return parsed(t, s, decimal.Parse) }
func dateTimeValue(t *testing.T, s string) time.Time {
	return parsed(t, s, func(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) })
}
func dateValue(t *testing.T, s string) calendar.Date { return parsed(t, s, calendar.Parse) }
func clockValue(t *testing.T, s string) clock.Time   { return parsed(t, s, clock.Parse) }
func durationValue(t *testing.T, s string) duration.Duration {
	return duration.FromMicroseconds(integerValue(t, s))
}
func uuidValue(t *testing.T, s string) uuid.UUID { return parsed(t, s, uuid.Parse) }
func jsonValue(t *testing.T, s string) jsonvalue.Value {
	return parsed(t, s, func(s string) (jsonvalue.Value, error) { return jsonvalue.Parse([]byte(s)) })
}

func datumCreate(t *testing.T, label string, required []string, nullable []*string) models.DatumCreate {
	t.Helper()
	if len(required) != 11 || len(nullable) != 11 {
		t.Fatal("incomplete datum inputs")
	}
	create := models.NewDatumCreate(label, integerValue(t, required[0]), textValue(t, required[1]), booleanValue(t, required[2]), floatValue(t, required[3]), decimalValue(t, required[4]), dateTimeValue(t, required[5]), dateValue(t, required[6]), clockValue(t, required[7]), durationValue(t, required[8]), uuidValue(t, required[9]), jsonValue(t, required[10]))
	if nullable[0] != nil {
		create = create.WithNInteger(integerValue(t, *nullable[0]))
	}
	if nullable[1] != nil {
		create = create.WithNText(textValue(t, *nullable[1]))
	}
	if nullable[2] != nil {
		create = create.WithNBoolean(booleanValue(t, *nullable[2]))
	}
	if nullable[3] != nil {
		create = create.WithNFloat(floatValue(t, *nullable[3]))
	}
	if nullable[4] != nil {
		create = create.WithNDecimal(decimalValue(t, *nullable[4]))
	}
	if nullable[5] != nil {
		create = create.WithNDateTime(dateTimeValue(t, *nullable[5]))
	}
	if nullable[6] != nil {
		create = create.WithNDate(dateValue(t, *nullable[6]))
	}
	if nullable[7] != nil {
		create = create.WithNTime(clockValue(t, *nullable[7]))
	}
	if nullable[8] != nil {
		create = create.WithNDuration(durationValue(t, *nullable[8]))
	}
	if nullable[9] != nil {
		create = create.WithNUUID(uuidValue(t, *nullable[9]))
	}
	if nullable[10] != nil {
		create = create.WithNJSON(jsonValue(t, *nullable[10]))
	}
	return create
}

func pointerValue[V any](value *V, normalize func(V) any) any {
	if value == nil {
		return nil
	}
	return normalize(*value)
}

type orderedScalarField[V any] interface {
	orm.ScalarField[models.Entry, *V]
	Asc() orm.Ordering[models.Entry]
	Desc() orm.Ordering[models.Entry]
}

func selection[V any](field orderedScalarField[V], normalize func(V) any) scalarSelection {
	return scalarSelection{
		ascending: field.Asc(), descending: field.Desc(),
		rows: func(ctx context.Context, source orm.QuerySet[models.Entry]) ([]projectedRow, error) {
			return orm.SelectInto(ctx, source, orm.Project2(models.EntryFields.ID, field, func(id int64, v *V) projectedRow { return projectedRow{id, pointerValue(v, normalize)} }))
		},
		values: func(ctx context.Context, source orm.QuerySet[models.Entry]) ([]any, error) {
			return orm.SelectInto(ctx, source, orm.Project1(field, func(v *V) any { return pointerValue(v, normalize) }))
		},
	}
}
func bindSelections(t *testing.T, route orm.ForwardRelation[models.Entry, models.Datum]) map[string]scalarSelection {
	t.Helper()
	result := map[string]scalarSelection{}
	{
		field, err := route.Integer(models.DatumFields.VInteger)
		if err != nil {
			t.Fatal(err)
		}
		result["v_integer"] = selection(field, func(v int64) any { return strconv.FormatInt(v, 10) })
	}
	{
		field, err := route.String(models.DatumFields.VText)
		if err != nil {
			t.Fatal(err)
		}
		result["v_text"] = selection(field, func(v string) any { return v })
	}
	{
		field, err := route.Boolean(models.DatumFields.VBoolean)
		if err != nil {
			t.Fatal(err)
		}
		result["v_boolean"] = selection(field, func(v bool) any { return strconv.FormatBool(v) })
	}
	{
		field, err := route.Float(models.DatumFields.VFloat)
		if err != nil {
			t.Fatal(err)
		}
		result["v_float"] = selection(field, func(v float64) any { return fmt.Sprintf("%016x", math.Float64bits(v)) })
	}
	{
		field, err := route.Decimal(models.DatumFields.VDecimal)
		if err != nil {
			t.Fatal(err)
		}
		result["v_decimal"] = selection(field, func(v decimal.Decimal) any {
			s, err := v.Fixed(6)
			if err != nil {
				t.Fatal(err)
			}
			return s
		})
	}
	{
		field, err := route.DateTime(models.DatumFields.VDateTime)
		if err != nil {
			t.Fatal(err)
		}
		result["v_datetime"] = selection(field, func(v time.Time) any { return v.UTC().Format("2006-01-02T15:04:05.000000Z") })
	}
	{
		field, err := route.Date(models.DatumFields.VDate)
		if err != nil {
			t.Fatal(err)
		}
		result["v_date"] = selection(field, func(v calendar.Date) any { return v.String() })
	}
	{
		field, err := route.Time(models.DatumFields.VTime)
		if err != nil {
			t.Fatal(err)
		}
		result["v_time"] = selection(field, func(v clock.Time) any {
			return fmt.Sprintf("%02d:%02d:%02d.%06d", v.Hour, v.Minute, v.Second, v.Microsecond)
		})
	}
	{
		field, err := route.Duration(models.DatumFields.VDuration)
		if err != nil {
			t.Fatal(err)
		}
		result["v_duration"] = selection(field, func(v duration.Duration) any {
			micros, err := v.TotalMicroseconds()
			if err != nil {
				t.Fatal(err)
			}
			return strconv.FormatInt(micros, 10)
		})
	}
	{
		field, err := route.UUID(models.DatumFields.VUUID)
		if err != nil {
			t.Fatal(err)
		}
		result["v_uuid"] = selection(field, func(v uuid.UUID) any { return v.String() })
	}
	{
		field, err := route.JSON(models.DatumFields.VJSON)
		if err != nil {
			t.Fatal(err)
		}
		result["v_json"] = selection(field, func(v jsonvalue.Value) any {
			if v == jsonvalue.Null() {
				return nil
			}
			return v.Text
		})
	}
	{
		field, err := route.Integer(models.DatumFields.NInteger)
		if err != nil {
			t.Fatal(err)
		}
		result["n_integer"] = selection(field, func(v int64) any { return strconv.FormatInt(v, 10) })
	}
	{
		field, err := route.String(models.DatumFields.NText)
		if err != nil {
			t.Fatal(err)
		}
		result["n_text"] = selection(field, func(v string) any { return v })
	}
	{
		field, err := route.Boolean(models.DatumFields.NBoolean)
		if err != nil {
			t.Fatal(err)
		}
		result["n_boolean"] = selection(field, func(v bool) any { return strconv.FormatBool(v) })
	}
	{
		field, err := route.Float(models.DatumFields.NFloat)
		if err != nil {
			t.Fatal(err)
		}
		result["n_float"] = selection(field, func(v float64) any { return fmt.Sprintf("%016x", math.Float64bits(v)) })
	}
	{
		field, err := route.Decimal(models.DatumFields.NDecimal)
		if err != nil {
			t.Fatal(err)
		}
		result["n_decimal"] = selection(field, func(v decimal.Decimal) any {
			s, err := v.Fixed(6)
			if err != nil {
				t.Fatal(err)
			}
			return s
		})
	}
	{
		field, err := route.DateTime(models.DatumFields.NDateTime)
		if err != nil {
			t.Fatal(err)
		}
		result["n_datetime"] = selection(field, func(v time.Time) any { return v.UTC().Format("2006-01-02T15:04:05.000000Z") })
	}
	{
		field, err := route.Date(models.DatumFields.NDate)
		if err != nil {
			t.Fatal(err)
		}
		result["n_date"] = selection(field, func(v calendar.Date) any { return v.String() })
	}
	{
		field, err := route.Time(models.DatumFields.NTime)
		if err != nil {
			t.Fatal(err)
		}
		result["n_time"] = selection(field, func(v clock.Time) any {
			return fmt.Sprintf("%02d:%02d:%02d.%06d", v.Hour, v.Minute, v.Second, v.Microsecond)
		})
	}
	{
		field, err := route.Duration(models.DatumFields.NDuration)
		if err != nil {
			t.Fatal(err)
		}
		result["n_duration"] = selection(field, func(v duration.Duration) any {
			micros, err := v.TotalMicroseconds()
			if err != nil {
				t.Fatal(err)
			}
			return strconv.FormatInt(micros, 10)
		})
	}
	{
		field, err := route.UUID(models.DatumFields.NUUID)
		if err != nil {
			t.Fatal(err)
		}
		result["n_uuid"] = selection(field, func(v uuid.UUID) any { return v.String() })
	}
	{
		field, err := route.JSON(models.DatumFields.NJSON)
		if err != nil {
			t.Fatal(err)
		}
		result["n_json"] = selection(field, func(v jsonvalue.Value) any {
			if v == jsonvalue.Null() {
				return nil
			}
			return v.Text
		})
	}
	return result
}

func verifyResultBoundaries(t *testing.T, backend resultBackend, native bool, source orm.QuerySet[models.Entry], routes []orm.ForwardRelation[models.Entry, models.Datum], data []models.Datum, expected reference, numbers map[int64]int64) {
	t.Helper()
	ctx := t.Context()
	seenJSONNull := false
	for _, test := range expected.JSONNulls {
		index := slices.Index(expected.Routes, test.Route)
		if index < 0 {
			t.Fatal("unknown null route")
		}
		field, err := routes[index].JSON(models.DatumFields.VJSON)
		if test.Field == "n_json" {
			field, err = routes[index].JSON(models.DatumFields.NJSON)
		}
		if err != nil {
			t.Fatal(err)
		}
		type row struct {
			ID    int64
			Value *jsonvalue.Value
		}
		values, err := orm.SelectInto(ctx, source.OrderBy(models.EntryFields.ID.Asc()), orm.Project2(models.EntryFields.ID, field, func(id int64, v *jsonvalue.Value) row { return row{id, v} }))
		if err != nil || len(values) != 6 {
			t.Fatal("selected whole JSON", err)
		}
		for _, value := range values {
			null := slices.Contains(test.Nulls, numbers[value.ID])
			if (value.Value == nil) != null {
				t.Fatal("whole JSON SQL NULL/JSON null collapsed", test.Route, test.Field, numbers[value.ID])
			}
			if value.Value != nil && *value.Value == jsonvalue.Null() {
				seenJSONNull = true
			}
		}
	}
	if !seenJSONNull {
		t.Fatal("JSON null ownership case did not execute")
	}
	relatedID, err := routes[0].Integer(models.DatumFields.ID)
	if err != nil {
		t.Fatal(err)
	}
	idProjection := orm.Project1(relatedID, func(v *int64) *int64 { return v })
	ordered := source.Distinct().OrderBy(models.EntryFields.ID.Asc())
	empty, err := ordered.Limit(0)
	if err != nil {
		t.Fatal(err)
	}
	var before uint64
	if !native {
		before = backend.(*sqlite.Backend).QueryCount()
	}
	for _, candidate := range []orm.QuerySet[models.Entry]{ordered, empty, ordered.Filter(models.EntryFields.ID.In())} {
		if rows, err := orm.SelectInto(ctx, candidate, idProjection); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || rows != nil {
			t.Fatal("related target id satisfied root ordering selection", err)
		}
	}
	text, err := routes[0].String(models.DatumFields.VText)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := orm.Project2(text, text, func(a, b *string) int { return 0 })
	if _, err := orm.SelectInto(ctx, source, duplicate); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("duplicate related value accepted", err)
	}
	cause := errors.New("configured selected field failure")
	if _, err := orm.SelectInto(ctx, source, orm.Project1(text.WithConfigurationError(cause), func(v *string) *string { return v })); !errors.Is(err, cause) {
		t.Fatal("selected field discarded original error", err)
	}
	if _, err := orm.SelectInto(ctx, source, orm.Project1(orm.RelatedStringField[models.Entry]{}, func(v *string) *string { return v })); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("unbound selected field accepted", err)
	}
	if !native && backend.(*sqlite.Backend).QueryCount() != before {
		t.Fatal("invalid selected scalar performed I/O")
	}
	// Interleaving root and target columns must reset qualification for every cell.
	type mixedRow struct {
		Target, Root int64
		Text         string
	}
	mixed := orm.Project3(relatedID, models.EntryFields.ID, text, func(target *int64, root int64, v *string) mixedRow {
		if target == nil || v == nil {
			t.Fatal("required route returned nil")
		}
		return mixedRow{*target, root, *v}
	})
	mixedRows, err := orm.SelectInto(ctx, source.OrderBy(models.EntryFields.ID.Asc()), mixed)
	if err != nil || len(mixedRows) != 6 {
		t.Fatal(err)
	}
	for _, row := range mixedRows {
		input := expected.Entries[numbers[row.Root]-1]
		targetIndex := expected.Holders[input.Primary].Primary
		if row.Target != data[targetIndex].ID || row.Text != expected.Inputs[targetIndex].Required[1] {
			t.Fatal("root/target column qualification mixed")
		}
	}
	cost, err := routes[0].Decimal(models.DatumFields.VDecimal)
	if err != nil {
		t.Fatal(err)
	}
	span, err := routes[0].Duration(models.DatumFields.VDuration)
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := routes[0].UUID(models.DatumFields.VUUID)
	if err != nil {
		t.Fatal(err)
	}
	document, err := routes[0].JSON(models.DatumFields.VJSON)
	if err != nil {
		t.Fatal(err)
	}
	type nativeRow struct {
		Cost *decimal.Decimal
		Span *duration.Duration
		ID   *uuid.UUID
		JSON *jsonvalue.Value
	}
	nativeProjection := orm.Project4(cost, span, identifier, document, func(a *decimal.Decimal, b *duration.Duration, c *uuid.UUID, d *jsonvalue.Value) nativeRow {
		return nativeRow{a, b, c, d}
	})
	rows, err := orm.SelectInto(ctx, source.OrderBy(models.EntryFields.ID.Asc()), nativeProjection)
	if err != nil || len(rows) != 6 || rows[0].Cost == nil || rows[0].Span == nil || rows[0].ID == nil || rows[0].JSON == nil {
		t.Fatal("mixed native scalar projection", err)
	}
	if rows[0].Cost == rows[4].Cost || rows[0].JSON == rows[4].JSON {
		t.Fatal("selected row pointers share storage")
	}
	*rows[0].JSON = jsonvalue.Null()
	if *rows[4].JSON == jsonvalue.Null() {
		t.Fatal("mutating one row changed another")
	}
	// The established exact Decimal storage policy also holds for target results.
	// Keep this Go-specific precision case separate from the public reference matrix.
	wide := decimalValue(t, "9007199254740993.125000")
	rollback := errors.New("selected native values rollback")
	var expired db.Session
	err = backend.Atomic(ctx, func(session db.Session) error {
		expired = session
		if _, err := models.DatumObjects.Update(ctx, session, data[0], models.DatumPatch{}.WithVDecimal(wide)); err != nil {
			return err
		}
		result, err := orm.SelectInto(ctx, models.EntryObjects.Using(session).OrderBy(models.EntryFields.ID.Asc()), nativeProjection)
		if err != nil {
			return err
		}
		if len(result) != 6 || result[0].Cost == nil || !result[0].Cost.Equal(wide) || result[0].Span == nil || result[0].ID == nil || result[0].JSON == nil {
			return errors.New("native target precision/session read changed")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("selected native transaction", err)
	}
	if rows, err := orm.SelectInto(ctx, models.EntryObjects.Using(expired), nativeProjection); err == nil || rows != nil {
		t.Fatal("selected native values escaped session")
	}
	after, err := orm.SelectInto(ctx, source.OrderBy(models.EntryFields.ID.Asc()), nativeProjection)
	if err != nil || len(after) != 6 || after[0].Cost == nil || !after[0].Cost.Equal(data[0].VDecimal) {
		t.Fatal("selected native rollback lost value", err)
	}
	reverse, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := orm.SelectInto(ctx, models.DatumObjects.Using(backend), orm.Project1(reverse.ModelsDatum.PrimaryHolders.ID, func(v *int64) *int64 { return v })); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || rows != nil {
		t.Fatal("reverse scalar result widened", err)
	}
}
