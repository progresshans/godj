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

type DeadlineCalendar struct {
	app    *Application
	auth   *sessionauth.Runtime
	engine *templates.Engine
}

func (a *Application) DeadlineCalendar(authentication *sessionauth.Runtime) (*DeadlineCalendar, error) {
	if a == nil || nilFormReader(a.backend) || authentication == nil {
		return nil, errors.New("helpdesk: deadline calendar requires application and authentication")
	}
	if !authentication.CookiesApplyTo(DeadlineCalendarPath) || !authentication.AllowsNext(DeadlineCalendarPath) {
		return nil, errors.New("helpdesk: deadline calendar requires matching cookie paths and an allowed login destination")
	}
	files, err := fs.Sub(ticketEditorFiles, "editor_templates")
	if err != nil {
		return nil, err
	}
	engine, err := templates.New(files, templates.Config{})
	if err != nil {
		return nil, err
	}
	return &DeadlineCalendar{app: a, auth: authentication, engine: engine}, nil
}

func (page *DeadlineCalendar) Routes() []web.Route {
	return []web.Route{{Name: "helpdesk:deadline-calendar-page", Method: http.MethodGet, Path: DeadlineCalendarPath, Handler: page.auth.Require(ViewTicket, page.get)}}
}

func (page *DeadlineCalendar) get(request *web.Request, actor auth.Principal) (web.Response, error) {
	if request.HTTP().URL.RawQuery == "" {
		return page.render(request, nil)
	}
	input, diagnostics, err := page.app.queries.deadlineCalendar.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return web.Response{}, err
	}
	if !diagnostics.Empty() || !input.validRange() {
		return ticketEditorText(http.StatusBadRequest)
	}
	result, err := page.app.readDeadlineCalendar(request.Context(), actor, input)
	if err != nil {
		return ticketEditorOperationError(err)
	}
	return page.render(request, &result)
}

func (page *DeadlineCalendar) render(request *web.Request, result *deadlineCalendarPage) (web.Response, error) {
	rows := []templates.Value{}
	category, asOf, from, until, previous, next, clear := "", "", "", "", "", "", ""
	input := deadlineCalendarQuery{page: 1}
	total := int64(0)
	if result != nil {
		input = result.query
		category, asOf, total = ticketEditorDisplay(result.category.Name), temporal.Format(input.asOf), result.groups.Total
		if input.from != nil {
			from = input.from.String()
		}
		if input.until != nil {
			until = input.until.String()
		}
		for _, row := range result.groups.Rows {
			key, date, first, last := "unset", "Not set", "Not set", "Not set"
			if value, present := row.day.Get(); present {
				key, date = value.String(), value.String()
			}
			if value, present := row.first.Get(); present {
				first = value.String()
			}
			if value, present := row.last.Get(); present {
				last = value.String()
			}
			value, err := templates.Object(map[string]templates.Value{
				"key": templates.String(key), "date": templates.String(date), "total": templates.Integer(row.total),
				"open": templates.Integer(row.open), "overdue": templates.Integer(row.overdue),
				"first": templates.String(first), "last": templates.String(last),
			})
			if err != nil {
				return web.Response{}, err
			}
			rows = append(rows, value)
		}
		link := func(number int, withRange bool) string {
			values := url.Values{"p": {strconv.Itoa(number)}, "as_of": {asOf}}
			if withRange && input.from != nil {
				values.Set("from_date", from)
			}
			if withRange && input.until != nil {
				values.Set("until_date", until)
			}
			return DeadlineCalendarPath + "?" + values.Encode()
		}
		clear = link(1, false)
		if input.page > 1 {
			previous = link(input.page-1, true)
		}
		if input.page < deadlineCalendarMaximumPage && int64(input.page*deadlineCalendarPageSize) < total {
			next = link(input.page+1, true)
		}
	}
	values, err := templates.NewContext(map[string]templates.Value{
		"has_calendar": templates.Bool(result != nil), "category": templates.String(category), "rows": templates.List(rows...),
		"as_of": templates.String(asOf), "from": templates.String(from), "until": templates.String(until),
		"has_from": templates.Bool(input.from != nil), "has_until": templates.Bool(input.until != nil),
		"has_range": templates.Bool(input.from != nil || input.until != nil), "clear_range": templates.String(clear),
		"total": templates.Integer(total), "page": templates.Integer(int64(input.page)), "previous": templates.String(previous), "next": templates.String(next),
	})
	if err != nil {
		return web.Response{}, err
	}
	body, err := page.engine.Render(request.Context(), "deadline_calendar.html", values, templates.Capabilities{})
	if err != nil {
		return web.Response{}, err
	}
	return web.NewResponse(http.StatusOK, ticketEditorHeaders("text/html; charset=utf-8"), body)
}
