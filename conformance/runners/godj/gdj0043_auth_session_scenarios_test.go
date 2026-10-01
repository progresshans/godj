package godj

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0043AuthSessionScenarioRegistryIsExact(t *testing.T) {
	t.Parallel()

	want := []string{
		"django.auth_session.anonymous_request",
		"django.auth_session.valid_login_rotation",
		"django.auth_session.rejected_login",
		"django.auth_session.logout_flush",
		"django.auth_session.cookie_policy",
		"django.auth_session.permission_and_safe_next",
		"django.auth_session.csrf_rejection",
		"django.auth_session.csrf_acceptance_and_rotation",
	}
	if len(authSessionScenarioRegistry) != len(want) {
		t.Fatalf("auth/session registry size = %d, want %d", len(authSessionScenarioRegistry), len(want))
	}
	for _, scenario := range want {
		if handler, ok := authSessionScenarioHandler(scenario); !ok || handler == nil {
			t.Fatalf("auth/session scenario %q is not registered", scenario)
		}
	}
	for _, scenario := range []string{
		"",
		"django.auth_session.anonymous_request.extra",
		"django.auth_session.cookie-policy",
		"Django.auth_session.logout_flush",
	} {
		if handler, ok := authSessionScenarioHandler(scenario); ok || handler != nil {
			t.Fatalf("near-miss auth/session scenario %q was registered", scenario)
		}
	}
}

func TestGDJ0043AuthCookieAndCSRFScenariosRemainOnAdminSiteSQLiteBoundary(t *testing.T) {
	t.Parallel()

	parsed, err := parser.ParseFile(
		token.NewFileSet(),
		"gdj0043_auth_session_scenarios.go",
		nil,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]map[string]int{
		"authSessionCookiePolicy": {
			"newAuthSessionSiteFixture": 1,
			"authSessionSiteLogin":      1,
			"authSessionSiteCSRFToken":  1,
		},
		"authSessionCSRFRejection": {
			"newAuthSessionSiteFixture": 1,
			"authSessionSiteLogin":      1,
			"articleAdminRows":          2,
		},
		"authSessionCSRFAcceptanceAndRotation": {
			"newAuthSessionSiteFixture": 1,
			"authSessionSiteLogin":      2,
			"authSessionSiteCSRFToken":  2,
			"articleAdminRows":          2,
		},
		"newAuthSessionSiteFixture": {
			"newArticleAdminFixture": 1,
			"admin.NewSite":          1,
			"web.NewApplication":     1,
		},
	}
	forbidden := map[string]struct{}{
		"newAuthSessionFixture":   {},
		"authSessionMutationPath": {},
		"articlesSnapshot":        {},
		"acceptedWrites":          {},
		"rejectedRequests":        {},
	}
	found := make(map[string]bool, len(tests))
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Body == nil {
			continue
		}
		required, audited := tests[function.Name.Name]
		if !audited {
			continue
		}
		found[function.Name.Name] = true
		calls := make(map[string]int)
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.Ident:
				if _, blocked := forbidden[typed.Name]; blocked {
					t.Fatalf("%s uses surrogate auth/session boundary %s", function.Name.Name, typed.Name)
				}
			case *ast.CallExpr:
				if name := authSessionTestCallName(typed.Fun); name != "" {
					calls[name]++
				}
			}
			return true
		})
		for name, want := range required {
			if calls[name] != want {
				t.Fatalf("%s call %s = %d, want %d on public Site/SQLite boundary", function.Name.Name, name, calls[name], want)
			}
		}
	}
	for name := range tests {
		if !found[name] {
			t.Fatalf("audited auth/session function %s is missing", name)
		}
	}
}

func authSessionTestCallName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		owner, ok := typed.X.(*ast.Ident)
		if !ok {
			return typed.Sel.Name
		}
		return owner.Name + "." + typed.Sel.Name
	default:
		return ""
	}
}

func TestGDJ0043AuthSessionScenariosExecuteProductBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		scenario string
		phase    protocol.Phase
		result   protocol.Value
		dbState  *protocol.Value
		metrics  protocol.Value
	}{
		{
			id:       "AUT-001",
			scenario: "django.auth_session.anonymous_request",
			phase:    protocol.PhaseEvaluation,
			result: protocol.Object(map[string]protocol.Value{
				"authenticated": protocol.Boolean(false),
				"permission":    protocol.Boolean(false),
				"redirect":      protocol.String("admin_login_local_next"),
				"status":        protocol.Integer("302"),
			}),
			metrics: protocol.Object(map[string]protocol.Value{
				"session_writes": protocol.Integer("0"),
			}),
		},
		{
			id:       "AUT-002",
			scenario: "django.auth_session.valid_login_rotation",
			phase:    protocol.PhaseCommit,
			result: protocol.Object(map[string]protocol.Value{
				"authenticated":       protocol.Boolean(true),
				"local_redirect":      protocol.String("admin_index"),
				"old_session_removed": protocol.Boolean(true),
				"rotation":            protocol.Boolean(true),
				"session_survives":    protocol.Boolean(true),
				"status":              protocol.Integer("302"),
			}),
			metrics: protocol.Object(map[string]protocol.Value{
				"session_rows_after":  protocol.Integer("1"),
				"session_rows_before": protocol.Integer("1"),
			}),
		},
		{
			id:       "AUT-003",
			scenario: "django.auth_session.rejected_login",
			phase:    protocol.PhaseEvaluation,
			result: protocol.Object(map[string]protocol.Value{
				"cases": protocol.List(
					protocol.Object(map[string]protocol.Value{
						"authenticated": protocol.Boolean(false),
						"case":          protocol.String("invalid"),
						"redirect":      protocol.String("none"),
						"status":        protocol.Integer("200"),
					}),
					protocol.Object(map[string]protocol.Value{
						"authenticated": protocol.Boolean(false),
						"case":          protocol.String("inactive"),
						"redirect":      protocol.String("none"),
						"status":        protocol.Integer("200"),
					}),
				),
			}),
			metrics: protocol.Object(map[string]protocol.Value{
				"auth_state_writes": protocol.Integer("0"),
			}),
		},
		{
			id:       "AUT-004",
			scenario: "django.auth_session.logout_flush",
			phase:    protocol.PhaseCommit,
			result: protocol.Object(map[string]protocol.Value{
				"old_session_removed":      protocol.Boolean(true),
				"redirect":                 protocol.String("admin_login"),
				"subsequent_authenticated": protocol.Boolean(false),
				"subsequent_redirect":      protocol.String("admin_login_local_next"),
			}),
			metrics: protocol.Object(map[string]protocol.Value{
				"session_rows_after_logout": protocol.Integer("0"),
			}),
		},
		{
			id:       "AUT-005",
			scenario: "django.auth_session.cookie_policy",
			phase:    protocol.PhaseEvaluation,
			result: protocol.Object(map[string]protocol.Value{
				"delete": protocol.Object(map[string]protocol.Value{
					"expires_present": protocol.Boolean(true),
					"http_only":       protocol.Boolean(true),
					"max_age":         protocol.Integer("0"),
					"path":            protocol.String("/"),
					"same_site":       protocol.String("Lax"),
					"secure":          protocol.Boolean(false),
				}),
				"delete_semantics": protocol.Boolean(true),
				"login": protocol.Object(map[string]protocol.Value{
					"expires_present": protocol.Boolean(true),
					"http_only":       protocol.Boolean(true),
					"max_age":         protocol.Integer("7200"),
					"path":            protocol.String("/"),
					"same_site":       protocol.String("Lax"),
					"secure":          protocol.Boolean(false),
				}),
				"session_cookie_category": protocol.String("configured_session_cookie"),
			}),
			metrics: protocol.Object(map[string]protocol.Value{
				"cookie_values_serialized": protocol.Integer("0"),
			}),
		},
		{
			id:       "AUT-006",
			scenario: "django.auth_session.permission_and_safe_next",
			phase:    protocol.PhaseEvaluation,
			result: protocol.Object(map[string]protocol.Value{
				"anonymous": protocol.Object(map[string]protocol.Value{
					"redirect": protocol.String("admin_login_local_next"),
					"status":   protocol.Integer("302"),
				}),
				"authenticated_without_permission": protocol.Object(map[string]protocol.Value{
					"status": protocol.Integer("403"),
				}),
				"unsafe_next": protocol.Object(map[string]protocol.Value{
					"external": protocol.Boolean(false),
					"redirect": protocol.String("admin_index"),
					"status":   protocol.Integer("302"),
				}),
			}),
			metrics: protocol.Object(map[string]protocol.Value{
				"external_redirects": protocol.Integer("0"),
			}),
		},
		{
			id:       "AUT-007",
			scenario: "django.auth_session.csrf_rejection",
			phase:    protocol.PhaseEvaluation,
			result: protocol.Object(map[string]protocol.Value{
				"missing_status": protocol.Integer("403"),
				"mutation_zero":  protocol.Boolean(true),
				"wrong_status":   protocol.Integer("403"),
			}),
			dbState: authSessionTestValuePointer(protocol.Object(map[string]protocol.Value{
				"after":  protocol.List(),
				"before": protocol.List(),
			})),
			metrics: protocol.Object(map[string]protocol.Value{
				"accepted_writes":   protocol.Integer("0"),
				"rejected_requests": protocol.Integer("2"),
			}),
		},
		{
			id:       "AUT-008",
			scenario: "django.auth_session.csrf_acceptance_and_rotation",
			phase:    protocol.PhaseCommit,
			result: protocol.Object(map[string]protocol.Value{
				"form_status":             protocol.Integer("302"),
				"header_status":           protocol.Integer("302"),
				"login_rotated_csrf":      protocol.Boolean(true),
				"pre_login_replay_status": protocol.Integer("403"),
				"replay_mutation_zero":    protocol.Boolean(true),
			}),
			dbState: authSessionTestValuePointer(protocol.Object(map[string]protocol.Value{
				"articles": protocol.List(protocol.Object(map[string]protocol.Value{
					"id":        protocol.PrimaryKey(protocol.Integer("1")),
					"published": protocol.Boolean(true),
					"summary":   protocol.String("Header"),
					"title":     protocol.String("Header accepted"),
				})),
			})),
			metrics: protocol.Object(map[string]protocol.Value{
				"accepted_writes":          protocol.Integer("2"),
				"rejected_replays":         protocol.Integer("1"),
				"secret_values_serialized": protocol.Integer("0"),
			}),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			t.Parallel()
			handler, ok := authSessionScenarioHandler(test.scenario)
			if !ok {
				t.Fatalf("scenario %q is not registered", test.scenario)
			}
			observation, err := handler(context.Background(), protocol.Contract{
				ID:       test.id,
				Scenario: test.scenario,
				Phase:    test.phase,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ID != test.id || observation.Status != protocol.StatusObserved || observation.Phase != test.phase {
				t.Fatalf("observation envelope = %#v", observation)
			}
			if observation.Result == nil || !reflect.DeepEqual(*observation.Result, test.result) {
				t.Fatalf("result = %#v, want %#v", observation.Result, test.result)
			}
			if !reflect.DeepEqual(observation.DBState, test.dbState) {
				t.Fatalf("db state = %#v, want %#v", observation.DBState, test.dbState)
			}
			if observation.Metrics == nil || !reflect.DeepEqual(*observation.Metrics, test.metrics) {
				t.Fatalf("metrics = %#v, want %#v", observation.Metrics, test.metrics)
			}
			encoded, err := json.Marshal(observation)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{
				authSessionAdminPassword,
				authSessionLimitedPassword,
				authSessionInactivePassword,
				sessionCookieWireName,
				csrfCookieWireName,
			} {
				if strings.Contains(string(encoded), secret) {
					t.Fatalf("observation serialized forbidden value %q", secret)
				}
			}
		})
	}
}

const (
	sessionCookieWireName = "godj_session"
	csrfCookieWireName    = "godj_csrf"
)

func authSessionTestValuePointer(value protocol.Value) *protocol.Value { return &value }
