package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestProject5OwnsNullableSlotsAndRejectsInvalidFifthFieldBeforeIO(t *testing.T) {
	fields := newResultTestFields()
	fifth := NewNullableStringField[resultTestModel](resultFiveDescriptor{}.Metadata().Fields[4])
	manager := NewManager[resultTestModel](resultFiveDescriptor{})
	type row struct {
		ID            int64
		Title         string
		Published     bool
		First, Second *string
	}
	build := func(id int64, title string, first *string, published bool, second *string) row {
		return row{id, title, published, first, second}
	}
	backend := resultBackendForRows([][]any{{int64(1), "one", nil, true, nil}, {int64(2), "two", "value", false, "value"}, {int64(3), "three", "next", true, "next"}})
	projection := Project5(fields.ID, fields.Title, fields.Note, fields.Published, fifth, build)
	values, err := SelectInto(t.Context(), manager.Using(backend), projection)
	if err != nil || len(values) != 3 || values[0].First != nil || values[0].Second != nil || values[1].Second == nil || *values[1].Second != "value" || values[2].Second == nil || *values[2].Second != "next" {
		t.Fatal("five-field nullable decoding lost values", values, err)
	}
	if values[1].First == values[1].Second || values[1].Second == values[2].Second {
		t.Fatal("projection shared nullable storage between fields or rows")
	}
	plan := backend.plans[0]
	want := []query.FieldRef{fields.ID.reference, fields.Title.reference, fields.Note.reference, fields.Published.reference, fifth.reference}
	expressions, sources := plan.ResultShape().Expressions(), plan.SourceFields()
	if plan.ResultShape().Kind() != query.ResultProjection || len(expressions) != len(want) || len(sources) != len(want) {
		t.Fatal("five-field plan lost its projection or source shape")
	}
	for index, expected := range want {
		actual, ok := expressions[index].Field()
		if !ok || !actual.Equal(expected) || !sources[index].Equal(expected) {
			t.Fatal("five-field plan changed column order or identity", index)
		}
	}
	for _, invalid := range []Projection[resultTestModel, row]{
		Project5(fields.ID, fields.Title, fields.Note, fields.Published, fields.Note, build),
		Project5(fields.ID, fields.Title, fields.Note, fields.Published, NullableStringField[resultTestModel]{}, build),
		Project5(fields.ID, fields.Title, fields.Note, fields.Published, fifth, (func(int64, string, *string, bool, *string) row)(nil)),
	} {
		backend := resultBackendForRows(nil)
		_, err := SelectInto(t.Context(), manager.Using(backend), invalid)
		assertResultQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
		if len(backend.plans) != 0 {
			t.Fatal("invalid fifth field or builder reached the backend")
		}
	}
}

// Projection decoding uses the selected scalar slots rather than Scan. The
// descriptor supplies a distinct fifth nullable column for that plan.
type resultFiveDescriptor struct{ resultTestDescriptor }

func (resultFiveDescriptor) Metadata() ir.Model {
	model := resultTestMetadata()
	model.Fields = append(model.Fields, ir.Field{Name: "other", GoName: "Other", Column: "other", Kind: ir.FieldChar, Nullable: true})
	return model
}
