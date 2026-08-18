package definitionhandoff

import (
	"context"
	"reflect"
	"testing"
)

func TestHandoffSnapshotsValidatesClonesAndConsumesContext(t *testing.T) {
	t.Parallel()

	records := []Record{relationRecord()}
	handoff, err := New(records)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if handoff.IsZero() || handoff.Digest() != "sha256:5abaa4dff57b7454d1526cb88917390d5593b5c297be12eebbb8bb175d1fa682" {
		t.Fatalf("handoff = zero:%t digest:%q", handoff.IsZero(), handoff.Digest())
	}

	visible := []Definition{records[0].Definition}
	if err := handoff.ValidateVisible(visible); err != nil {
		t.Fatalf("ValidateVisible(): %v", err)
	}
	records[0].SourceID = "caller-mutated"
	records[0].Definition.Operations[0].Field.Relation.TargetApp = "caller-mutated"
	visible[0].Operations[0].Field.Relation.TargetApp = "caller-mutated"
	if err := handoff.ValidateVisible(visible); err == nil {
		t.Fatal("ValidateVisible(mutated) error = nil")
	}
	fresh := handoff.Records()
	if fresh[0].SourceID != "relation-blog-author" || fresh[0].Definition.Operations[0].Field.Relation.TargetApp != "authors" {
		t.Fatalf("handoff retained caller alias: %#v", fresh[0])
	}
	fresh[0].Definition.Operations[0].Field.Relation.ReverseName = "mutated-accessor"
	if again := handoff.Records(); again[0].Definition.Operations[0].Field.Relation.ReverseName != "articles" {
		t.Fatalf("Records retained accessor alias: %#v", again[0])
	}

	type valueKey struct{}
	base := context.WithValue(context.Background(), valueKey{}, "preserved")
	attached := WithContext(base, handoff)
	stripped, consumed, found := Take(attached)
	if !found || stripped != base || stripped.Value(valueKey{}) != "preserved" || consumed.IsZero() {
		t.Fatalf("Take() = base:%t value:%v found:%t zero:%t", stripped == base, stripped.Value(valueKey{}), found, consumed.IsZero())
	}
	if secondBase, second, secondFound := Take(stripped); secondBase != stripped || secondFound || !second.IsZero() {
		t.Fatalf("second Take() = same:%t found:%t zero:%t", secondBase == stripped, secondFound, second.IsZero())
	}
	if !reflect.DeepEqual(consumed.Records(), handoff.Records()) {
		t.Fatal("consumed carrier differs from source handoff")
	}
}

func TestHandoffDigestExcludesProvenanceButSealsIt(t *testing.T) {
	t.Parallel()

	first := relationRecord()
	second := relationRecord()
	second.SourceID = "renamed"
	second.Producer = Producer{Name: "different", Version: "9"}
	left, err := New([]Record{first})
	if err != nil {
		t.Fatalf("New(first): %v", err)
	}
	right, err := New([]Record{second})
	if err != nil {
		t.Fatalf("New(second): %v", err)
	}
	if left.Digest() != right.Digest() {
		t.Fatalf("provenance changed digest: %q != %q", left.Digest(), right.Digest())
	}
	if reflect.DeepEqual(left.Records(), right.Records()) {
		t.Fatal("provenance snapshots unexpectedly collapsed")
	}
}

func TestHandoffRejectsEveryForgedPrivateSeal(t *testing.T) {
	t.Parallel()

	handoff, err := New([]Record{relationRecord()})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	visible := []Definition{relationRecord().Definition}
	tests := []struct {
		name   string
		mutate func(*Handoff)
	}{
		{name: "profile", mutate: func(value *Handoff) { value.records[0].record.Profile.LoaderABI = 9 }},
		{name: "provenance", mutate: func(value *Handoff) { value.records[0].record.SourceID = "forged" }},
		{name: "canonical definition", mutate: func(value *Handoff) { value.records[0].canonical[0] ^= 1 }},
		{name: "definition seal", mutate: func(value *Handoff) { value.records[0].definitionSeal = "sha256:forged" }},
		{name: "provenance seal", mutate: func(value *Handoff) { value.records[0].provenanceSeal = "sha256:forged" }},
		{name: "set digest", mutate: func(value *Handoff) { value.digest = "sha256:forged" }},
		{name: "full graph seal", mutate: func(value *Handoff) { value.graphSeal = "sha256:forged" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			forged := handoff.Clone()
			test.mutate(&forged)
			if err := forged.ValidateVisible(visible); err == nil {
				t.Fatal("ValidateVisible(forged) error = nil")
			}
		})
	}
}

func TestHandoffRejectsMissingDiagnosticProvenance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "source ID", mutate: func(value *Record) { value.SourceID = "" }},
		{name: "producer name", mutate: func(value *Record) { value.Producer.Name = "" }},
		{name: "producer version", mutate: func(value *Record) { value.Producer.Version = "" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record := relationRecord()
			test.mutate(&record)
			if _, err := New([]Record{record}); err == nil {
				t.Fatal("New(missing provenance) error = nil")
			}
		})
	}
}

func relationRecord() Record {
	return Record{
		SourceID: "relation-blog-author",
		Producer: Producer{Name: "test-only-relation-candidate", Version: "0.1.0"},
		Profile:  Compatibility{DefinitionFormat: 1, LoaderABI: 2, OperationCodec: 2, SchemaIR: 3},
		Definition: Definition{
			App: "blog", Name: "0002_article_author", Dependencies: []Identity{},
			Operations: []Operation{{
				Kind: "add_field", AppLabel: "blog", ModelName: "article", HasField: true,
				Field: Field{
					Name: "author", GoName: "Author", Column: "author_id", Kind: "foreign_key",
					Relation: Relation{
						Present: true, TargetApp: "authors", TargetModel: "author", Cardinality: "many_to_one",
						ReverseName: "articles", OnDelete: "protect",
					},
				},
			}},
		},
	}
}
