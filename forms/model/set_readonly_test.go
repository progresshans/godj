package model_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"

	helpdesk "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/validation"
)

func TestReadOnlyInlineRowsAgainstPinnedDjangoAdmin(t *testing.T) {
	var reference struct {
		Django, Python, Backend string
		Source                  string `json:"source_sha256"`
		Unchanged               bool   `json:"storage_unchanged"`
		Cleanup                 bool   `json:"cleanup_empty"`
		Cases                   []struct {
			Name                 string
			Bound, Valid, Narrow bool
			Add                  bool `json:"can_add"`
			Delete               bool `json:"can_delete"`
			Data                 map[string][]string
			Calls                []string `json:"clean_calls"`
			Saved                []string `json:"save_names"`
			Deleted              []int64  `json:"deleted_ids"`
			Rows                 []struct {
				Candidate string `json:"candidate_name"`
			}
		}
	}
	raw, err := os.ReadFile("testdata/admin-inline-readonly-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != "sqlite" ||
		reference.Source != "7526be63d08ff1338acc3e78e56171683bcdc697d32ce4af1de17aff8f328a48" || !reference.Unchanged || !reference.Cleanup || len(reference.Cases) != 10 {
		t.Fatal("unexpected Admin inline reference")
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	for _, observed := range reference.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			limit := 64
			if observed.Narrow {
				limit = 3
			}
			name, err := forms.CharField("name", forms.WithMaxLength(limit))
			if err != nil {
				t.Fatal(err)
			}
			row, err := forms.NewSpec([]forms.Field{name})
			if err != nil {
				t.Fatal(err)
			}
			config := forms.DefaultSetConfig()
			config.Prefix, config.ExtraForms, config.ReadOnlyInitial, config.CanDelete = "items", 0, true, observed.Delete
			if observed.Add {
				config.ExtraForms = 1
			}
			rows, err := forms.NewSetSpec(row, config)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := formmodel.NewInlineSpec(binding, helpdesk.CategoryObjects, helpdesk.LabelObjects, "category", rows)
			if err != nil {
				t.Fatal(err)
			}
			current := helpdesk.NewLabelWithID(10)
			current.Name, current.CategoryID = "kept", 1
			calls := []string{}
			post := formmodel.PostClean{Clean: func(values forms.Values) (forms.Values, validation.Errors) {
				name, _ := values.String("name")
				calls = append(calls, name)
				return forms.Values{}, validation.Errors{}
			}}
			var set formmodel.InlineSet[helpdesk.Category, helpdesk.Label]
			if observed.Bound {
				set, err = spec.Bind(t.Context(), forms.NewData(observed.Data), helpdesk.NewCategoryWithID(1), []helpdesk.Label{current}, post)
			} else {
				set, err = spec.Unbound(helpdesk.NewCategoryWithID(1), []helpdesk.Label{current})
			}
			wantValid := observed.Valid
			// Native Admin checks the new row against the still-stored old row.
			// Pure preparation can describe delete+replacement; the consumer's
			// final database validation and write ordering remain explicit.
			if observed.Name == "replace_deleted" {
				wantValid = true
			}
			if err != nil || set.Valid() != wantValid || len(set.FormSet().Forms()) != len(observed.Rows) {
				t.Fatal("native validity/display differs", err)
			}
			if !set.FormSet().Forms()[0].Form().ReadOnly() {
				t.Fatal("initial row remained editable")
			}
			if observed.Bound {
				instance, present := set.Instance(0)
				if !present {
					t.Fatal("read-only current snapshot unavailable")
				}
				candidate, _ := instance.BoundForm().Candidate().String("name")
				if candidate != "kept" || candidate != observed.Rows[0].Candidate {
					t.Fatal("read-only model accepted submitted input")
				}
				if _, err := instance.Prepare(); err == nil {
					t.Fatal("independent preparation exposed read-only write input")
				}
			}
			wantCalls := observed.Calls
			// Django cleans ordinary fields on a deleted view-only row, then
			// ignores its errors. Go keeps that row's server snapshot and cleans
			// only explicitly enabled controls, so it never runs that model clean.
			if _, requested := observed.Data["items-0-DELETE"]; requested {
				wantCalls = wantCalls[1:]
			}
			if !slices.Equal(calls, wantCalls) {
				t.Fatal("editable model-clean calls differ", calls, wantCalls)
			}
			prepared, err := set.Prepare()
			if !wantValid {
				if err == nil {
					t.Fatal("invalid/unbound set prepared")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			names, deleted := []string{}, []int64{}
			for _, row := range prepared.Rows() {
				value, err := row.Model()
				if err != nil || row.Existing() {
					t.Fatal("read-only row reached persistence preparation", err)
				}
				names = append(names, value.Name)
			}
			for _, row := range prepared.Deleted() {
				value, err := row.Model()
				if err != nil {
					t.Fatal(err)
				}
				deleted = append(deleted, value.ID)
			}
			wantSaved, wantDeleted := observed.Saved, observed.Deleted
			if observed.Name == "replace_deleted" {
				wantSaved, wantDeleted = []string{"kept"}, []int64{10}
			}
			if !reflect.DeepEqual(names, wantSaved) || !reflect.DeepEqual(deleted, wantDeleted) {
				t.Fatal("native write/delete selection differs", names, deleted)
			}
		})
	}
}

func TestReadOnlyInlineKeepsIdentityParentAndCandidateOwnership(t *testing.T) {
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	row := inlineRows(t, (helpdesk.LabelDescriptor{}).Metadata(), []string{"name"})
	config := row.Config()
	config.ReadOnlyInitial = true
	row, err = row.WithConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewInlineSpec(binding, helpdesk.CategoryObjects, helpdesk.LabelObjects, "category", row)
	if err != nil {
		t.Fatal(err)
	}
	current := helpdesk.NewLabelWithID(10)
	current.Name, current.CategoryID = "kept", 1
	for _, forged := range []string{"parent", "identity", "none"} {
		data := map[string][]string{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"1"}, "items-0-id": {"10"}, "items-0-category": {"1"}, "items-0-name": {"forged"}}
		if forged == "parent" {
			data["items-0-category"] = []string{"2"}
		}
		if forged == "identity" {
			data["items-0-id"] = []string{"99"}
		}
		set, err := spec.Bind(t.Context(), forms.NewData(data), helpdesk.NewCategoryWithID(1), []helpdesk.Label{current}, formmodel.PostClean{Clean: func(forms.Values) (forms.Values, validation.Errors) {
			t.Error("read-only model clean executed")
			return forms.Values{}, validation.Errors{}
		}})
		if err != nil || set.Valid() != (forged == "none") {
			t.Fatal("read-only bypassed request admission", forged, err)
		}
		if forged != "none" {
			if _, err := set.Prepare(); err == nil {
				t.Fatal("forged read-only set prepared")
			}
			continue
		}
		var workers sync.WaitGroup
		for range 8 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				result, err := set.Prepare()
				if err != nil || len(result.Rows()) != 0 || len(result.Deleted()) != 0 {
					t.Error("read-only preparation produced a mutation", err)
				}
			}()
		}
		workers.Wait()
		instance, _ := set.Instance(0)
		rejected, err := instance.WithErrors(validation.NewErrors(validation.New("name", "stored_error")))
		if err != nil || !rejected.BoundForm().Form().ReadOnly() {
			t.Fatal("diagnostic removed read-only policy", err)
		}
		if _, err := rejected.Prepare(); err == nil {
			t.Fatal("diagnostic converted snapshot to write input")
		}
	}
}
