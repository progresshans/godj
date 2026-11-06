package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type siteLoginProbe struct {
	auth.CredentialAuthenticator
	input                             auth.LoginInput
	failure                           error
	metadata, authenticates, resolves int
	username, password                string
}

func (probe *siteLoginProbe) LoginInput() (auth.LoginInput, error) {
	probe.metadata++
	return probe.input, probe.failure
}
func (probe *siteLoginProbe) Authenticate(ctx context.Context, username, password string) (auth.Credential, error) {
	probe.authenticates++
	probe.username, probe.password = username, password
	return probe.CredentialAuthenticator.Authenticate(ctx, username, password)
}
func (probe *siteLoginProbe) Resolve(ctx context.Context, id string) (auth.Credential, error) {
	probe.resolves++
	return probe.CredentialAuthenticator.Resolve(ctx, id)
}

type siteLoginEntropy struct{ calls int }

func (entropy *siteLoginEntropy) Read([]byte) (int, error) {
	entropy.calls++
	return 0, errors.New("unexpected entropy")
}

func siteLoginRuntime(t *testing.T, registry Registry, authenticator auth.CredentialAuthenticator) (*sessionauth.Runtime, *siteCountingStore) {
	t.Helper()
	memory, err := sessions.NewMemoryStore(64)
	if err != nil {
		t.Fatal(err)
	}
	store := &siteCountingStore{Store: memory}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{},
		SessionCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: paths})
	if err != nil {
		t.Fatal(err)
	}
	return runtime, store
}

func TestSiteRejectsMissingLoginMetadataBeforeIO(t *testing.T) {
	registry := siteTestRegistry(t, newSiteModelState())
	input, err := auth.NewLoginInput(auth.LoginInputConfig{Label: "Contact", MaxLength: 5, Normalization: auth.LoginExact})
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("private metadata cause")
	for _, mode := range []string{"missing_provider", "zero", "failure"} {
		t.Run(mode, func(t *testing.T) {
			probe := &siteLoginProbe{input: input}
			if mode == "zero" {
				probe.input = auth.LoginInput{}
			}
			if mode == "failure" {
				probe.failure = fault
			}
			var authenticator auth.CredentialAuthenticator = probe
			if mode == "missing_provider" {
				authenticator = struct{ auth.CredentialAuthenticator }{probe}
			}
			runtime, store := siteLoginRuntime(t, registry, authenticator)
			entropy := &siteLoginEntropy{}
			site, err := NewSite(SiteConfig{Apps: mustApps(t), Namespace: "godj_conformance", Registry: registry, Auth: runtime, Random: entropy})
			var config *ConfigError
			if site != nil || !errors.As(err, &config) || config.Path != "site.auth.login_input" || config.Code != "invalid" || mode == "failure" && !errors.Is(err, fault) {
				t.Fatal("invalid input declaration became an Admin site", err)
			}
			if entropy.calls != 0 || probe.authenticates != 0 || probe.resolves != 0 || store.counts() != (siteStoreCounts{}) {
				t.Fatal("metadata failure performed I/O")
			}
			if strings.Contains(fmt.Sprintf("%#v", err), fault.Error()) {
				t.Fatal("metadata cause leaked")
			}
		})
	}
}

func TestSiteLoginAppliesSelectedFormBeforeAuthentication(t *testing.T) {
	for _, policy := range []auth.LoginNormalization{auth.LoginExact, auth.LoginUnicode16NFKC} {
		t.Run(string(policy), func(t *testing.T) {
			input, err := auth.NewLoginInput(auth.LoginInputConfig{Label: "Contact <private>", MaxLength: 5, Normalization: policy})
			if err != nil {
				t.Fatal(err)
			}
			principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "selected", Active: true, Staff: true, Superuser: true})
			if err != nil {
				t.Fatal(err)
			}
			credential, err := auth.NewCredential("user", "plain:  secret  ", principal)
			if err != nil {
				t.Fatal(err)
			}
			verifier, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, sitePlainHasher{})
			if err != nil {
				t.Fatal(err)
			}
			probe := &siteLoginProbe{CredentialAuthenticator: verifier, input: input}
			registry := siteTestRegistry(t, newSiteModelState())
			runtime, store := siteLoginRuntime(t, registry, probe)
			site, err := NewSite(SiteConfig{Apps: mustApps(t), Namespace: "godj_conformance", Registry: registry, Auth: runtime})
			if err != nil {
				t.Fatal(err)
			}
			configured, err := settings.New(settings.Definition{ProjectName: "selected_login", InstalledApps: mustApps(t).All()})
			if err != nil {
				t.Fatal(err)
			}
			application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
			if err != nil {
				t.Fatal(err)
			}
			// The compiled form keeps its original declaration across requests.
			probe.input = auth.LoginInput{}
			for _, mode := range []string{"length", "expansion", "contraction", "required", "csrf", "duplicate", "unknown", "fullwidth", "trimmed"} {
				t.Run(mode, func(t *testing.T) {
					client := newSiteHTTPClient(application)
					page := client.do(http.MethodGet, "/admin/login/", nil)
					if page.Code != 200 || !strings.Contains(page.Body.String(), "Contact &lt;private&gt;") || !strings.Contains(page.Body.String(), `maxlength="5"`) {
						t.Fatal("login metadata not rendered safely")
					}
					data := url.Values{"username": {"user"}, "password": {"  secret  "}, "csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "next": {"https://foreign.test/"}}
					wantStatus, wantAuth, admitted := 200, 0, false
					switch mode {
					case "length":
						data.Set("username", "abcdef")
					case "expansion":
						data.Set("username", "ﬃﬃ")
						if policy == auth.LoginExact {
							wantAuth = 1
						}
					case "contraction":
						data.Set("username", "A\u030aA\u030aA\u030a")
					case "required":
						data.Del("password")
					case "csrf":
						data.Del("csrfmiddlewaretoken")
						wantStatus = 403
					case "duplicate":
						data.Add("username", "user")
						wantStatus = 400
					case "unknown":
						data.Set("contact", "user")
						wantStatus = 400
					case "fullwidth":
						data.Set("username", " ｕｓｅｒ ")
						wantAuth = 1
						admitted = policy == auth.LoginUnicode16NFKC
					case "trimmed":
						data.Set("username", "\u001c user\u3000")
						wantAuth, admitted = 1, true
					}
					if admitted {
						wantStatus = 302
					}
					probe.authenticates = 0
					store.resetCounts()
					beforeCSRF := client.cookieValue(sessionauth.DefaultCSRFCookieName)
					response := client.do(http.MethodPost, "/admin/login/", data)
					if response.Code != wantStatus || probe.authenticates != wantAuth || probe.metadata != 1 || strings.Contains(response.Body.String(), "secret") {
						t.Fatal("Admin form boundary changed", response.Code, wantStatus, probe.authenticates, wantAuth)
					}
					if admitted {
						if response.Header().Get("Location") != "/admin/" || client.cookieValue(sessionauth.DefaultSessionCookieName) == "" || client.cookieValue(sessionauth.DefaultCSRFCookieName) == beforeCSRF || probe.username != "user" || probe.password != "  secret  " {
							t.Fatal("cleaned login, safe next or rotation lost")
						}
					} else if store.counts() != (siteStoreCounts{}) || response.Header().Get("Set-Cookie") != "" || client.cookieValue(sessionauth.DefaultCSRFCookieName) != beforeCSRF {
						t.Fatal("rejected login changed session/cookie", store.counts())
					}
				})
			}
		})
	}
}
