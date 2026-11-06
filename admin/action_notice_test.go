package admin

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestActionNoticeRejectsInvalidStartupDefinitions(t *testing.T) {
	for _, notice := range []ActionNotice{
		{Tag: "published"}, {Text: "{count} published."},
		{Tag: "unsafe tag", Text: "{count}"}, {Tag: strings.Repeat("a", MaximumModelBytes+1), Text: "{count}"},
		{Tag: "published", Text: "Count omitted"}, {Tag: "published", Text: "{count} {count}"},
		{Tag: "published", Text: "{count}\x00"}, {Tag: "published", Text: "{count}\n"},
		{Tag: "published", Text: "{count}\xff"}, {Tag: "published", Text: "{count}" + strings.Repeat("a", MaximumDisplayBytes)},
	} {
		config := validRegistryConfig(t)
		config.Actions[0].SuccessNotice = notice
		_, err := prepareRegistration(config, mustApps(t))
		var invalid *ConfigError
		if !errors.As(err, &invalid) || !strings.HasPrefix(invalid.Path, "model.actions[0].success_notice.") {
			t.Fatalf("invalid notice registered: %#v, %v", notice, err)
		}
	}
}

func TestActionNoticeUsesOwnedConfigurationAndConfirmedCount(t *testing.T) {
	matched, calls := 1, 0
	custom := ActionNotice{Tag: "published", Text: "<b>{count}</b> object(s) & published."}
	client, _, registry := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
		for _, name := range []string{"publish", "other"} {
			config.Actions = append(config.Actions, ActionConfig{Name: name, Label: name, Permission: "accounts.change", SuccessNotice: custom,
				Run: func(_ context.Context, _ auth.Principal, ids []int64) (ActionResult, error) {
					calls++
					return ActionResult{MatchedIDs: ids[:matched]}, nil
				},
			})
		}
		config.Actions = append(config.Actions, ActionConfig{Name: "generic", Label: "Generic", Permission: "accounts.change",
			Run: func(_ context.Context, _ auth.Principal, ids []int64) (ActionResult, error) {
				return ActionResult{MatchedIDs: ids}, nil
			},
		})
	})
	custom.Text = "forged"
	descriptor := registry.All()[0]
	descriptor.Actions[0].SuccessNotice.Text = "also forged"
	if registry.All()[0].Actions[0].SuccessNotice.Text != "<b>{count}</b> object(s) & published." {
		t.Fatal("action notice aliases mutable configuration")
	}
	client.login(t, "admin", "secret", "/admin/accounts/")
	page := client.do("GET", "/admin/accounts/", nil)
	token := siteCSRFToken(t, page.Body.String())
	for _, count := range []int{1, 0} {
		matched = count
		response := client.do("POST", "/admin/accounts/action/publish/", url.Values{"csrfmiddlewaretoken": {token}, "selected": {"1"}})
		location := response.Header().Get("Location")
		if response.Code != 302 || !siteSignedNoticeLocation(location, "/admin/accounts/", "action:publish", strconv.Itoa(count)) {
			t.Fatal("action notice lost identity or confirmed count", response.Code, location)
		}
		notice := client.do("GET", location, nil)
		if notice.Code != 200 || !strings.Contains(notice.Body.String(), `data-admin-message="published" data-affected="`+strconv.Itoa(count)+`"`) ||
			!strings.Contains(notice.Body.String(), "&lt;b&gt;"+strconv.Itoa(count)+"&lt;/b&gt; object(s) &amp; published.") || strings.Contains(notice.Body.String(), "<b>") {
			t.Fatal("action message was lost, unescaped, or changed", notice.Code, notice.Body.String())
		}
		for _, mutation := range []struct{ key, value string }{{"notice", "action:other"}, {"notice", "action:missing"}, {"notice", "action"}, {"count", "2"}, {"sig", "invalid"}} {
			parsed, err := url.Parse(location)
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			query.Set(mutation.key, mutation.value)
			parsed.RawQuery = query.Encode()
			denied := client.do("GET", parsed.String(), nil)
			if denied.Code != 400 || strings.Contains(denied.Body.String(), "data-admin-message") {
				t.Fatal("altered action notice accepted", mutation, denied.Code)
			}
		}
	}
	if calls != 2 {
		t.Fatal("reading a notice called an action", calls)
	}
	generic := client.do("POST", "/admin/accounts/action/generic/", url.Values{"csrfmiddlewaretoken": {token}, "selected": {"1"}})
	notice := client.do("GET", generic.Header().Get("Location"), nil)
	if generic.Code != 302 || notice.Code != 200 || !strings.Contains(notice.Body.String(), `data-admin-message="action" data-affected="1"`) ||
		!strings.Contains(notice.Body.String(), "1 object(s) changed by the action.") {
		t.Fatal("default action notice unavailable", generic.Code, notice.Code)
	}
}

func TestActionNoticeValidatesSignedCountAndModelScope(t *testing.T) {
	config := validRegistryConfig(t)
	model, err := prepareRegistration(config, mustApps(t))
	if err != nil {
		t.Fatal(err)
	}
	site := &Site{noticeKey: [32]byte{1}}
	for _, count := range []string{"0", "1", strconv.Itoa(MaximumSelectedIDs), "", "-1", "01", "+1", "1.0", strconv.Itoa(MaximumSelectedIDs + 1), "9999999999999999999999"} {
		location, err := url.Parse(site.signedNoticeLocation(model, "action:publish", count))
		if err != nil {
			t.Fatal(err)
		}
		valid := count == "0" || count == "1" || count == strconv.Itoa(MaximumSelectedIDs)
		if err := site.validateNotice(model, location.Query()); (err == nil) != valid {
			t.Fatal("signed count admission", count, err)
		}
		if valid {
			other := model
			other.appLabel += "other"
			if site.validateNotice(other, location.Query()) == nil {
				t.Fatal("signed action notice transferred to another model")
			}
		}
	}
}
