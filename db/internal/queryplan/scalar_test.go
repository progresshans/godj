package queryplan

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestCompileScalarPreservesMixedTemporalNullTypes(t *testing.T) {
	instant, err := query.NullExpression(query.FieldDateTime)
	if err != nil {
		t.Fatal(err)
	}
	period, err := query.NullExpression(query.FieldDuration)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		operator    query.ArithmeticOperator
		left, right query.ScalarExpression
		kinds       []query.FieldKind
	}{
		{"shift", query.ArithmeticAdd, instant, period, []query.FieldKind{query.FieldDateTime, query.FieldDuration}},
		{"reverse_shift", query.ArithmeticAdd, period, instant, []query.FieldKind{query.FieldDuration, query.FieldDateTime}},
		{"difference", query.ArithmeticSubtract, instant, instant, []query.FieldKind{query.FieldDateTime, query.FieldDateTime}},
	} {
		t.Run(test.name, func(t *testing.T) {
			expression, err := query.Arithmetic(test.operator, test.left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			var kinds []query.FieldKind
			var arguments []any
			statement, err := CompileScalar(expression, expression.ResultKind(), &arguments, ScalarDialect{
				ParameterLimit: 2,
				Field:          func(query.FieldRef) (string, error) { t.Fatal("NULL acquired field authority"); return "", nil },
				Value:          func(value query.Value) (any, error) { return value.DatabaseValue() },
				Placeholder:    func(index int) string { return "$" + strconv.Itoa(index) },
				Literal: func(value query.Value, kind query.FieldKind, parameter string) (string, error) {
					if !value.IsNull() {
						t.Fatal("NULL changed value")
					}
					kinds = append(kinds, kind)
					return parameter, nil
				},
			})
			if err != nil || !reflect.DeepEqual(kinds, test.kinds) || !reflect.DeepEqual(arguments, []any{nil, nil}) || statement != "($1 "+string(test.operator)+" $2)" {
				t.Fatal(statement, kinds, arguments, err)
			}
		})
	}
}
