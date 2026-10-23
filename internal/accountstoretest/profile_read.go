package accountstoretest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func profileReadConfig() accountmodel.ProfileReadConfig {
	return accountmodel.ProfileReadConfig{Fields: []string{"username", "first_name", "email", "last_login"}, ViewPermission: "account.direct"}
}

func profileReader(t *testing.T, owner db.SnapshotReader, f *fixture, overlay auth.Authorizer, config accountmodel.ProfileReadConfig) *accountmodel.ProfileReader[models.User] {
	t.Helper()
	store, err := accountmodel.NewCredentialStore(owner, f.binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := accountmodel.NewProfileReader(store, overlay, config)
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func requireReadProfile(t *testing.T, value accountmodel.Profile[models.User], id, revision int64, name string) {
	t.Helper()
	if value.ID() != id || value.Revision() != revision {
		t.Fatal("profile identity/revision differs", value.ID(), value.Revision())
	}
	field, present := value.View().Get("first_name")
	if !present || !field.Equal(query.String(name)) {
		t.Fatal("profile selected value differs")
	}
	for _, hidden := range []string{"encoded_password", "principal_id", "revision", "date_joined", "active", "staff", "superuser", "groups", "permissions"} {
		if _, loaded := value.View().Get(hidden); loaded {
			t.Fatal("profile implicitly exposed field", hidden)
		}
	}
}

func profileReadSelectionAndHistory(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	config := profileReadConfig()
	reader, err := project.NewAccountProfileReader(backend, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Fields[0] = "encoded_password"
	before, err := reader.Get(t.Context(), actor, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireReadProfile(t, before, f.user.ID, 1, "")
	beforeData := before.View().Values()
	beforeData["first_name"] = query.String("outside mutation")
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"First private profile", "Second private profile"} {
		if _, err := editor.Update(t.Context(), actor, f.user.ID, int64(index+1), profileInput(t, map[string]query.Value{"first_name": query.String(name)})); err != nil {
			t.Fatal(err)
		}
	}
	current, err := reader.Get(t.Context(), actor, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireReadProfile(t, current, f.user.ID, 3, "Second private profile")
	requireReadProfile(t, before, f.user.ID, 1, "")
	metadataReader := profileReader(t, backend, f, auth.PrincipalAuthorizer{}, accountmodel.ProfileReadConfig{
		Fields: []string{"id", "revision", "date_joined", "active", "staff", "superuser"}, ViewPermission: "account.direct",
	})
	metadata, err := metadataReader.Get(t.Context(), actor, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for field, expected := range map[string]query.Value{"id": query.Integer(f.user.ID), "revision": query.Integer(3), "date_joined": query.DateTime(joined), "active": query.Boolean(true), "staff": query.Boolean(false), "superuser": query.Boolean(false)} {
		if value, loaded := metadata.View().Get(field); !loaded || !value.Equal(expected) {
			t.Fatal("explicit server-owned read selection differs", field)
		}
	}
	entries, err := reader.History(t.Context(), actor, f.user.ID, 1)
	if err != nil || len(entries) != 1 || entries[0].ObjectID() != f.user.ID || entries[0].Sequence() <= 0 || !reflect.DeepEqual(entries[0].ChangedFields(), []string{"first_name"}) {
		t.Fatal("bounded current-domain history", err)
	}
	all, err := reader.History(t.Context(), actor, f.user.ID, 100)
	if err != nil || len(all) != 2 || all[0].Sequence() >= all[1].Sequence() || all[1].Sequence() != entries[0].Sequence() {
		t.Fatal("profile history order", err)
	}
	for _, value := range []any{current, &current, current.View()} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%f", "%o", "%p", "%w", "%020d", "%#[1]v"} {
			printed := fmt.Sprintf(format, value)
			if strings.Contains(printed, "private profile") || strings.Contains(printed, f.user.EncodedPassword) || strings.Contains(printed, f.user.PrincipalID) {
				t.Fatal("profile formatting disclosed data", format)
			}
		}
	}
	encoded, err := json.Marshal(current)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("profile bypassed declared output", err)
	}
	restarted := profileReader(t, reopen(t), f, auth.PrincipalAuthorizer{}, profileReadConfig())
	got, err := restarted.Get(t.Context(), actor, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireReadProfile(t, got, f.user.ID, 3, "Second private profile")
	if entries, err := restarted.History(t.Context(), actor, f.user.ID, 100); err != nil || len(entries) != 2 {
		t.Fatal("reopened profile history", err)
	}
	if value, err := reader.Get(t.Context(), actor, 9999); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFound}) || value.Revision() != 0 {
		t.Fatal("missing profile published data", err)
	}
	if entries, err := reader.History(t.Context(), actor, 9999, 100); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFound}) || entries != nil {
		t.Fatal("missing profile published history", err)
	}
	queries := 0
	counting := snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
		return backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
			return observe(&profileReadProbe{Queryer: scope, validator: scope.(db.SessionValidator), queries: &queries})
		})
	})
	counted := profileReader(t, counting, f, auth.PrincipalAuthorizer{}, profileReadConfig())
	page, err := counted.List(t.Context(), actor, 0, 100)
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal("initial profile page", err)
	}
	oneRowQueries := queries
	for index := range 3 {
		name := fmt.Sprintf("profile-%d", index)
		if _, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate(name, name, f.user.EncodedPassword, joined).WithFirstName(name)); err != nil {
			t.Fatal(err)
		}
	}
	queries = 0
	page, err = counted.List(t.Context(), actor, 0, 100)
	if err != nil || page.Total != 4 || len(page.Items) != 4 || queries != oneRowQueries {
		t.Fatal("listing added per-target reads", page.Total, len(page.Items), queries, oneRowQueries, err)
	}
	for index := 1; index < len(page.Items); index++ {
		if page.Items[index-1].ID() >= page.Items[index].ID() {
			t.Fatal("profile page is not in key order")
		}
	}
	second, err := reader.List(t.Context(), actor, 2, 1)
	if err != nil || second.Total != 4 || len(second.Items) != 1 || second.Items[0].ID() != page.Items[2].ID() {
		t.Fatal("bounded profile offset", err)
	}
	empty, err := reader.List(t.Context(), actor, 9, 2)
	if err != nil || empty.Total != 4 || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatal("past-end profile page lost total", err)
	}
}

func profileReadAuthorityAndDomain(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	writer := reopen(t)
	config := profileReadConfig()
	config.ChangePermission, config.AdditionalPermissions = "account.team", []auth.Permission{"account.shared"}
	var observed []auth.Permission
	deniedView := overlayFunc(func(_ context.Context, current auth.Principal, permission auth.Permission) (bool, error) {
		if current.ID() != actor.ID() {
			return false, errors.New("wrong current actor")
		}
		observed = append(observed, permission)
		return permission != "account.direct", nil
	})
	reader := profileReader(t, backend, f, deniedView, config)
	config.AdditionalPermissions[0] = "account.missing"
	if _, err := reader.Get(t.Context(), actor, f.user.ID); err != nil || !reflect.DeepEqual(observed, []auth.Permission{"account.direct", "account.team", "account.shared"}) {
		t.Fatal("read/change alternative or owned conjunction changed", observed, err)
	}
	missingAdditional := profileReader(t, backend, f, auth.PrincipalAuthorizer{}, accountmodel.ProfileReadConfig{
		Fields: profileReadConfig().Fields, ViewPermission: "account.direct", ChangePermission: "account.team", AdditionalPermissions: []auth.Permission{"account.missing"},
	})
	if value, err := missingAdditional.Get(t.Context(), actor, f.user.ID); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || value.Revision() != 0 {
		t.Fatal("read alternative bypassed required additional permission", err)
	}
	fault := errors.New("private read overlay failure")
	for _, accepted := range []bool{false, true} {
		calls := 0
		failing := profileReader(t, backend, f, overlayFunc(func(context.Context, auth.Principal, auth.Permission) (bool, error) {
			calls++
			return accepted, errors.Join(fault, &accountmodel.Error{Code: accountmodel.CodePermission})
		}), accountmodel.ProfileReadConfig{Fields: profileReadConfig().Fields, ViewPermission: "account.direct", ChangePermission: "account.team"})
		if value, err := failing.Get(t.Context(), actor, f.user.ID); !errors.Is(err, fault) || calls != 1 || value.Revision() != 0 || strings.Contains(fmt.Sprintf("%+v", err), "private") {
			t.Fatal("overlay failure fell back or exposed data", calls, err)
		}
	}
	strict := profileReader(t, backend, f, auth.PrincipalAuthorizer{}, profileReadConfig())
	if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), writer, &f.direct); err != nil || count != 1 {
		t.Fatal("revoke profile view grant", err)
	}
	if value, err := strict.Get(t.Context(), actor, f.user.ID); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || value.Revision() != 0 {
		t.Fatal("old grant admitted profile read", err)
	}
	if page, err := strict.List(t.Context(), actor, 0, 100); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || page.Items != nil {
		t.Fatal("old grant admitted profile list", err)
	}
	if entries, err := strict.History(t.Context(), actor, f.user.ID, 100); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || entries != nil {
		t.Fatal("old grant admitted profile history", err)
	}
	if _, err := reader.Get(t.Context(), actor, f.user.ID); err != nil {
		t.Fatal("current change grant did not admit read", err)
	}
	if _, err := models.UserObjects.Patch(t.Context(), writer, f.user, models.UserPatch{}.WithActive(false).WithRevision(2)); err != nil {
		t.Fatal(err)
	}
	if value, err := reader.Get(t.Context(), actor, f.user.ID); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || value.Revision() != 0 {
		t.Fatal("inactive current actor inherited old admission", err)
	}
	if _, err := models.UserObjects.Patch(t.Context(), writer, f.user, models.UserPatch{}.WithActive(true).WithRevision(3)); err != nil {
		t.Fatal(err)
	}
	restored, err := models.UserPermissionsLinkObjects.Create(t.Context(), writer, models.NewUserPermissionsLinkCreate(f.user.ID, f.grants[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	f.direct = restored
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editor.Update(t.Context(), actor, f.user.ID, 3, profileInput(t, map[string]query.Value{"first_name": query.String("old domain profile")})); err != nil {
		t.Fatal(err)
	}
	fingerprint, _ := f.binding.RoleFingerprint()
	digest := sha256.Sum256([]byte("profile read ownership transition"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		_, err := domain.TransitionIn(t.Context(), tx, f.ownership, change, joined.Add(time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if value, err := strict.Get(t.Context(), actor, f.user.ID); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || value.Revision() != 0 {
		t.Fatal("old-domain actor inherited target", err)
	}
	fresh := profileActor(t, f).Principal()
	entries, err := strict.History(t.Context(), fresh, f.user.ID, 100)
	if err != nil || len(entries) != 0 {
		t.Fatal("old domain history attached to new owner", err)
	}
	if _, err := editor.Update(t.Context(), fresh, f.user.ID, 4, profileInput(t, map[string]query.Value{"first_name": query.String("new domain profile")})); err != nil {
		t.Fatal(err)
	}
	entries, err = strict.History(t.Context(), fresh, f.user.ID, 100)
	namespace, _ := fresh.CredentialDomain()
	if err != nil || len(entries) != 1 || entries[0].Domain().Identifier() != namespace.Identifier() {
		t.Fatal("new-domain profile history missing", err)
	}
	unbound, err := auth.NewPrincipal(auth.PrincipalConfig{ID: fresh.ID(), Active: true, Permissions: fresh.Permissions()})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := strict.Get(t.Context(), unbound, f.user.ID); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || value.Revision() != 0 {
		t.Fatal("domain-less actor inherited target", err)
	}
	current, err := domain.Read(t.Context(), writer, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	foreign := sha256.Sum256([]byte("different stored account role selection"))
	change, err = domain.Prepare(t.Context(), hex.EncodeToString(foreign[:]), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		_, err := domain.TransitionIn(t.Context(), tx, current, change, joined.Add(2*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if value, err := strict.Get(t.Context(), fresh, f.user.ID); !errors.Is(err, domain.ErrMismatch) || value.Revision() != 0 {
		t.Fatal("foreign stored role supplied a profile", err)
	}
	if page, err := strict.List(t.Context(), fresh, 0, 100); !errors.Is(err, domain.ErrMismatch) || page.Items != nil {
		t.Fatal("foreign stored role supplied a profile page", err)
	}
	if entries, err := strict.History(t.Context(), fresh, f.user.ID, 100); !errors.Is(err, domain.ErrMismatch) || entries != nil {
		t.Fatal("foreign stored role supplied account history", err)
	}
}

type profileReadProbe struct {
	db.Queryer
	validator db.SessionValidator
	queries   *int
	fault     error
	closeFail bool
}

func (probe *profileReadProbe) ValidateSession(ctx context.Context) error {
	return probe.validator.ValidateSession(ctx)
}
func (probe *profileReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if probe.queries != nil {
		*probe.queries += 1
	}
	if probe.fault != nil && !probe.closeFail {
		return nil, probe.fault
	}
	rows, err := probe.Queryer.Query(ctx, plan)
	if rows != nil && probe.closeFail {
		return &profileReadFaultRows{Rows: rows, fault: probe.fault}, err
	}
	return rows, err
}

type profileReadFaultRows struct {
	db.Rows
	fault error
}

func (rows *profileReadFaultRows) Close() error { return errors.Join(rows.Rows.Close(), rows.fault) }

func profileReadFailures(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	for _, mode := range []string{"skipped", "nil", "typed_nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "cancel", "denial_cleanup", "missing_cleanup"} {
		t.Run(mode, func(t *testing.T) {
			for _, operation := range []string{"get", "list", "history"} {
				if mode == "missing_cleanup" && operation == "list" {
					continue // A list has a page, not an object-not-found outcome.
				}
				t.Run(operation, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					fault := errors.New("private profile read cleanup details")
					owner := snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
						switch mode {
						case "skipped":
							return nil
						case "nil":
							return observe(nil)
						case "typed_nil":
							return observe((*profileReadProbe)(nil))
						case "root":
							return observe(backend)
						}
						err := backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
							probe := &profileReadProbe{Queryer: scope, validator: scope.(db.SessionValidator)}
							if mode == "query" || mode == "close" || mode == "swallowed" {
								probe.fault, probe.closeFail = fault, mode == "close"
							}
							err := observe(probe)
							if mode == "swallowed" {
								return nil
							}
							if mode == "repeated" {
								err = errors.Join(err, observe(probe))
							}
							return err
						})
						if mode == "cleanup" || mode == "denial_cleanup" || mode == "missing_cleanup" {
							return errors.Join(err, fault)
						}
						if mode == "cancel" {
							cancel()
						}
						return err
					})
					config := profileReadConfig()
					config.Fields = append(config.Fields, "groups", "permissions")
					if mode == "denial_cleanup" {
						config.ViewPermission = "account.absent"
					}
					reader := profileReader(t, owner, f, auth.PrincipalAuthorizer{}, config)
					id := f.user.ID
					if mode == "missing_cleanup" {
						id = 9999
					}
					var err error
					switch operation {
					case "get":
						value, failure := reader.Get(ctx, actor, id)
						err = failure
						if value.Revision() != 0 || value.View().Values() != nil || value.View().Collections() != nil {
							t.Fatal("failed read retained profile")
						}
					case "list":
						value, failure := reader.List(ctx, actor, 0, 100)
						err = failure
						if value.Items != nil || value.Total != 0 {
							t.Fatal("failed read retained page")
						}
					case "history":
						value, failure := reader.History(ctx, actor, id, 100)
						err = failure
						if value != nil {
							t.Fatal("failed read retained history")
						}
					}
					requireProfileCode(t, err, accountmodel.CodePersistence)
					if strings.Contains(fmt.Sprintf("%+v", err), "private") {
						t.Fatal("read diagnostics disclosed private cause")
					}
					if mode == "query" || mode == "close" || mode == "swallowed" || strings.HasSuffix(mode, "cleanup") {
						if !errors.Is(err, fault) {
							t.Fatal("read cause was lost", err)
						}
					}
					if mode == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal("read cancellation lost")
					}
				})
			}
		})
	}
}

type profileCoherentReader struct {
	db.Queryer
	validator db.SessionValidator
	once      *sync.Once
	after     func() error
}

func (reader *profileCoherentReader) ValidateSession(ctx context.Context) error {
	return reader.validator.ValidateSession(ctx)
}
func (reader *profileCoherentReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := reader.Queryer.Query(ctx, plan)
	if rows != nil {
		return &profileCoherentRows{Rows: rows, once: reader.once, after: reader.after}, err
	}
	return rows, err
}

type profileCoherentRows struct {
	db.Rows
	once  *sync.Once
	after func() error
}

func (rows *profileCoherentRows) Close() error {
	err := rows.Rows.Close()
	if err == nil {
		rows.once.Do(func() { err = rows.after() })
	}
	return err
}

func profileReadCoherentPage(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	writer := reopen(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	observed, finished := make(chan struct{}), make(chan struct{})
	committed := make(chan error, 1)
	go func() {
		defer close(finished)
		select {
		case <-observed:
		case <-ctx.Done():
			committed <- ctx.Err()
			return
		}
		committed <- writer.CoordinatedAtomic(ctx, func(tx db.Session) error {
			if _, err := models.UserObjects.Patch(ctx, tx, f.user, models.UserPatch{}.WithFirstName("after snapshot").WithRevision(2)); err != nil {
				return err
			}
			if _, err := models.UserPermissionsLinkObjects.Delete(ctx, tx, &f.direct); err != nil {
				return err
			}
			_, err := models.UserObjects.Create(ctx, tx, models.NewUserCreate("profile-second", "profile-second", f.user.EncodedPassword, joined))
			return err
		})
	}()
	defer func() { cancel(); <-finished }()
	owner := snapshotFunc(func(ctx context.Context, callback func(db.Queryer) error) error {
		return backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
			return callback(&profileCoherentReader{Queryer: scope, validator: scope.(db.SessionValidator), once: &sync.Once{}, after: func() error {
				close(observed)
				select {
				case err := <-committed:
					return err
				case <-ctx.Done():
					return ctx.Err()
				}
			}})
		})
	})
	reader := profileReader(t, owner, f, auth.PrincipalAuthorizer{}, profileReadConfig())
	page, err := reader.List(ctx, actor, 0, 100)
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal("authorization/count/page mixed concurrent commits", page.Total, len(page.Items), err)
	}
	requireReadProfile(t, page.Items[0], f.user.ID, 1, "")
	strict := profileReader(t, backend, f, auth.PrincipalAuthorizer{}, profileReadConfig())
	if page, err := strict.List(t.Context(), actor, 0, 100); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || page.Items != nil {
		t.Fatal("next read retained revoked grant", err)
	}
	config := profileReadConfig()
	config.ViewPermission = "account.team"
	fresh := profileReader(t, backend, f, auth.PrincipalAuthorizer{}, config)
	page, err = fresh.List(t.Context(), actor, 0, 100)
	if err != nil || page.Total != 2 || len(page.Items) != 2 {
		t.Fatal("new snapshot missed committed page", err)
	}
	requireReadProfile(t, page.Items[0], f.user.ID, 2, "after snapshot")
}

func profileReadTargetIntegrity(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	reader := profileReader(t, backend, f, auth.PrincipalAuthorizer{}, profileReadConfig())
	target, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("read-target", "read-target", f.user.EncodedPassword, joined))
	if err != nil {
		t.Fatal(err)
	}
	broken, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("bad grant", "Broken"))
	if err != nil {
		t.Fatal(err)
	}
	link, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(target.ID, broken.ID))
	if err != nil {
		t.Fatal(err)
	}
	if value, err := reader.Get(t.Context(), actor, target.ID); err == nil || value.Revision() != 0 {
		t.Fatal("corrupt target grants admitted detail")
	}
	if entries, err := reader.History(t.Context(), actor, target.ID, 100); err == nil || entries != nil {
		t.Fatal("corrupt target grants admitted history")
	}
	// A list deliberately has no target-grant projection or per-row authority
	// reads. The valid caller and scalar target rows remain readable there.
	if page, err := reader.List(t.Context(), actor, 0, 100); err != nil || page.Total != 2 || len(page.Items) != 2 {
		t.Fatal("scalar list tried to load every target grant", err)
	}
	if _, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &link); err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithRevision(0)); err != nil {
		t.Fatal(err)
	}
	if page, err := reader.List(t.Context(), actor, 0, 100); err == nil || page.Items != nil || page.Total != 0 {
		t.Fatal("partial profile list escaped corrupt scalar state")
	}
	if _, err := models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithRevision(1)); err != nil {
		t.Fatal(err)
	}
	if value, err := reader.Get(t.Context(), actor, target.ID); err != nil || value.ID() != target.ID {
		t.Fatal("restored target is not readable", err)
	}
}
