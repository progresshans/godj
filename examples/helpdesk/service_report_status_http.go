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

type ServiceReportStatus struct {
	app    *Application
	auth   *sessionauth.Runtime
	engine *templates.Engine
}

func (a *Application) ServiceReportStatus(authentication *sessionauth.Runtime) (*ServiceReportStatus, error) {
	if a == nil || interfaceNil(a.backend) || authentication == nil {
		return nil, errors.New("helpdesk: report status requires application and authentication")
	}
	if !authentication.CookiesApplyTo(ServiceReportStatusPath) || !authentication.AllowsNext(ServiceReportStatusPath) {
		return nil, errors.New("helpdesk: report status requires matching cookie paths and an allowed login destination")
	}
	files, err := fs.Sub(ticketEditorFiles, "editor_templates")
	if err != nil {
		return nil, err
	}
	engine, err := templates.New(files, templates.Config{})
	if err != nil {
		return nil, err
	}
	return &ServiceReportStatus{app: a, auth: authentication, engine: engine}, nil
}

func (page *ServiceReportStatus) Routes() []web.Route {
	return []web.Route{{Name: "helpdesk:service-report-status-page", Method: http.MethodGet, Path: ServiceReportStatusPath,
		Handler: page.auth.Require(ViewServiceReport, page.get)}}
}

func (page *ServiceReportStatus) get(request *web.Request, actor auth.Principal) (web.Response, error) {
	allowed, err := page.auth.Authorized(request.Context(), actor, ViewTicket)
	if err != nil {
		return web.Response{}, err
	}
	if !allowed {
		return ticketEditorText(http.StatusForbidden)
	}
	input, diagnostics, err := page.app.queries.reportStatus.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return web.Response{}, err
	}
	if !diagnostics.Empty() {
		return ticketEditorText(http.StatusBadRequest)
	}
	result, err := page.app.readServiceReportStatus(request.Context(), actor, input)
	if err != nil {
		return ticketEditorOperationError(err)
	}
	rows := make([]templates.Value, 0, len(result.reports.Rows))
	for _, row := range result.reports.Rows {
		matched, _ := row.matched.Get()
		value, err := templates.Object(map[string]templates.Value{
			"id": templates.Integer(row.id), "ticket": templates.Integer(row.ticketID), "summary": templates.String(row.summary),
			"completed": templates.Bool(row.completed), "ticket_closed": templates.Bool(*row.ticketClosed), "matched": templates.Bool(matched),
		})
		if err != nil {
			return web.Response{}, err
		}
		rows = append(rows, value)
	}
	previous, next := "", ""
	link := func(number int) string {
		return ServiceReportStatusPath + "?p=" + strconv.Itoa(number) + "&comparison=" + input.comparison
	}
	if input.page > 1 {
		previous = link(input.page - 1)
	}
	if input.page < serviceReportStatusMaximumPage && int64(input.page*serviceReportStatusPageSize) < result.reports.Total {
		next = link(input.page + 1)
	}
	values, err := templates.NewContext(map[string]templates.Value{
		"category": templates.String(ticketEditorDisplay(result.category.Name)), "rows": templates.List(rows...),
		"total": templates.Integer(result.reports.Total), "page": templates.Integer(int64(input.page)),
		"comparison_all": templates.Bool(input.comparison == "all"), "comparison_same": templates.Bool(input.comparison == "same"),
		"comparison_different": templates.Bool(input.comparison == "different"), "previous": templates.String(previous), "next": templates.String(next),
	})
	if err != nil {
		return web.Response{}, err
	}
	body, err := page.engine.Render(request.Context(), "report_status.html", values, templates.Capabilities{})
	if err != nil {
		return web.Response{}, err
	}
	return web.NewResponse(http.StatusOK, ticketEditorHeaders("text/html; charset=utf-8"), body)
}
