package model_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/conformance/relationfixture/authors"
	"github.com/progresshans/godj/conformance/relationfixture/blog"
	relationproject "github.com/progresshans/godj/conformance/relationfixture/project"
	helpdesk "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

type inlineObservation struct {
	Name, Model string
	Fields      []string
	NewParent   bool   `json:"new_parent"`
	ParentKey   *int64 `json:"parent_key"`
	Bound       bool
	Data        map[string][]string
	Valid       bool
	Initial     int
	MaxNum      int `json:"max_num"`
	Forms       []struct {
		Valid           bool
		CandidateKey    *int64 `json:"candidate_key"`
		CandidateParent *int64 `json:"candidate_parent"`
		Changed         []string
		Errors          map[string][]*string
		ParentHidden    bool `json:"parent_hidden"`
		ParentRequired  bool `json:"parent_required"`
	}
	NonFormErrors []*string `json:"non_form_errors"`
	CleanCalls    []*int64  `json:"clean_calls"`
}

func inlineLabelCurrent() []helpdesk.Label {
	one, two := helpdesk.NewLabelWithID(10), helpdesk.NewLabelWithID(11)
	one.Name, two.Name, one.CategoryID, two.CategoryID = "a", "b", 1, 1
	return []helpdesk.Label{one, two}
}

func inlineRows(t *testing.T, metadata ir.Model, fields []string) forms.SetSpec {
	t.Helper()
	row, err := formmodel.NewSpecForFields(metadata, fields)
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix, config.MaxForms, config.AbsoluteMax, config.CanDelete = "items", 10, 12, true
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestInlineSetsAgainstPinnedDjango(t *testing.T) {
	raw, err := os.ReadFile("testdata/inline-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Python, Backend string
		Source                  string `json:"source_sha256"`
		Unchanged               bool   `json:"storage_unchanged"`
		Cleanup                 bool   `json:"cleanup_empty"`
		Cases                   []inlineObservation
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != "sqlite" ||
		reference.Source != "858772e26a8e1d023782b410d3de67b9fc73dfb98120683fdf14584c0d5b0910" || !reference.Unchanged || !reference.Cleanup || len(reference.Cases) != 22 {
		t.Fatal("unexpected inline reference")
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	for _, observed := range reference.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			switch observed.Model {
			case "label":
				parent := helpdesk.Category{Name: "private parent"}
				var current []helpdesk.Label
				if observed.ParentKey != nil {
					parent = helpdesk.NewCategoryWithID(*observed.ParentKey)
					parent.Name = "private parent"
					if *observed.ParentKey == 1 {
						current = inlineLabelCurrent()
					}
				}
				compareInline(t, binding, helpdesk.CategoryObjects, helpdesk.LabelObjects, "category", parent, current, observed)
			case "report":
				compareInline(t, binding, helpdesk.TicketObjects, helpdesk.ServiceReportObjects, "ticket", helpdesk.Ticket{}, nil, observed)
			default:
				t.Fatal("unexpected reference model")
			}
		})
	}
}

func compareInline[P, C any](t *testing.T, binding orm.ProjectBinding, parents orm.Manager[P], children orm.Manager[C], foreign string, parent P, current []C, observed inlineObservation) {
	t.Helper()
	metadata, err := children.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewInlineSpec(binding, parents, children, foreign, inlineRows(t, metadata, observed.Fields))
	if err != nil {
		t.Fatal(err)
	}
	calls := []*int64{}
	post := formmodel.PostClean{Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
		var key *int64
		if value, present := candidate.Integer(foreign); present {
			key = &value
		}
		calls = append(calls, key)
		return forms.Values{}, validation.Errors{}
	}}
	var bound formmodel.InlineSet[P, C]
	if observed.Bound {
		bound, err = spec.Bind(forms.NewData(observed.Data), parent, current, post)
	} else {
		bound, err = spec.Unbound(parent, current)
	}
	if err != nil {
		t.Fatal(err)
	}
	parentMismatch := slices.Contains([]string{"forged_parent", "deleted_forged_parent", "empty_extra_forged_parent", "repeated_parent", "aliased_parent", "new_parent_forged", "new_parent_none_spelling"}, observed.Name)
	if parentMismatch {
		if bound.Valid() || !slices.Contains(modelSetCodes(bound.FormSet().NonFormErrors())[string(validation.NonField)], "invalid_parent") {
			t.Fatal("parent mismatch escaped whole-set admission")
		}
	} else if bound.Valid() != observed.Valid {
		t.Fatal("native validity differs", bound.Valid(), observed.Valid)
	}
	if len(calls) != len(observed.CleanCalls) || !reflect.DeepEqual(calls, observed.CleanCalls) {
		t.Fatal("clean did not see the server parent exactly once", calls, observed.CleanCalls)
	}
	set := bound.FormSet()
	if set.TotalForms() != len(observed.Forms) || set.InitialForms() != observed.Initial {
		t.Fatal("native display/initial counts differ")
	}
	if !parentMismatch && observed.Bound {
		if got := modelSetCodes(set.NonFormErrors())[string(validation.NonField)]; !slices.Equal(got, nativeUniqueCodes(observed.NonFormErrors)) {
			t.Fatal("native cross-row diagnostics differ", got)
		}
	}
	for index, row := range set.Forms() {
		want := observed.Forms[index]
		parentFields := 0
		for _, field := range row.Fields() {
			if field.Name() == foreign {
				parentFields++
				if field.Widget() != forms.HiddenInput || field.Required() || !want.ParentHidden || want.ParentRequired {
					t.Fatal("parent remained an editable/required field")
				}
			}
		}
		if parentFields != 1 || slices.Contains(row.Form().Changed(), foreign) {
			t.Fatal("parent changed the row or was duplicated")
		}
		instance, evaluated := bound.Instance(index)
		if evaluated {
			candidate := instance.BoundForm().Candidate()
			value, _ := candidate.Get(foreign)
			if want.CandidateParent == nil && !value.IsNull() || want.CandidateParent != nil && !value.Equal(forms.Integer(*want.CandidateParent)) {
				t.Fatal("candidate parent differs from server/native parent")
			}
			if observed.NewParent && instance.BoundForm().Form().Valid() {
				if _, err := instance.Prepare(); err == nil {
					t.Fatal("individual child prepared without its parent key")
				}
			}
		}
		if !parentMismatch && observed.Bound {
			wantErrors := map[string][]string{}
			for name, codes := range want.Errors {
				wantErrors[name] = nativeUniqueCodes(codes)
			}
			if row.Form().Valid() != want.Valid || !reflect.DeepEqual(modelSetCodes(row.Form().Errors()), wantErrors) {
				t.Fatal("native row errors differ", index, row.Form().Errors().All(), wantErrors)
			}
		}
	}
	if !bound.Valid() || observed.NewParent {
		if _, err := bound.Prepare(); err == nil {
			t.Fatal("invalid/pending parent preparation accepted")
		}
	} else if _, err := bound.Prepare(); err != nil {
		t.Fatal("saved parent preparation failed", err)
	}
	if len(calls) != len(observed.CleanCalls) {
		t.Fatal("inspection/preparation repeated callbacks")
	}
}

func TestInlineSetPendingParentCompletionAndScopeAreImmutable(t *testing.T) {
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	row := inlineRows(t, (helpdesk.LabelDescriptor{}).Metadata(), []string{"name"})
	spec, err := formmodel.NewInlineSpec(binding, helpdesk.CategoryObjects, helpdesk.LabelObjects, "category", row)
	if err != nil {
		t.Fatal(err)
	}
	parent := helpdesk.Category{Name: "private parent"}
	data := forms.NewData(map[string][]string{"items-TOTAL_FORMS": {"2"}, "items-INITIAL_FORMS": {"0"}, "items-0-name": {"private child"}})
	bound, err := spec.Bind(data, parent, nil, formmodel.PostClean{})
	if err != nil || !bound.Valid() {
		t.Fatal("new parent form did not validate", err)
	}
	if _, err := bound.Prepare(); err == nil {
		t.Fatal("pending parent prepared")
	}
	for _, key := range []int64{0, 71} {
		saved := helpdesk.NewCategoryWithID(key)
		prepared, err := bound.PrepareWithParent(saved)
		if err != nil || len(prepared.Rows()) != 1 || prepared.Rows()[0].Existing() {
			t.Fatal("generated/zero parent identity lost", err)
		}
		child, err := prepared.Rows()[0].Model()
		if err != nil || child.CategoryID != key || child.Name != "private child" {
			t.Fatal("prepared child lost parent or submitted input", err)
		}
		instance, _ := bound.Instance(0)
		if value, _ := instance.BoundForm().Candidate().Get("category"); !value.IsNull() || !bound.ParentIdentity().IsNull() {
			t.Fatal("completion mutated pending candidate")
		}
	}
	if _, err := spec.Bind(data, parent, inlineLabelCurrent(), formmodel.PostClean{}); err == nil {
		t.Fatal("unsaved parent adopted existing child rows")
	}
	if _, err := spec.Unbound(helpdesk.NewCategoryWithID(2), inlineLabelCurrent()); err == nil {
		t.Fatal("cross-parent current rows accepted")
	}
	saved, err := spec.Bind(forms.NewData(map[string][]string{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"0"}, "items-0-name": {"new"}}), helpdesk.NewCategoryWithID(1), nil, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saved.PrepareWithParent(helpdesk.NewCategoryWithID(2)); err == nil {
		t.Fatal("saved parent retargeted during preparation")
	}
	if _, err := spec.Bind(data, parent, nil, formmodel.PostClean{Fields: []string{"category"}, Clean: func(forms.Values) (forms.Values, validation.Errors) { return forms.Values{}, validation.Errors{} }}); err == nil {
		t.Fatal("model clean allowed to own the parent")
	}
	if _, err := formmodel.NewInlineSpec(binding, helpdesk.TicketObjects, helpdesk.LabelObjects, "category", row); err == nil {
		t.Fatal("wrong parent model accepted")
	}
	if _, err := formmodel.NewInlineSpec(orm.ProjectBinding{}, helpdesk.CategoryObjects, helpdesk.LabelObjects, "category", row); err == nil {
		t.Fatal("unbound project accepted")
	}
	if _, err := formmodel.NewInlineSpec(binding, helpdesk.CategoryObjects, helpdesk.LabelObjects, "name", row); err == nil {
		t.Fatal("non-relation accepted")
	}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(verb, bound), "private") || strings.Contains(fmt.Sprintf(verb, spec), "private") {
			t.Fatal("inline formatting leaked parent/child data")
		}
	}
	var workers sync.WaitGroup
	for key := int64(1); key <= 12; key++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			prepared, err := bound.PrepareWithParent(helpdesk.NewCategoryWithID(key))
			if err != nil {
				t.Error(err)
				return
			}
			child, err := prepared.Rows()[0].Model()
			if err != nil || child.CategoryID != key {
				t.Error("concurrent completions shared parent state", err)
			}
		}()
	}
	workers.Wait()
}

func TestInlineNullableCrossAppParentCannotPrepareAnOrphan(t *testing.T) {
	binding, err := relationproject.Bind()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewInlineSpec(binding, authors.AuthorObjects, blog.PostObjects, "reviewer", inlineRows(t, (blog.PostDescriptor{}).Metadata(), []string{"title"}))
	if err != nil {
		t.Fatal(err)
	}
	post := formmodel.PostClean{Fields: []string{"author"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
		return forms.NewValues(map[string]forms.Value{"author": forms.Integer(9)}), validation.Errors{}
	}}
	data := forms.NewData(map[string][]string{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"0"}, "items-0-title": {"review"}})
	bound, err := spec.Bind(data, authors.Author{Name: "pending"}, nil, post)
	if err != nil || !bound.Valid() {
		t.Fatal("nullable cross-app inline did not validate", err)
	}
	instance, _ := bound.Instance(0)
	if _, err := instance.Prepare(); err == nil {
		t.Fatal("nullable inline silently prepared an orphan")
	}
	prepared, err := bound.PrepareWithParent(authors.NewAuthorWithID(0))
	if err != nil {
		t.Fatal(err)
	}
	value, err := prepared.Rows()[0].Model()
	if err != nil || value.AuthorID != 9 || value.ReviewerID == nil || *value.ReviewerID != 0 {
		t.Fatal("cross-app/nullable parent or other relation lost", err)
	}
	*value.ReviewerID = 42
	again, err := prepared.Rows()[0].Model()
	if err != nil || again.ReviewerID == nil || *again.ReviewerID != 0 {
		t.Fatal("nullable parent pointer escaped preparation ownership")
	}
}

type defaultParentLabelDescriptor struct{ helpdesk.LabelDescriptor }

func (defaultParentLabelDescriptor) Metadata() ir.Model {
	model := (helpdesk.LabelDescriptor{}).Metadata()
	for index := range model.Fields {
		if model.Fields[index].Name == "category" {
			model.Fields[index].Default = &ir.Scalar{Kind: ir.ScalarInteger, Integer: 99}
		}
	}
	return model
}

func TestInlineRejectsNoncanonicalForeignKeyPolicy(t *testing.T) {
	child := orm.NewManager[helpdesk.Label](defaultParentLabelDescriptor{})
	metadata, err := child.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	// ForeignKey defaults are not supported by current Schema IR. A custom
	// manager cannot smuggle that different policy into a canonical binding.
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := formmodel.NewInlineSpec(binding, helpdesk.CategoryObjects, child, "category", inlineRows(t, metadata, []string{"name"})); err == nil {
		t.Fatal("manager policy bypassed project relation metadata")
	}
}
