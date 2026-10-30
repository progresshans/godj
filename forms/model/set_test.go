package model_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	article "github.com/progresshans/godj/examples/article/models"
	helpdesk "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

type setArticleState struct {
	ID        *int64  `json:"id"`
	Title     string  `json:"title"`
	Summary   *string `json:"summary"`
	Published bool    `json:"published"`
}

func setArticles() []article.Article {
	summary := "stored-one"
	one := article.NewArticleWithID(1)
	one.Title = "one"
	one.Summary = &summary
	one.Published = true
	two := article.NewArticleWithID(2)
	two.Title = "two"
	return []article.Article{one, two}
}
func articleSetSpec(t *testing.T, config forms.SetConfig, validators ...forms.CrossValidator) forms.SetSpec {
	t.Helper()
	definition := formmodel.Definition{Fields: []string{"title"}, Overrides: []formmodel.Override{formmodel.OverrideField("title", formmodel.WithMaxLength(8))}, Validators: validators}
	row, err := definition.Spec((article.ArticleDescriptor{}).Metadata())
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
func modelSetData(rows ...map[string]string) forms.Data {
	values := map[string][]string{"items-TOTAL_FORMS": {strconv.Itoa(len(rows))}, "items-INITIAL_FORMS": {"2"}}
	for index, row := range rows {
		for name, value := range row {
			values[fmt.Sprintf("items-%d-%s", index, name)] = []string{value}
		}
	}
	return forms.NewData(values)
}
func candidateArticle(values forms.Values) setArticleState {
	result := setArticleState{}
	if id, ok := values.Integer("id"); ok {
		result.ID = &id
	}
	result.Title, _ = values.String("title")
	result.Published, _ = values.Boolean("published")
	if summary, ok := values.String("summary"); ok {
		result.Summary = &summary
	}
	return result
}
func typedArticle(value article.Article) setArticleState {
	result := setArticleState{Title: value.Title, Summary: value.Summary, Published: value.Published}
	if key, present := (article.ArticleDescriptor{}).PrimaryKey(value); present {
		id, _ := key.Integer()
		result.ID = &id
	}
	return result
}
func modelSetCodes(errors validation.Errors) map[string][]string {
	result := map[string][]string{}
	for _, failure := range errors.All() {
		field := string(failure.Field())
		result[field] = append(result[field], string(failure.Code()))
	}
	return result
}

func TestInstanceSetsAgainstPinnedDjango(t *testing.T) {
	data, err := os.ReadFile("testdata/formset-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Python, Backend string
		StorageUnchanged        bool `json:"storage_unchanged"`
		Cases                   []struct {
			Name         string
			Bound, Valid bool
			Options      map[string]json.RawMessage
			Data         map[string]string
			Forms        []struct {
				Valid        bool
				Candidate    setArticleState
				Changed      []string
				Errors       map[string][]string
				CleanedTitle *string `json:"cleaned_title"`
			}
			Errors     []string
			Prepared   []setArticleState
			Deleted    []int64
			Ordered    []int
			CleanCalls []*int64 `json:"clean_calls"`
		}
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != "sqlite" || !reference.StorageUnchanged || len(reference.Cases) != 20 {
		t.Fatal("unexpected native model formset reference")
	}
	identityCases := []string{"missing_identity", "invalid_identity", "outside_identity", "duplicate_identity", "extra_identity", "empty_extra_identity", "deleted_outside_identity", "deleted_missing_identity", "deleted_extra_identity"}
	stricter := 0
	for _, observed := range reference.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			config := forms.DefaultSetConfig()
			config.Prefix = "items"
			for name, raw := range observed.Options {
				var target any
				switch name {
				case "can_delete":
					target = &config.CanDelete
				case "can_order":
					target = &config.CanOrder
				case "max_num":
					target = &config.MaxForms
				case "validate_max":
					target = &config.ValidateMax
				default:
					t.Fatal("unexpected native option", name)
				}
				if err := json.Unmarshal(raw, target); err != nil {
					t.Fatal(err)
				}
			}
			config.AbsoluteMax = config.MaxForms + 1000
			spec := articleSetSpec(t, config)
			current := setArticles()
			var calls []*int64
			post := formmodel.PostClean{Fields: []string{"summary"}, Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
				var key *int64
				if id, ok := candidate.Integer("id"); ok {
					key = &id
				}
				calls = append(calls, key)
				title, _ := candidate.String("title")
				var errors validation.Errors
				if title == "blocked" {
					errors = validation.NewErrors(validation.New("title", "blocked_title"))
				}
				return forms.NewValues(map[string]forms.Value{"summary": forms.String("clean:" + title)}), errors
			}}
			var bound formmodel.InstanceSet[article.Article]
			var err error
			if observed.Bound {
				raw := map[string][]string{}
				for name, value := range observed.Data {
					raw[name] = []string{value}
				}
				bound, err = formmodel.BindSet(t.Context(), article.ArticleObjects, spec, forms.NewData(raw), current, post)
			} else {
				bound, err = formmodel.UnboundSet(article.ArticleObjects, spec, current)
			}
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(identityCases, observed.Name) {
				if observed.Valid {
					stricter++
				}
				if bound.Valid() || len(modelSetCodes(bound.FormSet().NonFormErrors())[string(validation.NonField)]) == 0 {
					t.Fatal("identity failure did not invalidate the whole request")
				}
				if _, err := bound.Prepare(); err == nil {
					t.Fatal("forged/deleted identity prepared")
				}
				return
			}
			if bound.Valid() != observed.Valid || bound.FormSet().Bound() != observed.Bound || bound.FormSet().TotalForms() != len(observed.Forms) {
				t.Fatal("model formset validity/count differs")
			}
			if !slices.Equal(modelSetCodes(bound.FormSet().NonFormErrors())[string(validation.NonField)], observed.Errors) {
				t.Fatal("set error priority differs", modelSetCodes(bound.FormSet().NonFormErrors()))
			}
			if !reflect.DeepEqual(calls, observed.CleanCalls) && !(len(calls) == 0 && len(observed.CleanCalls) == 0) {
				t.Fatal("model clean calls/identity order differ", calls, observed.CleanCalls)
			}
			for index, want := range observed.Forms {
				row := bound.FormSet().Forms()[index]
				if row.Form().Valid() != want.Valid || !reflect.DeepEqual(modelSetCodes(row.Form().Errors()), want.Errors) {
					t.Fatal("row validity/errors differ", index, modelSetCodes(row.Form().Errors()), want.Errors)
				}
				instance, present := bound.Instance(index)
				if !observed.Bound || index >= 2 && len(row.Form().Changed()) == 0 {
					if present {
						t.Fatal("unbound/empty extra manufactured candidate")
					}
					continue
				}
				if !present || !reflect.DeepEqual(candidateArticle(instance.BoundForm().Candidate()), want.Candidate) {
					t.Fatal("model candidate differs", index, candidateArticle(instance.BoundForm().Candidate()), want.Candidate)
				}
				if !slices.Equal(row.Form().Changed(), want.Changed) {
					t.Fatal("business/order/deletion changes differ", index, row.Form().Changed(), want.Changed)
				}
				input, err := instance.BoundForm().Input()
				if err == nil {
					if _, present := input.Get("id"); present {
						t.Fatal("identity entered writable form input")
					}
				}
			}
			if !bound.Valid() {
				if _, err := bound.Prepare(); err == nil {
					t.Fatal("invalid or unbound set prepared")
				}
				return
			}
			prepared, err := bound.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			// Prepare intentionally retains unchanged current candidates. Native
			// save(commit=False) returns only changed/current and nonempty new rows.
			deferred := []setArticleState{}
			for _, row := range prepared.Rows() {
				value, err := row.Model()
				if err != nil {
					t.Fatal(err)
				}
				if !row.Existing() || len(row.Changed()) != 0 {
					deferred = append(deferred, typedArticle(value))
				}
			}
			if !reflect.DeepEqual(deferred, observed.Prepared) {
				t.Fatal("deferred model values differ", deferred, observed.Prepared)
			}
			deleted := []int64{}
			for _, row := range prepared.Deleted() {
				value, err := row.Model()
				if err != nil {
					t.Fatal(err)
				}
				deleted = append(deleted, value.ID)
			}
			if !slices.Equal(deleted, observed.Deleted) {
				t.Fatal("existing delete identities differ", deleted, observed.Deleted)
			}
			if config.CanOrder {
				ordered, err := bound.FormSet().OrderedForms()
				if err != nil {
					t.Fatal(err)
				}
				indices := []int{}
				for _, row := range ordered {
					indices = append(indices, row.Index())
				}
				if !slices.Equal(indices, observed.Ordered) {
					t.Fatal("ordering differs")
				}
			}
			if !reflect.DeepEqual(current, setArticles()) {
				t.Fatal("binding/preparation mutated current models")
			}
		})
	}
	if stricter != 6 {
		t.Fatal("native acceptance differences not accounted for", stricter)
	}
}

func TestInstanceSetOwnsSnapshotsAndKeepsAdmissionSeparateFromDeletedDataErrors(t *testing.T) {
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	config.CanDelete = true
	spec := articleSetSpec(t, config)
	current := setArticles()
	bound, err := formmodel.BindSet(t.Context(), article.ArticleObjects, spec, modelSetData(map[string]string{"id": "2", "title": "updated"}, map[string]string{"id": "1", "title": "one", "DELETE": "on"}, nil), current, formmodel.PostClean{})
	if err != nil || !bound.Valid() {
		t.Fatal(err)
	}
	current[0].Title = "caller"
	*current[0].Summary = "caller"
	key, present := bound.Identity(0)
	id, ok := key.AsInteger()
	if !present || !ok || id != 2 {
		t.Fatal("identity order came from row position")
	}
	name, err := bound.IdentityName(0)
	if err != nil || name != "items-0-id" {
		t.Fatal("identity field name", err)
	}
	if _, present := bound.Identity(2); present {
		t.Fatal("extra acquired identity")
	}
	copy, present, err := bound.Current(1)
	if err != nil || !present || copy.Title != "one" || *copy.Summary != "stored-one" {
		t.Fatal("current snapshot retained caller state", err)
	}
	*copy.Summary = "mutated-copy"
	copy, _, err = bound.Current(1)
	if err != nil || *copy.Summary != "stored-one" {
		t.Fatal("current getter leaked pointee", err)
	}
	dataRejected, err := bound.WithRowErrors(1, validation.NewErrors(validation.New("title", "data_error")))
	if err != nil || !dataRejected.Valid() || !bound.FormSet().Forms()[1].Form().Errors().Empty() {
		t.Fatal("deleted row data errors mutated source/blocked deletion", err)
	}
	denied, err := dataRejected.WithErrors(validation.NewErrors(validation.New(validation.NonField, "permission_denied")))
	if err != nil || denied.Valid() {
		t.Fatal("deletion suppressed admission denial", err)
	}
	if _, err := denied.Prepare(); err == nil {
		t.Fatal("denied set prepared")
	}
	activeRejected, err := bound.WithRowErrors(0, validation.NewErrors(validation.New("title", "unique")))
	if err != nil || activeRejected.Valid() {
		t.Fatal("active row rejection ignored", err)
	}
	instance, present := activeRejected.Instance(0)
	if !present || instance.BoundForm().Form().Valid() {
		t.Fatal("typed row rejection out of sync")
	}
	if _, err := bound.WithRowErrors(2, validation.NewErrors(validation.New("title", "invalid"))); err == nil {
		t.Fatal("unevaluated extra accepted typed errors")
	}
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	rows := prepared.Rows()
	if len(rows) != 1 || len(prepared.Deleted()) != 1 {
		t.Fatal("wrong active/deleted selection")
	}
	model, err := prepared.Deleted()[0].Model()
	if err != nil {
		t.Fatal(err)
	}
	*model.Summary = "changed"
	again, err := prepared.Deleted()[0].Model()
	if err != nil || *again.Summary != "stored-one" {
		t.Fatal("delete snapshot is mutable", err)
	}
	for _, value := range []any{bound, prepared, rows[0], prepared.Deleted()[0]} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			text := fmt.Sprintf(verb, value)
			if strings.Contains(text, "stored-one") || strings.Contains(text, "updated") {
				t.Fatal("model formset leaked content")
			}
		}
	}
	for _, bad := range [][]article.Article{{article.Article{}}, {setArticles()[0], setArticles()[0]}} {
		if _, err := formmodel.UnboundSet(article.ArticleObjects, spec, bad); err == nil {
			t.Fatal("unsaved/duplicate current identity accepted")
		}
	}
	if _, err := (formmodel.InstanceSet[article.Article]{}).Prepare(); err == nil {
		t.Fatal("zero model set prepared")
	}
}

func TestInstanceSetTypedCollectionsAndConcurrentPreparation(t *testing.T) {
	metadata := (helpdesk.TicketDescriptor{}).Metadata()
	row, err := formmodel.NewSpecForFields(metadata, []string{"subject", "labels"})
	if err != nil {
		t.Fatal(err)
	}
	row, err = row.WithModelChoices("labels", forms.Choice{Value: forms.Integer(7), Label: "First"}, forms.Choice{Value: forms.Integer(8), Label: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "tickets"
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	current := helpdesk.NewTicketWithID(5)
	current.Subject = "old"
	current.CategoryID = 9
	keys := []int64{7}
	reader := func(value helpdesk.Ticket, field ir.ManyToManyField) ([]int64, bool) {
		return keys, value.ID == 5 && field.Name == "labels"
	}
	post := formmodel.PostClean{Fields: []string{"category"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
		return forms.NewValues(map[string]forms.Value{"category": forms.Integer(9)}), validation.Errors{}
	}}
	data := forms.NewData(map[string][]string{"tickets-TOTAL_FORMS": {"2"}, "tickets-INITIAL_FORMS": {"1"}, "tickets-0-id": {"5"}, "tickets-0-subject": {"changed"}, "tickets-0-labels": {"8"}, "tickets-0-category": {"999"}, "tickets-1-subject": {"new"}, "tickets-1-labels": {"7"}})
	bound, err := formmodel.BindSet(t.Context(), helpdesk.TicketObjects, spec, data, []helpdesk.Ticket{current}, post, reader)
	if err != nil || !bound.Valid() {
		t.Fatal(err)
	}
	keys[0] = 88
	if _, err := formmodel.UnboundSet(helpdesk.TicketObjects, spec, []helpdesk.Ticket{current}); err == nil {
		t.Fatal("selected collection read was silently omitted")
	}
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Rows()) != 2 {
		t.Fatal("missing typed rows")
	}
	for index, row := range prepared.Rows() {
		value, err := row.Model()
		if err != nil || value.CategoryID != 9 {
			t.Fatal("server-owned scope lost", err)
		}
		want := int64(8)
		if index == 1 {
			want = 7
		}
		got, ok := row.Prepared().Collections().Integers("labels")
		if !ok || !slices.Equal(got, []int64{want}) {
			t.Fatal("selected collection intent lost", got)
		}
		got[0] = 0
		again, _ := row.Prepared().Collections().Integers("labels")
		if again[0] != want {
			t.Fatal("collection intent aliased")
		}
	}
	var wait sync.WaitGroup
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := bound.Prepare()
			if err != nil || len(result.Rows()) != 2 {
				failures <- fmt.Errorf("concurrent prepare: %v", err)
				return
			}
			for _, row := range result.Rows() {
				if _, err := row.Model(); err != nil {
					failures <- err
				}
			}
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestInstanceSetCleansEachEvaluatedRowOnceAndRetainsSubmission(t *testing.T) {
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	fieldCalls, modelCalls := 0, 0
	spec := articleSetSpec(t, config, forms.CrossValidatorFunc(func(forms.Values) validation.Errors {
		fieldCalls++
		return validation.Errors{}
	}))
	post := formmodel.PostClean{Clean: func(forms.Values) (forms.Values, validation.Errors) {
		modelCalls++
		return forms.Values{}, validation.Errors{}
	}}
	bound, err := formmodel.BindSet(t.Context(), article.ArticleObjects, spec, modelSetData(map[string]string{"id": "01", "title": " changed "}, map[string]string{"id": "2", "title": "two"}, nil), setArticles(), post)
	if err != nil || !bound.Valid() {
		t.Fatal("valid identity alias/input rejected", err)
	}
	if fieldCalls != 2 || modelCalls != 2 {
		t.Fatal("field/model cleaning repeated or empty extra evaluated", fieldCalls, modelCalls)
	}
	instance, present := bound.Instance(0)
	if !present {
		t.Fatal("missing evaluated model")
	}
	raw, _ := instance.BoundForm().Form().Submitted().Get("title")
	title, _ := instance.BoundForm().Candidate().String("title")
	if !slices.Equal(raw, []string{" changed "}) || title != "changed" {
		t.Fatal("model adapter rebound cleaned values or lost normalization")
	}
	for range 2 {
		if _, err := bound.Prepare(); err != nil {
			t.Fatal(err)
		}
	}
	if fieldCalls != 2 || modelCalls != 2 {
		t.Fatal("typed preparation reran validation")
	}
}
