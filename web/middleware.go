package web

func applyMiddleware(terminal Handler, middleware []Middleware) (Handler, error) {
	result := terminal
	for index := len(middleware) - 1; index >= 0; index-- {
		if middleware[index] == nil {
			return nil, &Error{Code: CodeInvalidConfig, Field: "middleware", Detail: "middleware is nil"}
		}
		downstream := result
		middlewareIndex := index
		guarded := Handler(func(request *Request) (Response, error) {
			if request == nil || !request.claimNext(middlewareIndex) {
				return Response{}, &Error{
					Code:   CodeMiddlewareViolation,
					Detail: "middleware invoked its downstream handler more than once or outside the request lifetime",
				}
			}
			return downstream(request)
		})
		var err error
		result, err = invokeMiddleware(middleware[index], guarded)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, &Error{Code: CodeInvalidConfig, Field: "middleware", Detail: "middleware returned a nil handler"}
		}
	}
	return result, nil
}

func invokeMiddleware(middleware Middleware, downstream Handler) (handler Handler, err error) {
	defer func() {
		if recover() != nil {
			handler = nil
			err = &Error{Code: CodeInvalidConfig, Field: "middleware", Detail: "middleware panicked during startup"}
		}
	}()
	return middleware(downstream), nil
}
