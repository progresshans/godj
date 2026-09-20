// Package uniquetest holds the independent uniqueness observations and their
// typed Go inputs. Backend tests still own actual migration, I/O and recovery.
package uniquetest

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/uuidinput"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/django61-postgres.json
var postgresReference []byte

//go:embed testdata/django61-sqlite.json
var sqliteReference []byte

type Attempt struct {
	Index     int             `json:"index"`
	Input     json.RawMessage `json:"input"`
	Saved     bool            `json:"saved"`
	SQLState  string          `json:"sqlstate"`
	Exception string          `json:"exception"`
}

type Profile struct {
	Name     string    `json:"name"`
	Attempts []Attempt `json:"attempts"`
}

func Profiles(t testing.TB, backend string) []Profile {
	t.Helper()
	raw := postgresReference
	if backend == "sqlite" {
		raw = sqliteReference
	} else if backend != "postgres" {
		t.Fatal("unknown uniqueness reference backend")
	}
	var reference struct {
		Django, Backend string
		Profiles        []Profile
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	want := []string{"char", "text", "integer", "boolean", "float", "decimal", "uuid", "date", "datetime", "time", "duration", "json", "json_canonical"}
	if reference.Django != "6.1" || reference.Backend != map[string]string{"postgres": "postgresql", "sqlite": "sqlite"}[backend] || len(reference.Profiles) != len(want) {
		t.Fatal("uniqueness reference version/backend/roster changed")
	}
	total := 0
	for index, profile := range reference.Profiles {
		if profile.Name != want[index] || len(profile.Attempts) < 2 {
			t.Fatal("uniqueness profile omitted or reordered")
		}
		for index, attempt := range profile.Attempts {
			if attempt.Index != index || len(attempt.Input) == 0 || !attempt.Saved && attempt.Exception != "IntegrityError" {
				t.Fatal("uniqueness attempt is incomplete")
			}
		}
		total += len(profile.Attempts)
	}
	if total != 96 {
		t.Fatal("uniqueness reference attempt roster changed")
	}
	return reference.Profiles
}

func Field(t testing.TB, name string) (schema.Field, query.FieldRef) {
	t.Helper()
	var field schema.Field
	var kind query.FieldKind
	switch name {
	case "char":
		field, kind = schema.CharField("value", "Value", 60), query.FieldString
	case "text":
		field, kind = schema.TextField("value", "Value"), query.FieldString
	case "integer":
		field, kind = schema.IntegerField("value", "Value"), query.FieldInteger
	case "boolean":
		field, kind = schema.BooleanField("value", "Value"), query.FieldBoolean
	case "float":
		field, kind = schema.FloatField("value", "Value"), query.FieldFloat
	case "decimal":
		field, kind = schema.DecimalField("value", "Value", 14, 2), query.FieldDecimal
	case "uuid":
		field, kind = schema.UUIDField("value", "Value"), query.FieldUUID
	case "date":
		field, kind = schema.DateField("value", "Value"), query.FieldDate
	case "datetime":
		field, kind = schema.DateTimeField("value", "Value"), query.FieldDateTime
	case "time":
		field, kind = schema.TimeField("value", "Value"), query.FieldTime
	case "duration":
		field, kind = schema.DurationField("value", "Value"), query.FieldDuration
	case "json", "json_canonical":
		field, kind = schema.JSONField("value", "Value"), query.FieldJSON
	default:
		t.Fatal("unknown uniqueness field profile", name)
	}
	schema.Nullable()(&field)
	schema.Unique()(&field)
	if kind == query.FieldDecimal {
		return field, query.NewDecimalFieldRef("value", "value", true, 14, 2)
	}
	return field, query.NewFieldRef("value", "value", kind, true)
}

func Value(t testing.TB, profile string, raw json.RawMessage) query.Value {
	t.Helper()
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	if value == nil {
		return query.Null()
	}
	if profile == "json" || profile == "json_canonical" {
		wire, err := json.Marshal(jsonInput(t, value))
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := jsonvalue.Parse(wire)
		if err != nil {
			t.Fatal(err)
		}
		return query.JSON(parsed)
	}
	text := func(tag string) string {
		t.Helper()
		if plain, ok := value.(string); ok {
			return plain
		}
		tagged, ok := value.(map[string]any)
		if !ok || len(tagged) != 1 {
			t.Fatal("invalid tagged uniqueness input", profile)
		}
		s, ok := tagged[tag].(string)
		if !ok {
			t.Fatal("missing uniqueness input tag", profile, tag)
		}
		return s
	}
	switch profile {
	case "char", "text":
		return query.String(text("string"))
	case "integer":
		v, err := strconv.ParseInt(text("integer"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return query.Integer(v)
	case "boolean":
		v, ok := value.(map[string]any)["boolean"].(bool)
		if !ok {
			t.Fatal("invalid Boolean reference")
		}
		return query.Boolean(v)
	case "float":
		tag := "float"
		if _, ok := value.(map[string]any)["integer"]; ok {
			tag = "integer"
		}
		v, err := strconv.ParseFloat(text(tag), 64)
		if err != nil {
			t.Fatal(err)
		}
		return query.Float(v)
	case "decimal":
		v, err := decimal.Parse(text("decimal"))
		if err != nil {
			t.Fatal(err)
		}
		return query.Decimal(v)
	case "uuid":
		// Reference strings are input spellings. Convert through the existing
		// form/API input boundary before testing the typed UUID storage value.
		v, err := uuidinput.Parse(text("uuid"))
		if err != nil {
			t.Fatal(err)
		}
		return query.UUID(v)
	case "date":
		v, err := calendar.Parse(text("date"))
		if err != nil {
			t.Fatal(err)
		}
		return query.Date(v)
	case "datetime":
		v, err := time.Parse(time.RFC3339Nano, text("datetime"))
		if err != nil {
			t.Fatal(err)
		}
		return query.DateTime(v)
	case "time":
		v, err := clock.Parse(text("time"))
		if err != nil {
			t.Fatal(err)
		}
		return query.Time(v)
	case "duration":
		number, ok := value.(map[string]any)["microseconds"].(json.Number)
		if !ok {
			t.Fatal("invalid duration reference")
		}
		v, err := number.Int64()
		if err != nil {
			t.Fatal(err)
		}
		return query.Duration(duration.FromMicroseconds(v))
	default:
		t.Fatal("unknown uniqueness value profile", profile)
	}
	return query.Value{}
}

func jsonInput(t testing.TB, value any) any {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		if len(value) == 1 {
			for _, key := range []string{"integer", "float"} {
				if raw, ok := value[key].(string); ok {
					return json.Number(raw)
				}
			}
			if boolean, ok := value["boolean"].(bool); ok {
				return boolean
			}
			if null, ok := value["json_null"].(bool); ok && null {
				return nil
			}
		}
		result := make(map[string]any, len(value))
		for key, child := range value {
			result[key] = jsonInput(t, child)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			result[index] = jsonInput(t, child)
		}
		return result
	case string, nil:
		return value
	default:
		t.Fatal("untagged JSON reference scalar")
	}
	return nil
}

func Model(t testing.TB, profile string) (ir.Model, query.FieldRef) {
	t.Helper()
	field, ref := Field(t, profile)
	s, err := schema.Build(schema.Definition{AppLabel: "uniqueref", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{field}}}})
	if err != nil {
		t.Fatal(err)
	}
	return s.Models[0], ref
}
