package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"example.com/godj-model-save/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

//go:embed formset-reference.json
var setReference []byte

type nativeSetSave struct {
	Name           string
	Rows           []map[string]json.RawMessage
	Selected       []int
	Changed        [][]string
	Deleted        []int
	PrepareQueries int `json:"prepare_queries"`
	Events         [][]json.RawMessage
	Error          *string
	HasKeys        []bool `json:"has_keys"`
	Stored         []storedArticle
}

type setEvent struct {
	Index int
	Kind  string
}

func runSetSave(t *testing.T, b probeBackend, reset func(*testing.T), snapshot func(*testing.T) []storedArticle, labelsSaver, reviewersSaver formmodel.CollectionSaver[models.Article], remove func(context.Context, db.Session, models.Article) error) {
	t.Helper()
	var fixture struct {
		Django, Python, Backend string
		Cases                   []nativeSetSave
	}
	if err := json.Unmarshal(setReference, &fixture); err != nil || fixture.Django != "6.1" || fixture.Python != "3.14.3" || fixture.Backend != "sqlite" || len(fixture.Cases) != 13 {
		t.Fatal("native set fixture", err)
	}
	for _, observed := range fixture.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			reset(t)
			labels := map[string]models.Label{}
			for _, code := range []string{"a", "b", "c"} {
				value, err := models.LabelObjects.Create(t.Context(), b, models.NewLabelCreate(code))
				if err != nil {
					t.Fatal(err)
				}
				labels[code] = value
			}
			current := []models.Article{}
			for _, title := range []string{"one", "two", "three"} {
				value, err := models.ArticleObjects.Create(t.Context(), b, models.NewArticleCreate(title).WithHidden("stored"))
				if err != nil {
					t.Fatal(err)
				}
				current = append(current, value)
				for _, saver := range []formmodel.CollectionSaver[models.Article]{labelsSaver, reviewersSaver} {
					if err := saver.Save(t.Context(), b, value, []int64{labels["a"].ID}); err != nil {
						t.Fatal(err)
					}
				}
			}
			spec, err := formmodel.NewSpecForFields((models.ArticleDescriptor{}).Metadata(), []string{"title", "labels", "reviewers"})
			if err != nil {
				t.Fatal(err)
			}
			choices := []forms.Choice{}
			for _, code := range []string{"a", "b", "c"} {
				choices = append(choices, forms.Choice{Value: forms.Integer(labels[code].ID), Label: code})
			}
			for _, field := range []string{"labels", "reviewers"} {
				spec, err = spec.WithModelChoices(field, choices...)
				if err != nil {
					t.Fatal(err)
				}
			}
			config := forms.DefaultSetConfig()
			config.Prefix, config.CanDelete, config.CanOrder = "items", true, observed.Name == "order_only"
			setSpec, err := forms.NewSetSpec(spec, config)
			if err != nil {
				t.Fatal(err)
			}
			data := map[string][]string{"items-TOTAL_FORMS": {strconv.Itoa(len(observed.Rows))}, "items-INITIAL_FORMS": {"3"}}
			for i, row := range observed.Rows {
				for field, raw := range row {
					var values []string
					if field == "labels" || field == "reviewers" {
						var codes []string
						if err := json.Unmarshal(raw, &codes); err != nil {
							t.Fatal(err)
						}
						for _, code := range codes {
							values = append(values, strconv.FormatInt(labels[code].ID, 10))
						}
					} else {
						var value string
						if err := json.Unmarshal(raw, &value); err != nil {
							t.Fatal(err)
						}
						if field == "id" {
							index, err := strconv.Atoi(value)
							if err != nil || index < 1 || index > 3 {
								t.Fatal("fixture identity")
							}
							value = strconv.FormatInt(current[index-1].ID, 10)
						}
						values = []string{value}
					}
					data[fmt.Sprintf("items-%d-%s", i, field)] = values
				}
			}
			post := formmodel.PostClean{}
			if observed.Name == "post_clean_only" {
				post.Fields = []string{"hidden"}
				post.Clean = func(forms.Values) (forms.Values, validation.Errors) {
					return forms.NewValues(map[string]forms.Value{"hidden": forms.String("cleaned")}), validation.Errors{}
				}
			}
			set, err := formmodel.BindSet(t.Context(), models.ArticleObjects, setSpec, forms.NewData(data), current, post, func(models.Article, ir.ManyToManyField) ([]int64, bool) { return []int64{labels["a"].ID}, true })
			if err != nil || !set.Valid() {
				t.Fatal("bind native set", err)
			}
			before := snapshot(t)
			prepared, err := set.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			plan, err := prepared.SavePlan()
			if err != nil {
				t.Fatal(err)
			}
			selected, deleted := []int{}, []int{}
			changed := [][]string{}
			for _, row := range plan.Rows() {
				selected = append(selected, row.Index())
				changed = append(changed, row.Changed())
			}
			for _, row := range plan.Deleted() {
				deleted = append(deleted, row.Index())
			}
			if !reflect.DeepEqual(selected, observed.Selected) || !reflect.DeepEqual(changed, observed.Changed) || !reflect.DeepEqual(deleted, observed.Deleted) || observed.PrepareQueries != 0 || !reflect.DeepEqual(before, snapshot(t)) {
				t.Fatal("deferred selection differs", selected, changed, deleted)
			}
			failure := errors.New("synthetic late writer/collection failure")
			savers := []formmodel.CollectionSaver[models.Article]{reviewersSaver, labelsSaver}
			if strings.HasPrefix(observed.Name, "collection_failure") {
				savers[0].Save = func(ctx context.Context, backend db.Session, value models.Article, keys []int64) error {
					if value.Title == "new" {
						return failure
					}
					return reviewersSaver.Save(ctx, backend, value, keys)
				}
			}
			events := []setEvent{}
			save := func(backend db.Session) error {
				if strings.HasPrefix(observed.Name, "deferred") {
					// No deletion is implied by obtaining or saving the deferred models.
					if observed.Name == "deferred_delete" {
						for _, row := range plan.Deleted() {
							value, err := row.Model()
							if err != nil {
								return err
							}
							if err := remove(t.Context(), backend, value); err != nil {
								return err
							}
						}
					}
					for _, row := range plan.Rows() {
						row.Model().Title = "server-" + row.Model().Title
						if err := models.ArticleObjects.Save(t.Context(), backend, row.Model()); err != nil {
							return err
						}
					}
					return plan.SaveCollections(t.Context(), backend, savers...)
				}
				options := formmodel.SetSaveOptions[models.Article]{Collections: savers, Delete: func(ctx context.Context, backend db.Session, row formmodel.DeletedSetRow[models.Article]) error {
					value, err := row.Model()
					if err != nil {
						return err
					}
					return remove(ctx, backend, value)
				}}
				if strings.HasPrefix(observed.Name, "late_failure") {
					options.Collections = nil
					options.Save = func(ctx context.Context, backend db.Session, row *formmodel.SetSaveRow[models.Article]) error {
						if err := row.Save(ctx, backend, savers...); err != nil {
							return err
						}
						if !row.Existing() {
							return failure
						}
						return nil
					}
				}
				writes, err := plan.Save(t.Context(), backend, options)
				for _, write := range writes {
					events = append(events, setEvent{write.Index(), write.Kind()})
				}
				if err != nil {
					return err
				}
				if observed.Name == "new_repeat" {
					writes, err = plan.Save(t.Context(), backend, options)
					for _, write := range writes {
						events = append(events, setEvent{write.Index(), write.Kind()})
					}
				}
				return err
			}
			if strings.HasSuffix(observed.Name, "_atomic") {
				err = b.AtomicRelation(t.Context(), func(session db.RelationSession) error { return save(session) })
			} else {
				err = save(b)
			}
			if (err != nil) != (observed.Error != nil) || observed.Error != nil && !errors.Is(err, failure) {
				t.Fatal("native write outcome differs", err)
			}
			expectedEvents := []setEvent{}
			for _, raw := range observed.Events {
				if len(raw) != 2 {
					t.Fatal("native event shape")
				}
				var event setEvent
				if err := json.Unmarshal(raw[0], &event.Index); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw[1], &event.Kind); err != nil {
					t.Fatal(err)
				}
				expectedEvents = append(expectedEvents, event)
			}
			if !reflect.DeepEqual(events, expectedEvents) {
				t.Fatal("native write order differs", events, expectedEvents)
			}
			hasKeys := []bool{}
			for _, row := range plan.Rows() {
				_, hasKey := (models.ArticleDescriptor{}).PrimaryKey(*row.Model())
				hasKeys = append(hasKeys, hasKey)
			}
			if !reflect.DeepEqual(hasKeys, observed.HasKeys) {
				t.Fatal("native retained key differs", hasKeys, observed.HasKeys)
			}
			if actual := snapshot(t); !reflect.DeepEqual(actual, observed.Stored) {
				t.Fatalf("native stored state differs: got %+v; want %+v", actual, observed.Stored)
			}
		})
	}
}
