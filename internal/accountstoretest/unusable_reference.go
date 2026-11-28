package accountstoretest

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/sessions"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
)

type unusableFixture struct {
	*userReferenceFixture
	backend    Backend
	owner      *userReferenceOwner
	target     models.User
	permission models.Permission
	records    []sessions.Record
	keys       map[int64]bool
}

func unusableConfig(t *testing.T) accountmodel.RuntimeConfig[models.User, models.Group, models.Permission] {
	t.Helper()
	config := userReferenceConfig(t)
	config.SessionStorage.MaxRecords = 512
	return config
}

func provisionUnusable(t *testing.T, backend Backend, targetSessions int) *unusableFixture {
	t.Helper()
	f := &unusableFixture{userReferenceFixture: provisionUserReference(t, backend), backend: backend, keys: map[int64]bool{}}
	f.owner = newUserReferenceOwner(t, backend, f.userReferenceFixture, unusableConfig(t))
	var err error
	f.target, err = models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("unusable-target", "member", f.base.user.EncodedPassword, joined).
		WithStaff(true).WithEmail("member@example.test").WithFirstName("Retained").WithLastName("Profile"))
	if err != nil {
		t.Fatal(err)
	}
	f.permission, err = models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("helpdesk.ticket.view", "View ticket"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.target.ID, f.permission.ID)); err != nil {
		t.Fatal(err)
	}
	credential, err := f.owner.runtime.Authenticator().Resolve(t.Context(), f.target.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		passwordSession(t, f.owner.runtime.Sessions(), f.actor, fmt.Sprintf("unusable-actor-%d", i))
		if _, err := f.owner.runtime.Sessions().Create(t.Context(), map[string]string{"anonymous": fmt.Sprintf("unusable-preserved-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range targetSessions {
		f.addTargetSession(t, credential, fmt.Sprintf("unusable-target-%d", i))
	}
	return f
}

func (f *unusableFixture) addTargetSession(t *testing.T, credential auth.Credential, label string) sessions.Record {
	t.Helper()
	before := userReferenceSnapshot(t, f.backend)
	record := passwordSession(t, f.owner.runtime.Sessions(), credential, label)
	key := userReferenceSession(t, f.owner.runtime, credential, record.ID(), before, userReferenceSnapshot(t, f.backend), f.actor.Principal())
	f.keys[key] = true
	f.records = append(f.records, record)
	return record
}

func assertUnusableEncoding(t *testing.T, encoded string) {
	t.Helper()
	if !strings.HasPrefix(encoded, "!") || auth.IsPasswordUsable(encoded) {
		t.Fatal("selected owner did not produce a reserved unusable marker")
	}
	entropy, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, "!"))
	if err != nil || len(entropy) != 32 || "!"+base64.RawURLEncoding.EncodeToString(entropy) != encoded {
		t.Fatal("selected unusable marker lost the canonical fresh entropy envelope")
	}
}

// A marker is opaque entropy, but its envelope, freshness and every semantic
// field are checked independently. A usable hash comes from the actual hasher.
func (f *unusableFixture) changed(t *testing.T, before userReferenceState, owner *userReferenceOwner, unusable bool) models.User {
	t.Helper()
	actual := profileRow(t, f.backend, f.target.ID)
	expected := f.target
	expected.Revision++
	if unusable {
		assertUnusableEncoding(t, actual.EncodedPassword)
		expected.EncodedPassword = actual.EncodedPassword
	} else {
		expected.EncodedPassword = owner.encoded.Load().(string)
	}
	if expected.EncodedPassword == f.target.EncodedPassword {
		t.Fatal("credential command retained its earlier marker or hash")
	}
	userReferenceReplace(&before.account, expected)
	before.account.sessions = slices.DeleteFunc(slices.Clone(before.account.sessions), func(row sessionmodels.Session) bool { return f.keys[row.ID] })
	assertUserReferenceState(t, before, userReferenceSnapshot(t, f.backend), f.actor.Principal(), catalogExpectedEvent{"user", expected.ID, admin.ActionChange, []string{"encoded_password"}})
	for _, record := range f.records {
		accountSessionRecord(t, f.owner.runtime.SessionStore(), record, false)
	}
	return expected
}

func assertUnusableLifecycleReference(t *testing.T, actual map[string]any) {
	t.Helper()
	if len(actual) != 4 {
		t.Fatal("selected unusable lifecycle did not observe all four reference sections")
	}
	var prior map[string]string
	for _, vendor := range []string{"sqlite", "postgres"} {
		raw, err := identityreference.UnusablePassword(vendor)
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Django, Python, Backend string
			Sources                 map[string]string `json:"source_sha256"`
			Observations            map[string]json.RawMessage
		}
		if err := json.Unmarshal(raw, &reference); err != nil || reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[vendor] || len(reference.Sources) != 5 {
			t.Fatal("unusable reference lost its pinned runtime or producer", err)
		}
		for _, name := range []string{"auth", "backends", "base_user", "forms", "hashers"} {
			value := reference.Sources[name]
			digest, err := hex.DecodeString(value)
			if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
				t.Fatal("unusable reference lost an independent source digest", name)
			}
		}
		if prior != nil && !reflect.DeepEqual(prior, reference.Sources) {
			t.Fatal("unusable reference producers differ across backends")
		}
		prior = reference.Sources
		for _, key := range []string{"created", "checks", "rotations", "restored"} {
			encoded, err := json.Marshal(actual[key])
			if err != nil {
				t.Fatal(err)
			}
			var observed, wanted any
			if err := json.Unmarshal(encoded, &observed); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(reference.Observations[key], &wanted); err != nil || !reflect.DeepEqual(observed, wanted) {
				t.Fatal("selected unusable lifecycle differs from independent Django", vendor, key, err)
			}
		}
	}
}
