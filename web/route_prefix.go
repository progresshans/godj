package web

import "strings"

// RoutePrefixDescription describes the overlap between a route's accepted paths
// and one static subtree prefix. Some reports that at least one path has the
// prefix; All reports that every accepted path does. All therefore implies Some.
// Both false means there is no overlap.
type RoutePrefixDescription struct {
	All  bool
	Some bool
}

// DescribeRoutePrefix uses the routing compiler and canonical int64 matcher to
// classify a clean, absolute, static prefix ending in slash. The root prefix /
// covers every route. A trailing slash is significant: /items/ does not cover
// /items, and /items/1/ does not cover /items/<int64:id> without its final slash.
// No request is routed, no handler is called, and neither argument is normalized.
func DescribeRoutePrefix(routePath, prefix string) (RoutePrefixDescription, error) {
	pattern, err := compileRoutePath(routePath)
	if err != nil {
		return RoutePrefixDescription{}, err
	}
	prefixPattern, err := compileRoutePath(prefix)
	if err != nil {
		return RoutePrefixDescription{}, &Error{Code: CodeInvalidRoute, Field: "prefix", Detail: "prefix must be a canonical static path ending in slash", Cause: err}
	}
	if prefixPattern != nil || !strings.HasSuffix(prefix, "/") {
		return RoutePrefixDescription{}, &Error{Code: CodeInvalidRoute, Field: "prefix", Detail: "prefix must be a canonical static path ending in slash"}
	}
	if pattern == nil {
		matches := strings.HasPrefix(routePath, prefix)
		return RoutePrefixDescription{All: matches, Some: matches}, nil
	}
	prefixSegments := strings.Split(prefix, "/")
	// Keep the prefix's final empty segment for this length check. It requires
	// an actual slash after the last constrained segment, even on an exact path.
	if len(prefixSegments) > len(pattern.segments) {
		return RoutePrefixDescription{}, nil
	}
	constrained := len(prefixSegments) - 1
	all := true
	minimumPathBytes := len(pattern.segments) - 1 // Separating slashes.
	for index, segment := range pattern.segments {
		if segment.kind == routeParameterInvalid {
			minimumPathBytes += len(segment.literal)
			if index < constrained && segment.literal != prefixSegments[index] {
				return RoutePrefixDescription{}, nil
			}
			continue
		}
		if index < constrained {
			if _, valid := parseCanonicalInt64(prefixSegments[index]); !valid {
				return RoutePrefixDescription{}, nil
			}
			minimumPathBytes += len(prefixSegments[index])
			all = false
		} else {
			minimumPathBytes++ // Zero is the shortest accepted parameter value.
		}
	}
	// A syntactically compatible prefix may force a long integer before a large
	// static suffix. Even its shortest completion must fit the router's limit.
	if minimumPathBytes > maximumRoutePathBytes {
		return RoutePrefixDescription{}, nil
	}
	return RoutePrefixDescription{All: all, Some: true}, nil
}
