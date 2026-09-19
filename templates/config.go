package templates

import (
	"context"
	"fmt"
)

// Limits bounds both startup parsing and each render. Zero fields select safe
// defaults; negative values are invalid startup configuration.
type Limits struct {
	MaxTemplates     int
	MaxTemplateBytes int
	MaxTotalBytes    int
	MaxParseNodes    int
	MaxParseDepth    int
	MaxRenderDepth   int
	MaxLoopItems     int
	MaxContextDepth  int
	MaxOutputBytes   int
}

func DefaultLimits() Limits {
	return Limits{
		MaxTemplates:     256,
		MaxTemplateBytes: 1 << 20,
		MaxTotalBytes:    8 << 20,
		MaxParseNodes:    16_384,
		MaxParseDepth:    64,
		MaxRenderDepth:   64,
		MaxLoopItems:     10_000,
		MaxContextDepth:  64,
		MaxOutputBytes:   1 << 20,
	}
}

type Config struct {
	Root   string
	Limits Limits
}

func normalizeLimits(input Limits) (Limits, error) {
	defaults := DefaultLimits()
	values := []*int{
		&input.MaxTemplates,
		&input.MaxTemplateBytes,
		&input.MaxTotalBytes,
		&input.MaxParseNodes,
		&input.MaxParseDepth,
		&input.MaxRenderDepth,
		&input.MaxLoopItems,
		&input.MaxContextDepth,
		&input.MaxOutputBytes,
	}
	fallback := []int{
		defaults.MaxTemplates,
		defaults.MaxTemplateBytes,
		defaults.MaxTotalBytes,
		defaults.MaxParseNodes,
		defaults.MaxParseDepth,
		defaults.MaxRenderDepth,
		defaults.MaxLoopItems,
		defaults.MaxContextDepth,
		defaults.MaxOutputBytes,
	}
	for index, value := range values {
		if *value < 0 {
			return Limits{}, fmt.Errorf("templates: limits[%d]: invalid", index)
		}
		if *value == 0 {
			*value = fallback[index]
		}
	}
	return input, nil
}

// URLResolver is an explicit, narrowly scoped template capability. It is not a
// general function registry and receives only a literal route name parsed at
// startup.
type URLResolver interface {
	Reverse(context.Context, string) (string, error)
}

// CSRFProvider provides the current request's already-generated form token.
type CSRFProvider interface {
	Token(context.Context) (string, error)
}

type Capabilities struct {
	URL  URLResolver
	CSRF CSRFProvider
}
