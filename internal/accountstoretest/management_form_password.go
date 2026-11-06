package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func passwordFormRows[M any](t *testing.T, backend db.Queryer, manager orm.Manager[M]) []M {
	t.Helper()
	key, err := manager.PrimaryKeyField()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := manager.Using(backend).OrderBy(orm.NewAutoField[M](key).Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func managementFormPasswords(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	snapshot := func(source Backend) map[string]any {
		return map[string]any{
			"accounts":          passwordFormRows(t, source, models.UserObjects),
			"groups":            passwordFormRows(t, source, models.GroupObjects),
			"permissions":       passwordFormRows(t, source, models.PermissionObjects),
			"user_groups":       passwordFormRows(t, source, models.UserGroupsLinkObjects),
			"user_permissions":  passwordFormRows(t, source, models.UserPermissionsLinkObjects),
			"group_permissions": passwordFormRows(t, source, models.GroupPermissionsLinkObjects),
			"audit":             passwordFormRows(t, source, auditmodels.EventObjects),
		}
	}
	before := snapshot(backend)
	for _, mode := range []string{"duplicate", "missing_login", "invalid_email", "model_error", "nil_password", "too_long", "policy_failure", "wrapped_rejection", "foreign_field", "cleanup", "denied", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private partial password failure")
			probe := &catalogReadBackend{Backend: backend, read: backend.ReadSnapshot}
			hasher := &creationHasher{PasswordHasher: f.hasher}
			effects, calls := 0, 0
			config := managementConfig()
			config.MaxPasswordBytes = 32
			config.CaseInsensitiveCreationLogin = true
			config.PrincipalID = func(context.Context) (string, error) { effects++; return "unexpected", nil }
			config.Now = func() time.Time { effects++; return joined }
			config.ValidateCreate = func(context.Context, db.Queryer, models.User, accountmodel.AccountMemberships) (validation.Errors, error) {
				effects++
				return validation.Errors{}, nil
			}
			if mode == "denied" {
				config.Permissions.Add = "account.unheld"
			}
			config.ValidatePassword = func(_ context.Context, raw string, view orm.ModelView[models.User]) (validation.Errors, error) {
				calls++
				if raw != "weak" {
					t.Fatal("advisory password changed raw input")
				}
				source, valid := view.Model()
				if !valid || !source.Equal((models.UserDescriptor{}).Metadata()) {
					t.Fatal("advisory password lost actual model source")
				}
				for _, name := range []string{"id", "principal_id", "revision", "date_joined", "last_login"} {
					if _, present := view.Get(name); present {
						t.Fatal("advisory password allocated or trusted server state", name)
					}
				}
				if passwordViewString(t, view, "encoded_password") != "" {
					t.Fatal("advisory password exposed encoding")
				}
				want := "form-password"
				if mode == "duplicate" || mode == "missing_login" || mode == "cleanup" {
					want = ""
				}
				if mode == "model_error" {
					want = "bad/name"
				}
				if passwordViewString(t, view, "username") != want {
					t.Fatal("advisory password lost field/model/duplicate provenance", mode)
				}
				if mode == "invalid_email" && passwordViewString(t, view, "email") != "" {
					t.Fatal("field-invalid email became a policy attribute")
				}
				copied := view.Values()
				copied["username"] = query.String("mutated")
				if passwordViewString(t, view, "username") != want {
					t.Fatal("password policy mutated its owned view")
				}
				diagnostics := validation.NewErrors(validation.New("password", "weak"))
				switch mode {
				case "policy_failure":
					return diagnostics, fault
				case "wrapped_rejection":
					return validation.Errors{}, fmt.Errorf("private cause: %w", validation.Reject(diagnostics, nil))
				case "foreign_field":
					return validation.NewErrors(validation.New("username", "foreign")), nil
				case "cancel":
					cancel()
				}
				return diagnostics, nil
			}
			owner := accountManagement(t, probe, hasher, config)
			if mode == "cleanup" {
				probe.read = func(ctx context.Context, read func(db.Queryer) error) error {
					return errors.Join(backend.ReadSnapshot(ctx, read), fault)
				}
			}
			data := map[string][]string{"username": {"form-password"}, "email": {"person@example.test"}}
			if mode == "duplicate" || mode == "cleanup" {
				data["username"] = []string{strings.ToUpper(f.user.Username)}
			}
			if mode == "missing_login" {
				data["username"] = []string{""}
			}
			if mode == "invalid_email" {
				data["email"] = []string{"invalid"}
			}
			if mode == "model_error" {
				data["username"] = []string{"bad/name"}
			}
			bound := managementBoundForm(t, owner, data, nil, nil, nil)
			if mode == "model_error" {
				var err error
				bound, err = bound.WithErrors(validation.NewErrors(validation.New("username", "model_policy")))
				if err != nil {
					t.Fatal(err)
				}
			}
			raw := "weak"
			password := &raw
			if mode == "nil_password" {
				password = nil
			}
			if mode == "too_long" {
				raw = strings.Repeat("x", 33)
			}
			result, err := owner.CheckCreateForm(ctx, actor, bound, password)
			failed := mode == "policy_failure" || mode == "wrapped_rejection" || mode == "foreign_field" || mode == "cleanup" || mode == "denied" || mode == "cancel"
			if failed {
				code := accountmodel.CodePersistence
				if mode == "denied" {
					code = accountmodel.CodePermission
				}
				requireProfileCode(t, err, code)
				if !result.Fields.Empty() || !result.Password.Empty() {
					t.Fatal("failed advisory snapshot published diagnostics")
				}
				if (mode == "policy_failure" || mode == "cleanup") && !errors.Is(err, fault) {
					t.Fatal("advisory password lost execution cause")
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("advisory password lost cancellation")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if (mode == "duplicate") != (!result.Fields.ByField("username").Empty()) {
					t.Fatal("model and password diagnostics were conflated", mode)
				}
				if mode == "nil_password" {
					if !result.Password.Empty() {
						t.Fatal("absent credential gained diagnostics")
					}
				} else {
					code := validation.Code("weak")
					if mode == "too_long" {
						code = "max_length"
					}
					errors := result.Password.ByField("password").All()
					if len(errors) != 1 || errors[0].Code() != code {
						t.Fatal("partial model suppressed independent password diagnostics", mode)
					}
				}
			}
			expectedCalls := 1
			if mode == "nil_password" || mode == "too_long" || mode == "denied" {
				expectedCalls = 0
			}
			if calls != expectedCalls || effects != 0 || hasher.calls.Load() != 0 || !reflect.DeepEqual(before, snapshot(reopen(t))) {
				t.Fatal("advisory password retried, invoked final sources or changed durable state", mode, calls, effects)
			}
		})
	}
}
