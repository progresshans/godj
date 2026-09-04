package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/progresshans/godj/settings"
)

// Application is an immutable, concurrent-safe HTTP application.
type Application struct {
	settings         settings.Settings
	router           router
	handler          Handler
	logger           *slog.Logger
	maxResponseBytes int64
	middlewareCount  int
}

// NewApplication constructs and validates a complete HTTP application at startup.
func NewApplication(config Config) (*Application, error) {
	if config.Settings.ProjectName() == "" {
		return nil, &Error{Code: CodeInvalidConfig, Field: "settings", Detail: "settings are zero or invalid"}
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	if maxResponseBytes < 0 {
		return nil, &Error{Code: CodeInvalidConfig, Field: "max_response_bytes", Detail: "limit must be positive"}
	}
	if len(config.Routes) > maximumRoutes {
		return nil, &Error{Code: CodeInvalidRoute, Field: "routes", Detail: "route count exceeds the application limit"}
	}
	routes := append([]Route(nil), config.Routes...)
	configuredRouter, err := newRouter(config.Settings.Apps(), routes)
	if err != nil {
		return nil, err
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	application := &Application{
		settings:         config.Settings,
		router:           configuredRouter,
		logger:           logger,
		maxResponseBytes: maxResponseBytes,
		middlewareCount:  len(config.Middleware),
	}
	application.handler, err = applyMiddleware(application.dispatch, append([]Middleware(nil), config.Middleware...))
	if err != nil {
		return nil, err
	}
	return application, nil
}

// Reverse resolves a namespaced route that requires no arguments without
// request state. Parameterized routes return CodeReverseArguments.
func (a *Application) Reverse(name string) (string, error) {
	if a == nil {
		return "", &Error{Code: CodeInvalidConfig, Detail: "application is nil"}
	}
	return a.router.reverse(name, nil)
}

// ReverseWith resolves a namespaced route with closed typed arguments without
// request state.
func (a *Application) ReverseWith(name string, arguments ...ReverseArgument) (string, error) {
	if a == nil {
		return "", &Error{Code: CodeInvalidConfig, Detail: "application is nil"}
	}
	return a.router.reverse(name, arguments)
}

// ServeHTTP executes one synchronous, fully buffered request. Errors are
// logged and replaced by a fixed 500 response before any client bytes are
// written.
func (a *Application) ServeHTTP(writer http.ResponseWriter, rawRequest *http.Request) {
	if a == nil || writer == nil || rawRequest == nil {
		return
	}
	request := newRequest(rawRequest, a.settings, a.router.reverse, a.middlewareCount)
	response, err := a.execute(request)
	request.release()
	if err == nil {
		err = response.validate(a.maxResponseBytes)
	}
	if err != nil {
		a.logger.ErrorContext(logContext(rawRequest), "web request failed", "method", rawRequest.Method, "path", requestPath(rawRequest), "error", err)
		response = plainText(http.StatusInternalServerError, "Internal Server Error\n")
	}
	writeResponse(writer, response)
}

func (a *Application) execute(request *Request) (response Response, err error) {
	defer func() {
		if recover() != nil {
			response = Response{}
			err = &Error{Code: CodeHandlerFailure, Detail: "handler panicked"}
		}
	}()
	response, err = a.handler(request)
	if request.middlewareViolated() {
		return Response{}, &Error{Code: CodeMiddlewareViolation, Detail: "middleware invoked its downstream handler more than once"}
	}
	return response, err
}

func (a *Application) dispatch(request *Request) (Response, error) {
	if request == nil || request.HTTP() == nil {
		return Response{}, &Error{Code: CodeInvalidRequest, Detail: "request is nil or outside its lifetime"}
	}
	match := a.router.match(request.Method(), request.HTTP())
	switch match.code {
	case "":
		request.setRouteParameters(match.parameters)
		return match.route.Handler(request)
	case CodeRouteNotFound:
		response := plainText(http.StatusNotFound, "Not Found\n")
		response.routingError = CodeRouteNotFound
		return response, nil
	case CodeMethodNotAllowed:
		return methodNotAllowedResponse(match.allow), nil
	default:
		return Response{}, &Error{Code: CodeHandlerFailure, Detail: "router returned an unknown match state"}
	}
}

func writeResponse(writer http.ResponseWriter, response Response) {
	header := writer.Header()
	for name, values := range response.header {
		for _, value := range values {
			header.Add(name, value)
		}
	}
	header.Set("Content-Length", strconv.Itoa(len(response.body)))
	writer.WriteHeader(response.status)
	_, _ = writer.Write(response.body)
}

func plainText(status int, body string) Response {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain; charset=utf-8")
	response, _ := NewResponse(status, header, []byte(body))
	return response
}

func logContext(request *http.Request) context.Context {
	if request == nil || request.Context() == nil || request.Context().Err() != nil {
		return context.Background()
	}
	return request.Context()
}

func requestPath(request *http.Request) string {
	if request == nil || request.URL == nil {
		return ""
	}
	return request.URL.Path
}

func invalidServerField(field, detail string, cause error) error {
	return &Error{Code: CodeServerState, Field: field, Detail: detail, Cause: cause}
}
