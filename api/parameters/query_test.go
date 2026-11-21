package parameters_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/validation"
)

type queryDTO struct {
	Page    int64
	Minimum *int64
	Search  *string
	Token   string
}

func TestPresenceAndAtomicAssignment(t *testing.T) {
	var assignments atomic.Int64
	query, err := parameters.New(256,
		parameters.Field("page", parameters.Default(parameters.CanonicalInt64(1, 50), 1), func(out *queryDTO, value int64) { assignments.Add(1); out.Page = value }, ""),
		parameters.Field("min", parameters.Optional(parameters.CanonicalInt64(math.MinInt64, math.MaxInt64)), func(out *queryDTO, value *int64) { assignments.Add(1); out.Minimum = value }, ""),
		parameters.Field("search", parameters.Optional(parameters.String(8, true)), func(out *queryDTO, value *string) { assignments.Add(1); out.Search = value }, ""),
		parameters.Field("token", parameters.Required(parameters.String(8, false)), func(out *queryDTO, value string) { assignments.Add(1); out.Token = value }, ""),
	)
	if err != nil || assignments.Load() != 0 {
		t.Fatal("preparation invoked a setter", err)
	}
	for _, test := range []struct{ raw, field, code string }{
		{"", "token", "required"}, {"token", "token", "invalid"},
		{"token=ok&page=", "page", "invalid"}, {"token=ok&page=02", "page", "invalid"},
		{"token=ok&min=bad&page=bad", "page", "invalid"},
		{"token=ok&search=%ff", "search", "invalid"}, {"token=ok&search=%00", "search", "invalid"},
		{"token=ok&search=123456789", "search", "invalid"},
		{"page=bad&unknown=x", "__all__", "invalid"}, {"token=ok&token=ok", "__all__", "invalid"},
		{"token=ok&%74oken=ok", "__all__", "invalid"}, {"token=ok&Token=ok", "__all__", "invalid"},
		{"token=%ZZ", "__all__", "invalid"}, {"token=ok;min=1", "__all__", "invalid"},
		{strings.Repeat("&", 257), "__all__", "invalid"},
	} {
		t.Run(test.raw, func(t *testing.T) {
			value, violations, err := query.Parse(test.raw)
			if err != nil || !reflect.DeepEqual(value, queryDTO{}) || assignments.Load() != 0 {
				t.Fatal("failed input published or assigned a DTO", value, err)
			}
			checkViolation(t, violations, test.field, test.code)
		})
	}
	first, violations, err := query.Parse("token=ok")
	if err != nil || !violations.Empty() || first.Page != 1 || first.Minimum != nil || first.Search != nil || first.Token != "ok" || assignments.Load() != 4 {
		t.Fatal("omission/default", first, violations, err)
	}
	second, violations, err := query.Parse("&token=ok&&page=%32&min=0&search=&")
	if err != nil || !violations.Empty() || second.Page != 2 || second.Minimum == nil || *second.Minimum != 0 || second.Search == nil || *second.Search != "" || assignments.Load() != 8 {
		t.Fatal("present zero and empty", second, violations, err)
	}
	*second.Minimum, *second.Search = 9, "changed"
	third, violations, err := query.Parse("token=ok&min=0&search=")
	if err != nil || !violations.Empty() || third.Minimum == nil || *third.Minimum != 0 || third.Search == nil || *third.Search != "" {
		t.Fatal("request values share mutable state", third, err)
	}
}

func TestIntegerGrammarsAndExactBounds(t *testing.T) {
	for _, test := range []struct {
		name    string
		codec   parameters.Codec[int64]
		valid   map[string]int64
		invalid []string
	}{
		{"canonical", parameters.CanonicalInt64(math.MinInt64, math.MaxInt64), map[string]int64{"0": 0, "1": 1, "-1": -1, "9223372036854775807": math.MaxInt64, "-9223372036854775808": math.MinInt64}, []string{"", "00", "01", "-0", "-01", "+1", "%2B1", "1.0", "1e0", "%201", "1%20", "9223372036854775808", "-9223372036854775809", "%D9%A1", "%ff", "%00"}},
		{"digits", parameters.DigitsInt64(0, math.MaxInt64), map[string]int64{"0": 0, "00": 0, "0001": 1, "0009223372036854775807": math.MaxInt64}, []string{"", "-0", "-1", "+1", "%2B1", "1.0", "1e0", "9223372036854775808", "%201", "%D9%A1", "%ff"}},
		{"range", parameters.DigitsInt64(1, 100), map[string]int64{"1": 1, "00100": 100}, []string{"0", "101", "2147483648"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			query, err := parameters.New(256, parameters.Field("n", parameters.Required(test.codec), func(out *int64, value int64) { *out = value }, ""))
			if err != nil {
				t.Fatal(err)
			}
			for raw, want := range test.valid {
				got, violations, err := query.Parse("n=" + raw)
				if err != nil || !violations.Empty() || got != want {
					t.Fatal(raw, got, violations, err)
				}
			}
			for _, raw := range test.invalid {
				got, violations, err := query.Parse("n=" + raw)
				if err != nil || got != 0 {
					t.Fatal(raw, got, err)
				}
				checkViolation(t, violations, "n", "invalid")
			}
		})
	}
}

func TestStringBytesURLDecodingAndRawBudget(t *testing.T) {
	query, err := parameters.New(64, parameters.Field("q", parameters.Default(parameters.String(4, true), "yes"), func(out *string, value string) { *out = value }, ""))
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]string{"": "yes", "q": "", "q=": "", "q=éé": "éé", "q=%F0%9F%98%80": "😀", "q=a+b": "a b", "q=%2B": "+", "q=%3B": ";", "q=%20a%20": " a ", "q=null": "null", "q=%0A": "\n"} {
		value, violations, err := query.Parse(raw)
		if err != nil || !violations.Empty() || value != want {
			t.Fatal(raw, value, violations, err)
		}
	}
	for _, raw := range []string{"q=ééa", "q=😀a", "q=%ff", "q=\xff", "q=%00", "q=\x00"} {
		value, violations, err := query.Parse(raw)
		if err != nil || value != "" {
			t.Fatal(raw, value, err)
		}
		checkViolation(t, violations, "q", "invalid")
	}
	bounded, err := parameters.New(6, parameters.Field("q", parameters.Required(parameters.String(5, true)), func(out *string, value string) { *out = value }, ""))
	if err != nil {
		t.Fatal(err)
	}
	if value, violations, err := bounded.Parse("q=abcd"); err != nil || !violations.Empty() || value != "abcd" {
		t.Fatal("inclusive raw budget", value, violations, err)
	}
	if value, violations, err := bounded.Parse("q=abcde"); err != nil || value != "" {
		t.Fatal(value, err)
	} else {
		checkViolation(t, violations, "__all__", "invalid")
	}
	closed, err := parameters.New[int64](10)
	if err != nil {
		t.Fatal(err)
	}
	if value, violations, err := closed.Parse("&&"); err != nil || !violations.Empty() || value != 0 {
		t.Fatal("empty declaration", err)
	}
	if _, violations, err := closed.Parse("x=1"); err != nil {
		t.Fatal(err)
	} else {
		checkViolation(t, violations, "__all__", "invalid")
	}
}

func TestPreparationRejectsInvalidDeclarations(t *testing.T) {
	assignments := 0
	setter := func(out *int64, value int64) { assignments++; *out = value }
	valid := parameters.Field("n", parameters.Default(parameters.CanonicalInt64(1, 3), 2), setter, "")
	for name, declarations := range map[string][]parameters.Parameter[int64]{
		"zero parameter":          {{}},
		"zero input":              {parameters.Field("n", parameters.Input[int64]{}, setter, "")},
		"zero required codec":     {parameters.Field("n", parameters.Required(parameters.Codec[int64]{}), setter, "")},
		"zero default codec":      {parameters.Field("n", parameters.Default(parameters.Codec[int64]{}, 1), setter, "")},
		"nil setter":              {parameters.Field[int64]("n", parameters.Required(parameters.CanonicalInt64(1, 3)), nil, "")},
		"reversed range":          {parameters.Field("n", parameters.Required(parameters.CanonicalInt64(3, 1)), setter, "")},
		"negative unsigned range": {parameters.Field("n", parameters.Required(parameters.DigitsInt64(-1, 3)), setter, "")},
		"invalid default":         {parameters.Field("n", parameters.Default(parameters.CanonicalInt64(1, 3), 0), setter, "")},
		"duplicate":               {valid, valid},
		"empty name":              {parameters.Field("", parameters.Required(parameters.CanonicalInt64(1, 3)), setter, "")},
		"too long name":           {parameters.Field(strings.Repeat("n", 129), parameters.Required(parameters.CanonicalInt64(1, 3)), setter, "")},
		"invalid name":            {parameters.Field("no?", parameters.Required(parameters.CanonicalInt64(1, 3)), setter, "")},
		"invalid description":     {parameters.Field("n", parameters.Required(parameters.CanonicalInt64(1, 3)), setter, "\x00")},
		"too long description":    {parameters.Field("n", parameters.Required(parameters.CanonicalInt64(1, 3)), setter, strings.Repeat("x", 4097))},
	} {
		t.Run(name, func(t *testing.T) {
			query, err := parameters.New(64, declarations...)
			if !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
				t.Fatal("invalid declaration accepted", err)
			}
			if value, violations, err := query.Parse("n=1"); value != 0 || !violations.Empty() || !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
				t.Fatal("invalid query became usable", err)
			}
		})
	}
	for _, budget := range []int{-1, 0, 1<<20 + 1} {
		if _, err := parameters.New(budget, valid); err == nil {
			t.Fatal("invalid query budget", budget)
		}
	}
	if _, err := parameters.New(64, valid, valid); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig, Field: "parameters[1]"}) {
		t.Fatal("configuration error did not identify the invalid declaration", err)
	}
	tooMany := make([]parameters.Parameter[int64], 129)
	for i := range tooMany {
		tooMany[i] = parameters.Field(fmt.Sprintf("p%d", i), parameters.Required(parameters.CanonicalInt64(0, 1)), setter, "")
	}
	if _, err := parameters.New(128, tooMany...); err == nil {
		t.Fatal("parameter count was not bounded")
	}
	for _, input := range []parameters.Input[string]{parameters.Required(parameters.String(0, true)), parameters.Default(parameters.String(3, true), "four"), parameters.Default(parameters.String(3, false), ""), parameters.Default(parameters.String(3, true), "\xff"), parameters.Default(parameters.String(3, true), "\x00")} {
		if _, err := parameters.New(64, parameters.Field("q", input, func(out *string, value string) { assignments++; *out = value }, "")); err == nil {
			t.Fatal("invalid string declaration accepted")
		}
	}
	if _, err := parameters.New(64, parameters.Field("q", parameters.Optional(parameters.Codec[string]{}), func(out **string, value *string) { assignments++; *out = value }, "")); err == nil {
		t.Fatal("invalid optional codec accepted")
	}
	if assignments != 0 {
		t.Fatal("invalid preparation called setters")
	}
}

func TestSchemaAndConcurrentOwnership(t *testing.T) {
	fields := []parameters.Parameter[queryDTO]{
		parameters.Field("page", parameters.Default(parameters.DigitsInt64(1, math.MaxInt64), math.MaxInt64), func(out *queryDTO, value int64) { out.Page = value }, "Page."),
		parameters.Field("search", parameters.Optional(parameters.String(4, true)), func(out *queryDTO, value *string) { out.Search = value }, "Search."),
		parameters.Field("token", parameters.Required(parameters.String(8, false)), func(out *queryDTO, value string) { out.Token = value }, "Token."),
	}
	query, err := parameters.New(128, fields...)
	if err != nil {
		t.Fatal(err)
	}
	fields[0] = parameters.Parameter[queryDTO]{}
	metadata := query.Parameters()
	if query.MaxBytes() != 128 || len(metadata) != 3 || metadata[0].Required || metadata[0].AllowEmptyValue || metadata[1].Required || !metadata[1].AllowEmptyValue || !metadata[2].Required || metadata[2].AllowEmptyValue {
		t.Fatal("parameter presence/empty policy", metadata)
	}
	integer, _ := metadata[0].Schema.Value().AsObject()
	for _, key := range []string{"default", "maximum"} {
		value, _ := integer.Get(key)
		number, valid := value.AsInteger()
		if !valid || number != math.MaxInt64 {
			t.Fatal("schema lost exact int64", key, value)
		}
	}
	grammar, _ := integer.Get("x-godj-query-integer")
	text, _ := grammar.AsString()
	if text != "unsigned-digits" {
		t.Fatal("schema lost integer grammar")
	}
	str, _ := metadata[1].Schema.Value().AsObject()
	for _, key := range []string{"maxLength", "x-godj-max-bytes"} {
		value, _ := str.Get(key)
		number, _ := value.AsInteger()
		if number != 4 {
			t.Fatal("schema lost byte constraint", key)
		}
	}
	if _, present := str.Get("default"); present {
		t.Fatal("optional parameter gained a default")
	}
	for _, parameter := range metadata {
		if err := openapi.ValidateParameter(parameter); err != nil {
			t.Fatal(err)
		}
	}
	metadata[0].Name, metadata[0].Schema = "changed", openapi.Boolean()
	if query.Parameters()[0].Name != "page" {
		t.Fatal("metadata mutation changed prepared query")
	}
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Go(func() {
			for j := range 12 {
				value, violations, err := query.Parse(fmt.Sprintf("token=%d&search=éé", i*12+j))
				if err != nil || !violations.Empty() || value.Page != math.MaxInt64 || value.Search == nil || *value.Search != "éé" {
					t.Error("concurrent parse", value, err)
					return
				}
				*value.Search = "owned"
			}
		})
	}
	wg.Wait()
}

func checkViolation(t *testing.T, violations validation.Errors, field, code string) {
	t.Helper()
	violation, valid := violations.At(0)
	if violations.Len() != 1 || !valid || string(violation.Field()) != field || string(violation.Code()) != code || len(violation.Params()) != 0 {
		t.Fatalf("diagnostic = %#v, want %s/%s without input values", violations.All(), field, code)
	}
}
