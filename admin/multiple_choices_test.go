package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

type multipleSelection struct {
	id   int64
	keys []int64
}

func multipleChoiceConfig(t *testing.T) (*Builder, ModelConfig[multipleSelection]) {
	t.Helper()
	builder, base := ticketSelectionConfig(t)
	config := ModelConfig[multipleSelection]{
		AppLabel: base.AppLabel, Slug: base.Slug, Model: base.Model,
		FormFields: []string{"labels"}, ListFields: []string{"id"},
		FormOverrides: []formmodel.Override{formmodel.OverrideField("labels", formmodel.WithRequired(false))},
		Permissions:   Permissions{View: "helpdesk.view_ticket", Add: "helpdesk.add_ticket", Change: "helpdesk.change_ticket", Delete: "helpdesk.delete_ticket"},
		List: func(_ context.Context, request ListRequest) (Page[multipleSelection], error) {
			return Page[multipleSelection]{Items: []multipleSelection{{1, []int64{7, 9}}}, Total: 1, Offset: request.Offset, Limit: request.Limit}, nil
		},
		Get: func(context.Context, int64) (multipleSelection, bool, error) {
			return multipleSelection{1, []int64{7, 9}}, true, nil
		},
		Snapshot: func(row multipleSelection) (Object, error) {
			values := make([]templates.Value, len(row.keys))
			for i, key := range row.keys {
				values[i] = templates.Integer(key)
			}
			return NewObject(row.id, "Ticket", map[string]templates.Value{"id": templates.Integer(row.id), "labels": templates.List(values...)})
		},
		Initial: func(row multipleSelection) (map[string]forms.Value, error) {
			return map[string]forms.Value{"labels": forms.Integers(row.keys...)}, nil
		},
		Create: func(_ context.Context, _ auth.Principal, values forms.Values) (multipleSelection, error) {
			keys, ok := values.Integers("labels")
			if !ok {
				return multipleSelection{}, errors.New("missing collection")
			}
			return multipleSelection{1, keys}, nil
		},
		Update: func(_ context.Context, _ auth.Principal, id int64, values forms.Values) (multipleSelection, []string, error) {
			keys, ok := values.Integers("labels")
			if !ok {
				return multipleSelection{}, nil, errors.New("missing collection")
			}
			return multipleSelection{id, keys}, []string{"labels"}, nil
		},
		Delete: func(context.Context, auth.Principal, int64) (multipleSelection, error) {
			return multipleSelection{1, []int64{}}, nil
		},
		RelatedChoices: []RelatedChoices{{Field: "labels", Permission: "helpdesk.view_label", Load: func(context.Context, auth.Principal) ([]forms.Choice, error) {
			return []forms.Choice{{Value: forms.Integer(7), Label: "Seven"}, {Value: forms.Integer(9), Label: "Nine"}}, nil
		}}},
	}
	return builder, config
}

func TestMultipleChoicesRevalidateEveryKeyAndPreserveEmptyReplacement(t *testing.T) {
	builder, config := multipleChoiceConfig(t)
	choices, _ := config.RelatedChoices[0].Load(context.Background(), auth.Principal{})
	var failure error
	var reads, writes int
	config.RelatedChoices[0].Load = func(context.Context, auth.Principal) ([]forms.Choice, error) { reads++; return choices, failure }
	create, update := config.Create, config.Update
	var received []int64
	config.Create = func(ctx context.Context, p auth.Principal, v forms.Values) (multipleSelection, error) {
		writes++
		received, _ = v.Integers("labels")
		return create(ctx, p, v)
	}
	config.Update = func(ctx context.Context, p auth.Principal, id int64, v forms.Values) (multipleSelection, []string, error) {
		writes++
		received, _ = v.Integers("labels")
		return update(ctx, p, id, v)
	}
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	model := registry.models[0]
	principal := mustPrincipalWithPermissions(t, config.Permissions.View, config.Permissions.Add, config.Permissions.Change, "helpdesk.view_label")
	spec, err := model.formFor(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"labels": {"9", "7", "9"}}), nil)
	if err != nil || !bound.Valid() {
		t.Fatal(err, bound.Errors())
	}
	for _, isUpdate := range []bool{false, true} {
		run := func(form forms.Form) error {
			if isUpdate {
				_, _, err := model.update(context.Background(), principal, 1, form)
				return err
			}
			_, err := model.create(context.Background(), principal, form)
			return err
		}
		before := writes
		choices = choices[:1]
		if rejected, ok := validation.Rejected(run(bound)); !ok || len(rejected.All()) != 1 || rejected.All()[0].Field() != "labels" || rejected.All()[0].Code() != "invalid_choice" || writes != before {
			t.Fatal("missing second choice reached mutation", rejected, writes)
		}
		failure = errors.New("choice source unavailable")
		if err := run(bound); !errors.Is(err, failure) || writes != before {
			t.Fatal("source failure changed data", err)
		}
		failure = nil
		choices = append(choices, forms.Choice{Value: forms.Integer(9), Label: "Nine"})
		if err := run(bound); err != nil || writes != before+1 || !slices.Equal(received, []int64{7, 9}) {
			t.Fatal("collection was truncated or duplicated", err, received)
		}
		empty, _ := spec.Bind(forms.NewData(nil), nil)
		if err := run(empty); err != nil || received == nil || len(received) != 0 {
			t.Fatal("empty replacement was omitted", err, received)
		}
	}
	denied := mustPrincipalWithPermissions(t, config.Permissions.Add)
	beforeReads, beforeWrites := reads, writes
	if _, err := model.create(context.Background(), denied, bound); err == nil || reads != beforeReads || writes != beforeWrites {
		t.Fatal("unauthorized collection queried or mutated", err)
	}
}

func TestMultipleChoicesRejectMissingSourcesAndInconsistentSnapshots(t *testing.T) {
	builder, config := multipleChoiceConfig(t)
	config.RelatedChoices = nil
	if err := RegisterModel(builder, config); err == nil {
		t.Fatal("collection without source accepted")
	}
	for _, test := range []struct {
		name   string
		change func(*ModelConfig[multipleSelection])
	}{
		{"scalar snapshot", func(c *ModelConfig[multipleSelection]) {
			c.Snapshot = func(row multipleSelection) (Object, error) {
				return NewObject(row.id, "Ticket", map[string]templates.Value{"id": templates.Integer(row.id), "labels": templates.Integer(7)})
			}
		}},
		{"nonpositive key", func(c *ModelConfig[multipleSelection]) {
			c.Get = func(context.Context, int64) (multipleSelection, bool, error) {
				return multipleSelection{1, []int64{0}}, true, nil
			}
		}},
		{"null initial", func(c *ModelConfig[multipleSelection]) {
			c.Initial = func(multipleSelection) (map[string]forms.Value, error) {
				return map[string]forms.Value{"labels": forms.Null()}, nil
			}
		}},
		{"different cardinality", func(c *ModelConfig[multipleSelection]) {
			c.Initial = func(multipleSelection) (map[string]forms.Value, error) {
				return map[string]forms.Value{"labels": forms.Integers(7)}, nil
			}
		}},
		{"different order", func(c *ModelConfig[multipleSelection]) {
			c.Initial = func(multipleSelection) (map[string]forms.Value, error) {
				return map[string]forms.Value{"labels": forms.Integers(9, 7)}, nil
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder, config := multipleChoiceConfig(t)
			test.change(&config)
			if err := RegisterModel(builder, config); err != nil {
				t.Fatal(err)
			}
			registry, err := builder.Build()
			if err != nil {
				t.Fatal(err)
			}
			p := mustPrincipalWithPermissions(t, config.Permissions.View, config.Permissions.Change, "helpdesk.view_label")
			if _, _, err := registry.models[0].get(context.Background(), p, 1); err == nil {
				t.Fatal("inconsistent collection published")
			}
		})
	}
}

func articleMultipleChoices(t *testing.T, config *ModelConfig[registryArticle], load func(context.Context, auth.Principal) ([]forms.Choice, error)) {
	t.Helper()
	definition, err := schema.Build(schema.Definition{AppLabel: config.AppLabel, Models: []schema.Model{{Name: config.Model.Name, GoName: config.Model.GoName, Fields: []schema.Field{schema.CharField("title", "Title", 100)}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("labels", "Labels", schema.Target(config.AppLabel, "label"), schema.RelatedName("articles"))}}}})
	if err != nil {
		t.Fatal(err)
	}
	config.Model.ManyToMany = definition.Models[0].ManyToMany
	config.FormFields = []string{"title", "summary", "published", "labels"}
	config.FormOverrides = append(config.FormOverrides, formmodel.OverrideField("labels", formmodel.WithRequired(false)))
	config.RelatedChoices = []RelatedChoices{{Field: "labels", Permission: "articles.view", Load: load}}
	snapshot, initial := config.Snapshot, config.Initial
	config.Snapshot = func(row registryArticle) (Object, error) {
		base, err := snapshot(row)
		if err != nil {
			return Object{}, err
		}
		members, _ := base.Values().Members()
		values := make(map[string]templates.Value, len(members)+1)
		for _, member := range members {
			values[member.Name()] = member.Value()
		}
		values["labels"] = templates.List(templates.Integer(7), templates.Integer(99))
		return NewObject(base.ID(), base.Label(), values)
	}
	config.Initial = func(row registryArticle) (map[string]forms.Value, error) {
		values, err := initial(row)
		if err != nil {
			return nil, err
		}
		values["labels"] = forms.Integers(7, 99)
		return values, nil
	}
}

func TestMultipleChoicesHTTPRenderingCSRFAndWholeSetRevalidation(t *testing.T) {
	var reads int
	var dropSecondOnRead int
	load := func(context.Context, auth.Principal) ([]forms.Choice, error) {
		reads++
		choices := []forms.Choice{{Value: forms.Integer(7), Label: `<script>Seven & "quoted"</script>`}}
		if reads != dropSecondOnRead {
			choices = append(choices, forms.Choice{Value: forms.Integer(9), Label: "Nine"})
		}
		return choices, nil
	}
	harness := newSiteApplicationHarnessWithAuthorizer(t, 10, auth.PrincipalAuthorizer{}, func(config *ModelConfig[registryArticle]) { articleMultipleChoices(t, config, load) })
	client := newSiteHTTPClient(harness.application)
	client.login(t, "admin", "secret", "/admin/articles/")
	change := client.do(http.MethodGet, "/admin/articles/change/?id=1", nil)
	body := change.Body.String()
	if change.Code != http.StatusOK || !strings.Contains(body, `<select name="labels" multiple>`) || !strings.Contains(body, `<option value="7" selected>`) || !strings.Contains(body, `<option value="99" selected>99 (not a current choice)</option>`) || strings.Contains(body, `<script>`) || !strings.Contains(body, `&lt;script&gt;`) {
		t.Fatalf("invalid multiple widget: %d %s", change.Code, body)
	}
	token := siteCSRFToken(t, body)
	before, beforeReads := harness.state.mutationCounts(), reads
	noCSRF := client.do(http.MethodPost, "/admin/articles/change/?id=1", url.Values{"labels": {"7", "9"}})
	if noCSRF.Code != http.StatusForbidden || reads != beforeReads || harness.state.mutationCounts() != before {
		t.Fatal("CSRF rejection queried candidates or mutated", noCSRF.Code, reads, beforeReads)
	}
	input := url.Values{"csrfmiddlewaretoken": {token}, "title": {"First article"}, "summary": {""}, "labels": {"7", "<script>bad</script>"}}
	invalid := client.do(http.MethodPost, "/admin/articles/change/?id=1", input)
	if invalid.Code != http.StatusOK || !strings.Contains(invalid.Body.String(), `data-error-code="invalid_pk_value"`) || !strings.Contains(invalid.Body.String(), `<option value="7" selected>`) || strings.Contains(invalid.Body.String(), `<script>`) || !strings.Contains(invalid.Body.String(), `&lt;script&gt;bad&lt;/script&gt;`) || harness.state.mutationCounts() != before {
		t.Fatal("invalid collection was truncated, unsafe or mutated", invalid.Code, invalid.Body.String())
	}
	input.Set("labels", "7")
	input.Add("labels", "9")
	dropSecondOnRead = reads + 2 // first binding succeeds; the mutation's fresh source must reject.
	stale := client.do(http.MethodPost, "/admin/articles/change/?id=1", input)
	if stale.Code != http.StatusOK || !strings.Contains(stale.Body.String(), `data-error-code="invalid_choice"`) || harness.state.mutationCounts() != before {
		t.Fatal("stale second key reached mutation", stale.Code, stale.Body.String())
	}
	dropSecondOnRead = 0
	valid := client.do(http.MethodPost, "/admin/articles/change/?id=1", input)
	if valid.Code != http.StatusFound || harness.state.mutationCounts().updates != before.updates+1 {
		t.Fatal("valid multiselect did not reach mutation", valid.Code, valid.Body.String())
	}
}

func TestMultipleChoicesHTTPAuthorizerDeniesBeforeCandidateReads(t *testing.T) {
	reads := 0
	harness := newSiteApplicationHarnessWithAuthorizer(t, 10, siteDenyAuthorizer{denied: mustPermission(t, "articles.view")}, func(config *ModelConfig[registryArticle]) {
		articleMultipleChoices(t, config, func(context.Context, auth.Principal) ([]forms.Choice, error) { reads++; return nil, nil })
	})
	client := newSiteHTTPClient(harness.application)
	client.login(t, "admin", "secret", "/admin/")
	for _, target := range []string{"/admin/articles/add/", "/admin/articles/change/?id=1"} {
		if result := client.do(http.MethodGet, target, nil); result.Code != http.StatusForbidden || reads != 0 {
			t.Fatal("denied candidates accessed", result.Code, reads)
		}
	}
}
