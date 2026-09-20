// Package jsontest loads independent pinned JSONField observations.
package jsontest

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
)

//go:embed testdata/django61.json
var raw []byte

type Value struct {
	Kind  string
	Value json.RawMessage
}

func (value Value) JSON(t testing.TB) jsonvalue.Value {
	t.Helper()
	encoded := value.document(t)
	result, err := jsonvalue.Parse(encoded)
	if err != nil {
		t.Fatalf("independent value is outside strict JSON: %s: %v", value.Kind, err)
	}
	return result
}

func (value Value) document(t testing.TB) []byte {
	t.Helper()
	switch value.Kind {
	case "null":
		return []byte("null")
	case "bool", "string":
		return append([]byte(nil), value.Value...)
	case "integer", "float":
		var text string
		if err := json.Unmarshal(value.Value, &text); err != nil {
			t.Fatal(err)
		}
		return []byte(text)
	case "list", "tuple":
		var items []Value
		if err := json.Unmarshal(value.Value, &items); err != nil {
			t.Fatal(err)
		}
		result := make([]json.RawMessage, len(items))
		for index, item := range items {
			result[index] = item.document(t)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	case "object":
		var members [][]Value
		if err := json.Unmarshal(value.Value, &members); err != nil {
			t.Fatal(err)
		}
		result := map[string]json.RawMessage{}
		for _, member := range members {
			if len(member) != 2 || (member[0].Kind != "string" && member[0].Kind != "integer") {
				t.Fatal("reference object key has no supported JSON spelling")
			}
			var key string
			if err := json.Unmarshal(member[0].Value, &key); err != nil {
				t.Fatal(err)
			}
			if _, exists := result[key]; exists {
				t.Fatal("reference object repeats key")
			}
			result[key] = member[1].document(t)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	default:
		t.Fatalf("unsupported independent JSON value kind: %s", value.Kind)
		return nil
	}
}

type Outcome struct {
	Value     Value
	Codes     []string
	Exception string
}

type Reference struct {
	Django, DRF, Python string
	Model               []json.RawMessage
	Form                []struct {
		Required bool
		Input    *string
		Cleaned  Outcome
		Changed  map[string]Outcome
		Bound    Outcome
		Widget   string
	}
	Serializer []json.RawMessage
	Parsed     []json.RawMessage
}

func Load(t testing.TB) Reference {
	t.Helper()
	var result Reference
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Django != "6.1" || result.DRF != "3.18.0" || result.Python != "3.14.3" || len(result.Model) != 62 || len(result.Form) != 88 || len(result.Serializer) != 192 || len(result.Parsed) != 43 {
		t.Fatal("JSON reference version or inventory changed")
	}
	return result
}
