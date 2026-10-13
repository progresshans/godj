package helpdesk

import (
	"errors"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type LabelUsage struct {
	app    *Application
	auth   *sessionauth.Runtime
	engine *templates.Engine
}

func (a *Application) LabelUsage(authentication *sessionauth.Runtime) (*LabelUsage, error) {
	if a == nil || nilFormReader(a.backend) || authentication == nil {
		return nil, errors.New("helpdesk: label usage requires application and authentication")
	}
	if !authentication.CookiesApplyTo(LabelUsagePath) || !authentication.AllowsNext(LabelUsagePath) {
		return nil, errors.New("helpdesk: label usage requires matching cookie paths and an allowed login destination")
	}
	files, err := fs.Sub(ticketEditorFiles, "editor_templates")
	if err != nil {
		return nil, err
	}
	engine, err := templates.New(files, templates.Config{})
	if err != nil {
		return nil, err
	}
	return &LabelUsage{app: a, auth: authentication, engine: engine}, nil
}
func (page *LabelUsage) Routes() []web.Route {
	return []web.Route{{Name: "helpdesk:label-usage-page", Method: http.MethodGet, Path: LabelUsagePath, Handler: page.auth.Require(ViewLabel, page.get)}}
}
func (page *LabelUsage) get(request *web.Request, actor auth.Principal) (web.Response, error) {
	allowed, err := page.auth.Authorized(request.Context(), actor, ViewTicket)
	if err != nil {
		return web.Response{}, err
	}
	if !allowed {
		return ticketEditorText(http.StatusForbidden)
	}
	input, diagnostics, err := page.app.queries.labelUsage.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return web.Response{}, err
	}
	if !diagnostics.Empty() {
		return ticketEditorText(http.StatusBadRequest)
	}
	result, err := page.app.readLabelUsage(request.Context(), actor, input)
	if err != nil {
		return ticketEditorOperationError(err)
	}
	rows := make([]templates.Value, 0, len(result.labels.Rows))
	for _, row := range result.labels.Rows {
		cost, effort, elapsed := "Not set", "Not set", "Not set"
		if value, present := row.cost.Get(); present {
			cost = value.String()
		}
		if value, present := row.effort.Get(); present {
			effort = strconv.FormatFloat(value, 'g', -1, 64)
		}
		if value, present := row.elapsed.Get(); present {
			elapsed = value.String()
		}
		value, err := templates.Object(map[string]templates.Value{
			"id": templates.Integer(row.id), "name": templates.String(row.name), "total": templates.Integer(row.total), "open": templates.Integer(row.open), "closed": templates.Integer(row.total - row.open),
			"cost": templates.String(cost), "effort": templates.String(effort), "elapsed": templates.String(elapsed),
		})
		if err != nil {
			return web.Response{}, err
		}
		rows = append(rows, value)
	}
	previous, next := "", ""
	link := func(number int) string {
		return LabelUsagePath + "?p=" + strconv.Itoa(number) + "&usage=" + input.usage
	}
	if input.page > 1 {
		previous = link(input.page - 1)
	}
	if input.page < labelUsageMaximumPage && int64(input.page*labelUsagePageSize) < result.labels.Total {
		next = link(input.page + 1)
	}
	values, err := templates.NewContext(map[string]templates.Value{
		"category": templates.String(ticketEditorDisplay(result.category.Name)), "rows": templates.List(rows...), "total": templates.Integer(result.labels.Total), "page": templates.Integer(int64(input.page)),
		"usage_all": templates.Bool(input.usage == "all"), "usage_used": templates.Bool(input.usage == "used"), "usage_unused": templates.Bool(input.usage == "unused"), "previous": templates.String(previous), "next": templates.String(next),
	})
	if err != nil {
		return web.Response{}, err
	}
	body, err := page.engine.Render(request.Context(), "label_usage.html", values, templates.Capabilities{})
	if err != nil {
		return web.Response{}, err
	}
	return web.NewResponse(http.StatusOK, ticketEditorHeaders("text/html; charset=utf-8"), body)
}
