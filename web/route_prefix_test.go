package web_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/web"
)

func TestRoutePrefixClassifiesCompletePartialAndDisjointSubtrees(t *testing.T) {
	tests := []struct {
		name   string
		route  string
		prefix string
		all    bool
		some   bool
	}{
		{name: "root route", route: "/", prefix: "/", all: true, some: true},
		{name: "root prefix", route: "/api/items/<int64:id>", prefix: "/", all: true, some: true},
		{name: "static subtree", route: "/api/items/", prefix: "/api/", all: true, some: true},
		{name: "static exact", route: "/api/", prefix: "/api/", all: true, some: true},
		{name: "static sibling", route: "/apix/items/", prefix: "/api/"},
		{name: "static different", route: "/other/items/", prefix: "/api/"},
		{name: "static slash absent", route: "/api", prefix: "/api/"},
		{name: "prefix longer than static route", route: "/api/", prefix: "/api/items/"},
		{name: "all integer values", route: "/api/items/<int64:id>/", prefix: "/api/items/", all: true, some: true},
		{name: "all integer values without final slash", route: "/api/items/<int64:id>", prefix: "/api/items/", all: true, some: true},
		{name: "one integer value", route: "/api/items/<int64:id>/", prefix: "/api/items/1/", some: true},
		{name: "zero integer", route: "/api/items/<int64:id>/", prefix: "/api/items/0/", some: true},
		{name: "maximum integer", route: "/api/items/<int64:id>/", prefix: "/api/items/9223372036854775807/", some: true},
		{name: "integer slash absent", route: "/api/items/<int64:id>", prefix: "/api/items/1/"},
		{name: "prefix longer than parameter route", route: "/api/items/<int64:id>/", prefix: "/api/items/1/extra/"},
		{name: "parameter before static suffix", route: "/api/<int64:id>/items", prefix: "/api/1/", some: true},
		{name: "static suffix exact", route: "/api/<int64:id>/items/", prefix: "/api/1/items/", some: true},
		{name: "static suffix mismatch", route: "/api/<int64:id>/items/", prefix: "/api/1/other/"},
		{name: "static suffix slash absent", route: "/api/<int64:id>/items", prefix: "/api/1/items/"},
		{name: "two parameters unconstrained", route: "/api/<int64:id>/revisions/<int64:revision>/", prefix: "/api/", all: true, some: true},
		{name: "first parameter constrained", route: "/api/<int64:id>/revisions/<int64:revision>/", prefix: "/api/1/revisions/", some: true},
		{name: "both parameters constrained", route: "/api/<int64:id>/revisions/<int64:revision>/", prefix: "/api/1/revisions/0/", some: true},
		{name: "later static mismatch", route: "/api/<int64:id>/revisions/<int64:revision>/", prefix: "/api/1/other/0/"},
		{name: "static digits are not parameters", route: "/api/01/<int64:id>/", prefix: "/api/01/", all: true, some: true},
		{name: "static paths are not URL decoded", route: "/api/%31/items/", prefix: "/api/%31/", all: true, some: true},
		{name: "non-ASCII static subtree", route: "/항목/<int64:id>/", prefix: "/항목/", all: true, some: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			description, err := web.DescribeRoutePrefix(test.route, test.prefix)
			if err != nil || description.All != test.all || description.Some != test.some {
				t.Fatalf("DescribeRoutePrefix(%q, %q) = %#v, %v; want All=%t Some=%t", test.route, test.prefix, description, err, test.all, test.some)
			}
		})
	}
}

func TestRoutePrefixUsesCanonicalNonnegativeInt64Matching(t *testing.T) {
	for _, value := range []string{"01", "00", "-1", "-0", "+1", "1.0", "1e0", "9223372036854775808", "18446744073709551615", "100000000000000000000", "one", "１", "%31"} {
		t.Run(value, func(t *testing.T) {
			for _, prefix := range []string{"/api/" + value + "/", "/api/1/revisions/" + value + "/"} {
				description, err := web.DescribeRoutePrefix("/api/<int64:id>/revisions/<int64:revision>/", prefix)
				if err != nil || description.All || description.Some {
					t.Fatalf("noncanonical integer prefix %q = %#v, %v", prefix, description, err)
				}
			}
		})
	}
}

func TestRoutePrefixRejectsInvalidRouteAndNonstaticPrefix(t *testing.T) {
	for _, route := range []string{"", "relative/", "/api//items/", "/api/../items/", "/api/{id}/", "/api/<uuid:id>/", "/api/prefix<int64:id>/", "/<int64:id>/<int64:id>/", "/api/<int64:id>/bad\x00/"} {
		description, err := web.DescribeRoutePrefix(route, "/api/")
		if !errors.Is(err, &web.Error{Code: web.CodeInvalidRoute}) || description.All || description.Some {
			t.Fatalf("invalid route %q published a prefix description: %#v, %v", route, description, err)
		}
	}
	for _, prefix := range []string{"", "api/", "/api", "//", "/api//", "/api/../", "/api/./", "/api/<int64:id>/", "/api/{id}/", "/api/*/", "/api/?query", "/api/#fragment", "/api\\/", "/api/\x00/", "/api/\xff/", "/" + strings.Repeat("a", 4096) + "/", "/" + strings.Repeat("a/", 65)} {
		description, err := web.DescribeRoutePrefix("/api/<int64:id>/", prefix)
		if !errors.Is(err, &web.Error{Code: web.CodeInvalidRoute, Field: "prefix"}) || description.All || description.Some {
			t.Fatalf("invalid prefix %q published a description: %#v, %v", prefix, description, err)
		}
	}
}

func TestRoutePrefixAccountsForConcretePathByteLimit(t *testing.T) {
	const routeStart = "/api/<int64:id>/"
	const routeLimit = 4096
	// This accepted declaration has room for a one-digit value but not for a
	// maximum int64 followed by its fixed suffix. Prefixes themselves fit.
	route := routeStart + strings.Repeat("a", routeLimit-len(routeStart))
	for _, test := range []struct {
		prefix string
		all    bool
		some   bool
	}{
		{prefix: "/api/", all: true, some: true},
		{prefix: "/api/0/", some: true},
		{prefix: "/api/9223372036854775807/"},
	} {
		description, err := web.DescribeRoutePrefix(route, test.prefix)
		if err != nil || description.All != test.all || description.Some != test.some {
			t.Fatalf("long suffix with prefix %q = %#v, %v; want All=%t Some=%t", test.prefix, description, err, test.all, test.some)
		}
	}
	// At the exact concrete path byte limit, the same numeric prefix overlaps.
	const concreteStart = "/api/9223372036854775807/"
	route = routeStart + strings.Repeat("a", routeLimit-len(concreteStart))
	description, err := web.DescribeRoutePrefix(route, concreteStart)
	if err != nil || description.All || !description.Some {
		t.Fatalf("exact concrete path byte limit = %#v, %v", description, err)
	}
}
