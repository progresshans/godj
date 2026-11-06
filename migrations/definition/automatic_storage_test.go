package definition_test

import (
	"bytes"
	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
	"strings"
	"testing"
)

func TestAutomaticCreateDefinitionClosedShapeDigestAndBounds(t *testing.T) {
	history, models, field := manytomanytest.AutomaticHistory(t, true, false)
	models[0].ManyToMany = []ir.ManyToManyField{field}
	change := history[0]
	change.Operations[0] = migrations.CreateModel{AppLabel: change.App, Model: models[0]}
	producer := definition.Producer{Name: "automatic", Version: "1"}
	wire, err := definition.Encode(producer, change)
	if err != nil {
		t.Fatal(err)
	}
	load := func(data []byte) (migrations.LoadedDefinitionSet, error) {
		set, _, err := definition.Load(definition.Source{SourceID: "create", Document: data})
		return set, err
	}
	set, err := load(wire)
	if err != nil {
		t.Fatal(err)
	}
	r, err := set.Reconstructor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := r.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := state.Model("manyhistory", "owner")
	if !owner.Equal(models[0]) {
		t.Fatal("round trip lost metadata")
	}
	for _, pair := range [][2]string{
		{`"go_name":"Labels"`, `"go_name":"Other"`},
		{`"symmetry":"symmetrical"`, `"symmetry":"directed"`},
	} {
		next := bytes.Replace(wire, []byte(pair[0]), []byte(pair[1]), 1)
		changed, err := load(next)
		if err != nil || changed.Digest() == set.Digest() {
			t.Fatal("digest omitted inline metadata", err)
		}
	}
	for _, pair := range [][2]string{
		{`"many_to_many":[{`, `"many_to_many":[{"column":"fake",`},
		{`"name":"labels"`, `"name":null`},
		{`"model_name":"owner"`, `"model_name":"owner","model_name":"label"`},
		{`"reverse":{"disabled":true}`, `"reverse":{"disabled":true,"future":1}`},
	} {
		bad := bytes.Replace(wire, []byte(pair[0]), []byte(pair[1]), 1)
		if bytes.Equal(bad, wire) {
			t.Fatal("negative control absent", pair)
		}
		if loaded, err := load(bad); err == nil || loaded.Digest() != "" {
			t.Fatal("malformed nested Create metadata accepted", err)
		}
	}
	oversized := models[0].Clone()
	oversized.ManyToMany[0].Name = strings.Repeat("x", definition.MaxDocumentBytes+1)
	change.Operations[0] = migrations.CreateModel{AppLabel: change.App, Model: oversized}
	if data, err := definition.Encode(producer, change); err == nil || data != nil || !strings.Contains(err.Error(), "resource limit") {
		t.Fatal("Create metadata escaped prescan", err)
	}
}
