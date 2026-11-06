package godj

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestArticleAdminScenarioHandlersExecuteActualSiteBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id       string
		scenario string
		phase    protocol.Phase
		dbState  bool
	}{
		{id: "ADM-001", scenario: "django.article_admin.access_matrix", phase: protocol.PhaseEvaluation},
		{id: "ADM-002", scenario: "django.article_admin.stable_list", phase: protocol.PhaseEvaluation, dbState: true},
		{id: "ADM-003", scenario: "django.article_admin.search_boundary", phase: protocol.PhaseEvaluation, dbState: true},
		{id: "ADM-004", scenario: "django.article_admin.change_form_shape", phase: protocol.PhaseEvaluation, dbState: true},
		{id: "ADM-005", scenario: "django.article_admin.invalid_edit", phase: protocol.PhaseEvaluation, dbState: true},
		{id: "ADM-006", scenario: "django.article_admin.valid_add", phase: protocol.PhaseCommit, dbState: true},
		{id: "ADM-007", scenario: "django.article_admin.valid_edit", phase: protocol.PhaseCommit, dbState: true},
		{id: "ADM-008", scenario: "django.article_admin.delete_boundaries", phase: protocol.PhaseCommit, dbState: true},
		{id: "ADM-009", scenario: "django.article_admin.semantic_history", phase: protocol.PhaseEvaluation, dbState: true},
		{id: "ADM-010", scenario: "django.article_admin.publish_action", phase: protocol.PhaseCommit, dbState: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			t.Parallel()
			handler, ok := articleAdminScenarioHandler(test.scenario)
			if !ok {
				t.Fatalf("articleAdminScenarioHandler(%q) is not registered", test.scenario)
			}
			observation, err := handler(context.Background(), protocol.Contract{
				ID:       test.id,
				Scenario: test.scenario,
				Phase:    test.phase,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := observation.Validate(); err != nil {
				t.Fatalf("Observation.Validate() error = %v", err)
			}
			if observation.ID != test.id || observation.Phase != test.phase || observation.Status != protocol.StatusObserved {
				t.Fatalf("observation identity = %q/%q/%q", observation.ID, observation.Phase, observation.Status)
			}
			if observation.Result == nil || observation.Metrics == nil {
				t.Fatalf("observation result/metrics = %#v/%#v", observation.Result, observation.Metrics)
			}
			if (observation.DBState != nil) != test.dbState {
				t.Fatalf("observation DB state present = %t, want %t", observation.DBState != nil, test.dbState)
			}
		})
	}
}

func TestArticleAdminStableListReportsGoDjRegistryHonestly(t *testing.T) {
	t.Parallel()
	handler, ok := articleAdminScenarioHandler("django.article_admin.stable_list")
	if !ok {
		t.Fatal("stable-list handler is not registered")
	}
	observation, err := handler(context.Background(), protocol.Contract{
		ID:       "ADM-002",
		Scenario: "django.article_admin.stable_list",
		Phase:    protocol.PhaseEvaluation,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := protocol.MarshalCanonical(observation.Result)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := protocol.MarshalCanonical(observation.Metrics)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"name":"actions","value":{"type":"list","items":[{"type":"string","value":"publish"}]}`) {
		t.Fatalf("stable-list actions do not report the one registered publish action: %s", result)
	}
	if strings.Contains(string(result), "delete_selected") || strings.Contains(string(result), "publish_selected") {
		t.Fatalf("stable-list result synthesized a Django-only action: %s", result)
	}
	if !strings.Contains(string(metrics), `"registered_models","value":{"type":"int","value":"1"}`) {
		t.Fatalf("stable-list metrics do not report the one-model registry: %s", metrics)
	}
}

func TestArticleAdminSemanticHTMLNormalizersReadRenderedSurface(t *testing.T) {
	t.Parallel()
	body := `<table><thead><tr><th>Select</th><th data-list-field="id">id</th><th data-list-field="title">title</th><th>Operations</th></tr></thead>` +
		`<tbody><tr data-object-id="4"><td><input type="checkbox" name="selected" value="4"></td></tr></tbody></table>` +
		`<button type="submit" formaction="/admin/articles/action/publish/" data-action="publish">Publish</button>`
	columns, err := articleAdminRenderedColumns(body, []int64{4})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"action_checkbox", "id", "title"}; !reflect.DeepEqual(columns, want) {
		t.Fatalf("rendered columns = %v, want %v", columns, want)
	}
	paths, actions, err := articleAdminRenderedActions(body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actions, []string{"publish"}) || paths["publish"] != "/admin/articles/action/publish/" {
		t.Fatalf("rendered actions = %v / %v", actions, paths)
	}
	if _, err := articleAdminRenderedColumns(strings.ReplaceAll(body, `name="selected"`, `name="other"`), []int64{4}); err == nil {
		t.Fatal("rendered columns accepted a synthesized selection column without selected inputs")
	}

	form := `<div data-field-name="title"><input name="title" value="A&amp;B">` +
		`<p data-error-field="title" data-error-code="observed_code">observed</p></div>` +
		`<div data-field-name="published"><input type="checkbox" name="published" value="on" checked></div>`
	title, found, err := articleAdminInput(form, "title")
	if err != nil || !found {
		t.Fatalf("rendered title input = found %t, err %v", found, err)
	}
	if value, ok := title.attribute("value"); !ok || value != "A&B" {
		t.Fatalf("rendered title value = %q/%t", value, ok)
	}
	errors, err := articleAdminRenderedErrors(form, []string{"title", "published"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []articleAdminRenderedError{{field: "title", code: "observed_code"}}; !reflect.DeepEqual(errors, want) {
		t.Fatalf("rendered errors = %#v, want %#v", errors, want)
	}

	history := `<body data-object-id="9"><ol><li data-sequence="1" data-action="change" data-actor="operator">` +
		`change by operator: Label [title published ]</li></ol></body>`
	entries, err := articleAdminRenderedHistory(history)
	if err != nil {
		t.Fatal(err)
	}
	wantHistory := []articleAdminRenderedHistoryEntry{{
		sequence: 1, action: "change", actor: "operator", objectID: 9, changedFields: []string{"title", "published"},
	}}
	if !reflect.DeepEqual(entries, wantHistory) {
		t.Fatalf("rendered history = %#v, want %#v", entries, wantHistory)
	}

	noticeBody := `<p data-admin-message="published" data-affected="2">2 object(s) published.</p>`
	notice, found, err := articleAdminRenderedAdminNotice(noticeBody)
	if err != nil || !found {
		t.Fatalf("rendered notice = found %t, err %v", found, err)
	}
	if level, ok := articleAdminNormalizedMessageLevel(notice); !ok || level != 25 {
		t.Fatalf("normalized rendered notice = %d/%t", level, ok)
	}
	notice.text = "different"
	if _, ok := articleAdminNormalizedMessageLevel(notice); ok {
		t.Fatal("altered rendered notice produced a synthesized success level")
	}
}

func TestArticleAdminScenarioHandlerFailsClosed(t *testing.T) {
	t.Parallel()
	if handler, ok := articleAdminScenarioHandler("django.article_admin.unknown"); ok || handler != nil {
		t.Fatalf("unknown Article Admin scenario = %#v, %t", handler, ok)
	}
}

func TestArticleAdminScenarioAdapterDoesNotReplayExpectedArtifacts(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("gdj0043_article_admin_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{
		"conformance/oracles",
		"conformance/fixtures",
		"LoadManifest",
		"LoadObservationSuite",
		"LoadDeviationExpectation",
		"protocol.Compare",
		"os.ReadFile",
		"json.Unmarshal",
		`columns := append([]string{"action_checkbox"}`,
		`protocol.String("max_length")`,
		`protocol.String("null_characters_not_allowed")`,
		`requestsBeforeAction`,
		`eventValues[index] = articleAdminAuditValue(entry)`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Article Admin adapter contains expected-artifact replay fragment %q", forbidden)
		}
	}
}

func TestArticleAdminObservedPayloadMatchesReferenceExceptReviewedRegistryDifference(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "article-admin-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	reference, err := protocol.LoadObservationSuite(filepath.Join(
		root,
		"conformance",
		"oracles",
		"django-6.1-sqlite-darwin-arm64",
		"article-admin-oracle.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contracts) != len(reference.Contracts) {
		t.Fatalf("manifest/reference contract count = %d/%d", len(manifest.Contracts), len(reference.Contracts))
	}
	for index, contract := range manifest.Contracts {
		handler, ok := articleAdminScenarioHandler(contract.Scenario)
		if !ok {
			t.Fatalf("contract %s scenario %q is not registered", contract.ID, contract.Scenario)
		}
		actual, err := handler(context.Background(), contract)
		if err != nil {
			t.Fatalf("contract %s: %v", contract.ID, err)
		}
		want := reference.Contracts[index]
		if contract.ID == "ADM-002" {
			want.Result = valuePointer(articleAdminTestReplaceObjectField(
				t,
				*want.Result,
				"actions",
				protocol.List(protocol.String("publish")),
			))
			want.Metrics = valuePointer(articleAdminTestReplaceObjectField(
				t,
				*want.Metrics,
				"registered_models",
				protocol.Integer("1"),
			))
		}
		if !reflect.DeepEqual(actual, want) {
			actualJSON, actualErr := protocol.MarshalCanonical(protocol.ObservationSuite{
				FormatVersion: protocol.FormatVersion,
				Profile:       reference.Profile,
				Contracts:     []protocol.Observation{actual},
			})
			wantJSON, wantErr := protocol.MarshalCanonical(protocol.ObservationSuite{
				FormatVersion: protocol.FormatVersion,
				Profile:       reference.Profile,
				Contracts:     []protocol.Observation{want},
			})
			if actualErr != nil || wantErr != nil {
				t.Fatalf("contract %s mismatch; canonical errors actual=%v want=%v", contract.ID, actualErr, wantErr)
			}
			t.Fatalf("contract %s observation mismatch\nactual: %s\nwant:   %s", contract.ID, actualJSON, wantJSON)
		}
	}
}

func articleAdminTestReplaceObjectField(t *testing.T, object protocol.Value, name string, replacement protocol.Value) protocol.Value {
	t.Helper()
	if object.Type != protocol.ValueObject {
		t.Fatalf("replace field %q on value type %q", name, object.Type)
	}
	fields := append([]protocol.NamedValue(nil), object.Fields...)
	for index := range fields {
		if fields[index].Name == name {
			fields[index].Value = replacement
			object.Fields = fields
			return object
		}
	}
	t.Fatalf("object has no field %q", name)
	return protocol.Value{}
}
