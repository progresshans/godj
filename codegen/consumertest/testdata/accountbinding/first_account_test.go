package consumer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	projectcommand "github.com/progresshans/godj/project/command"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

func firstConsumerConfig[M any](role ir.AccountRole, profile string) accountmodel.FirstAccountConfig[M] {
	digest := sha256.Sum256([]byte("independent generated first-account declaration " + role.Model.AppLabel + "." + role.Model.ModelName))
	return accountmodel.FirstAccountConfig[M]{CreationPolicy: accountmodel.CreationPolicy[M]{Fields: []string{role.Login, profile}}, EvidenceSHA256: hex.EncodeToString(digest[:])}
}

type firstProfileInput struct {
	present        bool
	text, expected string
}

func assertFirstAccountConsumer[M any](t *testing.T, backend database.Backend, selected database.Config, binding accountmodel.Binding[M], definition accountmodel.FirstAccountDefinition[M], hasher auth.PasswordHasher, store *accountmodel.CredentialStore[M], sessionStore *accountmodel.SessionStore[M], profileField string, profile firstProfileInput) M {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	owner, err := definition.Bind(backend)
	check(err)
	objects := definition.Manager()
	metadata, err := objects.Metadata()
	check(err)
	role, err := binding.Role()
	check(err)
	form, err := formmodel.NewSpecForFields(metadata, owner.Fields())
	check(err)
	raw := map[string][]string{role.Login: {"first@example.test"}}
	if profile.present {
		raw[profileField] = []string{profile.text}
	}
	data := forms.NewData(raw)
	bound, err := formmodel.BindInstance(t.Context(), objects, form, data, nil, formmodel.PostClean{})
	check(err)
	input, err := formmodel.PrepareInput(objects, bound.BoundForm())
	check(err)
	for _, field := range metadata.Fields {
		if field.Name == profileField && !field.Blank && field.Default == nil {
			missing, err := formmodel.BindInstance(t.Context(), objects, form, forms.NewData(map[string][]string{role.Login: {"missing@example.test"}}), nil, formmodel.PostClean{})
			check(err)
			if missing.BoundForm().Form().Valid() || missing.BoundForm().Form().Errors().ByField(validation.Field(profileField)).Empty() {
				t.Fatal("required custom first profile disappeared")
			}
		}
	}
	operation := sha256.Sum256([]byte("parent-owned generated first-account operation"))
	operationID := hex.EncodeToString(operation[:])
	opens, closes := 0, 0
	command, err := firstaccount.New(definition, func(ctx context.Context) (firstaccount.Backend, error) {
		opens++
		opened, err := database.Open(ctx, selected)
		if err != nil {
			return nil, err
		}
		return &firstCommandBackend{Backend: opened, closes: &closes}, nil
	})
	check(err)
	description, err := command.Description()
	check(err)
	check(firstaccount.ValidateDescription(description))
	if description.Login != role.Login || description.Model != role.Model.AppLabel+"."+role.Model.ModelName || len(description.Fields) != len(definition.Fields()) {
		t.Fatal("command description substituted a default account shape")
	}
	commandValues := make([]firstaccount.Value, len(description.Fields))
	for i, field := range description.Fields {
		text, present := data.Get(field.Name)
		if present && len(text) != 1 {
			t.Fatal("command did not describe the actual form input")
		}
		commandValues[i] = firstaccount.Value{Name: field.Name, Present: present}
		if present {
			commandValues[i].Text = []byte(text[0])
		}
		if field.Name == profileField && !profile.present && !field.HasDefault {
			t.Fatal("command description lost the declared default")
		}
	}
	for i, field := range description.Fields {
		if field.Name == profileField && len(field.Choices) != 0 {
			if len(field.Choices) != 2 || field.Choices[0].Value != "NZ" || field.Choices[0].Label != "New Zealand" {
				t.Fatal("command description lost typed profile choices")
			}
			bad := slices.Clone(commandValues)
			bad[i].Text = []byte("  NZ  ")
			_, err := command.Prepare(t.Context(), description.Fingerprint, bad)
			if failures, rejected := validation.Rejected(err); !rejected || failures.ByField(validation.Field(profileField)).Empty() {
				t.Fatal("command normalized exact model choice input", err)
			}
		}
		if field.Name == profileField && field.Required && !field.HasDefault {
			missing := slices.Clone(commandValues)
			missing[i].Present, missing[i].Text = false, nil
			_, err := command.Prepare(t.Context(), description.Fingerprint, missing)
			if failures, rejected := validation.Rejected(err); !rejected || failures.ByField(validation.Field(profileField)).Empty() {
				t.Fatal("command lost required custom profile validation", err)
			}
		}
	}
	pending, err := command.Prepare(t.Context(), description.Fingerprint, commandValues)
	check(err)
	pending.Clear()
	if opens != 0 {
		t.Fatal("model form description/cleaning opened a database")
	}
	if count, err := objects.Using(backend).Count(t.Context()); err != nil || count != 0 {
		t.Fatal("first preparation wrote a row", err)
	}
	assertFirstCommandRuntime(t, command, description, operationID, commandValues)
	for _, value := range commandValues {
		clear(value.Text)
	}
	if opens != 1 || closes != 1 {
		t.Fatal("runtime did not own exactly one real connection")
	}
	if count, err := objects.Using(backend).Count(t.Context()); err != nil || count != 1 {
		t.Fatal("runtime did not commit exactly one selected account", err)
	}
	key := orm.NewAutoField[M](metadata.Fields[0])
	keyName := metadata.Fields[0].Name
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			key = orm.NewAutoField[M](field)
			keyName = field.Name
		}
	}
	row, present, err := objects.Using(backend).OrderBy(key.Asc()).First(t.Context())
	check(err)
	if !present {
		t.Fatal("typed first row missing")
	}
	values, err := objects.ModelValues(row)
	check(err)
	rowID, keyOK := values[keyName].Integer()
	if !keyOK {
		t.Fatal("selected account key lost")
	}
	if revision, ok := values[role.Revision].Integer(); !ok || revision != 1 {
		t.Fatal("first account revision missing")
	}
	if value, _ := values[profileField].String(); value != profile.expected {
		t.Fatal("first profile/default/cleaning mismatch")
	}
	principal, _ := values[role.PrincipalID].String()
	if principal == "" {
		t.Fatal("first principal not generated")
	}
	if role.LastLogin != "" && !values[role.LastLogin].IsNull() {
		t.Fatal("bootstrap implicitly logged in")
	}
	authenticator, err := accountmodel.NewAuthenticator(t.Context(), store, hasher)
	check(err)
	credential, err := authenticator.Authenticate(t.Context(), "first@example.test", accountPasswordText)
	check(err)
	actor := credential.Principal()
	if !actor.Active() || !actor.Staff() || !actor.Superuser() || actor.ID() != principal {
		t.Fatal("first command role meaning changed")
	}
	namespace, _ := actor.CredentialDomain()
	var entries []accountaudit.Entry
	check(backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		var err error
		entries, err = accountaudit.HistoryIn(t.Context(), scope, namespace, role.Model.AppLabel+"."+role.Model.ModelName, rowID, 100)
		return err
	}))
	if len(entries) != 1 || entries[0].ActorID() != principal {
		t.Fatal("first audit actor lost selected domain")
	}
	receipt, err := domain.Lookup(t.Context(), backend, operationID)
	check(err)
	expectedDomain, err := binding.CredentialDomain(receipt.Generation())
	check(err)
	if receipt.OperationID() != operationID || receipt.Sequence() != 1 || receipt.RoleFingerprint() != description.RoleSHA256 || receipt.EvidenceSHA256() != description.EvidenceSHA256 || expectedDomain.Identifier() != namespace.Identifier() {
		t.Fatal("first operation receipt lost selected ownership")
	}
	if _, err := owner.Prepare(t.Context(), input, accountPasswordText); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFresh}) {
		t.Fatal("first operation implicitly adopted existing state", err)
	}
	assertFirstLoginHTTP(t, sessionStore, authenticator)
	return row
}

type firstCommandBackend struct {
	database.Backend
	closes *int
}

func (backend *firstCommandBackend) Close() error { *backend.closes++; return backend.Backend.Close() }
func (backend *firstCommandBackend) CoordinatedAtomicRelation(ctx context.Context, write func(db.RelationSession) error) error {
	return backend.Backend.(db.CoordinatedRelationAtomic).CoordinatedAtomicRelation(ctx, write)
}

// This external generated module exercises the runtime's actual private wire
// without importing framework-internal codecs or borrowing its live DB handle.
// The parent retains the operation before the single sensitive handoff.
func assertFirstCommandRuntime(t *testing.T, command firstaccount.Command, description firstaccount.Description, operation string, values []firstaccount.Value) {
	t.Helper()
	registry, err := projectcommand.New(projectcommand.Config{FirstAccount: command})
	if err != nil {
		t.Fatal(err)
	}
	var output, stderr bytes.Buffer
	if err := registry.Serve(t.Context(), []string{"__godj_first_account_describe_v2"}, projectcommand.Streams{Stdin: firstNoInput{}, Stdout: &output, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	var described struct {
		Version     uint64                   `json:"version"`
		Description firstaccount.Description `json:"description"`
		Code        string                   `json:"code"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&described); err != nil {
		t.Fatal(err)
	}
	if described.Version != 2 || described.Code != "" || described.Description.Fingerprint != description.Fingerprint || firstaccount.ValidateDescription(described.Description) != nil {
		t.Fatal("runtime changed the selected declaration")
	}
	var request bytes.Buffer
	request.WriteString("GODJFA2")
	request.WriteString(described.Description.Fingerprint)
	request.WriteString(operation)
	request.WriteByte(byte(len(values)))
	if err := binary.Write(&request, binary.BigEndian, uint16(len(accountPasswordText))); err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		request.WriteByte(byte(len(value.Name)))
		if value.Present {
			request.WriteByte(1)
		} else {
			request.WriteByte(0)
		}
		if err := binary.Write(&request, binary.BigEndian, uint16(len(value.Text))); err != nil {
			t.Fatal(err)
		}
		request.WriteString(value.Name)
		request.Write(value.Text)
	}
	request.WriteString(accountPasswordText)
	frame := request.Bytes()
	defer clear(frame)
	output.Reset()
	if err := registry.Serve(t.Context(), []string{"__godj_first_account_provision_v2"}, projectcommand.Streams{Stdin: bytes.NewReader(frame), Stdout: &output, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Version       uint64 `json:"version"`
		OperationID   string `json:"operation_id"`
		Outcome       string `json:"outcome"`
		Code          string `json:"code"`
		CleanupFailed bool   `json:"cleanup_failed"`
	}
	decoder = json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Version != 2 || result.OperationID != operation || result.Outcome != "created" || result.Code != "" || result.CleanupFailed || stderr.Len() != 0 {
		t.Fatal("runtime did not publish its confirmed outcome", result)
	}
	if strings.Contains(output.String(), accountPasswordText) || strings.Contains(output.String(), "first@example.test") {
		t.Fatal("runtime exposed input")
	}
}

type firstNoInput struct{}

func (firstNoInput) Read([]byte) (int, error) { panic("description read private input") }

func assertFirstLoginHTTP[M any](t *testing.T, store *accountmodel.SessionStore[M], authenticator *accountmodel.Authenticator[M]) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	check(err)
	owner, err := store.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: owner,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/login/", FallbackPath: "/manage/", AllowedNextPaths: []string{"/manage/"}})
	check(err)
	routes := []web.Route{
		{Name: "first:csrf", Method: "GET", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
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
		{Name: "first:login", Method: "POST", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			raw := request.HTTP()
			if err := raw.ParseForm(); err != nil {
				return web.HTML(400, []byte("Invalid form"))
			}
			if err := runtime.VerifyCSRF(request, raw.PostForm["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, []byte("CSRF rejected"))
			}
			result, err := runtime.Login(request, raw.PostForm.Get("username"), raw.PostForm.Get("password"))
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(204, nil, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
		{Name: "first:manage", Method: "GET", Path: "/manage/", Handler: runtime.Require("bootstrap.manage", func(_ *web.Request, actor auth.Principal) (web.Response, error) {
			if !actor.Staff() || !actor.Superuser() {
				return web.HTML(403, []byte("Invalid first role"))
			}
			return web.HTML(200, []byte("First account authenticated"))
		})},
	}
	config, err := settings.New(settings.Definition{ProjectName: "first_account_consumer", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "first"})})
	check(err)
	app, err := web.NewApplication(web.Config{Settings: config, Routes: routes})
	check(err)
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	check(err)
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	send := func(method, path, body string) (int, string) {
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
		check(err)
		if method == "POST" {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		response, err := client.Do(request)
		check(err)
		data, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		return response.StatusCode, string(data)
	}
	status, token := send("GET", "/login/", "")
	if status != 200 || len(token) != 128 {
		t.Fatal("first HTTP CSRF", status)
	}
	data := url.Values{"username": {"first@example.test"}, "password": {accountPasswordText}, "csrfmiddlewaretoken": {token}}
	status, _ = send("POST", "/login/", data.Encode())
	if status != 204 {
		t.Fatal("first HTTP login", status)
	}
	status, _ = send("GET", "/manage/", "")
	if status != 200 {
		t.Fatal("first account lost HTTP superuser role", status)
	}
	location, err := url.Parse(server.URL)
	check(err)
	found := false
	for _, cookie := range jar.Cookies(location) {
		if cookie.Name == websession.DefaultSessionCookieName {
			id, err := sessions.ParseID(cookie.Value)
			check(err)
			record, present, err := store.Load(t.Context(), id)
			check(err)
			found = present && record.ID() == id
		}
	}
	if !found {
		t.Fatal("first login did not persist selected DB session")
	}
}
