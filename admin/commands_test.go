package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func managementCommandForm(t *testing.T) forms.Spec {
	t.Helper()
	field, err := forms.CharField("password", forms.WithTrimWhitespace(false), forms.WithWidget(forms.PasswordInput))
	if err != nil {
		t.Fatal(err)
	}
	form, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	return form
}

func TestAdminCommandUsesOwnFormPermissionAndConditionalWrite(t *testing.T) {
	for _, mode := range []string{"success", "stale", "late", "unknown", "rollback", "invalid_result", "invalid_input", "forged_input", "csrf", "denied"} {
		t.Run(mode, func(t *testing.T) {
			var state *managementFormState
			var calls int
			client, storage, registry := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(c *ModelConfig[managementFormRow]) {
				c.Commands = []CommandConfig{{Name: "password", Label: "Change password", Permission: "accounts.change", Form: managementCommandForm(t), Run: func(_ context.Context, p auth.Principal, m Mutation, values forms.Values) (CommandResult, error) {
					calls++
					state.actor = p.ID()
					if mode == "late" {
						state.row.revision++
					}
					if state.row.revision != m.Revision {
						return CommandResult{}, NewOperationError(OperationConflict, nil)
					}
					if mode == "rollback" {
						return CommandResult{}, errors.Join(validation.Reject(validation.NewErrors(validation.New("password", "invalid")), nil), errors.New("private rollback failure"))
					}
					state.password, _ = values.String("password")
					state.row.revision++
					state.writes++
					if mode == "unknown" {
						return CommandResult{}, NewOperationError(OperationOutcomeUnknown, errors.New("private outcome cause"))
					}
					if mode == "invalid_result" {
						return CommandResult{ID: m.ID + 1, Revision: state.row.revision}, nil
					}
					return CommandResult{ID: m.ID, Revision: state.row.revision}, nil
				}}}
			})
			state = storage
			client.login(t, "admin", "secret", "/admin/accounts/")
			change := client.do("GET", "/admin/accounts/change/?id=1", nil)
			path := "/admin/accounts/command/password/?id=1"
			if change.Code != 200 || !strings.Contains(change.Body.String(), path) || len(registry.All()[0].Commands) != 1 {
				t.Fatal("command discovery missing")
			}
			get := client.do("GET", path, nil)
			if get.Code != 200 || strings.Contains(get.Body.String(), `name="username"`) || !strings.Contains(get.Body.String(), `type="password"`) || managementRevision(t, get.Body.String()) != "1" {
				t.Fatal("command form inherited profile input or omitted condition")
			}
			const secret = "  private-command-λ<&>  "
			values := url.Values{"password": {secret}, "expected_revision": {"1"}, "csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}}
			want, writes, expectedCalls := 302, 1, 1
			switch mode {
			case "stale":
				state.row.revision++
				want, writes, expectedCalls = 409, 0, 0
			case "late":
				want, writes = 409, 0
			case "unknown":
				want = 503
			case "rollback":
				want, writes = 500, 0
			case "invalid_result":
				want = 500
			case "invalid_input":
				values.Set("password", "")
				want, writes, expectedCalls = 200, 0, 0
			case "forged_input":
				values.Set("username", "forged")
				want, writes, expectedCalls = 400, 0, 0
			case "csrf":
				values.Del("csrfmiddlewaretoken")
				want, writes, expectedCalls = 403, 0, 0
			case "denied":
				if logout := client.do("POST", "/admin/logout/", url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}}); logout.Code != 302 {
					t.Fatal("could not end manager session")
				}
				client.login(t, "viewer", "secret", "/admin/accounts/")
				page := client.do("GET", "/admin/accounts/", nil)
				values.Set("csrfmiddlewaretoken", siteCSRFToken(t, page.Body.String()))
				if denied := client.do("GET", path, nil); denied.Code != 403 {
					t.Fatal("command GET bypassed action permission")
				}
				want, writes, expectedCalls = 403, 0, 0
			}
			response := client.do("POST", path, values)
			if response.Code != want || state.writes != writes || calls != expectedCalls || strings.Contains(response.Body.String(), "private-") || strings.Contains(response.Body.String(), "private ") {
				t.Fatalf("command %s: status=%d writes=%d calls=%d", mode, response.Code, state.writes, calls)
			}
			if want != 302 && response.Header().Get("Location") != "" || response.Header().Get("Retry-After") != "" {
				t.Fatal("failed command redirected or invited retry")
			}
			if mode == "success" && (state.password != secret || state.actor != "manager" || state.row.revision != 2) {
				t.Fatal("command lost secret bytes, current actor or revision")
			}
			if mode == "invalid_input" && managementRevision(t, response.Body.String()) != "1" {
				t.Fatal("invalid command input lost submitted revision")
			}
		})
	}
}

func TestAdminCommandStartupAndForgedFormsFailBeforeMutation(t *testing.T) {
	good := CommandConfig{Name: "password", Label: "Change password", Permission: "accounts.change", Form: managementCommandForm(t), Run: func(context.Context, auth.Principal, Mutation, forms.Values) (CommandResult, error) {
		t.Fatal("invalid definition/input invoked command")
		return CommandResult{}, nil
	}}
	for _, change := range []func(*CommandConfig){
		func(c *CommandConfig) { c.Name = "../bad" }, func(c *CommandConfig) { c.Label = "" },
		func(c *CommandConfig) { c.Permission = "bad permission" }, func(c *CommandConfig) { c.Run = nil },
		func(c *CommandConfig) { c.Form = forms.Spec{} },
		func(c *CommandConfig) { c.Form, _ = forms.NewSpec([]forms.Field{cField(t, "expected_revision")}) },
	} {
		config := good
		change(&config)
		if _, err := prepareCommands([]CommandConfig{config}, registeredModel{revisionField: "revision"}); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
	if _, err := prepareCommands([]CommandConfig{good, good}, registeredModel{}); err == nil {
		t.Fatal("duplicate command accepted")
	}
	commands, err := prepareCommands([]CommandConfig{good}, registeredModel{revisionField: "revision"})
	if err != nil {
		t.Fatal(err)
	}
	forged, _ := forms.NewSpec([]forms.Field{cField(t, "username")})
	bound, _ := forged.Bind(t.Context(), forms.NewData(map[string][]string{"username": {"unrelated"}}), nil)
	if _, err := commands[0].run(context.Background(), mustPrincipalWithPermissions(t, "accounts.change"), Mutation{ID: 1, Revision: 1}, bound); err == nil {
		t.Fatal("unrelated valid form was accepted")
	}
}

func TestAdminOperationErrorKeepsPrivateCauseAndOuterClassification(t *testing.T) {
	cause := configDiagnosticSecretCause{Secret: "private-command-error-marker"}
	err := NewOperationError(OperationOutcomeUnknown, cause)
	for _, value := range []any{err, *err} {
		for _, format := range []string{"%v", "%+v", "%#v", "%p", "%w"} {
			if strings.Contains(fmt.Sprintf(format, value), cause.Secret) {
				t.Fatal("operation error exposed private cause")
			}
		}
		encoded, e := json.Marshal(value)
		if e != nil || strings.Contains(string(encoded), cause.Secret) {
			t.Fatal("operation JSON exposed private cause")
		}
	}
	var retained configDiagnosticSecretCause
	if !errors.As(err, &retained) || retained.Secret != cause.Secret {
		t.Fatal("operation error lost diagnostic cause")
	}
	joined := errors.Join(err, errors.New("rollback"))
	if _, returned := operationResponse(joined); returned != joined {
		t.Fatal("nested classification hid execution failure")
	}
}

func TestAdminCommandAuditNamesAreExplicitAndDoNotExposeStoredFields(t *testing.T) {
	for _, declared := range []bool{false, true} {
		client, _, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(c *ModelConfig[managementFormRow]) {
			if declared {
				c.AdditionalAuditFields = []string{"password"}
			}
			c.History = func(context.Context, auth.Principal, int64, HistoryRequest) ([]AuditEntry, error) {
				return []AuditEntry{{Sequence: 1, ActorID: "manager", Model: "godj_conformance.account", ObjectID: 1, Action: ActionChange, ChangedFields: []string{"password"}}}, nil
			}
		})
		client.login(t, "admin", "secret", "/admin/accounts/")
		response := client.do("GET", "/admin/accounts/history/?id=1", nil)
		if declared {
			if response.Code != 200 || !strings.Contains(response.Body.String(), "password") {
				t.Fatal("declared semantic event was not readable")
			}
		} else if response.Code != 500 {
			t.Fatal("undeclared semantic audit field was exposed")
		}
	}
	for _, names := range [][]string{{"id"}, {"title"}, {"password", "password"}, {"raw secret"}} {
		config := validRegistryConfig(t)
		config.AdditionalAuditFields = names
		if err := RegisterModel(NewBuilder(mustApps(t)), config); err == nil {
			t.Fatal("invalid semantic audit configuration accepted")
		}
	}
}
