package admin

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net/http"

	"github.com/progresshans/godj/web"
)

//go:embed site_assets/choices.js
var choiceScriptBytes []byte

func (site *Site) choiceScriptPath() string {
	return fmt.Sprintf("%s/assets/choices.%x.js", site.basePath, sha256.Sum256(choiceScriptBytes))
}

// The public immutable asset contains no request or relation data. It is not an
// allowed post-login destination; every search request is authorized separately.
func (site *Site) choiceScript(request *web.Request) (web.Response, error) {
	if _, err := parseSiteQuery(request, inputRules{}); err != nil {
		return siteBadRequest()
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "text/javascript; charset=utf-8")
	headers.Set("Cache-Control", "public, max-age=31536000, immutable")
	headers.Set("X-Content-Type-Options", "nosniff")
	setSiteFramingHeaders(headers)
	return web.NewResponse(http.StatusOK, headers, choiceScriptBytes)
}
