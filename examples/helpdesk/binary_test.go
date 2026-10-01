package helpdesk_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func verifyHistoricalBinaryGrowth(t *testing.T, ctx context.Context, backend helpdeskBackend, open func(context.Context) (helpdeskBackend, error), loaded migrations.LoadedDefinitionSet, id int64) {
	t.Helper()
	read := func(reader db.Queryer) models.Ticket {
		t.Helper()
		row, found, err := models.TicketObjects.Using(reader).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal("historical binary row", err)
		}
		return row
	}
	before := read(backend)
	if before.ExternalPayloadDigest != nil {
		t.Fatal("binary addition invented a digest")
	}
	sample := binaryvalue.Value{Data: "\x00\xffa\x80"}
	if _, err := models.TicketObjects.Update(ctx, backend, before, models.TicketPatch{}.WithExternalPayload(helpdeskJSON(t, `{"legacy":1}`)).WithExternalPayloadDigest(sample)); err != nil {
		t.Fatal(err)
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored := read(second)
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if stored.ExternalPayloadDigest == nil || *stored.ExternalPayloadDigest != sample {
		t.Fatal("binary legacy bytes changed on reopen")
	}
	executor := migrations.Executor{Backend: backend}
	state, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0022_ticket_external_url"})))
	if err != nil {
		t.Fatal(err)
	}
	model, found := state.Model("helpdesk", "ticket")
	if !found {
		t.Fatal("binary reverse removed model")
	}
	for _, field := range model.Fields {
		if field.Name == "external_payload_digest" {
			t.Fatal("binary reverse retained column state")
		}
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	after := read(backend)
	stored.ExternalPayloadDigest = nil
	if after.ExternalPayloadDigest != nil || !reflect.DeepEqual(after, stored) {
		t.Fatal("binary re-add computed existing rows or changed unrelated data")
	}
	// Restore the independent public consumer's original fixture explicitly.
	if _, err := models.TicketObjects.Update(ctx, backend, after, models.TicketPatch{}.WithExternalPayloadNull()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read(backend), before) {
		t.Fatal("binary history fixture did not restore")
	}
}

func assertStoredPayloadDigest(t *testing.T, row models.Ticket) string {
	t.Helper()
	if row.ExternalPayload == nil {
		if row.ExternalPayloadDigest != nil {
			t.Fatal("SQL NULL payload retained digest")
		}
		return ""
	}
	sum := sha256.Sum256([]byte(row.ExternalPayload.Text))
	if row.ExternalPayloadDigest == nil || row.ExternalPayloadDigest.Data != string(sum[:]) {
		t.Fatal("digest does not identify actual stored JSON")
	}
	return base64.StdEncoding.EncodeToString(sum[:])
}

type binaryFaultBackend struct {
	helpdesk.Backend
	mode             string
	cancel           context.CancelFunc
	atomics, digests int
}
type binaryFaultSession struct {
	db.Session
	owner *binaryFaultBackend
}

func (b *binaryFaultBackend) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	b.atomics++
	return b.Backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		return callback(decoratedHelpdeskRelation{Session: binaryFaultSession{session, b}, relation: session})
	})
}
func (s binaryFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	digest := false
	for _, assignment := range plan.Assignments() {
		digest = digest || assignment.Field().Name() == "external_payload_digest"
	}
	if digest {
		s.owner.digests++
		if s.owner.mode == "before_digest" {
			return 0, errors.New("injected binary write failure")
		}
	}
	rows, err := s.Session.Update(ctx, plan)
	if err == nil && digest {
		if s.owner.mode == "after_digest" {
			return 0, errors.New("injected failure after native binary write")
		}
		if s.owner.cancel != nil {
			s.owner.cancel()
		}
	}
	return rows, err
}

func verifyHelpdeskBinaryDigest(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, outsideID int64) {
	t.Helper()
	backend := &binaryFaultBackend{Backend: runtime}
	application, err := helpdesk.New(backend, categoryID)
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
	safe := client.request("GET", "/api/tickets/", "", false)
	client.csrf = safe.Header().Get(websessionauth.DefaultCSRFHeader)
	if safe.Code != http.StatusOK || client.csrf == "" {
		t.Fatal("binary client CSRF setup")
	}
	response := client.request("POST", "/api/tickets/", `{"subject":"Binary digest probe","external_payload":{"a":1e0,"b":"bytes"}}`, true)
	var created struct{ ID int64 }
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil || created.ID <= 0 {
		t.Fatal("binary create", response.Code, response.Body.String())
	}
	id := created.ID
	path := fmt.Sprintf("/api/tickets/%d/", id)
	changePath := fmt.Sprintf("/admin/tickets/change/?id=%d", id)
	read := func(reader db.Queryer) models.Ticket {
		t.Helper()
		row, found, err := models.TicketObjects.Using(reader).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal("binary row", err)
		}
		return row
	}
	defer func() {
		row := read(runtime)
		if _, err := deleteHelpdeskTicket(ctx, runtime, &row); err != nil {
			t.Error(err)
		}
	}()
	checkResponse := func(raw []byte, want string) {
		t.Helper()
		var output map[string]json.RawMessage
		if json.Unmarshal(raw, &output) != nil {
			t.Fatal("binary response JSON")
		}
		encoded, ok := output["external_payload_digest"]
		if !ok {
			t.Fatal("binary output omitted")
		}
		if want == "" {
			if string(encoded) != "null" {
				t.Fatal("NULL digest changed")
			}
			return
		}
		var value string
		if json.Unmarshal(encoded, &value) != nil || value != want {
			t.Fatal("digest response differs from storage")
		}
	}
	checkResponse(response.Body.Bytes(), assertStoredPayloadDigest(t, read(runtime)))
	t.Run("native_normalization_and_omission", func(t *testing.T) {
		for _, raw := range []string{`{"a":1e1,"b":2}`, `[]`, `""`, `false`, `0`, `null`} {
			response := client.request("PATCH", path, `{"external_payload":`+raw+`}`, true)
			if response.Code != http.StatusOK {
				t.Fatal("binary payload update", response.Code, response.Body.String())
			}
			row := read(runtime)
			checkResponse(response.Body.Bytes(), assertStoredPayloadDigest(t, row))
			before := backend.digests
			omitted := client.request("PATCH", path, `{}`, true)
			if omitted.Code != http.StatusOK || backend.digests != before || !reflect.DeepEqual(read(runtime), row) {
				t.Fatal("omission changed binary value")
			}
		}
	})
	t.Run("empty_legacy_and_no_hidden_read_write", func(t *testing.T) {
		row := read(runtime)
		for _, value := range []binaryvalue.Value{{}, {Data: "\x00\xff\x80"}, {Data: strings.Repeat("x", 33)}} {
			if _, err := models.TicketObjects.Update(ctx, runtime, row, models.TicketPatch{}.WithExternalPayloadDigest(value)); err != nil {
				t.Fatal(err)
			}
			row = read(runtime)
			before := backend.digests
			response := client.request("GET", path, "", false)
			var body struct{ Ticket map[string]json.RawMessage }
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
				t.Fatal("legacy binary output")
			}
			var text string
			if json.Unmarshal(body.Ticket["external_payload_digest"], &text) != nil || text != value.Base64() || row.ExternalPayloadDigest == nil || backend.digests != before {
				t.Fatal("empty bytes or existing output revalidated")
			}
		}
		// An unrelated scalar write holds the row and clears the legacy digest for
		// SQL NULL. Reads and no-op submissions alone never repair server state.
		response := client.request("PATCH", path, `{"subject":"Binary legacy repair"}`, true)
		if response.Code != http.StatusOK {
			t.Fatal(response.Code)
		}
		assertStoredPayloadDigest(t, read(runtime))
		if _, err := models.TicketObjects.Update(ctx, runtime, read(runtime), models.TicketPatch{}.WithExternalPayload(helpdeskJSON(t, `null`))); err != nil {
			t.Fatal(err)
		}
		response = client.request("PATCH", path, `{"subject":"Binary JSON null"}`, true)
		if response.Code != http.StatusOK || read(runtime).ExternalPayload == nil {
			t.Fatal("stored JSON null became SQL NULL")
		}
		checkResponse(response.Body.Bytes(), assertStoredPayloadDigest(t, read(runtime)))
	})
	t.Run("read_only_admission_and_admin", func(t *testing.T) {
		before := read(runtime)
		for _, method := range []string{"POST", "PUT", "PATCH"} {
			target := path
			if method == "POST" {
				target = "/api/tickets/"
			}
			for _, raw := range []string{`null`, `""`, `"AAAA"`, `123`} {
				count := backend.atomics
				response := client.request(method, target, `{"subject":"Forged","external_payload_digest":`+raw+`}`, true)
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"field":"external_payload_digest"`) || !strings.Contains(response.Body.String(), `"code":"read_only"`) || backend.atomics != count || !reflect.DeepEqual(before, read(runtime)) {
					t.Fatal("server digest became API input", response.Code, response.Body.String())
				}
			}
		}
		page := client.request("GET", changePath, "", false)
		if page.Code != http.StatusOK || strings.Contains(page.Body.String(), ` name="external_payload_digest"`) || !strings.Contains(page.Body.String(), assertStoredPayloadDigest(t, before)) {
			t.Fatal("Admin read-only binary display")
		}
		data := url.Values{"csrfmiddlewaretoken": {client.csrf}, "subject": {before.Subject}, "external_payload": {`{"form":1e0}`}, "external_payload_digest": {"AAAA"}}
		count := backend.atomics
		forged := client.request("POST", changePath, data.Encode(), false)
		if forged.Code != http.StatusBadRequest || backend.atomics != count || !reflect.DeepEqual(before, read(runtime)) {
			t.Fatal("Admin accepted server digest")
		}
		data.Del("external_payload_digest")
		changed := client.request("POST", changePath, data.Encode(), false)
		if changed.Code != http.StatusFound {
			t.Fatal("Admin derived binary write", changed.Code, changed.Body.String())
		}
		before = read(runtime)
		assertStoredPayloadDigest(t, before)
		csrf := client.csrf
		client.csrf = "invalid"
		count = backend.atomics
		rejected := client.request("PATCH", path, `{"external_payload":0}`, true)
		client.csrf = csrf
		if rejected.Code != http.StatusForbidden || backend.atomics != count || !reflect.DeepEqual(before, read(runtime)) {
			t.Fatal("binary side effect bypassed CSRF")
		}
		denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermissions{helpdesk.ChangeTicket})
		denied.cookies = maps.Clone(client.cookies)
		safe := denied.request("GET", "/api/tickets/", "", false)
		denied.csrf = safe.Header().Get(websessionauth.DefaultCSRFHeader)
		if safe.Code != http.StatusOK || denied.csrf == "" {
			t.Fatal("binary permission probe setup")
		}
		rejected = denied.request("PATCH", path, `{"external_payload":0}`, true)
		if rejected.Code != http.StatusForbidden || backend.atomics != count || !reflect.DeepEqual(before, read(runtime)) {
			t.Fatal("binary side effect bypassed change permission")
		}
		digests := backend.digests
		rejected = client.request("PATCH", fmt.Sprintf("/api/tickets/%d/", outsideID), `{"external_payload":0}`, true)
		if rejected.Code != http.StatusNotFound || backend.digests != digests || !reflect.DeepEqual(before, read(runtime)) {
			t.Fatal("binary side effect escaped category scope")
		}
	})
	t.Run("late_failure_and_cancellation_rollback", func(t *testing.T) {
		before := read(runtime)
		for _, mode := range []string{"before_digest", "after_digest", "cancel"} {
			backend.mode = mode
			requestCtx, cancel := context.WithCancel(ctx)
			if mode == "cancel" {
				backend.cancel = cancel
			}
			digests := backend.digests
			response := client.requestContext(requestCtx, "PATCH", path, `{"subject":"Must roll back","external_payload":{"rollback":true}}`, true)
			backend.mode, backend.cancel = "", nil
			if mode == "cancel" && requestCtx.Err() != context.Canceled {
				t.Fatal("native digest cancellation did not run")
			}
			cancel()
			if response.Code != http.StatusInternalServerError || backend.digests != digests+1 || !reflect.DeepEqual(before, read(runtime)) {
				t.Fatal("partial payload/digest survived failure", mode, response.Code)
			}
		}
		second, err := open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		stored := read(second)
		_, native := second.(*postgres.Backend)
		if err := second.Close(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, stored) {
			t.Fatal("binary rollback changed on reopen")
		}
		if native {
			response := client.request("PATCH", path, `{"external_payload":1e5000}`, true)
			if response.Code != http.StatusInternalServerError || !reflect.DeepEqual(before, read(runtime)) {
				t.Fatal("native output expansion published payload or digest")
			}
		}
	})
}
