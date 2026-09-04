package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type fixedCSRFToken string

func (token fixedCSRFToken) Token(context.Context) (string, error) { return string(token), nil }

func (site *Site) render(request *web.Request, name string, values map[string]templates.Value) (web.Response, error) {
	token, err := site.auth.CSRFToken(request)
	if err != nil {
		return web.Response{}, err
	}
	values = cloneTemplateValues(values)
	values["index_path"] = templates.String(site.basePath + "/")
	values["login_path"] = templates.String(site.basePath + "/login/")
	values["logout_path"] = templates.String(site.basePath + "/logout/")
	contextValues, err := templates.NewContext(values)
	if err != nil {
		return web.Response{}, &ConfigError{Path: "site.context", Code: "invalid", Cause: err}
	}
	body, err := site.templates.Render(request.Context(), name, contextValues, templates.Capabilities{
		CSRF: fixedCSRFToken(token.Value()),
	})
	if err != nil {
		return web.Response{}, err
	}
	header := make(http.Header)
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Referrer-Policy", "same-origin")
	header.Set("X-Content-Type-Options", "nosniff")
	setSiteFramingHeaders(header)
	response, err := web.NewResponse(http.StatusOK, header, body)
	if err != nil {
		return web.Response{}, err
	}
	return token.Apply(response)
}

func (site *Site) indexContext(ctx context.Context, principal auth.Principal) (map[string]templates.Value, error) {
	models := make([]templates.Value, 0, len(site.registry.models))
	for _, model := range site.registry.models {
		allowed, err := site.auth.Authorized(ctx, principal, model.permissions.View)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		item, err := templateObject(map[string]templates.Value{
			"name": templates.String(model.model.GoName),
			"path": templates.String(site.modelPath(model)),
		})
		if err != nil {
			return nil, err
		}
		models = append(models, item)
	}
	return map[string]templates.Value{
		"principal_id": templates.String(principal.ID()),
		"models":       templates.List(models...),
	}, nil
}

func (site *Site) loginContext(username, next string, invalid bool) (map[string]templates.Value, error) {
	return map[string]templates.Value{
		"username": templates.String(safeDisplayText(username)),
		"next":     templates.String(next),
		"invalid":  templates.Bool(invalid),
	}, nil
}

func (site *Site) listContext(
	ctx context.Context,
	model registeredModel,
	principal auth.Principal,
	page registeredPage,
	pageNumber int,
	search string,
	query url.Values,
	invalidPage bool,
) (map[string]templates.Value, error) {
	canAdd, err := site.auth.Authorized(ctx, principal, model.permissions.Add)
	if err != nil {
		return nil, err
	}
	canChange, err := site.auth.Authorized(ctx, principal, model.permissions.Change)
	if err != nil {
		return nil, err
	}
	canDelete, err := site.auth.Authorized(ctx, principal, model.permissions.Delete)
	if err != nil {
		return nil, err
	}
	fields := make([]templates.Value, len(model.listFields))
	for index, name := range model.listFields {
		fields[index] = templates.String(name)
	}
	objects := make([]templates.Value, len(page.objects))
	for index, object := range page.objects {
		cells := make([]templates.Value, len(model.listFields))
		for fieldIndex, field := range model.listFields {
			value, ok := object.Value(field)
			if !ok {
				return nil, &ConfigError{Path: "site.list." + field, Code: "missing_snapshot_value"}
			}
			cells[fieldIndex] = value
		}
		item, err := templateObject(map[string]templates.Value{
			"id":          templates.Integer(object.id),
			"label":       templates.String(object.label),
			"cells":       templates.List(cells...),
			"change_url":  templates.String(site.modelPath(model) + "change/?id=" + strconv.FormatInt(object.id, 10)),
			"delete_url":  templates.String(site.modelPath(model) + "delete/?id=" + strconv.FormatInt(object.id, 10)),
			"history_url": templates.String(site.modelPath(model) + "history/?id=" + strconv.FormatInt(object.id, 10)),
			"can_change":  templates.Bool(canChange),
			"can_delete":  templates.Bool(canDelete),
		})
		if err != nil {
			return nil, err
		}
		objects[index] = item
	}
	actions := make([]templates.Value, 0, len(model.actions))
	for _, action := range model.actions {
		allowed, err := site.auth.Authorized(ctx, principal, action.permission)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		item, err := templateObject(map[string]templates.Value{
			"name":  templates.String(action.name),
			"label": templates.String(action.label),
			"path":  templates.String(site.modelPath(model) + "action/" + action.name + "/"),
		})
		if err != nil {
			return nil, err
		}
		actions = append(actions, item)
	}
	pageCount := listPageCount(page.total, page.limit)
	previousURL, nextURL := "", ""
	if pageNumber > 1 {
		previousURL = listPageURL(site.modelPath(model), search, pageNumber-1)
	}
	if pageNumber < int(^uint(0)>>1) && int64(page.offset)+int64(len(page.objects)) < page.total {
		nextURL = listPageURL(site.modelPath(model), search, pageNumber+1)
	}
	notice := noticeText(query.Get("notice"), query.Get("count"))
	affected := int64(0)
	if query.Get("notice") == "published" {
		affected, _ = strconv.ParseInt(query.Get("count"), 10, 64)
	}
	return map[string]templates.Value{
		"model_name":   templates.String(model.model.GoName),
		"list_path":    templates.String(site.modelPath(model)),
		"add_path":     templates.String(site.modelPath(model) + "add/"),
		"can_add":      templates.Bool(canAdd),
		"search":       templates.String(safeDisplayText(search)),
		"fields":       templates.List(fields...),
		"objects":      templates.List(objects...),
		"actions":      templates.List(actions...),
		"has_actions":  templates.Bool(len(actions) > 0),
		"page":         templates.Integer(int64(pageNumber)),
		"page_count":   templates.Integer(pageCount),
		"total":        templates.Integer(page.total),
		"previous_url": templates.String(previousURL),
		"has_previous": templates.Bool(previousURL != ""),
		"next_url":     templates.String(nextURL),
		"has_next":     templates.Bool(nextURL != ""),
		"notice":       templates.String(notice),
		"notice_tag":   templates.String(query.Get("notice")),
		"has_notice":   templates.Bool(notice != ""),
		"affected":     templates.Integer(affected),
		"invalid_page": templates.Bool(invalidPage),
	}, nil
}

func listPageCount(total int64, limit int) int64 {
	if total <= 0 || limit < 1 {
		return 1
	}
	count := total / int64(limit)
	if total%int64(limit) != 0 {
		count++
	}
	return count
}

func (site *Site) validateNotice(model registeredModel, values url.Values) error {
	notice := values.Get("notice")
	count := values.Get("count")
	signature := values.Get("sig")
	switch notice {
	case "":
		if count != "" || signature != "" {
			return &ConfigError{Path: "request.values.count", Code: "unexpected"}
		}
		return nil
	case "added", "changed", "deleted":
		if count != "" {
			return &ConfigError{Path: "request.values.count", Code: "unexpected"}
		}
	case "published":
		parsed, err := strconv.Atoi(count)
		if err != nil || parsed < 0 || strconv.Itoa(parsed) != count || parsed > MaximumSelectedIDs {
			return &ConfigError{Path: "request.values.count", Code: "invalid"}
		}
	default:
		return &ConfigError{Path: "request.values.notice", Code: "invalid"}
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil || len(decoded) != sha256.Size || base64.RawURLEncoding.EncodeToString(decoded) != signature ||
		subtle.ConstantTimeCompare(decoded, site.noticeSignature(model, notice, count)) != 1 {
		return &ConfigError{Path: "request.values.sig", Code: "invalid"}
	}
	return nil
}

func (site *Site) signedNoticeLocation(model registeredModel, notice, count string) string {
	query := make(url.Values)
	query.Set("notice", notice)
	if count != "" {
		query.Set("count", count)
	}
	query.Set("sig", base64.RawURLEncoding.EncodeToString(site.noticeSignature(model, notice, count)))
	return site.modelPath(model) + "?" + query.Encode()
}

func (site *Site) noticeSignature(model registeredModel, notice, count string) []byte {
	mac := hmac.New(sha256.New, site.noticeKey[:])
	_, _ = mac.Write([]byte(model.appLabel))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(model.model.Name))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(notice))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(count))
	return mac.Sum(nil)
}

func noticeText(notice, count string) string {
	switch notice {
	case "added":
		return "Object added."
	case "changed":
		return "Object changed."
	case "deleted":
		return "Object deleted."
	case "published":
		return count + " object(s) published."
	default:
		return ""
	}
}

func listPageURL(base, search string, page int) string {
	query := make(url.Values)
	if search != "" {
		query.Set("q", search)
	}
	if page > 1 {
		query.Set("p", strconv.Itoa(page))
	}
	if len(query) == 0 {
		return base
	}
	return base + "?" + query.Encode()
}

func (site *Site) formContext(
	model registeredModel,
	title, action, submit string,
	form forms.Form,
	submitted url.Values,
) (map[string]templates.Value, error) {
	fields := model.form.Fields()
	fieldValues := make([]templates.Value, len(fields))
	for index, field := range fields {
		value, checked := renderedFieldValue(field, form, submitted)
		errors, err := violationValues(form.Errors().ByField(validation.Field(field.Name())))
		if err != nil {
			return nil, err
		}
		item, err := templateObject(map[string]templates.Value{
			"name":       templates.String(field.Name()),
			"label":      templates.String(field.Label()),
			"char":       templates.Bool(field.Kind() == forms.FieldChar),
			"boolean":    templates.Bool(field.Kind() == forms.FieldBoolean),
			"required":   templates.Bool(field.Required()),
			"max_length": templates.Integer(int64(field.MaxLength())),
			"value":      templates.String(value),
			"checked":    templates.Bool(checked),
			"errors":     templates.List(errors...),
		})
		if err != nil {
			return nil, err
		}
		fieldValues[index] = item
	}
	nonField, err := violationValues(form.Errors().ByField(validation.NonField))
	if err != nil {
		return nil, err
	}
	return map[string]templates.Value{
		"title":            templates.String(title),
		"action":           templates.String(action),
		"submit_label":     templates.String(submit),
		"list_path":        templates.String(site.modelPath(model)),
		"fields":           templates.List(fieldValues...),
		"non_field_errors": templates.List(nonField...),
		"bound":            templates.Bool(form.Bound()),
	}, nil
}

func renderedFieldValue(field forms.Field, form forms.Form, submitted url.Values) (string, bool) {
	if form.Bound() {
		if raw, ok := submitted[field.Name()]; ok {
			first := ""
			if len(raw) > 0 {
				first = raw[0]
			}
			if field.Kind() == forms.FieldBoolean {
				switch strings.ToLower(strings.TrimSpace(first)) {
				case "1", "true", "on", "yes":
					return "", true
				default:
					return "", false
				}
			}
			return safeDisplayText(first), false
		}
		if field.Kind() == forms.FieldBoolean {
			return "", false
		}
	}
	initial, ok := form.Initial().Get(field.Name())
	if !ok || initial.IsNull() {
		return "", false
	}
	if field.Kind() == forms.FieldBoolean {
		checked, _ := initial.AsBoolean()
		return "", checked
	}
	value, _ := initial.AsString()
	return safeDisplayText(value), false
}

func violationValues(errors validation.Errors) ([]templates.Value, error) {
	violations := errors.All()
	result := make([]templates.Value, len(violations))
	for index, violation := range violations {
		message := string(violation.Code())
		params := violation.Params()
		if len(params) > 0 {
			parts := make([]string, len(params))
			for paramIndex, param := range params {
				parts[paramIndex] = param.Key() + "=" + param.Value()
			}
			message += " (" + strings.Join(parts, ", ") + ")"
		}
		item, err := templates.Object(map[string]templates.Value{
			"code":    templates.String(string(violation.Code())),
			"message": templates.String(message),
		})
		if err != nil {
			return nil, &ConfigError{Path: "site.context.errors", Code: "invalid", Cause: err}
		}
		result[index] = item
	}
	return result, nil
}

func (site *Site) deleteContext(model registeredModel, object Object) (map[string]templates.Value, error) {
	return map[string]templates.Value{
		"model_name": templates.String(model.model.GoName),
		"label":      templates.String(object.label),
		"action":     templates.String(site.modelPath(model) + "delete/?id=" + strconv.FormatInt(object.id, 10)),
		"list_path":  templates.String(site.modelPath(model)),
	}, nil
}

func (site *Site) historyContext(model registeredModel, id int64, entries []AuditEntry) (map[string]templates.Value, error) {
	items := make([]templates.Value, len(entries))
	for index, entry := range entries {
		changed := make([]templates.Value, len(entry.ChangedFields))
		for fieldIndex, field := range entry.ChangedFields {
			changed[fieldIndex] = templates.String(field)
		}
		item, err := templateObject(map[string]templates.Value{
			"sequence": templates.Integer(int64(entry.Sequence)),
			"actor":    templates.String(entry.ActorID),
			"action":   templates.String(string(entry.Action)),
			"label":    templates.String(entry.DisplayLabel),
			"changed":  templates.List(changed...),
		})
		if err != nil {
			return nil, err
		}
		items[index] = item
	}
	return map[string]templates.Value{
		"model_name": templates.String(model.model.GoName),
		"object_id":  templates.Integer(id),
		"entries":    templates.List(items...),
		"list_path":  templates.String(site.modelPath(model)),
	}, nil
}

func templateObject(values map[string]templates.Value) (templates.Value, error) {
	value, err := templates.Object(values)
	if err != nil {
		return templates.Value{}, &ConfigError{Path: "site.context.object", Code: "invalid", Cause: err}
	}
	return value, nil
}

func cloneTemplateValues(values map[string]templates.Value) map[string]templates.Value {
	clone := make(map[string]templates.Value, len(values)+3)
	for name, value := range values {
		clone[name] = value
	}
	return clone
}

func safeDisplayText(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	return strings.Map(func(character rune) rune {
		if character == 0 || unicode.IsControl(character) && character != '\t' && character != '\n' && character != '\r' {
			return '\uFFFD'
		}
		return character
	}, value)
}
