package jsoninput_test

import (
	"github.com/progresshans/godj/internal/jsoninput"
	"github.com/progresshans/godj/jsonvalue"
	"testing"
)

func TestJSONInputComparisonKeepsExactKindsAndUnboundedExponentMagnitude(t *testing.T) {
	for _, test := range []struct {
		left, right string
		equal       bool
	}{
		{`1.0`, `1e0`, true}, {`1.00`, `10e-1`, true}, {`0.001e3`, `1.0`, true}, {`-0`, `0`, true},
		{`-0.0`, `-0e9999999999999999999999999999999999999`, true},
		{`-0.0`, `0.0`, false}, {`0`, `0.0`, false}, {`1`, `1e0`, false}, {`true`, `1`, false}, {`false`, `0`, false},
		{`1e-400`, `0.0`, false}, {`9007199254740993.0`, `9007199254740992.0`, false},
		{`1e9999999999999999999999999999999999999`, `10e9999999999999999999999999999999999998`, true},
		{`{"b":[1e0,null],"":false}`, `{"":false,"b":[1.00,null]}`, true},
		{`{"b":[1,null]}`, `{"b":[1.0,null]}`, false}, {`null`, `"null"`, false}, {`[]`, `{}`, false},
	} {
		left, err := jsonvalue.Parse([]byte(test.left))
		if err != nil {
			t.Fatal(err)
		}
		right, err := jsonvalue.Parse([]byte(test.right))
		if err != nil {
			t.Fatal(err)
		}
		if jsoninput.Equal(left, right) != test.equal || jsoninput.Equal(right, left) != test.equal {
			t.Fatalf("JSON input comparison changed: %s %s", test.left, test.right)
		}
	}
	if jsoninput.Equal(jsonvalue.Value{}, jsonvalue.Value{}) {
		t.Fatal("invalid JSON values compare as an unchanged form")
	}
}
