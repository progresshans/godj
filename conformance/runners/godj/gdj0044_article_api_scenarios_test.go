package godj

import (
	"context"
	"net/http"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0044ArticleAPIHandlersObserveSQLiteProduct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		scenario string
		phase    protocol.Phase
		dbState  bool
	}{
		{id: "API-001", scenario: "drf.article_api.json_transport_boundary", phase: protocol.PhaseEvaluation},
		{id: "API-002", scenario: "drf.article_api.article_serializer_semantics", phase: protocol.PhaseEvaluation},
		{id: "API-003", scenario: "drf.article_api.session_permission_csrf_denial", phase: protocol.PhaseEvaluation, dbState: true},
		{id: "API-004", scenario: "drf.article_api.list_filter_order", phase: protocol.PhaseEvaluation, dbState: true},
		{id: "API-005", scenario: "drf.article_api.page_number_pagination", phase: protocol.PhaseEvaluation, dbState: true},
		{id: "API-006", scenario: "drf.article_api.create_article", phase: protocol.PhaseCommit, dbState: true},
		{id: "API-007", scenario: "drf.article_api.retrieve_article", phase: protocol.PhaseEvaluation, dbState: true},
		{id: "API-008", scenario: "drf.article_api.full_update", phase: protocol.PhaseCommit, dbState: true},
		{id: "API-009", scenario: "drf.article_api.partial_update", phase: protocol.PhaseCommit, dbState: true},
		{id: "API-010", scenario: "drf.article_api.delete_article", phase: protocol.PhaseCommit, dbState: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			t.Parallel()
			handler, ok := articleAPIScenarioHandler(test.scenario)
			if !ok {
				t.Fatalf("scenario %q is not registered in the local Article API handler", test.scenario)
			}
			observation, err := handler(context.Background(), protocol.Contract{
				ID: test.id, Scenario: test.scenario, Phase: test.phase,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ID != test.id || observation.Status != protocol.StatusObserved || observation.Phase != test.phase {
				t.Fatalf("observation envelope = %#v", observation)
			}
			if observation.Result == nil || observation.Metrics == nil || observation.Error != nil || (observation.DBState != nil) != test.dbState {
				t.Fatalf("observation dimensions = %#v", observation)
			}
			if err := observation.Result.Validate(); err != nil {
				t.Fatalf("result: %v", err)
			}
			if err := observation.Metrics.Validate(); err != nil {
				t.Fatalf("metrics: %v", err)
			}
			if observation.DBState != nil {
				if err := observation.DBState.Validate(); err != nil {
					t.Fatalf("db_state: %v", err)
				}
			}
		})
	}
}

func TestGDJ0044ArticleAPIDenialsMutateNoRowsAndObserveEveryUnsafeAttempt(t *testing.T) {
	t.Parallel()

	handler, _ := articleAPIScenarioHandler("drf.article_api.session_permission_csrf_denial")
	observation, err := handler(context.Background(), protocol.Contract{
		ID: "API-003", Scenario: "drf.article_api.session_permission_csrf_denial", Phase: protocol.PhaseEvaluation,
	})
	if err != nil {
		t.Fatal(err)
	}
	unsafeAttempts := parameterRoutingTestObjectField(t, *observation.Result, "unsafe_attempts")
	if unsafeAttempts.Type != protocol.ValueList || len(unsafeAttempts.Items) != 4 {
		t.Fatalf("unsafe_attempts = %#v, want four observations", unsafeAttempts)
	}
	mutations := parameterRoutingTestObjectField(t, *observation.Metrics, "article_mutations")
	if mutations.Text == nil || *mutations.Text != "0" {
		t.Fatalf("article_mutations = %#v, want 0", mutations)
	}
}

func TestGDJ0044ArticleAPICreateAndDeleteUseOneDurableMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		scenario string
		want     string
	}{
		{id: "API-006", scenario: "drf.article_api.create_article", want: "1"},
		{id: "API-010", scenario: "drf.article_api.delete_article", want: "-1"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			t.Parallel()
			handler, _ := articleAPIScenarioHandler(test.scenario)
			observation, err := handler(context.Background(), protocol.Contract{
				ID: test.id, Scenario: test.scenario, Phase: protocol.PhaseCommit,
			})
			if err != nil {
				t.Fatal(err)
			}
			delta := parameterRoutingTestObjectField(t, *observation.Metrics, "article_row_delta")
			if delta.Text == nil || *delta.Text != test.want {
				t.Fatalf("article_row_delta = %#v, want %s", delta, test.want)
			}
		})
	}
}

func TestGDJ0044ArticleAPIRequestCancellationPreventsDurableMutation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fixture, err := newArticleAPIFixture(ctx, "API-CONTEXT")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fixture.close(); err != nil {
			t.Errorf("close Article API fixture: %v", err)
		}
	})
	if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Existing"}); err != nil {
		t.Fatal(err)
	}
	client := fixture.client(fixture.allSession)
	token, err := client.csrf(ctx, "/api/articles/")
	if err != nil {
		t.Fatal(err)
	}
	before, err := articleAPICount(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}

	cancel()
	response, err := client.do(ctx, http.MethodPost, "/api/articles/", articleAPIRequestOptions{
		body: `{"title":"Must Not Persist"}`, contentType: api.JSONContentType, token: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.status == http.StatusCreated {
		t.Fatal("canceled Article API request returned a successful create")
	}
	after, err := articleAPICount(context.Background(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("canceled Article API row count = %d, want unchanged %d", after, before)
	}
}

func TestGDJ0044UnknownArticleAPIScenarioStaysLocalFailClosed(t *testing.T) {
	t.Parallel()

	if handler, ok := articleAPIScenarioHandler("drf.article_api.unknown"); ok || handler != nil {
		t.Fatalf("unknown handler = %v, %t", handler, ok)
	}
}
