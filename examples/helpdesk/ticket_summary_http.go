package helpdesk

import (
	"errors"
	"io/fs"
	"math"
	"net/http"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

// TicketSummary owns a read-only HTML page over its application's category.
// Construction performs no I/O and requires no write permission or audit sink.
type TicketSummary struct {
	app    *Application
	auth   *sessionauth.Runtime
	engine *templates.Engine
}

func (a *Application) TicketSummary(authentication *sessionauth.Runtime) (*TicketSummary, error) {
	if a == nil || nilFormReader(a.backend) || authentication == nil {
		return nil, errors.New("helpdesk: ticket summary requires application and authentication")
	}
	if !authentication.CookiesApplyTo(TicketSummaryPath) || !authentication.AllowsNext(TicketSummaryPath) {
		return nil, errors.New("helpdesk: ticket summary requires matching cookie paths and an allowed login destination")
	}
	files, err := fs.Sub(ticketEditorFiles, "editor_templates")
	if err != nil {
		return nil, err
	}
	engine, err := templates.New(files, templates.Config{})
	if err != nil {
		return nil, err
	}
	return &TicketSummary{app: a, auth: authentication, engine: engine}, nil
}

func (summary *TicketSummary) Routes() []web.Route {
	return []web.Route{{Name: "helpdesk:ticket-summary-page", Method: http.MethodGet, Path: TicketSummaryPath, Handler: summary.auth.Require(ViewTicket, summary.get)}}
}

func (summary *TicketSummary) get(request *web.Request, _ auth.Principal) (web.Response, error) {
	input, diagnostics, err := summary.app.queries.summary.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return web.Response{}, err
	}
	if !diagnostics.Empty() {
		return ticketEditorText(http.StatusBadRequest)
	}
	page, err := summary.app.readTicketSummary(request.Context(), input)
	if err != nil {
		return ticketEditorOperationError(err)
	}
	rows := make([]templates.Value, 0, len(page.groups.Rows))
	for _, row := range page.groups.Rows {
		cost, effort, elapsed := "Not set", "Not set", "Not set"
		if value, valid := row.cost.Get(); valid {
			cost = value.String()
		}
		if value, valid := row.effort.Get(); valid {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return web.Response{}, errors.New("helpdesk: summary effort is not finite")
			}
			effort = strconv.FormatFloat(value, 'g', -1, 64)
		}
		if value, valid := row.elapsed.Get(); valid {
			elapsed = value.String()
		}
		value, err := templates.Object(map[string]templates.Value{
			"priority": templates.String(ticketSummaryPriorityLabel(row.priority)), "total": templates.Integer(row.total), "open": templates.Integer(row.open),
			"cost": templates.String(cost), "effort": templates.String(effort), "elapsed": templates.String(elapsed),
		})
		if err != nil {
			return web.Response{}, err
		}
		rows = append(rows, value)
	}
	previous, next := "", ""
	link := func(number int) string {
		return TicketSummaryPath + "?p=" + strconv.Itoa(number) + "&min_open=" + strconv.FormatInt(input.minimumOpen, 10)
	}
	if input.page > 1 {
		previous = link(input.page - 1)
	}
	if input.page < ticketSummaryMaximumPage && int64(input.page*ticketSummaryPageSize) < page.groups.Total {
		next = link(input.page + 1)
	}
	values, err := templates.NewContext(map[string]templates.Value{
		"category": templates.String(ticketEditorDisplay(page.category.Name)), "rows": templates.List(rows...), "total_groups": templates.Integer(page.groups.Total),
		"page": templates.Integer(int64(input.page)), "min_open": templates.Integer(input.minimumOpen), "previous": templates.String(previous), "next": templates.String(next),
	})
	if err != nil {
		return web.Response{}, err
	}
	body, err := summary.engine.Render(request.Context(), "summary.html", values, templates.Capabilities{})
	if err != nil {
		return web.Response{}, err
	}
	return web.NewResponse(http.StatusOK, ticketEditorHeaders("text/html; charset=utf-8"), body)
}
