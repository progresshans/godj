package identitytest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/web/sessionauth"
)

// Only the manager uses this probe. Authentication still resolves its real
// database-backed session through f.runtime before header validation.
type headerManagementBackend struct {
	identity.ManagementBackend
	reads, writes atomic.Int64
	cancelWrite   atomic.Bool
}

func (b *headerManagementBackend) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	b.reads.Add(1)
	return b.ManagementBackend.ReadSnapshot(ctx, callback)
}
func (b *headerManagementBackend) writeContext(ctx context.Context) context.Context {
	b.writes.Add(1)
	if b.cancelWrite.Load() {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		return canceled
	}
	return ctx
}
func (b *headerManagementBackend) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	return b.ManagementBackend.CoordinatedAtomic(b.writeContext(ctx), callback)
}
func (b *headerManagementBackend) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	owner, ok := b.ManagementBackend.(db.CoordinatedRelationAtomic)
	if !ok {
		return errors.New("identity header probe lacks relation owner")
	}
	return owner.CoordinatedAtomicRelation(b.writeContext(ctx), callback)
}

func runManagementAPIHeaders(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	const revision = int64(9007199254740993)
	for _, kind := range []string{"users", "groups", "permissions"} {
		t.Run("typed_header_"+kind, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 0)
			id := f.user.ID
			switch kind {
			case "users":
				if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithRevision(revision)); err != nil {
					t.Fatal(err)
				}
			case "groups":
				created, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Revision target").WithRevision(revision))
				if err != nil {
					t.Fatal(err)
				}
				id = created.ID
			case "permissions":
				created, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("fixture.header", "Revision target").WithRevision(revision))
				if err != nil {
					t.Fatal(err)
				}
				id = created.ID
			}
			probe := &headerManagementBackend{ManagementBackend: f.runtime}
			h, _ := newManagementHTTP(t, f, probe)
			for _, test := range []struct {
				name    string
				headers http.Header
				csrf    bool
				status  int
				code    string
			}{
				{"missing", nil, true, 428, "precondition_required"},
				{"empty", http.Header{"If-Revision": {""}}, true, 400, "invalid_precondition"},
				{"duplicate", http.Header{"If-Revision": {"1", "1"}}, true, 400, "invalid_precondition"},
				{"case_alias", http.Header{"If-Revision": {"1"}, "if-revision": {"1"}}, true, 400, "invalid_precondition"},
				{"noncanonical", http.Header{"If-Revision": {"09007199254740993"}}, true, 400, "invalid_precondition"},
				{"comma", http.Header{"If-Revision": {"1,1"}}, true, 400, "invalid_precondition"},
				{"maximum", http.Header{"If-Revision": {"9223372036854775807"}}, true, 400, "invalid_precondition"},
				{"overflow", http.Header{"If-Revision": {"9223372036854775808"}}, true, 400, "invalid_precondition"},
				{"csrf_first", nil, false, 403, "csrf_rejected"},
			} {
				t.Run(test.name, func(t *testing.T) {
					before := catalogSnapshot(t, f)
					request, err := http.NewRequestWithContext(t.Context(), "PATCH", h.server.URL+apiPath(kind, id), strings.NewReader("invalid-json"))
					if err != nil {
						t.Fatal(err)
					}
					request.Header = test.headers.Clone()
					if request.Header == nil {
						request.Header = make(http.Header)
					}
					request.Header.Set("Content-Type", "application/json")
					if test.csrf {
						_, token := h.request(t, h.client, "GET", "/login/", nil)
						request.Header.Set(sessionauth.DefaultCSRFHeader, token)
					}
					response, err := h.client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(response.Body)
					closed := response.Body.Close()
					var envelope struct{ Code string }
					if err != nil || closed != nil || json.Unmarshal(data, &envelope) != nil || response.StatusCode != test.status || envelope.Code != test.code {
						t.Fatal("native header condition", response.StatusCode, string(data), err, closed)
					}
					if response.Header.Get("Revision") != "" || response.Header.Get("Cache-Control") != "no-store" || probe.reads.Load() != 0 || probe.writes.Load() != 0 || f.hasher.calls.Load() != 0 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
						t.Fatal("header rejection reached business I/O or changed durable identity")
					}
				})
			}
			body := `{"name":"Header updated"}`
			if kind == "users" {
				body = `{"first_name":"Header updated"}`
			}
			t.Run("canceled_business_scope", func(t *testing.T) {
				before := catalogSnapshot(t, f)
				probe.cancelWrite.Store(true)
				defer probe.cancelWrite.Store(false)
				managementCall(t, h, h.client, "PATCH", apiPath(kind, id), body, strconv.FormatInt(revision, 10), true, 500)
				if probe.writes.Load() != 1 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
					t.Fatal("canceled accepted header wrote or retried")
				}
			})
			t.Run("exact_success_and_stale", func(t *testing.T) {
				response, updated := managementCall(t, h, h.client, "PATCH", apiPath(kind, id), body, strconv.FormatInt(revision, 10), true, 200)
				if response.Header.Get("Revision") != strconv.FormatInt(revision+1, 10) || apiInteger(t, updated, "revision") != revision+1 {
					t.Fatal("header or response lost exact int64 revision")
				}
				before := catalogSnapshot(t, f)
				_, stale := managementCall(t, h, h.client, "PATCH", apiPath(kind, id), body, strconv.FormatInt(revision, 10), true, 412)
				if string(stale["code"]) != `"revision_conflict"` || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
					t.Fatal("stale condition changed durable identity")
				}
				_, detail := managementCall(t, h, h.client, "GET", apiPath(kind, id), "", "", false, 200)
				if apiInteger(t, detail, "revision") != revision+1 {
					t.Fatal("detail did not retain committed revision")
				}
				audit, err := f.runtime.AuditHistory(t.Context(), "godj_identity."+strings.TrimSuffix(kind, "s"), id, 10)
				if err != nil || len(audit) != 1 {
					t.Fatal("header/cancel/stale audit count", len(audit), err)
				}
			})
		})
	}
}
