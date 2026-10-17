package article_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/examples/article/articleapp"
)

// The Session owners exercise actual Admin login, form/CSRF, a target lost
// after the Site's read, and native rollback/commit on both SQLite/PostgreSQL.
func verifyArticleAdminMutationOutcomes(t *testing.T, native articleapp.Backend) {
	t.Helper()
	repository, err := articleapp.NewRepository(native)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"change", "delete"} {
		modes := []string{"success", "confirmed_miss", "swallowed_miss", "joined_miss", "wrapped_miss", "missing_callback", "commit_unknown"}
		if operation == "delete" {
			modes = append(modes, "delete_cancel")
		}
		for _, mode := range modes {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				before, err := repository.Create(t.Context(), articleapp.Input{Title: "Admin mutation baseline"})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := repository.Delete(context.Background(), before.ID); err != nil && !errors.Is(err, articleapp.ErrNotFound) {
						t.Error(err)
					}
				}()
				backend := &typedArticleBackend{Backend: native, id: before.ID, mode: mode}
				fixture := newArticleAPIAdminSessionFixture(t, backend)
				fixture.login(t, fixture.client, articleAdminUsername, articleAdminPassword)
				path := fmt.Sprintf("%s/articles/%s/?id=%d", articleAdminBasePath, operation, before.ID)
				page := fixture.request(t, fixture.client, "GET", path, "", "", "")
				if page.status != 200 {
					t.Fatal("Admin mutation form unavailable", page.status)
				}
				values := url.Values{"csrfmiddlewaretoken": {page.adminCSRFToken(t)}}
				if operation == "delete" {
					values.Set("confirm", "yes")
				} else {
					values.Set("title", "Confirmed Admin change")
					values.Set("summary", "")
					values.Set("slug", "")
				}
				result := fixture.request(t, fixture.client, "POST", path, "application/x-www-form-urlencoded", values.Encode(), "")
				want := 500
				if mode == "success" {
					want = 302
				}
				if mode == "confirmed_miss" {
					want = 404
				}
				if result.status != want || backend.transactions.Load() != 1 || strings.Contains(result.body, "private") {
					t.Fatal("Admin confused a confirmed miss with execution failure", result.status, backend.transactions.Load())
				}
				if want != 302 && result.header.Get("Location") != "" {
					t.Fatal("failed Admin mutation redirected as success")
				}
				stored, found, err := repository.Get(t.Context(), before.ID)
				committed := mode == "success" || mode == "commit_unknown"
				deleted := committed && operation == "delete"
				if err != nil || found == deleted {
					t.Fatal("Admin mutation row lifetime", found, err)
				}
				if found {
					wantTitle := before.Title
					if committed {
						wantTitle = "Confirmed Admin change"
					}
					if stored.Title != wantTitle || stored.Published || stored.Summary != nil || stored.Slug != nil {
						t.Fatal("Admin rollback/commit state differs", stored)
					}
				}
				if committed && backend.writes.Load() != 1 {
					t.Fatal("Admin mutation retried", backend.writes.Load())
				}
				entries := fixture.audit.ForObject("godj_conformance.article", before.ID)
				wantAudit := 0
				if mode == "success" {
					wantAudit = 1
				}
				if len(entries) != wantAudit {
					t.Fatal("unconfirmed Admin outcome published memory audit", len(entries))
				}
				if wantAudit == 1 {
					wantAction, wantLabel := admin.ActionChange, "Confirmed Admin change"
					if operation == "delete" {
						wantAction, wantLabel = admin.ActionDelete, before.Title
					}
					if entries[0].Action != wantAction || entries[0].DisplayLabel != wantLabel || entries[0].ActorID != "staff-1" {
						t.Fatal("Admin audit lost confirmed action, snapshot or actor")
					}
				}
			})
		}
	}
}
