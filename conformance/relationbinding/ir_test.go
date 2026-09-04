package relationbinding

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

func unionIRFixture() unionSchemaVNext {
	authors := modelKey{App: "authors", Model: "author"}
	posts := modelKey{App: "blog", Model: "post"}
	return unionSchemaVNext{
		FormatVersion: candidateRelationIRVersion,
		Models: []unionModel{
			{
				Key: posts,
				Fields: []unionField{
					{Name: "reviewer", Column: "reviewer_id", Relation: &relationArm{StorageType: "int64", Target: authors, Nullable: true, Reverse: "reviewed_posts", Delete: deleteSetNull}},
					{Name: "title", Column: "title", Scalar: &scalarArm{Type: "string"}},
					{Name: "author", Column: "author_id", Relation: &relationArm{StorageType: "int64", Target: authors, Reverse: "posts", Delete: deleteProtect}},
					{Name: "id", Column: "id", Scalar: &scalarArm{Type: "int64"}},
				},
			},
			{
				Key: authors,
				Fields: []unionField{
					{Name: "manager", Column: "manager_id", Relation: &relationArm{StorageType: "int64", Target: authors, Nullable: true, Reverse: "reports", Delete: deleteSetNull}},
					{Name: "name", Column: "name", Scalar: &scalarArm{Type: "string"}},
					{Name: "favorite_post", Column: "favorite_post_id", Relation: &relationArm{StorageType: "int64", Target: posts, Nullable: true, Reverse: "favored_by", Delete: deleteSetNull}},
					{Name: "id", Column: "id", Scalar: &scalarArm{Type: "int64"}},
				},
			},
		},
	}
}

func splitIRFixture() splitSchemaVNext {
	authors := modelKey{App: "authors", Model: "author"}
	posts := modelKey{App: "blog", Model: "post"}
	return splitSchemaVNext{
		FormatVersion: candidateRelationIRVersion,
		Models: []splitModel{
			{
				Key: posts,
				Storage: []storageField{
					{Name: "reviewer_id", Column: "reviewer_id", Type: "int64", Nullable: true},
					{Name: "title", Column: "title", Type: "string"},
					{Name: "author_id", Column: "author_id", Type: "int64"},
					{Name: "id", Column: "id", Type: "int64"},
				},
				Relations: []splitRelation{
					{Name: "reviewer", StorageField: "reviewer_id", Target: authors, Nullable: true, Reverse: "reviewed_posts", Delete: deleteSetNull},
					{Name: "author", StorageField: "author_id", Target: authors, Reverse: "posts", Delete: deleteProtect},
				},
			},
			{
				Key: authors,
				Storage: []storageField{
					{Name: "manager_id", Column: "manager_id", Type: "int64", Nullable: true},
					{Name: "name", Column: "name", Type: "string"},
					{Name: "favorite_post_id", Column: "favorite_post_id", Type: "int64", Nullable: true},
					{Name: "id", Column: "id", Type: "int64"},
				},
				Relations: []splitRelation{
					{Name: "manager", StorageField: "manager_id", Target: authors, Nullable: true, Reverse: "reports", Delete: deleteSetNull},
					{Name: "favorite_post", StorageField: "favorite_post_id", Target: posts, Nullable: true, Reverse: "favored_by", Delete: deleteSetNull},
				},
			},
		},
	}
}

func TestExplicitVNextLayoutsNormalizeRoundTripAndShareSemantics(t *testing.T) {
	t.Parallel()

	union, err := normalizeUnionSchema(unionIRFixture())
	if err != nil {
		t.Fatalf("normalize union: %v", err)
	}
	unionRoundTrip, err := decodeUnionVNext(union.Canonical)
	if err != nil {
		t.Fatalf("round-trip union: %v", err)
	}
	if !bytes.Equal(union.Canonical, unionRoundTrip.Canonical) || union.Digest != unionRoundTrip.Digest {
		t.Fatal("union vNext round-trip changed canonical bytes or digest")
	}

	split, splitProjection, err := normalizeSplitSchema(splitIRFixture())
	if err != nil {
		t.Fatalf("normalize split: %v", err)
	}
	splitRoundTrip, projectionRoundTrip, err := decodeSplitVNext(split.Canonical)
	if err != nil {
		t.Fatalf("round-trip split: %v", err)
	}
	if !bytes.Equal(split.Canonical, splitRoundTrip.Canonical) || split.Digest != splitRoundTrip.Digest {
		t.Fatal("split vNext round-trip changed canonical bytes or digest")
	}
	if !bytes.Equal(splitProjection.Canonical, projectionRoundTrip.Canonical) {
		t.Fatal("split semantic projection changed on round-trip")
	}
	if !bytes.Equal(union.Canonical, splitProjection.Canonical) {
		t.Fatalf("vNext layouts do not preserve the same normalized relation meaning\nunion=%s\nsplit-projection=%s", union.Canonical, splitProjection.Canonical)
	}

	permuted := unionIRFixture()
	slices.Reverse(permuted.Models)
	for i := range permuted.Models {
		slices.Reverse(permuted.Models[i].Fields)
	}
	permutedUnion, err := normalizeUnionSchema(permuted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(union.Canonical, permutedUnion.Canonical) || union.Digest != permutedUnion.Digest {
		t.Fatal("cross-app/model/field order changed union canonical bytes")
	}
}

func TestVNextMeaningMutationsChangeDigestOrFail(t *testing.T) {
	t.Parallel()

	baseline, err := normalizeUnionSchema(unionIRFixture())
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name string
		edit func(*unionSchemaVNext)
	}{
		{"target", func(schema *unionSchemaVNext) {
			schema.Models[0].Fields[0].Relation.Target = modelKey{App: "blog", Model: "post"}
		}},
		{"column", func(schema *unionSchemaVNext) { schema.Models[0].Fields[0].Column = "reviewed_by_id" }},
		{"nullable", func(schema *unionSchemaVNext) {
			schema.Models[0].Fields[0].Relation.Nullable = false
			schema.Models[0].Fields[0].Relation.Delete = deleteProtect
		}},
		{"reverse", func(schema *unionSchemaVNext) { schema.Models[0].Fields[0].Relation.Reverse = "reviewed" }},
		{"delete", func(schema *unionSchemaVNext) { schema.Models[0].Fields[0].Relation.Delete = deleteProtect }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			fixture := unionIRFixture()
			mutation.edit(&fixture)
			changed, err := normalizeUnionSchema(fixture)
			if err == nil && (bytes.Equal(baseline.Canonical, changed.Canonical) || baseline.Digest == changed.Digest) {
				t.Fatalf("%s mutation remained canonical-byte/digest green", mutation.name)
			}
		})
	}

	split := splitIRFixture()
	split.Models[0].Relations[0].Nullable = false
	if _, _, err := normalizeSplitSchema(split); err == nil {
		t.Fatal("split storage/relation nullability drift unexpectedly normalized")
	}
}

func TestSchemaIRV2AndExistingMigrationTupleRejectRelations(t *testing.T) {
	t.Parallel()

	validV2 := []byte(`{"format_version":2,"models":[{"key":{"app":"authors","model":"author"},"fields":[{"name":"id","column":"id","type":"auto","nullable":false}]}]}`)
	if _, err := decodeScalarV2(validV2); err != nil {
		t.Fatalf("valid scalar v2 rejected: %v", err)
	}

	union, err := normalizeUnionSchema(unionIRFixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeScalarV2(union.Canonical); err == nil {
		t.Fatal("old v2 decoder accepted relation-bearing vNext")
	}
	v2WithRelationField := []byte(`{"format_version":2,"models":[{"key":{"app":"blog","model":"post"},"fields":[{"name":"author","column":"author_id","type":"auto","nullable":false,"relation":{"target":{"app":"authors","model":"author"}}}]}]}`)
	if _, err := decodeScalarV2(v2WithRelationField); err == nil {
		t.Fatal("v2 decoder silently accepted a relation side field")
	}
	v2WithRelationList := []byte(`{"format_version":2,"models":[],"relations":[]}`)
	if _, err := decodeScalarV2(v2WithRelationList); err == nil {
		t.Fatal("v2 decoder silently accepted a relation side list")
	}

	existingTupleRelation := migrationDocumentV1{
		Compatibility: compatibilityTupleV1{DefinitionFormat: 1, LoaderABI: 1, OperationCodec: 1, SchemaIR: 2},
		Operations:    []migrationOperationV1{{Type: "create_model", Schema: union.Canonical}},
	}
	document, err := json.Marshal(existingTupleRelation)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeExistingMigrationV1(document); err == nil {
		t.Fatal("existing (1,1,1,2) tuple/codec accepted relation-bearing schema")
	}

	vNextTuple := existingTupleRelation
	vNextTuple.Compatibility.SchemaIR = candidateRelationIRVersion
	document, err = json.Marshal(vNextTuple)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeExistingMigrationV1(document); err == nil {
		t.Fatal("existing operation codec accepted an unapproved Schema IR tuple upgrade")
	}
}

func TestIRLayoutEvidenceRecommendsFieldUnionWithoutFreezingWireAPI(t *testing.T) {
	t.Parallel()

	union, err := normalizeUnionSchema(unionIRFixture())
	if err != nil {
		t.Fatal(err)
	}
	_, projection, err := normalizeSplitSchema(splitIRFixture())
	if err != nil {
		t.Fatal(err)
	}
	recommendation := recommendIRLayout(
		layoutEvidence{Name: "field_union_relation_arm", PhysicalColumnOwners: 1, CrossRecordInvariantCount: 0, CanonicalSemanticRoundTrip: bytes.Equal(union.Canonical, projection.Canonical)},
		layoutEvidence{Name: "separate_relation_list", PhysicalColumnOwners: 2, CrossRecordInvariantCount: 2, CanonicalSemanticRoundTrip: bytes.Equal(union.Canonical, projection.Canonical)},
	)
	if recommendation != "field_union_relation_arm" {
		t.Fatalf("layout recommendation = %q, want field_union_relation_arm", recommendation)
	}
}
