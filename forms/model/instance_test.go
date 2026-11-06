package model_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func TestInstanceFormOwnsCurrentSnapshotAndPreparesTypedCleanOutputs(t *testing.T) {
	summary := "stored-summary"
	current := models.NewArticleWithID(0)
	current.Title = "original"
	current.Published = true
	current.Summary = &summary
	metadata, err := models.ArticleObjects.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewSpecForFields(metadata, []string{"title"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	bound, err := formmodel.BindInstance(t.Context(), models.ArticleObjects, spec, forms.NewData(map[string][]string{"title": {"candidate"}}), &current, formmodel.PostClean{Fields: []string{"summary"}, Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
		calls++
		value, _ := candidate.String("summary")
		key, _ := candidate.Integer("id")
		if value != "stored-summary" || key != 0 {
			t.Fatal("clean did not receive typed current instance")
		}
		return forms.NewValues(map[string]forms.Value{"summary": forms.String("derived-summary")}), validation.Errors{}
	}})
	if err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	current.Title = "later-caller-title"
	summary = "later-caller-summary"
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	first, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	if first.Title != "candidate" || !first.Published || first.Summary == nil || *first.Summary != "derived-summary" {
		t.Fatal("typed preparation lost selected, excluded or clean values")
	}
	if key, present := (models.ArticleDescriptor{}).PrimaryKey(first); !present || !key.Equal(query.Integer(0)) {
		t.Fatal("present zero key lost")
	}
	*first.Summary = "changed-result"
	second, err := prepared.Model()
	if err != nil || *second.Summary != "derived-summary" || current.Title != "later-caller-title" || summary != "later-caller-summary" {
		t.Fatal("prepared model shares mutable instance data", err)
	}
	if raw, _ := prepared.Input().String("summary"); raw != "derived-summary" {
		t.Fatal("declared hidden output missing from input")
	}
	if _, found := bound.BoundForm().Form().Cleaned().Get("summary"); found {
		t.Fatal("typed preparation rewrote cleaned input")
	}
	rejected, err := bound.WithErrors(validation.NewErrors(validation.New("title", "unique")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rejected.Prepare(); err == nil {
		t.Fatal("DB rejection allowed preparation")
	}
	if _, err := bound.Prepare(); err != nil {
		t.Fatal("WithErrors mutated previous bound form", err)
	}
}

func TestInstanceFormNewDefaultsAndUnrepresentableCleanNull(t *testing.T) {
	metadata, err := models.ArticleObjects.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewSpecForFields(metadata, []string{"title"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := formmodel.BindInstance(t.Context(), models.ArticleObjects, spec, forms.NewData(map[string][]string{"title": {"new"}}), nil, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	value, err := prepared.Model()
	if err != nil || value.Title != "new" || value.Published || value.Summary != nil {
		t.Fatal("new typed candidate lost IR defaults", err)
	}
	if _, present := (models.ArticleDescriptor{}).PrimaryKey(value); present {
		t.Fatal("new preparation manufactured a primary key")
	}
	nullBound, err := formmodel.BindInstance(t.Context(), models.ArticleObjects, spec, forms.NewData(map[string][]string{"title": {"valid-input"}}), nil, formmodel.PostClean{Fields: []string{"title"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
		return forms.NewValues(map[string]forms.Value{"title": forms.Null()}), validation.Errors{}
	}})
	if err != nil || !nullBound.BoundForm().Form().Valid() {
		t.Fatal("clean reran field validation", err)
	}
	var representationError *formmodel.Error
	if _, err := nullBound.Prepare(); !errors.As(err, &representationError) || representationError.Code != "nonnullable" || representationError.Path != "input.title" {
		t.Fatal("nonnullable clean NULL silently became a Go zero value", err)
	}
	candidate, _ := nullBound.BoundForm().Candidate().Get("title")
	if !candidate.IsNull() {
		t.Fatal("failed typed preparation changed unrepresentable candidate")
	}
	if _, err := (formmodel.InstanceForm[models.Article]{}).Prepare(); err == nil {
		t.Fatal("zero bound form prepared")
	}
	if _, err := (formmodel.PreparedInstance[models.Article]{}).Model(); err == nil {
		t.Fatal("zero prepared model looked valid")
	}
}

func TestInstanceFormKeepsCollectionAndCommandPersistenceSeparate(t *testing.T) {
	metadata := (models.ArticleDescriptor{}).Metadata()
	collection, err := ir.NormalizeManyToManyField("godj_conformance", metadata.Name, ir.ManyToManyField{Name: "labels", GoName: "Labels", Blank: true, Target: ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}})
	if err != nil {
		t.Fatal(err)
	}
	metadata.ManyToMany = []ir.ManyToManyField{collection}
	manager := orm.NewManager[models.Article](collectionInstanceDescriptor{metadata: metadata})
	for _, mode := range []string{"selected", "clear", "excluded", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			fields := []string{"title", "labels"}
			if mode == "excluded" {
				fields = []string{"title"}
			}
			command, err := forms.CharField("confirmation", forms.WithWidget(forms.PasswordInput))
			if err != nil {
				t.Fatal(err)
			}
			definition := formmodel.Definition{Fields: fields, ExtraFields: []forms.Field{command}}
			spec, err := definition.Spec(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "excluded" {
				spec, err = spec.WithModelChoices("labels", forms.Choice{Value: forms.Integer(7), Label: "Visible"})
				if err != nil {
					t.Fatal(err)
				}
			}
			data := map[string][]string{"title": {"selected"}, "confirmation": {"private-command"}, "labels": {"7"}}
			if mode == "clear" {
				delete(data, "labels")
			}
			if mode == "invalid" {
				data["labels"] = []string{"8"}
			}
			instance, err := formmodel.BindInstance(t.Context(), manager, spec, forms.NewData(data), nil, formmodel.PostClean{})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := instance.Prepare()
			if mode == "invalid" {
				if err == nil {
					t.Fatal("unapproved collection selection prepared")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			value, err := prepared.Model()
			if err != nil || value.Title != "selected" {
				t.Fatal("scalar model preparation failed", err)
			}
			collection, present := prepared.Collections().Get("labels")
			if mode == "excluded" {
				if present {
					t.Fatal("excluded collection became pending write")
				}
			} else {
				keys, ok := collection.AsIntegers()
				if !present || !ok {
					t.Fatal("selected collection write disappeared")
				}
				if mode == "selected" {
					if !reflect.DeepEqual(keys, []int64{7}) {
						t.Fatal("collection keys changed")
					}
					keys[0] = 99
					unchanged, _ := collection.AsIntegers()
					if unchanged[0] != 7 {
						t.Fatal("collection snapshot is mutable")
					}
				} else if len(keys) != 0 {
					t.Fatal("explicit empty collection lost clear intent")
				}
			}
			if secret, ok := prepared.Input().String("confirmation"); !ok || secret != "private-command" {
				t.Fatal("command input disappeared")
			}
			if strings.Contains(fmt.Sprintf("%#v %#v", instance, prepared), "private-command") {
				t.Fatal("typed form diagnostic disclosed command input")
			}
		})
	}
}

type collectionInstanceDescriptor struct {
	models.ArticleDescriptor
	metadata ir.Model
}

func (descriptor collectionInstanceDescriptor) Metadata() ir.Model { return descriptor.metadata }

func TestInstanceFormRejectsUnsupportedTypedPreparationBeforeClean(t *testing.T) {
	metadata := (models.ArticleDescriptor{}).Metadata()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"title"})
	if err != nil {
		t.Fatal(err)
	}
	var descriptor orm.WriteDescriptor[models.Article] = models.ArticleDescriptor{}
	manager := orm.NewManager[models.Article](instanceWriteOnlyDescriptor{WriteDescriptor: descriptor})
	calls := 0
	_, err = formmodel.BindInstance(t.Context(), manager, spec, forms.NewData(map[string][]string{"title": {"candidate"}}), nil, formmodel.PostClean{Clean: func(forms.Values) (forms.Values, validation.Errors) {
		calls++
		return forms.Values{}, validation.Errors{}
	}})
	if err == nil || calls != 0 {
		t.Fatal("missing typed assignment capability reached clean", err, calls)
	}
}

type instanceWriteOnlyDescriptor struct {
	orm.WriteDescriptor[models.Article]
}

func TestPrepareInstancePreservesBoundCandidateAndTypedCurrentOwnership(t *testing.T) {
	metadata, err := models.ArticleObjects.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	definition := formmodel.Definition{Fields: []string{"title"}, PostClean: formmodel.PostClean{Fields: []string{"title"}, Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
		title, _ := candidate.String("title")
		return forms.NewValues(map[string]forms.Value{"title": forms.String(strings.ToUpper(title))}), validation.Errors{}
	}}}
	current := models.NewArticleWithID(0)
	current.Summary = new("original")
	current.Published = true
	values, err := models.ArticleObjects.ModelValues(current)
	if err != nil {
		t.Fatal(err)
	}
	initial := map[string]forms.Value{"id": forms.Integer(0), "summary": forms.String("original"), "published": forms.Boolean(true)}
	if !values["id"].Equal(query.Integer(0)) {
		t.Fatal("present zero fixture lost")
	}
	bound, err := definition.Bind(t.Context(), metadata, forms.NewData(map[string][]string{"title": {"mixed"}}), initial)
	if err != nil {
		t.Fatal(err)
	}
	*current.Summary = "fresh-excluded"
	prepared, err := formmodel.PrepareInstance(models.ArticleObjects, bound, &current)
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != 0 || result.Title != "MIXED" || !result.Published || *result.Summary != "fresh-excluded" {
		t.Fatal("bound/current ownership lost")
	}
	*result.Summary = "result-only"
	if *current.Summary != "fresh-excluded" {
		t.Fatal("preparation borrowed current pointee")
	}
	raw, _ := bound.Form().Submitted().Get("title")
	cleaned, _ := bound.Form().Cleaned().String("title")
	if !reflect.DeepEqual(raw, []string{"mixed"}) || cleaned != "mixed" {
		t.Fatal("preparation rebound or rewrote input")
	}
}

func TestPrepareInstanceRejectsPolicyIdentityAndAppliedErrors(t *testing.T) {
	metadata, err := models.ArticleObjects.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"different_policy", "different_model", "different_key", "absent_current", "unexpected_present_zero", "errors", "zero_bound"} {
		t.Run(mode, func(t *testing.T) {
			policy := metadata.Clone()
			current := models.NewArticleWithID(0)
			current.Title = "current"
			initial := map[string]forms.Value{"id": forms.Integer(0)}
			if mode == "different_policy" {
				policy.Fields[1].Blank = !policy.Fields[1].Blank
			}
			if mode == "different_model" {
				policy.Name = "other"
			}
			if mode == "unexpected_present_zero" {
				initial = nil
			}
			calls := 0
			bound, err := (formmodel.Definition{Fields: []string{"title"}, PostClean: formmodel.PostClean{Clean: func(forms.Values) (forms.Values, validation.Errors) {
				calls++
				return forms.Values{}, validation.Errors{}
			}}}).Bind(t.Context(), policy, forms.NewData(map[string][]string{"title": {"new"}}), initial)
			if err != nil {
				t.Fatal(err)
			}
			instance := &current
			if mode == "different_key" {
				current = models.NewArticleWithID(7)
			}
			if mode == "absent_current" {
				instance = nil
			}
			if mode == "errors" {
				bound, err = bound.WithErrors(validation.NewErrors(validation.New("title", "unique")))
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "zero_bound" {
				bound = formmodel.BoundForm{}
			}
			if _, err := formmodel.PrepareInstance(models.ArticleObjects, bound, instance); err == nil {
				t.Fatal("incompatible bound/current preparation accepted")
			}
			if calls != 1 {
				t.Fatal("preparation reran model clean")
			}
		})
	}
}
