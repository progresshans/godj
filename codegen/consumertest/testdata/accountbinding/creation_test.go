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
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/api"
	apiinput "github.com/progresshans/godj/api/input"
	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
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

func creationConsumerConfig[M any](role ir.AccountRole, profile string) accountmodel.CreationConfig[M] {
	return accountmodel.CreationConfig[M]{CreationPolicy: accountmodel.CreationPolicy[M]{Fields: []string{role.Login, profile, role.Groups, role.Permissions}}, AddPermission: "account.shared", ChangePermission: "account.team"}
}

// A generated default/custom creator consumes the same real ModelForm and JSON
// model binding over TCP. Password is an explicit command input, never a model
// hash field. These consumer handlers compose existing public transport APIs;
// shared Admin/HTTP installation remains a separate product integration.
func assertAccountCreationHTTP[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], creator *accountmodel.Creator[M], authenticator *accountmodel.Authenticator[M], store *accountmodel.SessionStore[M], actor auth.Principal, actorLogin, profileField string, groupID, permissionID int64) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	objects := creator.Manager()
	metadata, err := objects.Metadata()
	check(err)
	role, err := binding.Role()
	check(err)
	formDefinition, err := binding.FormDefinition(creator.Fields())
	check(err)
	form, err := formDefinition.Spec(metadata)
	check(err)
	form, err = form.WithModelChoices(role.Groups, forms.Choice{Value: forms.Integer(groupID), Label: "Operators"})
	check(err)
	form, err = form.WithModelChoices(role.Permissions, forms.Choice{Value: forms.Integer(permissionID), Label: "Direct grant"})
	check(err)
	fields := make([]serializers.ModelField, 0, len(creator.Fields()))
	for _, name := range creator.Fields() {
		selection := serializers.ModelField{Name: name}
		for _, field := range metadata.Fields {
			if field.Name == name {
				switch field.Kind {
				case ir.FieldChar, ir.FieldEmail, ir.FieldURL, ir.FieldSlug, ir.FieldText:
					selection.AllowEmpty = field.Blank
				}
			}
		}
		fields = append(fields, selection)
	}
	spec, err := binding.InputSpec(fields...)
	check(err)
	body, err := apiinput.NewModel(objects, spec, api.ParserConfig{})
	check(err)
	password, err := serializers.StringField("password", serializers.WithTrimWhitespace(false), serializers.WithMaxLength(4096))
	check(err)
	passwordSpec, err := serializers.NewSpec([]serializers.Field{password})
	check(err)
	type passwordCommand struct{ Password apiinput.Presence[string] }
	var jsonSetters, jsonSaves atomic.Int64
	command, err := apiinput.New(passwordSpec, api.ParserConfig{}, apiinput.Field("password", apiinput.String(), func(command *passwordCommand, value apiinput.Presence[string]) {
		jsonSetters.Add(1)
		command.Password = value
	}))
	check(err)
	type creationRequest struct {
		Fields     orm.ModelInput[M]
		Credential passwordCommand
	}
	creationBody, err := apiinput.Object(api.ParserConfig{MaxBodyBytes: 64 * 1024, JSONLimits: serializers.Limits{MaxStringBytes: 4096}},
		apiinput.ObjectField("fields", body, func(request *creationRequest, value orm.ModelInput[M]) { jsonSetters.Add(1); request.Fields = value }),
		apiinput.ObjectField("credential", command, func(request *creationRequest, value passwordCommand) { jsonSetters.Add(1); request.Credential = value }),
	)
	check(err)
	manager, err := sessions.NewManager(store, sessions.Config{})
	check(err)
	owner, err := store.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: owner,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/login/", FallbackPath: "/create-form/", AllowedNextPaths: []string{"/create-form/", "/create-json/"}})
	check(err)
	jsonAuthentication, err := apisession.New(runtime, apisession.WithReadOnlyResolution())
	check(err)
	save := func(request *web.Request, current auth.Principal, input orm.ModelInput[M], password string) (web.Response, error) {
		prepared, err := creator.Prepare(request.Context(), current, input, password)
		var result accountmodel.Creation
		if err == nil {
			result, err = creator.Commit(request.Context(), current, prepared)
		}
		if _, rejected := validation.Rejected(err); rejected {
			return web.HTML(400, []byte("Invalid account"))
		}
		if errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
			return web.HTML(403, []byte("Permission required"))
		}
		if err != nil {
			return web.Response{}, err
		}
		wire, err := json.Marshal(result)
		if err != nil {
			return web.Response{}, err
		}
		return web.NewResponse(201, http.Header{"Content-Type": {api.JSONContentType}}, wire)
	}
	jsonHandler, err := jsonAuthentication.Require("account.shared", func(request *web.Request, current auth.Principal) (web.Response, error) {
		value, failures, err := creationBody.Parse(request, serializers.ModeFull)
		if err != nil {
			if response, handled, responseErr := api.RequestErrorResponse(err); handled {
				return response, responseErr
			}
			return web.Response{}, err
		}
		if !failures.Empty() {
			return api.ErrorResponse(400, api.CodeValidationError, failures)
		}
		password, present := value.Credential.Password.Get()
		if !present {
			return web.Response{}, errors.New("prepared credential command omitted required password")
		}
		jsonSaves.Add(1)
		return save(request, current, value.Fields, password)
	}, "account.team")
	check(err)
	routes := []web.Route{
		{Name: "account:create_login_form", Method: "GET", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
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
		{Name: "account:create_login", Method: "POST", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
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
		{Name: "account:create_form", Method: "POST", Path: "/create-form/", Handler: runtime.Require("account.team", func(request *web.Request, current auth.Principal) (web.Response, error) {
			data, err := profileFormData(request)
			if err != nil {
				return web.Response{}, err
			}
			if err := runtime.VerifyCSRF(request, data["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, []byte("CSRF rejected"))
			}
			if len(data["new_password"]) != 1 {
				return web.HTML(400, []byte("Password required"))
			}
			password := data.Get("new_password")
			data.Del("new_password")
			data.Del("csrfmiddlewaretoken")
			bound, err := formmodel.BindInstance(request.Context(), objects, form, forms.NewData(data), nil, formmodel.PostClean{})
			if err != nil {
				return web.Response{}, err
			}
			if !bound.BoundForm().Form().Valid() {
				return web.HTML(400, []byte("Invalid account form"))
			}
			input, err := formmodel.PrepareInput(objects, bound.BoundForm())
			if err != nil {
				return web.Response{}, err
			}
			return save(request, current, input, password)
		})},
		{Name: "account:create_json", Method: "POST", Path: "/create-json/", Handler: jsonHandler},
	}
	configured, err := settings.New(settings.Definition{ProjectName: "account_creation_consumer", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "account"})})
	check(err)
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: routes})
	check(err)
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		check(err)
		return &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	send := func(client *http.Client, method, path, contentType, raw, token string) (int, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(raw))
		check(err)
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.Header.Set(runtime.CSRFHeader(), token)
		}
		response, err := client.Do(request)
		check(err)
		data, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		if strings.Contains(string(data), accountPasswordText) || strings.Contains(string(data), actor.ID()) {
			t.Fatal("creation HTTP exposed private input")
		}
		return response.StatusCode, string(data)
	}
	login := func(client *http.Client, name string) string {
		status, token := send(client, "GET", "/login/", "", "", "")
		if status != 200 || len(token) != 128 {
			t.Fatal("creation login CSRF", status)
		}
		data := url.Values{"username": {name}, "password": {accountPasswordText}, "csrfmiddlewaretoken": {token}}
		status, _ = send(client, "POST", "/login/", "application/x-www-form-urlencoded", data.Encode(), "")
		if status != 204 {
			t.Fatal("created account HTTP login", status)
		}
		status, token = send(client, "GET", "/login/", "", "", "")
		if status != 200 {
			t.Fatal(status)
		}
		return token
	}
	client := newClient()
	token := login(client, actorLogin)
	writeAlias := func(value string) string {
		if role.WriteLoginNormalization != ir.AccountUnicode16NFKC {
			return value
		}
		return strings.Map(func(char rune) rune {
			if char >= '!' && char <= '~' {
				return char + 0xfee0
			}
			return char
		}, value)
	}
	data := url.Values{role.Login: {writeAlias("created-form@example.test")}, profileField: {"  NZ  "}, role.Groups: {strconv.FormatInt(groupID, 10)}, role.Permissions: {strconv.FormatInt(permissionID, 10)}, "new_password": {accountPasswordText}, "csrfmiddlewaretoken": {token}}
	status, raw := send(client, "POST", "/create-form/", "application/x-www-form-urlencoded", data.Encode(), "")
	if status != 201 {
		t.Fatal("typed form account creation", status, raw)
	}
	var formResult accountmodel.Creation
	check(json.Unmarshal([]byte(raw), &formResult))
	encode := func(data map[string]any) string { wire, err := json.Marshal(data); check(err); return string(wire) }
	jsonData := map[string]any{"fields": map[string]any{role.Login: writeAlias("created-json@example.test"), profileField: "  NZ  ", role.Groups: []int64{groupID}, role.Permissions: []int64{permissionID}}, "credential": map[string]any{"password": accountPasswordText}}
	status, raw = send(client, "POST", "/create-json/", api.JSONContentType, encode(jsonData), token)
	if status != 201 {
		t.Fatal("typed JSON account creation", status, raw)
	}
	var jsonResult accountmodel.Creation
	check(json.Unmarshal([]byte(raw), &jsonResult))
	keyField := orm.NewAutoField[M](metadata.Fields[0])
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			keyField = orm.NewAutoField[M](field)
		}
	}
	for _, result := range []accountmodel.Creation{formResult, jsonResult} {
		if result.Revision != 1 {
			t.Fatal("created revision differs")
		}
		row, present, err := objects.Using(backend).Filter(keyField.Exact(result.ID)).OrderBy(keyField.Asc()).First(t.Context())
		check(err)
		if !present {
			t.Fatal("created typed row missing")
		}
		values, err := objects.ModelValues(row)
		check(err)
		storedLogin, _ := values[role.Login].String()
		expectedLogin := "created-form@example.test"
		if result.ID == jsonResult.ID {
			expectedLogin = "created-json@example.test"
		}
		if storedLogin != expectedLogin {
			t.Fatal("write normalization did not reach the selected stored login")
		}
		extra, _ := values[profileField].String()
		if extra != "NZ" {
			t.Fatal("custom required profile/cleaning lost")
		}
		if role.LastLogin != "" && !values[role.LastLogin].IsNull() {
			t.Fatal("creation implicitly logged in")
		}
		principal, _ := values[role.PrincipalID].String()
		secret, _ := values[role.Password].String()
		if principal == "" || secret == "" || secret == accountPasswordText {
			t.Fatal("server credential not generated")
		}
		namespace, _ := actor.CredentialDomain()
		var entries []accountaudit.Entry
		check(backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
			var err error
			entries, err = accountaudit.HistoryIn(t.Context(), scope, namespace, role.Model.AppLabel+"."+role.Model.ModelName, result.ID, 100)
			return err
		}))
		if len(entries) != 1 || entries[0].ActorID() != actor.ID() {
			t.Fatal("creation audit missing")
		}
	}
	before, err := objects.Using(backend).Count(t.Context())
	check(err)
	for _, mode := range []string{"duplicate", "reserved", "missing_choice", "empty_password", "required_profile", "missing_fields", "missing_credential", "null_fields", "null_credential", "unknown_envelope", "unknown_command", "csrf_before_parse", "malformed_before_setters", "raw_login_bytes"} {
		if mode == "required_profile" {
			required := false
			for _, field := range metadata.Fields {
				if field.Name == profileField {
					required = !field.Blank && field.Default == nil
				}
			}
			if !required {
				continue
			}
		}
		t.Run(mode, func(t *testing.T) {
			badFields := map[string]any{role.Login: "rejected-" + mode + "@example.test", profileField: "NZ", role.Groups: []int64{groupID}, role.Permissions: []int64{permissionID}}
			badCredential := map[string]any{"password": accountPasswordText}
			bad := map[string]any{"fields": badFields, "credential": badCredential}
			wantStatus, beforeOwner := 400, true
			switch mode {
			case "duplicate":
				badFields[role.Login] = "created-json@example.test"
				beforeOwner = false
			case "reserved":
				badFields[role.PrincipalID] = "forged"
			case "missing_choice":
				badFields[role.Groups] = []int64{9223372036854775807}
				beforeOwner = false
			case "empty_password":
				badCredential["password"] = ""
			case "raw_login_bytes":
				badFields[role.Login] = strings.Repeat(" ", auth.MaximumUsernameBytes) + "short@example.test"
			case "required_profile":
				delete(badFields, profileField)
			case "missing_fields":
				delete(bad, "fields")
			case "missing_credential":
				delete(bad, "credential")
			case "null_fields":
				bad["fields"] = nil
			case "null_credential":
				bad["credential"] = nil
			case "unknown_envelope":
				bad[role.PrincipalID] = "forged"
			case "unknown_command":
				badCredential["unexpected"] = "forged"
			case "csrf_before_parse":
				wantStatus = 403
			}
			wire, proof := encode(bad), token
			if mode == "csrf_before_parse" {
				wire, proof = "malformed JSON", ""
			}
			if mode == "malformed_before_setters" {
				wire = `{"fields":{},"fields":{}}`
			}
			setters, saves := jsonSetters.Load(), jsonSaves.Load()
			status, _ = send(client, "POST", "/create-json/", api.JSONContentType, wire, proof)
			if status != wantStatus {
				t.Fatal("invalid account JSON accepted", mode, status)
			}
			if beforeOwner && (jsonSetters.Load() != setters || jsonSaves.Load() != saves) {
				t.Fatal("invalid envelope reached a setter or creation owner")
			}
		})
	}
	after, err := objects.Using(backend).Count(t.Context())
	check(err)
	if after != before {
		t.Fatal("rejected creation left partial account")
	}
	// This browser logs in through the real DB login owner using the new hash.
	login(newClient(), "created-form@example.test")
}
