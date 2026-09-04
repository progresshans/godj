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
			schema.CharField("summary", "Summary", 200, schema.Nullable()),
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
	const root = "example.com/godj-operator-product/"
	return codegen.ProjectSpec{
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
		}},
	}, nil
}
`

const operatorPolicySource = `package operatorpolicy

import (
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
)

const PrincipalID = "external-article-operator"

func CredentialPolicy() (systemstate.CredentialPolicy, error) {
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID: PrincipalID,
		Active: true,
		Permissions: []auth.Permission{
			admin.DefaultAccessPermission,
			articleapp.ArticleViewPermission,
			articleapp.ArticleAddPermission,
			articleapp.ArticleChangePermission,
			articleapp.ArticleDeletePermission,
		},
	})
	if err != nil {
		return systemstate.CredentialPolicy{}, err
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return systemstate.CredentialPolicy{}, err
	}
	return systemstate.CredentialPolicy{Principal: principal, PasswordHasher: hasher}, nil
}

func RuntimeConfig() (systemstate.RuntimeConfig, error) {
	policy, err := CredentialPolicy()
	if err != nil {
		return systemstate.RuntimeConfig{}, err
	}
	return systemstate.RuntimeConfig{
		CredentialPolicy: policy,
		SessionLimits: sessions.DefaultLimits(),
		MaxSessions: 64,
		AuditCapacity: 256,
	}, nil
}
`

const operatorProjectRunnerSource = `package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"example.com/godj-operator-product/modeldef"
	"example.com/godj-operator-product/operatorpolicy"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/project"
	"github.com/progresshans/godj/systemstate"
	"golang.org/x/sys/unix"
)

const (
	markerEnvironment = "GODJ_OPERATOR_PRODUCT_MARKER"
	runnerMarkerEnvironment = "GODJ_OPERATOR_PRODUCT_MARK_RUNNER"
	holdEnvironment = "GODJ_OPERATOR_PRODUCT_HOLD_MILLISECONDS"
	responseModeEnvironment = "GODJ_OPERATOR_PRODUCT_RESPONSE_MODE"
)

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

type closeFailureBackend struct {
	project.SystemStateBackend
}

func (backend closeFailureBackend) Close() error {
	return errors.Join(backend.SystemStateBackend.Close(), errors.New("external operator backend close failed"))
}

func main() {
	if os.Getenv(runnerMarkerEnvironment) == "1" {
		if err := appendMarker(); err != nil {
			fatal()
		}
	}
	if milliseconds, err := strconv.Atoi(os.Getenv(holdEnvironment)); err == nil && milliseconds > 0 && milliseconds <= 2000 {
		time.Sleep(time.Duration(milliseconds) * time.Millisecond)
	}
	selected, selectionErr := databaseconfig.FromEnvironment(os.LookupEnv)
	policy, policyErr := operatorpolicy.CredentialPolicy()
	writer := io.Writer(os.Stdout)
	mode := os.Getenv(responseModeEnvironment)
	closeFailure := false
	switch mode {
	case "":
	case "broken-pipe":
		if err := prepareBrokenStandardOutput(); err != nil {
			fatal()
		}
	case "backend-close-broken-pipe":
		closeFailure = true
		if err := prepareBrokenStandardOutput(); err != nil {
			fatal()
		}
	case "abort", "empty", "malformed", "over-limit":
		writer = responseWriter{mode: mode}
	default:
		fatal()
	}
	err := project.Run(context.Background(), project.Config{
		MigrationDefinitionRoots: []string{"migrations"},
		MigrationDefinitionSources: []definition.Source{systemstate.InitialDefinitionSource()},
		LoadProjectSpec: modeldef.ProjectSpec,
		OpenMigrationBackend: func(ctx context.Context) (project.MigrationBackend, error) {
			if selectionErr != nil {
				return nil, selectionErr
			}
			return databaseconfig.Open(ctx, selected)
		},
		MigrationSQLRenderer: selected.MigrationSQLRenderer(),
		OpenSystemStateBackend: func(ctx context.Context) (project.SystemStateBackend, error) {
			if selectionErr != nil {
				return nil, selectionErr
			}
			if policyErr != nil {
				return nil, policyErr
			}
			opened, err := databaseconfig.Open(ctx, selected)
			if err != nil {
				return nil, err
			}
			if closeFailure {
				return closeFailureBackend{SystemStateBackend: opened}, nil
			}
			return opened, nil
		},
		SystemOperatorPolicy: policy,
	}, os.Args[1:], os.Stdin, writer)
	if err != nil {
		exitCode := project.RunnerExitCode(err)
		if exitCode == 1 {
			_, _ = fmt.Fprintln(os.Stderr, "external operator project failed")
		}
		os.Exit(exitCode)
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
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func New(ctx context.Context, backend systemstate.Backend) (*web.Application, error) {
	if ctx == nil {
		return nil, errors.New("external operator application: nil context")
	}
	runtimeConfig, err := operatorpolicy.RuntimeConfig()
	if err != nil {
		return nil, err
	}
	runtime, err := systemstate.OpenExisting(ctx, backend, runtimeConfig)
	if err != nil {
		return nil, err
	}
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
	service, err := adminapp.NewDurableService(runtime, runtime)
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
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{})
	if err != nil {
		return nil, err
	}
	allowedNext, err := admin.SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		return nil, err
	}
	authRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions: manager,
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
	articleAPI, err := apiapp.New(runtime, apiAuth)
	if err != nil {
		return nil, err
	}
	middleware, err := apiapp.Middleware()
	if err != nil {
		return nil, err
	}
	routes := append(adminSite.Routes(), articleAPI.Routes()...)
	application, err := webapp.NewComposedApplication(runtime, routes, middleware)
	if err != nil {
		return nil, fmt.Errorf("compose external operator application: %w", err)
	}
	return application, nil
}
`

const operatorSiteSource = `package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"example.com/godj-operator-product/application"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/web"
)

const markerEnvironment = "GODJ_OPERATOR_PRODUCT_MARKER"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "external operator site failed")
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, stdout io.Writer) (resultErr error) {
	if ctx == nil || len(arguments) != 3 || arguments[0] != "serve" || arguments[1] != "--listen" {
		return errors.New("invalid external operator site invocation")
	}
	address := strings.TrimSpace(arguments[2])
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("external operator site requires loopback")
	}
	if err := appendMarker(); err != nil {
		return err
	}
	selected, err := databaseconfig.FromEnvironment(os.LookupEnv)
	if err != nil {
		return errors.New("database configuration")
	}
	backend, err := databaseconfig.Open(ctx, selected)
	if err != nil {
		return errors.New("database open")
	}
	defer func() { resultErr = errors.Join(resultErr, backend.Close()) }()
	app, err := application.New(ctx, backend)
	if err != nil {
		return errors.New("application composition")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	server, err := web.NewServer(app, web.ServerOptions{})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "external operator site listening on http://%s\n", listener.Addr()); err != nil {
		return err
	}
	if err := server.Serve(ctx, listener); err != nil {
		if contextErr := ctx.Err(); contextErr != nil && errors.Is(err, contextErr) {
			return nil
		}
		return err
	}
	return nil
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
