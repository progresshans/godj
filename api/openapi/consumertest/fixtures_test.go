package consumertest_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/examples/helpdesk"
	helpdeskmodels "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

// This private transport is written only to the child process's stdin. Session
// fields contain raw IDs for the documented session cookie, not cookie headers.
// Neither credentials nor ephemeral listener URLs belong in test diagnostics.
type consumerInput struct {
	ArticleBearer   serverInput `json:"article_bearer"`
	ArticleSession  serverInput `json:"article_session"`
	HelpdeskSession serverInput `json:"helpdesk_session"`
}

type serverInput struct {
	URL             string `json:"url"`
	Token           string `json:"token,omitempty"`
	ReadOnlyToken   string `json:"read_only_token,omitempty"`
	Session         string `json:"session,omitempty"`
	ReadOnlySession string `json:"read_only_session,omitempty"`
	CategoryID      int64  `json:"category_id,omitempty"`
	TicketID        int64  `json:"ticket_id,omitempty"`
	OtherTicketID   int64  `json:"other_ticket_id,omitempty"`
}

// newConsumerFixtures publishes only the real API routes. The returned document
// snapshots come from those exact adapters, and the final callback observes the
// databases after the generated client has completed its HTTP operations.
func newConsumerFixtures(t *testing.T) (consumerInput, map[string][]byte, func(*testing.T)) {
	t.Helper()
	articleMigration, err := os.ReadFile(filepath.Join(repositoryRoot(t), "examples", "article", "migrations", "0001_initial.godj.json"))
	if err != nil {
		t.Fatal("read fixed Article migration:", err)
	}
	articleSource := definition.Source{SourceID: "article/0001_initial", Document: articleMigration}
	articleAll := consumerPrincipal(t, "article-client-all",
		articleapp.ArticleViewPermission, articleapp.ArticleAddPermission,
		articleapp.ArticleChangePermission, articleapp.ArticleDeletePermission,
	)
	articleView := consumerPrincipal(t, "article-client-view", articleapp.ArticleViewPermission)

	bearerBackend := newConsumerBackend(t, "article-bearer", articleSource)
	fullToken, viewToken := consumerToken(t), consumerToken(t)
	bearer, err := bearerauth.New(bearerauth.Config{
		Verifier:   consumerTokenVerifier{fullToken: articleAll, viewToken: articleView},
		Authorizer: auth.PrincipalAuthorizer{},
	})
	if err != nil {
		t.Fatal("construct Article Bearer profile:", err)
	}
	bearerURL, bearerDocument := newArticleConsumerAPI(t, bearerBackend, bearer)

	sessionBackend := newConsumerBackend(t, "article-session", articleSource)
	articleSessionAuth, articleSession, articleViewSession := newConsumerSessionAuthentication(t, articleAll, articleView, apiapp.ListPath)
	sessionURL, sessionDocument := newArticleConsumerAPI(t, sessionBackend, articleSessionAuth)

	helpdeskBackend := newConsumerBackend(t, "helpdesk-session", helpdesk.MigrationSources()...)
	category, err := helpdeskmodels.CategoryObjects.Create(t.Context(), helpdeskBackend, helpdeskmodels.NewCategoryCreate("Hardware & repairs"))
	if err != nil {
		t.Fatal("seed selected Helpdesk category:", err)
	}
	otherCategory, err := helpdeskmodels.CategoryObjects.Create(t.Context(), helpdeskBackend, helpdeskmodels.NewCategoryCreate("Other"))
	if err != nil {
		t.Fatal("seed other Helpdesk category:", err)
	}
	ticket, err := helpdeskmodels.TicketObjects.Create(t.Context(), helpdeskBackend, helpdeskmodels.NewTicketCreate("Existing ticket", category.ID).WithDetailsNull())
	if err != nil {
		t.Fatal("seed selected Helpdesk ticket:", err)
	}
	otherTicket, err := helpdeskmodels.TicketObjects.Create(t.Context(), helpdeskBackend, helpdeskmodels.NewTicketCreate("Other category ticket", otherCategory.ID).WithDetailsNull())
	if err != nil {
		t.Fatal("seed other Helpdesk ticket:", err)
	}
	// Ticket viewing includes the category summary. Neither principal receives
	// ViewCategory, which protects the separate Category Admin surface.
	helpdeskAll := consumerPrincipal(t, "helpdesk-client-all", helpdesk.ViewTicket, helpdesk.AddTicket)
	helpdeskView := consumerPrincipal(t, "helpdesk-client-view", helpdesk.ViewTicket)
	helpdeskAuth, helpdeskSession, helpdeskViewSession := newConsumerSessionAuthentication(t, helpdeskAll, helpdeskView, "/api/tickets/")
	helpdeskApplication, err := helpdesk.New(helpdeskBackend, category.ID)
	if err != nil {
		t.Fatal("construct Helpdesk application:", err)
	}
	helpdeskAPI, err := helpdeskApplication.API(helpdeskAuth)
	if err != nil {
		t.Fatal("construct Helpdesk API:", err)
	}
	helpdeskDocument, err := helpdeskAPI.OpenAPI()
	if err != nil {
		t.Fatal("describe Helpdesk API:", err)
	}
	helpdeskURL := serveConsumerAPI(t, "helpdesk_generated_client", helpdesk.InstalledApps(), helpdeskAPI.Routes(), nil)

	input := consumerInput{
		ArticleBearer:  serverInput{URL: bearerURL, Token: fullToken, ReadOnlyToken: viewToken},
		ArticleSession: serverInput{URL: sessionURL, Session: articleSession, ReadOnlySession: articleViewSession},
		HelpdeskSession: serverInput{
			URL: helpdeskURL, Session: helpdeskSession, ReadOnlySession: helpdeskViewSession,
			CategoryID: category.ID, TicketID: ticket.ID, OtherTicketID: otherTicket.ID,
		},
	}
	documents := map[string][]byte{
		"articlebearer":   bearerDocument,
		"articlesession":  sessionDocument,
		"helpdesksession": helpdeskDocument.Bytes(),
	}
	verify := func(t *testing.T) {
		t.Helper()
		for _, fixture := range []struct {
			name    string
			backend *sqlite.Backend
		}{{"Article Bearer", bearerBackend}, {"Article Session", sessionBackend}} {
			count, err := articlemodels.ArticleObjects.Using(fixture.backend).Count(t.Context())
			if err != nil || count != 0 {
				t.Errorf("%s generated client left %d Article rows after create/update/delete: %v", fixture.name, count, err)
			}
		}
		tickets, err := helpdeskmodels.TicketObjects.Using(helpdeskBackend).OrderBy(helpdeskmodels.TicketFields.ID.Asc()).All(t.Context())
		if err != nil {
			t.Fatal("read Helpdesk effects:", err)
		}
		if len(tickets) != 7 {
			t.Fatalf("Helpdesk generated client left %d tickets, want two original and five created", len(tickets))
		}
		selectedCount, createdCount := 0, 0
		wantPriority := map[string]*int64{
			"Consumer ticket": nil, "Null priority": nil,
			"Maximum priority": new(int64(math.MaxInt64)), "Minimum priority": new(int64(math.MinInt64)), "Zero priority": new(int64(0)),
		}
		for _, stored := range tickets {
			if stored.CategoryID == category.ID {
				selectedCount++
			}
			switch stored.ID {
			case ticket.ID:
				if !sameConsumerTicket(stored, ticket) {
					t.Error("generated client changed the original selected ticket")
				}
			case otherTicket.ID:
				if !sameConsumerTicket(stored, otherTicket) {
					t.Error("generated client changed the ticket outside the selected category")
				}
			default:
				createdCount++
				if stored.CategoryID != category.ID {
					t.Error("generated client created a ticket outside the selected category")
				}
				want, known := wantPriority[stored.Subject]
				if !known || (stored.Priority == nil) != (want == nil) || (want != nil && stored.Priority != nil && *stored.Priority != *want) || stored.Closed || stored.Details != nil {
					t.Error("generated client changed an integer value, null, or default during persistence")
				}
				delete(wantPriority, stored.Subject)
			}
		}
		if selectedCount != 6 || createdCount != 5 || len(wantPriority) != 0 {
			t.Errorf("Helpdesk final selection contains %d selected and %d newly created tickets", selectedCount, createdCount)
		}
		categories, err := helpdeskmodels.CategoryObjects.Using(helpdeskBackend).OrderBy(helpdeskmodels.CategoryFields.ID.Asc()).All(t.Context())
		if err != nil || len(categories) != 2 || categories[0].ID != category.ID || categories[0].Name != category.Name || categories[1].ID != otherCategory.ID || categories[1].Name != otherCategory.Name {
			t.Errorf("generated client changed the original Helpdesk categories: %v", err)
		}
	}
	return input, documents, verify
}

func newConsumerBackend(t *testing.T, name string, sources ...definition.Source) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), name+".sqlite3"))
	if err != nil {
		t.Fatal("open isolated consumer database:", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error("close consumer database:", err)
		}
	})
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal("load fixed consumer migration:", err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("migrate isolated consumer database:", err)
	}
	return backend
}

func newArticleConsumerAPI(t *testing.T, backend *sqlite.Backend, authentication api.Authentication) (string, []byte) {
	t.Helper()
	adapter, err := apiapp.New(backend, authentication)
	if err != nil {
		t.Fatal("construct Article API:", err)
	}
	document, err := adapter.OpenAPI()
	if err != nil {
		t.Fatal("describe Article API:", err)
	}
	url := serveConsumerAPI(t, "article_generated_client", []apps.Config{{Name: "github.com/progresshans/godj/examples/article/articleapp", Label: apiapp.Namespace}}, adapter.Routes(), adapter.Middleware())
	return url, document.Bytes()
}

func serveConsumerAPI(t *testing.T, name string, installed []apps.Config, routes []web.Route, middleware []web.Middleware) string {
	t.Helper()
	configured, err := settings.New(settings.Definition{ProjectName: name, InstalledApps: installed})
	if err != nil {
		t.Fatal("configure consumer HTTP fixture:", err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes, Middleware: middleware})
	if err != nil {
		t.Fatal("construct consumer HTTP fixture:", err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	return server.URL
}

func consumerPrincipal(t *testing.T, id string, permissions ...auth.Permission) auth.Principal {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: id, Active: true, Permissions: permissions})
	if err != nil {
		t.Fatal("construct consumer principal:", err)
	}
	return principal
}

func consumerToken(t *testing.T) string {
	t.Helper()
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal("create ephemeral consumer credential")
	}
	return base64.RawURLEncoding.EncodeToString(value[:])
}

type consumerTokenVerifier map[string]auth.Principal

func (verifier consumerTokenVerifier) Verify(ctx context.Context, token bearerauth.Token) (auth.Principal, error) {
	if err := ctx.Err(); err != nil {
		return auth.Principal{}, err
	}
	if principal, found := verifier[token.Encoded()]; found {
		return principal, nil
	}
	return auth.Principal{}, auth.ErrInvalidCredentials
}

// The fixture explicitly seeds session records for known principals. It tests
// the real cookie/CSRF/API profile, not the separate credential login flow.
func newConsumerSessionAuthentication(t *testing.T, full, viewer auth.Principal, listPath string) (*apisessionauth.Runtime, string, string) {
	t.Helper()
	store, err := sessions.NewMemoryStore(8)
	if err != nil {
		t.Fatal("construct consumer session store:", err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal("construct consumer session manager:", err)
	}
	seed := func(principal auth.Principal) string {
		record, err := manager.Create(t.Context(), map[string]string{"_godj_principal_id": principal.ID()})
		if err != nil {
			t.Fatal("seed consumer session:", err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := manager.Delete(ctx, record.ID()); err != nil {
				t.Error("delete consumer session:", err)
			}
		})
		return record.ID().Encoded()
	}
	fullSession, viewSession := seed(full), seed(viewer)
	webRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions:         manager,
		Authenticator:    consumerPrincipalResolver{full.ID(): full, viewer.ID(): viewer},
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie:       websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		FallbackPath:     listPath,
		AllowedNextPaths: []string{listPath},
	})
	if err != nil {
		t.Fatal("construct consumer Web session profile:", err)
	}
	runtime, err := apisessionauth.New(webRuntime)
	if err != nil {
		t.Fatal("construct consumer API session profile:", err)
	}
	return runtime, fullSession, viewSession
}

type consumerPrincipalResolver map[string]auth.Principal

func (consumerPrincipalResolver) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return auth.Principal{}, auth.ErrInvalidCredentials
}

func (resolver consumerPrincipalResolver) Resolve(ctx context.Context, id string) (auth.Principal, error) {
	if err := ctx.Err(); err != nil {
		return auth.Principal{}, err
	}
	if principal, found := resolver[id]; found {
		return principal, nil
	}
	return auth.Principal{}, auth.ErrInvalidCredentials
}

func sameConsumerTicket(left, right helpdeskmodels.Ticket) bool {
	if left.ID != right.ID || left.Subject != right.Subject || left.Closed != right.Closed || left.CategoryID != right.CategoryID {
		return false
	}
	if (left.Priority == nil) != (right.Priority == nil) || (left.Priority != nil && *left.Priority != *right.Priority) {
		return false
	}
	if left.Details == nil || right.Details == nil {
		return left.Details == nil && right.Details == nil
	}
	return *left.Details == *right.Details
}
