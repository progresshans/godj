package accountstoretest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/sessionwire"
	"github.com/progresshans/godj/sessions"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
)

var nativeLoginInstant = time.Date(2026, 9, 27, 1, 2, 3, 123456000, time.UTC)

const loginIdle = 10 * time.Minute
const loginLifetime = 2 * time.Hour

// Observe real entropy before persistence, including when the caller cannot
// receive a bearer after an unknown commit. Expected rows never obtain their
// principal, stamp, timestamps or payload from the post-operation database.
type loginEntropy struct {
	mu  sync.Mutex
	ids []sessions.ID
}

func (source *loginEntropy) Read(output []byte) (int, error) {
	if len(output) != 32 {
		return 0, errors.New("unexpected selected login entropy request")
	}
	n, err := rand.Read(output)
	if err != nil {
		return n, err
	}
	id, err := sessions.ParseID(base64.RawURLEncoding.EncodeToString(output))
	if err != nil {
		return n, err
	}
	source.mu.Lock()
	source.ids = append(source.ids, id)
	source.mu.Unlock()
	return n, nil
}
func (source *loginEntropy) count() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return len(source.ids)
}
func (source *loginEntropy) one(t *testing.T, before int) sessions.ID {
	t.Helper()
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.ids) != before+1 {
		t.Fatal("login did not prepare exactly one real bearer")
	}
	return source.ids[before]
}

type loginFixture struct {
	*userReferenceFixture
	backend        Backend
	owner          *userReferenceOwner
	target         models.User
	view           models.Permission
	credential     auth.Credential
	targetSessions []sessions.Record
	instant        atomic.Int64
	entropy        loginEntropy
}

func (f *loginFixture) clock() time.Time { return time.Unix(0, f.instant.Load()).UTC() }
func (f *loginFixture) config(t *testing.T) accountmodel.RuntimeConfig[models.User, models.Group, models.Permission] {
	t.Helper()
	config := userReferenceConfig(t)
	config.SessionStorage.MaxRecords = 512
	config.Sessions.Clock, config.Sessions.Random = f.clock, &f.entropy
	config.Sessions.IdleTimeout, config.Sessions.AbsoluteLifetime = loginIdle, loginLifetime
	return config
}

func provisionLogin(t *testing.T, backend Backend, staff bool) *loginFixture {
	t.Helper()
	f := &loginFixture{userReferenceFixture: provisionUserReference(t, backend), backend: backend}
	f.instant.Store(nativeLoginInstant.UnixNano())
	f.owner = newUserReferenceOwner(t, backend, f.userReferenceFixture, f.config(t))
	f.runtime = f.owner.runtime
	var err error
	f.target, err = models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("login-target", "member", f.base.user.EncodedPassword, nativeLoginInstant.Add(-24*time.Hour)).
		WithStaff(staff).WithEmail("member@example.test").WithFirstName("Retained").WithLastName("Profile"))
	if err != nil {
		t.Fatal(err)
	}
	f.view, err = models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("helpdesk.ticket.view", "View ticket"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.target.ID, f.view.ID)); err != nil {
		t.Fatal(err)
	}
	f.credential, err = f.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, password)
	if err != nil {
		t.Fatal(err)
	}
	for index := range 3 {
		passwordSession(t, f.runtime.Sessions(), f.actor, fmt.Sprintf("login-preserved-actor-%d", index))
		f.targetSessions = append(f.targetSessions, passwordSession(t, f.runtime.Sessions(), f.credential, fmt.Sprintf("login-preserved-target-%d", index)))
		if _, err := f.runtime.Sessions().Create(t.Context(), map[string]string{"anonymous": fmt.Sprintf("login-preserved-%d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	f.owner.hasher.hashes.Store(0)
	f.owner.hasher.verifies.Store(0)
	return f
}

func loginAdmission(_ context.Context, principal auth.Principal) error {
	if !principal.Authenticated() || !principal.Staff() || !principal.Has("helpdesk.ticket.view") {
		return auth.ErrInvalidCredentials
	}
	return nil
}

func loginNoResult(t *testing.T, result auth.SessionLoginResult, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(result, auth.SessionLoginResult{}) {
		t.Fatal("unconfirmed login published a credential or bearer", err)
	}
}

func loginExpectedRecord(t *testing.T, credential auth.Credential, previous sessions.Record, operation string, id sessions.ID, at time.Time) sessions.Record {
	t.Helper()
	snapshot := sessions.RecordSnapshot{ID: id, CreatedAt: at, AccessedAt: at, AbsoluteExpiresAt: at.Add(loginLifetime), IdleExpiresAt: at.Add(loginIdle), Values: map[string]string{}}
	if operation == "rotate" {
		snapshot.CreatedAt, snapshot.AbsoluteExpiresAt = previous.CreatedAt(), previous.AbsoluteExpiresAt()
		snapshot.Values = previous.Values()
	} else if operation != "create" && operation != "replace" {
		t.Fatal("unknown expected login operation")
	}
	if snapshot.IdleExpiresAt.After(snapshot.AbsoluteExpiresAt) {
		snapshot.IdleExpiresAt = snapshot.AbsoluteExpiresAt
	}
	snapshot.Values[auth.SessionPrincipalIDKey], snapshot.Values[auth.SessionCredentialStampKey] = credential.Principal().ID(), credential.SessionStamp()
	if previous.ID().Valid() && id == previous.ID() {
		t.Fatal("login reused its old bearer")
	}
	record, err := sessions.RestoreRecord(snapshot, sessions.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func loginNamespace(t *testing.T, credential auth.Credential) string {
	t.Helper()
	domain, bound := credential.Principal().CredentialDomain()
	if !bound || domain.Identifier() == "" {
		t.Fatal("login observation lacks its selected namespace")
	}
	return domain.Identifier()
}
func loginDigest(namespace string, id sessions.ID) string {
	digest := sha256.Sum256([]byte("godj.sessions.database.v1\x00" + namespace + "\x00" + id.Encoded()))
	return hex.EncodeToString(digest[:])
}
func loginRemoveRecord(t *testing.T, state *accountNativeState, namespace string, id sessions.ID) {
	t.Helper()
	before := len(state.sessions)
	digest := loginDigest(namespace, id)
	state.sessions = slices.DeleteFunc(slices.Clone(state.sessions), func(row sessionmodels.Session) bool { return row.Namespace == namespace && row.Digest == digest })
	if len(state.sessions) != before-1 {
		t.Fatal("login expected deletion did not identify exactly one prior row")
	}
}
func loginPutRecord(t *testing.T, state *accountNativeState, after accountNativeState, namespace string, record sessions.Record, replacing bool) {
	t.Helper()
	digest := loginDigest(namespace, record.ID())
	var keys []int64
	for _, row := range after.sessions {
		if row.Namespace == namespace && row.Digest == digest {
			keys = append(keys, row.ID)
		}
	}
	if len(keys) != 1 || keys[0] <= 0 {
		t.Fatal("login did not persist the independently observed bearer")
	}
	row := sessionmodels.NewSessionWithID(keys[0])
	row.Namespace, row.Digest = namespace, digest
	var err error
	row.Payload, err = sessionwire.Encode(record, sessions.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if replacing {
		for _, old := range state.sessions {
			if old.Namespace == namespace && old.Digest == digest && old.ID != row.ID {
				t.Fatal("session access changed its existing native row key")
			}
		}
		loginRemoveRecord(t, state, namespace, record.ID())
	} else {
		for _, old := range state.sessions {
			if old.ID == row.ID || old.Digest == digest {
				t.Fatal("login new row reused an existing allocation")
			}
		}
	}
	state.sessions = append(slices.Clone(state.sessions), row)
	slices.SortFunc(state.sessions, func(a, b sessionmodels.Session) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
}

func loginExpectedState(t *testing.T, before, after userReferenceState, credential auth.Credential, row models.User, previous, record sessions.Record, at time.Time) userReferenceState {
	t.Helper()
	instant := at.UTC().Truncate(time.Microsecond)
	row.LastLogin = &instant
	userReferenceReplace(&before.account, row)
	namespace := loginNamespace(t, credential)
	if previous.ID().Valid() {
		loginRemoveRecord(t, &before.account, namespace, previous.ID())
	}
	loginPutRecord(t, &before.account, after.account, namespace, record, false)
	return before
}
