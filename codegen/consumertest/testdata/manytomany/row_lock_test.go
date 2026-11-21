package consumer

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type rowLockRecorder struct {
	db.Session
	plans  []query.Plan
	before func(context.Context, query.Plan) error
}

func (reader *rowLockRecorder) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	reader.plans = append(reader.plans, plan)
	if reader.before != nil {
		if err := reader.before(ctx, plan); err != nil {
			return nil, err
		}
	}
	return reader.Session.Query(ctx, plan)
}
func (reader *rowLockRecorder) ValidateSession(ctx context.Context) error {
	return reader.Session.(db.SessionValidator).ValidateSession(ctx)
}

func TestGeneratedRowLocks(t *testing.T) { withCollectionBackends(t, runGeneratedRowLocks) }

func runGeneratedRowLocks(t *testing.T, backend collectionBackend, open func() (collectionBackend, error), _ func(string) error, postgres bool) {
	t.Cleanup(func() { check(t, backend.Close()) })
	migrateCollections(t, backend)
	ctx := t.Context()
	owner, err := owners.OwnerObjects.Create(ctx, backend, owners.NewOwnerCreate("lock owner"))
	check(t, err)
	label, err := labels.LabelObjects.Create(ctx, backend, labels.NewLabelCreate("lock label"))
	check(t, err)
	collections, err := project.BindCollections()
	check(t, err)
	collection, err := collections.OwnersOwnerLabels.From(backend, owner)
	check(t, err)
	check(t, collection.Add(ctx, []labels.Label{label}))
	required, err := owners.RequiredOwnerObjects.Create(ctx, backend, owners.NewRequiredOwnerCreate(owner.ID, 1))
	check(t, err)
	link, err := owners.RankedLinkObjects.Create(ctx, backend, owners.NewRankedLinkCreate(1).WithOwnerID(owner.ID).WithLabelID(label.ID))
	check(t, err)
	badge, err := owners.BadgeObjects.Create(ctx, backend, owners.BadgeCreate{}.WithOwnerID(owner.ID).WithName("lock badge"))
	check(t, err)
	api, err := project.Using(backend)
	check(t, err)
	relations, err := project.BindRelations()
	check(t, err)
	verify := func(t *testing.T, err error, inside bool) {
		t.Helper()
		if postgres && inside {
			check(t, err)
			return
		}
		code := query.CodeUnsupported
		if postgres {
			code = query.CodeTransactionRequired
		}
		if !errors.Is(err, &query.Error{Code: code}) {
			t.Fatalf("lock boundary inside=%v: %v", inside, err)
		}
	}
	t.Run("root_cache_count_and_errors", func(t *testing.T) {
		base := api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(owner.ID))
		warm, err := base.All(ctx)
		check(t, err)
		if len(warm) != 1 {
			t.Fatal("missing warm source")
		}
		locked := base.SelectForUpdate(orm.RowLockOptions{}, base.LockTarget())
		_, err = locked.All(ctx)
		verify(t, err, false)
		count, err := locked.Count(ctx)
		check(t, err)
		if count != 1 {
			t.Fatal("count retained locking or changed membership")
		}
		if _, err := base.SelectForUpdatePaths(orm.RowLockOptions{}, "unknown"); err == nil {
			t.Fatal("unknown lock path accepted")
		}
		if _, err := base.SelectForUpdatePaths(orm.RowLockOptions{NoWait: true, SkipLocked: true}, "self"); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
			t.Fatal("incompatible wait options accepted", err)
		}
		if _, err := base.SelectForUpdatePaths(orm.RowLockOptions{}, "labels"); err == nil {
			t.Fatal("collection target accepted")
		}
	})
	t.Run("typed_dynamic_targets", func(t *testing.T) {
		check(t, backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			reader := &rowLockRecorder{Session: session}
			bound, err := project.UsingSession(reader)
			if err != nil {
				return err
			}
			base := bound.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(required.ID))
			eager := base.SelectRelated(base.Related.Owner)
			_, err = eager.SelectForUpdate(orm.RowLockOptions{NoWait: true}, relations.OwnersRequiredOwner.Owner.LockTarget()).Get(ctx)
			verify(t, err, true)
			dynamic, err := eager.SelectForUpdatePaths(orm.RowLockOptions{NoWait: true}, "owner")
			if err != nil {
				return err
			}
			_, err = dynamic.Get(ctx)
			verify(t, err, true)
			if len(reader.plans) != 2 || !reader.plans[0].Equal(reader.plans[1]) {
				return errors.New("generated typed and dynamic lock plans differ")
			}
			return nil
		}))
	})
	t.Run("terminals_and_scope_lifetime", func(t *testing.T) {
		var retained project.OwnersOwnerQuery
		check(t, backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			bound, err := project.UsingSession(session)
			if err != nil {
				return err
			}
			retained = bound.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(owner.ID)).OrderBy(owners.OwnerFields.ID.Asc()).SelectForUpdate(orm.RowLockOptions{NoKey: true})
			_, err = retained.Get(ctx)
			verify(t, err, true)
			_, _, err = retained.First(ctx)
			verify(t, err, true)
			calls := 0
			err = retained.Iterate(ctx, 1, func(_ context.Context, value *project.OwnersOwner) (bool, error) {
				calls++
				if value.ID != owner.ID {
					return false, errors.New("iterator returned foreign owner")
				}
				return true, nil
			})
			verify(t, err, true)
			if postgres && calls != 1 || !postgres && calls != 0 {
				return fmt.Errorf("iterator calls=%d", calls)
			}
			raw := owners.OwnerObjects.Using(session).Filter(owners.OwnerFields.ID.Exact(owner.ID)).SelectForUpdate(orm.RowLockOptions{})
			_, err = raw.Exists(ctx)
			verify(t, err, true)
			return nil
		}))
		if _, err := retained.All(ctx); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			t.Fatal("expired locked query accepted", err)
		}
		if _, err := retained.Count(ctx); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			t.Fatal("expired count accepted", err)
		}
	})
	t.Run("default_prefetch_and_custom_locks", func(t *testing.T) {
		for _, kind := range []string{"default", "many", "reverse"} {
			check(t, backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
				reader := &rowLockRecorder{Session: session}
				bound, err := project.UsingSession(reader)
				if err != nil {
					return err
				}
				base := bound.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(owner.ID))
				var request project.OwnersOwnerPrefetchQuery
				if kind == "default" {
					request = base.PrefetchRelated(base.Prefetch.Labels).SelectForUpdate(orm.RowLockOptions{}, base.LockTarget())
				}
				if kind == "many" {
					request = base.PrefetchRelated(base.Prefetch.Labels.SelectForUpdate(orm.RowLockOptions{NoWait: true}))
				}
				if kind == "reverse" {
					request = base.PrefetchRelated(base.Prefetch.RankedLinkRows.SelectForUpdate(orm.RowLockOptions{NoWait: true}))
				}
				_, err = request.Get(ctx)
				verify(t, err, true)
				want := 2
				if !postgres && kind == "default" {
					want = 1
				}
				if len(reader.plans) != want {
					return fmt.Errorf("%s prefetch emitted %d plans", kind, len(reader.plans))
				}
				_, rootLocked := reader.plans[0].RowLock()
				if rootLocked != (kind == "default") {
					return errors.New("prefetch lock moved to the wrong query")
				}
				if want == 2 {
					_, targetLocked := reader.plans[1].RowLock()
					if targetLocked != (kind != "default") {
						return errors.New("root lock leaked to target or custom lock disappeared")
					}
				}
				return nil
			}))
		}
	})
	t.Run("eager_cache_cannot_replace_lock", func(t *testing.T) {
		other, err := open()
		check(t, err)
		defer func() { check(t, other.Close()) }()
		check(t, backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			reader := &rowLockRecorder{Session: session}
			bound, err := project.UsingSession(reader)
			if err != nil {
				return err
			}
			base := bound.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(required.ID))
			selected := base.Related.Owner.WithChildren(bound.OwnersOwner.Related.Badge.WithChildren(bound.OwnersBadge.Related.Owner))
			request := base.SelectRelated(selected).PrefetchRelated(base.Prefetch.Owner.SelectForUpdate(orm.RowLockOptions{NoKey: true}))
			result, err := request.Get(ctx)
			verify(t, err, true)
			want := 2
			if postgres {
				want = 3
			}
			if len(reader.plans) != want {
				return errors.New("eager target cache suppressed the explicit lock")
			}
			if _, rootLocked := reader.plans[0].RowLock(); rootLocked {
				return errors.New("target lock affected the source")
			}
			if lock, targetLocked := reader.plans[1].RowLock(); !targetLocked || lock.Strength() != query.LockForNoKeyUpdate {
				return errors.New("target query lost lock options")
			}
			if !postgres {
				return nil
			}
			if len(reader.plans[1].RelationProjections()) != 0 {
				return errors.New("preserved descendants expanded the locking SELECT")
			}
			if _, locked := reader.plans[2].RowLock(); locked {
				return errors.New("preserved descendants inherited the parent lock")
			}
			parent, err := result.Owner(ctx)
			if err != nil {
				return err
			}
			child, present, err := parent.Badge(ctx)
			if err != nil || !present || child.ID != badge.ID {
				return fmt.Errorf("preserved badge present=%v: %w", present, err)
			}
			grandchild, err := child.Owner(ctx)
			if err != nil || grandchild.ID != owner.ID || len(reader.plans) != want {
				return fmt.Errorf("selected descendants were lost or read lazily: %w", err)
			}
			return other.(db.Atomic).Atomic(ctx, func(otherSession db.Session) error {
				_, err := owners.BadgeObjects.Using(otherSession).Filter(owners.BadgeFields.ID.Exact(badge.ID)).SelectForUpdate(orm.RowLockOptions{NoWait: true}).Get(ctx)
				return err
			})
		}))
	})
	t.Run("locked_refresh_preserves_current_descendants", func(t *testing.T) {
		second, err := owners.OwnerObjects.Create(ctx, backend, owners.NewOwnerCreate("new locked parent"))
		check(t, err)
		secondBadge, err := owners.BadgeObjects.Create(ctx, backend, owners.BadgeCreate{}.WithOwnerID(second.ID).WithName("new badge"))
		check(t, err)
		optional, err := owners.OptionalObjects.Create(ctx, backend, owners.NewOptionalCreate().WithLinkID(link.ID))
		check(t, err)
		other, err := open()
		check(t, err)
		defer func() { check(t, other.Close()) }()
		check(t, backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			reader := &rowLockRecorder{Session: session}
			bound, err := project.UsingSession(reader)
			if err != nil {
				return err
			}
			base := bound.OwnersOptional.Filter(owners.OptionalFields.ID.Exact(optional.ID))
			selected := base.Related.Link.WithChildren(bound.OwnersRankedLink.Related.Owner.WithChildren(bound.OwnersOwner.Related.Badge))
			eager := base.SelectRelated(selected)
			warm, err := eager.Get(ctx)
			if err != nil {
				return err
			}
			reader.plans = nil
			changed, failNext := false, false
			failure := errors.New("descendant read failed after locking")
			reader.before = func(ctx context.Context, plan query.Plan) error {
				if !postgres {
					return nil
				}
				if _, locked := plan.RowLock(); locked && !changed {
					changed = true
					bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()
					_, err := owners.RankedLinkObjects.Patch(bounded, other, link, owners.RankedLinkPatch{}.WithOwnerID(second.ID))
					return err
				}
				if len(reader.plans) == 3 && failNext {
					failNext = false
					return failure
				}
				return nil
			}
			request := eager.PrefetchRelated(base.Prefetch.Link.SelectForUpdate(orm.RowLockOptions{}))
			result, err := request.Get(ctx)
			if !postgres {
				verify(t, err, true)
				return nil
			}
			if err != nil {
				return err
			}
			if !changed || len(reader.plans) != 3 || len(reader.plans[1].RelationProjections()) != 0 {
				return errors.New("refresh reused the old graph or widened the lock")
			}
			currentLink, present, err := result.Link(ctx)
			if err != nil || !present {
				return fmt.Errorf("refreshed link present=%v: %w", present, err)
			}
			parent, present, err := currentLink.Owner(ctx)
			if err != nil || !present || parent.ID != second.ID {
				return fmt.Errorf("refreshed FK retained the old parent: %w", err)
			}
			child, present, err := parent.Badge(ctx)
			if err != nil || !present || child.ID != secondBadge.ID {
				return fmt.Errorf("refreshed nested graph is stale: %w", err)
			}
			oldLink, present, err := warm.Link(ctx)
			if err != nil || !present {
				return fmt.Errorf("old source graph was modified: %w", err)
			}
			oldParent, present, err := oldLink.Owner(ctx)
			if err != nil || !present || oldParent.ID != owner.ID || len(reader.plans) != 3 {
				return fmt.Errorf("old graph was modified or descendants were loaded lazily: %w", err)
			}
			reader.plans = nil
			failNext = true
			failedRequest := eager.PrefetchRelated(base.Prefetch.Link.SelectForUpdate(orm.RowLockOptions{}))
			if _, err := failedRequest.All(ctx); !errors.Is(err, failure) || failNext {
				return fmt.Errorf("partial locked graph was accepted: %w", err)
			}
			reader.plans = nil
			retried, err := failedRequest.All(ctx)
			if err != nil || len(retried) != 1 || len(reader.plans) != 3 {
				return fmt.Errorf("retry reused a partial graph: %w", err)
			}
			if _, err := failedRequest.All(ctx); err != nil || len(reader.plans) != 3 {
				return fmt.Errorf("completed graph did not publish its own cache: %w", err)
			}
			return nil
		}))
	})
	t.Run("actual_generated_contention", func(t *testing.T) {
		other, err := open()
		check(t, err)
		defer func() { check(t, other.Close()) }()
		check(t, backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			bound, err := project.UsingSession(session)
			if err != nil {
				return err
			}
			base := bound.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(required.ID))
			_, err = base.SelectRelated(base.Related.Owner).SelectForUpdate(orm.RowLockOptions{}, relations.OwnersRequiredOwner.Owner.LockTarget()).Get(ctx)
			verify(t, err, true)
			if !postgres {
				return nil
			}
			err = other.(db.Atomic).Atomic(ctx, func(otherSession db.Session) error {
				_, err := owners.OwnerObjects.Using(otherSession).Filter(owners.OwnerFields.ID.Exact(owner.ID)).SelectForUpdate(orm.RowLockOptions{NoWait: true}).Get(ctx)
				return err
			})
			var native *pgconn.PgError
			if !errors.As(err, &native) || native.Code != "55P03" {
				return fmt.Errorf("generated target was not locked: %w", err)
			}
			return other.(db.Atomic).Atomic(ctx, func(otherSession db.Session) error {
				_, err := owners.RequiredOwnerObjects.Using(otherSession).Filter(owners.RequiredOwnerFields.ID.Exact(required.ID)).SelectForUpdate(orm.RowLockOptions{NoWait: true}).Get(ctx)
				return err
			})
		}))
	})
}
