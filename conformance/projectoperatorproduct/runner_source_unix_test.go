//go:build darwin || linux

package projectoperatorproduct_test

const (
	operatorSQLiteDatabaseEnvironment  = "GODJ_ARTICLE_SQLITE_DATABASE"
	operatorPostgresURLEnvironment     = "GODJ_ARTICLE_POSTGRES_URL"
	operatorPostgresSchemaEnvironment  = "GODJ_ARTICLE_POSTGRES_SCHEMA"
	operatorMarkerEnvironment          = "GODJ_OPERATOR_PRODUCT_MARKER"
	operatorRunnerMarkerEnvironment    = "GODJ_OPERATOR_PRODUCT_MARK_RUNNER"
	operatorHoldEnvironment            = "GODJ_OPERATOR_PRODUCT_HOLD_MILLISECONDS"
	operatorResponseModeEnvironment    = "GODJ_OPERATOR_PRODUCT_RESPONSE_MODE"
	operatorRetiredUsernameEnvironment = "GODJ_ARTICLE_ADMIN_USERNAME"
	operatorRetiredPasswordEnvironment = "GODJ_ARTICLE_ADMIN_PASSWORD"
)

const operatorModelDefinitionSource = `package modeldef

import (
	"context"
	"errors"

	"github.com/progresshans/godj/codegen"
	identitydef "github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/schema"
)

var definition = schema.Definition{
	AppLabel: "godj_conformance",
	Models: []schema.Model{{
		Name: "article",
		GoName: "Article",
		Fields: []schema.Field{
			schema.CharField("title", "Title", 200),
			schema.BooleanField("published", "Published", schema.Default(false)),
			schema.CharField("summary", "Summary", 200, schema.Nullable(), schema.Blank()),
			schema.SlugField("slug", "Slug", schema.Nullable(), schema.Blank(), schema.Unique(), schema.AllowUnicode(true)),
		},
	}},
}

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, errors.New("external operator project: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	appSchema, err := schema.Build(definition)
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	identityApp, err := identitydef.AppSpec()
	if err != nil { return codegen.ProjectSpec{}, err }
	const root = "example.com/godj-operator-product/"
	account := identitydef.AccountRole()
	return codegen.ProjectSpec{
		Account: &account,
		Project: codegen.PackageSpec{
			PackageName: "project",
			ImportPath: root + "project",
			Directory: "project",
		},
		Apps: []codegen.AppSpec{{
			Alias: "models",
			Package: codegen.PackageSpec{
				PackageName: "models",
				ImportPath: root + "models",
				Directory: "models",
			},
			Schema: appSchema,
		}, identityApp},
	}, nil
}
`

const operatorPolicySource = `// Package accounts owns this project's selected account and catalog policy.
// Change these typed declarations alongside modeldef when selecting another
// model. No policy here opens a database or supplies a default account row.
package operatorpolicy

import (
 "context"
	"example.com/godj-operator-product/project"
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
)

type Runtime = accountmodel.Runtime[models.User, models.Group, models.Permission]

func Policy() (accountmodel.RuntimeConfig[models.User, models.Group, models.Permission], error) {
	var result accountmodel.RuntimeConfig[models.User, models.Group, models.Permission]
	binding, err := project.BindAccount()
	if err != nil {
		return result, err
	}
	passwords, err := binding.DefaultPasswordValidator(
		accountmodel.PasswordAttribute{Field: "username", Label: "username"},
		accountmodel.PasswordAttribute{Field: "first_name", Label: "first name"},
		accountmodel.PasswordAttribute{Field: "last_name", Label: "last name"},
		accountmodel.PasswordAttribute{Field: "email", Label: "email address"},
	)
	if err != nil {
		return result, err
	}
	accountPermissions := accountmodel.AccountPermissions{View: identity.ViewUser, Add: identity.AddUser, Change: identity.ChangeUser, Delete: identity.DeleteUser}
	groupPermissions := accountmodel.CatalogPermissions{View: identity.ViewGroup, Add: identity.AddGroup, Change: identity.ChangeGroup, Delete: identity.DeleteGroup}
	permissionPermissions := accountmodel.CatalogPermissions{View: identity.ViewPermission, Add: identity.AddPermission, Change: identity.ChangePermission, Delete: identity.DeletePermission}
	fields := []string{"username", "email", "first_name", "last_name", "active", "staff", "superuser", "groups", "permissions"}
	result.Accounts = accountmodel.AccountManagementConfig[models.User]{
		ReadFields: fields, CreateFields: fields, EditFields: fields,
		Permissions:           accountPermissions,
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: groupPermissions.Delete, Change: groupPermissions.Change},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: permissionPermissions.Delete, Change: permissionPermissions.Change},
		MaxPasswordBytes:      1024, ValidatePassword: passwords,
	}
	result.Catalog = accountmodel.CatalogConfig[models.Group, models.Permission]{
		Group:              accountmodel.CatalogModelConfig[models.Group]{ReadFields: []string{"name", "permissions"}, WriteFields: []string{"name", "permissions"}, Permissions: groupPermissions},
		Permission:         accountmodel.CatalogModelConfig[models.Permission]{ReadFields: []string{"code", "name"}, WriteFields: []string{"code", "name"}, Permissions: permissionPermissions},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: accountPermissions.Delete, Change: accountPermissions.Change},
	}
	result.SessionStorage.MaxRecords = 64
 result.Accounts.PrincipalID = func(context.Context) (string, error) { return "external-article-operator", nil }
	return result, nil
}

// FirstAccountPolicy selects operator prompts explicitly. Server identity,
// active/staff/superuser and revision stay with the first-account owner. A
// custom model must include every additional required profile field here.
func FirstAccountPolicy() (accountmodel.CreationPolicy[models.User], error) {
	config, err := Policy()
	if err != nil {
		return accountmodel.CreationPolicy[models.User]{}, err
	}
	return accountmodel.CreationPolicy[models.User]{
		Fields: []string{"username"}, MaxPasswordBytes: config.Accounts.MaxPasswordBytes,
		ValidatePassword: config.Accounts.ValidatePassword, Validate: config.Accounts.ValidateCreate,
		CaseInsensitiveLogin: config.Accounts.CaseInsensitiveCreationLogin,
		PrincipalID:          config.Accounts.PrincipalID, Now: config.Accounts.Now,
	}, nil
}
`

const operatorProjectRunnerSource = `package main
import (
 "context"
 "errors"
 "fmt"
 "os"
 "example.com/godj-operator-product/modeldef"
 "github.com/progresshans/godj/examples/article/databaseconfig"
 "github.com/progresshans/godj/project"
 "github.com/progresshans/godj/project/compatibility"
 "github.com/progresshans/godj/settings/database"
)
const markerEnvironment = "GODJ_OPERATOR_PRODUCT_MARKER"
func main() {
 if handled, err := compatibility.Serve(context.Background(), os.Args[1:], os.Stdout); handled {
  if err != nil { os.Exit(1) }; return
 }
 // Declaration/source reads are not migration or first-account attempts.
 if os.Getenv("GODJ_OPERATOR_PRODUCT_MARK_RUNNER") == "1" && len(os.Args) == 2 && os.Args[1] == "__godj_project_migrate_runner_v2" {
  if err := appendMarker(); err != nil { fatal() }
 }
 selected, selectionErr := databaseconfig.FromEnvironment(os.LookupEnv)
 err := project.Run(context.Background(), project.Config{
  MigrationDefinitionRoots: []string{"migrations"},
  MigrationDefinitionSources: modeldef.MigrationSources(),
  LoadProjectSpec: modeldef.ProjectSpec,
  OpenMigrationBackend: func(ctx context.Context) (project.MigrationBackend, error) {
   if selectionErr != nil { return nil, selectionErr }
   return database.Open(ctx, selected)
  },
  MigrationSQLRenderer: selected.MigrationSQLRenderer(),
 }, os.Args[1:], os.Stdin, os.Stdout)
 if err != nil { fatal() }
}
func appendMarker() error {
	path := os.Getenv(markerEnvironment)
	if path == "" {
		return errors.New("operator marker path is empty")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "%d\n", os.Getpid())
	return errors.Join(writeErr, file.Close())
}

func fatal() {
	_, _ = fmt.Fprintln(os.Stderr, "external operator project failed")
	os.Exit(1)
}
`

const operatorApplicationSource = `package application

import (
	"context"
	"errors"
	"fmt"

	"example.com/godj-operator-product/operatorpolicy"
	"github.com/progresshans/godj/admin"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func New(ctx context.Context, backend database.Backend) (*web.Application, error) {
	if ctx == nil {
		return nil, errors.New("external operator application: nil context")
	}
 runtime, err := operatorpolicy.New(ctx, backend)
 if err != nil { return nil,err }
 startup,err := runtime.SessionStore().InspectStartup(ctx)
 if err != nil { return nil,err }
 if !startup.Ready() { return nil,errors.New("external operator account is not provisioned") }
	configured, err := settings.New(settings.Definition{
		ProjectName: "external_operator_product",
		InstalledApps: []apps.Config{{
			Name: "github.com/progresshans/godj/examples/article/models",
			Label: apiapp.Namespace,
		}},
	})
	if err != nil {
		return nil, err
	}
	service, err := adminapp.NewSelectedService(backend, runtime.Credentials(), auth.PrincipalAuthorizer{})
	if err != nil {
		return nil, err
	}
	builder := admin.NewBuilder(configured.Apps())
	if err := adminapp.RegisterArticle(builder, service); err != nil {
		return nil, err
	}
	registry, err := builder.Build()
	if err != nil {
		return nil, err
	}
	allowedNext, err := admin.SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		return nil, err
	}
	authRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions: runtime.Sessions(),
 LoginPersistence: runtime.LoginPersistence(),
		Authenticator: runtime.Authenticator(),
		Authorizer: auth.PrincipalAuthorizer{},
		SessionCookie: websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie: websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath: "/admin/login/",
		FallbackPath: "/admin/",
		AllowedNextPaths: allowedNext,
	})
	if err != nil {
		return nil, err
	}
	adminSite, err := admin.NewSite(admin.SiteConfig{
		Apps: configured.Apps(),
		Namespace: apiapp.Namespace,
		BasePath: "/admin",
		Registry: registry,
		Auth: authRuntime,
	})
	if err != nil {
		return nil, err
	}
	apiAuth, err := apisessionauth.New(authRuntime)
	if err != nil {
		return nil, err
	}
	articleAPI, err := apiapp.NewSelected(backend, runtime.Credentials(), auth.PrincipalAuthorizer{}, apiAuth)
	if err != nil {
		return nil, err
	}
	routes := append(adminSite.Routes(), articleAPI.Routes()...)
	application, err := webapp.NewComposedApplication(backend, routes, articleAPI.Middleware())
	if err != nil {
		return nil, fmt.Errorf("compose external operator application: %w", err)
	}
	return application, nil
}
`

const operatorSiteSource = `package main

import (
 "github.com/progresshans/godj/project/compatibility"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"example.com/godj-operator-product/application"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	projectserver "github.com/progresshans/godj/project/server"
	"github.com/progresshans/godj/settings/database"
)

const markerEnvironment = "GODJ_OPERATOR_PRODUCT_MARKER"

func main() {
 if handled, err := compatibility.Serve(context.Background(), os.Args[1:], os.Stdout); handled {
  if err != nil { os.Exit(1) }; return
 }
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "external operator site failed")
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, stdout io.Writer) error {
	selected, err := databaseconfig.FromEnvironment(os.LookupEnv)
	if err != nil {
		return errors.New("database configuration")
	}
	return projectserver.Serve(ctx, arguments, projectserver.Config{
		Start: func(start context.Context) (projectserver.Runtime, error) {
			if err := appendMarker(); err != nil { return projectserver.Runtime{}, err }
			backend, err := database.Open(start, selected)
			if err != nil { return projectserver.Runtime{}, errors.New("database open") }
			runtime := projectserver.Runtime{Stop: func(context.Context) error { return backend.Close() }}
			transferred := false
			defer func() { if !transferred { _ = runtime.Stop(context.WithoutCancel(start)) } }()
			runtime.Application, err = application.New(start, backend)
			transferred = true
			return runtime, err
		},
		Ready: func(_ context.Context, info projectserver.ListenerInfo) error {
			message := "external operator site listening on http://" + info.Address + "\n"
			n, err := io.WriteString(stdout, message)
			if err == nil && n != len(message) { return io.ErrShortWrite }
			return err
		},
	})
}

func appendMarker() error {
	path := os.Getenv(markerEnvironment)
	if path == "" {
		return errors.New("operator runtime marker path is empty")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "%d\n", os.Getpid())
	return errors.Join(writeErr, file.Close())
}
`

const operatorCommandsSource = `package main
import (
 "bytes"
 "context"
 "errors"
 "fmt"
 "io"
 "os"
 "os/signal"
 "strconv"
 "syscall"
 "time"
 "example.com/godj-operator-product/operatorpolicy"
 "example.com/godj-operator-product/project"
 "github.com/progresshans/godj/apps"
 "github.com/progresshans/godj/examples/article/databaseconfig"
 "github.com/progresshans/godj/project/command"
 "github.com/progresshans/godj/project/compatibility"
 "github.com/progresshans/godj/project/firstaccount"
 "github.com/progresshans/godj/settings/database"
 "golang.org/x/sys/unix"
)
const markerEnvironment = "GODJ_OPERATOR_PRODUCT_MARKER"
// Deliberate transport fault injection belongs only to the private provision
// invocation. Compatibility and description must stay resource-free.
const provisionArgument = "__godj_first_account_provision_v4"
type closeFailureBackend struct { database.Backend }
func (backend closeFailureBackend) Close() error {
 return errors.Join(backend.Backend.Close(), errors.New("external operator backend close failed"))
}
func main() {
 ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
 defer stop()
 brokenPipe := make(chan os.Signal, 1)
 signal.Notify(brokenPipe, syscall.SIGPIPE)
 defer signal.Stop(brokenPipe)
 if handled, err := compatibility.Serve(ctx, os.Args[1:], os.Stdout); handled {
  if err != nil { os.Exit(1) }; return
 }
 writer := io.Writer(os.Stdout)
 closeFailure := false
 if len(os.Args)==2 && os.Args[1]==provisionArgument {
  if err := appendMarker(); err != nil { fatal() }
  if milliseconds, err := strconv.Atoi(os.Getenv("GODJ_OPERATOR_PRODUCT_HOLD_MILLISECONDS")); err == nil && milliseconds > 0 && milliseconds <= 2000 {
   time.Sleep(time.Duration(milliseconds)*time.Millisecond)
  }
  mode := os.Getenv("GODJ_OPERATOR_PRODUCT_RESPONSE_MODE")
  switch mode {
  case "":
  case "broken-pipe", "backend-close-broken-pipe":
   closeFailure = mode == "backend-close-broken-pipe"
   if err := prepareBrokenStandardOutput(); err != nil { fatal() }
  case "abort", "empty", "malformed", "over-limit": writer = responseWriter{mode: mode}
  default: fatal()
  }
 }
 selected, selectionErr := databaseconfig.FromEnvironment(os.LookupEnv)
 first, err := operatorpolicy.FirstAccount(func(ctx context.Context) (firstaccount.Backend,error) {
  if selectionErr != nil { return nil,selectionErr }
  opened, err := database.Open(ctx,selected)
  if err != nil { return nil,err }
  if closeFailure { return closeFailureBackend{Backend: opened},nil }
  return opened,nil
 })
 if err != nil { fatal() }
 installed,err := apps.New(project.InstalledApps())
 if err != nil { fatal() }
 registry,err := command.New(command.Config{Apps:installed,FirstAccount:first,Check:func(context.Context)error{return selectionErr}})
 if err != nil { fatal() }
 if err := registry.Serve(ctx,os.Args[1:],command.Streams{Stdin:os.Stdin,Stdout:writer,Stderr:os.Stderr}); err != nil {
  _,_ = fmt.Fprintln(os.Stderr,command.Diagnostic(err))
  os.Exit(command.ExitCode(err))
 }
}
type responseWriter struct {
	mode string
}

func (writer responseWriter) Write(input []byte) (int, error) {
	switch writer.mode {
	case "abort":
		os.Exit(1)
		return 0, errors.New("unreachable response abort")
	case "empty":
		return len(input), nil
	case "malformed":
		document := bytes.Repeat([]byte{'x'}, len(input))
		written, err := os.Stdout.Write(document)
		if err != nil || written != len(document) {
			return 0, errors.New("malformed response write failed")
		}
		return len(input), nil
	case "over-limit":
		document := bytes.Repeat([]byte{'x'}, 4097)
		written, err := os.Stdout.Write(document)
		if err != nil || written != len(document) {
			return 0, errors.New("over-limit response write failed")
		}
		return len(input), nil
	default:
		return 0, errors.New("invalid response failure mode")
	}
}

func prepareBrokenStandardOutput() error {
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	if err := reader.Close(); err != nil {
		_ = writer.Close()
		return err
	}
	if err := unix.Dup2(int(writer.Fd()), int(os.Stdout.Fd())); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

func appendMarker() error {
	path := os.Getenv(markerEnvironment)
	if path == "" {
		return errors.New("operator marker path is empty")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "%d\n", os.Getpid())
	return errors.Join(writeErr, file.Close())
}

func fatal() {
	_, _ = fmt.Fprintln(os.Stderr, "external operator project failed")
	os.Exit(1)
}
`

const operatorRuntimeSource = `package operatorpolicy

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"

	"github.com/progresshans/godj/auth"
	"example.com/godj-operator-product/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/project/firstaccount"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// Keep callback policy in policy.go so a changed declaration also changes the
// first-account evidence. The generated definition separately binds the full
// schema, role, prompt fields and password envelope. This digest identifies the
// host declaration; it does not prove arbitrary callback behavior.
//
//go:embed policy.go
var policySource []byte

func New(ctx context.Context, backend sessiondb.Backend) (*Runtime, error) {
	config, err := Policy()
	if err != nil {
		return nil, err
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return nil, err
	}
	return project.NewAccountRuntime(ctx, backend, hasher, auth.PrincipalAuthorizer{}, config)
}

func FirstAccount(open func(context.Context) (firstaccount.Backend, error)) (firstaccount.Command, error) {
	if open == nil {
		return firstaccount.Command{}, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}
	}
	runtimeConfig, err := Policy()
	if err != nil {
		return firstaccount.Command{}, err
	}

	policy, err := FirstAccountPolicy()
	if err != nil {
		return firstaccount.Command{}, err
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return firstaccount.Command{}, err
	}
	digest := sha256.Sum256(policySource)
	definition, err := project.NewFirstAccountDefinition(hasher, accountmodel.FirstAccountConfig[models.User]{
		CreationPolicy: policy, EvidenceSHA256: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		return firstaccount.Command{}, err
	}
	return firstaccount.New(definition, func(ctx context.Context) (firstaccount.Backend, error) {
		backend, err := open(ctx)
		if err != nil || backend == nil {
			return backend, err
		}
		native, ok := backend.(sessiondb.Backend)
		if !ok {
			return backend, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}
		}
		store, err := project.NewAccountSessionStore(native, runtimeConfig.SessionStorage)
		if err != nil {
			return backend, err
		}
		_, err = store.InspectStartup(ctx)
		return backend, err
	}, firstaccount.Config{})
}
`

const operatorMigrationSources = `package modeldef

import (
	"github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/identity"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// MigrationSources explicitly installs the selected external identity models
// and the independent account-domain, audit and session storage. When selecting
// project-owned account models, replace identity.MigrationSources with their
// reviewed project migrations; keep the three framework storage apps.
// Changing this declaration never adopts or converts an existing account DB.
func MigrationSources() []migrationdefinition.Source {
	sources := identity.MigrationSources()
	sources = append(sources, domain.MigrationSources()...)
	sources = append(sources, audit.MigrationSources()...)
	return append(sources, sessiondb.MigrationSources()...)
}
`
