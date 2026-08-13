package migrationrelation

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestStatePromotionAndScalarDemotionAreLosslessAndExplicit(t *testing.T) {
	t.Parallel()

	input := stateScalarFixture()
	scalar, err := stateNewProject(stateFormatScalar, input)
	if err != nil {
		t.Fatalf("stateNewProject scalar: %v", err)
	}
	promoted, err := statePromote(scalar)
	if err != nil {
		t.Fatalf("statePromote: %v", err)
	}
	if promoted.stateFormatVersion() != stateFormatRelation || scalar.stateFormatVersion() != stateFormatScalar {
		t.Fatalf("promotion versions = before:%d after:%d", scalar.stateFormatVersion(), promoted.stateFormatVersion())
	}

	promotedSchema, exists := promoted.stateSchema("blog")
	if !exists {
		t.Fatal("promoted blog schema missing")
	}
	wantPromoted := input.Clone()
	wantPromoted.FormatVersion = ir.RelationFormatVersion
	if !reflect.DeepEqual(promotedSchema, wantPromoted) {
		t.Fatalf("promotion changed scalar IR meaning:\n got=%#v\nwant=%#v", promotedSchema, wantPromoted)
	}

	demoted, err := stateDemote(promoted)
	if err != nil {
		t.Fatalf("stateDemote scalar-only state 2: %v", err)
	}
	demotedSchema, exists := demoted.stateSchema("blog")
	if !exists || demoted.stateFormatVersion() != stateFormatScalar || !reflect.DeepEqual(demotedSchema, input) {
		t.Fatalf("promotion round trip lost scalar IR:\n got=%#v\nwant=%#v", demotedSchema, input)
	}

	fields := demotedSchema.Models[0].Fields
	if got, want := stateFieldOrder(fields), []string{"id", "title", "subtitle", "published"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("field order = %v, want %v", got, want)
	}
	if fields[1].GoName != "Title" || fields[1].MaxLength != 200 || fields[1].Default == nil ||
		fields[1].Default.Kind != ir.ScalarString || fields[1].Default.String != "" {
		t.Fatalf("empty string Char default lost: %#v", fields[1])
	}
	if !fields[2].Nullable || fields[2].MaxLength != 80 || fields[2].Default == nil || fields[2].Default.String != "draft" {
		t.Fatalf("nullable/non-empty Char meaning lost: %#v", fields[2])
	}
	if fields[3].Default == nil || fields[3].Default.Kind != ir.ScalarBoolean || fields[3].Default.Boolean {
		t.Fatalf("explicit false Boolean default lost: %#v", fields[3])
	}

	if _, err := statePromote(promoted); !stateErrorHas(err, "promotion_source_version", "promotion_requires_state_1") {
		t.Fatalf("statePromote(state2) = %#v", err)
	}
	if _, err := stateDemote(scalar); !stateErrorHas(err, "demotion_source_version", "demotion_requires_state_2") {
		t.Fatalf("stateDemote(state1) = %#v", err)
	}
}

func TestStateRelationDemotionRejectsCanonicalFirstRelationWithoutPublishing(t *testing.T) {
	t.Parallel()

	relation, err := stateNewProject(stateFormatRelation, stateRelationFixture())
	if err != nil {
		t.Fatalf("stateNewProject relation: %v", err)
	}
	demoted, err := stateDemote(relation)
	var failure *stateCandidateError
	if !errors.As(err, &failure) || failure.Code != "relation_state_demotion_rejected" || failure.Reason != "relation_present" ||
		failure.App != "blog" || failure.Model != "article" || failure.Field != "author" {
		t.Fatalf("relation demotion failure = %#v", err)
	}
	if demoted.stateFormatVersion() != 0 || len(demoted.apps) != 0 {
		t.Fatalf("failed demotion published partial state: %#v", demoted)
	}
	if relation.stateFormatVersion() != stateFormatRelation {
		t.Fatal("failed demotion mutated source state")
	}

	if _, err := stateNewProject(stateFormatScalar, stateRelationFixture()); !stateErrorHas(
		err,
		"schema_ir_version_mismatch",
		"schema_ir_version",
	) {
		t.Fatalf("state 1 relation construction = %#v", err)
	}
}

func TestStateSnapshotsAndNestedRelationsNeverAliasCallers(t *testing.T) {
	t.Parallel()

	input := stateRelationFixture()
	state, err := stateNewProject(stateFormatRelation, input)
	if err != nil {
		t.Fatalf("stateNewProject relation: %v", err)
	}
	input.Models[0].Fields[1].Name = "mutated_input"
	input.Models[0].Fields[1].Relation.Target.AppLabel = "mutated_input"

	first, exists := state.stateModel("blog", "article")
	if !exists {
		t.Fatal("relation state model missing")
	}
	first.Fields[1].Name = "mutated_accessor"
	first.Fields[1].Relation.Reverse.Name = "mutated_accessor"
	clone := state.stateClone()
	clone.apps["blog"].Models[0].Fields[1].Relation.Target.ModelName = "mutated_clone"

	fresh, exists := state.stateModel("blog", "article")
	if !exists || fresh.Name != "article" || fresh.GoName != "Article" || fresh.DBTable != "blog_article" ||
		fresh.Fields[1].Name != "author" || fresh.Fields[1].GoName != "AuthorID" ||
		fresh.Fields[1].Relation.Target != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) ||
		fresh.Fields[1].Relation.Cardinality != ir.RelationManyToOne ||
		fresh.Fields[1].Relation.Reverse.Name != "articles" || fresh.Fields[1].Relation.OnDelete != ir.DeleteProtect {
		t.Fatalf("state retained nested relation alias: %#v", fresh)
	}

	scalarInput := stateScalarFixture()
	scalar, err := stateNewProject(stateFormatScalar, scalarInput)
	if err != nil {
		t.Fatalf("stateNewProject scalar: %v", err)
	}
	scalarInput.Models[0].Fields[1].Default.String = "mutated_input"
	promoted, err := statePromote(scalar)
	if err != nil {
		t.Fatalf("statePromote: %v", err)
	}
	promoted.apps["blog"].Models[0].Fields[1].Default.String = "mutated_promoted"
	original, exists := scalar.stateModel("blog", "article")
	if !exists || original.Fields[1].Default == nil || original.Fields[1].Default.String != "" {
		t.Fatalf("promotion/default retained source alias: %#v", original)
	}
}

func TestStateValidationPrecedenceIsVersionThenStructureThenRelation(t *testing.T) {
	t.Parallel()

	valid, err := stateNewProject(stateFormatRelation, stateRelationFixture())
	if err != nil {
		t.Fatalf("stateNewProject relation: %v", err)
	}

	invalidVersion := valid.stateClone()
	invalidVersion.formatVersion = 9
	invalidVersion.apps["wrong-key"] = invalidVersion.apps["blog"]
	delete(invalidVersion.apps, "blog")
	if _, err := stateDemote(invalidVersion); !stateErrorHas(err, "state_format_incompatible", "format_version") {
		t.Fatalf("version precedence error = %#v", err)
	}

	invalidApp := valid.stateClone()
	invalidApp.apps["wrong-key"] = invalidApp.apps["blog"]
	delete(invalidApp.apps, "blog")
	if _, err := stateDemote(invalidApp); !stateErrorHas(err, "invalid_app_identity", "app_identity") {
		t.Fatalf("app precedence error = %#v", err)
	}

	invalidIRVersion := valid.stateClone()
	schema := invalidIRVersion.apps["blog"]
	schema.FormatVersion = ir.FormatVersion
	invalidIRVersion.apps["blog"] = schema
	if _, err := stateDemote(invalidIRVersion); !stateErrorHas(err, "schema_ir_version_mismatch", "schema_ir_version") {
		t.Fatalf("IR-version precedence error = %#v", err)
	}

	invalidRelation := valid.stateClone()
	schema = invalidRelation.apps["blog"]
	schema.Models[0].Fields[1].Relation.OnDelete = ir.DeleteSetNull
	invalidRelation.apps["blog"] = schema
	if _, err := stateDemote(invalidRelation); !stateErrorHas(err, "schema_invalid", "invalid_nullability") {
		t.Fatalf("relation validation error = %#v", err)
	}
}

func TestStateResourceShapeFailsClosedBeforeCloneNormalizeOrPublication(t *testing.T) {
	t.Parallel()

	assertZero := func(t *testing.T, value stateProjectState) {
		t.Helper()
		if value.formatVersion != 0 || value.apps != nil {
			t.Fatalf("resource failure published state: %#v", value)
		}
	}

	t.Run("schema count is bounded before candidate map allocation", func(t *testing.T) {
		schemas := make([]ir.Schema, definition.MaxSources+1)
		got, err := stateNewProject(stateFormatScalar, schemas...)
		if !stateErrorHas(err, "resource_limit_exceeded", "app_count_exceeds_profile_limit") {
			t.Fatalf("schema count failure = %#v", err)
		}
		assertZero(t, got)
	})

	t.Run("identifier and default payload are individually bounded", func(t *testing.T) {
		identifier := stateScalarFixture()
		identifier.Models[0].Fields[1].Name = strings.Repeat("i", definition.MaxSourceIDBytes+1)
		got, err := stateNewProject(stateFormatScalar, identifier)
		if !stateErrorHas(err, "resource_limit_exceeded", "identifier_bytes_exceed_profile_limit") {
			t.Fatalf("identifier failure = %#v", err)
		}
		assertZero(t, got)

		payload := stateScalarFixture()
		ownedDefault := payload.Models[0].Fields[1].Default
		ownedDefault.String = strings.Repeat("d", definition.MaxDocumentBytes+1)
		got, err = stateNewProject(stateFormatScalar, payload)
		if !stateErrorAt(
			err,
			"resource_limit_exceeded",
			"default_payload_bytes_exceed_profile_limit",
			"blog",
			"article",
			"title",
		) {
			t.Fatalf("default payload failure = %#v", err)
		}
		assertZero(t, got)
		if payload.Models[0].Fields[1].Default != ownedDefault || len(ownedDefault.String) != definition.MaxDocumentBytes+1 {
			t.Fatal("failed construction replaced or mutated caller-owned default")
		}
	})

	t.Run("one schema document has an aggregate byte budget", func(t *testing.T) {
		schema := stateScalarFixture()
		payload := strings.Repeat("p", definition.MaxDocumentBytes/3+1)
		schema.Models[0].Fields[1].Default.String = payload
		schema.Models[0].Fields[2].Default.String = payload
		schema.Models[0].Fields = append(schema.Models[0].Fields, ir.Field{
			Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, MaxLength: 1,
			Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: payload},
		})
		got, err := stateNewProject(stateFormatScalar, schema)
		if !stateErrorHas(err, "resource_limit_exceeded", "schema_document_bytes_exceed_profile_limit") {
			t.Fatalf("schema document failure = %#v", err)
		}
		assertZero(t, got)
	})

	t.Run("project batch has an aggregate byte budget", func(t *testing.T) {
		payload := strings.Repeat("b", definition.MaxDocumentBytes/2)
		schemaCount := definition.MaxBatchBytes/(definition.MaxDocumentBytes/2) + 1
		schemas := make([]ir.Schema, schemaCount)
		for index := range schemas {
			schemas[index] = stateScalarFixture()
			schemas[index].AppLabel = fmt.Sprintf("app_%03d", index)
			schemas[index].Models[0].Fields[1].Default.String = payload
		}
		got, err := stateNewProject(stateFormatScalar, schemas...)
		if !stateErrorHas(err, "resource_limit_exceeded", "aggregate_bytes_exceed_profile_limit") {
			t.Fatalf("aggregate bytes failure = %#v", err)
		}
		assertZero(t, got)
	})

	t.Run("shared caller slices cannot bypass aggregate node budget", func(t *testing.T) {
		defaultValue := &ir.ScalarDefault{}
		relationValue := &ir.ForeignKeyRelation{}
		fields := make([]ir.Field, definition.MaxFieldsPerCreateModel)
		for index := range fields {
			fields[index].Default = defaultValue
			fields[index].Relation = relationValue
		}
		nodesPerModel := 1 + 3*definition.MaxFieldsPerCreateModel
		models := make([]ir.Model, definition.MaxJSONValues/nodesPerModel+1)
		for index := range models {
			models[index].Fields = fields
		}
		schema := ir.Schema{FormatVersion: ir.FormatVersion, AppLabel: "nodes", Models: models}
		got, err := stateNewProject(stateFormatScalar, schema)
		if !stateErrorHas(err, "resource_limit_exceeded", "aggregate_node_count_exceeds_profile_limit") {
			t.Fatalf("aggregate nodes failure = %#v", err)
		}
		assertZero(t, got)
	})

	t.Run("one model cannot exceed the create-model field budget", func(t *testing.T) {
		schema := ir.Schema{
			FormatVersion: ir.FormatVersion,
			AppLabel:      "fields",
			Models: []ir.Model{{
				Name:   "entry",
				Fields: make([]ir.Field, definition.MaxFieldsPerCreateModel+1),
			}},
		}
		got, err := stateNewProject(stateFormatScalar, schema)
		if !stateErrorAt(
			err,
			"resource_limit_exceeded",
			"model_field_count_exceeds_profile_limit",
			"fields",
			"entry",
			"",
		) {
			t.Fatalf("field count failure = %#v", err)
		}
		assertZero(t, got)
	})
}

func TestStatePromotionAndDemotionScanCallerOwnedMapBeforeCloning(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		version int
		promote bool
	}{
		{name: "promotion source", version: stateFormatScalar, promote: true},
		{name: "demotion source", version: stateFormatRelation},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema := stateScalarFixture()
			if test.version == stateFormatRelation {
				schema.FormatVersion = ir.RelationFormatVersion
			}
			ownedDefault := schema.Models[0].Fields[1].Default
			ownedDefault.String = strings.Repeat("s", definition.MaxDocumentBytes+1)
			source := stateProjectState{
				formatVersion: test.version,
				apps:          map[string]ir.Schema{"blog": schema},
			}
			var got stateProjectState
			var err error
			if test.promote {
				got, err = statePromote(source)
			} else {
				got, err = stateDemote(source)
			}
			if !stateErrorHas(err, "resource_limit_exceeded", "default_payload_bytes_exceed_profile_limit") {
				t.Fatalf("source resource failure = %#v", err)
			}
			if got.formatVersion != 0 || got.apps != nil {
				t.Fatalf("resource failure published state: %#v", got)
			}
			if source.apps["blog"].Models[0].Fields[1].Default != ownedDefault ||
				len(ownedDefault.String) != definition.MaxDocumentBytes+1 {
				t.Fatal("failed transition cloned over or mutated caller-owned source")
			}
		})
	}
}

func TestStateMapResourceFailureSelectionIsDeterministicWithoutSorting(t *testing.T) {
	t.Parallel()

	a := stateRelationFixture()
	a.AppLabel = "a"
	a.Models[0].Fields[1].Relation.Reverse.Name = strings.Repeat("r", definition.MaxSourceIDBytes+1)
	z := stateRelationFixture()
	z.AppLabel = "z"
	z.Models[0].Fields[1].Relation.Reverse.Name = strings.Repeat("z", definition.MaxSourceIDBytes+1)
	value := stateProjectState{
		formatVersion: stateFormatRelation,
		apps:          map[string]ir.Schema{"z": z, "a": a},
	}
	for attempt := 0; attempt < 64; attempt++ {
		failure := stateValidate(value)
		if failure == nil || failure.Code != "resource_limit_exceeded" ||
			failure.Reason != "identifier_bytes_exceed_profile_limit" ||
			failure.App != "a" || failure.Model != "article" || failure.Field != "author" ||
			failure.Path != "models[0].fields[1].relation.reverse.name" {
			t.Fatalf("attempt %d resource failure = %#v", attempt, failure)
		}
	}
}

func TestStateFieldKindsAndPrimaryKeyShapeFailClosedBeforePromotion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*ir.Schema)
		wantReason string
		wantField  string
	}{
		{
			name: "unknown kind", wantReason: "unsupported_field_kind", wantField: "title",
			mutate: func(schema *ir.Schema) { schema.Models[0].Fields[1].Kind = ir.FieldKind("mystery") },
		},
		{
			name: "auto must be primary key", wantReason: "required", wantField: "id",
			mutate: func(schema *ir.Schema) { schema.Models[0].Fields[0].PrimaryKey = false },
		},
		{
			name: "char max length is exact", wantReason: "invalid", wantField: "title",
			mutate: func(schema *ir.Schema) { schema.Models[0].Fields[1].MaxLength = 0 },
		},
		{
			name: "char default kind is exact", wantReason: "type_mismatch", wantField: "title",
			mutate: func(schema *ir.Schema) {
				schema.Models[0].Fields[1].Default = &ir.ScalarDefault{Kind: ir.ScalarBoolean}
			},
		},
		{
			name: "boolean cannot be nullable", wantReason: "unsupported", wantField: "published",
			mutate: func(schema *ir.Schema) { schema.Models[0].Fields[3].Nullable = true },
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema := stateScalarFixture()
			test.mutate(&schema)
			if _, err := stateNewProject(stateFormatScalar, schema); !stateErrorAt(
				err,
				"schema_invalid",
				test.wantReason,
				"blog",
				"article",
				test.wantField,
			) {
				t.Fatalf("stateNewProject invalid field = %#v", err)
			}

			invalid := stateProjectState{
				formatVersion: stateFormatScalar,
				apps:          map[string]ir.Schema{"blog": schema.Clone()},
			}
			promoted, err := statePromote(invalid)
			if !stateErrorHas(err, "schema_invalid", test.wantReason) {
				t.Fatalf("statePromote invalid field = %#v", err)
			}
			if promoted.stateFormatVersion() != 0 || len(promoted.apps) != 0 {
				t.Fatalf("failed promotion published partial state: %#v", promoted)
			}
		})
	}
}

func TestStateValidationAndDemotionChooseCanonicalFieldAcrossPermutations(t *testing.T) {
	t.Parallel()

	t.Run("explicit Auto and field order are preserved", func(t *testing.T) {
		schema := stateScalarFixture()
		state, err := stateNewProject(stateFormatScalar, schema)
		if err != nil {
			t.Fatalf("explicit normalized Auto rejected: %v", err)
		}
		model, _ := state.stateModel("blog", "article")
		if !reflect.DeepEqual(stateFieldOrder(model.Fields), []string{"id", "title", "subtitle", "published"}) {
			t.Fatalf("explicit field order changed: %#v", model.Fields)
		}
	})

	t.Run("implicit Auto is rejected rather than silently normalized", func(t *testing.T) {
		schema := stateScalarFixture()
		schema.Models[0].Fields = append([]ir.Field(nil), schema.Models[0].Fields[1:]...)
		if _, err := stateNewProject(stateFormatScalar, schema); !stateErrorHas(
			err,
			"schema_not_normalized",
			"normalization_would_change_state",
		) {
			t.Fatalf("implicit Auto input = %#v", err)
		}
	})

	t.Run("empty derived table and column are rejected as unnormalized", func(t *testing.T) {
		schema := stateScalarFixture()
		schema.Models[0].DBTable = ""
		schema.Models[0].Fields[1].Column = ""
		if _, err := stateNewProject(stateFormatScalar, schema); !stateErrorHas(
			err,
			"schema_not_normalized",
			"normalization_would_change_state",
		) {
			t.Fatalf("derived identities were silently accepted: %#v", err)
		}
	})

	relationSchema := stateRelationFixture()
	relationSchema.Models[0].Fields = append(relationSchema.Models[0].Fields, ir.Field{
		Name: "editor", GoName: "EditorID", Column: "editor_id", Kind: ir.FieldForeignKey, Nullable: true,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Disabled: true},
			OnDelete:    ir.DeleteSetNull,
		},
	})
	for permutation := 0; permutation < 2; permutation++ {
		schema := relationSchema.Clone()
		if permutation != 0 {
			fields := schema.Models[0].Fields
			for left, right := 0, len(fields)-1; left < right; left, right = left+1, right-1 {
				fields[left], fields[right] = fields[right], fields[left]
			}
		}
		state, err := stateNewProject(stateFormatRelation, schema)
		if err != nil {
			t.Fatalf("permutation %d relation state: %v", permutation, err)
		}
		if _, err := stateDemote(state); !stateErrorAt(
			err,
			"relation_state_demotion_rejected",
			"relation_present",
			"blog",
			"article",
			"author",
		) {
			t.Fatalf("permutation %d demotion failure = %#v", permutation, err)
		}
	}
}

func stateErrorHas(err error, code, reason string) bool {
	var failure *stateCandidateError
	return errors.As(err, &failure) && failure.Category == "migration_relation_state_candidate_error" &&
		failure.Stage == "state" && failure.Code == code && failure.Reason == reason
}

func stateErrorAt(err error, code, reason, app, model, field string) bool {
	var failure *stateCandidateError
	return errors.As(err, &failure) && failure.Category == "migration_relation_state_candidate_error" &&
		failure.Stage == "state" && failure.Code == code && failure.Reason == reason &&
		failure.App == app && failure.Model == model && failure.Field == field
}

func stateFieldOrder(fields []ir.Field) []string {
	names := make([]string, len(fields))
	for index, field := range fields {
		names[index] = field.Name
	}
	return names
}

func stateScalarFixture() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.FormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:    "article",
			GoName:  "Article",
			DBTable: "blog_article",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200,
					Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: ""},
				},
				{
					Name: "subtitle", GoName: "Subtitle", Column: "subtitle", Kind: ir.FieldChar, Nullable: true, MaxLength: 80,
					Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: "draft"},
				},
				{
					Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean,
					Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false},
				},
			},
		}},
	}
}

func stateRelationFixture() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.RelationFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:    "article",
			GoName:  "Article",
			DBTable: "blog_article",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "articles"},
						OnDelete:    ir.DeleteProtect,
					},
				},
			},
		}},
	}
}
