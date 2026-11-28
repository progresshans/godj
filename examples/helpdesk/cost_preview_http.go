package helpdesk

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type CostPreview struct {
	app    *Application
	auth   *sessionauth.Runtime
	engine *templates.Engine
}

func (a *Application) CostPreview(authentication *sessionauth.Runtime) (*CostPreview, error) {
	if a == nil || interfaceNil(a.backend) || authentication == nil {
		return nil, errors.New("helpdesk: cost preview requires application and authentication")
	}
	if !authentication.CookiesApplyTo(CostPreviewPath) || !authentication.AllowsNext(CostPreviewPath) {
		return nil, errors.New("helpdesk: cost preview requires matching cookie paths and an allowed login destination")
	}
	files, err := fs.Sub(ticketEditorFiles, "editor_templates")
	if err != nil {
		return nil, err
	}
	engine, err := templates.New(files, templates.Config{})
	if err != nil {
		return nil, err
	}
	return &CostPreview{app: a, auth: authentication, engine: engine}, nil
}
func (page *CostPreview) Routes() []web.Route {
	return []web.Route{{Name: "helpdesk:cost-preview-page", Method: http.MethodGet, Path: CostPreviewPath, Handler: page.auth.Require(ViewTicket, page.get)}}
}
func (page *CostPreview) get(request *web.Request, actor auth.Principal) (web.Response, error) {
	input, diagnostics, err := page.app.queries.costPreview.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return web.Response{}, err
	}
	if !diagnostics.Empty() {
		return ticketEditorText(http.StatusBadRequest)
	}
	result, err := page.app.readCostPreview(request.Context(), actor, input)
	if err != nil {
		return ticketEditorOperationError(err)
	}
	rows := make([]templates.Value, 0, len(result.tickets.Rows))
	for _, row := range result.tickets.Rows {
		original, adjusted := "Not set", "Not set"
		if row.original != nil {
			original = row.original.String()
		}
		if value, present := row.adjusted.Get(); present {
			adjusted = value.String()
		}
		value, err := templates.Object(map[string]templates.Value{"id": templates.Integer(row.id), "subject": templates.String(ticketEditorDisplay(row.subject)), "original": templates.String(original), "adjusted": templates.String(adjusted)})
		if err != nil {
			return web.Response{}, err
		}
		rows = append(rows, value)
	}
	minimum := ""
	if input.minimum != nil {
		minimum = input.minimum.String()
	}
	link := func(number int, withMinimum bool) string {
		values := url.Values{"p": {strconv.Itoa(number)}, "factor": {input.factor.String()}, "delta": {input.delta.String()}}
		if withMinimum && input.minimum != nil {
			values.Set("min_cost", minimum)
		}
		return CostPreviewPath + "?" + values.Encode()
	}
	previous, next := "", ""
	if input.page > 1 {
		previous = link(input.page-1, true)
	}
	if input.page < costPreviewMaximumPage && int64(input.page*costPreviewPageSize) < result.tickets.Total {
		next = link(input.page+1, true)
	}
	values, err := templates.NewContext(map[string]templates.Value{
		"category": templates.String(ticketEditorDisplay(result.category.Name)), "rows": templates.List(rows...), "total": templates.Integer(result.tickets.Total), "page": templates.Integer(int64(input.page)),
		"factor": templates.String(input.factor.String()), "delta": templates.String(input.delta.String()), "minimum": templates.String(minimum), "has_minimum": templates.Bool(input.minimum != nil),
		"clear_minimum": templates.String(link(1, false)), "previous": templates.String(previous), "next": templates.String(next),
	})
	if err != nil {
		return web.Response{}, err
	}
	body, err := page.engine.Render(request.Context(), "cost_preview.html", values, templates.Capabilities{})
	if err != nil {
		return web.Response{}, err
	}
	return web.NewResponse(http.StatusOK, ticketEditorHeaders("text/html; charset=utf-8"), body)
}
