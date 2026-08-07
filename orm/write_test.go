package orm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestGeneratedCreatePreservesDefaultNullEmptyAndBuilderImmutability(t *testing.T) {
	t.Parallel()

	base := models.NewArticleCreate("Created")
	withEmpty := base.WithPublished(false).WithSummary("")
	firstBackend := &writeSpy{lastInsertID: 41, updateRows: 1, deleteRows: 1}
	created, err := models.ArticleObjects.Create(context.Background(), firstBackend, withEmpty)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID != 41 || created.Title != "Created" || created.Published || created.Summary == nil || *created.Summary != "" {
		t.Fatalf("created Article = %#v", created)
	}
	if _, present := (models.ArticleDescriptor{}).PrimaryKey(created); !present {
		t.Fatal("created Article did not record explicit primary-key presence")
	}
	assertAssignmentValue(t, firstBackend.insertPlan.Assignments(), "published", query.ValueBoolean, false)
	assertAssignmentValue(t, firstBackend.insertPlan.Assignments(), "summary", query.ValueString, "")

	secondBackend := &writeSpy{lastInsertID: 42, updateRows: 1, deleteRows: 1}
	fromBase, err := models.ArticleObjects.Create(context.Background(), secondBackend, base)
	if err != nil {
		t.Fatalf("Create() from base error = %v", err)
	}
	if fromBase.Summary != nil || fromBase.Published {
		t.Fatalf("value-receiver builder mutated its source: %#v", fromBase)
	}
	assertAssignmentValue(t, secondBackend.insertPlan.Assignments(), "summary", query.ValueNull, nil)
}

func TestChangeStateDistinguishesUnsetZeroAndNull(t *testing.T) {
	t.Parallel()

	var omitted orm.Change[bool]
	if _, set := omitted.Get(); set {
		t.Fatal("zero Change was set")
	}
	explicitFalse := orm.Set(false)
	if value, set := explicitFalse.Get(); !set || value {
		t.Fatalf("explicit false = (%v, %v), want (false, true)", value, set)
	}
	var nullableOmitted orm.NullableChange[string]
	if _, state := nullableOmitted.Get(); state != orm.NullableChangeUnset {
		t.Fatalf("nullable omitted state = %v", state)
	}
	if value, state := orm.SetNullable("").Get(); state != orm.NullableChangeValue || value != "" {
		t.Fatalf("nullable empty = (%q, %v)", value, state)
	}
	if _, state := orm.SetNull[string]().Get(); state != orm.NullableChangeNull {
		t.Fatalf("nullable null state = %v", state)
	}
}

func TestGeneratedWriteValidationPerformsNoBackendCall(t *testing.T) {
	t.Parallel()

	loaded := models.Article{Title: "Loaded"}
	(models.ArticleDescriptor{}).SetPrimaryKey(&loaded, 7)
	tests := []struct {
		name  string
		run   func(*writeSpy) error
		code  string
		field string
	}{
		{
			name: "required create field",
			run: func(backend *writeSpy) error {
				_, err := models.ArticleObjects.Create(context.Background(), backend, models.ArticleCreate{})
				return err
			},
			code:  query.CodeRequiredField,
			field: "title",
		},
		{
			name: "empty patch",
			run: func(backend *writeSpy) error {
				_, err := models.ArticleObjects.Update(context.Background(), backend, loaded, models.ArticlePatch{})
				return err
			},
			code: query.CodeEmptyPatch,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &writeSpy{lastInsertID: 1, updateRows: 1, deleteRows: 1}
			err := test.run(backend)
			var queryError *query.Error
			if !errors.As(err, &queryError) || queryError.Code != test.code || queryError.Field != test.field {
				t.Fatalf("error = %v, want code=%s field=%s", err, test.code, test.field)
			}
			if backend.calls != 0 {
				t.Fatalf("validation invoked backend %d time(s)", backend.calls)
			}
		})
	}
}

func TestManagerRejectsInjectedMutationMetadataAndValuesBeforeBackend(t *testing.T) {
	t.Parallel()

	metadata := (models.ArticleDescriptor{}).Metadata()
	tests := []struct {
		name       string
		assignment query.Assignment
		code       string
	}{
		{
			name:       "wrong scalar",
			assignment: orm.NewAssignment(metadata.Fields[1], query.Boolean(true)),
			code:       query.CodeInvalidValue,
		},
		{
			name:       "null for non-null field",
			assignment: orm.NewAssignment(metadata.Fields[1], query.Null()),
			code:       query.CodeInvalidValue,
		},
		{
			name: "foreign field",
			assignment: query.NewAssignment(
				query.NewFieldRef("title", "foreign_title", query.FieldString, false),
				query.String("value"),
			),
			code: query.CodeUnknownField,
		},
		{
			name:       "result model mismatch",
			assignment: orm.NewAssignment(metadata.Fields[1], query.String("stored")),
			code:       query.CodeInvalidValue,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &writeSpy{lastInsertID: 1}
			input := injectedArticleCreate{mutation: orm.NewCreateMutation(
				models.Article{Title: "value"},
				metadata.DBTable,
				[]query.Assignment{test.assignment},
			)}
			_, err := models.ArticleObjects.Create(context.Background(), backend, input)
			var queryError *query.Error
			if !errors.As(err, &queryError) || queryError.Code != test.code {
				t.Fatalf("Create() error = %v, want code %s", err, test.code)
			}
			if backend.calls != 0 {
				t.Fatalf("invalid injected mutation invoked backend %d time(s)", backend.calls)
			}
		})
	}
}

func TestManagerRejectsInjectedPatchPrimaryKeyMismatchBeforeBackend(t *testing.T) {
	t.Parallel()

	descriptor := models.ArticleDescriptor{}
	metadata := descriptor.Metadata()
	current := models.Article{Title: "Before"}
	descriptor.SetPrimaryKey(&current, 7)
	forged := current
	forged.Title = "After"
	descriptor.SetPrimaryKey(&forged, 8)
	input := injectedArticlePatch{mutation: orm.NewPatchMutation(
		forged,
		metadata.DBTable,
		[]query.Assignment{orm.NewAssignment(metadata.Fields[1], query.String("After"))},
	)}
	backend := &writeSpy{updateRows: 1}
	_, err := models.ArticleObjects.Update(context.Background(), backend, current, input)
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("Update() error = %v, want invalid_plan", err)
	}
	if backend.calls != 0 {
		t.Fatalf("invalid injected patch invoked backend %d time(s)", backend.calls)
	}
}

func TestManagerRejectsInjectedPatchOmittedFieldMutationBeforeBackend(t *testing.T) {
	t.Parallel()

	descriptor := models.ArticleDescriptor{}
	metadata := descriptor.Metadata()
	persistedSummary := "Persisted"
	current := models.Article{Title: "Before", Summary: &persistedSummary}
	descriptor.SetPrimaryKey(&current, 7)
	forged := current
	forged.Title = "After"
	forged.Published = true
	memoryOnlySummary := "Memory only"
	forged.Summary = &memoryOnlySummary
	input := injectedArticlePatch{mutation: orm.NewPatchMutation(
		forged,
		metadata.DBTable,
		[]query.Assignment{orm.NewAssignment(metadata.Fields[1], query.String("After"))},
	)}
	backend := &writeSpy{updateRows: 1}
	_, err := models.ArticleObjects.Update(context.Background(), backend, current, input)
	if !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}) {
		t.Fatalf("Update() error = %v, want invalid_value", err)
	}
	if backend.calls != 0 {
		t.Fatalf("invalid injected patch invoked backend %d time(s)", backend.calls)
	}
}

func TestUpdateAndDeleteRequireExactlyOneAffectedRow(t *testing.T) {
	t.Parallel()

	descriptor := models.ArticleDescriptor{}
	article := models.Article{Title: "Before"}
	descriptor.SetPrimaryKey(&article, 9)
	backend := &writeSpy{updateRows: 0, deleteRows: 0}
	_, err := models.ArticleObjects.Update(
		context.Background(),
		backend,
		article,
		models.ArticlePatch{}.WithTitle("After"),
	)
	if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows}) {
		t.Fatalf("Update() error = %v, want unexpected rows", err)
	}
	if article.Title != "Before" {
		t.Fatalf("failed update mutated caller value: %#v", article)
	}

	rows, err := models.ArticleObjects.Delete(context.Background(), backend, &article)
	if rows != 0 || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows}) {
		t.Fatalf("Delete() = (%d, %v), want unexpected rows", rows, err)
	}
	if key, present := descriptor.PrimaryKey(article); !present || key.Kind() != query.ValueInteger || article.ID != 9 {
		t.Fatalf("zero-row delete cleared key state: article=%#v present=%v", article, present)
	}

	backend.deleteRows = 1
	rows, err = models.ArticleObjects.Delete(context.Background(), backend, &article)
	if err != nil || rows != 1 {
		t.Fatalf("successful Delete() = (%d, %v)", rows, err)
	}
	if _, present := descriptor.PrimaryKey(article); present || article.ID != 0 {
		t.Fatalf("successful delete retained key state: article=%#v present=%v", article, present)
	}
}

func TestWriteRejectsTypedNilBackend(t *testing.T) {
	t.Parallel()

	var backend *writeSpy
	_, err := models.ArticleObjects.Create(context.Background(), backend, models.NewArticleCreate("Created"))
	if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("Create() error = %v, want backend invalid_plan", err)
	}
}

type injectedArticleCreate struct {
	mutation orm.Mutation[models.Article]
}

type injectedArticlePatch struct {
	mutation orm.Mutation[models.Article]
}

func (input injectedArticlePatch) BuildPatch(models.Article) orm.Mutation[models.Article] {
	return input.mutation
}

func (input injectedArticleCreate) BuildCreate() orm.Mutation[models.Article] {
	return input.mutation
}

type writeSpy struct {
	calls        int
	lastInsertID int64
	updateRows   int64
	deleteRows   int64
	insertPlan   query.InsertPlan
	updatePlan   query.UpdatePlan
	deletePlan   query.DeletePlan
}

func (backend *writeSpy) Insert(_ context.Context, plan query.InsertPlan) (int64, error) {
	backend.calls++
	backend.insertPlan = plan
	return backend.lastInsertID, nil
}

func (backend *writeSpy) Update(_ context.Context, plan query.UpdatePlan) (int64, error) {
	backend.calls++
	backend.updatePlan = plan
	return backend.updateRows, nil
}

func (backend *writeSpy) Delete(_ context.Context, plan query.DeletePlan) (int64, error) {
	backend.calls++
	backend.deletePlan = plan
	return backend.deleteRows, nil
}

func assertAssignmentValue(t *testing.T, assignments []query.Assignment, field string, kind query.ValueKind, want any) {
	t.Helper()
	for _, assignment := range assignments {
		if assignment.Field().Name() != field {
			continue
		}
		if assignment.Value().Kind() != kind {
			t.Fatalf("field %s kind = %q, want %q", field, assignment.Value().Kind(), kind)
		}
		got, err := assignment.Value().DatabaseValue()
		if err != nil {
			t.Fatalf("field %s DatabaseValue() error = %v", field, err)
		}
		if got != want {
			t.Fatalf("field %s value = %#v, want %#v", field, got, want)
		}
		return
	}
	t.Fatalf("assignment for field %s not found", field)
}
