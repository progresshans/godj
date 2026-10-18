package helpdesk

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type ServicePreview struct {
	app    *Application
	auth   *sessionauth.Runtime
	engine *templates.Engine
}

func (a *Application) ServicePreview(authentication *sessionauth.Runtime) (*ServicePreview, error) {
	if a == nil || nilFormReader(a.backend) || authentication == nil {
		return nil, errors.New("helpdesk: service preview requires application and authentication")
	}
	if !authentication.CookiesApplyTo(ServicePreviewPath) || !authentication.AllowsNext(ServicePreviewPath) {
		return nil, errors.New("helpdesk: service preview requires matching cookie paths and an allowed login destination")
	}
	files, err := fs.Sub(ticketEditorFiles, "editor_templates")
	if err != nil {
		return nil, err
	}
	engine, err := templates.New(files, templates.Config{})
	if err != nil {
		return nil, err
	}
	return &ServicePreview{app: a, auth: authentication, engine: engine}, nil
}
func (page *ServicePreview) Routes() []web.Route {
	return []web.Route{{Name: "helpdesk:service-preview-page", Method: http.MethodGet, Path: ServicePreviewPath, Handler: page.auth.Require(ViewTicket, page.get)}}
}
func (page *ServicePreview) get(request *web.Request, actor auth.Principal) (web.Response, error) {
	input, diagnostics, err := page.app.queries.servicePreview.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return web.Response{}, err
	}
	if !diagnostics.Empty() {
		return ticketEditorText(http.StatusBadRequest)
	}
	result, err := page.app.readServicePreview(request.Context(), actor, input)
	if err != nil {
		return ticketEditorOperationError(err)
	}
	rows := make([]templates.Value, 0, len(result.tickets.Rows))
	for _, row := range result.tickets.Rows {
		day, wall, elapsed, due, start, adjusted, finish, slack := "Not set", "Not set", "Not set", "Not set", "Not set", "Not set", "Not set", "Not set"
		if row.day != nil {
			day = row.day.String()
		}
		if row.clock != nil {
			wall = row.clock.String()
		}
		if row.elapsed != nil {
			elapsed = row.elapsed.String()
		}
		if row.due != nil {
			due = temporal.Format(*row.due)
		}
		if value, ok := row.start.Get(); ok {
			start = temporal.Format(value)
		}
		if value, ok := row.adjusted.Get(); ok {
			adjusted = temporal.Format(value)
		}
		if value, ok := row.finish.Get(); ok {
			finish = temporal.Format(value)
		}
		if value, ok := row.slack.Get(); ok {
			slack = value.String()
		}
		value, err := templates.Object(map[string]templates.Value{
			"id": templates.Integer(row.id), "subject": templates.String(ticketEditorDisplay(row.subject)),
			"day": templates.String(day), "clock": templates.String(wall), "elapsed": templates.String(elapsed), "due": templates.String(due),
			"start": templates.String(start), "adjusted": templates.String(adjusted), "finish": templates.String(finish), "slack": templates.String(slack),
		})
		if err != nil {
			return web.Response{}, err
		}
		rows = append(rows, value)
	}
	minimum := ""
	if input.minimum != nil {
		minimum = temporal.Format(*input.minimum)
	}
	link := func(number int, withMinimum bool) string {
		values := url.Values{"p": {strconv.Itoa(number)}, "shift": {input.shift.String()}}
		if withMinimum && input.minimum != nil {
			values.Set("min_start", minimum)
		}
		return ServicePreviewPath + "?" + values.Encode()
	}
	previous, next := "", ""
	if input.page > 1 {
		previous = link(input.page-1, true)
	}
	if input.page < servicePreviewMaximumPage && int64(input.page*servicePreviewPageSize) < result.tickets.Total {
		next = link(input.page+1, true)
	}
	values, err := templates.NewContext(map[string]templates.Value{
		"category": templates.String(ticketEditorDisplay(result.category.Name)), "rows": templates.List(rows...),
		"total": templates.Integer(result.tickets.Total), "page": templates.Integer(int64(input.page)),
		"shift": templates.String(input.shift.String()), "minimum": templates.String(minimum), "has_minimum": templates.Bool(input.minimum != nil),
		"clear_minimum": templates.String(link(1, false)), "previous": templates.String(previous), "next": templates.String(next),
	})
	if err != nil {
		return web.Response{}, err
	}
	body, err := page.engine.Render(request.Context(), "service_preview.html", values, templates.Capabilities{})
	if err != nil {
		return web.Response{}, err
	}
	return web.NewResponse(http.StatusOK, ticketEditorHeaders("text/html; charset=utf-8"), body)
}
