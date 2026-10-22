package model_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/conformance/relationfixture/authors"
	relationproject "github.com/progresshans/godj/conformance/relationfixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func labelChoicePolicy() formmodel.ModelChoiceConfig[models.Label] {
	return formmodel.ModelChoiceConfig[models.Label]{
		Label: func(row models.Label) (string, error) { return row.Name, nil },
		Search: func(term string) ([]orm.Predicate[models.Label], error) {
			if term == "" {
				return nil, nil
			}
			return []orm.Predicate[models.Label]{models.LabelFields.Name.IContains(term)}, nil
		},
		Order: []orm.Ordering[models.Label]{models.LabelFields.Name.Asc()},
	}
}

func newLabelChoices(t *testing.T, config formmodel.ModelChoiceConfig[models.Label]) formmodel.ModelChoices[models.Label] {
	t.Helper()
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	choices, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"}, "labels", models.LabelObjects, config)
	if err != nil {
		t.Fatal(err)
	}
	return choices
}

func TestRelationChoicesBindCanonicalFKOneToOneManyToManyAndCrossApp(t *testing.T) {
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	categories, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"}, "category", models.CategoryObjects, formmodel.ModelChoiceConfig[models.Category]{Label: func(models.Category) (string, error) { called++; return "", nil }})
	if err != nil || !categories.Initialized() || categories.Metadata().Kind != formmodel.ModelChoiceRelation || categories.Metadata().Multiple || categories.Metadata().Target.ModelName != "category" {
		t.Fatal("FK binding", err)
	}
	tickets, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "service_report"}, "ticket", models.TicketObjects, formmodel.ModelChoiceConfig[models.Ticket]{Label: func(models.Ticket) (string, error) { called++; return "", nil }})
	if err != nil || tickets.Metadata().Multiple || tickets.Metadata().Target.ModelName != "ticket" {
		t.Fatal("OneToOne binding", err)
	}
	labels := newLabelChoices(t, labelChoicePolicy())
	metadata := labels.Metadata()
	if metadata.Kind != formmodel.ModelChoiceRelation || !metadata.Multiple || metadata.Source.ModelName != "ticket" || metadata.Field != "labels" || metadata.Target.ModelName != "label" {
		t.Fatal("M2M binding")
	}
	metadata.SourceModel.Fields[0].Name = "mutated"
	metadata.TargetModel.Fields[0].Name = "mutated"
	metadata.SourceModel.ManyToMany[0].Through.Model.ModelName = "mutated"
	fresh := labels.Metadata()
	if fresh.SourceModel.Fields[0].Name == "mutated" || fresh.TargetModel.Fields[0].Name == "mutated" || fresh.SourceModel.ManyToMany[0].Through.Model.ModelName == "mutated" {
		t.Fatal("metadata accessor aliases declaration")
	}
	cross, err := relationproject.Bind()
	if err != nil {
		t.Fatal(err)
	}
	authorChoices, err := formmodel.NewRelationChoices(cross, ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, "author", authors.AuthorObjects, formmodel.ModelChoiceConfig[authors.Author]{Label: func(authors.Author) (string, error) { called++; return "", nil }})
	if err != nil || authorChoices.Metadata().Target.AppLabel != "authors" || called != 0 {
		t.Fatal("cross-app binding or startup I/O", err, called)
	}
}

func TestModelChoicesBindTargetWithoutInventingSourceRelation(t *testing.T) {
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	policy := labelChoicePolicy()
	policy.Label = func(row models.Label) (string, error) { called++; return row.Name, nil }
	identity := ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "label"}
	choices, err := formmodel.NewModelChoices(binding, identity, models.LabelObjects, policy)
	if err != nil || !choices.Initialized() || called != 0 {
		t.Fatal("target-only construction evaluated a callback or failed", err)
	}
	check := func(metadata formmodel.ModelChoiceMetadata) {
		t.Helper()
		if metadata.Kind != formmodel.ModelChoiceTarget || metadata.Target != identity || !metadata.TargetModel.Equal((models.LabelDescriptor{}).Metadata()) ||
			metadata.Source != (ir.ModelIdentity{}) || !metadata.SourceModel.Equal(ir.Model{}) || metadata.Field != "" || metadata.Multiple {
			t.Fatal("target-only declaration gained relation metadata")
		}
	}
	check(choices.Metadata())
	metadata := choices.Metadata()
	metadata.Kind, metadata.Field, metadata.TargetModel.Fields[0].Name = formmodel.ModelChoiceRelation, "forged", "forged"
	check(choices.Metadata())
	backend := &choiceReadBackend{rows: []models.Label{{ID: 7, Name: "Selected", CategoryID: 1}, {ID: 9, Name: "Later", CategoryID: 1}}}
	scoped := choices.Filter(models.LabelFields.CategoryID.Exact(1))
	page, err := scoped.Search(t.Context(), backend, formmodel.ChoiceRequest{Limit: 1})
	if err != nil || !page.More() || !slices.Equal(choiceKeys(page.Items()), []int64{7}) {
		t.Fatal("target-only page lost common bounds", err)
	}
	check(page.Metadata())
	backend.rows = backend.rows[1:]
	selection, err := scoped.Resolve(t.Context(), backend, []int64{9})
	if err != nil || !slices.Equal(choiceKeys(selection.Items()), []int64{9}) {
		t.Fatal("target-only selection outside page failed", err)
	}
	check(selection.Metadata())
	before := len(backend.plans)
	empty, err := scoped.Resolve(t.Context(), backend, nil)
	if err != nil || !empty.Initialized() || len(backend.plans) != before {
		t.Fatal("target-only empty selection enumerated rows", err)
	}
	check(empty.Metadata())
	if (formmodel.ModelChoices[models.Label]{}).Metadata().Kind != "" || (formmodel.ChoicePage{}).Metadata().Kind != "" || (formmodel.ChoiceSelection{}).Metadata().Kind != "" {
		t.Fatal("zero result acquired a binding kind")
	}
}

func TestModelChoicesRejectInvalidTargetDeclarations(t *testing.T) {
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"zero_project", "zero_identity", "unknown_target", "wrong_target", "metadata_mismatch", "missing_key_reader", "missing_label", "invalid_order"} {
		t.Run(mode, func(t *testing.T) {
			canonical, identity := binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "label"}
			target, config := models.LabelObjects, labelChoicePolicy()
			switch mode {
			case "zero_project":
				canonical = orm.ProjectBinding{}
			case "zero_identity":
				identity = ir.ModelIdentity{}
			case "unknown_target":
				identity.ModelName = "unknown"
			case "wrong_target":
				identity.ModelName = "category"
			case "metadata_mismatch":
				metadata := (models.LabelDescriptor{}).Metadata()
				metadata.Fields[1].MaxLength++
				target = orm.NewManager[models.Label](choiceLabelDescriptor{metadata: metadata})
			case "missing_key_reader":
				target = orm.NewManager[models.Label](choiceLabelWithoutKey{})
			case "missing_label":
				config.Label = nil
			case "invalid_order":
				config.Order = []orm.Ordering[models.Label]{{}}
			}
			value, err := formmodel.NewModelChoices(canonical, identity, target, config)
			if err == nil || value.Initialized() || value.Metadata().Kind != "" {
				t.Fatal("invalid target declaration acquired an initialized binding")
			}
		})
	}
}

func TestRelationChoicesRejectWrongSourceTargetAndInvalidOrdering(t *testing.T) {
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	source := ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"}
	for _, mode := range []string{"zero_project", "unknown_source", "unknown_field", "scalar", "reverse", "wrong_target", "metadata_mismatch", "missing_key_reader", "missing_label", "invalid_order", "foreign_order"} {
		t.Run(mode, func(t *testing.T) {
			canonical, owner, field := binding, source, "labels"
			target, config := models.LabelObjects, labelChoicePolicy()
			switch mode {
			case "zero_project":
				canonical = orm.ProjectBinding{}
			case "unknown_source":
				owner.AppLabel = "unknown"
			case "unknown_field":
				field = "unknown"
			case "scalar":
				field = "subject"
			case "reverse":
				owner.ModelName, field = "category", "labels"
			case "wrong_target":
				field = "category"
			case "metadata_mismatch":
				metadata := (models.LabelDescriptor{}).Metadata()
				metadata.Fields[1].MaxLength++
				target = orm.NewManager[models.Label](choiceLabelDescriptor{metadata: metadata})
			case "missing_key_reader":
				target = orm.NewManager[models.Label](choiceLabelWithoutKey{})
			case "missing_label":
				config.Label = nil
			case "invalid_order":
				config.Order = []orm.Ordering[models.Label]{{}}
			case "foreign_order":
				config.Order = []orm.Ordering[models.Label]{orm.NewStringField[models.Label](ir.Field{Name: "other", Column: "other", Kind: ir.FieldChar}).Asc()}
			}
			value, err := formmodel.NewRelationChoices(canonical, owner, field, target, config)
			if err == nil || value.Initialized() {
				t.Fatal("invalid relation declaration accepted")
			}
		})
	}
}

func TestRelationChoiceSearchBoundsSQLAndSeparatesSelectedKeys(t *testing.T) {
	config := labelChoicePolicy()
	choices := newLabelChoices(t, config)
	config.Order[0] = models.LabelFields.ID.Desc()
	predicates := []orm.Predicate[models.Label]{models.LabelFields.CategoryID.Exact(7)}
	scoped := choices.Filter(predicates...)
	predicates[0] = models.LabelFields.CategoryID.Exact(999)
	backend := &choiceReadBackend{rows: []models.Label{{ID: 1, Name: "A", CategoryID: 7}, {ID: 2, Name: "A", CategoryID: 7}, {ID: 3, Name: "B", CategoryID: 7}}}
	page, err := scoped.Search(t.Context(), backend, formmodel.ChoiceRequest{Term: "A%_", Offset: 4, Limit: 2})
	if err != nil || !page.Initialized() || !page.More() || page.Offset() != 4 || page.Limit() != 2 || !slices.Equal(choiceKeys(page.Items()), []int64{1, 2}) {
		t.Fatal("bounded page", err)
	}
	plan := backend.plans[0]
	if limit, _ := plan.Limit(); limit != 3 {
		t.Fatal("search did not request one sentinel")
	}
	if offset, _ := plan.Offset(); offset != 4 || len(plan.Orderings()) != 2 || plan.Orderings()[0].Field().Name() != "name" || plan.Orderings()[1].Field().Name() != "id" {
		t.Fatal("ordering or offset lost")
	}
	if plan.Orderings()[0].Direction() != query.Ascending || len(plan.Conditions()) != 2 || !plan.Conditions()[0].Value().Equal(query.Integer(7)) {
		t.Fatal("configuration or scope slice was borrowed")
	}
	statement, _, err := sqlite.Compile(plan)
	if err != nil || !strings.Contains(statement, "LIMIT") || !strings.Contains(statement, "OFFSET") || !strings.Contains(statement, "LIKE") {
		t.Fatal("bounded typed AST did not compile", err)
	}
	items := page.Items()
	items[0].Label = "mutated"
	metadata := page.Metadata()
	metadata.TargetModel.Fields[0].Name = "mutated"
	if page.Items()[0].Label != "A" || page.Metadata().TargetModel.Fields[0].Name == "mutated" {
		t.Fatal("page accessors retained caller aliases")
	}
	backend.rows = []models.Label{{ID: 99, Name: "Outside page", CategoryID: 7}}
	selection, err := scoped.Resolve(t.Context(), backend, []int64{99, 999})
	if err != nil || !slices.Equal(selection.Keys(), []int64{99, 999}) || !slices.Equal(choiceKeys(selection.Items()), []int64{99}) {
		t.Fatal("selection outside page or missing key", err)
	}
	last := backend.plans[len(backend.plans)-1]
	if len(last.Conditions()) != 2 || last.Conditions()[1].Lookup() != query.LookupIn {
		t.Fatal("Resolve reused search term or omitted PK membership")
	}
	if limit, _ := last.Limit(); limit != 3 {
		t.Fatal("selected-key resolution did not bound complete read")
	}
	if offset, _ := last.Offset(); offset != 0 {
		t.Fatal("Resolve inherited search offset")
	}
	backend.rows = nil
	if _, err := choices.Search(t.Context(), backend, formmodel.ChoiceRequest{Limit: 2}); err != nil || len(backend.plans[len(backend.plans)-1].Conditions()) != 0 {
		t.Fatal("Filter mutated reusable declaration", err)
	}
}

func TestRelationChoiceResolutionPreservesInt64AndRawFormSemantics(t *testing.T) {
	choices := newLabelChoices(t, labelChoicePolicy())
	keys := []int64{1 << 60, 0, -4, 0, math.MaxInt64}
	backend := &choiceReadBackend{rows: []models.Label{{ID: -4, Name: "Negative"}, {ID: 0, Name: "Zero"}, {ID: 1 << 60, Name: "Large"}}}
	selected, err := choices.Resolve(t.Context(), backend, keys)
	if err != nil || !selected.Initialized() || !slices.Equal(selected.Keys(), []int64{1 << 60, 0, -4, math.MaxInt64}) || !slices.Equal(choiceKeys(selected.Items()), []int64{-4, 0, 1 << 60}) {
		t.Fatal("int64, duplicate or absent selection changed", err)
	}
	keys[0] = 1
	ownedKeys, ownedItems := selected.Keys(), selected.Items()
	ownedKeys[0], ownedItems[0].Label = 1, "mutated"
	metadata := selected.Metadata()
	metadata.SourceModel.Fields[0].Name = "mutated"
	if selected.Keys()[0] != 1<<60 || selected.Items()[0].Label != "Negative" || selected.Metadata().SourceModel.Fields[0].Name == "mutated" {
		t.Fatal("selection accessor aliases")
	}
	field, err := forms.ModelMultipleChoiceField("labels")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	spec, err = spec.WithModelChoices("labels", selected.Items()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]string{{"0", "0"}, {"1152921504606846976", "-4"}, {"9223372036854775807"}, {"+0"}} {
		bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"labels": raw}), nil)
		want := raw[0] != "9223372036854775807" && raw[0] != "+0"
		if err != nil || bound.Valid() != want {
			t.Fatal("resolution changed pure form semantics", raw, err)
		}
	}
	before := len(backend.plans)
	empty, err := choices.Resolve(t.Context(), backend, nil)
	if err != nil || !empty.Initialized() || empty.Metadata().Field != "labels" || len(empty.Keys()) != 0 || len(empty.Items()) != 0 || len(backend.plans) != before {
		t.Fatal("empty resolution lost binding or queried target catalog", err)
	}
}

func TestRelationChoicesUseFreshQueriesAndReadOnlyKeyDescriptors(t *testing.T) {
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	var descriptor orm.ModelDescriptor[models.Label] = choiceLabelDescriptor{}
	if _, writer := descriptor.(orm.WriteDescriptor[models.Label]); writer {
		t.Fatal("read-only descriptor fixture has write capability")
	}
	choices, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"}, "labels", orm.NewManager(descriptor), labelChoicePolicy())
	if err != nil {
		t.Fatal(err)
	}
	backend := &choiceReadBackend{rows: []models.Label{{ID: 7, Name: "Before"}}}
	first, err := choices.Resolve(t.Context(), backend, []int64{7})
	if err != nil {
		t.Fatal(err)
	}
	backend.rows[0].Name = "After"
	second, err := choices.Resolve(t.Context(), backend, []int64{7})
	if err != nil || len(backend.plans) != 2 || first.Items()[0].Label != "Before" || second.Items()[0].Label != "After" {
		t.Fatal("declaration retained evaluated rows", err)
	}
}

func TestRelationChoicesDefaultOrderingAndOptionalSearchPolicy(t *testing.T) {
	for _, mode := range []string{"default_key_order", "declared_key_order", "related_key_order", "search_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			config := labelChoicePolicy()
			config.Search, config.Order = nil, nil
			request := formmodel.ChoiceRequest{Limit: 1}
			if mode == "declared_key_order" {
				config.Order = []orm.Ordering[models.Label]{models.LabelFields.ID.Desc()}
			}
			if mode == "related_key_order" {
				relations, err := project.BindRelations()
				if err != nil {
					t.Fatal(err)
				}
				config.Order = []orm.Ordering[models.Label]{relations.ModelsLabel.Category.ID.Asc()}
			}
			if mode == "search_unavailable" {
				request.Term = "unavailable"
			}
			choices := newLabelChoices(t, config)
			backend := &choiceReadBackend{}
			page, err := choices.Search(t.Context(), backend, request)
			if mode == "search_unavailable" {
				if err == nil || page.Initialized() || len(backend.plans) != 0 {
					t.Fatal("nonempty search silently ignored")
				}
				return
			}
			if err != nil || !page.Initialized() || page.More() || len(backend.plans) != 1 {
				t.Fatal("empty page", err)
			}
			order := backend.plans[0].Orderings()
			if mode == "related_key_order" {
				if len(order) != 2 {
					t.Fatal("related target id was mistaken for a unique root key")
				}
				if _, related := order[0].Expression().RelationPath(); !related {
					t.Fatal("related ordering lost its source path")
				}
				if _, related := order[1].Expression().RelationPath(); related || order[1].Expression().Kind() != query.ResultField {
					t.Fatal("tie-breaker did not use the root model key")
				}
				order = order[1:]
			}
			want := query.Ascending
			if mode == "declared_key_order" {
				want = query.Descending
			}
			if len(order) != 1 || order[0].Field().Name() != "id" || order[0].Direction() != want {
				t.Fatal("key order changed or was duplicated")
			}
		})
	}
}

func TestRelationChoicesRejectInvalidInputsBeforeReads(t *testing.T) {
	choices := newLabelChoices(t, labelChoicePolicy())
	backend := &choiceReadBackend{}
	for _, request := range []formmodel.ChoiceRequest{
		{}, {Limit: -1}, {Limit: 101}, {Limit: 1, Offset: -1}, {Limit: 1, Offset: 1_000_001}, {Limit: math.MaxInt, Offset: math.MaxInt},
		{Limit: 1, Term: "\xff"}, {Limit: 1, Term: "a\x00b"}, {Limit: 1, Term: strings.Repeat("한", 86)},
	} {
		page, err := choices.Search(t.Context(), backend, request)
		if err == nil || page.Initialized() || len(backend.plans) != 0 {
			t.Fatal("invalid page read target rows")
		}
	}
	keys := make([]int64, formmodel.MaximumModelChoiceKeys+1)
	for index := range keys {
		keys[index] = int64(index)
	}
	if selected, err := choices.Resolve(t.Context(), backend, keys); err == nil || selected.Initialized() || len(backend.plans) != 0 {
		t.Fatal("oversized key set was queried")
	}
	for _, mode := range []string{"nil_context", "nil_backend", "typed_nil", "canceled", "expired", "uninitialized", "invalid_filter", "foreign_filter"} {
		t.Run(mode, func(t *testing.T) {
			ctx, current, queryer := t.Context(), choices, db.Queryer(backend)
			switch mode {
			case "nil_context":
				ctx = nil
			case "nil_backend":
				queryer = nil
			case "typed_nil":
				queryer = (*choiceReadBackend)(nil)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "expired":
				queryer = &choiceReadBackend{sessionErr: errors.New("expired")}
			case "uninitialized":
				current = formmodel.ModelChoices[models.Label]{}
			case "invalid_filter":
				current = choices.Filter(orm.Predicate[models.Label]{})
			case "foreign_filter":
				current = choices.Filter(orm.NewAutoField[models.Label](ir.Field{Name: "other", Column: "other", Kind: ir.FieldAuto, PrimaryKey: true}).Exact(7))
			}
			if value, err := current.Resolve(ctx, queryer, nil); err == nil || value.Initialized() || len(backend.plans) != 0 {
				t.Fatal("invalid no-op read accepted", err)
			}
		})
	}
}

func TestRelationChoicesDiscardPartialResultsAndPreserveFailures(t *testing.T) {
	failure := errors.New("read failure")
	for _, mode := range []string{"duplicate", "unrequested", "overflow", "label_error", "label_large", "label_invalid", "label_null", "query_error", "iteration_error", "close_error", "cancel_callback", "cancel_with_error", "expired_callback", "search_error", "search_cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backend := &choiceReadBackend{rows: []models.Label{{ID: 7, Name: "Visible"}, {ID: 8, Name: "Later"}}}
			config := labelChoicePolicy()
			search := false
			switch mode {
			case "duplicate":
				backend.rows[1].ID = 7
			case "unrequested":
				backend.rows[1].ID = 99
			case "overflow":
				search = true
				backend.rows = append(backend.rows, models.Label{ID: 9, Name: "Overflow"})
			case "label_error":
				config.Label = func(row models.Label) (string, error) {
					if row.ID == 8 {
						return "", failure
					}
					return row.Name, nil
				}
			case "label_large":
				config.Label = func(models.Label) (string, error) { return strings.Repeat("x", 4097), nil }
			case "label_invalid":
				config.Label = func(models.Label) (string, error) { return "\xff", nil }
			case "label_null":
				config.Label = func(models.Label) (string, error) { return "a\x00b", nil }
			case "query_error":
				backend.queryErr = failure
			case "iteration_error":
				backend.iterationErr = failure
			case "close_error":
				backend.closeErr = failure
			case "cancel_callback":
				config.Label = func(models.Label) (string, error) { cancel(); return "", nil }
			case "cancel_with_error":
				config.Label = func(models.Label) (string, error) { cancel(); return "", failure }
			case "expired_callback":
				config.Label = func(models.Label) (string, error) { backend.sessionErr = failure; return "", nil }
			case "search_error":
				search = true
				config.Search = func(string) ([]orm.Predicate[models.Label], error) { return nil, failure }
			case "search_cancel":
				search = true
				config.Search = func(string) ([]orm.Predicate[models.Label], error) { cancel(); return nil, failure }
			}
			choices := newLabelChoices(t, config)
			var err error
			if search {
				var result formmodel.ChoicePage
				result, err = choices.Search(ctx, backend, formmodel.ChoiceRequest{Limit: 1})
				if result.Initialized() || len(result.Items()) != 0 {
					t.Fatal("partial search page published")
				}
			} else {
				var result formmodel.ChoiceSelection
				result, err = choices.Resolve(ctx, backend, []int64{7, 8})
				if result.Initialized() || len(result.Items()) != 0 {
					t.Fatal("partial selection published")
				}
			}
			if err == nil {
				t.Fatal("callback/backend contract violation accepted")
			}
			if strings.Contains(mode, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if strings.HasSuffix(mode, "error") || mode == "cancel_with_error" || mode == "expired_callback" || mode == "search_cancel" {
				if !errors.Is(err, failure) {
					t.Fatal("execution error converted to validation failure", err)
				}
			}
			if len(backend.plans) != 0 && mode != "query_error" && backend.closed != 1 {
				t.Fatal("cursor not closed synchronously", backend.closed)
			}
		})
	}
}

func TestRelationChoiceResultsAreRecognizableAndRedacted(t *testing.T) {
	choices := newLabelChoices(t, labelChoicePolicy())
	backend := &choiceReadBackend{rows: []models.Label{{ID: 7, Name: "private label"}}}
	page, err := choices.Search(t.Context(), backend, formmodel.ChoiceRequest{Limit: 1})
	if err != nil || page.More() {
		t.Fatal(err)
	}
	selection, err := choices.Resolve(t.Context(), backend, []int64{7})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{choices, choices.Metadata(), page, selection} {
		if text := fmt.Sprintf("%+v %#v", value, value); strings.Contains(text, "private") || !strings.Contains(text, "redacted") {
			t.Fatal("choice state leaked through formatting")
		}
	}
	if (formmodel.ChoicePage{}).Initialized() || (formmodel.ChoiceSelection{}).Initialized() || (formmodel.ModelChoices[models.Label]{}).Initialized() {
		t.Fatal("zero state looks initialized")
	}
}

type choiceNativeDatabase interface {
	db.Queryer
	db.Mutator
	db.Atomic
	Close() error
}

// The controlled cursor tests above own malformed backend contracts. These
// native fixtures exercise actual scoped SQL, page boundaries and borrowed
// session lifetimes on the same generated model and public declaration.
func TestRelationChoicesNative(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "choices.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		})
		exec := func(statement string, values ...any) error {
			_, err := backend.ExecContext(t.Context(), statement, values...)
			return err
		}
		verifyNativeRelationChoices(t, backend, exec, func(name string) string { return pgx.Identifier{name}.Sanitize() })
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
		if dsn == "" {
			if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
				t.Fatal("required PostgreSQL absent")
			}
			t.Skip("GODJ_TEST_POSTGRES_URL is not configured; relation choice PostgreSQL fixture was not run")
		}
		connection, err := pgx.Connect(t.Context(), dsn)
		if err != nil {
			t.Fatal("connect relation choice PostgreSQL")
		}
		name := fmt.Sprintf("godj_relation_choices_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{name}.Sanitize()
		if _, err := connection.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
			_ = connection.Close(context.Background())
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				t.Error(err)
			}
			if err := connection.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		backend, err := postgres.Open(t.Context(), postgres.Config{URL: dsn, Schema: name})
		if err != nil {
			t.Fatal("open relation choice PostgreSQL")
		}
		t.Cleanup(func() {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		})
		exec := func(statement string, values ...any) error {
			_, err := connection.Exec(t.Context(), statement, values...)
			return err
		}
		verifyNativeRelationChoices(t, backend, exec, func(table string) string { return pgx.Identifier{name, table}.Sanitize() })
	})
}

func verifyNativeRelationChoices(t *testing.T, backend choiceNativeDatabase, exec func(string, ...any) error, table func(string) string) {
	t.Helper()
	categoryTable := table((models.CategoryDescriptor{}).Metadata().DBTable)
	labelTable := table((models.LabelDescriptor{}).Metadata().DBTable)
	for _, statement := range []string{
		"CREATE TABLE " + categoryTable + " (id BIGINT PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE TABLE " + labelTable + " (id BIGINT PRIMARY KEY, name TEXT NOT NULL, category_id BIGINT NOT NULL REFERENCES " + categoryTable + "(id), UNIQUE(category_id,name))",
	} {
		if err := exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture supplies an existing database with explicit signed keys.
	// PostgreSQL's ORM generated-key assignment/sequence reconciliation is a
	// separate unsupported write capability, not a prerequisite for reading
	// these valid target keys through the bounded choice API.
	for _, key := range []int64{1, 2} {
		if err := exec("INSERT INTO "+categoryTable+" (id, name) VALUES ($1, $2)", key, fmt.Sprint("Category ", key)); err != nil {
			t.Fatal(err)
		}
	}
	for index, key := range []int64{-4, 0, 7, 1 << 60, math.MaxInt64} {
		// Name order deliberately opposes PK order so a category/name index
		// cannot accidentally make an omitted root tie-breaker appear stable.
		name, categoryID := fmt.Sprintf("Visible %d", 3-index), int64(1)
		if key == math.MaxInt64 {
			name, categoryID = "Private", 2
		}
		if err := exec("INSERT INTO "+labelTable+" (id, name, category_id) VALUES ($1, $2, $3)", key, name, categoryID); err != nil {
			t.Fatal(err)
		}
	}
	config := labelChoicePolicy()
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	// Every visible row shares Category.ID, whose terminal name and column
	// match Label.ID. Only the Label root key is a unique tie-breaker.
	config.Order = []orm.Ordering[models.Label]{relations.ModelsLabel.Category.ID.Asc()}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	choices, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"}, "labels", orm.NewManager[models.Label](choiceLabelDescriptor{}), config)
	if err != nil {
		t.Fatal(err)
	}
	scoped := choices.Filter(models.LabelFields.CategoryID.Exact(1))
	t.Run("target_only_binding", func(t *testing.T) {
		commands, err := formmodel.NewModelChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "label"}, models.LabelObjects, config)
		if err != nil {
			t.Fatal(err)
		}
		commands = commands.Filter(models.LabelFields.CategoryID.Exact(1))
		reader := &choiceTraceQueryer{Queryer: backend}
		page, err := commands.Search(t.Context(), reader, formmodel.ChoiceRequest{Term: "Visible", Limit: 1})
		if err != nil || page.Metadata().Kind != formmodel.ModelChoiceTarget || !page.More() || !slices.Equal(choiceKeys(page.Items()), []int64{-4}) || reader.calls != 1 {
			t.Fatal("native command target search differs", err)
		}
		selected, err := commands.Resolve(t.Context(), reader, []int64{1 << 60, math.MaxInt64})
		if err != nil || selected.Metadata().Kind != formmodel.ModelChoiceTarget || selected.Metadata().Field != "" || !slices.Equal(choiceKeys(selected.Items()), []int64{1 << 60}) || reader.calls != 2 {
			t.Fatal("native target selection escaped scope or fabricated relation", err)
		}
	})
	t.Run("stable_pages_and_search", func(t *testing.T) {
		for _, mode := range []string{"root_foreign_key", "related_primary_key"} {
			t.Run(mode, func(t *testing.T) {
				policy := config
				if mode == "root_foreign_key" {
					policy.Order = []orm.Ordering[models.Label]{models.LabelFields.CategoryID.Asc()}
				}
				ordered, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"}, "labels", orm.NewManager[models.Label](choiceLabelDescriptor{}), policy)
				if err != nil {
					t.Fatal(err)
				}
				ordered = ordered.Filter(models.LabelFields.CategoryID.Exact(1))
				reader := &choiceTraceQueryer{Queryer: backend}
				for _, expected := range []struct {
					offset int
					keys   []int64
					more   bool
				}{
					{0, []int64{-4, 0}, true}, {2, []int64{7, 1 << 60}, false}, {4, []int64{}, false},
				} {
					before := reader.calls
					page, err := ordered.Search(t.Context(), reader, formmodel.ChoiceRequest{Term: "Visible", Offset: expected.offset, Limit: 2})
					if err != nil || !slices.Equal(choiceKeys(page.Items()), expected.keys) || page.More() != expected.more || reader.calls != before+1 {
						t.Fatal("actual bounded page/order/search differs", expected.offset, err)
					}
					if limit, present := reader.last.Limit(); !present || limit != 3 {
						t.Fatal("native search lacked SQL row limit")
					}
					order := reader.last.Orderings()
					if len(order) != 2 || order[1].Field().Name() != "id" {
						t.Fatal("native page lacks root PK tie-breaker")
					}
					if _, related := order[1].Expression().RelationPath(); related {
						t.Fatal("native tie-breaker is a target key")
					}
				}
				page, err := ordered.Search(t.Context(), reader, formmodel.ChoiceRequest{Term: "Visible 0", Limit: 1})
				if err != nil || !slices.Equal(choiceKeys(page.Items()), []int64{1 << 60}) || page.More() {
					t.Fatal("search predicate not applied", err)
				}
			})
		}
	})
	t.Run("selected_outside_page_and_scope", func(t *testing.T) {
		reader := &choiceTraceQueryer{Queryer: backend}
		selected, err := scoped.Resolve(t.Context(), reader, []int64{1 << 60, 0, -4, 0, math.MaxInt64, 999})
		if err != nil || !slices.Equal(choiceKeys(selected.Items()), []int64{-4, 0, 1 << 60}) || reader.calls != 1 {
			t.Fatal("actual key/scope resolution differs", err)
		}
		before := reader.calls
		empty, err := scoped.Resolve(t.Context(), reader, nil)
		if err != nil || !empty.Initialized() || reader.calls != before {
			t.Fatal("empty selection queried whole table", err)
		}
		other, err := choices.Filter(models.LabelFields.CategoryID.Exact(2)).Resolve(t.Context(), reader, []int64{math.MaxInt64})
		if err != nil || !slices.Equal(choiceKeys(other.Items()), []int64{math.MaxInt64}) {
			t.Fatal("scope derivation or signed int64 precision", err)
		}
	})
	t.Run("fresh_stored_scope", func(t *testing.T) {
		before, err := scoped.Resolve(t.Context(), backend, []int64{7})
		if err != nil || len(before.Items()) != 1 {
			t.Fatal(err)
		}
		current := models.NewLabelWithID(7)
		current.Name, current.CategoryID = "Moved", 2
		if err := models.LabelObjects.Save(t.Context(), backend, &current, orm.ForceUpdate[models.Label]()); err != nil {
			t.Fatal(err)
		}
		after, err := scoped.Resolve(t.Context(), backend, []int64{7})
		if err != nil || len(after.Items()) != 0 || before.Items()[0].Label != "Visible 1" {
			t.Fatal("stale authorization snapshot reused", err)
		}
		newScope, err := choices.Filter(models.LabelFields.CategoryID.Exact(2)).Resolve(t.Context(), backend, []int64{7})
		if err != nil || len(newScope.Items()) != 1 || newScope.Items()[0].Label != "Moved" {
			t.Fatal("fresh target label not read", err)
		}
	})
	t.Run("borrowed_lifetime", func(t *testing.T) {
		var expired db.Queryer
		err := backend.Atomic(t.Context(), func(session db.Session) error {
			expired = session
			selected, err := scoped.Resolve(t.Context(), session, []int64{0})
			if err != nil {
				return err
			}
			if !slices.Equal(choiceKeys(selected.Items()), []int64{0}) {
				return errors.New("live borrowed lookup lost zero key")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if value, err := scoped.Resolve(t.Context(), expired, nil); err == nil || value.Initialized() {
			t.Fatal("empty resolution escaped borrowed lifetime", err)
		}
		if value, err := scoped.Search(t.Context(), expired, formmodel.ChoiceRequest{Limit: 1}); err == nil || value.Initialized() {
			t.Fatal("search escaped borrowed lifetime", err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		reader := &choiceTraceQueryer{Queryer: backend}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if value, err := scoped.Search(ctx, reader, formmodel.ChoiceRequest{Limit: 1}); !errors.Is(err, context.Canceled) || value.Initialized() {
			t.Fatal("canceled search", err)
		}
		if value, err := scoped.Resolve(ctx, reader, []int64{0}); !errors.Is(err, context.Canceled) || value.Initialized() || reader.calls != 0 {
			t.Fatal("canceled resolution read rows", err)
		}
		streamCtx, stop := context.WithCancel(t.Context())
		defer stop()
		cancelPolicy := config
		cancelPolicy.Label = func(row models.Label) (string, error) { stop(); return row.Name, nil }
		canceling, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"}, "labels", orm.NewManager[models.Label](choiceLabelDescriptor{}), cancelPolicy)
		if err != nil {
			t.Fatal(err)
		}
		if value, err := canceling.Search(streamCtx, backend, formmodel.ChoiceRequest{Limit: 2}); !errors.Is(err, context.Canceled) || value.Initialized() {
			t.Fatal("native cursor cancellation published a partial page", err)
		}
		if value, err := scoped.Search(t.Context(), backend, formmodel.ChoiceRequest{Limit: 1}); err != nil || !value.Initialized() {
			t.Fatal("canceled cursor was not released for a fresh query", err)
		}
	})
}

type choiceTraceQueryer struct {
	db.Queryer
	calls int
	last  query.Plan
}

func (reader *choiceTraceQueryer) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	reader.calls++
	reader.last = plan
	return reader.Queryer.Query(ctx, plan)
}

func choiceKeys(items []forms.Choice) []int64 {
	keys := make([]int64, len(items))
	for index, item := range items {
		keys[index], _ = item.Value.AsInteger()
	}
	return keys
}

type choiceLabelDescriptor struct{ metadata ir.Model }

func (descriptor choiceLabelDescriptor) Metadata() ir.Model {
	if descriptor.metadata.Name != "" {
		return descriptor.metadata
	}
	return (models.LabelDescriptor{}).Metadata()
}
func (choiceLabelDescriptor) Scan(row db.Row) (models.Label, error) {
	return (models.LabelDescriptor{}).Scan(row)
}
func (choiceLabelDescriptor) CloneModel(row models.Label) models.Label {
	return (models.LabelDescriptor{}).CloneModel(row)
}
func (choiceLabelDescriptor) PrimaryKey(row models.Label) (query.Value, bool) {
	return (models.LabelDescriptor{}).PrimaryKey(row)
}

type choiceLabelWithoutKey struct{}

func (choiceLabelWithoutKey) Metadata() ir.Model { return (models.LabelDescriptor{}).Metadata() }
func (choiceLabelWithoutKey) Scan(row db.Row) (models.Label, error) {
	return (models.LabelDescriptor{}).Scan(row)
}
func (choiceLabelWithoutKey) CloneModel(row models.Label) models.Label { return row }

type choiceReadBackend struct {
	rows                                         []models.Label
	plans                                        []query.Plan
	queryErr, iterationErr, closeErr, sessionErr error
	closed                                       int
}

func (backend *choiceReadBackend) ValidateSession(context.Context) error { return backend.sessionErr }
func (backend *choiceReadBackend) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	backend.plans = append(backend.plans, plan)
	if backend.queryErr != nil {
		return nil, backend.queryErr
	}
	if _, _, err := sqlite.Compile(plan); err != nil {
		return nil, err
	}
	return &choiceReadRows{backend: backend, rows: slices.Clone(backend.rows), index: -1}, nil
}

type choiceReadRows struct {
	backend *choiceReadBackend
	rows    []models.Label
	index   int
}

func (rows *choiceReadRows) Next() bool   { rows.index++; return rows.index < len(rows.rows) }
func (rows *choiceReadRows) Err() error   { return rows.backend.iterationErr }
func (rows *choiceReadRows) Close() error { rows.backend.closed++; return rows.backend.closeErr }
func (rows *choiceReadRows) Scan(destinations ...any) error {
	row := rows.rows[rows.index]
	values := []any{row.ID, row.Name, row.CategoryID}
	if len(destinations) != len(values) {
		return errors.New("unexpected Label projection")
	}
	for index, destination := range destinations {
		reflect.ValueOf(destination).Elem().Set(reflect.ValueOf(values[index]))
	}
	return nil
}
