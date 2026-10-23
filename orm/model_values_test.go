package orm_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestModelValuesPreservePresenceMetadataAndOwnedNullableSnapshots(t *testing.T) {
	summary := "private-summary"
	current := models.NewArticleWithID(0)
	current.Title = "original"
	current.Summary = &summary
	metadata, err := models.ArticleObjects.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	metadata.Fields[1].Name = "forged"
	metadata.DBTable = "forged"
	fresh, err := models.ArticleObjects.Metadata()
	if err != nil || fresh.DBTable == "forged" || fresh.Fields[1].Name == "forged" {
		t.Fatal("metadata getter shares manager storage", err)
	}
	values, err := models.ArticleObjects.ModelValues(current)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := values["id"].Integer(); !ok || value != 0 {
		t.Fatal("present zero became absent")
	}
	values["title"] = query.String("forged")
	summary = "changed-by-caller"
	if value, _ := values["summary"].String(); value != "private-summary" {
		t.Fatal("snapshot borrowed nullable pointee")
	}
	unkeyed := models.Article{ID: 42, Title: "unsaved"}
	values, err = models.ArticleObjects.ModelValues(unkeyed)
	if err != nil || !values["id"].IsNull() {
		t.Fatal("numeric ID manufactured presence", err)
	}
	if _, err := (orm.Manager[models.Article]{}).Metadata(); err == nil {
		t.Fatal("zero manager metadata succeeded")
	}
	if _, err := (orm.Manager[models.Article]{}).ModelValues(current); err == nil {
		t.Fatal("zero manager snapshot succeeded")
	}
}

func TestApplyModelValuesPreservesCallerExcludedFieldsAndKeyState(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsaved", true: "present_zero"}[present], func(t *testing.T) {
			summary := "unchanged"
			current := models.Article{Title: "original", Published: true, Summary: &summary}
			if present {
				(models.ArticleDescriptor{}).SetPrimaryKey(&current, 0)
			}
			prepared, err := models.ArticleObjects.ApplyValues(current, map[string]query.Value{"title": query.String("changed")})
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Title != "changed" || !prepared.Published || prepared.Summary == current.Summary || *prepared.Summary != "unchanged" || current.Title != "original" {
				t.Fatal("preparation changed caller or excluded fields")
			}
			key, found := (models.ArticleDescriptor{}).PrimaryKey(prepared)
			if found != present || !key.Equal(query.Integer(0)) {
				t.Fatal("preparation changed key state")
			}
			*prepared.Summary = "prepared-only"
			if summary != "unchanged" {
				t.Fatal("prepared result aliases caller")
			}
			cleared, err := models.ArticleObjects.ApplyValues(current, map[string]query.Value{"summary": query.Null()})
			if err != nil || cleared.Summary != nil || current.Summary == nil {
				t.Fatal("nullable clear failed", err)
			}
			cloned, err := models.ArticleObjects.ApplyValues(current, nil)
			if err != nil || cloned.Summary == current.Summary {
				t.Fatal("empty preparation did not return a detached copy", err)
			}
		})
	}
}

func TestApplyModelValuesRejectsAllBadInputsBeforeAssignment(t *testing.T) {
	for _, input := range []map[string]query.Value{
		{"title": query.Null()}, {"published": query.Integer(1)}, {"id": query.Integer(2)}, {"unknown": query.String("secret")}, {"title": query.String("good"), "summary": query.Integer(3)},
	} {
		descriptor := &assignmentDescriptor{}
		manager := orm.NewManager[models.Article](descriptor)
		current := models.NewArticleWithID(0)
		current.Title = "original"
		value, err := manager.ApplyValues(current, input)
		if err == nil || descriptor.calls != 0 || !reflect.DeepEqual(value, models.Article{}) || current.Title != "original" || strings.Contains(err.Error(), "secret") {
			t.Fatal("bad input reached assignment or returned partial model", err, descriptor.calls)
		}
	}
}

func TestApplyModelValuesRejectsDescriptorViolationsWithoutPublishingPartialResults(t *testing.T) {
	for _, mode := range []string{"reject", "wrong_value", "change_omitted", "change_key", "change_presence", "callback_input_mutation"} {
		t.Run(mode, func(t *testing.T) {
			summary := "private-summary"
			current := models.NewArticleWithID(0)
			current.Title = "original"
			current.Published = true
			current.Summary = &summary
			input := map[string]query.Value{"title": query.String("new")}
			descriptor := &assignmentDescriptor{mode: mode, input: input}
			manager := orm.NewManager[models.Article](descriptor)
			value, err := manager.ApplyValues(current, input)
			if mode == "callback_input_mutation" {
				if err != nil || value.Title != "new" {
					t.Fatal("assignment read mutated caller input", err)
				}
			} else if err == nil || !reflect.DeepEqual(value, models.Article{}) {
				t.Fatal("descriptor violation published a model", err)
			}
			if current.Title != "original" || current.ID != 0 || !current.Published || summary != "private-summary" {
				t.Fatal("descriptor mutation reached caller")
			}
			if _, found := (models.ArticleDescriptor{}).PrimaryKey(current); !found {
				t.Fatal("caller presence was cleared")
			}
		})
	}
}

func TestApplyModelValuesConcurrentPreparationDoesNotShareState(t *testing.T) {
	summary := "shared-initial"
	current := models.NewArticleWithID(7)
	current.Title = "original"
	current.Summary = &summary
	for _, name := range []string{"one", "two", "three", "four"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result, err := models.ArticleObjects.ApplyValues(current, map[string]query.Value{"title": query.String(name)})
			if err != nil {
				t.Fatal(err)
			}
			*result.Summary = name
			if result.Title != name || current.Title != "original" || *current.Summary != "shared-initial" {
				t.Fatal("concurrent preparations share values")
			}
		})
	}
}

type assignmentDescriptor struct {
	models.ArticleDescriptor
	calls int
	mode  string
	input map[string]query.Value
}

func (descriptor *assignmentDescriptor) SetFieldValue(model *models.Article, field ir.Field, input query.Value) bool {
	descriptor.calls++
	if descriptor.mode == "callback_input_mutation" {
		descriptor.input["title"] = query.String("forged")
	}
	if descriptor.mode == "reject" {
		model.Title = "partial"
		return false
	}
	if !descriptor.ArticleDescriptor.SetFieldValue(model, field, input) {
		return false
	}
	switch descriptor.mode {
	case "wrong_value":
		model.Title = "other"
	case "change_omitted":
		*model.Summary = "changed-without-assignment"
	case "change_key":
		model.ID = 1
	case "change_presence":
		descriptor.ClearPrimaryKey(model)
	}
	return true
}

func TestApplyModelValuesRequiresGeneratedAssignmentCapability(t *testing.T) {
	// A custom descriptor can intentionally expose only its existing write port.
	var write orm.WriteDescriptor[models.Article] = models.ArticleDescriptor{}
	manager := orm.NewManager[models.Article](writeOnlyDescriptor{WriteDescriptor: write})
	_, err := manager.ApplyValues(models.Article{}, nil)
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatal("missing assignment capability accepted", err)
	}
}

type writeOnlyDescriptor struct {
	orm.WriteDescriptor[models.Article]
}
