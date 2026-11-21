package input_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/crud"
	article "github.com/progresshans/godj/examples/article/models"
	helpdesk "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func articleModelBody(t *testing.T) (serializers.Spec, input.Body[orm.ModelInput[article.Article]]) {
	t.Helper()
	spec := require[serializers.Spec](t)(serializers.FromModel((article.ArticleDescriptor{}).Metadata(),
		serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "title"}, serializers.ModelField{Name: "published"},
		serializers.ModelField{Name: "summary", Optional: true, AllowEmpty: true}))
	body := require[input.Body[orm.ModelInput[article.Article]]](t)(input.NewModel(article.ArticleObjects, spec, api.ParserConfig{}))
	return spec, body
}

func TestModelBodySharesPresenceSchemasAndFormPreparation(t *testing.T) {
	spec, body := articleModelBody(t)
	full, diagnostics, err := body.Bind(t.Context(), object(t, spec, `{"title":"  prepared  "}`), serializers.ModeFull)
	if err != nil || !diagnostics.Empty() {
		t.Fatal(err, diagnostics)
	}
	if published, present := full.Get("published"); !present || !published.Equal(query.Boolean(false)) {
		t.Fatal("full default lost presence")
	}
	if _, present := full.Get("summary"); present {
		t.Fatal("optional omission became a value")
	}
	prepared := require[orm.PreparedModel[article.Article]](t)(article.ArticleObjects.PrepareInput(full, nil))
	created := require[article.Article](t)(prepared.Model())
	metadata := (article.ArticleDescriptor{}).Metadata()
	formSpec := require[forms.Spec](t)(formmodel.NewSpecForFields(metadata, []string{"title"}))
	bound := require[formmodel.InstanceForm[article.Article]](t)(formmodel.BindInstance(t.Context(), article.ArticleObjects, formSpec, forms.NewData(map[string][]string{"title": {"  prepared  "}}), nil, formmodel.PostClean{}))
	formPrepared := require[formmodel.PreparedInstance[article.Article]](t)(bound.Prepare())
	fromForm := require[article.Article](t)(formPrepared.Model())
	if !reflect.DeepEqual(created, fromForm) || created.Title != "prepared" || created.Published || created.Summary != nil || created.Slug != nil {
		t.Fatal("Form and API disagree on model preparation")
	}
	current := article.NewArticleWithID(0)
	current.Title, current.Published, current.Summary, current.Slug = "current", true, new("private-summary"), new("retained")
	empty, diagnostics, err := body.Bind(t.Context(), object(t, spec, `{}`), serializers.ModePartial)
	if err != nil || !diagnostics.Empty() || len(empty.Values()) != 0 {
		t.Fatal(err, diagnostics)
	}
	unchanged := require[orm.PreparedModel[article.Article]](t)(article.ArticleObjects.PrepareInput(empty, &current))
	snapshot := require[article.Article](t)(unchanged.Model())
	if len(unchanged.ChangedFields()) != 0 || !reflect.DeepEqual(snapshot, current) || snapshot.Summary == current.Summary {
		t.Fatal("empty PATCH lost current state or copied pointees")
	}
	cleared, diagnostics, err := body.Bind(t.Context(), object(t, spec, `{"summary":null,"published":false}`), serializers.ModePartial)
	if err != nil || !diagnostics.Empty() {
		t.Fatal(err, diagnostics)
	}
	if null, present := cleared.Get("summary"); !present || !null.IsNull() {
		t.Fatal("explicit null lost presence")
	}
	updated := require[orm.PreparedModel[article.Article]](t)(article.ArticleObjects.PrepareInput(cleared, &current))
	value := require[article.Article](t)(updated.Model())
	if value.Title != "current" || value.Published || value.Summary != nil || *value.Slug != "retained" || !slices.Equal(updated.ChangedFields(), []string{"published", "summary"}) {
		t.Fatal("partial assignment or changed columns are wrong")
	}
	for _, mode := range []serializers.Mode{serializers.ModeFull, serializers.ModePartial} {
		got := require[openapi.Schema](t)(body.Schema(mode))
		want := require[openapi.Schema](t)(openapi.RequestSchema(spec, mode))
		if !reflect.DeepEqual(got, want) {
			t.Fatal("model Body invented a different request schema")
		}
	}
	for _, value := range []any{full, prepared} {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			if !strings.Contains(fmt.Sprintf(verb, value), "redacted") {
				t.Fatal("model input or candidate formatting exposed data")
			}
		}
	}
}

func TestModelBodyRetainsSourceAndComputedReadOnlyPolicy(t *testing.T) {
	spec, _ := articleModelBody(t)
	metadata := (article.ArticleDescriptor{}).Metadata()
	source, present := spec.Model()
	if !present || !source.Equal(metadata) {
		t.Fatal("model origin is absent")
	}
	source.Fields[1].MaxLength = 1
	source.DBTable = "forged"
	if fresh, _ := spec.Model(); !fresh.Equal(metadata) {
		t.Fatal("source getter aliases metadata")
	}
	lookalike := require[serializers.Spec](t)(serializers.NewSpec(spec.Fields()))
	if _, err := input.NewModel(article.ArticleObjects, lookalike, api.ParserConfig{}); err == nil {
		t.Fatal("lookalike fields forged model provenance")
	}
	for _, mutate := range []func(*ir.Model){
		func(model *ir.Model) { model.DBTable = "another_table" },
		func(model *ir.Model) { model.Fields[len(model.Fields)-1].NonEditable = true },
		func(model *ir.Model) { model.Fields[2].Default.Boolean = true },
	} {
		changed := metadata.Clone()
		mutate(&changed)
		wrong := require[serializers.Spec](t)(serializers.FromModel(changed, serializers.ModelField{Name: "title"}))
		if _, err := input.NewModel(article.ArticleObjects, wrong, api.ParserConfig{}); err == nil {
			t.Fatal("different model source accepted")
		}
		if _, err := serializers.NewModelEncoder(wrong, metadata, article.ArticleDescriptor{}.WriteFieldValue); err == nil {
			t.Fatal("output accepted different model source")
		}
	}
	encoder := require[serializers.ModelEncoder[article.Article]](t)(serializers.NewModelEncoder(spec, metadata, article.ArticleDescriptor{}.WriteFieldValue))
	computed := require[serializers.Field](t)(serializers.BooleanField("display_state", serializers.WithReadOnly()))
	extended := require[serializers.ModelEncoder[article.Article]](t)(encoder.WithComputed(serializers.ComputedField[article.Article]{Field: computed, Read: func(row article.Article) (serializers.Value, bool) { return serializers.Boolean(row.Published), true }}))
	body := require[input.Body[orm.ModelInput[article.Article]]](t)(input.NewModel(article.ArticleObjects, extended.Spec(), api.ParserConfig{}))
	for _, raw := range []string{`{"id":0}`, `{"display_state":true}`, `{"slug":"hidden"}`, `{"title":null}`, `{"published":"false"}`} {
		value, diagnostics, err := body.Bind(t.Context(), object(t, extended.Spec(), raw), serializers.ModePartial)
		if err != nil || diagnostics.Empty() {
			t.Fatal("invalid model input was accepted", raw, err)
		}
		if _, err := article.ArticleObjects.PrepareInput(value, nil); err == nil || value.Values() != nil {
			t.Fatal("failed request published an input")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	value, _, err := body.Bind(ctx, object(t, extended.Spec(), `{"title":"value"}`), serializers.ModeFull)
	if !errors.Is(err, context.Canceled) || value.Values() != nil {
		t.Fatal("cancellation published an input", err)
	}
	if _, err := article.ArticleObjects.PrepareInput(orm.ModelInput[article.Article]{}, nil); err == nil {
		t.Fatal("zero input accepted")
	}
}

func TestResourceBodyRejectsExposureWideningAtStartup(t *testing.T) {
	resource := require[crud.Resource[article.Article]](t)(crud.New(article.ArticleObjects, crud.Config{Fields: []string{"title"}}))
	wide, _ := articleModelBody(t)
	if _, err := input.NewResource(resource, wide, api.ParserConfig{}); err == nil {
		t.Fatal("JSON exposure exceeded the CRUD allowlist")
	}
	narrow := require[serializers.Spec](t)(serializers.FromModel((article.ArticleDescriptor{}).Metadata(), serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "title"}, serializers.ModelField{Name: "published", ReadOnly: true}))
	body := require[input.Body[orm.ModelInput[article.Article]]](t)(input.NewResource(resource, narrow, api.ParserConfig{}))
	value, failures, err := body.Bind(t.Context(), object(t, narrow, `{"title":"allowed"}`), serializers.ModeFull)
	if err != nil || !failures.Empty() {
		t.Fatal(err, failures)
	}
	if _, present := value.Get("published"); present {
		t.Fatal("readonly output default became an input")
	}
	if _, failures, err := body.Bind(t.Context(), object(t, narrow, `{"title":"allowed","published":true}`), serializers.ModeFull); err != nil || failures.ByField("published").Empty() {
		t.Fatal("readonly policy was lost", err, failures)
	}
	readonly := require[crud.Resource[article.Article]](t)(crud.New(article.ArticleObjects, crud.Config{ReadOnly: true}))
	if _, err := input.NewResource(readonly, narrow, api.ParserConfig{}); err == nil {
		t.Fatal("readonly resource exposed writable input")
	}
}

func TestModelBodyTransfersAllStoredKindsAndOwnsCollections(t *testing.T) {
	manager := orm.NewManager[helpdesk.Ticket](editableBinaryTicketDescriptor{})
	metadata := (editableBinaryTicketDescriptor{}).Metadata()
	selected := make([]serializers.ModelField, 0, len(metadata.Fields)+1)
	for _, field := range metadata.Fields {
		selected = append(selected, serializers.ModelField{Name: field.Name, Optional: field.Nullable})
	}
	selected = append(selected, serializers.ModelField{Name: "labels", Optional: true})
	spec := require[serializers.Spec](t)(serializers.FromModel(metadata, selected...))
	body := require[input.Body[orm.ModelInput[helpdesk.Ticket]]](t)(input.NewModel(manager, spec, api.ParserConfig{}))
	raw := `{"subject":"  incident  ","details":"detail","category":3,"priority":0,"resolution":"resolved","due_at":"2026-10-22T01:02:03Z","reviewed":false,"service_on":"2026-10-22","service_at":"01:02:03.000004","elapsed":"00:00:01.000002","effort":-0.25,"expected_cost":"12.30","external_reference":"12345678-1234-5678-9abc-123456789abc","external_payload":{"key":[1,null]},"external_url":"https://example.com/","external_payload_digest":"AQI=","labels":[7,7,8]}`
	bound, diagnostics, err := body.Bind(t.Context(), object(t, spec, raw), serializers.ModeFull)
	if err != nil || !diagnostics.Empty() {
		t.Fatal(err, diagnostics)
	}
	prepared := require[orm.PreparedModel[helpdesk.Ticket]](t)(manager.PrepareInput(bound, nil))
	model := require[helpdesk.Ticket](t)(prepared.Model())
	if model.Subject != "incident" || model.CategoryID != 3 || model.Closed || model.Priority == nil || *model.Priority != 0 || model.Reviewed == nil || *model.Reviewed || model.ExpectedCost == nil || model.ExpectedCost.String() != "12.3" {
		t.Fatal("scalar transfer changed a value")
	}
	values := require[map[string]query.Value](t)(manager.ModelValues(model))
	for name, value := range bound.Values() {
		if !values[name].Equal(value) {
			t.Fatal("typed model differs from cleaned value", name)
		}
	}
	keys, present := bound.Collection("labels")
	if !present || !slices.Equal(keys, []int64{7, 7, 8}) {
		t.Fatal("collection order, duplicates or presence changed")
	}
	keys[0] = 99
	copy := bound.Collections()
	copy["labels"][1] = 99
	copyValues := bound.Values()
	copyValues["subject"] = query.String("changed")
	*model.Details = "caller-only"
	keys, _ = bound.Collection("labels")
	second := require[helpdesk.Ticket](t)(prepared.Model())
	if !slices.Equal(keys, []int64{7, 7, 8}) || *second.Details != "detail" {
		t.Fatal("model input shares caller storage")
	}
	readonlySpec := require[serializers.Spec](t)(serializers.FromModel((helpdesk.TicketDescriptor{}).Metadata(), serializers.ModelField{Name: "external_payload_digest"}))
	readonlyBody := require[input.Body[orm.ModelInput[helpdesk.Ticket]]](t)(input.NewModel(helpdesk.TicketObjects, readonlySpec, api.ParserConfig{}))
	if _, failures, err := readonlyBody.Bind(t.Context(), object(t, readonlySpec, `{"external_payload_digest":"AQI="}`), serializers.ModePartial); err != nil || failures.ByField("external_payload_digest").Empty() {
		t.Fatal("production digest became writable", err, failures)
	}
	for _, raw := range []string{`{}`, `{"labels":[]}`} {
		value, diagnostics, err := body.Bind(t.Context(), object(t, spec, raw), serializers.ModePartial)
		keys, present := value.Collection("labels")
		if err != nil || !diagnostics.Empty() || len(keys) != 0 || present != (raw != `{}`) {
			t.Fatal("collection omission and empty selection collapsed", err)
		}
	}
	// Lists and concurrent requests reuse the exact Body; no model-specific
	// parser, setter or schema is needed for a second consumption shape.
	list := require[input.ListBody[orm.ModelInput[helpdesk.Ticket]]](t)(input.NewList(body, input.ListConfig{MaxItems: 2}))
	items, diagnostics, err := list.Bind(t.Context(), []serializers.Object{object(t, spec, raw), object(t, spec, raw)}, serializers.ModeFull)
	if err != nil || !diagnostics.Empty() || len(items) != 2 {
		t.Fatal("model list failed", err, diagnostics)
	}
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			value, diagnostics, err := body.Bind(t.Context(), object(t, spec, raw), serializers.ModeFull)
			if err != nil || !diagnostics.Empty() {
				t.Error(err, diagnostics)
				return
			}
			keys, _ := value.Collection("labels")
			keys[0] = 99
			unchanged, _ := value.Collection("labels")
			if unchanged[0] != 7 {
				t.Error("concurrent input shares membership")
			}
		})
	}
	workers.Wait()
}

// Only this fixture changes the binary field's editing policy. The production
// Helpdesk digest remains noneditable and must still fail input binding.
type editableBinaryTicketDescriptor struct{ helpdesk.TicketDescriptor }

func (editableBinaryTicketDescriptor) Metadata() ir.Model {
	metadata := (helpdesk.TicketDescriptor{}).Metadata()
	for index := range metadata.Fields {
		if metadata.Fields[index].Name == "external_payload_digest" {
			metadata.Fields[index].NonEditable = false
		}
	}
	return metadata
}
