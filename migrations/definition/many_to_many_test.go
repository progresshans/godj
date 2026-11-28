package definition_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

func TestManyToManyDefinitionRoundTripBindingDigestAndOwnership(t *testing.T) {
	history, _, _ := manytomanytest.History(t, true, false, false)
	producer := definition.Producer{Name: "many-history", Version: "1"}
	sources := make([]definition.Source, len(history))
	for i, change := range history {
		wire, err := definition.Encode(producer, change)
		if err != nil {
			t.Fatal(err)
		}
		sources[i] = definition.Source{SourceID: change.Name, Document: wire}
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	history[1].Operations[0].(*migrations.AddManyToMany).Field.Through.SourceField = "forged"
	exported := loaded.Definitions()
	add := exported[1].Operations[0].(migrations.AddManyToMany)
	add.Field.Through.TargetField = "forged"
	for i, change := range loaded.Definitions() {
		wire, err := definition.Encode(producer, change)
		if err != nil || !bytes.Equal(wire, sources[i].Document) {
			t.Fatal("definition exposes nested through metadata", err)
		}
	}
	reconstructor, err := loaded.Reconstructor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"", "labels", "tags", ""} {
		state, err := reconstructor.Reconstruct(migrations.AfterStateRequest(history[i].Key()))
		if err != nil {
			t.Fatal(err)
		}
		owner, _ := state.Model("manyhistory", "owner")
		if want == "" && len(owner.ManyToMany) != 0 || want != "" && (len(owner.ManyToMany) != 1 || owner.ManyToMany[0].Name != want || owner.ManyToMany[0].Through.SourceField != "owner") {
			t.Fatal("historical declaration changed", i, owner)
		}
	}
	// Digest admission is independent of readiness: every declared coordinate
	// must change the sealed definition even when a new target is not yet valid.
	for _, pair := range [][2]string{{`"source_field":"owner"`, `"source_field":"other"`}, {`"target_field":"label"`, `"target_field":"other"`}, {`"model_name":"membership"`, `"model_name":"other"`}, {`"name":"owner_set"`, `"name":"other_set"`}, {`"go_name":"Labels"`, `"go_name":"Other"`}} {
		changed := append([]definition.Source(nil), sources...)
		changed[1].Document = bytes.ReplaceAll(sources[1].Document, []byte(pair[0]), []byte(pair[1]))
		if bytes.Equal(changed[1].Document, sources[1].Document) {
			t.Fatal("negative control did not change input", pair)
		}
		set, _, err := definition.Load(changed...)
		if err != nil || set.Digest() == loaded.Digest() {
			t.Fatal("binding absent from semantic digest", pair, err)
		}
	}
}

func TestManyToManyDefinitionRejectsMalformedNestedBindings(t *testing.T) {
	history, _, _ := manytomanytest.History(t, false, true, false)
	history[1].Dependencies = nil
	history[2].Dependencies = nil
	wire, err := definition.Encode(definition.Producer{Name: "test", Version: "1"}, history[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := definition.Load(definition.Source{SourceID: "valid", Document: wire}); err != nil {
		t.Fatal("positive control", err)
	}
	for _, pair := range [][2]string{
		{`"target":{"app_label":"manyhistory","model_name":"label"}`, `"target":{"app_label":"manyhistory","model_name":"label","future":true}`},
		{`"source_field":"owner"`, `"source_field":"owner","source_field":"label"`},
		{`"source_field":"owner"`, `"source_field":null`},
		{`"source_field":"owner"`, `"source_field":"label"`},
		{`"symmetry":"directed"`, `"symmetry":"symmetrical"`},
		{`"reverse":{"name":"owner_set"}`, `"reverse":{"name":"owner_set","disabled":true}`},
		{`"model_name":"membership"`, `"model_name":"membership","extra":false`},
		{`"field":{`, `"field":{"column":"fake",`},
		{`"model_name":"owner"`, `"model_name":"owner","before_field":"labels"`},
	} {
		bad := bytes.Replace(wire, []byte(pair[0]), []byte(pair[1]), 1)
		if bytes.Equal(bad, wire) {
			t.Fatal("negative control did not modify input", pair)
		}
		set, _, err := definition.Load(definition.Source{SourceID: "invalid", Document: bad})
		if err == nil || set.Digest() != "" {
			t.Fatal("invalid nested relation published history", pair, err)
		}
	}
	rename, err := definition.Encode(definition.Producer{Name: "test", Version: "1"}, history[2])
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(rename, &doc); err != nil {
		t.Fatal(err)
	}
	operation := doc["migration"].(map[string]any)["operations"].([]any)[0].(map[string]any)
	operation["after"].(map[string]any)["through"].(map[string]any)["target_field"] = "other"
	bad, _ := json.Marshal(doc)
	if _, _, err := definition.Load(definition.Source{SourceID: "retarget", Document: bad}); err == nil {
		t.Fatal("rename silently retargeted intermediary")
	}
}

func TestManyToManyDefinitionBoundsBeforeCopyingAndRejectsNil(t *testing.T) {
	history, _, field := manytomanytest.History(t, false, true, false)
	for _, kind := range []string{"add", "remove", "rename"} {
		change := history[1]
		field := field.Clone()
		field.Through.Model.ModelName = strings.Repeat("x", definition.MaxDocumentBytes+1)
		switch kind {
		case "add":
			change.Operations = []migrations.Operation{&migrations.AddManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: field}}
		case "remove":
			change.Operations = []migrations.Operation{&migrations.RemoveManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: field}}
		case "rename":
			after := field.Clone()
			after.Name = "other"
			change.Operations = []migrations.Operation{&migrations.RenameManyToMany{AppLabel: "manyhistory", ModelName: "owner", Before: field, After: after}}
		}
		if wire, err := definition.Encode(definition.Producer{Name: "test", Version: "1"}, change); err == nil || wire != nil || !strings.Contains(err.Error(), "resource limit") {
			t.Fatal("oversized through escaped encoding budget", err)
		}
		if _, err := migrations.NewStateReconstructor(change); err == nil || !strings.Contains(err.Error(), "resource limit") {
			t.Fatal("oversized through escaped reconstruction budget", err)
		}
	}
	for _, op := range []migrations.Operation{(*migrations.AddManyToMany)(nil), (*migrations.RemoveManyToMany)(nil), (*migrations.RenameManyToMany)(nil)} {
		change := history[1]
		change.Operations = []migrations.Operation{op}
		if wire, err := definition.Encode(definition.Producer{Name: "test", Version: "1"}, change); err == nil || wire != nil {
			t.Fatal(fmt.Sprintf("nil operation %T accepted", op))
		}
	}
}
