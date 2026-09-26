package consumer_test

import (
	"math"
	"testing"

	"example.com/godj-choices/models"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func TestGeneratedChoiceMetadataAndInputConsumers(t *testing.T) {
	descriptor := models.EntryDescriptor{}
	metadata := descriptor.Metadata()
	if len(metadata.Fields[1].Choices) != 2 || len(metadata.Fields[2].Choices) != 3 || len(metadata.Fields[3].Choices) != 3 {
		t.Fatal("generator omitted choices")
	}
	if label, found := metadata.Fields[1].ChoiceLabel(ir.Scalar{Kind: ir.ScalarString, String: "open"}); !found || label != "<Open>" {
		t.Fatal("string choice label changed")
	}
	metadata.Fields[1].Choices[0].Label = "mutated"
	if descriptor.Metadata().Fields[1].Choices[0].Label != "<Open>" {
		t.Fatal("generated descriptor retained a choice slice")
	}
	formSpec, err := formmodel.NewSpec(descriptor.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range formSpec.Fields() {
		if field.Widget() != forms.Select {
			t.Fatal("generated choice did not select the correct widget")
		}
	}
	bound, err := formSpec.Bind(forms.NewData(map[string][]string{"status": {"open"}, "reason": {" done "}, "priority": {"0"}}), nil)
	if err != nil || !bound.Valid() {
		t.Fatalf("generated form validation: %v %v", bound.Errors().All(), err)
	}
	if reason, _ := bound.Cleaned().String("reason"); reason != " done " {
		t.Fatal("choice Text was trimmed")
	}
	if priority, ok := bound.Cleaned().Integer("priority"); !ok || priority != 0 {
		t.Fatal("choice zero became missing")
	}
	blank, err := formSpec.Bind(forms.NewData(map[string][]string{"status": {"closed"}, "reason": {""}, "priority": {""}}), nil)
	if err != nil || !blank.Valid() {
		t.Fatal("optional choices rejected empty input")
	}
	if reason, found := blank.Cleaned().Get("reason"); !found || !reason.IsNull() {
		t.Fatal("nullable Text choice empty input did not remain null")
	}
	spec, err := serializers.FromModel(descriptor.Metadata(), serializers.ModelField{Name: "status"}, serializers.ModelField{Name: "reason"}, serializers.ModelField{Name: "priority"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := serializers.NewObject(serializers.MemberOf("reason", serializers.String("new\nline")), serializers.MemberOf("priority", serializers.Integer(-1)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := spec.Bind(input, serializers.ModeFull)
	if err != nil || !result.Valid() {
		t.Fatal("generated model serializer failed")
	}
	status, _ := result.Values().Get("status")
	if text, _ := status.AsString(); text != "open" {
		t.Fatal("choice model default disappeared")
	}
}

func TestGeneratedChoicesDoNotConstrainStorage(t *testing.T) {
	ctx := t.Context()
	backend, err := sqlite.OpenMemory(ctx, "choices-consumer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	descriptor := models.EntryDescriptor{}
	wire, err := definition.Encode(definition.Producer{Name: "choices-consumer", Version: "1"}, migrations.Migration{App: "choices", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "choices", Model: descriptor.Metadata()}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "choices-consumer", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	row, err := models.EntryObjects.Create(ctx, backend, models.NewEntryCreate().WithStatus("retired").WithReason("old body").WithPriority(math.MinInt64))
	if err != nil {
		t.Fatal(err)
	}
	row, err = models.EntryObjects.Update(ctx, backend, row, models.EntryPatch{}.WithPriority(math.MaxInt64))
	if err != nil {
		t.Fatal(err)
	}
	row.Status = "legacy"
	if err := models.EntryObjects.Save(ctx, backend, &row, models.EntryUpdateFields(models.EntryFields.Status)); err != nil {
		t.Fatal(err)
	}
	typed := models.EntryObjects.Using(backend).Filter(models.EntryFields.Status.In("legacy"))
	dynamic, err := orm.ParseDynamic(descriptor, nil, []orm.LookupInput{{Key: "status__in", Value: []string{"legacy"}}})
	if err != nil || !typed.Plan().Equal(models.EntryObjects.Using(backend).Filter(dynamic...).Plan()) {
		t.Fatal("choices changed query domain")
	}
	stored, found, err := typed.OrderBy(models.EntryFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Status != "legacy" || stored.Priority == nil || *stored.Priority != math.MaxInt64 || stored.Reason == nil || *stored.Reason != "old body" {
		t.Fatalf("stored choices were normalized or constrained: %v", err)
	}
	spec, err := serializers.FromModel(descriptor.Metadata(), serializers.ModelField{Name: "status"}, serializers.ModelField{Name: "reason"}, serializers.ModelField{Name: "priority"})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(spec, descriptor.Metadata(), descriptor.WriteFieldValue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Encode(stored); err != nil {
		t.Fatalf("stored out-of-choice values rejected during output: %v", err)
	}
	metadata := descriptor.Metadata()
	metadata.Fields[1].Choices = nil
	plain, err := formmodel.NewSpec(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Fields()[0].Widget() != forms.TextInput {
		t.Fatal("removing metadata choices did not restore input kind")
	}
}
