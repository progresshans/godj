package accountstoretest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func profileConfig() accountmodel.ProfileConfig[models.User] {
	return accountmodel.ProfileConfig[models.User]{Fields: []string{"username", "first_name", "last_name", "email"},
		ChangePermission: "account.direct", Now: func() time.Time { return joined }}
}
func profileActor(t *testing.T, f *fixture) auth.Credential {
	t.Helper()
	authenticator, err := accountmodel.NewAuthenticator(t.Context(), f.store, f.hasher)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := authenticator.Authenticate(t.Context(), f.user.Username, password)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}
func profileInput(t *testing.T, values map[string]query.Value) orm.ModelInput[models.User] {
	t.Helper()
	input, err := models.UserObjects.Input(values)
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func profileRow(t *testing.T, backend Backend, id int64) models.User {
	t.Helper()
	row, present, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(id)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !present {
		t.Fatal("profile row missing", err)
	}
	return row
}
func profileHistory(t *testing.T, backend Backend, actor auth.Principal, id int64) []accountaudit.Entry {
	t.Helper()
	namespace, _ := actor.CredentialDomain()
	var entries []accountaudit.Entry
	if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
		var err error
		entries, err = accountaudit.HistoryIn(t.Context(), reader, namespace, "godj_identity.user", id, 100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return entries
}
func requireProfileCode(t *testing.T, err error, code accountmodel.ErrorCode) {
	t.Helper()
	classified, ok := err.(*accountmodel.Error)
	if !ok || classified.Code != code {
		t.Fatalf("profile error=%v want direct code=%s", err, code)
	}
}

func profileScalarEdit(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := profileInput(t, map[string]query.Value{"first_name": query.String("Edited")})
	input, err = input.WithTargetKey(query.Integer(f.user.ID))
	if err != nil {
		t.Fatal(err)
	}
	result, err := editor.Update(t.Context(), credential.Principal(), f.user.ID, 1, input)
	if err != nil || result.ID != f.user.ID || result.Revision != 2 || !reflect.DeepEqual(result.ChangedFields, []string{"first_name"}) {
		t.Fatal("profile edit", result, err)
	}
	row := profileRow(t, reopen(t), f.user.ID)
	if row.FirstName != "Edited" || row.Revision != 2 || row.PrincipalID != f.user.PrincipalID || row.EncodedPassword != f.user.EncodedPassword || !row.DateJoined.Equal(joined) || row.LastLogin != nil || row.Active != f.user.Active || row.Staff != f.user.Staff || row.Superuser != f.user.Superuser {
		t.Fatal("profile altered hidden account state")
	}
	entries := profileHistory(t, reopen(t), credential.Principal(), f.user.ID)
	if len(entries) != 1 || entries[0].ActorID() != credential.Principal().ID() || !reflect.DeepEqual(entries[0].ChangedFields(), []string{"first_name"}) || !entries[0].At().Equal(joined) {
		t.Fatal("profile audit missing or contains revision/value", entries)
	}
	result.ChangedFields[0] = "forged"
	result, err = editor.Update(t.Context(), credential.Principal(), f.user.ID, 2, input)
	if err != nil || result.Revision != 2 || len(result.ChangedFields) != 0 || len(profileHistory(t, backend, credential.Principal(), f.user.ID)) != 1 {
		t.Fatal("no-op changed revision/audit", result, err)
	}
	if result, err = editor.Update(t.Context(), credential.Principal(), f.user.ID, 1, input); result.Revision != 0 {
		t.Fatal("stale input published result")
	} else {
		requireProfileCode(t, err, accountmodel.CodeConflict)
	}
	if result, err = editor.Update(t.Context(), credential.Principal(), -999, 1, profileInput(t, nil)); result.Revision != 0 {
		t.Fatal("missing target published result")
	} else {
		requireProfileCode(t, err, accountmodel.CodeNotFound)
	}
	if _, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("other", "taken", f.user.EncodedPassword, joined)); err != nil {
		t.Fatal(err)
	}
	duplicate := profileInput(t, map[string]query.Value{"username": query.String("taken"), "first_name": query.String("must-rollback")})
	if result, err = editor.Update(t.Context(), credential.Principal(), f.user.ID, 2, duplicate); result.Revision != 0 {
		t.Fatal("duplicate published result")
	} else if failures, rejected := validation.Rejected(err); !rejected || failures.ByField("username").Empty() {
		t.Fatal("unique refusal lost typed diagnostics", err)
	}
	if actual := profileRow(t, backend, f.user.ID); actual.Username != "alice" || actual.FirstName != "Edited" || actual.Revision != 2 {
		t.Fatal("failed edit changed target")
	}
	// Selected write NFKC does not change the selected exact authentication
	// policy; a normalized no-op still preserves credentials and audit.
	result, err = editor.Update(t.Context(), credential.Principal(), f.user.ID, 2, profileInput(t, map[string]query.Value{"username": query.String("ａｌｉｃｅ")}))
	if err != nil || result.Revision != 2 || len(result.ChangedFields) != 0 {
		t.Fatal("write normalization changed no-op", err)
	}
	fresh, found, err := f.store.CredentialByID(t.Context(), f.user.PrincipalID)
	if err != nil || !found || !fresh.MatchesSessionStamp(credential.SessionStamp()) || !reflect.DeepEqual(fresh.Principal().Permissions(), credential.Principal().Permissions()) {
		t.Fatal("profile edit rotated credential or grants", err)
	}
}

func profileDomainAndRevocation(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := profileInput(t, map[string]query.Value{"first_name": query.String("old-domain")})
	if _, err := editor.Update(t.Context(), credential.Principal(), f.user.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &f.direct); err != nil {
		t.Fatal(err)
	}
	if result, err := editor.Update(t.Context(), credential.Principal(), f.user.ID, 2, input); result.Revision != 0 {
		t.Fatal("revoked actor published no-op")
	} else {
		requireProfileCode(t, err, accountmodel.CodePermission)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.user.ID, f.grants[0].ID)); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := f.binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	evidence := sha256.Sum256([]byte("profile domain transition negative control"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(evidence[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		_, err := domain.TransitionIn(t.Context(), tx, f.ownership, change, joined)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	newInput := profileInput(t, map[string]query.Value{"first_name": query.String("new-domain")})
	if result, err := editor.Update(t.Context(), credential.Principal(), f.user.ID, 2, newInput); result.Revision != 0 {
		t.Fatal("old-domain actor published result")
	} else {
		requireProfileCode(t, err, accountmodel.CodePermission)
	}
	if actual := profileRow(t, reopen(t), f.user.ID); actual.Revision != 2 || actual.FirstName != "old-domain" {
		t.Fatal("denied actor changed target")
	}
	fresh := profileActor(t, f)
	if _, err := editor.Update(t.Context(), fresh.Principal(), f.user.ID, 2, newInput); err != nil {
		t.Fatal(err)
	}
	oldHistory, newHistory := profileHistory(t, reopen(t), credential.Principal(), f.user.ID), profileHistory(t, reopen(t), fresh.Principal(), f.user.ID)
	if len(oldHistory) != 1 || len(newHistory) != 1 || oldHistory[0].Sequence() == newHistory[0].Sequence() || oldHistory[0].Domain().Identifier() == newHistory[0].Domain().Identifier() {
		t.Fatal("profile inherited old-domain audit")
	}
}

func profileWriteNormalization(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	revision := int64(1)
	for _, pair := range [][2]string{
		{" Mixed@EXAMPLE.COM ", "Mixed@example.com"},
		{"Local@İ.ΟΣ", "Local@i\u0307.ος"},
		{"Mixed@\u1c89.\ua7cb", "Mixed@\u1c8a.\u0264"},
	} {
		change, err := editor.Update(t.Context(), credential.Principal(), f.user.ID, revision, profileInput(t, map[string]query.Value{"email": query.String(pair[0])}))
		revision++
		if err != nil || change.Revision != revision || !reflect.DeepEqual(change.ChangedFields, []string{"email"}) {
			t.Fatal("selected email normalization", change, err)
		}
		if got := profileRow(t, reopen(t), f.user.ID); got.Email != pair[1] || got.Revision != revision {
			t.Fatal("email write did not preserve local part and pinned Unicode domain policy", got.Email)
		}
	}
	role, err := f.binding.Role()
	if err != nil {
		t.Fatal(err)
	}
	role.WriteEmailNormalization, role.WriteLoginNormalization = ir.AccountExact, ir.AccountExact
	exactBinding, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := exactBinding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	evidence := sha256.Sum256([]byte("explicit exact email/login policy selection"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(evidence[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		_, err := domain.TransitionIn(t.Context(), tx, f.ownership, change, joined)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	exactStore, err := accountmodel.NewCredentialStore(backend, exactBinding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	fresh, found, err := exactStore.CredentialByID(t.Context(), f.user.PrincipalID)
	if err != nil || !found {
		t.Fatal(err)
	}
	input := profileInput(t, map[string]query.Value{"username": query.String("ａｌｉｃｅ"), "email": query.String("Mixed@EXAMPLE.TEST")})
	if result, err := editor.Update(t.Context(), fresh.Principal(), f.user.ID, revision, input); result.Revision != 0 || !errors.Is(err, domain.ErrMismatch) {
		t.Fatal("old selected policy remained writable", err)
	}
	exactEditor, err := accountmodel.NewProfileEditor(exactStore, auth.PrincipalAuthorizer{}, profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := exactEditor.Update(t.Context(), fresh.Principal(), f.user.ID, revision, input)
	if err != nil || result.Revision != revision+1 {
		t.Fatal("explicit exact write policy failed", err)
	}
	if got := profileRow(t, reopen(t), f.user.ID); got.Username != "ａｌｉｃｅ" || got.Email != "Mixed@EXAMPLE.TEST" {
		t.Fatal("exact selected policy was implicitly normalized")
	}
}

type profileFaultBackend struct {
	Backend
	mode   string
	fault  error
	cancel context.CancelFunc
	calls  int
}

func (backend *profileFaultBackend) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	backend.calls++
	if backend.mode == "skipped" {
		return nil
	}
	err := backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		if backend.mode == "nil_session" {
			return callback(nil)
		}
		if backend.mode == "root_session" {
			return callback(backend.Backend)
		}
		if backend.mode == "audit_failure" || backend.mode == "write_failure" {
			session = &profileFaultSession{Session: session, mode: backend.mode, fault: backend.fault}
		}
		err := callback(session)
		if backend.mode == "repeated" {
			return errors.Join(err, callback(session))
		}
		if err != nil {
			return err
		}
		if backend.mode == "unknown_rollback" {
			return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
		}
		if backend.mode == "cancel_before_commit" {
			backend.cancel()
		}
		return nil
	})
	if backend.mode == "swallowed_rejection" {
		return nil
	}
	if backend.mode == "cleanup_failure" {
		return errors.Join(err, backend.fault)
	}
	if err != nil {
		return err
	}
	if backend.mode == "unknown_commit" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	if backend.mode == "post_commit_cancel" {
		backend.cancel()
	}
	return nil
}

type profileFaultSession struct {
	db.Session
	mode  string
	fault error
}

func (session *profileFaultSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session *profileFaultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := session.Session.Insert(ctx, plan)
	if err == nil && session.mode == "audit_failure" && plan.Table() == "godj_account_audit_event" {
		return 0, session.fault
	}
	return key, err
}
func (session *profileFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	count, err := session.Session.Update(ctx, plan)
	if err == nil && session.mode == "write_failure" && plan.Table() == "godj_identity_user" {
		return 0, session.fault
	}
	return count, err
}

func profileCommitFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	for _, mode := range []string{"skipped", "nil_session", "root_session", "audit_failure", "write_failure", "repeated", "validator_rejection", "validator_failure", "swallowed_rejection", "cleanup_failure", "invalid_clock", "cancel_before_commit", "unknown_rollback", "unknown_commit", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private-profile-fault")
			owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
			config := profileConfig()
			if mode == "validator_rejection" || mode == "swallowed_rejection" || mode == "cleanup_failure" || mode == "validator_failure" {
				config.Validate = func(context.Context, db.Queryer, models.User, models.User) (validation.Errors, error) {
					diagnostics := validation.NewErrors(validation.New("first_name", "rejected"))
					if mode == "validator_failure" {
						return diagnostics, fault
					}
					return diagnostics, nil
				}
			}
			if mode == "invalid_clock" {
				config.Now = func() time.Time { return time.Time{} }
			}
			editor, err := project.NewAccountProfileEditor(owner, auth.PrincipalAuthorizer{}, config)
			if err != nil {
				t.Fatal(err)
			}
			before := profileRow(t, backend, f.user.ID)
			auditBefore := profileHistory(t, backend, credential.Principal(), f.user.ID)
			result, err := editor.Update(ctx, credential.Principal(), f.user.ID, before.Revision, profileInput(t, map[string]query.Value{"first_name": query.String(mode)}))
			if owner.calls != 1 {
				t.Fatal("profile work retried or skipped configured owner", owner.calls)
			}
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if mode == "post_commit_cancel" {
				if err != nil || result.Revision != before.Revision+1 {
					t.Fatal("late cancellation erased confirmed commit", err)
				}
			} else {
				if err == nil || result.Revision != 0 || result.ID != 0 || result.ChangedFields != nil {
					t.Fatal("failed profile published result", result, err)
				}
				if mode == "validator_rejection" {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("first_name").Empty() {
						t.Fatal("confirmed validation rollback lost diagnostics", err)
					}
				} else {
					if _, rejected := validation.Rejected(err); rejected {
						t.Fatal("execution failure became client validation")
					}
					code := accountmodel.CodePersistence
					if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || mode == "unknown_commit" || mode == "unknown_rollback" {
						code = accountmodel.CodeOutcomeUnknown
					}
					requireProfileCode(t, err, code)
				}
				if strings.Contains(fmt.Sprintf("%+v", err), fault.Error()) {
					t.Fatal("private profile failure exposed")
				}
				if (mode == "audit_failure" || mode == "write_failure" || mode == "validator_failure" || mode == "cleanup_failure") && !errors.Is(err, fault) {
					t.Fatal("underlying failure lost", err)
				}
			}
			after := profileRow(t, reopen(t), f.user.ID)
			auditAfter := profileHistory(t, reopen(t), credential.Principal(), f.user.ID)
			if committed {
				if after.Revision != before.Revision+1 || after.FirstName != mode || len(auditAfter) != len(auditBefore)+1 {
					t.Fatal("committed profile/audit mismatch")
				}
			} else if !reflect.DeepEqual(after, before) || len(auditAfter) != len(auditBefore) {
				t.Fatal("rollback did not preserve profile and audit")
			}
		})
	}
}

func profileConcurrentRevision(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	writers := []Backend{backend, reopen(t)}
	editors := make([]*accountmodel.ProfileEditor[models.User], 2)
	for i, writer := range writers {
		var err error
		editors[i], err = project.NewAccountProfileEditor(writer, auth.PrincipalAuthorizer{}, profileConfig())
		if err != nil {
			t.Fatal(err)
		}
	}
	inputs := []orm.ModelInput[models.User]{profileInput(t, map[string]query.Value{"first_name": query.String("first")}), profileInput(t, map[string]query.Value{"first_name": query.String("second")})}
	var wait sync.WaitGroup
	start := make(chan struct{})
	results := make([]accountmodel.ProfileChange, 2)
	failures := make([]error, 2)
	for i := range editors {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			results[i], failures[i] = editors[i].Update(t.Context(), credential.Principal(), f.user.ID, 1, inputs[i])
		}(i)
	}
	close(start)
	wait.Wait()
	success := 0
	for i, err := range failures {
		if err == nil {
			success++
			if results[i].Revision != 2 {
				t.Fatal("concurrent result revision")
			}
		} else {
			requireProfileCode(t, err, accountmodel.CodeConflict)
			if results[i].Revision != 0 {
				t.Fatal("conflicting result published")
			}
		}
	}
	row := profileRow(t, reopen(t), f.user.ID)
	if success != 1 || row.Revision != 2 || len(profileHistory(t, reopen(t), credential.Principal(), f.user.ID)) != 1 {
		t.Fatal("concurrent profile writes did not share revision/audit fence", success, row.Revision)
	}
}

func profileTargetIntegrity(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	target, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("target-principal", "target", f.user.EncodedPassword, joined))
	if err != nil {
		t.Fatal(err)
	}
	malformed, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("malformed", "Corrupt target grant"))
	if err != nil {
		t.Fatal(err)
	}
	link, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(target.ID, malformed.ID))
	if err != nil {
		t.Fatal(err)
	}
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := profileInput(t, map[string]query.Value{"first_name": query.String("valid edit")})
	result, err := editor.Update(t.Context(), credential.Principal(), target.ID, 1, input)
	requireProfileCode(t, err, accountmodel.CodePersistence)
	if result.Revision != 0 || !reflect.DeepEqual(profileRow(t, reopen(t), target.ID), target) || len(profileHistory(t, backend, credential.Principal(), target.ID)) != 0 {
		t.Fatal("corrupt target grants admitted profile/audit mutation")
	}
	if _, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &link); err != nil {
		t.Fatal(err)
	}
	if result, err := editor.Update(t.Context(), credential.Principal(), target.ID, 1, input); err != nil || result.Revision != 2 {
		t.Fatal("valid target control failed", err)
	}
}
