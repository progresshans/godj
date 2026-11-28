package consumer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	apiinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/output"
	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

func roleConsumerFields(role ir.AccountRole) []string {
	fields := []string{role.Groups, role.Permissions}
	for _, flag := range []ir.AccountBoolean{role.Active, role.Staff, role.Superuser} {
		if flag.Constant == nil {
			fields = append(fields, flag.Field)
		}
	}
	return fields
}

// The generated factory is instantiated separately with User and Member. A
// real browser cookie authenticates Form/JSON management and a second browser
// logs into the target before its deactivation. No principal or role is trusted
// from a request, and all membership mutations go through the typed owner.
func assertAccountRoleHTTP[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], authenticator *accountmodel.Authenticator[M], store *accountmodel.SessionStore[M], editor *accountmodel.AccountEditor[M], reader *accountmodel.ProfileReader[M], actor auth.Principal, actorLogin string, target M, groupID, permissionID int64, profileField, editedProfile string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	objects := editor.Manager()
	metadata, err := objects.Metadata()
	check(err)
	role, err := binding.Role()
	check(err)
	key, present, err := objects.PrimaryKey(target)
	check(err)
	id, integer := key.Integer()
	if !present || !integer {
		t.Fatal("role fixture has no key")
	}
	values, err := objects.ModelValues(target)
	check(err)
	targetLogin, _ := values[role.Login].String()
	originalProfile, ok := values[profileField].String()
	if !ok || originalProfile == editedProfile {
		t.Fatal("combined profile fixture has no actual change")
	}
	secret, _ := values[role.Password].String()
	subject, _ := values[role.PrincipalID].String()
	formDefinition, err := binding.FormDefinition(editor.Fields())
	check(err)
	form, err := formDefinition.Spec(metadata)
	check(err)
	form, err = form.WithModelChoices(role.Groups, forms.Choice{Value: forms.Integer(groupID), Label: "Operators"})
	check(err)
	form, err = form.WithModelChoices(role.Permissions, forms.Choice{Value: forms.Integer(permissionID), Label: "Direct permission"})
	check(err)
	// This host's JSON profile input follows the model's blank policy,
	// including a valid empty default for the selected profile field.
	profileAllowsEmpty := false
	for _, field := range metadata.Fields {
		if field.Name == profileField {
			profileAllowsEmpty = field.Blank
			break
		}
	}
	fields := make([]serializers.ModelField, 0, len(editor.Fields()))
	for _, name := range editor.Fields() {
		fields = append(fields, serializers.ModelField{Name: name, AllowEmpty: name == profileField && profileAllowsEmpty})
	}
	spec, err := binding.InputSpec(fields...)
	check(err)
	input, err := apiinput.NewModel(objects, spec, api.ParserConfig{})
	check(err)
	for i := range fields {
		fields[i].ReadOnly = true
	}
	readSpec, err := serializers.FromModel(metadata, fields...)
	check(err)
	out, err := output.New(output.ModelView(objects, readSpec), serializers.Limits{})
	check(err)
	manager, err := sessions.NewManager(store, sessions.Config{})
	check(err)
	owner, err := store.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: owner,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/login/", FallbackPath: "/roles/", AllowedNextPaths: []string{"/roles/", "/api/roles/"}})
	check(err)
	adapter, err := apisession.New(runtime)
	check(err)
	empty := output.NoContent()
	patch, err := endpoint.New(adapter, endpoint.Config[orm.ModelInput[M], struct{}]{
		Route: web.Route{Name: "account:roles_json", Method: http.MethodPatch, Path: "/api/roles/"}, Admission: endpoint.All("account.team"), Input: endpoint.JSONBody("AccountRoleInput", input, serializers.ModePartial, "Explicit role fields."), Output: empty,
		Success: []endpoint.Status{{Code: 204, Description: "Committed role change."}}, Errors: []endpoint.Status{{Code: 400, Description: "Invalid input."}, {Code: 403, Description: "Permission required."}, {Code: 409, Description: "Stale revision."}},
		Handle: func(request *web.Request, current auth.Principal, input orm.ModelInput[M]) (output.Prepared[struct{}], error) {
			versions := request.HTTP().Header.Values("X-Role-Revision")
			if len(versions) != 1 {
				return output.Prepared[struct{}]{}, endpoint.Reject(400, api.CodeValidationError, validation.Errors{})
			}
			revision, err := strconv.ParseInt(versions[0], 10, 64)
			if err != nil || revision < 1 {
				return output.Prepared[struct{}]{}, endpoint.Reject(400, api.CodeValidationError, validation.Errors{})
			}
			if _, err := editor.Update(request.Context(), current, id, revision, input); err != nil {
				if fields, rejected := validation.Rejected(err); rejected {
					return output.Prepared[struct{}]{}, endpoint.Reject(400, api.CodeValidationError, fields)
				}
				return output.Prepared[struct{}]{}, profileEndpointError(err)
			}
			return empty.Prepare(request.Context(), 204, struct{}{})
		},
	})
	check(err)
	operation, err := patch.Operation()
	check(err)
	routes := []web.Route{operation.Route,
		{Name: "account:roles_login_form", Method: "GET", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			token, err := runtime.CSRFToken(request)
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.HTML(200, []byte(token.Value()))
			if err != nil {
				return web.Response{}, err
			}
			return token.Apply(response)
		}},
		{Name: "account:roles_login", Method: "POST", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			data, err := profileFormData(request)
			if err != nil {
				return web.Response{}, err
			}
			if err := runtime.VerifyCSRF(request, data["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, []byte("CSRF rejected"))
			}
			result, err := runtime.Login(request, data.Get("username"), data.Get("password"))
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(204, nil, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
		{Name: "account:roles_detail", Method: "GET", Path: "/roles/", Handler: runtime.Require("account.team", func(request *web.Request, current auth.Principal) (web.Response, error) {
			profile, err := reader.Get(request.Context(), current, id)
			if err != nil {
				return web.Response{}, err
			}
			wire, err := out.Encode(request.Context(), profile.View())
			if err != nil {
				return web.Response{}, err
			}
			token, err := runtime.CSRFToken(request)
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(200, http.Header{"Content-Type": {api.JSONContentType}, "X-Role-Revision": {strconv.FormatInt(profile.Revision(), 10)}, runtime.CSRFHeader(): {token.Value()}}, wire)
			if err != nil {
				return web.Response{}, err
			}
			return token.Apply(response)
		})},
		{Name: "account:roles_form", Method: "POST", Path: "/roles/", Handler: runtime.Require("account.team", func(request *web.Request, current auth.Principal) (web.Response, error) {
			data, err := profileFormData(request)
			if err != nil {
				return web.Response{}, err
			}
			if err := runtime.VerifyCSRF(request, data["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, []byte("CSRF rejected"))
			}
			revision, err := strconv.ParseInt(data.Get("revision"), 10, 64)
			if err != nil || revision < 1 || len(data["revision"]) != 1 {
				return web.HTML(400, []byte("Invalid revision"))
			}
			data.Del("csrfmiddlewaretoken")
			data.Del("revision")
			profile, err := reader.Get(request.Context(), current, id)
			if err != nil {
				return web.Response{}, err
			}
			bound, err := formmodel.BindView(request.Context(), objects, form, forms.NewData(data), profile.View(), formmodel.PostClean{})
			if err != nil {
				return web.Response{}, err
			}
			if !bound.Form().Valid() {
				return web.HTML(400, []byte("Invalid roles"))
			}
			input, err := formmodel.PrepareInput(objects, bound)
			if err != nil {
				return web.Response{}, err
			}
			_, err = editor.Update(request.Context(), current, id, revision, input)
			if errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
				return web.HTML(409, []byte("Role changed"))
			}
			if err != nil {
				return web.Response{}, err
			}
			return web.NewResponse(204, nil, nil)
		})},
	}
	configured, err := settings.New(settings.Definition{ProjectName: "account_role_consumer", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "account"})})
	check(err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes})
	check(err)
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		check(err)
		return &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	client, targetClient := newClient(), newClient()
	send := func(client *http.Client, method, path, contentType, raw, token, revision string) (int, http.Header, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(raw))
		check(err)
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.Header.Set(runtime.CSRFHeader(), token)
		}
		if revision != "" {
			request.Header.Set("X-Role-Revision", revision)
		}
		response, err := client.Do(request)
		check(err)
		body, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		for _, private := range []string{secret, subject, actor.ID()} {
			if private != "" && strings.Contains(string(body), private) {
				t.Fatal("role HTTP disclosed credential identity")
			}
		}
		return response.StatusCode, response.Header, string(body)
	}
	login := func(client *http.Client, name string) sessions.ID {
		t.Helper()
		status, _, token := send(client, "GET", "/login/", "", "", "", "")
		if status != 200 || len(token) != 128 {
			t.Fatal("role login CSRF", status)
		}
		data := url.Values{"username": {name}, "password": {accountPasswordText}, "csrfmiddlewaretoken": {token}}
		status, _, _ = send(client, "POST", "/login/", "application/x-www-form-urlencoded", data.Encode(), "", "")
		if status != 204 {
			t.Fatal("role HTTP login", status)
		}
		location, err := url.Parse(server.URL)
		check(err)
		for _, cookie := range client.Jar.Cookies(location) {
			if cookie.Name == websession.DefaultSessionCookieName {
				id, err := sessions.ParseID(cookie.Value)
				check(err)
				return id
			}
		}
		t.Fatal("role login has no browser session")
		return sessions.ID{}
	}
	actorSession := login(client, actorLogin)
	status, headers, raw := send(client, "GET", "/roles/", "", "", "", "")
	if status != 200 || headers.Get("X-Role-Revision") != "1" {
		t.Fatal("role detail", status)
	}
	var detail map[string]any
	check(json.Unmarshal([]byte(raw), &detail))
	if len(detail) != len(editor.Fields()) {
		t.Fatal("role detail exposed hidden fields", raw)
	}
	token := headers.Get(runtime.CSRFHeader())
	data := url.Values{"revision": {"1"}, "csrfmiddlewaretoken": {token}, role.Groups: {strconv.FormatInt(groupID, 10)}, role.Permissions: {strconv.FormatInt(permissionID, 10)}, profileField: {editedProfile}}
	for _, flag := range []ir.AccountBoolean{role.Active, role.Staff, role.Superuser} {
		if flag.Constant == nil {
			data[flag.Field] = []string{"true"}
		}
	}
	status, _, _ = send(client, "POST", "/roles/", "application/x-www-form-urlencoded", data.Encode(), "", "")
	if status != 204 {
		t.Fatal("role ModelForm HTTP update", status)
	}
	status, headers, _ = send(client, "GET", "/roles/", "", "", "", "")
	if status != 200 || headers.Get("X-Role-Revision") != "2" {
		t.Fatal("role form revision", status)
	}
	formCurrent, err := reader.Get(t.Context(), actor, id)
	check(err)
	if value, _ := formCurrent.View().Values()[profileField].String(); value != editedProfile || formCurrent.Revision() != 2 {
		t.Fatal("Form profile and roles did not commit at one revision")
	}
	token = headers.Get(runtime.CSRFHeader())
	encode := func(data map[string]any) string { wire, err := json.Marshal(data); check(err); return string(wire) }
	raw = encode(map[string]any{role.Permissions: []int64{}, profileField: originalProfile})
	status, _, _ = send(client, "PATCH", "/api/roles/", api.JSONContentType, raw, token, "1")
	if status != 409 {
		t.Fatal("stale role JSON admitted", status)
	}
	staleCurrent, err := reader.Get(t.Context(), actor, id)
	check(err)
	if value, _ := staleCurrent.View().Values()[profileField].String(); value != editedProfile || staleCurrent.Revision() != 2 {
		t.Fatal("stale combined JSON changed profile")
	}
	status, _, _ = send(client, "PATCH", "/api/roles/", api.JSONContentType, encode(map[string]any{role.Password: "must-not-write"}), token, "2")
	if status != 400 {
		t.Fatal("role credential input admitted", status)
	}
	status, _, _ = send(client, "PATCH", "/api/roles/", api.JSONContentType, raw, token, "2")
	if status != 204 {
		t.Fatal("role JSON explicit clear", status)
	}
	current, err := reader.Get(t.Context(), actor, id)
	check(err)
	groupKeys, loaded := current.View().Collection(role.Groups)
	directKeys, directLoaded := current.View().Collection(role.Permissions)
	if current.Revision() != 3 || !loaded || len(groupKeys) != 1 || groupKeys[0] != groupID || !directLoaded || len(directKeys) != 0 {
		t.Fatal("role omission/clear semantics diverged")
	}
	if value, _ := current.View().Values()[profileField].String(); value != originalProfile {
		t.Fatal("JSON profile and memberships did not commit together")
	}
	status, _, _ = send(client, "PATCH", "/api/roles/", api.JSONContentType, encode(map[string]any{role.Groups: []int64{9223372036854775807}}), token, "3")
	if status != 400 {
		t.Fatal("missing role choice admitted", status)
	}
	targetSession := login(targetClient, targetLogin)
	if _, present, err := store.Load(t.Context(), targetSession); err != nil || !present {
		t.Fatal("target HTTP login not durable", err)
	}
	status, _, _ = send(client, "PATCH", "/api/roles/", api.JSONContentType, encode(map[string]any{role.Active.Field: false, profileField: editedProfile}), token, "3")
	if status != 204 {
		t.Fatal("role JSON deactivation", status)
	}
	if _, present, err := store.Load(t.Context(), targetSession); err != nil || present {
		t.Fatal("deactivation kept target browser session", err)
	}
	if _, present, err := store.Load(t.Context(), actorSession); err != nil || !present {
		t.Fatal("deactivation revoked managing browser session", err)
	}
	status, _, _ = send(targetClient, "PATCH", "/api/roles/", api.JSONContentType, `{}`, "", "4")
	if status != 403 {
		t.Fatal("revoked target browser retained API admission", status)
	}
	current, err = reader.Get(t.Context(), actor, id)
	check(err)
	if current.Revision() != 4 {
		t.Fatal("rejections advanced role revision")
	}
	if value, _ := current.View().Values()[profileField].String(); value != editedProfile {
		t.Fatal("deactivation did not commit the same profile edit")
	}
	history, err := reader.History(t.Context(), actor, id, 100)
	check(err)
	if len(history) != 3 {
		t.Fatal("role HTTP audit mismatch")
	}
	storedKey := orm.NewAutoField[M](metadata.Fields[0])
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			storedKey = orm.NewAutoField[M](field)
		}
	}
	stored, present, err := objects.Using(backend).Filter(storedKey.Exact(id)).OrderBy(storedKey.Asc()).First(t.Context())
	check(err)
	if !present {
		t.Fatal("role target lost")
	}
	after, err := objects.ModelValues(stored)
	check(err)
	if !after[role.Password].Equal(values[role.Password]) || !after[role.PrincipalID].Equal(values[role.PrincipalID]) {
		t.Fatal("role HTTP changed protected identity")
	}
}
