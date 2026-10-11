package article_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/examples/article/articleapp"
)

// The existing SQLite/PostgreSQL owners run each case over a real Bearer HTTP
// connection and compare the durable row after the complete response.
func verifyArticleTypedDelete(t *testing.T, native articleapp.Backend) {
	t.Helper()
	repository, err := articleapp.NewRepository(native)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "ignored_body_query", "missing", "invalid_id", "permission", "anonymous", "missing_callback", "swallowed_miss", "joined_miss", "confirmed_miss", "delete_error", "delete_cancel", "delete_rejection", "delete_rollback_unknown", "commit_unknown"} {
		t.Run(mode, func(t *testing.T) {
			before, err := repository.Create(t.Context(), articleapp.Input{Title: "Typed deletion"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := repository.Delete(context.Background(), before.ID); err != nil && !errors.Is(err, articleapp.ErrNotFound) {
					t.Error(err)
				}
			}()
			backend := &typedArticleBackend{Backend: native, id: before.ID, mode: mode}
			fixture := newArticleAPIBearerFixture(t, backend)
			path, authorization, body := fmt.Sprintf("/api/articles/%d/", before.ID), "Bearer "+articleAPIFullBearer, ""
			want, transactions := 500, int64(1)
			switch mode {
			case "success", "ignored_body_query":
				want = 204
				if mode == "ignored_body_query" {
					path, body = path+"?ignored=%zz", "{"
				}
			case "missing":
				path, want = "/api/articles/9223372036854775807/", 404
			case "invalid_id":
				path, want, transactions = "/api/articles/0/", 404, 0
			case "permission":
				authorization, want, transactions = "Bearer "+articleAPIViewerBearer, 403, 0
			case "anonymous":
				authorization, want, transactions = "", 401, 0
			case "confirmed_miss":
				want = 404
			case "delete_rejection":
				want = 400
			}
			result := fixture.request(t, articleAPIBearerRequest{method: "DELETE", target: path, contentType: "application/json", body: body, authorization: authorization})
			if result.status != want || strings.Contains(result.body, "private") || backend.transactions.Load() != transactions {
				t.Fatal("delete outcome or attempt count", result.status, result.body, backend.transactions.Load())
			}
			if want == 204 {
				result.requireNoContent(t)
			}
			if mode == "delete_rejection" && !strings.Contains(result.body, `"code":"protected"`) {
				t.Fatal("confirmed deletion rejection lost public diagnostics", result.body)
			}
			if transactions == 0 && backend.queries.Load()+backend.writes.Load() != 0 {
				t.Fatal("admission or invalid path reached persistence")
			}
			stored, found, err := repository.Get(t.Context(), before.ID)
			deleted := want == 204 || mode == "commit_unknown"
			if err != nil || found == deleted || found && stored != before {
				t.Fatal("deletion response disagrees with durable state", found, stored, err)
			}
			if deleted && backend.writes.Load() != 1 {
				t.Fatal("delete or uncertain commit retried the mutation", backend.writes.Load())
			}
		})
	}
}
