package helpdesk

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/validation"
)

// Called by the existing actual SQLite/PostgreSQL Form persistence owner.
// The public ModelForm/InlineSpec APIs share the supplied borrowed transaction.
func verifyInlineParentPersistence(t *testing.T, backend formSaveDatabase) {
	t.Helper()
	raw, err := os.ReadFile("../../forms/model/testdata/inline-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Storage []struct {
			Name                 string
			Error                *string
			ParentKeyAssigned    bool `json:"parent_key_assigned"`
			ParentStored         bool `json:"parent_stored"`
			Children             []string
			WritesBeforeTerminal int  `json:"writes_before_terminal"`
			DeferredWrites       *int `json:"deferred_writes"`
		}
	}
	if err := json.Unmarshal(raw, &reference); err != nil || len(reference.Storage) != 4 {
		t.Fatal("missing independent inline persistence reference", err)
	}
	parentSpec, err := formmodel.NewSpecForFields((models.CategoryDescriptor{}).Metadata(), []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	childSpec, err := formmodel.NewSpecForFields((models.LabelDescriptor{}).Metadata(), []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	rows, err := forms.NewSetSpec(childSpec, config)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	inlineSpec, err := formmodel.NewInlineSpec(binding, models.CategoryObjects, models.LabelObjects, "category", rows)
	if err != nil {
		t.Fatal(err)
	}
	for _, observed := range reference.Storage {
		t.Run(observed.Name, func(t *testing.T) {
			bound, err := formmodel.BindInstance(models.CategoryObjects, parentSpec, forms.NewData(map[string][]string{"name": {"inline-" + observed.Name}}), nil, formmodel.PostClean{})
			if err != nil {
				t.Fatal(err)
			}
			preparedParent, err := bound.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			parent, err := preparedParent.Model()
			if err != nil {
				t.Fatal(err)
			}
			post := formmodel.PostClean{}
			if observed.Name == "late_failure" {
				// Model clean can produce a DB conflict after cross-row input
				// validation. The second actual save must abort parent + first child.
				post = formmodel.PostClean{Fields: []string{"name"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
					return forms.NewValues(map[string]forms.Value{"name": forms.String("first")}), validation.Errors{}
				}}
			}
			children, err := inlineSpec.Bind(forms.NewData(map[string][]string{"items-TOTAL_FORMS": {"2"}, "items-INITIAL_FORMS": {"0"}, "items-0-name": {"first"}, "items-1-name": {"second"}}), parent, nil, post)
			if err != nil || !children.Valid() {
				t.Fatal("pending children did not validate", err)
			}
			writes := 0
			err = backend.AtomicRelation(t.Context(), func(session db.RelationSession) error {
				if observed.Name != "unsaved_parent" {
					if err := preparedParent.Save(t.Context(), session, &parent); err != nil {
						return err
					}
				}
				prepared, err := children.PrepareWithParent(parent)
				if err != nil {
					return err
				}
				if observed.DeferredWrites != nil {
					count, err := models.LabelObjects.Using(session).Count(t.Context())
					if err != nil {
						return err
					}
					if writes != *observed.DeferredWrites || count != beforeInlineLabels(t, session, parent.ID) {
						t.Fatal("pure preparation wrote children")
					}
				}
				for _, row := range prepared.Rows() {
					child, err := row.Model()
					if err != nil {
						return err
					}
					if child.CategoryID != parent.ID {
						t.Fatal("child lost assigned parent")
					}
					if err := row.Prepared().Save(t.Context(), session, &child); err != nil {
						return err
					}
					writes++
				}
				return nil
			})
			if (err != nil) != (observed.Error != nil) || writes != observed.WritesBeforeTerminal {
				t.Fatal("native terminal result/write prefix differs", err, writes)
			}
			_, hasKey := (models.CategoryDescriptor{}).PrimaryKey(parent)
			if hasKey != observed.ParentKeyAssigned || !children.ParentIdentity().IsNull() {
				t.Fatal("key allocation was mistaken for storage or mutated pending snapshot")
			}
			stored, err := models.CategoryObjects.Using(backend).Filter(models.CategoryFields.Name.Exact("inline-" + observed.Name)).All(t.Context())
			if err != nil || (len(stored) == 1) != observed.ParentStored {
				t.Fatal("parent transaction outcome differs", err)
			}
			labels, err := models.LabelObjects.Using(backend).OrderBy(models.LabelFields.ID.Asc()).All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, label := range labels {
				if hasKey && label.CategoryID == parent.ID {
					names = append(names, label.Name)
				}
			}
			if !reflect.DeepEqual(names, observed.Children) {
				t.Fatal("child rows survived wrong transaction outcome", names, observed.Children)
			}
		})
	}
}

func verifyReadOnlyInlinePersistence(t *testing.T, backend formSaveDatabase) {
	t.Helper()
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	childSpec, err := formmodel.NewSpecForFields((models.LabelDescriptor{}).Metadata(), []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix, config.ReadOnlyInitial = "items", true
	rows, err := forms.NewSetSpec(childSpec, config)
	if err != nil {
		t.Fatal(err)
	}
	readOnly, err := formmodel.NewInlineSpec(binding, models.CategoryObjects, models.LabelObjects, "category", rows)
	if err != nil {
		t.Fatal(err)
	}
	parent := models.Category{Name: "inline-readonly"}
	old := models.Label{Name: "kept"}
	writes := 0
	err = backend.AtomicRelation(t.Context(), func(session db.RelationSession) error {
		if err := models.CategoryObjects.Save(t.Context(), session, &parent); err != nil {
			return err
		}
		old.CategoryID = parent.ID
		if err := models.LabelObjects.Save(t.Context(), session, &old); err != nil {
			return err
		}
		children, err := readOnly.Bind(forms.NewData(map[string][]string{"items-TOTAL_FORMS": {"2"}, "items-INITIAL_FORMS": {"1"}, "items-0-id": {strconv.FormatInt(old.ID, 10)}, "items-0-name": {"forged"}, "items-1-name": {"new"}}), parent, []models.Label{old}, formmodel.PostClean{})
		if err != nil {
			return err
		}
		prepared, err := children.Prepare()
		if err != nil {
			return err
		}
		if len(prepared.Rows()) != 1 || prepared.Rows()[0].Existing() || len(prepared.Deleted()) != 0 {
			t.Fatal("read-only row reached save selection")
		}
		for _, row := range prepared.Rows() {
			child, err := row.Model()
			if err != nil {
				return err
			}
			if err := row.Prepared().Save(t.Context(), session, &child); err != nil {
				return err
			}
			writes++
		}
		return nil
	})
	if err != nil || writes != 1 {
		t.Fatal("read-only/new child transaction failed", err, writes)
	}
	stored, found, err := models.LabelObjects.Using(backend).Filter(models.LabelFields.ID.Exact(old.ID)).OrderBy(models.LabelFields.ID.Asc()).First(t.Context())
	if err != nil || !found || stored.Name != "kept" || stored.CategoryID != parent.ID {
		t.Fatal("forged read-only input changed the database", err)
	}
}

func beforeInlineLabels(t *testing.T, reader db.Queryer, parentID int64) int64 {
	t.Helper()
	rows, err := models.LabelObjects.Using(reader).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.CategoryID == parentID {
			t.Fatal("prepare created a child before persistence")
		}
	}
	return int64(len(rows))
}
