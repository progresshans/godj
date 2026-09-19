package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/modeldef"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

type selectionTicket struct {
	id       int64
	subject  string
	category int64
}

func TestRegistrationSnapshotRequiresOnlyDisplayedAndEditableFields(t *testing.T) {
	for _, outcome := range []string{"selected", "missing displayed field", "unknown field"} {
		t.Run(outcome, func(t *testing.T) {
			builder, config := ticketSelectionConfig(t)
			config.Model.Fields = append(config.Model.Fields, ir.Field{Name: "internal_note", GoName: "InternalNote", Column: "internal_note", Kind: ir.FieldChar, MaxLength: 200, Nullable: true})
			config.Snapshot = func(ticket selectionTicket) (Object, error) {
				values := map[string]templates.Value{"subject": templates.String(ticket.subject), "category": templates.Integer(ticket.category)}
				if outcome == "missing displayed field" {
					delete(values, "category")
				}
				if outcome == "unknown field" {
					values["unknown"] = templates.String("unmodeled")
				}
				return NewObject(ticket.id, ticket.subject, values)
			}
			if err := RegisterModel(builder, config); err != nil {
				t.Fatal(err)
			}
			registry, err := builder.Build()
			if err != nil {
				t.Fatal(err)
			}
			principal := mustPrincipalWithPermissions(t, config.Permissions.View)
			page, err := registry.models[0].list(context.Background(), principal, ListRequest{})
			switch outcome {
			case "selected":
				if err != nil || len(page.objects) != 1 {
					t.Fatalf("unexposed storage field forced snapshot changes: %v", err)
				}
				if _, exposed := page.objects[0].Value("internal_note"); exposed {
					t.Fatal("storage-only field was exposed")
				}
			case "missing displayed field":
				if errorCode(err) != "missing_field" {
					t.Fatalf("missing actual display field: %v", err)
				}
			case "unknown field":
				if errorCode(err) != "unknown_field" {
					t.Fatalf("unmodeled snapshot value: %v", err)
				}
			}
		})
	}
	config := validRegistryConfig(t)
	config.Snapshot = func(article registryArticle) (Object, error) {
		return NewObject(article.id, article.title, map[string]templates.Value{"id": templates.Integer(article.id), "title": templates.String(article.title), "published": templates.Bool(article.published)})
	}
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, _ := builder.Build()
	if _, err := registry.models[0].list(context.Background(), mustPrincipal(t), ListRequest{}); errorCode(err) != "missing_field" {
		t.Fatalf("missing editable summary accepted although not a list column: %v", err)
	}
}

func ticketSelectionConfig(t *testing.T) (*Builder, ModelConfig[selectionTicket]) {
	t.Helper()
	schema, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	installed, err := apps.New([]apps.Config{{Name: "example/helpdesk", Label: "helpdesk"}})
	if err != nil {
		t.Fatal(err)
	}
	config := ModelConfig[selectionTicket]{AppLabel: "helpdesk", Slug: "tickets", Model: schema.Models[1],
		ListFields: []string{"subject", "category"}, SearchFields: []string{"subject"}, ReadOnly: true,
		Permissions: Permissions{View: "helpdesk.view_ticket"},
		List: func(_ context.Context, request ListRequest) (Page[selectionTicket], error) {
			return Page[selectionTicket]{Items: []selectionTicket{{1, "Printer", 2}}, Total: 1, Offset: request.Offset, Limit: request.Limit}, nil
		},
		Snapshot: func(ticket selectionTicket) (Object, error) {
			return NewObject(ticket.id, ticket.subject, map[string]templates.Value{
				"id": templates.Integer(ticket.id), "subject": templates.String(ticket.subject), "details": templates.Null(), "closed": templates.Bool(false), "category": templates.Integer(ticket.category),
			})
		},
	}
	return NewBuilder(installed), config
}

func TestReadOnlyRelationModelRequiresNoMutationOrFormAdapter(t *testing.T) {
	builder, config := ticketSelectionConfig(t)
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	model := registry.models[0]
	principal := mustPrincipalWithPermissions(t, DefaultAccessPermission, config.Permissions.View)
	page, err := model.list(context.Background(), principal, ListRequest{})
	if err != nil || len(page.objects) != 1 {
		t.Fatalf("relation list: %v %v", page, err)
	}
	if _, err := model.create(context.Background(), principal, forms.Form{}); errorCode(err) != "read_only" {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := model.update(context.Background(), principal, 1, forms.Form{}); errorCode(err) != "read_only" {
		t.Fatalf("update: %v", err)
	}
	if _, err := model.delete(context.Background(), principal, 1); errorCode(err) != "read_only" {
		t.Fatalf("delete: %v", err)
	}
	paths, err := SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	runtime, _ := siteTestRuntimeConfigured(t, paths, "/admin/login/", "/admin/", []siteIdentity{{username: "reader", principal: principal}}, "/", "/", auth.PrincipalAuthorizer{})
	site, err := NewSite(SiteConfig{Apps: builder.state.apps, Namespace: "helpdesk", Registry: registry, Auth: runtime})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range site.Routes() {
		if strings.HasPrefix(route.Path, "/admin/tickets/") && (route.Method != "GET" || route.Path != "/admin/tickets/") {
			t.Fatalf("readonly mutation route: %+v", route)
		}
	}
	view, err := site.listContext(context.Background(), model, principal, page, 1, "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, _ := view["can_add"].AsBool(); allowed {
		t.Fatal("readonly add link")
	}
}

func TestSelectedAdminFormKeepsForeignKeyOutsideWritableSurface(t *testing.T) {
	builder, config := ticketSelectionConfig(t)
	config.ReadOnly = false
	config.FormFields = []string{"subject"}
	config.Permissions = Permissions{View: "helpdesk.view_ticket", Add: "helpdesk.add_ticket", Change: "helpdesk.change_ticket", Delete: "helpdesk.delete_ticket"}
	config.Get = func(context.Context, int64) (selectionTicket, bool, error) {
		return selectionTicket{1, "Printer", 2}, true, nil
	}
	config.Initial = func(ticket selectionTicket) (map[string]forms.Value, error) {
		return map[string]forms.Value{"subject": forms.String(ticket.subject)}, nil
	}
	config.Create = func(_ context.Context, _ auth.Principal, values forms.Values) (selectionTicket, error) {
		if len(values.All()) != 1 {
			t.Fatal("unselected data reached create")
		}
		subject, _ := values.String("subject")
		return selectionTicket{2, subject, 2}, nil
	}
	config.Update = func(context.Context, auth.Principal, int64, forms.Values) (selectionTicket, []string, error) {
		return selectionTicket{1, "Updated", 2}, []string{"subject"}, nil
	}
	config.Delete = func(context.Context, auth.Principal, int64) (selectionTicket, error) {
		return selectionTicket{1, "Printer", 2}, nil
	}
	config.History = func(context.Context, int64, HistoryRequest) ([]AuditEntry, error) { return nil, nil }
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	model := registry.models[0]
	if len(model.form.Fields()) != 1 || model.form.Fields()[0].Name() != "subject" {
		t.Fatal("wrong writable surface")
	}
	if _, allowed := modelFormRules(model)["category"]; allowed {
		t.Fatal("foreign key accepted from POST")
	}
	principal := mustPrincipalWithPermissions(t, config.Permissions.View, config.Permissions.Add)
	if _, found, err := model.get(context.Background(), principal, 1); err != nil || !found {
		t.Fatalf("selected initial: %v", err)
	}
	bound, err := model.form.Bind(forms.NewData(map[string][]string{"subject": {"New"}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := model.create(context.Background(), principal, bound)
	if err != nil || created.ID() != 2 {
		t.Fatalf("selected create: %v", err)
	}
	config.FormFields = []string{"category"}
	if err := RegisterModel(NewBuilder(builder.state.apps), config); err == nil {
		t.Fatal("unimplemented foreign-key input silently accepted")
	}
}
