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

type DeadlinePreview struct {
	app    *Application
	auth   *sessionauth.Runtime
	engine *templates.Engine
}

func (a *Application) DeadlinePreview(authentication *sessionauth.Runtime) (*DeadlinePreview, error) {
	if a == nil || nilFormReader(a.backend) || authentication == nil {
		return nil, errors.New("helpdesk: deadline preview requires application and authentication")
	}
	if !authentication.CookiesApplyTo(DeadlinePreviewPath) || !authentication.AllowsNext(DeadlinePreviewPath) {
		return nil, errors.New("helpdesk: deadline preview requires matching cookie paths and an allowed login destination")
	}
	files, err := fs.Sub(ticketEditorFiles, "editor_templates")
	if err != nil {
		return nil, err
	}
	engine, err := templates.New(files, templates.Config{})
	if err != nil {
		return nil, err
	}
	return &DeadlinePreview{app: a, auth: authentication, engine: engine}, nil
}

func (page *DeadlinePreview) Routes() []web.Route {
	return []web.Route{{Name: "helpdesk:deadline-preview-page", Method: http.MethodGet, Path: DeadlinePreviewPath, Handler: page.auth.Require(ViewTicket, page.get)}}
}

func (page *DeadlinePreview) get(request *web.Request, actor auth.Principal) (web.Response, error) {
	// The entry page asks for an explicit reference time. It performs no model
	// lookup or temporal query until the submitted parameters are valid.
	if request.HTTP().URL.RawQuery == "" {
		return page.render(request, nil)
	}
	input, diagnostics, err := page.app.queries.deadlinePreview.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return web.Response{}, err
	}
	if !diagnostics.Empty() {
		return ticketEditorText(http.StatusBadRequest)
	}
	result, err := page.app.readDeadlinePreview(request.Context(), actor, input)
	if err != nil {
		return ticketEditorOperationError(err)
	}
	return page.render(request, &result)
}

func (page *DeadlinePreview) render(request *web.Request, result *deadlinePreviewPage) (web.Response, error) {
	rows := []templates.Value{}
	category, asOf, minimum, previous, next, clear := "", "", "", "", "", ""
	input := deadlinePreviewQuery{page: 1}
	total := int64(0)
	if result != nil {
		input = result.query
		category, asOf, total = ticketEditorDisplay(result.category.Name), temporal.Format(input.asOf), result.tickets.Total
		if input.minimum != nil {
			minimum = input.minimum.String()
		}
		for _, row := range result.tickets.Rows {
			original, adjusted, remaining := "Not set", "Not set", "Not set"
			if row.original != nil {
				original = temporal.Format(*row.original)
			}
			if value, present := row.adjusted.Get(); present {
				adjusted = temporal.Format(value)
			}
			if value, present := row.remaining.Get(); present {
				remaining = value.String()
			}
			value, err := templates.Object(map[string]templates.Value{
				"id": templates.Integer(row.id), "subject": templates.String(ticketEditorDisplay(row.subject)),
				"original": templates.String(original), "adjusted": templates.String(adjusted), "remaining": templates.String(remaining),
			})
			if err != nil {
				return web.Response{}, err
			}
			rows = append(rows, value)
		}
		link := func(number int, withMinimum bool) string {
			values := url.Values{"p": {strconv.Itoa(number)}, "as_of": {asOf}, "shift": {input.shift.String()}}
			if withMinimum && input.minimum != nil {
				values.Set("min_remaining", minimum)
			}
			return DeadlinePreviewPath + "?" + values.Encode()
		}
		clear = link(1, false)
		if input.page > 1 {
			previous = link(input.page-1, true)
		}
		if input.page < deadlinePreviewMaximumPage && int64(input.page*deadlinePreviewPageSize) < total {
			next = link(input.page+1, true)
		}
	}
	values, err := templates.NewContext(map[string]templates.Value{
		"has_preview": templates.Bool(result != nil), "category": templates.String(category), "rows": templates.List(rows...),
		"as_of": templates.String(asOf), "shift": templates.String(input.shift.String()), "minimum": templates.String(minimum),
		"has_minimum": templates.Bool(input.minimum != nil), "clear_minimum": templates.String(clear),
		"total": templates.Integer(total), "page": templates.Integer(int64(input.page)), "previous": templates.String(previous), "next": templates.String(next),
	})
	if err != nil {
		return web.Response{}, err
	}
	body, err := page.engine.Render(request.Context(), "deadline_preview.html", values, templates.Capabilities{})
	if err != nil {
		return web.Response{}, err
	}
	return web.NewResponse(http.StatusOK, ticketEditorHeaders("text/html; charset=utf-8"), body)
}
