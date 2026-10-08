// Command browser exercises the real Helpdesk Admin and ticket editor against
// its own SQLite identity, session and audit stores. It never uses a deployed
// service. The sole fixture login is operator / demo-password.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/progresshans/godj/admin"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	directory, err := os.MkdirTemp("", "godj-helpdesk-browser-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	backend, err := sqlite.Open(ctx, "file:"+filepath.Join(directory, "fixture.sqlite3")+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer backend.Close()
	sources := append([]definition.Source{systemstate.InitialDefinitionSource()}, helpdesk.MigrationSources()...)
	graph, _, err := definition.Load(sources...)
	if err != nil {
		return err
	}
	if _, err = (migrations.Executor{Backend: backend}).Migrate(ctx, graph, migrations.LatestLifecycleRequest()); err != nil {
		return err
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return err
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "browser-operator", Active: true, Permissions: append([]auth.Permission{"godj.admin.access"}, helpdesk.Permissions()...)})
	if err != nil {
		return err
	}
	policy := systemstate.CredentialPolicy{Principal: principal, PasswordHasher: hasher}
	if err = systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{Username: "operator", Password: "demo-password", CredentialPolicy: policy}); err != nil {
		return err
	}
	graph, _, err = definition.Load(append(systemstate.IdentityMigrationSources(), helpdesk.MigrationSources()...)...)
	if err != nil {
		return err
	}
	if _, err = (migrations.Executor{Backend: backend}).Migrate(ctx, graph, migrations.LatestLifecycleRequest()); err != nil {
		return err
	}
	if _, err = systemstate.AdoptOperator(ctx, backend, systemstate.AdoptOperatorConfig{Expected: systemstate.RuntimeConfig{CredentialPolicy: policy}, Staff: true}); err != nil {
		return err
	}
	runtime, err := systemstate.OpenIdentity(ctx, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher})
	if err != nil {
		return err
	}
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Browser category"))
	if err != nil {
		return err
	}
	for _, name := range []string{"Desk & <one>", "Spare __prefix__"} {
		if _, err = models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate(name, category.ID)); err != nil {
			return err
		}
	}
	if _, err = models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Existing browser ticket", category.ID)); err != nil {
		return err
	}
	if os.Getenv("GODJ_BROWSER_SUMMARY") == "1" {
		for _, closed := range []bool{false, true} {
			cost, effort, microseconds := "0.1", 1.5, int64(1)
			if closed {
				cost, effort, microseconds = "0.2", 2.5, 2
			}
			amount, err := decimal.Parse(cost)
			if err != nil {
				return err
			}
			if _, err = models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Unset summary", category.ID).WithClosed(closed).WithExpectedCost(amount).WithEffort(effort).WithElapsed(duration.FromMicroseconds(microseconds))); err != nil {
				return err
			}
		}
		for _, priority := range []int64{-1, 0, 1, math.MinInt64, math.MaxInt64} {
			if _, err = models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Summary priority", category.ID).WithPriority(priority).WithClosed(priority == 0)); err != nil {
				return err
			}
		}
		if _, err = models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Closed low", category.ID).WithPriority(-1).WithClosed(true)); err != nil {
			return err
		}
		for priority := int64(10); priority <= 30; priority++ {
			if _, err = models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Legacy summary", category.ID).WithPriority(priority)); err != nil {
				return err
			}
		}
	}
	application, err := helpdesk.New(runtime, category.ID)
	if err != nil {
		return err
	}
	registry, err := application.AdminRegistry(helpdesk.AdminConfig{AppendAudit: runtime.AppendAudit})
	if err != nil {
		return err
	}
	configured, err := settings.New(settings.Definition{ProjectName: "helpdesk_browser", InstalledApps: helpdesk.InstalledApps()})
	if err != nil {
		return err
	}
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{})
	if err != nil {
		return err
	}
	allowed, err := admin.SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		return err
	}
	allowed = append(allowed, helpdesk.TicketEditorPath, helpdesk.TicketSummaryPath)
	persistence, err := runtime.LoginPersistence(manager)
	if err != nil {
		return err
	}
	authentication, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: runtime.Authenticator(), LoginPersistence: persistence, Authorizer: auth.PrincipalAuthorizer{}, SessionCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: allowed})
	if err != nil {
		return err
	}
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "helpdesk", Registry: registry, Auth: authentication, AdditionalNextPaths: []string{helpdesk.TicketEditorPath, helpdesk.TicketSummaryPath}, RenderLimits: helpdesk.AdminRenderLimits()})
	if err != nil {
		return err
	}
	apiAuth, err := apisessionauth.New(authentication)
	if err != nil {
		return err
	}
	api, err := application.API(helpdesk.APIConfig{Authentication: apiAuth, AppendAudit: runtime.AppendAudit})
	if err != nil {
		return err
	}
	editor, err := application.TicketEditor(helpdesk.TicketEditorConfig{Auth: authentication, AppendAudit: runtime.AppendAudit})
	if err != nil {
		return err
	}
	routes := append(site.Routes(), api.Routes()...)
	routes = append(routes, editor.Routes()...)
	summary, err := application.TicketSummary(authentication)
	if err != nil {
		return err
	}
	routes = append(routes, summary.Routes()...)
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: routes, MaxResponseBytes: helpdesk.TicketEditorMaxResponseBytes})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	fmt.Printf("http://%s/admin/tickets/\n", listener.Addr())
	fmt.Printf("fixture_pid=%d\n", os.Getpid())
	select {
	case err := <-finished:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdown); err != nil {
			return err
		}
		if err = <-finished; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		// Read committed rows and durable audit independently of browser claims.
		rows, err := models.TicketObjects.Using(runtime).OrderBy(models.TicketFields.ID.Asc()).All(shutdown)
		if err != nil {
			return err
		}
		evidence := make([]map[string]any, len(rows))
		for index, row := range rows {
			history, err := runtime.AuditHistory(shutdown, "helpdesk.ticket", row.ID, 10)
			if err != nil {
				return err
			}
			evidence[index] = map[string]any{"id": row.ID, "subject": row.Subject, "category": row.CategoryID, "closed": row.Closed, "priority": row.Priority, "audit": history}
		}
		links, err := models.TicketLabelObjects.Using(runtime).OrderBy(models.TicketLabelFields.ID.Asc()).All(shutdown)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"committed_tickets": evidence, "committed_links": links})
	}
}
