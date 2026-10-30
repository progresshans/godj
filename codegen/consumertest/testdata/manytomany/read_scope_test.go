package consumer

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

// This observer preserves only the native reader and its lifetime validator.
// It cannot recover a writer or start another transaction.
type observedReadScope struct {
	db.Queryer
	db.SessionValidator
	queries *atomic.Int64
}

func (scope *observedReadScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	scope.queries.Add(1)
	return scope.Queryer.Query(ctx, plan)
}

type unscopedReader struct{ db.Queryer }

type nestedReadScope struct {
	*observedReadScope
	db.SnapshotReader
}

type atomicReadScope struct {
	*observedReadScope
	owner db.Atomic
}

func (scope atomicReadScope) Atomic(ctx context.Context, run func(db.Session) error) error {
	return scope.owner.Atomic(ctx, run)
}

var _ db.Atomic = atomicReadScope{}
var _ db.SnapshotReader = nestedReadScope{}

type failedReadCleanup struct {
	db.SnapshotReader
	err error
}

func (owner failedReadCleanup) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	return errors.Join(owner.SnapshotReader.ReadSnapshot(ctx, read), owner.err)
}

type countedReadScopeInput struct{ calls *int }

func (input countedReadScopeInput) BuildCreate() orm.Mutation[owners.Owner] {
	*input.calls++
	return owners.NewOwnerCreate("read-scope-missing").BuildCreate()
}

func runFacadeReadScope(t *testing.T, backend collectionBackend, owner owners.Owner, a, b labels.Label) {
	t.Helper()
	ctx := t.Context()
	factory, err := project.BindCollections()
	check(t, err)
	root, err := factory.OwnersOwnerLabels.From(backend, owner)
	check(t, err)
	check(t, root.Set(ctx, []labels.Label{a, b}))
	snapshots := backend.(db.SnapshotReader)

	t.Run("admission", func(t *testing.T) {
		var typedNil *observedReadScope
		for _, reader := range []db.Queryer{nil, typedNil, backend, unscopedReader{backend}} {
			if _, err := project.UsingReadScope(reader); err == nil {
				t.Fatal("read facade accepted an invalid or root scope")
			}
			if _, err := factory.OwnersOwnerLabels.InReadScope(reader, owner); err == nil {
				t.Fatal("read collection accepted an invalid or root scope")
			}
		}
		check(t, backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			if _, err := project.UsingReadScope(session); err == nil {
				return errors.New("writable session accepted as a read facade")
			}
			if _, err := factory.OwnersOwnerLabels.InReadScope(session, owner); err == nil {
				return errors.New("writable session accepted as a read collection")
			}
			return nil
		}))
		check(t, snapshots.ReadSnapshot(ctx, func(reader db.Queryer) error {
			var queries atomic.Int64
			observer := &observedReadScope{reader, reader.(db.SessionValidator), &queries}
			for _, invalid := range []db.Queryer{unscopedReader{reader}, nestedReadScope{observer, snapshots}, atomicReadScope{observer, backend.(db.Atomic)}} {
				if _, err := project.UsingReadScope(invalid); err == nil {
					return errors.New("read facade accepted missing lifetime or transaction capability")
				}
				if _, err := factory.OwnersOwnerLabels.InReadScope(invalid, owner); err == nil {
					return errors.New("read collection accepted missing lifetime or transaction capability")
				}
			}
			if _, err := factory.OwnersOwnerLabels.From(reader, owner); err == nil {
				return errors.New("borrowed read scope accepted by root constructor")
			}
			if queries.Load() != 0 {
				return errors.New("invalid read constructors attempted SQL")
			}
			return nil
		}))
	})

	t.Run("reads_and_lifetime", func(t *testing.T) {
		var reads atomic.Int64
		var reader *observedReadScope
		var api project.Models
		var model *project.OwnersOwner
		var eager *project.OwnersOwnerLabelsLink
		var view *project.OwnersOwnerLabelsCollection
		var held, empty project.LabelsLabelQuery
		var direct *orm.ManyCollection[labels.Label, owners.OwnerLabelsLink]
		check(t, snapshots.ReadSnapshot(ctx, func(scope db.Queryer) error {
			reader = &observedReadScope{scope, scope.(db.SessionValidator), &reads}
			var err error
			api, err = project.UsingReadScope(reader)
			if err != nil {
				return err
			}
			base := api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(owner.ID))
			calls := 0
			existing, created, err := base.GetOrCreate(ctx, countedReadScopeInput{&calls})
			if err != nil || created || existing == nil || existing.ID != owner.ID || calls != 0 {
				return fmt.Errorf("read-only existing GetOrCreate: %v", err)
			}
			rows, err := base.PrefetchRelated(base.Prefetch.Labels.WithChildren(api.LabelsLabel.Prefetch.Owners)).All(ctx)
			if err != nil || len(rows) != 1 || rows[0].ID != owner.ID {
				return fmt.Errorf("snapshot owner graph: %v", err)
			}
			model = rows[0]
			before := reads.Load()
			view, err = model.Labels()
			if err != nil {
				return err
			}
			held, err = view.Query()
			if err != nil {
				return err
			}
			children, err := held.All(ctx)
			if err != nil || len(children) != 2 {
				return fmt.Errorf("snapshot collection: %v", err)
			}
			for _, child := range children {
				parents, err := child.Owners()
				if err != nil {
					return err
				}
				values, err := parents.All(ctx)
				if err != nil || len(values) != 1 || values[0].ID != owner.ID {
					return fmt.Errorf("snapshot nested reverse: %v", err)
				}
			}
			if reads.Load() != before {
				return errors.New("snapshot prefetch graph did not retain its cache")
			}
			empty = held.Filter(labels.LabelFields.ID.In())
			if values, err := empty.All(ctx); err != nil || len(values) != 0 {
				return fmt.Errorf("empty read query: %v", err)
			}
			var found bool
			links := api.OwnersOwnerLabelsLink.OrderBy(owners.OwnerLabelsLinkFields.ID.Asc())
			eager, found, err = links.SelectRelated(links.Related.Source, links.Related.Target).First(ctx)
			if err != nil || !found {
				return fmt.Errorf("snapshot eager link: %v", err)
			}
			before = reads.Load()
			parent, err := eager.Source(ctx)
			if err != nil || parent.ID != owner.ID {
				return fmt.Errorf("snapshot selected owner: %v", err)
			}
			label, err := eager.Target(ctx)
			if err != nil || label.ID != a.ID && label.ID != b.ID {
				return fmt.Errorf("snapshot selected label: %v", err)
			}
			if reads.Load() != before {
				return errors.New("snapshot selected graph lost its cache")
			}
			direct, err = factory.OwnersOwnerLabels.InReadScope(reader, owner)
			if err != nil {
				return err
			}
			values, err := direct.All(ctx)
			if err != nil || len(values) != 2 {
				return fmt.Errorf("direct read collection: %v", err)
			}
			return nil
		}))
		before := reads.Load()
		if _, err := project.UsingReadScope(reader); err == nil {
			t.Fatal("expired reader rebound")
		}
		if _, err := factory.OwnersOwnerLabels.InReadScope(reader, owner); err == nil {
			t.Fatal("expired reader rebound to collection")
		}
		if _, err := held.All(ctx); err == nil {
			t.Fatal("warm query escaped read scope")
		}
		if count, err := held.Count(ctx); err == nil || count != 0 {
			t.Fatal("warm count escaped read scope", count, err)
		}
		if values, err := empty.All(ctx); err == nil || values != nil {
			t.Fatal("empty query escaped read scope", err)
		}
		if _, err := model.Unwrap(); err == nil {
			t.Fatal("model escaped read scope")
		}
		if _, err := view.All(ctx); err == nil {
			t.Fatal("collection view escaped read scope")
		}
		if _, err := direct.All(ctx); err == nil {
			t.Fatal("direct collection cache escaped read scope")
		}
		if _, err := eager.Source(ctx); err == nil {
			t.Fatal("selected relation escaped read scope")
		}
		if _, err := api.OwnersOwner.New(owner); err == nil {
			t.Fatal("expired facade constructed another model")
		}
		if reads.Load() != before {
			t.Fatal("expired facade attempted SQL")
		}
	})

	t.Run("writes_refused", func(t *testing.T) {
		var reads atomic.Int64
		check(t, snapshots.ReadSnapshot(ctx, func(scope db.Queryer) error {
			reader := &observedReadScope{scope, scope.(db.SessionValidator), &reads}
			api, err := project.UsingReadScope(reader)
			if err != nil {
				return err
			}
			draft, err := api.OwnersOwner.New(owners.Owner{Name: "read-scope-missing"})
			if err != nil {
				return err
			}
			before := reads.Load()
			if err := draft.Save(ctx); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || draft.ID != 0 {
				return fmt.Errorf("read-only model Save: %v", err)
			}
			if count, err := api.OwnersOwner.Update(ctx); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || count != 0 {
				return fmt.Errorf("empty query update on read scope: %v", err)
			}
			if _, err := api.OwnersOwner.BulkCreate(ctx, nil); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				return fmt.Errorf("empty bulk create on read scope: %v", err)
			}
			if count, err := api.OwnersOwner.BulkUpdate(ctx, nil, orm.BulkUpdateFields(owners.OwnerFields.Name)); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || count != 0 {
				return fmt.Errorf("empty bulk update on read scope: %v", err)
			}
			model, err := api.OwnersOwner.New(owner)
			if err != nil {
				return err
			}
			view, err := model.Labels()
			if err != nil {
				return err
			}
			for _, mutate := range []func() error{
				func() error { return view.Add(ctx, nil) },
				func() error { return view.Remove(ctx) },
				func() error { return view.Set(ctx, nil) },
				func() error { return view.Clear(ctx) },
				func() error { return view.AddKeys(ctx, []int64{a.ID}) },
			} {
				if err := mutate(); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
					return fmt.Errorf("read-only collection mutation: %v", err)
				}
			}
			if reads.Load() != before {
				return errors.New("read-only writes attempted SQL")
			}
			links := api.OwnersOwnerLabelsLink.OrderBy(owners.OwnerLabelsLinkFields.ID.Asc())
			link, found, err := links.First(ctx)
			if err != nil || !found {
				return fmt.Errorf("read-only link lookup: %v", err)
			}
			originalOwner := link.SourceID
			pendingLink, err := link.WithSource(draft)
			if err != nil {
				return err
			}
			before = reads.Load()
			if err := pendingLink.Save(ctx); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || pendingLink.SourceID != 0 || link.SourceID != originalOwner {
				return fmt.Errorf("read-only Save reconciled a pending relation: %v", err)
			}
			pending, err := pendingLink.Source(ctx)
			if err != nil || pending != draft || reads.Load() != before {
				return fmt.Errorf("read-only Save changed pending relation state: %v", err)
			}
			calls := 0
			missing := api.OwnersOwner.Filter(owners.OwnerFields.Name.Exact("read-scope-missing"))
			value, created, err := missing.GetOrCreate(ctx, countedReadScopeInput{&calls})
			if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || value != nil || created || calls != 0 {
				return fmt.Errorf("read-only missing GetOrCreate evaluated input: %v", err)
			}
			return nil
		}))
		actual, found, err := owners.OwnerObjects.Using(backend).Filter(owners.OwnerFields.ID.Exact(owner.ID)).OrderBy(owners.OwnerFields.ID.Asc()).First(ctx)
		check(t, err)
		if !found || actual.Name != owner.Name {
			t.Fatal("read-only work changed stored owner")
		}
		if count, err := owners.OwnerObjects.Using(backend).Filter(owners.OwnerFields.Name.Exact("read-scope-missing")).Count(ctx); err != nil || count != 0 {
			t.Fatal("read-only work created an owner", count, err)
		}
		fresh, err := root.Fresh()
		check(t, err)
		values, err := fresh.All(ctx)
		check(t, err)
		if len(values) != 2 {
			t.Fatal("read-only work changed stored membership", values)
		}
	})

	t.Run("cancel_and_cleanup", func(t *testing.T) {
		var reads atomic.Int64
		work, cancel := context.WithCancel(ctx)
		defer cancel()
		err := snapshots.ReadSnapshot(work, func(scope db.Queryer) error {
			api, err := project.UsingReadScope(&observedReadScope{scope, scope.(db.SessionValidator), &reads})
			if err != nil {
				return err
			}
			q := api.OwnersOwner.OrderBy(owners.OwnerFields.ID.Asc())
			if _, err := q.All(work); err != nil {
				return err
			}
			before := reads.Load()
			cancel()
			if values, err := q.All(work); !errors.Is(err, context.Canceled) || values != nil || reads.Load() != before {
				return fmt.Errorf("cancelled warm read: %v", err)
			}
			return work.Err()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatal("snapshot did not retain cancellation", err)
		}
		cleanup := errors.New("native read owner cleanup failure")
		count, err := db.ReadSnapshotResult(ctx, failedReadCleanup{snapshots, cleanup}, func(work context.Context, scope db.Queryer) (int64, error) {
			api, err := project.UsingReadScope(scope)
			if err != nil {
				return 0, err
			}
			return api.OwnersOwner.Count(work)
		})
		if count != 0 || !errors.Is(err, cleanup) {
			t.Fatal("read result published before successful cleanup", count, err)
		}
	})
}
