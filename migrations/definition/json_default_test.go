package definition_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
)

func TestJSONHistoricalDefaultsPreservePrecisionNullAndIdentity(t *testing.T) {
	producer := definition.Producer{Name: "json-test", Version: "1"}
	for _, raw := range []string{`null`, `false`, `0`, `1.0`, `1e400`, `"text"`, `[]`, `{"large":340282366920938463463374607431768211455,"nested":[null,{},"<>&\u0000"]}`} {
		t.Run(raw, func(t *testing.T) {
			value, err := jsonvalue.Parse([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.JSONField("payload", "Payload", schema.Default(value))}}}})
			if err != nil {
				t.Fatal(err)
			}
			migration := migrations.Migration{App: "refs", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "refs", Model: built.Models[0]}}}
			encoded, err := definition.Encode(producer, migration)
			if err != nil {
				t.Fatal(err)
			}
			loaded, _, err := definition.Load(definition.Source{SourceID: "json", Document: encoded})
			if err != nil {
				t.Fatal(err)
			}
			again, err := definition.Encode(producer, loaded.Definitions()[0])
			if err != nil || !bytes.Equal(encoded, again) {
				t.Fatal("historical JSON round trip changed identity", err)
			}
			operation := loaded.Definitions()[0].Operations[0].(migrations.CreateModel)
			if operation.Model.Fields[1].Default.JSON != value.Text {
				t.Fatal("JSON default lost exact numeric tokens or null")
			}
			operation.Model.Fields[1].Default.JSON = `{"changed":true}`
			if loaded.Definitions()[0].Operations[0].(migrations.CreateModel).Model.Fields[1].Default.JSON != value.Text {
				t.Fatal("historical JSON default aliases caller")
			}
			changed, err := definition.Encode(producer, migrations.Migration{App: "refs", Name: migration.Name, Operations: []migrations.Operation{operation}})
			if err != nil {
				t.Fatal(err)
			}
			other, _, err := definition.Load(definition.Source{SourceID: "changed", Document: changed})
			if err != nil || other.Digest() == loaded.Digest() {
				t.Fatal("definition digest ignores JSON default", err)
			}

			quoted, _ := json.Marshal(value.Text)
			needle := append([]byte(`"json":`), quoted...)
			for _, replacement := range []string{`""`, `" null "`, `"{\"b\":1,\"a\":2}"`, `"{\"a\":1,\"a\":2}"`, `"NaN"`, `null`, `0`, `true`, `[]`, `{}`, string(quoted) + `,"string":"foreign"`, string(quoted) + `,"json":` + string(quoted)} {
				bad := bytes.Replace(encoded, needle, []byte(`"json":`+replacement), 1)
				if bytes.Equal(encoded, bad) {
					t.Fatal("invalid JSON control did not change wire")
				}
				if _, _, err := definition.Load(definition.Source{SourceID: "invalid", Document: bad}); err == nil {
					t.Fatalf("invalid historical JSON accepted: %s", replacement)
				}
			}
		})
	}
}
