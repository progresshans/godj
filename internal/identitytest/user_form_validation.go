package identitytest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

type modelFormReadBoundary struct {
	identity.ManagementBackend
	before              func(context.Context) error
	finish, readFailure error
	reader              db.Queryer
	calls               int
}

func (boundary *modelFormReadBoundary) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	boundary.calls++
	if boundary.before != nil {
		if err := boundary.before(ctx); err != nil {
			return err
		}
	}
	err := boundary.ManagementBackend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		boundary.reader = reader
		if boundary.readFailure != nil {
			reader = failedModelFormReader{cause: boundary.readFailure}
		}
		return callback(reader)
	})
	return errors.Join(err, boundary.finish)
}

type failedModelFormReader struct{ cause error }

func (reader failedModelFormReader) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, reader.cause
}

func runUserChangeModelValidation(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	t.Run("change_model_validation", func(t *testing.T) {
		for _, mode := range []string{"duplicate_and_field_error", "same_row", "authority_changed", "revision_changed", "read_failure", "cleanup_failure", "wrong_model", "wrong_key", "wrong_revision", "unbound", "canceled"} {
			t.Run(mode, func(t *testing.T) {
				backend, second := open(t)
				f := newManagementFixture(t, backend, 3)
				beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
				metadata := models.UserDescriptor{}.Metadata()
				if mode == "wrong_model" {
					metadata.Name = "other_user"
					metadata.DBTable = "other_user"
				}
				initial := map[string]forms.Value{"id": forms.Integer(f.user.ID), "revision": forms.Integer(f.user.Revision), "username": forms.String(f.user.Username), "email": forms.String(f.user.Email)}
				if mode == "wrong_key" {
					initial["id"] = forms.Integer(f.user.ID + 1)
				}
				if mode == "wrong_revision" {
					initial["revision"] = forms.Integer(f.user.Revision + 1)
				}
				data := map[string][]string{"username": {f.root.Username}, "email": {"invalid-address"}}
				if mode == "same_row" {
					data = map[string][]string{"username": {f.user.Username}, "email": {f.user.Email}}
				}
				bound, err := (formmodel.Definition{Fields: []string{"username", "email"}}).Bind(metadata, forms.NewData(data), initial)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "unbound" {
					bound = formmodel.BoundForm{}
				}
				secret := errors.New("private-model-form-read-failure")
				boundary := &modelFormReadBoundary{ManagementBackend: f.runtime}
				switch mode {
				case "authority_changed", "revision_changed":
					boundary.before = func(ctx context.Context) error {
						return second.CoordinatedAtomic(ctx, func(session db.Session) error {
							if mode == "authority_changed" {
								_, err := models.UserObjects.Update(ctx, session, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
								return err
							}
							_, err := models.UserObjects.Update(ctx, session, f.user, models.UserPatch{}.WithFirstName("Concurrent").WithRevision(2))
							return err
						})
					}
				case "read_failure":
					boundary.readFailure = secret
				case "cleanup_failure":
					boundary.finish = secret
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if mode == "canceled" {
					cancel()
				}
				err = f.manager(t, boundary).CheckUserChange(ctx, f.actor, f.user.ID, f.user.Revision, bound)
				failures, rejected := validation.Rejected(err)
				switch mode {
				case "same_row":
					if err != nil {
						t.Fatal("same row conflicted with itself", err)
					}
				case "duplicate_and_field_error":
					if !rejected || failures.Len() != 1 || failures.ByField("username").Len() != 1 {
						t.Fatal("unrelated invalid email suppressed username uniqueness", err)
					}
					checked, err := bound.WithErrors(failures)
					if err != nil || checked.Form().Errors().Len() != 2 || checked.Form().Errors().ByField("email").Len() != 1 {
						t.Fatal("DB diagnostics replaced field errors", err)
					}
				case "authority_changed":
					if !errors.Is(err, &identity.Error{Code: identity.CodePermission}) || rejected {
						t.Fatal("cached actor bypassed current authority", err)
					}
				case "revision_changed":
					if !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) || rejected {
						t.Fatal("changed row escaped current revision", err)
					}
				case "read_failure", "cleanup_failure":
					if !errors.Is(err, secret) || rejected {
						t.Fatal("read failure was downgraded to input diagnostics", err)
					}
				case "canceled":
					if !errors.Is(err, context.Canceled) || rejected {
						t.Fatal("canceled check published diagnostics", err)
					}
				default:
					if !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) || rejected {
						t.Fatal("foreign or unbound model accepted", err)
					}
				}
				wantCalls := 1
				if strings.HasPrefix(mode, "wrong_") || mode == "unbound" || mode == "canceled" {
					wantCalls = 0
				}
				if boundary.calls != wantCalls {
					t.Fatal("unexpected read scope count", boundary.calls)
				}
				if boundary.reader != nil {
					if _, err := models.UserObjects.Using(boundary.reader).Count(t.Context()); err == nil {
						t.Fatal("borrowed model validation reader survived its scope")
					}
				}
				if err != nil {
					for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
						if strings.Contains(fmt.Sprintf(format, err), secret.Error()) {
							t.Fatal("private DB detail leaked")
						}
					}
				}
				stored := f.stored(t)
				expected := f.user
				if mode == "revision_changed" {
					expected.FirstName = "Concurrent"
					expected.Revision = 2
				}
				if !reflect.DeepEqual(stored, expected) || f.hasher.calls.Load() != 0 {
					t.Fatal("read validation modified a profile or hashed a password")
				}
				afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
					t.Fatal("read validation modified sessions or audit")
				}
			})
		}
		t.Run("http_combined_errors", func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			h := newIdentityAdminHTTP(t, f, f.runtime)
			h.loginAdmin(t, h.client, "manager", managementOldPassword)
			details, err := f.manager(t, f.runtime).User(t.Context(), f.actor, f.user.ID)
			if err != nil {
				t.Fatal(err)
			}
			data := adminUserData(details)
			data.Set("username", f.root.Username)
			data.Set("email", "invalid-address")
			path := adminObjectPath("users", "change", f.user.ID)
			// Establish CSRF/revision before comparing durable state; the Site's
			// fixed access time keeps subsequent rejected requests read-only.
			h.call(t, h.client, "GET", path, nil, 200)
			beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
			h.postForm(t, path, url.Values(data), 200).contains(t, `data-error-field="email" data-error-code="invalid"`, `data-error-field="username" data-error-code="unique"`)
			if !reflect.DeepEqual(f.stored(t), f.user) || f.hasher.calls.Load() != 0 {
				t.Fatal("invalid form wrote a profile or credential")
			}
			afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
				t.Fatal("invalid form wrote sessions or audit")
			}
		})
	})
}
