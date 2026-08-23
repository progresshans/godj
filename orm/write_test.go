package orm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
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
	assertInsertReturningKey(t, firstBackend.insertPlan, query.NewFieldRef("id", "id", query.FieldInteger, false))

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

func TestManagerIsolatesPatchInputFromNullableCallerAliases(t *testing.T) {
	t.Parallel()

	descriptor := models.ArticleDescriptor{}
	persistedSummary := "Persisted"
	current := models.Article{Title: "Before", Summary: &persistedSummary}
	descriptor.SetPrimaryKey(&current, 7)
	backend := &writeSpy{updateRows: 1}

	_, err := models.ArticleObjects.Update(context.Background(), backend, current, aliasingArticlePatch{})
	if !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}) {
		t.Fatalf("Update() error = %v, want invalid_value", err)
	}
	if persistedSummary != "Persisted" || current.Summary == nil || *current.Summary != "Persisted" {
		t.Fatalf("PatchInput mutated caller through nullable alias: %#v", current)
	}
	if backend.calls != 0 {
		t.Fatalf("aliasing patch invoked backend %d time(s)", backend.calls)
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

func TestManagerCreateAcceptsForeignKeyIntegerValues(t *testing.T) {
	t.Parallel()

	manager := orm.NewManager[relationWriteModel](relationWriteDescriptor{})
	metadata := (relationWriteDescriptor{}).Metadata()
	reviewerID := int64(0)
	for _, test := range []struct {
		name          string
		reviewerID    *int64
		reviewerValue query.Value
	}{
		{name: "nullable null", reviewerValue: query.Null()},
		{name: "nullable explicit zero", reviewerID: &reviewerID, reviewerValue: query.Integer(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &writeSpy{lastInsertID: 41}
			value := relationWriteModel{AuthorID: 0, ReviewerID: test.reviewerID}
			created, err := manager.Create(context.Background(), backend, injectedRelationWriteCreate{
				mutation: orm.NewCreateMutation(
					value,
					metadata.DBTable,
					[]query.Assignment{
						orm.NewAssignment(metadata.Fields[1], query.Integer(0)),
						orm.NewAssignment(metadata.Fields[2], test.reviewerValue),
					},
				),
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if backend.calls != 1 || created.ID != 41 || !created.primaryKeyPresent {
				t.Fatalf("Create() = (%#v, calls=%d), want generated key and one insert", created, backend.calls)
			}
			assertAssignmentValue(t, backend.insertPlan.Assignments(), "author", query.ValueInteger, int64(0))
			assertInsertReturningKey(t, backend.insertPlan, query.NewFieldRef("id", "id", query.FieldInteger, false))
			if test.reviewerID == nil {
				assertAssignmentValue(t, backend.insertPlan.Assignments(), "reviewer", query.ValueNull, nil)
			} else {
				assertAssignmentValue(t, backend.insertPlan.Assignments(), "reviewer", query.ValueInteger, int64(0))
			}
		})
	}
}

func TestManagerCreateRejectsInvalidForeignKeyValuesBeforeBackendIO(t *testing.T) {
	t.Parallel()

	manager := orm.NewManager[relationWriteModel](relationWriteDescriptor{})
	metadata := (relationWriteDescriptor{}).Metadata()
	for _, test := range []struct {
		name       string
		fieldIndex int
		value      query.Value
		field      string
	}{
		{name: "required foreign key null", fieldIndex: 1, value: query.Null(), field: "author"},
		{name: "required foreign key string", fieldIndex: 1, value: query.String("1"), field: "author"},
		{name: "nullable foreign key boolean", fieldIndex: 2, value: query.Boolean(true), field: "reviewer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assignments := []query.Assignment{
				orm.NewAssignment(metadata.Fields[1], query.Integer(1)),
				orm.NewAssignment(metadata.Fields[2], query.Null()),
			}
			assignments[test.fieldIndex-1] = orm.NewAssignment(metadata.Fields[test.fieldIndex], test.value)
			backend := &writeSpy{lastInsertID: 41}
			_, err := manager.Create(context.Background(), backend, injectedRelationWriteCreate{
				mutation: orm.NewCreateMutation(relationWriteModel{AuthorID: 1}, metadata.DBTable, assignments),
			})
			if !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Field: test.field}) {
				t.Fatalf("Create() error = %v, want invalid_value field=%q", err, test.field)
			}
			if backend.calls != 0 {
				t.Fatalf("invalid foreign key invoked backend %d time(s)", backend.calls)
			}
		})
	}
}

type injectedArticleCreate struct {
	mutation orm.Mutation[models.Article]
}

type relationWriteModel struct {
	ID                int64
	AuthorID          int64
	ReviewerID        *int64
	primaryKeyPresent bool
}

type relationWriteDescriptor struct{}

func (relationWriteDescriptor) Metadata() ir.Model {
	return ir.Model{
		Name:    "post",
		GoName:  "Post",
		DBTable: "blog_post",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey},
			{Name: "reviewer", GoName: "ReviewerID", Column: "reviewer_id", Kind: ir.FieldForeignKey, Nullable: true},
		},
	}
}

func (relationWriteDescriptor) Scan(db.Row) (relationWriteModel, error) {
	return relationWriteModel{}, nil
}

func (relationWriteDescriptor) CloneModel(value relationWriteModel) relationWriteModel {
	return relationWriteDescriptor{}.CloneWriteModel(value)
}

func (relationWriteDescriptor) PrimaryKey(value relationWriteModel) (query.Value, bool) {
	return query.Integer(value.ID), value.primaryKeyPresent
}

func (relationWriteDescriptor) SetPrimaryKey(value *relationWriteModel, key int64) {
	value.ID = key
	value.primaryKeyPresent = true
}

func (relationWriteDescriptor) ClearPrimaryKey(value *relationWriteModel) {
	value.ID = 0
	value.primaryKeyPresent = false
}

func (relationWriteDescriptor) CloneWriteModel(value relationWriteModel) relationWriteModel {
	if value.ReviewerID != nil {
		reviewerID := *value.ReviewerID
		value.ReviewerID = &reviewerID
	}
	return value
}

func (relationWriteDescriptor) WriteFieldValue(value relationWriteModel, field ir.Field) (query.Value, bool) {
	switch field.Name {
	case "id":
		return query.Integer(value.ID), true
	case "author":
		return query.Integer(value.AuthorID), true
	case "reviewer":
		if value.ReviewerID == nil {
			return query.Null(), true
		}
		return query.Integer(*value.ReviewerID), true
	default:
		return query.Value{}, false
	}
}

type relationWriteInvalidForeignKeyDescriptor struct {
	relationWriteDescriptor
}

func (relationWriteInvalidForeignKeyDescriptor) WriteFieldValue(value relationWriteModel, field ir.Field) (query.Value, bool) {
	if field.Name == "author" {
		return query.String("invalid"), true
	}
	return relationWriteDescriptor{}.WriteFieldValue(value, field)
}

type injectedRelationWriteCreate struct {
	mutation orm.Mutation[relationWriteModel]
}

func (input injectedRelationWriteCreate) BuildCreate() orm.Mutation[relationWriteModel] {
	return input.mutation
}

type injectedArticlePatch struct {
	mutation orm.Mutation[models.Article]
}

type aliasingArticlePatch struct{}

func (aliasingArticlePatch) BuildPatch(current models.Article) orm.Mutation[models.Article] {
	*current.Summary = "Forged through alias"
	current.Title = "After"
	metadata := (models.ArticleDescriptor{}).Metadata()
	return orm.NewPatchMutation(
		current,
		metadata.DBTable,
		[]query.Assignment{orm.NewAssignment(metadata.Fields[1], query.String("After"))},
	)
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

func assertInsertReturningKey(t *testing.T, plan query.InsertPlan, want query.FieldRef) {
	t.Helper()
	got, present := plan.ReturningKey()
	if !present || !got.Equal(want) {
		t.Fatalf("insert returning key = (%#v, %v), want (%#v, true)", got, present, want)
	}
}
