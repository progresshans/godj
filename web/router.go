package web

import (
	"net/http"
	pathpkg "path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/apps"
)

type router struct {
	byPath map[string]map[string]Route
	byName map[string]string
}

type routeMatch struct {
	route Route
	allow []string
	code  ErrorCode
}

func newRouter(registry apps.Registry, routes []Route) (router, error) {
	result := router{
		byPath: make(map[string]map[string]Route),
		byName: make(map[string]string, len(routes)),
	}
	for index, route := range routes {
		if err := validateRoute(registry, route); err != nil {
			return router{}, &Error{
				Code:   CodeInvalidRoute,
				Field:  "routes",
				Detail: "route at index " + decimal(index) + " is invalid",
				Cause:  err,
			}
		}
		if _, exists := result.byName[route.Name]; exists {
			return router{}, &Error{Code: CodeDuplicateRoute, Field: "name", Detail: "route name is already registered"}
		}
		methods := result.byPath[route.Path]
		if methods == nil {
			methods = make(map[string]Route)
			result.byPath[route.Path] = methods
		}
		if _, exists := methods[route.Method]; exists {
			return router{}, &Error{Code: CodeDuplicateRoute, Field: "method_path", Detail: "method and path are already registered"}
		}
		methods[route.Method] = route
		result.byName[route.Name] = route.Path
	}
	return result, nil
}

func validateRoute(registry apps.Registry, route Route) error {
	if route.Handler == nil {
		return &Error{Code: CodeInvalidRoute, Field: "handler", Detail: "handler is nil"}
	}
	if !validMethod(route.Method) {
		return &Error{Code: CodeInvalidRoute, Field: "method", Detail: "method must be a non-empty uppercase HTTP token"}
	}
	if !validStaticPath(route.Path) {
		return &Error{Code: CodeInvalidRoute, Field: "path", Detail: "path must be absolute, clean, and static"}
	}
	label, name, found := strings.Cut(route.Name, ":")
	if !found || strings.Contains(name, ":") || !validRoutePart(name) {
		return &Error{Code: CodeInvalidRoute, Field: "name", Detail: "name must be <installed-app-label>:<route>"}
	}
	if _, installed := registry.Lookup(label); !installed {
		return &Error{Code: CodeInvalidRoute, Field: "name", Detail: "route namespace is not an installed app label"}
	}
	return nil
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
	if value == "" || !utf8.ValidString(value) || !strings.HasPrefix(value, "/") {
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

func (r router) match(method, path string) routeMatch {
	methods := r.byPath[path]
	if methods == nil {
		return routeMatch{code: CodeRouteNotFound}
	}
	if route, ok := methods[method]; ok {
		return routeMatch{route: route}
	}
	allow := make([]string, 0, len(methods))
	for allowedMethod := range methods {
		allow = append(allow, allowedMethod)
	}
	sort.Strings(allow)
	return routeMatch{allow: allow, code: CodeMethodNotAllowed}
}

func (r router) reverse(name string) (string, error) {
	path, ok := r.byName[name]
	if !ok {
		return "", &Error{Code: CodeReverseNotFound, Field: "name", Detail: "route name is not registered"}
	}
	return path, nil
}

func methodNotAllowedResponse(allow []string) Response {
	response := plainText(http.StatusMethodNotAllowed, "Method Not Allowed\n")
	response.header.Set("Allow", strings.Join(allow, ", "))
	return response
}

func decimal(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 20)
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}
