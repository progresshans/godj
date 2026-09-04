package orm_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestSaveNewInstanceInsertsAllWritableFieldsAndAssignsGeneratedKey(t *testing.T) {
	t.Parallel()

	summary := ""
	article := models.Article{Title: "New", Published: false, Summary: &summary}
	backend := &saveBackendSpy{insertIDs: []int64{41}}

	if err := models.ArticleObjects.Save(context.Background(), backend, &article); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !slices.Equal(backend.calls, []string{"insert"}) {
		t.Fatalf("backend calls = %v, want [insert]", backend.calls)
	}
	assertAssignmentNames(t, backend.insertPlans[0].Assignments(), "title", "published", "summary")
	assertSaveAssignment(t, backend.insertPlans[0].Assignments(), "title", query.String("New"))
	assertSaveAssignment(t, backend.insertPlans[0].Assignments(), "published", query.Boolean(false))
	assertSaveAssignment(t, backend.insertPlans[0].Assignments(), "summary", query.String(""))
	assertInsertReturningKey(t, backend.insertPlans[0], query.NewFieldRef("id", "id", query.FieldInteger, false))
	if article.ID != 41 {
		t.Fatalf("article.ID = %d, want 41", article.ID)
	}
	if key, present := (models.ArticleDescriptor{}).PrimaryKey(article); !present || !key.Equal(query.Integer(41)) {
		t.Fatalf("primary key = (%v, %v), want (41, true)", key, present)
	}
}

func TestSaveLoadedInstanceUpdatesEveryWritableFieldFromSnapshot(t *testing.T) {
	t.Parallel()

	summary := "Before"
	article := models.Article{Title: "Memory title", Published: true, Summary: &summary}
	(models.ArticleDescriptor{}).SetPrimaryKey(&article, 7)
	backend := &saveBackendSpy{updateRows: []int64{1}}
	backend.onUpdate = func() {
		article.Title = "Changed during backend call"
		*article.Summary = "Changed during backend call"
	}

	if err := models.ArticleObjects.Save(context.Background(), backend, &article); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !slices.Equal(backend.calls, []string{"update"}) {
		t.Fatalf("backend calls = %v, want [update]", backend.calls)
	}
	plan := backend.updatePlans[0]
	assertAssignmentNames(t, plan.Assignments(), "title", "published", "summary")
	assertSaveAssignment(t, plan.Assignments(), "title", query.String("Memory title"))
	assertSaveAssignment(t, plan.Assignments(), "published", query.Boolean(true))
	assertSaveAssignment(t, plan.Assignments(), "summary", query.String("Before"))
	if !plan.KeyValue().Equal(query.Integer(7)) || plan.KeyField().Name() != "id" {
		t.Fatalf("update key = (%v, %s), want (7, id)", plan.KeyValue(), plan.KeyField().Name())
	}
	if article.Title != "Changed during backend call" || article.Summary == nil || *article.Summary != "Changed during backend call" {
		t.Fatalf("Save unexpectedly restored caller mutation: %#v", article)
	}
}

func TestSaveTypedAndDynamicMasksConvergeOnMetadataOrderedAssignments(t *testing.T) {
	t.Parallel()

	newSummary := "Memory only"
	base := models.Article{Title: "Stored title", Published: true, Summary: &newSummary}
	(models.ArticleDescriptor{}).SetPrimaryKey(&base, 8)

	typedBackend := &saveBackendSpy{updateRows: []int64{1}}
	if err := models.ArticleObjects.Save(
		context.Background(),
		typedBackend,
		&base,
		orm.UpdateFields(models.ArticleFields.Published, models.ArticleFields.Title, models.ArticleFields.Title),
	); err != nil {
		t.Fatalf("typed Save() error = %v", err)
	}
	assertAssignmentNames(t, typedBackend.updatePlans[0].Assignments(), "title", "published")
	assertSaveAssignment(t, typedBackend.updatePlans[0].Assignments(), "title", query.String("Stored title"))
	assertSaveAssignment(t, typedBackend.updatePlans[0].Assignments(), "published", query.Boolean(true))

	dynamicBackend := &saveBackendSpy{updateRows: []int64{1}}
	if err := models.ArticleObjects.Save(
		context.Background(),
		dynamicBackend,
		&base,
		orm.UpdateFieldNames[models.Article]("published", "title", "title"),
	); err != nil {
		t.Fatalf("dynamic Save() error = %v", err)
	}
	if !dynamicBackend.updatePlans[0].Equal(typedBackend.updatePlans[0]) {
		t.Fatalf("typed and dynamic plans differ:\n typed=%#v\n dynamic=%#v", typedBackend.updatePlans[0], dynamicBackend.updatePlans[0])
	}
}

func TestSaveExplicitEmptyMaskIsZeroIONoop(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		option orm.SaveOption[models.Article]
	}{
		{name: "typed", option: orm.UpdateFields[models.Article]()},
		{name: "dynamic", option: orm.UpdateFieldNames[models.Article]()},
	} {
		t.Run(test.name, func(t *testing.T) {
			article := models.Article{Title: "Unsaved"}
			backend := &saveBackendSpy{}
			if err := models.ArticleObjects.Save(context.Background(), backend, &article, test.option); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			if len(backend.calls) != 0 || article.ID != 0 {
				t.Fatalf("empty mask caused side effects: calls=%v article=%#v", backend.calls, article)
			}
		})
	}
}

func TestSaveRejectsInvalidMasksBeforeBackendIO(t *testing.T) {
	t.Parallel()

	loaded := models.Article{Title: "Loaded"}
	(models.ArticleDescriptor{}).SetPrimaryKey(&loaded, 9)
	var nilTitle *orm.StringField[models.Article]
	zeroTitle := orm.StringField[models.Article]{}
	tests := []struct {
		name     string
		option   orm.SaveOption[models.Article]
		category string
		code     string
		field    string
	}{
		{
			name: "typed nil field", option: orm.UpdateFields[models.Article](nilTitle),
			category: query.CategoryField, code: query.CodeUnknownField,
		},
		{
			name: "typed zero field", option: orm.UpdateFields[models.Article](zeroTitle),
			category: query.CategoryField, code: query.CodeUnknownField,
		},
		{
			name: "dynamic primary key", option: orm.UpdateFieldNames[models.Article]("id"),
			category: query.CategoryField, code: query.CodePrimaryKeyUpdateField, field: "id",
		},
		{
			name: "dynamic unknown", option: orm.UpdateFieldNames[models.Article]("missing"),
			category: query.CategoryField, code: query.CodeUnknownField, field: "missing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &saveBackendSpy{}
			article := loaded
			err := models.ArticleObjects.Save(context.Background(), backend, &article, test.option)
			assertSaveError(t, err, test.category, test.code, test.field)
			if len(backend.calls) != 0 {
				t.Fatalf("invalid mask invoked backend: %v", backend.calls)
			}
		})
	}
}

func TestSaveRejectsInvalidConfigurationBeforeBackendIO(t *testing.T) {
	t.Parallel()

	t.Run("nil context", func(t *testing.T) {
		backend := &saveBackendSpy{}
		article := models.Article{Title: "New"}
		err := models.ArticleObjects.Save(nil, backend, &article)
		assertSaveError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
		assertNoSaveCalls(t, backend)
	})

	t.Run("cancelled context", func(t *testing.T) {
		backend := &saveBackendSpy{}
		article := models.Article{Title: "New"}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := models.ArticleObjects.Save(ctx, backend, &article)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Save() error = %v, want context.Canceled", err)
		}
		assertNoSaveCalls(t, backend)
	})

	t.Run("nil backend", func(t *testing.T) {
		var backend *saveBackendSpy
		article := models.Article{Title: "New"}
		err := models.ArticleObjects.Save(context.Background(), backend, &article)
		assertSaveError(t, err, query.CategoryBackend, query.CodeInvalidPlan, "")
	})

	t.Run("nil model pointer", func(t *testing.T) {
		backend := &saveBackendSpy{}
		err := models.ArticleObjects.Save(context.Background(), backend, nil)
		assertSaveError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
		assertNoSaveCalls(t, backend)
	})

	t.Run("zero option", func(t *testing.T) {
		backend := &saveBackendSpy{}
		article := models.Article{Title: "New"}
		var option orm.SaveOption[models.Article]
		err := models.ArticleObjects.Save(context.Background(), backend, &article, option)
		assertSaveError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
		assertNoSaveCalls(t, backend)
	})
}

func TestSavePrimaryKeyPresenceIsNotInferredFromNumericValue(t *testing.T) {
	t.Parallel()

	t.Run("nonzero value without presence", func(t *testing.T) {
		backend := &saveBackendSpy{insertIDs: []int64{55}}
		article := models.Article{ID: 77, Title: "Forged"}
		err := models.ArticleObjects.Save(context.Background(), backend, &article)
		assertSaveError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
		assertNoSaveCalls(t, backend)
		if article.ID != 77 {
			t.Fatalf("invalid Save mutated ID to %d", article.ID)
		}
	})

	t.Run("explicit zero value with presence", func(t *testing.T) {
		backend := &saveBackendSpy{updateRows: []int64{1}}
		article := models.NewArticleWithID(0)
		article.Title = "Explicit zero"
		err := models.ArticleObjects.Save(context.Background(), backend, &article, orm.ForceUpdate[models.Article]())
		if err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if !slices.Equal(backend.calls, []string{"update"}) || !backend.updatePlans[0].KeyValue().Equal(query.Integer(0)) {
			t.Fatalf("explicit-zero update = calls %v key %v", backend.calls, backend.updatePlans[0].KeyValue())
		}
	})

	t.Run("explicit zero default fallback preserves presence", func(t *testing.T) {
		backend := &saveBackendSpy{updateRows: []int64{0}, insertIDs: []int64{99}}
		article := models.NewArticleWithID(0)
		article.Title = "Explicit zero fallback"
		if err := models.ArticleObjects.Save(context.Background(), backend, &article); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if !slices.Equal(backend.calls, []string{"update", "insert"}) {
			t.Fatalf("backend calls = %v, want [update insert]", backend.calls)
		}
		assertSaveAssignment(t, backend.insertPlans[0].Assignments(), "id", query.Integer(0))
		assertInsertReturningKey(t, backend.insertPlans[0], query.NewFieldRef("id", "id", query.FieldInteger, false))
		key, present := (models.ArticleDescriptor{}).PrimaryKey(article)
		if article.ID != 0 || !present || !key.Equal(query.Integer(0)) {
			t.Fatalf("explicit-zero fallback key = ID %d value %v present %v", article.ID, key, present)
		}
	})
}

func TestSaveForceValidationAndMissingRows(t *testing.T) {
	t.Parallel()

	t.Run("mutually exclusive force flags", func(t *testing.T) {
		backend := &saveBackendSpy{}
		article := models.Article{Title: "New"}
		err := models.ArticleObjects.Save(
			context.Background(), backend, &article,
			orm.ForceInsert[models.Article](), orm.ForceUpdate[models.Article](),
		)
		assertSaveError(t, err, query.CategoryArgument, query.CodeMutuallyExclusiveForceFlags, "")
		assertNoSaveCalls(t, backend)
	})

	t.Run("force update without primary key", func(t *testing.T) {
		backend := &saveBackendSpy{}
		article := models.Article{Title: "New"}
		err := models.ArticleObjects.Save(context.Background(), backend, &article, orm.ForceUpdate[models.Article]())
		assertSaveError(t, err, query.CategoryModelState, query.CodeForceUpdateWithoutPrimaryKey, "id")
		assertNoSaveCalls(t, backend)
	})

	t.Run("update fields without primary key", func(t *testing.T) {
		backend := &saveBackendSpy{}
		article := models.Article{Title: "New"}
		err := models.ArticleObjects.Save(context.Background(), backend, &article, orm.UpdateFields(models.ArticleFields.Title))
		assertSaveError(t, err, query.CategoryModelState, query.CodeForceUpdateWithoutPrimaryKey, "id")
		assertNoSaveCalls(t, backend)
	})

	t.Run("force update missing row", func(t *testing.T) {
		backend := &saveBackendSpy{updateRows: []int64{0}}
		article := models.Article{Title: "Missing"}
		(models.ArticleDescriptor{}).SetPrimaryKey(&article, 13)
		err := models.ArticleObjects.Save(context.Background(), backend, &article, orm.ForceUpdate[models.Article]())
		assertSaveError(t, err, query.CategoryNotUpdated, query.CodeForceUpdateMissingRow, "")
		if !slices.Equal(backend.calls, []string{"update"}) {
			t.Fatalf("backend calls = %v, want [update]", backend.calls)
		}
	})

	t.Run("update fields missing row", func(t *testing.T) {
		backend := &saveBackendSpy{updateRows: []int64{0}}
		article := models.Article{Title: "Missing"}
		(models.ArticleDescriptor{}).SetPrimaryKey(&article, 14)
		err := models.ArticleObjects.Save(context.Background(), backend, &article, orm.UpdateFields(models.ArticleFields.Title))
		assertSaveError(t, err, query.CategoryNotUpdated, query.CodeUpdateFieldsMissingRow, "")
		if !slices.Equal(backend.calls, []string{"update"}) {
			t.Fatalf("backend calls = %v, want [update]", backend.calls)
		}
	})
}

func TestSaveExplicitKeyUpdateFallbackAndForceInsertPlans(t *testing.T) {
	t.Parallel()

	t.Run("default existing key", func(t *testing.T) {
		backend := &saveBackendSpy{updateRows: []int64{1}}
		article := models.Article{Title: "Existing"}
		(models.ArticleDescriptor{}).SetPrimaryKey(&article, 21)
		if err := models.ArticleObjects.Save(context.Background(), backend, &article); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if !slices.Equal(backend.calls, []string{"update"}) {
			t.Fatalf("backend calls = %v, want [update]", backend.calls)
		}
	})

	t.Run("default missing key falls back", func(t *testing.T) {
		backend := &saveBackendSpy{updateRows: []int64{0}, insertIDs: []int64{999}}
		article := models.Article{Title: "Fallback", Published: true}
		(models.ArticleDescriptor{}).SetPrimaryKey(&article, 22)
		if err := models.ArticleObjects.Save(context.Background(), backend, &article); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if !slices.Equal(backend.calls, []string{"update", "insert"}) {
			t.Fatalf("backend calls = %v, want [update insert]", backend.calls)
		}
		assertAssignmentNames(t, backend.updatePlans[0].Assignments(), "title", "published", "summary")
		assertAssignmentNames(t, backend.insertPlans[0].Assignments(), "id", "title", "published", "summary")
		assertSaveAssignment(t, backend.insertPlans[0].Assignments(), "id", query.Integer(22))
		assertInsertReturningKey(t, backend.insertPlans[0], query.NewFieldRef("id", "id", query.FieldInteger, false))
		if article.ID != 22 {
			t.Fatalf("explicit fallback replaced object ID with backend lastInsertID: %d", article.ID)
		}
	})

	t.Run("force insert explicit key", func(t *testing.T) {
		backend := &saveBackendSpy{insertIDs: []int64{999}}
		article := models.Article{Title: "Forced"}
		(models.ArticleDescriptor{}).SetPrimaryKey(&article, 23)
		if err := models.ArticleObjects.Save(context.Background(), backend, &article, orm.ForceInsert[models.Article]()); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if !slices.Equal(backend.calls, []string{"insert"}) {
			t.Fatalf("backend calls = %v, want [insert]", backend.calls)
		}
		assertAssignmentNames(t, backend.insertPlans[0].Assignments(), "id", "title", "published", "summary")
		assertSaveAssignment(t, backend.insertPlans[0].Assignments(), "id", query.Integer(23))
		assertInsertReturningKey(t, backend.insertPlans[0], query.NewFieldRef("id", "id", query.FieldInteger, false))
		if article.ID != 23 {
			t.Fatalf("explicit force insert replaced ID with backend lastInsertID: %d", article.ID)
		}
	})
}

func TestSavePropagatesBackendErrorsAndRejectsUnexpectedRowsWithoutFallback(t *testing.T) {
	t.Parallel()

	updateFailure := errors.New("update failed")
	article := models.Article{Title: "Loaded"}
	(models.ArticleDescriptor{}).SetPrimaryKey(&article, 30)
	updateBackend := &saveBackendSpy{updateErrors: []error{updateFailure}}
	if err := models.ArticleObjects.Save(context.Background(), updateBackend, &article); !errors.Is(err, updateFailure) {
		t.Fatalf("Save() error = %v, want raw update error", err)
	}
	if !slices.Equal(updateBackend.calls, []string{"update"}) {
		t.Fatalf("update error calls = %v, want [update]", updateBackend.calls)
	}

	insertFailure := errors.New("insert failed")
	newArticle := models.Article{Title: "New"}
	insertBackend := &saveBackendSpy{insertErrors: []error{insertFailure}, insertIDs: []int64{91}}
	if err := models.ArticleObjects.Save(context.Background(), insertBackend, &newArticle); !errors.Is(err, insertFailure) {
		t.Fatalf("Save() error = %v, want raw insert error", err)
	}
	if newArticle.ID != 0 {
		t.Fatalf("failed auto insert mutated ID to %d", newArticle.ID)
	}
	if _, present := (models.ArticleDescriptor{}).PrimaryKey(newArticle); present {
		t.Fatal("failed auto insert marked primary key present")
	}

	for _, rows := range []int64{-1, 2} {
		backend := &saveBackendSpy{updateRows: []int64{rows}}
		value := article
		err := models.ArticleObjects.Save(context.Background(), backend, &value)
		assertSaveError(t, err, query.CategoryBackend, query.CodeUnexpectedRows, "")
		if !slices.Equal(backend.calls, []string{"update"}) {
			t.Fatalf("rows=%d backend calls = %v, want [update]", rows, backend.calls)
		}
	}
}

func TestSaveOptionsCopyCallerSlices(t *testing.T) {
	t.Parallel()

	article := models.Article{Title: "Title", Published: true}
	(models.ArticleDescriptor{}).SetPrimaryKey(&article, 40)
	typed := []orm.WritableField[models.Article]{models.ArticleFields.Title}
	typedOption := orm.UpdateFields(typed...)
	typed[0] = models.ArticleFields.Published
	typedBackend := &saveBackendSpy{updateRows: []int64{1}}
	if err := models.ArticleObjects.Save(context.Background(), typedBackend, &article, typedOption); err != nil {
		t.Fatalf("typed Save() error = %v", err)
	}
	assertAssignmentNames(t, typedBackend.updatePlans[0].Assignments(), "title")

	names := []string{"title"}
	dynamicOption := orm.UpdateFieldNames[models.Article](names...)
	names[0] = "published"
	dynamicBackend := &saveBackendSpy{updateRows: []int64{1}}
	if err := models.ArticleObjects.Save(context.Background(), dynamicBackend, &article, dynamicOption); err != nil {
		t.Fatalf("dynamic Save() error = %v", err)
	}
	assertAssignmentNames(t, dynamicBackend.updatePlans[0].Assignments(), "title")
}

func TestSaveAcceptsForeignKeyIntegerValues(t *testing.T) {
	t.Parallel()

	manager := orm.NewManager[relationWriteModel](relationWriteDescriptor{})
	reviewerID := int64(0)
	for _, test := range []struct {
		name       string
		reviewerID *int64
	}{
		{name: "nullable null"},
		{name: "nullable explicit zero", reviewerID: &reviewerID},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &saveBackendSpy{insertIDs: []int64{41}}
			value := relationWriteModel{AuthorID: 0, ReviewerID: test.reviewerID}
			if err := manager.Save(context.Background(), backend, &value); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			if !slices.Equal(backend.calls, []string{"insert"}) || value.ID != 41 || !value.primaryKeyPresent {
				t.Fatalf("Save() = (%#v, calls=%v), want generated key and one insert", value, backend.calls)
			}
			assertAssignmentNames(t, backend.insertPlans[0].Assignments(), "author", "reviewer")
			assertSaveAssignment(t, backend.insertPlans[0].Assignments(), "author", query.Integer(0))
			assertInsertReturningKey(t, backend.insertPlans[0], query.NewFieldRef("id", "id", query.FieldInteger, false))
			if test.reviewerID == nil {
				assertSaveAssignment(t, backend.insertPlans[0].Assignments(), "reviewer", query.Null())
			} else {
				assertSaveAssignment(t, backend.insertPlans[0].Assignments(), "reviewer", query.Integer(0))
			}
		})
	}
}

func TestSaveRejectsInvalidForeignKeyDescriptorValueBeforeBackendIO(t *testing.T) {
	t.Parallel()

	manager := orm.NewManager[relationWriteModel](relationWriteInvalidForeignKeyDescriptor{})
	backend := &saveBackendSpy{insertIDs: []int64{41}}
	value := relationWriteModel{AuthorID: 1}
	err := manager.Save(context.Background(), backend, &value)
	assertSaveError(t, err, query.CategoryField, query.CodeInvalidValue, "author")
	assertNoSaveCalls(t, backend)
}

func TestQueryErrorPreservesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("driver constraint")
	err := &query.Error{
		Category: query.CategoryIntegrity,
		Code:     query.CodeUniquePrimaryKey,
		Cause:    cause,
	}
	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(%v, cause) = false", err)
	}
}

type saveBackendSpy struct {
	calls        []string
	insertPlans  []query.InsertPlan
	updatePlans  []query.UpdatePlan
	insertIDs    []int64
	insertErrors []error
	updateRows   []int64
	updateErrors []error
	onUpdate     func()
}

func (backend *saveBackendSpy) Insert(_ context.Context, plan query.InsertPlan) (int64, error) {
	index := len(backend.insertPlans)
	backend.calls = append(backend.calls, "insert")
	backend.insertPlans = append(backend.insertPlans, plan)
	var identifier int64
	if index < len(backend.insertIDs) {
		identifier = backend.insertIDs[index]
	}
	if index < len(backend.insertErrors) && backend.insertErrors[index] != nil {
		return identifier, backend.insertErrors[index]
	}
	return identifier, nil
}

func (backend *saveBackendSpy) Update(_ context.Context, plan query.UpdatePlan) (int64, error) {
	index := len(backend.updatePlans)
	backend.calls = append(backend.calls, "update")
	backend.updatePlans = append(backend.updatePlans, plan)
	if backend.onUpdate != nil {
		backend.onUpdate()
	}
	if index < len(backend.updateErrors) && backend.updateErrors[index] != nil {
		return 0, backend.updateErrors[index]
	}
	if index < len(backend.updateRows) {
		return backend.updateRows[index], nil
	}
	return 1, nil
}

func (backend *saveBackendSpy) Delete(context.Context, query.DeletePlan) (int64, error) {
	backend.calls = append(backend.calls, "delete")
	return 0, errors.New("unexpected delete")
}

func assertAssignmentNames(t *testing.T, assignments []query.Assignment, want ...string) {
	t.Helper()
	got := make([]string, len(assignments))
	for index, assignment := range assignments {
		got[index] = assignment.Field().Name()
	}
	if !slices.Equal(got, want) {
		t.Fatalf("assignment names = %v, want %v", got, want)
	}
}

func assertSaveAssignment(t *testing.T, assignments []query.Assignment, field string, want query.Value) {
	t.Helper()
	for _, assignment := range assignments {
		if assignment.Field().Name() == field {
			if !assignment.Value().Equal(want) {
				t.Fatalf("assignment %s = %v, want %v", field, assignment.Value(), want)
			}
			return
		}
	}
	t.Fatalf("assignment %s not found", field)
}

func assertSaveError(t *testing.T, err error, category, code, field string) {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) {
		t.Fatalf("error = %v, want *query.Error", err)
	}
	if queryError.Category != category || queryError.Code != code || queryError.Field != field {
		t.Fatalf(
			"error = category %q code %q field %q, want %q/%q field %q",
			queryError.Category, queryError.Code, queryError.Field, category, code, field,
		)
	}
}

func assertNoSaveCalls(t *testing.T, backend *saveBackendSpy) {
	t.Helper()
	if len(backend.calls) != 0 {
		t.Fatalf("backend calls = %v, want none", backend.calls)
	}
}
