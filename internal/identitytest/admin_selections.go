package identitytest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type adminSelectionBoundary struct {
	*systemstate.Runtime
	mode   string
	cancel context.CancelFunc
	before func(context.Context) error
	fault  error
}

func (b *adminSelectionBoundary) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	base := &managementBoundary{ManagementBackend: b.Runtime, mode: b.mode}
	err := base.ReadSnapshot(ctx, func(reader db.Queryer) error {
		if reader == nil {
			return callback(nil)
		}
		return callback(&adminSelectionReader{Queryer: reader, boundary: b})
	})
	if b.cancel != nil {
		b.cancel()
	}
	return err
}

type adminSelectionReader struct {
	db.Queryer
	boundary *adminSelectionBoundary
}

func (r *adminSelectionReader) ValidateSession(ctx context.Context) error {
	validator, ok := r.Queryer.(db.SessionValidator)
	if !ok {
		return errors.New("private-admin-fault: missing reader lifetime")
	}
	return validator.ValidateSession(ctx)
}
func (r *adminSelectionReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if plan.Table() == "godj_identity_group" {
		if r.boundary.before != nil {
			if err := r.boundary.before(ctx); err != nil {
				return nil, err
			}
		}
		if r.boundary.fault != nil {
			return nil, r.boundary.fault
		}
	}
	return r.Queryer.Query(ctx, plan)
}

func runAdminSelectionBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("choice_snapshot_current_authority_and_detached_results", func(t *testing.T) {
		backend, second := open(t)
		f := newManagementFixture(t, backend, 0)
		group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Original choice"))
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		boundary := &adminSelectionBoundary{Runtime: f.runtime, before: func(ctx context.Context) error {
			calls++
			return second.CoordinatedAtomic(ctx, func(session db.Session) error {
				if _, err := models.GroupObjects.Update(ctx, session, group, models.GroupPatch{}.WithName("Concurrent choice").WithRevision(2)); err != nil {
					return err
				}
				_, err := models.UserObjects.Update(ctx, session, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
				return err
			})
		}}
		manager := f.manager(t, boundary)
		choices, err := manager.UserGroupChoices(t.Context(), f.actor)
		if err != nil || len(choices) != 1 || choices[0].Label != "Original choice" || calls != 1 {
			t.Fatal("choices escaped authority snapshot", choices, err, calls)
		}
		choices[0].Label = "Caller edit"
		if values, err := manager.UserGroupChoices(t.Context(), f.actor); !errors.Is(err, &identity.Error{Code: identity.CodePermission}) || values != nil || calls != 1 {
			t.Fatal("revoked authority reused", values, err)
		}
		if row, _, err := models.GroupObjects.Using(backend).OrderBy(models.GroupFields.ID.Asc()).First(t.Context()); err != nil || row.Name != "Concurrent choice" {
			t.Fatal("result mutated storage", err)
		}
	})
	t.Run("choices_and_history_publish_nothing_after_read_failure", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		for _, mode := range []string{"read_end_failure", "read_zero", "read_nil", "read_twice", "read_swallowed_failure", "canceled"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				boundary := &adminSelectionBoundary{Runtime: f.runtime, mode: mode}
				if mode == "canceled" {
					boundary.cancel = cancel
				}
				manager := f.manager(t, boundary)
				for _, call := range []func() (int, error){
					func() (int, error) { values, err := manager.UserGroupChoices(ctx, f.actor); return len(values), err },
					func() (int, error) {
						values, err := manager.UserPermissionChoices(ctx, f.actor)
						return len(values), err
					},
					func() (int, error) {
						values, err := manager.GroupPermissionChoices(ctx, f.actor, admin.ActionAdd)
						return len(values), err
					},
					func() (int, error) {
						values, err := manager.UserHistory(ctx, f.actor, f.user.ID, 10)
						return len(values), err
					},
				} {
					if count, err := call(); err == nil || count != 0 {
						t.Fatal("failed scope published data", count, err)
					}
				}
			})
		}
		fault := errors.Join(errors.New("private-admin-fault"), &identity.Error{Code: identity.CodePermission})
		manager := f.manager(t, &adminSelectionBoundary{Runtime: f.runtime, fault: fault})
		if choices, err := manager.UserGroupChoices(t.Context(), f.actor); choices != nil || !errors.Is(err, fault) || err.(*identity.Error).Code != identity.CodePersistence {
			t.Fatal("execution failure became permission denial", err)
		}
		if values, err := manager.GroupPermissionChoices(t.Context(), f.actor, admin.ActionDelete); values != nil || !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) {
			t.Fatal("unsupported choice action accepted", err)
		}
	})
	t.Run("oversized_choice_catalog_is_never_silently_truncated", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		if err := backend.CoordinatedAtomic(t.Context(), func(session db.Session) error {
			for i := range identity.MaximumManagementChoices {
				if _, err := models.GroupObjects.Create(t.Context(), session, models.NewGroupCreate(fmt.Sprintf("choice-%04d", i))); err != nil {
					return err
				}
				if i > 0 {
					if _, err := models.PermissionObjects.Create(t.Context(), session, models.NewPermissionCreate(fmt.Sprintf("choice.p%04d", i), "Permission")); err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		manager := f.manager(t, f.runtime)
		for _, call := range []func() ([]identity.ManagementChoice, error){func() ([]identity.ManagementChoice, error) { return manager.UserGroupChoices(t.Context(), f.actor) }, func() ([]identity.ManagementChoice, error) {
			return manager.UserPermissionChoices(t.Context(), f.actor)
		}} {
			choices, err := call()
			if err != nil || len(choices) != identity.MaximumManagementChoices {
				t.Fatal("complete catalog rejected", len(choices), err)
			}
			for i := 1; i < len(choices); i++ {
				if choices[i-1].ID >= choices[i].ID {
					t.Fatal("choices not deterministically ordered")
				}
			}
		}
		if _, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("overflow")); err != nil {
			t.Fatal(err)
		}
		if _, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("choice.overflow", "Overflow")); err != nil {
			t.Fatal(err)
		}
		for _, call := range []func() ([]identity.ManagementChoice, error){func() ([]identity.ManagementChoice, error) { return manager.UserGroupChoices(t.Context(), f.actor) }, func() ([]identity.ManagementChoice, error) {
			return manager.UserPermissionChoices(t.Context(), f.actor)
		}, func() ([]identity.ManagementChoice, error) {
			return manager.GroupPermissionChoices(t.Context(), f.actor, admin.ActionAdd)
		}} {
			if choices, err := call(); choices != nil || !errors.Is(err, &identity.Error{Code: identity.CodePersistence}) {
				t.Fatal("oversized catalog truncated", len(choices), err)
			}
		}
	})
}
