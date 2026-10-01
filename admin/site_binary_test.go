package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestAdminBinaryWidgetAndServerOwnedModelValues(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "godj_conformance", Models: []schema.Model{{Name: "article", GoName: "Article", Fields: []schema.Field{
		schema.CharField("title", "Title", 40), schema.BinaryField("payload", "Payload", schema.Editable(true), schema.MaxLength(4), schema.Blank()),
		schema.BinaryField("fingerprint", "Fingerprint"), schema.CharField("internal_note", "InternalNote", 30, schema.Editable(false)),
		schema.ImageField("cover", "Cover", schema.Editable(false), schema.Choices(schema.Choice("current.png", "Current"))),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := definition.Models[0]
	type packet struct {
		id                   int64
		title                string
		payload, fingerprint binaryvalue.Value
		note, cover          string
	}
	current := packet{1, "Bytes", binaryvalue.Value{Data: "\x00\xff"}, binaryvalue.Value{Data: "private"}, "server", "legacy.png"}
	reader := func(value packet, field ir.Field) (query.Value, bool) {
		switch field.Name {
		case "id":
			return query.Integer(value.id), true
		case "title":
			return query.String(value.title), true
		case "payload":
			return query.Binary(value.payload), true
		case "fingerprint":
			return query.Binary(value.fingerprint), true
		case "internal_note":
			return query.String(value.note), true
		case "cover":
			return query.String(value.cover), true
		}
		return query.Value{}, false
	}
	projector, err := NewModelProjector(model, reader, "id", "title", "payload", "fingerprint", "internal_note", "cover")
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	apply := func(bound formmodel.BoundForm) (packet, error) {
		input, err := bound.Input()
		if err != nil {
			return packet{}, err
		}
		if _, exists := input.Get("fingerprint"); exists {
			return packet{}, errors.New("non-editable binary entered persistent input")
		}
		if _, exists := input.Get("internal_note"); exists {
			return packet{}, errors.New("non-editable text entered persistent input")
		}
		if _, exists := input.Get("cover"); exists {
			return packet{}, errors.New("non-editable image entered persistent input")
		}
		title, ok := input.String("title")
		if !ok {
			return packet{}, errors.New("title missing")
		}
		value, ok := input.Get("payload")
		if !ok {
			return packet{}, errors.New("binary missing")
		}
		payload, ok := value.AsBinary()
		if !ok {
			return packet{}, errors.New("binary lost type")
		}
		current.title, current.payload = title, payload
		// The callback derives a server field, and reports it for auditing.
		current.fingerprint = binaryvalue.Value{Data: "derived"}
		writes++
		return current, nil
	}
	config := ModelConfig[packet]{AppLabel: definition.AppLabel, Slug: "articles", Model: model, ListFields: []string{"id", "title", "payload", "fingerprint"}, Permissions: validRegistryConfig(t).Permissions,
		List: func(_ context.Context, _ auth.Principal, r ListRequest) (Page[packet], error) {
			return Page[packet]{Items: []packet{current}, Total: 1, Offset: r.Offset, Limit: r.Limit}, nil
		},
		Get: func(_ context.Context, _ auth.Principal, id int64) (packet, bool, error) {
			return current, id == current.id, nil
		},
		Snapshot: func(value packet) (Object, error) { return projector.Project(value, value.id, value.title) },
		Initial: func(value packet) (map[string]forms.Value, error) {
			return map[string]forms.Value{"title": forms.String(value.title), "payload": forms.Binary(value.payload), "fingerprint": forms.Binary(value.fingerprint), "internal_note": forms.String(value.note), "cover": forms.String(value.cover)}, nil
		},
		Create: func(_ context.Context, _ auth.Principal, b formmodel.BoundForm, _ InlineSubmission) (packet, error) {
			return apply(b)
		},
		Update: func(_ context.Context, _ auth.Principal, _ Mutation, b formmodel.BoundForm, _ InlineSubmission) (packet, []string, error) {
			value, err := apply(b)
			return value, []string{"payload", "fingerprint"}, err
		},
		Delete: func(context.Context, auth.Principal, Mutation) (packet, error) { return current, nil },
		ReadOnlyFields: []ReadOnlyField[packet]{
			{Name: "fingerprint", Label: "Fingerprint", Value: func(value packet) (string, error) { return value.fingerprint.Base64(), nil }},
			{Name: "cover", Label: "Cover", Value: func(value packet) (string, error) { return value.cover, nil }},
		},
	}
	client, _, _ := uploadTestSite(t, config, uploadTestPolicy(t), auth.PrincipalAuthorizer{})
	client.login(t, "admin", "secret", "/admin/")
	const target = "/admin/articles/change/?id=1"
	page := client.do(http.MethodGet, target, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), ` name="payload"`) || !strings.Contains(page.Body.String(), `value="AP8="`) || !strings.Contains(page.Body.String(), `maxlength="8"`) || strings.Contains(page.Body.String(), ` name="fingerprint"`) || strings.Contains(page.Body.String(), ` name="internal_note"`) || !strings.Contains(page.Body.String(), current.fingerprint.Base64()) {
		t.Fatal("binary initial/widget/server field display", page.Code, page.Body.String())
	}
	if strings.Contains(page.Body.String(), ` name="cover"`) || !strings.Contains(page.Body.String(), "legacy.png") {
		t.Fatal("stored non-editable image was treated as a choice input")
	}
	data := url.Values{"title": {"Bytes"}, "payload": {"AP8="}, "csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}}
	for _, name := range []string{"fingerprint", "internal_note", "cover"} {
		data.Set(name, "attacker")
		response := client.do(http.MethodPost, target, data)
		data.Del(name)
		if response.Code != http.StatusBadRequest || writes != 0 {
			t.Fatal("forged server field reached persistence", name, response.Code)
		}
	}
	for _, raw := range []string{"AP8", "YWJjZGU=", "<script>"} {
		data.Set("payload", raw)
		response := client.do(http.MethodPost, target, data)
		if response.Code != http.StatusOK || writes != 0 || !strings.Contains(response.Body.String(), `data-error-field="payload"`) || strings.Contains(response.Body.String(), `value="<script>"`) {
			t.Fatal("binary error rendering or length boundary", response.Code)
		}
	}
	for _, test := range []struct{ input, data string }{{"  AP9hgA==  ", "\x00\xffa\x80"}, {"", ""}, {"Zh==", "f"}} {
		data.Set("payload", test.input)
		before := writes
		response := client.do(http.MethodPost, target, data)
		if response.Code != http.StatusFound || writes != before+1 || current.payload.Data != test.data || current.note != "server" || current.cover != "legacy.png" {
			t.Fatal("binary form persistence or derived audit field", response.Code, response.Body.String())
		}
	}
	// Ordinary storage may contain a value longer than today's input policy.
	current.payload = binaryvalue.Value{Data: "old bytes"}
	page = client.do(http.MethodGet, target, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), current.payload.Base64()) {
		t.Fatal("Admin output applied input byte length")
	}
}
