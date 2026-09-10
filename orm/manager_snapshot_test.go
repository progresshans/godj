package orm_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestManagerSnapshotsMetadataAcrossReadWriteAndConcurrentUsing(t *testing.T) {
	t.Parallel()
	descriptor := &mutableManagerDescriptor{metadata: (models.ArticleDescriptor{}).Metadata()}
	manager := orm.NewManager[models.Article](descriptor)
	if descriptor.calls.Load() != 1 {
		t.Fatal("NewManager did not prepare metadata exactly once")
	}
	wantPlan := manager.Using(nil).Plan()
	descriptor.metadata.DBTable = "changed"
	descriptor.metadata.Fields[0].Name = "changed"
	descriptor.metadata.Fields[2].Default.Boolean = true
	fields := wantPlan.SourceFields()
	fields[0] = query.NewFieldRef("changed", "changed", query.FieldInteger, false)

	backend := &spyBackend{}
	const workers = 16
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			qs := manager.Using(backend)
			if !qs.Plan().Equal(wantPlan) {
				t.Error("caller metadata/getter mutation changed prepared plan")
			}
			for range 2 {
				if _, err := qs.All(context.Background()); err != nil {
					t.Errorf("All: %v", err)
				}
			}
		})
	}
	group.Wait()
	if got := backend.calls.Load(); got != workers {
		t.Fatalf("independent Using caches made %d queries, want %d", got, workers)
	}
	for range 2 {
		writer := &writeSpy{lastInsertID: 1}
		_, err := manager.Create(context.Background(), writer, models.NewArticleCreate("Prepared"))
		if err != nil || writer.insertPlan.Table() != wantPlan.Table() {
			t.Fatalf("Create did not retain private read/write metadata: plan=%v err=%v", writer.insertPlan, err)
		}
	}
	if got := descriptor.calls.Load(); got != 1 {
		t.Fatalf("Manager re-read Metadata %d times", got)
	}
	if got := orm.NewManager[models.Article](descriptor).Using(nil).Plan().Table(); got != "changed" {
		t.Fatalf("new Manager did not take the new snapshot: %q", got)
	}
}

type mutableManagerDescriptor struct {
	models.ArticleDescriptor
	metadata ir.Model
	calls    atomic.Int64
}

func (descriptor *mutableManagerDescriptor) Metadata() ir.Model {
	descriptor.calls.Add(1)
	return descriptor.metadata
}

func (descriptor *mutableManagerDescriptor) WriteFieldValue(value models.Article, field ir.Field) (query.Value, bool) {
	if field.Default != nil {
		if field.Default.Boolean {
			return query.Value{}, false
		}
		// Deliberately mutate callback input. A later operation must receive an
		// independent copy of the Manager's original metadata.
		field.Default.Boolean = true
	}
	return descriptor.ArticleDescriptor.WriteFieldValue(value, field)
}
