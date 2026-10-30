package web

import (
	"math"
	"net/http"
	"net/url"
	pathpkg "path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/apps"
)

const (
	maximumRoutes              = 1024
	maximumRoutePathBytes      = 4096
	maximumRoutePathSegments   = 64
	maximumRouteParameters     = 16
	maximumRouteParameterBytes = 64
)

type routeParameterKind uint8

const (
	routeParameterInvalid routeParameterKind = iota
	routeParameterInt64
	routeParameterString
)

type routeSegment struct {
	literal       string
	parameterName string
	kind          routeParameterKind
}

type routePattern struct {
	path       string
	segments   []routeSegment
	parameters []routeSegment
}

type parameterRoute struct {
	route   Route
	pattern routePattern
}

type reverseRoute struct {
	path    string
	pattern *routePattern
}

type router struct {
	byPath     map[string]map[string]Route
	byName     map[string]reverseRoute
	parameters []parameterRoute
}

type routeParameterValue struct {
	name         string
	kind         routeParameterKind
	integerValue int64
	stringValue  string
}

type routeMatch struct {
	route      Route
	parameters []routeParameterValue
	allow      []string
	code       ErrorCode
}

func newRouter(registry apps.Registry, routes []Route) (router, error) {
	return compileRouter(&registry, routes)
}

// A nil registry checks route declarations before an application has selected
// its installed apps. Publication through New still checks namespace membership.
func compileRouter(registry *apps.Registry, routes []Route) (router, error) {
	if len(routes) > maximumRoutes {
		return router{}, &Error{Code: CodeInvalidRoute, Field: "routes", Detail: "route count exceeds the application limit"}
	}
	result := router{
		byPath: make(map[string]map[string]Route),
		byName: make(map[string]reverseRoute, len(routes)),
	}
	for index, route := range routes {
		pattern, err := validateRoute(registry, route)
		if err != nil {
			return router{}, &Error{
				Code:   CodeInvalidRoute,
				Field:  "routes",
				Detail: "route at index " + strconv.Itoa(index) + " is invalid",
				Cause:  err,
			}
		}
		if _, exists := result.byName[route.Name]; exists {
			return router{}, &Error{Code: CodeDuplicateRoute, Field: "name", Detail: "route name is already registered"}
		}
		if pattern == nil {
			methods := result.byPath[route.Path]
			if methods == nil {
				methods = make(map[string]Route)
				result.byPath[route.Path] = methods
			}
			if _, exists := methods[route.Method]; exists {
				return router{}, &Error{Code: CodeDuplicateRoute, Field: "method_path", Detail: "method and path are already registered"}
			}
			methods[route.Method] = route
			result.byName[route.Name] = reverseRoute{path: route.Path}
			continue
		}
		for _, existing := range result.parameters {
			if route.Method == existing.route.Method && routePatternsOverlap(*pattern, existing.pattern) {
				return router{}, &Error{Code: CodeDuplicateRoute, Field: "method_path", Detail: "parameter route languages overlap for one method"}
			}
		}
		result.parameters = append(result.parameters, parameterRoute{route: route, pattern: *pattern})
		result.byName[route.Name] = reverseRoute{path: route.Path, pattern: pattern}
	}
	return result, nil
}

func validateRoute(registry *apps.Registry, route Route) (*routePattern, error) {
	if route.Handler == nil {
		return nil, &Error{Code: CodeInvalidRoute, Field: "handler", Detail: "handler is nil"}
	}
	if !validMethod(route.Method) {
		return nil, &Error{Code: CodeInvalidRoute, Field: "method", Detail: "method must be a non-empty uppercase HTTP token"}
	}
	pattern, err := compileRoutePath(route.Path)
	if err != nil {
		return nil, err
	}
	label, name, found := strings.Cut(route.Name, ":")
	if !found || label == "" || strings.Contains(name, ":") || !validRoutePart(name) {
		return nil, &Error{Code: CodeInvalidRoute, Field: "name", Detail: "name must be <installed-app-label>:<route>"}
	}
	if registry != nil {
		if _, installed := registry.Lookup(label); !installed {
			return nil, &Error{Code: CodeInvalidRoute, Field: "name", Detail: "route namespace is not an installed app label"}
		}
	}
	return pattern, nil
}

func compileRoutePath(value string) (*routePattern, error) {
	if len(value) > maximumRoutePathBytes {
		return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "path exceeds the route byte limit"}
	}
	if !strings.ContainsAny(value, "<>") {
		if !validStaticPath(value) {
			return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "path must be absolute, clean, and static"}
		}
		if routePathSegmentCount(value) > maximumRoutePathSegments {
			return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "path exceeds the route segment limit"}
		}
		return nil, nil
	}
	if value == "" || !utf8.ValidString(value) || !strings.HasPrefix(value, "/") || value == "//" {
		return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "parameter path must be absolute and canonical"}
	}
	parts := strings.Split(value, "/")
	if routePathSegmentCount(value) > maximumRoutePathSegments {
		return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "path exceeds the route segment limit"}
	}
	segments := make([]routeSegment, len(parts))
	parameters := make([]routeSegment, 0, maximumRouteParameters)
	canonicalParts := append([]string(nil), parts...)
	for index, part := range parts {
		if !strings.ContainsAny(part, "<>") {
			segments[index] = routeSegment{literal: part}
			continue
		}
		if len(part) < len("<str:a>") || part[0] != '<' || part[len(part)-1] != '>' || strings.Count(part, "<") != 1 || strings.Count(part, ">") != 1 {
			return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "parameter segment must be <converter:name>"}
		}
		converter, name, found := strings.Cut(part[1:len(part)-1], ":")
		kind := routeParameterInvalid
		switch converter {
		case "int64":
			kind = routeParameterInt64
		case "str":
			kind = routeParameterString
		}
		if !found || kind == routeParameterInvalid || !validRouteParameterName(name) {
			return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "parameter segment must use int64 or str with a valid name"}
		}
		if len(name) > maximumRouteParameterBytes {
			return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "parameter name exceeds the byte limit"}
		}
		for _, existing := range parameters {
			if existing.parameterName == name {
				return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "parameter names must be unique within a route"}
			}
		}
		if len(parameters) == maximumRouteParameters {
			return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "path exceeds the parameter count limit"}
		}
		segments[index] = routeSegment{parameterName: name, kind: kind}
		parameters = append(parameters, segments[index])
		canonicalParts[index] = "0"
	}
	if len(parameters) == 0 || !validStaticPath(strings.Join(canonicalParts, "/")) {
		return nil, &Error{Code: CodeInvalidRoute, Field: "path", Detail: "parameter path must be absolute, clean, and canonical"}
	}
	return &routePattern{path: value, segments: segments, parameters: parameters}, nil
}

func validMethod(method string) bool {
	if method == "" || method != strings.ToUpper(method) {
		return false
	}
	for index := 0; index < len(method); index++ {
		character := method[index]
		if character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validStaticPath(value string) bool {
	if value == "" || len(value) > maximumRoutePathBytes || !utf8.ValidString(value) || !strings.HasPrefix(value, "/") {
		return false
	}
	if value == "//" {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	cleanCandidate := value
	if value != "/" && strings.HasSuffix(value, "/") {
		cleanCandidate = strings.TrimSuffix(value, "/")
	}
	if pathpkg.Clean(cleanCandidate) != cleanCandidate {
		return false
	}
	return !strings.ContainsAny(value, "?#\\{}:<>*")
}

func validRoutePart(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if unicode.IsLetter(character) || (index > 0 && unicode.IsDigit(character)) || (index > 0 && (character == '_' || character == '-')) {
			continue
		}
		return false
	}
	return true
}

func validRouteParameterName(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			index > 0 && (character >= '0' && character <= '9' || character == '_') {
			continue
		}
		return false
	}
	return true
}

func routePathSegmentCount(value string) int {
	if value == "/" {
		return 0
	}
	return strings.Count(strings.Trim(value, "/"), "/") + 1
}

func routePatternsOverlap(left, right routePattern) bool {
	if len(left.segments) != len(right.segments) {
		return false
	}
	minimumBytes := len(left.segments) - 1
	for index, l := range left.segments {
		r := right.segments[index]
		switch {
		case l.kind != routeParameterInvalid && r.kind != routeParameterInvalid:
			// Both closed converters admit "0", a shortest common witness.
			minimumBytes++
		case l.kind != routeParameterInvalid:
			if _, ok := parseRouteParameter(l.kind, r.literal); !ok {
				return false
			}
			minimumBytes += len(r.literal)
		case r.kind != routeParameterInvalid:
			if _, ok := parseRouteParameter(r.kind, l.literal); !ok {
				return false
			}
			minimumBytes += len(l.literal)
		default:
			if l.literal != r.literal {
				return false
			}
			minimumBytes += len(l.literal)
		}
	}
	return minimumBytes <= maximumRoutePathBytes
}

func (r router) match(method string, request *http.Request) routeMatch {
	path, valid := validRequestRoutePath(request)
	if !valid {
		return routeMatch{code: CodeRouteNotFound}
	}
	if methods := r.byPath[path]; methods != nil {
		if route, ok := methods[method]; ok {
			return routeMatch{route: route}
		}
		return routeMatch{allow: sortedMethods(methods), code: CodeMethodNotAllowed}
	}
	pathSegments := strings.Split(path, "/")
	var allowed map[string]struct{}
	for _, candidate := range r.parameters {
		parameters, ok := matchRoutePattern(candidate.pattern, pathSegments)
		if !ok {
			continue
		}
		if candidate.route.Method == method {
			return routeMatch{route: candidate.route, parameters: parameters}
		}
		if allowed == nil {
			allowed = make(map[string]struct{})
		}
		allowed[candidate.route.Method] = struct{}{}
	}
	if len(allowed) == 0 {
		return routeMatch{code: CodeRouteNotFound}
	}
	allow := make([]string, 0, len(allowed))
	for allowedMethod := range allowed {
		allow = append(allow, allowedMethod)
	}
	sort.Strings(allow)
	return routeMatch{allow: allow, code: CodeMethodNotAllowed}
}

func validRequestRoutePath(request *http.Request) (string, bool) {
	if request == nil || request.URL == nil {
		return "", false
	}
	value := request.URL.Path
	if len(value) > maximumRoutePathBytes || routePathSegmentCount(value) > maximumRoutePathSegments || !validRequestDecodedPath(value) || containsUnsafeEncodedPathByte(request) {
		return "", false
	}
	return value, true
}

func validRequestDecodedPath(value string) bool {
	if value == "" || !utf8.ValidString(value) || !strings.HasPrefix(value, "/") || value == "//" {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f || character == '\\' {
			return false
		}
	}
	cleanCandidate := value
	if value != "/" && strings.HasSuffix(value, "/") {
		cleanCandidate = strings.TrimSuffix(value, "/")
	}
	return pathpkg.Clean(cleanCandidate) == cleanCandidate
}

func containsUnsafeEncodedPathByte(request *http.Request) bool {
	rawPath := request.URL.RawPath
	if rawPath == "" && request.RequestURI != "" {
		if strings.HasPrefix(request.RequestURI, "/") {
			rawPath, _, _ = strings.Cut(request.RequestURI, "?")
		} else {
			rawPath = request.URL.EscapedPath()
		}
	}
	if len(rawPath) > maximumRoutePathBytes {
		return true
	}
	for index := 0; index < len(rawPath); index++ {
		if rawPath[index] != '%' {
			continue
		}
		if index+2 >= len(rawPath) {
			return true
		}
		decoded, ok := decodeHexByte(rawPath[index+1], rawPath[index+2])
		if !ok {
			return true
		}
		if decoded == '/' || decoded == '\\' || decoded < 0x20 || decoded == 0x7f {
			return true
		}
		index += 2
	}
	return false
}

func decodeHexByte(high, low byte) (byte, bool) {
	highValue, highOK := hexValue(high)
	lowValue, lowOK := hexValue(low)
	return highValue<<4 | lowValue, highOK && lowOK
}

func hexValue(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func matchRoutePattern(pattern routePattern, pathSegments []string) ([]routeParameterValue, bool) {
	if len(pattern.segments) != len(pathSegments) {
		return nil, false
	}
	var values [maximumRouteParameters]routeParameterValue
	count := 0
	for index, segment := range pattern.segments {
		if segment.kind == routeParameterInvalid {
			if segment.literal != pathSegments[index] {
				return nil, false
			}
			continue
		}
		value, ok := parseRouteParameter(segment.kind, pathSegments[index])
		if !ok {
			return nil, false
		}
		value.name = segment.parameterName
		values[count] = value
		count++
	}
	return append([]routeParameterValue(nil), values[:count]...), true
}

func parseRouteParameter(kind routeParameterKind, text string) (routeParameterValue, bool) {
	switch kind {
	case routeParameterInt64:
		value, ok := parseCanonicalInt64(text)
		return routeParameterValue{kind: kind, integerValue: value}, ok
	case routeParameterString:
		return routeParameterValue{kind: kind, stringValue: text}, validStringSegment(text)
	default:
		return routeParameterValue{}, false
	}
}

func validStringSegment(text string) bool {
	if text == "" || len(text) > MaximumStringParameterBytes || !utf8.ValidString(text) || text == "." || text == ".." {
		return false
	}
	for _, character := range text {
		if character < 0x20 || character == 0x7f || character == '/' || character == '\\' {
			return false
		}
	}
	return true
}

func parseCanonicalInt64(value string) (int64, bool) {
	if value == "0" {
		return 0, true
	}
	if value == "" || value[0] == '0' || len(value) > 19 {
		return 0, false
	}
	var result int64
	for index := 0; index < len(value); index++ {
		digit := value[index]
		if digit < '0' || digit > '9' {
			return 0, false
		}
		number := int64(digit - '0')
		if result > (math.MaxInt64-number)/10 {
			return 0, false
		}
		result = result*10 + number
	}
	return result, true
}

func (r router) reverse(name string, arguments []ReverseArgument) (string, error) {
	route, ok := r.byName[name]
	if !ok {
		return "", &Error{Code: CodeReverseNotFound, Field: "name", Detail: "route name is not registered"}
	}
	if route.pattern == nil {
		if len(arguments) != 0 {
			return "", reverseArgumentError("static route does not accept arguments")
		}
		return reversePath(route.path)
	}
	return reversePattern(*route.pattern, arguments)
}

func reversePattern(pattern routePattern, arguments []ReverseArgument) (string, error) {
	if len(arguments) > maximumRouteParameters {
		return "", reverseArgumentError("argument count exceeds the route limit")
	}
	values := make([]string, len(pattern.parameters))
	provided := make([]bool, len(pattern.parameters))
	for _, argument := range arguments {
		if len(argument.name) > maximumRouteParameterBytes || !validRouteParameterName(argument.name) {
			return "", reverseArgumentError("argument name is invalid")
		}
		parameterIndex := -1
		for index, parameter := range pattern.parameters {
			if argument.name == parameter.parameterName {
				parameterIndex = index
				break
			}
		}
		if parameterIndex < 0 {
			return "", reverseArgumentError("route does not declare an argument with this name")
		}
		if provided[parameterIndex] {
			return "", reverseArgumentError("argument is provided more than once")
		}
		if argument.kind != pattern.parameters[parameterIndex].kind {
			return "", reverseArgumentError("argument kind does not match the route converter")
		}
		var value string
		switch argument.kind {
		case routeParameterInt64:
			if argument.integerValue < 0 {
				return "", reverseArgumentError("integer argument value is invalid")
			}
			value = strconv.FormatInt(argument.integerValue, 10)
		case routeParameterString:
			if !validStringSegment(argument.stringValue) {
				return "", reverseArgumentError("string argument value is invalid")
			}
			value = argument.stringValue
		default:
			return "", reverseArgumentError("argument has an unsupported kind")
		}
		provided[parameterIndex] = true
		values[parameterIndex] = value
	}
	for _, present := range provided {
		if !present {
			return "", reverseArgumentError("a required route argument is missing")
		}
	}
	var result strings.Builder
	result.Grow(len(pattern.path))
	parameterIndex := 0
	for index, segment := range pattern.segments {
		if index > 0 {
			result.WriteByte('/')
		}
		if segment.kind == routeParameterInvalid {
			result.WriteString(segment.literal)
		} else {
			result.WriteString(values[parameterIndex])
			parameterIndex++
		}
		if result.Len() > maximumRoutePathBytes {
			return "", reverseArgumentError("reversed path exceeds the route byte limit")
		}
	}
	return reversePath(result.String())
}

func reversePath(path string) (string, error) {
	escaped := (&url.URL{Path: path}).EscapedPath()
	if len(escaped) > maximumRoutePathBytes {
		return "", reverseArgumentError("reversed path exceeds the route byte limit")
	}
	return escaped, nil
}

func reverseArgumentError(detail string) error {
	return &Error{Code: CodeReverseArguments, Field: "arguments", Detail: detail}
}

func sortedMethods(methods map[string]Route) []string {
	allow := make([]string, 0, len(methods))
	for allowedMethod := range methods {
		allow = append(allow, allowedMethod)
	}
	sort.Strings(allow)
	return allow
}

func methodNotAllowedResponse(allow []string) Response {
	response := plainText(http.StatusMethodNotAllowed, "Method Not Allowed\n")
	response.header.Set("Allow", strings.Join(allow, ", "))
	response.routingError = CodeMethodNotAllowed
	return response
}
