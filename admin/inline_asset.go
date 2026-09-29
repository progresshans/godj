package admin

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net/http"

	"github.com/progresshans/godj/web"
)

//go:embed site_assets/inlines.js
var inlineScriptBytes []byte

func (site *Site) hasInlines() bool {
	for _, model := range site.registry.models {
		if len(model.inlines) > 0 {
			return true
		}
	}
	return false
}

func (site *Site) inlineScriptPath() string {
	return fmt.Sprintf("%s/assets/inlines.%x.js", site.basePath, sha256.Sum256(inlineScriptBytes))
}

// This public, immutable asset contains no request data and is deliberately
// excluded from authentication's allowed post-login destinations.
func (site *Site) inlineScript(request *web.Request) (web.Response, error) {
	if _, err := parseSiteQuery(request, inputRules{}); err != nil {
		return siteBadRequest()
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "text/javascript; charset=utf-8")
	headers.Set("Cache-Control", "public, max-age=31536000, immutable")
	headers.Set("X-Content-Type-Options", "nosniff")
	setSiteFramingHeaders(headers)
	return web.NewResponse(http.StatusOK, headers, inlineScriptBytes)
}
