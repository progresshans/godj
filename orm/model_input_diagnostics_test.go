package orm_test

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
)

func TestModelInputHidesSelectedValuesAcrossDiagnosticVerbs(t *testing.T) {
	const marker = "model-input-diagnostic-private-value"
	input, err := models.UserObjects.Input(map[string]query.Value{"first_name": query.String(marker)})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := input.WithTargetKey(query.Integer(0))
	if err != nil {
		t.Fatal(err)
	}
	collected, err := bound.WithCollections(map[string][]int64{"groups": {7, 0}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := models.UserObjects.WithServerValues(collected, map[string]query.Value{"revision": query.Integer(2)})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := input.TargetKey(); present {
		t.Fatal("derived target changed original input")
	}
	if _, present := bound.Collection("groups"); present {
		t.Fatal("derived membership changed original input")
	}
	if _, present := collected.Get("revision"); present {
		t.Fatal("server values changed original input")
	}
	if key, present := server.TargetKey(); !present || !key.Equal(query.Integer(0)) {
		t.Fatal("derived input lost target")
	}
	for _, value := range []any{input, &input, bound, collected, server} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%f", "%o", "%p", "%w", "%020d", "%#[1]v"} {
			printed := fmt.Sprintf(format, value)
			if strings.Contains(printed, marker) || strings.Contains(printed, hex.EncodeToString([]byte(marker))) {
				t.Fatal("typed input diagnostic disclosed selected value", format)
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != "{}" {
			t.Fatal("typed input bypassed explicit output", err)
		}
	}
}
