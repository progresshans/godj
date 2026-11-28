package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
)

func TestAdminReadOnlyFieldsDisplayOwnedAuthorizedSnapshots(t *testing.T) {
	for _, actor := range []string{"admin", "viewer"} {
		t.Run(actor, func(t *testing.T) {
			reads := 0
			fields := []ReadOnlyField[managementFormRow]{{Name: "status", Label: "<Status>", Value: func(row managementFormRow) (string, error) {
				reads++
				return row.username + " <script>unsafe</script>", nil
			}}}
			client, state, registry := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) { config.ReadOnlyFields = fields })
			fields[0] = ReadOnlyField[managementFormRow]{Name: "forged"}
			descriptors := registry.All()
			if len(descriptors[0].ReadOnlyFields) != 1 || descriptors[0].ReadOnlyFields[0].Name != "status" {
				t.Fatal("readonly descriptor missing")
			}
			descriptors[0].ReadOnlyFields[0].Label = "forged"
			if registry.All()[0].ReadOnlyFields[0].Label != "<Status>" {
				t.Fatal("readonly descriptor aliases registration")
			}
			client.login(t, actor, "secret", "/admin/accounts/")
			path := "/admin/accounts/change/?id=1"
			response := client.do("GET", path, nil)
			body := response.Body.String()
			if response.Code != 200 || !strings.Contains(body, "&lt;Status&gt;") || !strings.Contains(body, `data-field-name="status">Original &lt;script&gt;unsafe&lt;/script&gt;`) || strings.Contains(body, "<script>") || strings.Contains(body, ` name="status"`) || reads != 1 || state.reads != 1 || state.writes != 0 {
				t.Fatal("readonly display escaped/owned/single-read contract failed", response.Code, reads, state.reads)
			}
			if actor == "admin" {
				values := url.Values{"username": {""}, "active": {"on"}, "expected_revision": {"1"}, "csrfmiddlewaretoken": {siteCSRFToken(t, body)}}
				rejected := client.do("POST", path, values)
				if rejected.Code != 200 || !strings.Contains(rejected.Body.String(), `data-field-name="status">Original`) || !strings.Contains(rejected.Body.String(), `data-error-code="required"`) || state.writes != 0 {
					t.Fatal("validation redraw lost readonly snapshot")
				}
				values.Set("username", "forged edit")
				values.Set("status", "forged readonly state")
				before := state.reads
				if forged := client.do("POST", path, values); forged.Code != 400 || state.reads != before || state.writes != 0 {
					t.Fatal("readonly field accepted as input or read storage before rejection")
				}
				before = reads
				if created := client.do("GET", "/admin/accounts/add/", nil); created.Code != 200 || strings.Contains(created.Body.String(), `data-field-name="status"`) || reads != before {
					t.Fatal("creation evaluated an existing-object display field")
				}
			} else {
				if strings.Contains(body, ` name="username"`) {
					t.Fatal("view-only actor received writable field")
				}
				before := state.reads
				if denied := client.do("POST", path, url.Values{"username": {"forbidden"}, "expected_revision": {"1"}, "csrfmiddlewaretoken": {siteCSRFToken(t, body)}}); denied.Code != 403 || state.reads != before || state.writes != 0 {
					t.Fatal("view-only field bypassed mutation authority")
				}
			}
			state.mu.Lock()
			state.row.username = "Current"
			state.mu.Unlock()
			if current := client.do("GET", path, nil); current.Code != 200 || !strings.Contains(current.Body.String(), `data-field-name="status">Current`) {
				t.Fatal("readonly display retained an old object")
			}
		})
	}
}

func TestAdminReadOnlyFieldsValidateDefinitionsBeforeIO(t *testing.T) {
	valid := ReadOnlyField[registryArticle]{Name: "status", Label: "Status", Value: func(registryArticle) (string, error) { t.Fatal("startup evaluated reader"); return "", nil }}
	for _, mode := range []string{"nil_reader", "empty_label", "bad_label", "long_label", "control_label", "invalid_name", "writable", "csrf", "condition", "duplicate", "limit", "list_only"} {
		t.Run(mode, func(t *testing.T) {
			config := validRegistryConfig(t)
			field := valid
			switch mode {
			case "nil_reader":
				field.Value = nil
			case "empty_label":
				field.Label = "  "
			case "bad_label":
				field.Label = "\xff"
			case "long_label":
				field.Label = strings.Repeat("x", MaximumDisplayBytes+1)
			case "control_label":
				field.Label = "private\x00label"
			case "invalid_name":
				field.Name = "bad-name"
			case "writable":
				field.Name = "title"
			case "csrf":
				field.Name = "csrfmiddlewaretoken"
			case "condition":
				field.Name = "expected_revision"
			case "list_only":
				config.ReadOnly = true
			}
			config.ReadOnlyFields = []ReadOnlyField[registryArticle]{field}
			if mode == "duplicate" {
				config.ReadOnlyFields = append(config.ReadOnlyFields, valid)
			}
			if mode == "limit" {
				config.ReadOnlyFields = make([]ReadOnlyField[registryArticle], MaximumReadOnlyFields+1)
			}
			if err := RegisterModel(NewBuilder(mustApps(t)), config); err == nil {
				t.Fatal("invalid display definition accepted", mode)
			}
		})
	}
	creation := forms.Spec{}
	field, err := forms.CharField("creation_only")
	if err != nil {
		t.Fatal(err)
	}
	creation, err = forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	valid.Name = "creation_only"
	if _, _, err := prepareReadOnlyFields([]ReadOnlyField[registryArticle]{valid}, forms.Spec{}, creation); err == nil {
		t.Fatal("read-only field conflicts with creation input")
	}
}

func TestAdminReadOnlyFieldsPreserveCancellationAndPrivateFailures(t *testing.T) {
	secret := errors.New("private read-only getter detail")
	for _, mode := range []string{"canceled", "late_cancel", "getter_error", "bad_utf8", "too_long", "control"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			field := ReadOnlyField[managementFormRow]{Name: "status", Label: "Status", Value: func(managementFormRow) (string, error) {
				calls++
				switch mode {
				case "late_cancel":
					cancel()
				case "getter_error":
					return "private output", secret
				case "bad_utf8":
					return "\xff", nil
				case "too_long":
					return strings.Repeat("x", MaximumDisplayBytes+1), nil
				case "control":
					return "\x00", nil
				}
				return "valid", nil
			}}
			_, read, err := prepareReadOnlyFields([]ReadOnlyField[managementFormRow]{field}, forms.Spec{}, forms.Spec{})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "canceled" {
				cancel()
			}
			value, err := read(ctx, managementFormRow{})
			if err == nil {
				t.Fatal("failed display published")
			}
			if _, published := value.Items(); published {
				t.Fatal("partial display published")
			}
			if mode == "canceled" && calls != 0 {
				t.Fatal("canceled read ran getter")
			}
			if (mode == "canceled" || mode == "late_cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost")
			}
			if mode == "getter_error" && !errors.Is(err, secret) {
				t.Fatal("private cause lost")
			}
			if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), "private") {
				t.Fatal("getter diagnostics disclosed private data")
			}
		})
	}
	client, state, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
		config.ReadOnlyFields = []ReadOnlyField[managementFormRow]{{Name: "status", Label: "Status", Value: func(managementFormRow) (string, error) { return "private output", secret }}}
	})
	client.login(t, "admin", "secret", "/admin/accounts/")
	response := client.do("GET", "/admin/accounts/change/?id=1", nil)
	if response.Code != 500 || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "Original") || state.writes != 0 {
		t.Fatal("failed display leaked partial HTTP output")
	}
}
