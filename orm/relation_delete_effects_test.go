package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type effectsDeleteSession struct {
	*relationDeleteTestSession
	expired bool
}

func (session *effectsDeleteSession) ValidateSession(ctx context.Context) error {
	if session.expired {
		return errors.New("expired effect scope")
	}
	return ctx.Err()
}

func TestRelationDeleteEffectsCollectCompleteDetachedRowsBeforeWrites(t *testing.T) {
	deleter := cascadeTestDeleter(t)
	rows := []*relationDeleteTestRows{
		{values: [][2]any{{int64(10), int64(1)}, {int64(10), int64(1)}}},
		{values: [][2]any{{int64(30), int64(1)}, {int64(10), int64(1)}}},
		{}, {values: [][2]any{{int64(20), int64(10)}}}, {},
	}
	session := &effectsDeleteSession{relationDeleteTestSession: &relationDeleteTestSession{
		queryRows: []db.Rows{rows[0], rows[1], rows[2], rows[3], rows[4]}, setNullCounts: []int64{2}, deleteCount: 1,
	}}
	target := relationDeleteTestAuthorValue(1)
	want := []RelationDeleteEffect{
		{Model: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Key: 1, Deleted: true},
		{Model: ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, Key: 10, Deleted: true, NullFields: []string{"reviewer"}},
		{Model: ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, Key: 30, NullFields: []string{"reviewer"}},
		{Model: ir.ModelIdentity{AppLabel: "children", ModelName: "leaf"}, Key: 20, Deleted: true},
	}
	calls := 0
	var retained RelationDeleteEffects
	count, err := deleter.DeleteWithEffectsInSession(t.Context(), session, target, func(_ context.Context, effects RelationDeleteEffects) error {
		calls++
		if len(session.mutationOrder) != 0 {
			t.Fatal("observer ran after relation writes")
		}
		for _, row := range rows {
			if row.closeCalls != 1 || row.errCalls != 1 {
				t.Fatal("observer preceded complete row cleanup")
			}
		}
		if !reflect.DeepEqual(effects.Rows(), want) {
			t.Fatal("incomplete or duplicate effects", effects.Rows())
		}
		copy := effects.Rows()
		copy[0].Key, copy[0].Deleted, copy[0].Model.AppLabel = 999, false, "foreign"
		copy[1].NullFields[0] = "other"
		if !reflect.DeepEqual(effects.Rows(), want) {
			t.Fatal("effect observation aliases its caller")
		}
		if fmt.Sprintf("%+v", effects) != "orm.RelationDeleteEffects{redacted}" {
			t.Fatal("default diagnostics expose row keys")
		}
		retained = effects
		return nil
	})
	if err != nil || count != 3 || calls != 1 || target.ID != 1 || !target.primaryKeyPresent {
		t.Fatal("borrowed effect deletion", count, err, calls, target)
	}
	if !reflect.DeepEqual(retained.Rows(), want) {
		t.Fatal("execution mutated a retained observation")
	}
	_, keys, collected := session.setNullPlans[0].CollectedRows()
	if !collected || !reflect.DeepEqual(keys, []int64{10, 30}) {
		t.Fatal("physical mutation lost exact source keys", keys)
	}
}

func TestRelationDeleteEffectsMergeMultipleNullFieldsOnOneSurvivor(t *testing.T) {
	authors, blog := relationDeleteTestSchemas()
	editor := blog.Models[0].Fields[3].Clone()
	editor.Name, editor.GoName, editor.Column = "editor", "EditorID", "editor_id"
	editor.Relation.Reverse = ir.ReverseRelation{Disabled: true}
	blog.Models[0].Fields = append(blog.Models[0].Fields, editor)
	binding, err := BindProject(authors, blog)
	if err != nil {
		t.Fatal(err)
	}
	deleter, err := BindRelationDeleter(binding, ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, relationDeleteTestAuthorDescriptor{}, relationDeleteTestActualFingerprint(t, binding))
	if err != nil {
		t.Fatal(err)
	}
	session := &effectsDeleteSession{relationDeleteTestSession: &relationDeleteTestSession{
		queryRows: []db.Rows{&relationDeleteTestRows{}, relationDeleteNullRows(2, 1), relationDeleteNullRows(2, 1)}, setNullCounts: []int64{1, 1}, deleteCount: 1,
	}}
	count, err := deleter.DeleteWithEffectsInSession(t.Context(), session, relationDeleteTestAuthorValue(2), func(_ context.Context, effects RelationDeleteEffects) error {
		rows := effects.Rows()
		if len(rows) != 2 || rows[1].Deleted || rows[1].Key != 10 || !reflect.DeepEqual(rows[1].NullFields, []string{"editor", "reviewer"}) {
			t.Fatal("one surviving row was split or lost a field", rows)
		}
		return nil
	})
	if err != nil || count != 1 || len(session.setNullPlans) != 2 {
		t.Fatal("merged observation changed physical FK updates", count, err)
	}
}

func TestRelationDeleteEffectsFailuresPreventRelationMutation(t *testing.T) {
	for _, scenario := range []string{"callback_error", "canceled", "expired", "protected", "null_read", "invalid_null_row", "nil_callback"} {
		t.Run(scenario, func(t *testing.T) {
			deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("effect failure")
			protect, nulls := &relationDeleteTestRows{}, relationDeleteNullRows(2, 2)
			if scenario == "protected" {
				protect.values = [][2]any{{int64(10), int64(2)}}
			}
			if scenario == "null_read" {
				nulls.closeErr = failure
			}
			if scenario == "invalid_null_row" {
				nulls.values[0][1] = int64(99)
			}
			session := &effectsDeleteSession{relationDeleteTestSession: &relationDeleteTestSession{queryRows: []db.Rows{protect, nulls}, setNullCounts: []int64{2}, deleteCount: 1}}
			calls := 0
			before := func(context.Context, RelationDeleteEffects) error {
				calls++
				switch scenario {
				case "callback_error":
					return failure
				case "canceled":
					cancel()
				case "expired":
					session.expired = true
				}
				return nil
			}
			if scenario == "nil_callback" {
				before = nil
			}
			count, err := deleter.DeleteWithEffectsInSession(ctx, session, relationDeleteTestAuthorValue(2), before)
			if count != 0 || err == nil || len(session.mutationOrder) != 0 {
				t.Fatal("failed collection or observer mutated relations", count, err, session.mutationOrder)
			}
			wantCalls := 0
			if scenario == "callback_error" || scenario == "canceled" || scenario == "expired" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("partial effects were observed", calls)
			}
			if (scenario == "callback_error" || scenario == "null_read") && !errors.Is(err, failure) {
				t.Fatal("observer/read failure lost", err)
			}
			if scenario == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("observer cancellation lost", err)
			}
			if scenario == "nil_callback" && len(session.queryPlans) != 0 {
				t.Fatal("nil observer performed I/O")
			}
		})
	}
}

func TestRelationDeleteEffectsKeepSignedKeysAndCompleteSetNullBatches(t *testing.T) {
	for _, key := range []int64{0, -7} {
		t.Run(fmt.Sprintf("key_%d", key), func(t *testing.T) {
			deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
			nulls := &relationDeleteTestRows{}
			for index := 0; index <= query.MaximumRelationSetNullRows; index++ {
				nulls.values = append(nulls.values, [2]any{int64(index - 1), key})
			}
			session := &effectsDeleteSession{relationDeleteTestSession: &relationDeleteTestSession{queryRows: []db.Rows{&relationDeleteTestRows{}, nulls}, setNullCounts: []int64{query.MaximumRelationSetNullRows, 1}, deleteCount: 1}}
			count, err := deleter.DeleteWithEffectsInSession(t.Context(), session, relationDeleteTestAuthorValue(key), func(_ context.Context, effects RelationDeleteEffects) error {
				rows := effects.Rows()
				if len(rows) != query.MaximumRelationSetNullRows+2 || !rows[0].Deleted || rows[0].Key != key || rows[1].Key != -1 || rows[2].Key != 0 {
					t.Fatal("signed or excess rows were truncated", rows)
				}
				return nil
			})
			if err != nil || count != 1 || len(session.setNullPlans) != 2 {
				t.Fatal("complete bounded mutation failed", count, err, len(session.setNullPlans))
			}
			_, first, _ := session.setNullPlans[0].CollectedRows()
			_, last, _ := session.setNullPlans[1].CollectedRows()
			if len(first) != query.MaximumRelationSetNullRows || len(last) != 1 || first[0] != -1 || last[0] != query.MaximumRelationSetNullRows-1 {
				t.Fatal("mutation batches lost source keys", first, last)
			}
		})
	}
}

func TestRelationDeleteEmptyNullCollectionDoesNotIssueAnUpdate(t *testing.T) {
	deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
	session := &effectsDeleteSession{relationDeleteTestSession: &relationDeleteTestSession{queryRows: []db.Rows{&relationDeleteTestRows{}, &relationDeleteTestRows{}}, deleteCount: 1}}
	count, err := deleter.DeleteWithEffectsInSession(t.Context(), session, relationDeleteTestAuthorValue(2), func(_ context.Context, effects RelationDeleteEffects) error {
		rows := effects.Rows()
		if len(rows) != 1 || !rows[0].Deleted || len(rows[0].NullFields) != 0 {
			t.Fatal("empty relation invented an effect", rows)
		}
		return nil
	})
	if err != nil || count != 1 || len(session.setNullPlans) != 0 || len(session.deletePlans) != 1 {
		t.Fatal("empty collected SET_NULL was not a no-op", count, err)
	}
}
