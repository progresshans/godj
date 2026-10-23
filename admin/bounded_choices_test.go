package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

func TestRelatedChoiceScopeRejectsMissingRepeatedEscapedAndUnjoinedCallbacks(t *testing.T) {
	failure := errors.New("private target read failure")
	for _, mode := range []string{"missing", "duplicate", "escaped", "after_result", "swallowed", "canceled", "inflight", "nil_backend"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			entered, finished := make(chan struct{}), make(chan struct{})
			read := func(work context.Context, _ auth.Principal) ([]forms.Choice, error) {
				reads++
				if mode == "inflight" {
					close(entered)
					<-work.Done()
					close(finished)
					return nil, work.Err()
				}
				if mode == "swallowed" {
					return nil, failure
				}
				if mode == "canceled" {
					cancel()
				}
				return []forms.Choice{{Value: forms.Integer(7), Label: "Selected"}}, nil
			}
			source := testTargetChoices(t, "target", "accounts.view", read).(relatedChoices[forms.Choice])
			actor := mustPrincipalWithPermissions(t, "accounts.view")
			backend := testChoiceQueryer{actor: actor, read: read}
			var escaped func(db.Queryer, []orm.Predicate[forms.Choice]) error
			source.config.Scope = func(_ context.Context, _ auth.Principal, _ ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[forms.Choice]) error) error {
				switch mode {
				case "missing":
					return nil
				case "escaped":
					escaped = visit
					return nil
				case "duplicate":
					_ = visit(backend, nil)
					_ = visit(backend, nil)
					return nil
				case "after_result":
					if err := visit(backend, nil); err != nil {
						return err
					}
					return failure
				case "swallowed":
					_ = visit(backend, nil)
					return nil
				case "inflight":
					go func() { _ = visit(backend, nil) }()
					<-entered
					return nil
				case "nil_backend":
					return visit(nil, nil)
				default:
					return visit(backend, nil)
				}
			}
			origin := ChoiceOrigin{source: ir.ModelIdentity{AppLabel: "source", ModelName: "record"}, operation: ChoiceAdd, valid: true}
			page, err := source.search(ctx, actor, origin, formmodel.ChoiceRequest{Limit: 1})
			if err == nil || page.Initialized() || len(page.Items()) != 0 {
				t.Fatal("scope misuse published a page", mode, err)
			}
			if mode == "after_result" || mode == "swallowed" {
				if !errors.Is(err, failure) {
					t.Fatal("scope failure lost", err)
				}
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if mode == "inflight" {
				select {
				case <-finished:
				default:
					t.Fatal("scope returned before canceled query joined")
				}
			}
			if mode == "escaped" {
				if escaped == nil || escaped(backend, nil) == nil || reads != 0 {
					t.Fatal("escaped visitor performed a read")
				}
			}
			if (mode == "missing" || mode == "nil_backend") && reads != 0 {
				t.Fatal("invalid scope queried backend")
			}
			if mode == "duplicate" && reads != 1 {
				t.Fatal("duplicate visitor read twice", reads)
			}
		})
	}
}

func TestRelatedChoiceScopePreservesDefiniteDenialAndExpiresSuccessfulVisitor(t *testing.T) {
	actor := mustPrincipalWithPermissions(t, "accounts.view")
	reads := 0
	read := func(context.Context, auth.Principal) ([]forms.Choice, error) {
		reads++
		return []forms.Choice{{Value: forms.Integer(7), Label: "Visible"}}, nil
	}
	source := testTargetChoices(t, "target", "accounts.view", read).(relatedChoices[forms.Choice])
	origin := ChoiceOrigin{operation: ChoiceCollection, valid: true}
	denied := NewOperationError(OperationDenied, nil)
	source.config.Scope = func(context.Context, auth.Principal, ChoiceOrigin, func(db.Queryer, []orm.Predicate[forms.Choice]) error) error {
		return denied
	}
	if page, err := source.search(t.Context(), actor, origin, formmodel.ChoiceRequest{Limit: 1}); err != denied || page.Initialized() || reads != 0 {
		t.Fatal("definite denial became generic failure", err)
	}
	var escaped func(db.Queryer, []orm.Predicate[forms.Choice]) error
	backend := testChoiceQueryer{actor: actor, read: read}
	source.config.Scope = func(_ context.Context, _ auth.Principal, _ ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[forms.Choice]) error) error {
		escaped = visit
		return visit(backend, nil)
	}
	page, err := source.search(t.Context(), actor, origin, formmodel.ChoiceRequest{Limit: 1})
	if err != nil || !page.Initialized() || reads != 1 {
		t.Fatal("normal synchronous scope failed", err)
	}
	if err := escaped(backend, nil); err == nil || reads != 1 {
		t.Fatal("successful visitor remained usable", err)
	}
	if _, err := source.resolve(t.Context(), actor, ChoiceOrigin{}, nil); err == nil {
		t.Fatal("zero origin was admitted")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", origin, origin), "source:") {
		t.Fatal("origin formatting exposed internal state")
	}
}

func TestAdminBoundedChoiceSearchUsesStaticRoutesStringKeysAndCurrentPermissions(t *testing.T) {
	reads, writes := 0, 0
	var denied auth.Permission
	authorizer := managementFormAuthorizer(func(ctx context.Context, p auth.Principal, permission auth.Permission) (bool, error) {
		if permission == denied {
			return false, nil
		}
		return (auth.PrincipalAuthorizer{}).Allowed(ctx, p, permission)
	})
	items := make([]forms.Choice, 0, 24)
	for key := int64(1); key <= 22; key++ {
		items = append(items, forms.Choice{Value: forms.Integer(key), Label: "Small " + strconv.FormatInt(key, 10)})
	}
	items = append(items, forms.Choice{Value: forms.Integer(math.MaxInt64 - 1), Label: "Huge first"}, forms.Choice{Value: forms.Integer(math.MaxInt64), Label: "Huge last"})
	source := testTargetChoices(t, "target", "accounts.delete", func(context.Context, auth.Principal) ([]forms.Choice, error) { reads++; return items, nil }).(relatedChoices[forms.Choice])
	original := source.config.Scope
	source.config.Scope = func(ctx context.Context, actor auth.Principal, origin ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[forms.Choice]) error) error {
		if origin.Source() != (ir.ModelIdentity{AppLabel: "godj_conformance", ModelName: "account"}) || origin.Operation() != ChoiceCollection || origin.Name() != "save" {
			t.Fatal("route lost fixed source/operation", origin.Source(), origin.Operation(), origin.Name())
		}
		if _, ok := origin.ObjectID(); ok {
			t.Fatal("new collection acquired object")
		}
		if _, _, ok := origin.Parent(); ok {
			t.Fatal("collection acquired parent")
		}
		if _, ok := origin.RowIdentity(); ok {
			t.Fatal("collection acquired row identity")
		}
		return original(ctx, actor, origin, visit)
	}
	field, _ := forms.ModelChoiceField("target")
	form, _ := forms.NewSpec([]forms.Field{field})
	client, _, registry := newManagementFormSite(t, authorizer, func(config *ModelConfig[managementFormRow]) {
		config.CollectionCommands = []CollectionCommandConfig{{Name: "save", Label: "Save", Permission: "accounts.change", Form: form, RelatedChoices: []RelatedChoices{source}, Run: func(_ context.Context, _ auth.Principal, values forms.Values) (CollectionCommandResult, error) {
			key, valid := values.Integer("target")
			if !valid || key != math.MaxInt64 {
				t.Fatal("selected key changed", key)
			}
			writes++
			return CollectionCommandResult{ID: 45, Changed: true}, nil
		}}}
	})
	client.login(t, "admin", "secret", "/admin/accounts/")
	path := "/admin/accounts/collection/save/choices/target/"
	for _, test := range []struct {
		suffix string
		status int
		read   bool
	}{
		{"?q=Huge&offset=0&limit=1", 200, true}, {"?q=Huge&offset=1&limit=1", 200, true},
		{"?q=Huge&limit=0", 400, false}, {"?offset=-1", 400, false}, {"?limit=101", 400, false}, {"?limit=1&limit=2", 400, false},
		{"?field=target", 400, false}, {"?source=forged", 400, false}, {"?target=other", 400, false}, {"?id=1", 400, false}, {"?row_id=1", 400, false},
	} {
		before := reads
		response := client.do("GET", path+test.suffix, nil)
		if response.Code != test.status || (reads > before) != test.read {
			t.Fatal("search admission", test.suffix, response.Code, reads-before)
		}
		if test.status == 200 {
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("search response cache/security")
			}
			var payload struct {
				Items []struct {
					Value string
					Label string
				}
				Offset, Limit int
				More          bool
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal("keys were not JSON strings", err)
			}
			expected := strconv.FormatInt(math.MaxInt64-1, 10)
			more := true
			if strings.Contains(test.suffix, "offset=1") {
				expected = strconv.FormatInt(math.MaxInt64, 10)
				more = false
			}
			if len(payload.Items) != 1 || payload.Items[0].Value != expected || payload.Limit != 1 || payload.More != more {
				t.Fatal("bounded page/int64 changed", response.Body.String())
			}
		}
	}
	for _, permission := range []auth.Permission{"accounts.change", "accounts.delete"} {
		denied = permission
		before := reads
		if response := client.do("GET", path, nil); response.Code != 403 || reads != before {
			t.Fatal("deny overlay queried target", permission, response.Code)
		}
	}
	denied = ""
	viewer := newSiteHTTPClient(client.application)
	viewer.login(t, "viewer", "secret", "/admin/accounts/")
	before := reads
	if response := viewer.do("GET", path, nil); response.Code != 403 || reads != before {
		t.Fatal("view-only source searched writable choices")
	}
	paths, err := SiteAllowedNextPaths(registry, "/admin")
	if err != nil || slices.Contains(paths, path) {
		t.Fatal("JSON endpoint became login destination", err)
	}
	get := client.do("GET", "/admin/accounts/collection/save/", nil)
	if get.Code != 200 || strings.Contains(get.Body.String(), `value="9223372036854775807"`) {
		t.Fatal("initial page was unbounded", get.Code)
	}
	reads = 0
	post := client.do("POST", "/admin/accounts/collection/save/", url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}, "target": {strconv.FormatInt(math.MaxInt64, 10)}})
	if post.Code != 302 || writes != 1 || reads != 2 {
		t.Fatal("off-page selection was not independently resolved twice", post.Code, writes, reads)
	}
}

func TestInlineChoiceSearchOriginRequiresCurrentCohortIdentityAndWritableRows(t *testing.T) {
	config, actor := reportInlineFixture(t)
	loads := 0
	original := config.Load
	config.Load = func(ctx context.Context, p auth.Principal, id int64) (InlineSnapshot[models.ServiceReport], error) {
		loads++
		return original(ctx, p, id)
	}
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	definition := inline.View()
	origin, err := definition.searchOrigin(t.Context(), actor, 7, 11, InlineAccess{View: true, Change: true})
	if err != nil || loads != 1 {
		t.Fatal("current child rejected", err)
	}
	parent, id, ok := origin.Parent()
	row, present := origin.RowIdentity()
	if !ok || parent != (ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"}) || id != 7 || !present || row != 11 {
		t.Fatal("verified origin lost cohort identity")
	}
	if _, err := definition.searchOrigin(t.Context(), actor, 7, 99, InlineAccess{View: true, Change: true}); err != ErrObjectNotFound {
		t.Fatal("foreign child acquired origin", err)
	}
	before := loads
	for _, test := range []struct {
		parent, row int64
		access      InlineAccess
	}{{7, 11, InlineAccess{View: true}}, {0, 11, InlineAccess{View: true, Change: true}}, {7, 0, InlineAccess{View: true}}} {
		if _, err := definition.searchOrigin(t.Context(), actor, test.parent, test.row, test.access); err == nil {
			t.Fatal("read-only/existing/new admission bypass")
		}
	}
	if loads != before {
		t.Fatal("pre-admission rejection loaded cohort")
	}
	if origin, err := definition.searchOrigin(t.Context(), actor, 7, 0, InlineAccess{View: true, Add: true}); err != nil {
		t.Fatal(err)
	} else if _, present := origin.RowIdentity(); present {
		t.Fatal("new row inherited an existing identity")
	}
	config.Set.ReadOnlyInitial = true
	readOnly, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.View().searchOrigin(t.Context(), actor, 7, 11, InlineAccess{View: true, Change: true}); err == nil {
		t.Fatal("configured readonly row searched targets")
	}
}

func TestInlineChoiceInitialExcludesOrderDeleteControlsAndKeepsServerSelection(t *testing.T) {
	for _, related := range []bool{false, true} {
		t.Run(fmt.Sprintf("related_%t", related), func(t *testing.T) {
			fixture, actor := reportInlineFixture(t)
			fixture.Set.ExtraForms = 0
			fixture.Set.CanOrder, fixture.Set.CanDelete = true, true
			var definition Inline
			if !related {
				inline, err := NewInline(fixture)
				if err != nil {
					t.Fatal(err)
				}
				definition = inline.View()
			} else {
				permissions := Permissions{View: "helpdesk.view_ticket", Add: "helpdesk.add_ticket", Change: "helpdesk.change_ticket", Delete: "helpdesk.delete_ticket"}
				actor = sitePrincipal(t, "actor", true, permissions.View, permissions.Change, permissions.Delete, "helpdesk.view_label")
				child := models.NewTicketWithID(11)
				child.CategoryID, child.Subject = 7, "Current"
				inline, err := NewInline(InlineConfig[models.Category, models.Ticket]{
					Project: fixture.Project, Parent: models.CategoryObjects, Child: models.TicketObjects, ParentWithID: models.NewCategoryWithID, ForeignKey: "category", Label: "Tickets", Set: fixture.Set, Permissions: permissions,
					Form: FormConfig{Definition: formmodel.Definition{Fields: []string{"subject", "labels"}}, RelatedChoices: []RelatedChoices{testRelationChoices(t, (models.TicketDescriptor{}).Metadata(), "helpdesk", "labels", "helpdesk.view_label", func(context.Context, auth.Principal) ([]forms.Choice, error) {
						return []forms.Choice{{Value: forms.Integer(1), Label: "Selected"}, {Value: forms.Integer(2), Label: "Presentation only"}}, nil
					})}},
					Load: func(context.Context, auth.Principal, int64) (InlineSnapshot[models.Ticket], error) {
						return InlineSnapshot[models.Ticket]{Current: []models.Ticket{child}, Related: func(models.Ticket, ir.ManyToManyField) ([]int64, bool) { return []int64{1}, true }}, nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				definition = inline.View()
			}
			bound, err := definition.bind(t.Context(), actor, 7, InlineAccess{View: true, Change: true, Delete: true}, nil)
			if err != nil || bound.set.Bound() || len(bound.set.Forms()) != 1 {
				t.Fatal("existing controlled inline failed GET", err)
			}
			row := bound.set.Forms()[0]
			order, deleteControl := false, false
			for _, field := range row.Fields() {
				order = order || field.Name() == "ORDER"
				deleteControl = deleteControl || field.Name() == "DELETE"
				if field.Name() == "labels" {
					selected, ok := row.Form().Initial().Integers("labels")
					if !ok || !slices.Equal(selected, []int64{1}) {
						t.Fatal("server relation initial lost", selected)
					}
					choices := field.Choices()
					if len(choices) != 1 || choices[0].InputValue() != "1" {
						t.Fatal("presentation search entered validation choices", choices)
					}
				}
			}
			if !order || !deleteControl {
				t.Fatal("control fields disappeared from row")
			}
			if related {
				if view := bound.choiceViews[0]["labels"]; len(view.Items) != 2 {
					t.Fatal("display page did not remain separate", view.Items)
				}
			}
		})
	}
}

func TestReadonlyRowChoicesPreserveCancellationAndDefiniteDenial(t *testing.T) {
	for _, mode := range []string{"canceled_before", "canceled_invalid_keys", "denied_target", "anonymous", "nil_context"} {
		t.Run(mode, func(t *testing.T) {
			work, cancel := context.WithCancel(t.Context())
			defer cancel()
			var ctx context.Context = work
			actor := mustPrincipalWithPermissions(t, "accounts.view")
			reads, keyCalls := 0, 0
			source := testTargetChoices(t, "target", "accounts.view", func(context.Context, auth.Principal) ([]forms.Choice, error) { reads++; return nil, nil })
			switch mode {
			case "canceled_before":
				cancel()
			case "denied_target":
				actor = mustPrincipalWithPermissions(t, "accounts.change")
			case "anonymous":
				actor = auth.Principal{}
			case "nil_context":
				ctx = nil
			}
			result, err := resolveRowChoices(ctx, actor, ChoiceOrigin{valid: true}, []RelatedChoices{source}, true, nil, func(string) ([]int64, bool, error) { keyCalls++; cancel(); return nil, false, nil })
			if reads != 0 {
				t.Fatal("rejected/invalid readonly lookup read targets")
			}
			if mode == "denied_target" {
				if err != nil || len(result) != 0 || keyCalls != 0 {
					t.Fatal("definite denied label did not use numeric fallback", err)
				}
				return
			}
			if err == nil || result != nil {
				t.Fatal("readonly lookup swallowed failure", err)
			}
			if strings.HasPrefix(mode, "canceled") && !errors.Is(err, context.Canceled) {
				t.Fatal("readonly lookup lost cancellation", err)
			}
		})
	}
}

func TestAdminSnapshotsPreserveSignedTargetKeysAndPositiveOwningIdentity(t *testing.T) {
	model := (models.TicketDescriptor{}).Metadata()
	fields := map[string]ir.Field{}
	many := map[string]ir.ManyToManyField{}
	for _, field := range model.Fields {
		fields[field.Name] = field
	}
	for _, field := range model.ManyToMany {
		many[field.Name] = field
	}
	for _, key := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
		object, err := NewObject(1, "Ticket", map[string]templates.Value{"id": templates.Integer(1), "category": templates.Integer(key), "labels": templates.List(templates.Integer(key))})
		if err != nil {
			t.Fatal(err)
		}
		if err := validateObject(object, fields, many); err != nil {
			t.Fatal("target key acquired owning object positivity constraint", key, err)
		}
	}
	for _, values := range []map[string]templates.Value{
		{"category": templates.String("0")}, {"category": templates.Null()},
		{"labels": templates.List(templates.String("-1"))}, {"labels": templates.List(templates.Null())},
	} {
		object, err := NewObject(1, "Ticket", values)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateObject(object, fields, many); err == nil {
			t.Fatal("target key accepted a noninteger/nonnullable value")
		}
	}
	for _, id := range []int64{0, -1} {
		object, err := NewObject(id, "Invalid route identity", map[string]templates.Value{"id": templates.Integer(id)})
		if err == nil && validateObject(object, fields, many) == nil {
			t.Fatal("target key support widened owning Admin identity", id)
		}
	}
}
