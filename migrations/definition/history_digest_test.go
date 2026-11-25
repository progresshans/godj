package definition

import (
	"bytes"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/migrations"
)

func TestLoadedHistoryDigestsBindSharedAncestorsAndIgnoreCatalogExtensions(t *testing.T) {
	root := migrations.MigrationKey{App: "zapp", Name: "0001_root"}
	left := migrations.MigrationKey{App: "beta", Name: "0001_left"}
	right := migrations.MigrationKey{App: "gamma", Name: "0001_right"}
	join := migrations.MigrationKey{App: "alpha", Name: "0001_join"}
	tail := migrations.MigrationKey{App: "alpha", Name: "0002_tail"}
	other := migrations.MigrationKey{App: "unused", Name: "0001_initial"}
	nodes := []graphDefinitionSource{
		{sourceID: "a.json", key: root},
		{sourceID: "b.json", key: left, dependencies: []migrations.MigrationKey{root}},
		{sourceID: "c.json", key: right, dependencies: []migrations.MigrationKey{root}},
		{sourceID: "d.json", key: join, dependencies: []migrations.MigrationKey{right, left}},
		{sourceID: "e.json", key: tail, dependencies: []migrations.MigrationKey{join}},
		{sourceID: "f.json", key: other},
	}
	load := func(sources []Source) (map[migrations.MigrationKey]string, string) {
		t.Helper()
		set, _, err := Load(sources...)
		if err != nil {
			t.Fatal(err)
		}
		out := make(map[migrations.MigrationKey]string)
		for _, info := range set.Sources() {
			out[info.Migration] = info.HistoryDigest
		}
		return out, set.Digest()
	}
	prefix, prefixSet := load(graphDefinitionSources(t, nodes[:4]...))
	// Fixed vectors use canonical JSON and SHA-256 computed independently of
	// the Go loader. The ancestor sorts after its descendants by app name.
	want := map[migrations.MigrationKey]string{
		root:  "sha256:331786867feee838d78ceb8906044becd20c4954970d638543e068c8d1562445",
		left:  "sha256:bbc6ab9104ebf63c09560f2f914e37e267a454e2457aaeec18516964a45ee43f",
		right: "sha256:77394b7b1d4cb9fb6f8e716a4680aac77051c7e7c44e11464a2a931805bb731e",
		join:  "sha256:05a93ccee9ae0004a0900fb19c83f7e5cf82a23eaea29d027d6d4420e0789b38",
	}
	if !reflect.DeepEqual(prefix, want) {
		t.Fatal("unexpected migration history digests", prefix)
	}
	nodes[3].dependencies = []migrations.MigrationKey{left, right}
	extended := graphDefinitionSources(t, nodes...)
	extended[0].SourceID = "renamed.json"
	extended[0].Document = bytes.Replace(extended[0].Document, []byte("graph-test"), []byte("another-producer"), 1)
	slices.Reverse(extended)
	full, fullSet := load(extended)
	if fullSet == prefixSet || full[tail] != "sha256:8f317b014d5593c7fe9b12f5a3ff312252058f5a3fb16180607701ae5e8a590f" {
		t.Fatal("catalog extension lost its own definition identity")
	}
	for key, digest := range prefix {
		if full[key] != digest {
			t.Fatalf("new descendant, unrelated app, source ID or dependency order changed %v", key)
		}
	}
	changed := graphDefinitionSources(t, nodes...)
	changed[0].Document = oneCreateModelDocument(root.App, root.Name)
	changedRoot, _ := load(changed)
	for _, key := range []migrations.MigrationKey{root, left, right, join, tail} {
		if changedRoot[key] == full[key] {
			t.Fatalf("edited ancestor body retained the binding of %v", key)
		}
	}
	if changedRoot[other] != full[other] {
		t.Fatal("unrelated app changed with the historical root")
	}
	nodes[1].dependencies = nil
	detached, _ := load(graphDefinitionSources(t, nodes...))
	for _, key := range []migrations.MigrationKey{left, join, tail} {
		if detached[key] == full[key] {
			t.Fatalf("removed dependency edge retained the binding of %v", key)
		}
	}
	for _, key := range []migrations.MigrationKey{root, right, other} {
		if detached[key] != full[key] {
			t.Fatalf("removed edge changed independent %v", key)
		}
	}
}
