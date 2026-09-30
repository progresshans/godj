package admin

import (
	"mime"
	"net/url"
	"strings"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/web"
)

type siteForm struct {
	values url.Values
	files  map[string][]uploads.File
}

func multipartRequest(request *web.Request) bool {
	if request.HTTP() == nil {
		return false
	}
	media, _, err := mime.ParseMediaType(request.HTTP().Header.Get("Content-Type"))
	return err == nil && media == "multipart/form-data"
}

// Before allocating upload resources, inspect admission without touching idle
// expiry or cleaning up a stale session. CSRF and final authorization still run
// after parsing; this early check is not authority to save anything.
func (site *Site) admitMultipart(request *web.Request, permission auth.Permission) (web.Response, bool, error) {
	if !multipartRequest(request) {
		return web.Response{}, false, nil
	}
	principal, err := site.auth.InspectPrincipal(request)
	if err != nil {
		return web.Response{}, true, err
	}
	admitted := false
	response, err := site.authorizePrincipal(request, principal, permission, func() (web.Response, error) {
		admitted = true
		return web.Response{}, nil
	})
	return response, !admitted, err
}

func (site *Site) parseModelForm(request *web.Request, model registeredModel, inlines ...Inline) (siteForm, error) {
	rules := modelFormRules(model)
	if !multipartRequest(request) {
		values, err := parseSiteForm(request, rules, inlines...)
		return siteForm{values: values}, err
	}
	parsed, err := request.Multipart(site.uploads)
	if err != nil {
		return siteForm{}, err
	}
	input := siteForm{values: parsed.Values(), files: parsed.Files()}
	if err := validateSiteValues(input.values, rules, inlines...); err != nil {
		return siteForm{}, err
	}
	count := 0
	for _, values := range input.values {
		count += len(values)
	}
	for name, files := range input.files {
		allowed := fileFieldNamed(model.form.Fields(), name)
		for _, inline := range inlines {
			if _, ok := inlineInputRule(inline, name); !ok {
				continue
			}
			_, field, found := strings.Cut(strings.TrimPrefix(name, inline.prefix+"-"), "-")
			allowed = allowed || found && fileFieldNamed(inline.fields, field)
		}
		if !allowed {
			return siteForm{}, &ConfigError{Path: "request.files", Code: "undeclared_field"}
		}
		count += len(files)
		if count > MaximumInputValues {
			return siteForm{}, &ConfigError{Path: "request.files", Code: "limit_exceeded"}
		}
	}
	return input, nil
}

func fileFieldNamed(fields []forms.Field, name string) bool {
	for _, field := range fields {
		if field.Name() == name && field.IsFile() {
			return true
		}
	}
	return false
}

// Only known direct input failures are client errors. Storage, cancellation,
// cleanup and policy/lifecycle failures remain execution errors.
func siteFormResponse(err error) (web.Response, error) {
	switch failure := err.(type) {
	case *ConfigError:
		if failure != nil && failure.Code != "read_failed" {
			return siteBadRequest()
		}
	case *uploads.Error:
		if failure != nil {
			switch failure.Code {
			case "invalid_content_type", "invalid_part", "invalid_filename", "malformed_body",
				"body_too_large", "file_too_large", "value_too_large", "too_many_parts", "too_many_files":
				return siteBadRequest()
			}
		}
	}
	return web.Response{}, err
}
