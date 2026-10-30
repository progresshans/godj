package consumer

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
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
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

// This independent consumer runs real TCP HTTP, password authentication,
// cookie/session resolution, CSRF, ModelForm and typed JSON input/output with
// either generated account type. The tiny application owns its HTML and its
// revision control; it never receives an account row from ProfileReader.
func assertProfileHTTP[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], editor *accountmodel.ProfileEditor[M], reader *accountmodel.ProfileReader[M], authenticator *accountmodel.Authenticator[M], sessionStore *accountmodel.SessionStore[M], actor auth.Principal, row M, field, desired string, revoke func(bool) error) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	objects, err := binding.Objects()
	check(err)
	metadata, err := objects.Metadata()
	check(err)
	role, err := binding.Role()
	check(err)
	key, present, err := objects.PrimaryKey(row)
	check(err)
	id, integer := key.Integer()
	if !present || !integer {
		t.Fatal("HTTP account fixture has no stored key")
	}
	values, err := objects.ModelValues(row)
	check(err)
	login, _ := values[role.Login].String()
	secret, _ := values[role.Password].String()
	formSpec, err := formmodel.NewSpecForFields(metadata, editor.Fields())
	check(err)
	spec := profileJSONSpec(t, metadata, field)
	body, err := apiinput.NewModel(objects, spec, api.ParserConfig{})
	check(err)
	out, err := output.New(output.Named("AccountProfile", output.ModelView(objects, spec)), serializers.Limits{})
	check(err)
	empty := output.NoContent()
	manager, err := sessions.NewManager(sessionStore, sessions.Config{})
	check(err)
	loginOwner, err := sessionStore.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{},
		LoginPersistence: loginOwner,
		SessionCookie:    websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath: "/login/", FallbackPath: "/profile/", AllowedNextPaths: []string{"/profile/", "/api/profile/"}})
	check(err)
	adapter, err := apisession.New(runtime)
	check(err)
	get, err := endpoint.New(adapter, endpoint.Config[struct{}, orm.ModelView[M]]{
		Route:     web.Route{Name: "account:read", Method: http.MethodGet, Path: "/api/profile/"},
		Admission: endpoint.Authenticated(), Input: endpoint.NoInput(), Output: out,
		Success: []endpoint.Status{{Code: http.StatusOK, Description: "Selected account profile."}},
		Errors:  []endpoint.Status{{Code: http.StatusForbidden, Description: "Current read permission required."}},
		Handle: func(request *web.Request, current auth.Principal, _ struct{}) (output.Prepared[orm.ModelView[M]], error) {
			profile, err := reader.Get(request.Context(), current, id)
			if err != nil {
				return output.Prepared[orm.ModelView[M]]{}, profileEndpointError(err)
			}
			prepared, err := out.Prepare(request.Context(), http.StatusOK, profile.View())
			if err != nil {
				return prepared, err
			}
			return prepared.WithHeaders(http.Header{"Content-Type": {api.JSONContentType}, "X-Profile-Revision": {strconv.FormatInt(profile.Revision(), 10)}})
		},
	})
	check(err)
	patch, err := endpoint.New(adapter, endpoint.Config[orm.ModelInput[M], struct{}]{
		Route:     web.Route{Name: "account:update", Method: http.MethodPatch, Path: "/api/profile/"},
		Admission: endpoint.All("account.team"), Input: endpoint.JSONBody("AccountProfileInput", body, serializers.ModePartial, "Explicit account fields."), Output: empty,
		Success: []endpoint.Status{{Code: http.StatusNoContent, Description: "Committed profile change."}},
		Errors:  []endpoint.Status{{Code: http.StatusBadRequest, Description: "Revision required."}, {Code: http.StatusConflict, Description: "Profile changed."}, {Code: http.StatusForbidden, Description: "Current permission required."}},
		Handle: func(request *web.Request, current auth.Principal, input orm.ModelInput[M]) (output.Prepared[struct{}], error) {
			prepared, err := empty.Prepare(request.Context(), http.StatusNoContent, struct{}{})
			if err != nil {
				return prepared, err
			}
			versions := request.HTTP().Header.Values("X-Profile-Revision")
			if len(versions) != 1 {
				return output.Prepared[struct{}]{}, endpoint.Reject(400, api.CodeValidationError, validation.Errors{})
			}
			version, err := strconv.ParseInt(versions[0], 10, 64)
			if err != nil || version <= 0 {
				return output.Prepared[struct{}]{}, endpoint.Reject(400, api.CodeValidationError, validation.Errors{})
			}
			_, err = editor.Update(request.Context(), current, id, version, input)
			if err != nil {
				return output.Prepared[struct{}]{}, profileEndpointError(err)
			}
			return prepared, nil
		},
	})
	check(err)
	getOperation, err := get.Operation()
	check(err)
	patchOperation, err := patch.Operation()
	check(err)
	routes := []web.Route{getOperation.Route, patchOperation.Route,
		{Name: "account:login_page", Method: http.MethodGet, Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
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
		{Name: "account:login", Method: http.MethodPost, Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
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
		{Name: "account:form", Method: http.MethodGet, Path: "/profile/", Handler: runtime.Require("account.team", func(request *web.Request, current auth.Principal) (web.Response, error) {
			profile, err := reader.Get(request.Context(), current, id)
			if err != nil {
				return web.Response{}, err
			}
			initial, err := formmodel.InitialView(objects, formSpec, profile.View())
			if err != nil {
				return web.Response{}, err
			}
			form, err := formSpec.Unbound(initial)
			if err != nil {
				return web.Response{}, err
			}
			value, found := form.Initial().String(field)
			if !found {
				return web.Response{}, errors.New("selected form value absent")
			}
			token, err := runtime.CSRFToken(request)
			if err != nil {
				return web.Response{}, err
			}
			markup := fmt.Sprintf(`<form method="post"><input name="%s" value="%s"><input type="hidden" name="_profile_revision" value="%d"><input type="hidden" name="csrfmiddlewaretoken" value="%s"></form>`, html.EscapeString(field), html.EscapeString(value), profile.Revision(), token.Value())
			response, err := web.HTML(200, []byte(markup))
			if err != nil {
				return web.Response{}, err
			}
			header := response.Header()
			header.Set(runtime.CSRFHeader(), token.Value())
			response, err = response.WithHeaders(header)
			if err != nil {
				return web.Response{}, err
			}
			return token.Apply(response)
		})},
		{Name: "account:form_update", Method: http.MethodPost, Path: "/profile/", Handler: runtime.Require("account.team", func(request *web.Request, current auth.Principal) (web.Response, error) {
			data, err := profileFormData(request)
			if err != nil {
				return web.Response{}, err
			}
			if err := runtime.VerifyCSRF(request, data["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, []byte("CSRF rejected"))
			}
			version, err := strconv.ParseInt(data.Get("_profile_revision"), 10, 64)
			if err != nil || version <= 0 || len(data["_profile_revision"]) != 1 {
				return web.HTML(400, []byte("Invalid revision"))
			}
			data.Del("_profile_revision")
			data.Del("csrfmiddlewaretoken")
			profile, err := reader.Get(request.Context(), current, id)
			if err != nil {
				return web.Response{}, err
			}
			bound, err := formmodel.BindView(request.Context(), objects, formSpec, forms.NewData(data), profile.View(), formmodel.PostClean{})
			if err != nil {
				return web.Response{}, err
			}
			if !bound.Form().Valid() {
				return web.HTML(400, []byte("Invalid profile"))
			}
			input, err := formmodel.PrepareInput(objects, bound)
			if err != nil {
				return web.Response{}, err
			}
			_, err = editor.Update(request.Context(), current, id, version, input)
			if errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
				return web.HTML(409, []byte("Profile changed"))
			}
			if err != nil {
				return web.Response{}, err
			}
			return web.NewResponse(204, nil, nil)
		})},
	}
	configured, err := settings.New(settings.Definition{ProjectName: "account_profile_consumer",
		InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "account"}),
	})
	check(err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes})
	check(err)
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	check(err)
	client := server.Client()
	client.Jar = jar
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	send := func(method, path, contentType, raw, token, version, origin string) (int, http.Header, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(raw))
		check(err)
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.Header.Set(runtime.CSRFHeader(), token)
		}
		if version != "" {
			request.Header.Set("X-Profile-Revision", version)
		}
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response, err := client.Do(request)
		check(err)
		payload, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		if strings.Contains(string(payload), secret) || strings.Contains(string(payload), actor.ID()) {
			t.Fatal("HTTP profile exposed credential identity")
		}
		return response.StatusCode, response.Header, string(payload)
	}
	status, _, _ := send("GET", "/api/profile/", "", "", "", "", "")
	if status != 403 {
		t.Fatal("anonymous API read admitted", status)
	}
	status, _, token := send("GET", "/login/", "", "", "", "", "")
	if status != 200 || len(token) != 128 {
		t.Fatal("HTTP login CSRF absent", status)
	}
	loginData := url.Values{"username": {login}, "password": {accountPasswordText}, "csrfmiddlewaretoken": {token}}
	status, _, _ = send("POST", "/login/", "application/x-www-form-urlencoded", loginData.Encode(), "", "", "")
	if status != 204 {
		t.Fatal("real account password login failed", status)
	}
	serverURL, err := url.Parse(server.URL)
	check(err)
	var browserID sessions.ID
	for _, cookie := range jar.Cookies(serverURL) {
		if cookie.Name == websession.DefaultSessionCookieName {
			browserID, err = sessions.ParseID(cookie.Value)
			check(err)
		}
	}
	durable, present, err := sessionStore.Load(t.Context(), browserID)
	check(err)
	if !present {
		t.Fatal("HTTP login cookie has no durable selected-account session")
	}
	if subject, _ := durable.Value(auth.SessionPrincipalIDKey); subject != actor.ID() {
		t.Fatal("HTTP session bound another account")
	}
	var observed M
	present = false
	for _, primary := range metadata.Fields {
		if !primary.PrimaryKey {
			continue
		}
		keyField := orm.NewAutoField[M](primary)
		observed, present, err = objects.Using(backend).Filter(keyField.Exact(id)).OrderBy(keyField.Asc()).First(t.Context())
		check(err)
		break
	}
	if !present {
		t.Fatal("HTTP login lost selected account")
	}
	observedValues, err := objects.ModelValues(observed)
	check(err)
	if revision, _ := observedValues[role.Revision].Integer(); revision != 3 || !observedValues[role.Password].Equal(values[role.Password]) {
		t.Fatal("HTTP login changed management revision/password")
	}
	if role.LastLogin != "" && observedValues[role.LastLogin].IsNull() {
		t.Fatal("HTTP login omitted selected LastLogin observation")
	}
	status, headers, markup := send("GET", "/profile/", "", "", "", "", "")
	if status != 200 || !strings.Contains(markup, `name="`+field+`" value="`+desired+` JSON"`) || !strings.Contains(markup, `name="_profile_revision" value="3"`) {
		t.Fatal("HTTP form did not display selected current profile", status, markup)
	}
	for _, hidden := range metadata.Fields {
		if hidden.Name != field && strings.Contains(markup, `name="`+hidden.Name+`"`) {
			t.Fatal("HTML exposed an unselected model field", hidden.Name)
		}
	}
	token = headers.Get(runtime.CSRFHeader())
	formData := url.Values{field: {"  " + desired + " HTTP  "}, "_profile_revision": {"3"}, "csrfmiddlewaretoken": {token}}
	status, _, _ = send("POST", "/profile/", "application/x-www-form-urlencoded", formData.Encode(), "", "", "https://foreign.test")
	if status != 403 {
		t.Fatal("foreign-origin Form accepted", status)
	}
	current, err := reader.Get(t.Context(), actor, id)
	check(err)
	if current.Revision() != 3 {
		t.Fatal("CSRF denial changed account")
	}
	status, _, _ = send("POST", "/profile/", "application/x-www-form-urlencoded", formData.Encode(), "", "", "")
	if status != 204 {
		t.Fatal("HTTP ModelView form update failed", status)
	}
	status, headers, raw := send("GET", "/api/profile/", "", "", "", "", "")
	var selected map[string]string
	check(json.Unmarshal([]byte(raw), &selected))
	if status != 200 || headers.Get("X-Profile-Revision") != "4" || !reflect.DeepEqual(selected, map[string]string{field: desired + " HTTP"}) {
		t.Fatal("HTTP output selection differs", status, raw)
	}
	token = headers.Get(runtime.CSRFHeader())
	wire, err := json.Marshal(map[string]string{field: "  " + desired + " JSON  "})
	check(err)
	status, _, _ = send("PATCH", "/api/profile/", "application/json", string(wire), "", "4", "")
	if status != 403 {
		t.Fatal("JSON update omitted CSRF", status)
	}
	status, _, _ = send("PATCH", "/api/profile/", "application/json", `{}`, token, "4", "")
	if status != 204 {
		t.Fatal("HTTP JSON omitted field did not remain absent", status)
	}
	status, _, _ = send("PATCH", "/api/profile/", "application/json", string(wire), token, "4", "")
	if status != 204 {
		t.Fatal("HTTP typed JSON update failed", status)
	}
	status, _, _ = send("PATCH", "/api/profile/", "application/json", string(wire), token, "4", "")
	if status != 409 {
		t.Fatal("stale HTTP JSON accepted", status)
	}
	status, _, _ = send("POST", "/profile/", "application/x-www-form-urlencoded", formData.Encode(), "", "", "")
	if status != 409 {
		t.Fatal("stale HTTP form accepted", status)
	}
	for _, name := range []string{role.Password, role.PrincipalID, role.Revision, role.LastLogin, role.CreatedAt, role.Active.Field, role.Staff.Field, role.Superuser.Field} {
		if name == "" {
			continue
		}
		wire, err := json.Marshal(map[string]string{name: "forged"})
		check(err)
		status, _, _ = send("PATCH", "/api/profile/", "application/json", string(wire), token, "5", "")
		if status != 400 {
			t.Fatal("reserved HTTP JSON input accepted", name, status)
		}
	}
	current, err = reader.Get(t.Context(), actor, id)
	check(err)
	value, loaded := current.View().Get(field)
	stored, stringValue := value.String()
	if current.Revision() != 5 || !loaded || !stringValue || stored != desired+" JSON" {
		t.Fatal("HTTP denial/no-op changed durable profile")
	}
	// Permission changes preserve the credential stamp but must immediately
	// affect an already authenticated browser and the old in-memory actor.
	check(revoke(true))
	status, _, _ = send("GET", "/api/profile/", "", "", "", "", "")
	if status != 403 {
		t.Fatal("existing session retained revoked profile read authority", status)
	}
	status, _, _ = send("GET", "/profile/", "", "", "", "", "")
	if status != 403 {
		t.Fatal("existing session retained revoked form authority", status)
	}
	status, _, _ = send("PATCH", "/api/profile/", "application/json", `{"unparsed`, token, "5", "")
	if status != 403 {
		t.Fatal("revoked API permission reached JSON parsing", status)
	}
	if entries, err := reader.History(t.Context(), actor, id, 100); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || entries != nil {
		t.Fatal("old actor retained revoked history authority", err)
	}
	check(revoke(false))
	status, headers, raw = send("GET", "/api/profile/", "", "", "", "", "")
	if status != 200 || headers.Get("X-Profile-Revision") != "5" {
		t.Fatal("restored permission did not resolve on the existing session", status)
	}
	check(json.Unmarshal([]byte(raw), &selected))
	if !reflect.DeepEqual(selected, map[string]string{field: desired + " JSON"}) {
		t.Fatal("denied request changed selected profile", raw)
	}
	entries, err := reader.History(t.Context(), actor, id, 100)
	check(err)
	if len(entries) != 4 {
		t.Fatal("HTTP mutation/audit diverged", len(entries))
	}
	for _, entry := range entries {
		if !reflect.DeepEqual(entry.ChangedFields(), []string{field}) || entry.ActorID() != actor.ID() {
			t.Fatal("HTTP audit recorded wrong fields or actor")
		}
	}
}

func profileFormData(request *web.Request) (url.Values, error) {
	httpRequest := request.HTTP()
	httpRequest.Body = http.MaxBytesReader(nil, httpRequest.Body, 64<<10)
	if err := httpRequest.ParseForm(); err != nil {
		return nil, err
	}
	return httpRequest.PostForm, nil
}

func profileEndpointError(err error) error {
	var failure *accountmodel.Error
	if errors.As(err, &failure) {
		switch failure.Code {
		case accountmodel.CodeConflict:
			return endpoint.Reject(409, api.CodeValidationError, validation.Errors{})
		case accountmodel.CodePermission:
			return endpoint.Reject(403, api.CodePermissionDenied, validation.Errors{})
		}
	}
	return err
}
