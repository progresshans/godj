package queryplan

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/timezone"
)

func TestCompileFunctionsRetainInputKindsOrderAndBackendOwnership(t *testing.T) {
	instant, err := query.NullExpression(query.FieldDateTime)
	if err != nil {
		t.Fatal(err)
	}
	date, err := query.FunctionExpression(query.FunctionDate, instant)
	if err != nil {
		t.Fatal(err)
	}
	year, err := query.FunctionExpression(query.FunctionYear, date)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := query.Arithmetic(query.ArithmeticAdd, year, year)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []query.FieldKind
	var functions []query.FunctionKind
	dialect := ScalarDialect{
		ParameterLimit: 2,
		Field:          func(query.FieldRef) (string, error) { return "", errors.New("function fabricated a field") },
		Value:          func(value query.Value) (any, error) { return value.DatabaseValue() },
		Placeholder:    func(index int) string { return "$" + strconv.Itoa(index) },
		Literal: func(value query.Value, kind query.FieldKind, placeholder string) (string, error) {
			if !value.IsNull() {
				t.Fatal("function changed its literal")
			}
			kinds = append(kinds, kind)
			return placeholder, nil
		},
		Function: func(value query.ScalarExpression, arguments []string, _ ScalarParameterBinder) (string, error) {
			function, _, ok := value.Function()
			if !ok || len(arguments) != 1 {
				t.Fatal("backend received a different grammar")
			}
			functions = append(functions, function)
			if function == query.FunctionDate {
				return "date(" + arguments[0] + ")", nil
			}
			return "year(" + arguments[0] + ")", nil
		},
	}
	var arguments []any
	statement, err := CompileScalar(sum, query.FieldInteger, &arguments, dialect)
	if err != nil || statement != "(year(date($1)) + year(date($2)))" || !reflect.DeepEqual(arguments, []any{nil, nil}) ||
		!reflect.DeepEqual(kinds, []query.FieldKind{query.FieldDateTime, query.FieldDateTime}) ||
		!reflect.DeepEqual(functions, []query.FunctionKind{query.FunctionDate, query.FunctionYear, query.FunctionDate, query.FunctionYear}) {
		t.Fatal("function traversal changed kind, parameter order or multiplicity", statement, arguments, kinds, functions, err)
	}
	arguments = nil
	dialect.ParameterLimit = 1
	if _, err := CompileScalar(sum, query.FieldInteger, &arguments, dialect); err == nil {
		t.Fatal("function parameters bypassed the statement budget")
	}
	arguments = nil
	dialect.Function = nil
	if _, err := CompileScalar(date, query.FieldDate, &arguments, dialect); err == nil || len(arguments) != 0 {
		t.Fatal("unsupported function was compiled by the shared traversal")
	}
	sentinel := errors.New("calendar compiler refused operand")
	dialect.Function = func(query.ScalarExpression, []string, ScalarParameterBinder) (string, error) { return "", sentinel }
	if _, err := CompileScalar(date, query.FieldDate, &arguments, dialect); !errors.Is(err, sentinel) {
		t.Fatal("backend function error was lost", err)
	}
	arguments = nil
	dialect.Function = func(query.ScalarExpression, []string, ScalarParameterBinder) (string, error) {
		return strings.Repeat("x", (4<<20)+1), nil
	}
	if _, err := CompileScalar(date, query.FieldDate, &arguments, dialect); err == nil {
		t.Fatal("function SQL bypassed the statement budget")
	}
}

func TestFunctionParametersKeepZoneIdentityAndShareStatementBudget(t *testing.T) {
	zone, err := timezone.Parse("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	instant, err := query.NullExpression(query.FieldDateTime)
	if err != nil {
		t.Fatal(err)
	}
	local, err := query.FunctionExpressionInZone(query.FunctionDate, zone, instant)
	if err != nil {
		t.Fatal(err)
	}
	dialect := ScalarDialect{
		ParameterLimit: 2,
		Field:          func(query.FieldRef) (string, error) { return "", errors.New("fabricated field") },
		Value:          func(value query.Value) (any, error) { return value.DatabaseValue() },
		Placeholder:    func(index int) string { return "$" + strconv.Itoa(index) },
		Function: func(value query.ScalarExpression, inputs []string, bind ScalarParameterBinder) (string, error) {
			got, explicit := value.FunctionZone()
			if !explicit || got != zone || len(inputs) != 1 {
				t.Fatal("compiler lost explicit zone or argument")
			}
			parameter, err := bind(query.String(got.Name()))
			if err != nil {
				return "", err
			}
			return "local_date(" + inputs[0] + ", " + parameter + ")", nil
		},
	}
	var arguments []any
	statement, err := CompileScalar(local, query.FieldDate, &arguments, dialect)
	if err != nil || statement != "local_date($1, $2)" || !reflect.DeepEqual(arguments, []any{nil, "Asia/Seoul"}) {
		t.Fatal("function parameters changed order or value", statement, arguments, err)
	}
	dialect.ParameterLimit = 1
	arguments = nil
	if _, err := CompileScalar(local, query.FieldDate, &arguments, dialect); err == nil {
		t.Fatal("zone parameter bypassed statement budget")
	}
	dialect.ParameterLimit = 2
	sentinel := errors.New("zone parameter conversion failed")
	dialect.Value = func(value query.Value) (any, error) {
		if value.Kind() == query.ValueString {
			return nil, sentinel
		}
		return value.DatabaseValue()
	}
	arguments = nil
	if _, err := CompileScalar(local, query.FieldDate, &arguments, dialect); !errors.Is(err, sentinel) {
		t.Fatal("function parameter error lost", err)
	}
}
