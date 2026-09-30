package identitytest

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

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

func RunUserCreationForms(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	input, err := userCreationReference.ReadFile("testdata/user-creation-inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []creationCase
	if err := json.Unmarshal(input, &cases); err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Python, Unicode string
		InputSHA                string `json:"input_sha256"`
		Observations            map[string]map[string]creationObservation
		Lifecycle               map[string]map[string]map[string]any
	}
	var previous []byte
	for _, backend := range []string{"sqlite", "postgres"} {
		payload, err := userCreationReference.ReadFile("testdata/user-creation-django61-" + backend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(payload, &reference); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(input)
		if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Unicode != "16.0.0" || reference.InputSHA != hex.EncodeToString(digest[:]) {
			t.Fatal("unbound form lifecycle reference")
		}
		current, _ := json.Marshal(reference.Lifecycle)
		if previous != nil && string(previous) != string(current) {
			t.Fatal("native save lifecycle differs between backends")
		}
		previous = current
	}
	for _, mode := range []string{"standard", "admin_enabled", "admin_disabled"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 0)
			// A reusable form has manager authority but no Site staff requirement.
			if _, err := models.UserObjects.Update(t.Context(), backend, f.root, models.UserPatch{}.WithStaff(false).WithRevision(2)); err != nil {
				t.Fatal(err)
			}
			credential, err := f.runtime.Authenticator().Resolve(t.Context(), f.actor.ID())
			if err != nil || credential.Principal().Staff() {
				t.Fatal("nonstaff fixture failed", err)
			}
			actor := credential.Principal()
			if _, err := f.manager(t, f.runtime).CreateUserWithUnusablePassword(t.Context(), actor, identity.NewUserCreate("invalid-name-seed", "bad name")); err != nil {
				t.Fatal(err)
			}
			var profiles []string
			record := identity.PasswordValidatorFunc(func(_ string, p identity.Profile) validation.Errors {
				profiles = append(profiles, p.Username)
				return validation.Errors{}
			})
			minimum, err := identity.NewMinimumLengthValidator(8)
			if err != nil {
				t.Fatal(err)
			}
			similarity, err := identity.NewUserAttributeSimilarityValidator(identity.SimilarityConfig{Attributes: []string{"username"}})
			if err != nil {
				t.Fatal(err)
			}
			manager, err := identity.NewManager(f.runtime, f.hasher, auth.PrincipalAuthorizer{}, identity.WithPasswordValidators(record, minimum, similarity))
			if err != nil {
				t.Fatal(err)
			}
			factory := identity.NewUserCreationForm
			if mode != "standard" {
				factory = identity.NewAdminUserCreationForm
			}
			form, err := factory(manager)
			if err != nil {
				t.Fatal(err)
			}
			for _, probe := range cases {
				t.Run("bind_"+probe.Name, func(t *testing.T) {
					want, found := reference.Observations[mode][probe.Name]
					if !found {
						t.Fatal("missing native form case")
					}
					profiles = nil
					data := map[string][]string{"username": {probe.Username}, "password1": {probe.Password1}, "password2": {probe.Password2}}
					if mode == "admin_disabled" {
						data["unusable_password"] = []string{"true"}
					}
					beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
					bound, err := form.Bind(t.Context(), actor, forms.NewData(data))
					if err != nil {
						t.Fatal(err)
					}
					codes := map[string][]string{}
					for _, item := range bound.Errors().All() {
						codes[string(item.Field())] = append(codes[string(item.Field())], string(item.Code()))
					}
					if bound.Valid() != want.Valid || !reflect.DeepEqual(codes, want.Codes) {
						t.Fatal("reusable form differs from native", bound.Valid(), codes, want.Codes)
					}
					gotName, hasName := bound.Cleaned().String("username")
					if hasName != (want.Username != nil) || hasName && gotName != *want.Username {
						t.Fatal("cleaned username differs")
					}
					wanted := []string{}
					for _, profile := range want.Profiles {
						value := ""
						if profile != nil {
							value = *profile
						}
						wanted = append(wanted, value)
					}
					if !reflect.DeepEqual(append([]string{}, profiles...), wanted) {
						t.Fatal("candidate profile differs", profiles, wanted)
					}
					afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
					if f.hasher.calls.Load() != 0 || !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
						t.Fatal("Bind hashed or mutated")
					}
				})
			}
			for _, operation := range []string{"immediate", "deferred", "abandoned", "invalid"} {
				t.Run(operation, func(t *testing.T) {
					want, found := reference.Lifecycle[mode][operation]
					if !found {
						t.Fatal("missing native lifecycle")
					}
					username := "Lifecycle_" + mode + "_" + operation
					if operation == "invalid" {
						username = "MEMBER"
					}
					data := map[string][]string{"username": {username}, "password1": {"independent-secret"}, "password2": {"independent-secret"}}
					if mode == "admin_disabled" {
						data["unusable_password"] = []string{"true"}
					}
					count, err := models.UserObjects.Using(backend).Count(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					hashes := f.hasher.calls.Load()
					state := func() map[string]any {
						t.Helper()
						n, err := models.UserObjects.Using(backend).Count(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						return map[string]any{"user_delta": n - count, "hashes": f.hasher.calls.Load() - hashes}
					}
					bound, err := form.Bind(t.Context(), actor, forms.NewData(data))
					if err != nil {
						t.Fatal(err)
					}
					actual := map[string]any{"valid": bound.Valid(), "checked": state()}
					var saved identity.UserDetails
					if operation == "immediate" {
						saved, err = form.Create(t.Context(), actor, "principal-"+username, bound.Cleaned())
						if err != nil {
							t.Fatal(err)
						}
						prepared := state()
						prepared["pk_set"] = saved.ID > 0
						actual["prepared"] = prepared
					} else {
						beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
						prepared, err := form.Prepare(t.Context(), actor, "principal-"+username, bound.Cleaned())
						if operation == "invalid" {
							_, actual["rejected"] = validation.Rejected(err)
							if err == nil {
								t.Fatal("invalid form prepared")
							}
						} else {
							if err != nil {
								t.Fatal(err)
							}
							phase := state()
							phase["pk_set"] = false
							actual["prepared"] = phase
							encoded, err := json.Marshal(prepared)
							if err != nil {
								t.Fatal(err)
							}
							printed := fmt.Sprintf("%v %+v %#v %x %p", prepared, prepared, prepared, prepared, prepared) + string(encoded)
							if strings.Contains(printed, "independent-secret") || strings.Contains(printed, "principal-") || strings.Contains(printed, username) {
								t.Fatal("prepared credential leaked")
							}
						}
						afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
						if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
							t.Fatal("Prepare mutated session/audit")
						}
						if operation == "deferred" {
							saved, err = form.Commit(t.Context(), actor, prepared)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					for key, value := range state() {
						actual[key] = value
					}
					// The Python unsaved model's in-memory attributes are not Go's
					// opaque prepared credential API. Compare them after persistence.
					selected := map[string]any{}
					for key, value := range want {
						selected[key] = value
					}
					for _, key := range []string{"usable", "active", "staff", "superuser", "last_login_none"} {
						delete(selected, key)
					}
					gotJSON, _ := json.Marshal(actual)
					wantJSON, _ := json.Marshal(selected)
					if string(gotJSON) != string(wantJSON) {
						t.Fatalf("save lifecycle differs: %s != %s", gotJSON, wantJSON)
					}
					if saved.ID > 0 {
						row, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(saved.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
						if err != nil || !found {
							t.Fatal("saved user missing", err)
						}
						credential, err := f.runtime.Authenticator().Resolve(t.Context(), row.PrincipalID)
						if err != nil {
							t.Fatal(err)
						}
						if credential.HasUsablePassword() != want["usable"] || row.Active != want["active"] || row.Staff != want["staff"] || row.Superuser != want["superuser"] || (row.LastLogin == nil) != want["last_login_none"] {
							t.Fatal("stored creation defaults differ")
						}
					}
				})
			}
		})
	}
	runPreparedUserBoundaries(t, open)
}

func runPreparedUserBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("single_attempt_survives_user_deletion", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		policies := managementHost(t, backend)
		manager := f.manager(t, f.runtime)
		prepared, err := manager.PrepareUserCreation(t.Context(), f.actor, identity.NewUserCreate("single-use", "SingleUse"), managementNewPassword)
		if err != nil {
			t.Fatal(err)
		}
		copy := prepared
		created, err := manager.CommitUserCreation(t.Context(), f.actor, prepared)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.DeleteUser(t.Context(), f.actor, created.ID, created.Revision, policies.AccountsUser); err != nil {
			t.Fatal(err)
		}
		if value, err := manager.CommitUserCreation(t.Context(), f.actor, copy); !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) || value.ID != 0 {
			t.Fatal("consumed candidate recreated deleted credential", err)
		}
		if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 || f.hasher.calls.Load() != 1 {
			t.Fatal("deleted credential was recreated or rehashed", err)
		}
	})
	t.Run("concurrent_copies_share_one_attempt", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		manager := f.manager(t, f.runtime)
		prepared, err := manager.PrepareUserCreation(t.Context(), f.actor, identity.NewUserCreate("single-concurrent", "SingleConcurrent"), managementNewPassword)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 4)
		var workers sync.WaitGroup
		for range 4 {
			workers.Add(1)
			go func(candidate identity.PreparedUserCreation) {
				defer workers.Done()
				<-start
				_, err := manager.CommitUserCreation(t.Context(), f.actor, candidate)
				results <- err
			}(prepared)
		}
		close(start)
		workers.Wait()
		close(results)
		succeeded, conflicted := 0, 0
		for err := range results {
			if err == nil {
				succeeded++
			} else if errors.Is(err, &identity.Error{Code: identity.CodeConflict}) {
				conflicted++
			} else {
				t.Fatal("unexpected concurrent result", err)
			}
		}
		if succeeded != 1 || conflicted != 3 || f.hasher.calls.Load() != 1 {
			t.Fatal("copies did not share one attempt", succeeded, conflicted)
		}
	})
	t.Run("foreign_attempts_do_not_consume", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		manager := f.manager(t, f.runtime)
		prepared, err := manager.PrepareUserCreation(t.Context(), f.actor, identity.NewUserCreate("owned-attempt", "OwnedAttempt"), managementNewPassword)
		if err != nil {
			t.Fatal(err)
		}
		other := f.manager(t, f.runtime)
		if _, err := other.CommitUserCreation(t.Context(), f.actor, prepared); err == nil {
			t.Fatal("foreign manager adopted candidate")
		}
		credential, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.CommitUserCreation(t.Context(), credential.Principal(), prepared); !errors.Is(err, &identity.Error{Code: identity.CodePermission}) {
			t.Fatal("foreign actor adopted candidate", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := manager.CommitUserCreation(ctx, f.actor, prepared); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled attempt lost context", err)
		}
		if created, err := manager.CommitUserCreation(t.Context(), f.actor, prepared); err != nil || created.ID == 0 || f.hasher.calls.Load() != 1 {
			t.Fatal("unadmitted call consumed candidate", err)
		}
	})
	t.Run("foreign_values", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		manager := f.manager(t, f.runtime)
		for _, factory := range []func(*identity.Manager) (*identity.UserCreationForm, error){identity.NewUserCreationForm, identity.NewAdminUserCreationForm} {
			form, err := factory(manager)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"mismatch", "username_grammar", "foreign_type", "foreign_field"} {
				var fields []forms.Field
				data := map[string][]string{"username": {"Candidate"}, "password1": {"first-secret"}, "password2": {"first-secret"}}
				for _, name := range []string{"username", "password1", "password2"} {
					field, err := forms.CharField(name, forms.WithTrimWhitespace(false))
					if err != nil {
						t.Fatal(err)
					}
					if mode == "foreign_type" && name == "username" {
						field, err = forms.BooleanField(name)
						data[name] = []string{"true"}
						if err != nil {
							t.Fatal(err)
						}
					}
					fields = append(fields, field)
				}
				if mode == "mismatch" {
					data["password2"] = []string{"another-secret"}
				}
				if mode == "username_grammar" {
					data["username"] = []string{"bad/name"}
				}
				if mode == "foreign_field" {
					field, err := forms.BooleanField("staff")
					if err != nil {
						t.Fatal(err)
					}
					fields = append(fields, field)
					data["staff"] = []string{"true"}
				}
				spec, err := forms.NewSpec(fields)
				if err != nil {
					t.Fatal(err)
				}
				bound, err := spec.Bind(t.Context(), forms.NewData(data), nil)
				if err != nil || !bound.Valid() {
					t.Fatal("foreign form setup", err)
				}
				if _, err := form.Prepare(t.Context(), f.actor, "foreign-candidate", bound.Cleaned()); err == nil {
					t.Fatal("foreign cleaned input bypassed creation rules", mode)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := form.Prepare(ctx, f.actor, "canceled", forms.Values{}); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation became an input error", err)
			}
		}
		if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 || f.hasher.calls.Load() != 0 {
			t.Fatal("invalid foreign form hashed or wrote", err)
		}
	})
	for _, mode := range []string{"foreign_manager", "wrong_actor", "zero", "canceled", "revoked", "duplicate", "deleted_group", "audit_failure", "unknown_commit", "replay"} {
		t.Run(mode, func(t *testing.T) {
			backend, second := open(t)
			f := newManagementFixture(t, backend, 3)
			boundary := &adminMutationBoundary{Runtime: f.runtime}
			if mode == "audit_failure" || mode == "unknown_commit" {
				boundary.mode = mode
			}
			manager, err := identity.NewManager(boundary, f.hasher, auth.PrincipalAuthorizer{})
			if err != nil {
				t.Fatal(err)
			}
			group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("CandidateGroup"))
			if err != nil {
				t.Fatal(err)
			}
			input := identity.NewUserCreate("prepared-principal", "PreparedCandidate").WithFirstName("Initial").WithGroups(group.ID).WithCaseInsensitiveUsernameCheck()
			prepared, err := manager.PrepareUserCreation(t.Context(), f.actor, input, managementNewPassword)
			if err != nil {
				t.Fatal(err)
			}
			if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 || f.hasher.calls.Load() != 1 {
				t.Fatal("Prepare wrote or hashed incorrectly", err)
			}
			actor := f.actor
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "foreign_manager":
				manager = f.manager(t, f.runtime)
			case "wrong_actor":
				if _, err := models.UserObjects.Update(ctx, second, f.user, models.UserPatch{}.WithSuperuser(true).WithRevision(2)); err != nil {
					t.Fatal(err)
				}
				credential, err := f.runtime.Authenticator().Resolve(ctx, f.user.PrincipalID)
				if err != nil {
					t.Fatal(err)
				}
				actor = credential.Principal()
			case "zero":
				prepared = identity.PreparedUserCreation{}
			case "canceled":
				cancel()
			case "revoked":
				_, err = models.UserObjects.Update(ctx, second, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
			case "duplicate":
				_, err = models.UserObjects.Create(ctx, second, models.NewUserCreate("competing-principal", "PREPAREDCANDIDATE", f.user.EncodedPassword, f.user.DateJoined))
			case "deleted_group":
				_, err = models.GroupObjects.Delete(ctx, second, &group)
			case "replay":
				_, err = manager.CommitUserCreation(ctx, actor, prepared)
			}
			if err != nil {
				t.Fatal("boundary setup", err)
			}
			beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
			result, err := manager.CommitUserCreation(ctx, actor, prepared)
			if err == nil || result.ID != 0 || f.hasher.calls.Load() != 1 {
				t.Fatal("invalid prepared commit published or rehashed", result.ID, err)
			}
			if mode == "unknown_commit" {
				if !errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown}) || boundary.calls != 1 {
					t.Fatal("unknown commit downgraded or retried", err)
				}
			} else {
				afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
					t.Fatal("failed commit mutated system rows")
				}
			}
			if mode == "unknown_commit" || mode == "audit_failure" {
				if _, err := manager.CommitUserCreation(t.Context(), actor, prepared); !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) || boundary.calls != 1 {
					t.Fatal("consumed candidate reached storage again", err, boundary.calls)
				}
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost")
			}
			if mode == "revoked" || mode == "wrong_actor" {
				if !errors.Is(err, &identity.Error{Code: identity.CodePermission}) {
					t.Fatal("current/owner authority lost", err)
				}
			}
			want := int64(2)
			if mode == "duplicate" || mode == "unknown_commit" || mode == "replay" {
				want++
			}
			if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != want {
				t.Fatal("failed commit persisted candidate", count, want, err)
			}
		})
	}
}
