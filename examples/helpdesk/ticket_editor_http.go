package helpdesk

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func ticketEditorPageNumber(request *web.Request) (int, error) {
	httpRequest := request.HTTP()
	if len(httpRequest.URL.RawQuery) > 64 {
		return 0, errors.New("helpdesk: editor query exceeds limit")
	}
	values, err := url.ParseQuery(httpRequest.URL.RawQuery)
	if err != nil {
		return 0, err
	}
	for name, values := range values {
		if name != "p" || len(values) != 1 {
			return 0, errors.New("helpdesk: invalid editor query")
		}
	}
	if len(values) == 0 {
		return 1, nil
	}
	raw := values.Get("p")
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 || page > admin.MaximumListOffset/ticketEditorPageSize || strconv.Itoa(page) != raw {
		return 0, errors.New("helpdesk: invalid editor page")
	}
	return page, nil
}

func ticketEditorData(request *web.Request) (url.Values, error) {
	r := request.HTTP()
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" || r.Body == nil {
		return nil, errors.New("helpdesk: invalid editor content type")
	}
	if r.ContentLength > admin.MaximumFormBodyBytes {
		return nil, errors.New("helpdesk: editor body exceeds limit")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, admin.MaximumFormBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > admin.MaximumFormBodyBytes {
		return nil, errors.New("helpdesk: editor body exceeds limit")
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}
	count := 0
	for name, entries := range values {
		allowed := name == "csrfmiddlewaretoken" || name == "tickets-TOTAL_FORMS" || name == "tickets-INITIAL_FORMS" || name == "tickets-MIN_NUM_FORMS" || name == "tickets-MAX_NUM_FORMS"
		if !allowed {
			parts := strings.Split(name, "-")
			if len(parts) != 3 || parts[0] != "tickets" {
				return nil, errors.New("helpdesk: unknown editor input")
			}
			index, err := strconv.Atoi(parts[1])
			if err != nil || index < 0 || index >= ticketEditorMaxForms || strconv.Itoa(index) != parts[1] {
				return nil, errors.New("helpdesk: invalid editor row")
			}
			switch parts[2] {
			case "id", "subject", "closed", "external_reference", "labels", "DELETE":
				allowed = true
			}
		}
		if !allowed {
			return nil, errors.New("helpdesk: unknown editor field")
		}
		count += len(entries)
		if count > admin.MaximumInputValues {
			return nil, errors.New("helpdesk: editor value count exceeds limit")
		}
		for _, value := range entries {
			if len(value) > admin.MaximumInputBytes {
				return nil, errors.New("helpdesk: editor value exceeds limit")
			}
		}
	}
	return values, nil
}

func ticketEditorHeaders(contentType string) http.Header {
	h := make(http.Header)
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
	return h
}
func ticketEditorText(status int) (web.Response, error) {
	return web.NewResponse(status, ticketEditorHeaders("text/plain; charset=utf-8"), []byte(http.StatusText(status)+"\n"))
}

type ticketEditorCSRF string

func (token ticketEditorCSRF) Token(context.Context) (string, error) { return string(token), nil }

func ticketEditorDisplay(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, "\uFFFD"))
}

func ticketEditorFieldText(form forms.Form, name string) string {
	if form.Bound() {
		raw, _ := form.Submitted().Get(name)
		return ticketEditorDisplay(strings.Join(raw, " "))
	}
	value, _ := form.Initial().Get(name)
	if text, ok := value.AsString(); ok {
		return ticketEditorDisplay(text)
	}
	if key, ok := value.AsUUID(); ok {
		return key.String()
	}
	return ""
}
func ticketEditorChecked(form forms.Form, name string) bool {
	if form.Bound() {
		value, _ := form.Cleaned().Boolean(name)
		return value
	}
	value, _ := form.Initial().Boolean(name)
	return value
}

func ticketEditorErrors(diagnostics validation.Errors) (templates.Value, error) {
	values := make([]templates.Value, 0, diagnostics.Len())
	for _, failure := range diagnostics.All() {
		message := "Check this value."
		switch string(failure.Code()) {
		case "required":
			message = "Enter a value."
		case "max_length":
			message = "This value is too long."
		case "invalid_choice":
			message = "Choose a label from this category."
		case "multiple":
			message = "Submit only one value for this field."
		case "unique":
			message = "This external reference is already in use."
		case "protected":
			message = "A ticket with a service report cannot be deleted."
		case "invalid_identity", "missing_management_form", "invalid_count":
			message = "The selected tickets changed. Reload the page and review your changes."
		case "too_many_forms":
			message = "Submit no more than 40 rows at once."
		}
		value, err := templates.Object(map[string]templates.Value{"field": templates.String(string(failure.Field())), "code": templates.String(string(failure.Code())), "message": templates.String(message)})
		if err != nil {
			return templates.Value{}, err
		}
		values = append(values, value)
	}
	return templates.List(values...), nil
}

func (editor *TicketEditor) render(request *web.Request, page ticketEditorPage) (web.Response, error) {
	set := page.set.FormSet()
	rows := make([]templates.Value, 0, set.TotalForms())
	for _, row := range set.Forms() {
		identity := ""
		if value, present := page.set.Identity(row.Index()); present {
			key, _ := value.AsInteger()
			identity = strconv.FormatInt(key, 10)
		}
		selected := make(map[int64]bool)
		if row.Form().Bound() {
			raw, _ := row.Form().Submitted().Get("labels")
			for _, text := range raw {
				if key, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
					selected[key] = true
				}
			}
		} else {
			keys, _ := row.Form().Initial().Integers("labels")
			for _, key := range keys {
				selected[key] = true
			}
		}
		options := make([]templates.Value, 0, len(page.choices))
		for _, choice := range page.choices {
			key, _ := choice.Value.AsInteger()
			option, err := templates.Object(map[string]templates.Value{"value": templates.Integer(key), "label": templates.String(ticketEditorDisplay(choice.Label)), "selected": templates.Bool(selected[key])})
			if err != nil {
				return web.Response{}, err
			}
			options = append(options, option)
		}
		diagnostics, err := ticketEditorErrors(row.Form().Errors())
		if err != nil {
			return web.Response{}, err
		}
		value, err := templates.Object(map[string]templates.Value{
			"prefix": templates.String(row.Prefix()), "number": templates.Integer(int64(row.Index() + 1)), "identity": templates.String(identity),
			"subject": templates.String(ticketEditorFieldText(row.Form(), "subject")), "reference": templates.String(ticketEditorFieldText(row.Form(), "external_reference")),
			"closed": templates.Bool(ticketEditorChecked(row.Form(), "closed")), "deleted": templates.Bool(row.DeletionRequested()), "options": templates.List(options...), "errors": diagnostics,
		})
		if err != nil {
			return web.Response{}, err
		}
		rows = append(rows, value)
	}
	nonForm, err := ticketEditorErrors(validation.Join(set.NonFormErrors(), set.Management().Errors()))
	if err != nil {
		return web.Response{}, err
	}
	previous, next := "", ""
	if page.page > 1 {
		previous = TicketEditorPath + "?p=" + strconv.Itoa(page.page-1)
	}
	if page.next {
		next = TicketEditorPath + "?p=" + strconv.Itoa(page.page+1)
	}
	values, err := templates.NewContext(map[string]templates.Value{
		"rows": templates.List(rows...), "total": templates.Integer(int64(set.TotalForms())), "initial": templates.Integer(int64(set.InitialForms())), "errors": nonForm,
		"action": templates.String(TicketEditorPath + "?p=" + strconv.Itoa(page.page)), "can_delete": templates.Bool(page.permissions.delete), "can_add": templates.Bool(page.permissions.add),
		"previous": templates.String(previous), "next": templates.String(next), "page": templates.Integer(int64(page.page)),
	})
	if err != nil {
		return web.Response{}, err
	}
	token, err := editor.auth.CSRFToken(request)
	if err != nil {
		return web.Response{}, err
	}
	body, err := editor.engine.Render(request.Context(), "tickets.html", values, templates.Capabilities{CSRF: ticketEditorCSRF(token.Value())})
	if err != nil {
		return web.Response{}, err
	}
	response, err := web.NewResponse(http.StatusOK, ticketEditorHeaders("text/html; charset=utf-8"), body)
	if err != nil {
		return web.Response{}, err
	}
	return token.Apply(response)
}
