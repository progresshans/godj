package identitytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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
	mode          string
	skipSnapshots int
	cancel        context.CancelFunc
	before        func(context.Context) error
	fault         error
}

func (b *adminSelectionBoundary) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	if b.skipSnapshots > 0 {
		b.skipSnapshots--
		return b.Runtime.ReadSnapshot(ctx, callback)
	}
	if b.mode == "canceled" {
		work, cancel := context.WithCancel(ctx)
		defer cancel()
		err := b.Runtime.ReadSnapshot(work, func(reader db.Queryer) error {
			result := callback(reader)
			cancel()
			return result
		})
		return errors.Join(err, work.Err())
	}
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
	_, filtered := plan.Where()
	limit, limited := plan.Limit()
	// User source admission reads current memberships first. Inject the
	// failure/revocation only into the subsequent bounded target Search.
	if plan.Table() == "godj_identity_group" && !filtered && limited && limit == admin.DefaultListLimit+1 {
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

type adminChoicePage struct {
	Items  []struct{ Value, Label string } `json:"items"`
	Offset int                             `json:"offset"`
	Limit  int                             `json:"limit"`
	More   bool                            `json:"more"`
}

func readAdminChoicePage(t *testing.T, response adminHTTPResult) adminChoicePage {
	t.Helper()
	var page adminChoicePage
	if err := json.Unmarshal([]byte(response.body), &page); err != nil {
		t.Fatal("invalid choice JSON", err)
	}
	if response.header.Get("Content-Type") != "application/json; charset=utf-8" || response.header.Get("X-Content-Type-Options") != "nosniff" || page.Items == nil {
		t.Fatal("choice response lost its bounded JSON envelope")
	}
	return page
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
				if _, err := models.GroupObjects.Patch(ctx, session, group, models.GroupPatch{}.WithName("Concurrent choice").WithRevision(2)); err != nil {
					return err
				}
				_, err := models.UserObjects.Patch(ctx, session, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
				return err
			})
		}}
		h := newIdentityAdminHTTP(t, f, boundary)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		path := adminObjectPath("users", "change/choices/groups", f.user.ID)
		page := readAdminChoicePage(t, h.call(t, h.client, "GET", path, nil, 200))
		if len(page.Items) != 1 || page.Items[0].Label != "Original choice" || calls != 1 {
			t.Fatal("choices escaped authority snapshot", page, calls)
		}
		page.Items[0].Label = "Caller edit"
		h.call(t, h.client, "GET", path, nil, 403)
		if calls != 1 {
			t.Fatal("revoked authority performed target I/O")
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
				boundary := &adminSelectionBoundary{Runtime: f.runtime}
				h := newIdentityAdminHTTP(t, f, boundary)
				h.loginAdmin(t, h.client, "manager", managementOldPassword)
				boundary.mode = mode
				for _, path := range []string{
					adminObjectPath("users", "change/choices/groups", f.user.ID),
					adminObjectPath("users", "change/choices/permissions", f.user.ID),
					"/admin/groups/add/choices/permissions/",
				} {
					if strings.HasPrefix(path, "/admin/users/change/") {
						boundary.skipSnapshots = 1
					}
					response := h.call(t, h.client, "GET", path, nil, 500)
					if strings.Contains(response.body, `"items"`) {
						t.Fatal("failed choice scope published a page")
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if mode == "canceled" {
					boundary.mode, boundary.cancel = "", cancel
				}
				if values, err := f.manager(t, boundary).UserHistory(ctx, f.actor, f.user.ID, 10); err == nil || len(values) != 0 {
					t.Fatal("failed history scope published data", err)
				}
			})
		}
		fault := errors.Join(errors.New("private-admin-fault"), &identity.Error{Code: identity.CodePermission})
		boundary := &adminSelectionBoundary{Runtime: f.runtime, fault: fault}
		h := newIdentityAdminHTTP(t, f, boundary)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		response := h.call(t, h.client, "GET", adminObjectPath("users", "change/choices/groups", f.user.ID), nil, 500)
		if strings.Contains(response.body, `"items"`) {
			t.Fatal("execution failure became successful selection")
		}
		if source, err := f.manager(t, f.runtime).GroupPermissionChoiceSource(admin.ActionDelete); source != nil || !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) {
			t.Fatal("unsupported choice action accepted", err)
		}
	})
	t.Run("large_choice_catalog_has_complete_pages_and_search", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		const total = 121
		if err := backend.CoordinatedAtomic(t.Context(), func(session db.Session) error {
			for i := range total {
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
		h := newIdentityAdminHTTP(t, f, f.runtime)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		for _, path := range []string{
			adminObjectPath("users", "change/choices/groups", f.user.ID),
			adminObjectPath("users", "change/choices/permissions", f.user.ID),
			"/admin/groups/add/choices/permissions/",
		} {
			address, err := url.Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			seen, last := 0, int64(0)
			for offset := 0; ; offset += 50 {
				params := address.Query()
				params.Set("offset", strconv.Itoa(offset))
				params.Set("limit", "50")
				address.RawQuery = params.Encode()
				page := readAdminChoicePage(t, h.call(t, h.client, "GET", address.String(), nil, 200))
				if page.Offset != offset || page.Limit != 50 || len(page.Items) > 50 || page.More != (offset+len(page.Items) < total) {
					t.Fatal("page boundary silently truncated catalog", page)
				}
				for _, item := range page.Items {
					id, err := strconv.ParseInt(item.Value, 10, 64)
					if err != nil || id <= last {
						t.Fatal("choice key/order changed", item.Value, err)
					}
					last, seen = id, seen+1
				}
				if !page.More {
					break
				}
			}
			if seen != total {
				t.Fatal("catalog row missing or duplicated", seen)
			}
			params := address.Query()
			params.Del("offset")
			params.Set("q", "0120")
			address.RawQuery = params.Encode()
			page := readAdminChoicePage(t, h.call(t, h.client, "GET", address.String(), nil, 200))
			if len(page.Items) != 1 || page.More || !strings.Contains(page.Items[0].Label, "0120") {
				t.Fatal("search did not find target outside first page", page)
			}
		}
	})
}
