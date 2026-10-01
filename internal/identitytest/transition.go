package identitytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
)

type TransitionBackend interface {
	DirectoryBackend
	systemstate.IdentityBackend
}

type transitionHasher struct {
	auth.PasswordHasher
	hashes, verifies atomic.Int64
}

func (h *transitionHasher) Hash(ctx context.Context, password string) (string, error) {
	h.hashes.Add(1)
	return h.PasswordHasher.Hash(ctx, password)
}
func (h *transitionHasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	h.verifies.Add(1)
	return h.PasswordHasher.Verify(ctx, password, encoded)
}

func applyIdentitySources(t *testing.T, backend TransitionBackend, sources ...definition.Source) {
	t.Helper()
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
}

// RunOperatorTransition starts from the actual historical single-operator
// format, including live durable sessions and audit rows. It does not seed a
// post-adoption shortcut or rebuild the user's identity from configuration.
func RunOperatorTransition(t *testing.T, backend TransitionBackend) {
	t.Helper()
	ctx := t.Context()
	applyIdentitySources(t, backend, systemstate.InitialDefinitionSource())
	base, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	hasher := &transitionHasher{PasswordHasher: base}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "adopted-operator", Active: true, Permissions: []auth.Permission{"helpdesk.ticket.view", "helpdesk.ticket.change"}})
	if err != nil {
		t.Fatal(err)
	}
	policy := systemstate.CredentialPolicy{Principal: principal, PasswordHasher: hasher}
	legacyConfig := systemstate.RuntimeConfig{CredentialPolicy: policy, MaxSessions: 32, AuditCapacity: 32}
	if err := systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{Username: "operator", Password: "original operator password", CredentialPolicy: policy}); err != nil {
		t.Fatal(err)
	}
	old, err := systemstate.OpenExisting(ctx, backend, legacyConfig)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := old.Authenticator().Authenticate(ctx, "operator", "original operator password")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(old.SessionStore(), sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Create(ctx, map[string]string{"_godj_principal_id": principal.ID(), "_godj_credential_stamp": credential.SessionStamp(), "application_value": "keep-me"})
	if err != nil {
		t.Fatal(err)
	}
	event, err := admin.PrepareEvent(principal.ID(), "helpdesk.ticket", 7, admin.ActionChange, []string{"title"}, "Original event")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Atomic(ctx, func(transaction db.Session) error { return old.AppendAudit(ctx, transaction, event) }); err != nil {
		t.Fatal(err)
	}
	sessionRows, auditRows := snapshotIdentitySystemRows(t, backend)
	applyIdentitySources(t, backend, systemstate.IdentityMigrationSources()...)
	// Existing target data and permission labels are not overwritten.
	at := time.Date(2026, 9, 27, 1, 2, 3, 456000, time.UTC)
	otherHash, err := hasher.Hash(ctx, "other password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := models.UserObjects.Create(ctx, backend, models.NewUserCreate("unrelated", "unrelated", otherHash, at))
	if err != nil {
		t.Fatal(err)
	}
	known, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate("helpdesk.ticket.view", "Existing custom label"))
	if err != nil {
		t.Fatal(err)
	}
	config := systemstate.AdoptOperatorConfig{Expected: legacyConfig, Staff: true, Superuser: false, DateJoined: at}
	if _, err := systemstate.OpenIdentity(ctx, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher}); !errors.Is(err, &systemstate.Error{Code: systemstate.CodeIdentityTransitionRequired}) {
		t.Fatal("unadopted domain opened", err)
	}
	wrong := config
	wrong.Expected.CredentialPolicy, err = policy.WithPermissions("helpdesk.ticket.view")
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := systemstate.AdoptOperator(ctx, backend, wrong); !errors.Is(err, &systemstate.Error{Code: systemstate.CodeCredentialPolicyMismatch}) || receipt.UserID() != 0 {
		t.Fatal("mismatched policy adopted source", err)
	}
	fault := errors.New("private source retirement failure")
	failing := &transitionFaultBackend{TransitionBackend: backend, failRetire: fault}
	beforeHashes, beforeVerifies := hasher.hashes.Load(), hasher.verifies.Load()
	if receipt, err := systemstate.AdoptOperator(ctx, failing, config); err == nil || receipt.UserID() != 0 || failing.calls != 1 {
		t.Fatal("failed retirement published or retried adoption", err)
	}
	if count, err := models.UserObjects.Using(backend).Count(ctx); err != nil || count != 1 {
		t.Fatal("failed adoption retained a new user", count, err)
	}
	if count, err := models.PermissionObjects.Using(backend).Count(ctx); err != nil || count != 1 {
		t.Fatal("failed adoption retained new permissions", count, err)
	}
	if _, err := systemstate.InspectIdentityTransition(ctx, backend); !errors.Is(err, &systemstate.Error{Code: systemstate.CodeIdentityTransitionRequired}) {
		t.Fatal("failed adoption retained receipt", err)
	}
	if _, err := old.Authenticator().Authenticate(ctx, "operator", "original operator password"); err != nil {
		t.Fatal("failed adoption retired source", err)
	}
	// The explicit verification above is the only extra password operation.
	beforeVerifies++
	receipt, err := systemstate.AdoptOperator(ctx, backend, config)
	if err != nil || receipt.Kind() != "operator" || receipt.PrincipalID() != principal.ID() || receipt.UserID() <= other.ID || !receipt.At().Equal(at) {
		t.Fatal("adopt operator", err)
	}
	if hasher.hashes.Load() != beforeHashes || hasher.verifies.Load() != beforeVerifies {
		t.Fatal("adoption derived or verified a password")
	}
	afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
	if !reflect.DeepEqual(sessionRows, afterSessions) || !reflect.DeepEqual(auditRows, afterAudit) {
		t.Fatal("adoption changed durable session or audit bytes")
	}
	user, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(receipt.UserID())).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil || !found || !user.Active || !user.Staff || user.Superuser || user.Revision != 1 || user.Username != "operator" || user.PrincipalID != principal.ID() {
		t.Fatal("adopted profile differs", err)
	}
	existing, found, err := models.PermissionObjects.Using(backend).Filter(models.PermissionFields.ID.Exact(known.ID)).OrderBy(models.PermissionFields.ID.Asc()).First(ctx)
	if err != nil || !found || existing.Name != "Existing custom label" {
		t.Fatal("existing permission catalog was overwritten", err)
	}
	opened, err := systemstate.OpenIdentity(ctx, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher, MaxSessions: 32, AuditCapacity: 32})
	if err != nil {
		t.Fatal(err)
	}
	current, err := opened.Authenticator().Authenticate(ctx, "operator", "original operator password")
	if err != nil || current.SessionStamp() != credential.SessionStamp() || !current.Principal().Staff() || current.Principal().Superuser() || !current.Principal().Has("helpdesk.ticket.view") || !current.Principal().Has("helpdesk.ticket.change") {
		t.Fatal("adoption changed authentication or grants", err)
	}
	if _, err := old.Authenticator().Resolve(ctx, principal.ID()); err == nil {
		t.Fatal("old runtime authenticated retired source")
	}
	if _, err := systemstate.OpenExisting(ctx, backend, legacyConfig); err == nil {
		t.Fatal("old startup accepted transitioned history")
	}
	if err := systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{Username: "replacement", Password: "must not hash", CredentialPolicy: policy}); err == nil {
		t.Fatal("old provisioning reactivated")
	}
	if repeated, err := systemstate.AdoptOperator(ctx, backend, config); !errors.Is(err, &systemstate.Error{Code: systemstate.CodeIdentityAlreadyInitialized}) || repeated.UserID() != 0 {
		t.Fatal("adoption replayed source", err)
	}
	reopened, err := systemstate.OpenIdentity(ctx, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher, MaxSessions: 32, AuditCapacity: 32})
	if err != nil {
		t.Fatal(err)
	}
	retained, found, err := reopened.SessionStore().Load(ctx, session.ID())
	if err != nil || !found {
		t.Fatal("adopted runtime lost session", err)
	}
	if value, present := retained.Value("application_value"); !present || value != "keep-me" {
		t.Fatal("session application values were lost")
	}
	history, err := reopened.AuditHistory(ctx, "helpdesk.ticket", 7, 10)
	if err != nil || len(history) != 1 || history[0].ActorID != principal.ID() {
		t.Fatal("adopted runtime lost audit identity", err)
	}
	verifyTransitionHTTP(t, reopened, session.ID(), principal.ID())
	inspected, err := systemstate.InspectIdentityTransition(ctx, backend)
	if err != nil || inspected.UserID() != receipt.UserID() {
		t.Fatal("durable adoption receipt changed", err)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("receipt serialized private source fingerprint", err)
	}
	for _, display := range []string{fmt.Sprint(receipt), fmt.Sprintf("%+v", receipt), fmt.Sprintf("%#v", receipt)} {
		if display != "systemstate.IdentityTransition{redacted}" {
			t.Fatal("receipt diagnostic was not redacted")
		}
	}
}

func RunIdentityBootstrap(t *testing.T, backend TransitionBackend) {
	t.Helper()
	ctx := t.Context()
	applyIdentitySources(t, backend, systemstate.IdentityMigrationSources()...)
	base, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	hasher := &transitionHasher{PasswordHasher: base}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "bootstrap-root", Active: true, Staff: true, Superuser: true})
	if err != nil {
		t.Fatal(err)
	}
	config := systemstate.ProvisionIdentityConfig{Principal: principal, Username: "root", Password: "bootstrap password", PasswordHasher: hasher, DateJoined: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}
	// Commit succeeds but its acknowledgement is lost. The caller receives no
	// success receipt and must read the durable result, never retry implicitly.
	unknown := &transitionFaultBackend{TransitionBackend: backend, unknownCommit: true}
	receipt, err := systemstate.ProvisionIdentity(ctx, unknown, config)
	if !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || receipt.UserID() != 0 || unknown.calls != 1 || hasher.hashes.Load() != 1 {
		t.Fatal("uncertain bootstrap reported success or retried", err)
	}
	inspected, err := systemstate.InspectIdentityTransition(ctx, backend)
	if err != nil || inspected.Kind() != "bootstrap" || inspected.PrincipalID() != principal.ID() {
		t.Fatal("cannot reconcile committed bootstrap", err)
	}
	if again, err := systemstate.ProvisionIdentity(ctx, backend, config); !errors.Is(err, &systemstate.Error{Code: systemstate.CodeIdentityAlreadyInitialized}) || again.UserID() != 0 || hasher.hashes.Load() != 1 {
		t.Fatal("bootstrap retry derived or wrote another identity", err)
	}
	runtime, err := systemstate.OpenIdentity(ctx, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher})
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := runtime.Authenticator().Authenticate(ctx, "root", "bootstrap password")
	if err != nil || !authenticated.Principal().Staff() || !authenticated.Principal().Superuser() || !authenticated.Principal().Has("unregistered.permission") {
		t.Fatal("bootstrapped identity cannot authenticate", err)
	}
	if authenticated.Principal().Has("not-a-permission") {
		t.Fatal("bootstrap bypassed permission grammar")
	}
}

type transitionFaultBackend struct {
	TransitionBackend
	calls         int
	failRetire    error
	unknownCommit bool
}

func (b *transitionFaultBackend) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	b.calls++
	err := b.TransitionBackend.CoordinatedAtomic(ctx, func(session db.Session) error {
		return callback(transitionFaultSession{Session: session, failure: b.failRetire})
	})
	if err == nil && b.unknownCommit {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Detail: "injected unknown outcome"}
	}
	return err
}

type transitionFaultSession struct {
	db.Session
	failure error
}

func (s transitionFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if s.failure != nil && plan.Table() == "godj_system_credential" {
		return 0, s.failure
	}
	return s.Session.Update(ctx, plan)
}

func snapshotIdentitySystemRows(t *testing.T, reader db.Queryer) ([][]string, [][]string) {
	t.Helper()
	read := func(table string, names []string) [][]string {
		fields := []query.FieldRef{query.NewFieldRef("id", "id", query.FieldInteger, false)}
		for _, name := range names {
			fields = append(fields, query.NewFieldRef(name, name, query.FieldString, false))
		}
		plan := query.NewPlan(table, fields).WithOrderings(query.NewOrdering(fields[0], query.Ascending))
		rows, err := reader.Query(t.Context(), plan)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result [][]string
		for rows.Next() {
			var id int64
			values := make([]string, len(names))
			destinations := []any{&id}
			for index := range values {
				destinations = append(destinations, &values[index])
			}
			if err := rows.Scan(destinations...); err != nil {
				t.Fatal(err)
			}
			result = append(result, append([]string{strconv.FormatInt(id, 10)}, values...))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	return read("godj_system_session", []string{"digest", "payload"}), read("godj_system_audit", strings.Fields("actor_id model object_id action changed_fields display_label"))
}
