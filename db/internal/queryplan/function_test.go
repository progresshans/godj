package queryplan

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
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
		Function: func(value query.ScalarExpression, arguments []string) (string, error) {
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
	dialect.Function = func(query.ScalarExpression, []string) (string, error) { return "", sentinel }
	if _, err := CompileScalar(date, query.FieldDate, &arguments, dialect); !errors.Is(err, sentinel) {
		t.Fatal("backend function error was lost", err)
	}
	arguments = nil
	dialect.Function = func(query.ScalarExpression, []string) (string, error) { return strings.Repeat("x", (4<<20)+1), nil }
	if _, err := CompileScalar(date, query.FieldDate, &arguments, dialect); err == nil {
		t.Fatal("function SQL bypassed the statement budget")
	}
}
