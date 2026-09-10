package admin

import (
	"fmt"
	"io"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/web"
)

type inputRules map[string]int

func parseSiteQuery(request *web.Request, rules inputRules) (url.Values, error) {
	httpRequest := request.HTTP()
	if httpRequest == nil || httpRequest.URL == nil {
		return nil, &ConfigError{Path: "request", Code: "invalid"}
	}
	if len(httpRequest.URL.RawQuery) > MaximumQueryBytes {
		return nil, &ConfigError{Path: "request.query", Code: "limit_exceeded"}
	}
	return parseSiteValues(httpRequest.URL.RawQuery, rules)
}

func parseSiteForm(request *web.Request, rules inputRules) (url.Values, error) {
	httpRequest := request.HTTP()
	if httpRequest == nil || httpRequest.Body == nil {
		return nil, &ConfigError{Path: "request.body", Code: "invalid"}
	}
	mediaType, _, err := mime.ParseMediaType(httpRequest.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return nil, &ConfigError{Path: "request.content_type", Code: "invalid"}
	}
	if httpRequest.ContentLength > MaximumFormBodyBytes {
		return nil, &ConfigError{Path: "request.body", Code: "limit_exceeded"}
	}
	body, err := io.ReadAll(io.LimitReader(httpRequest.Body, MaximumFormBodyBytes+1))
	if err != nil {
		return nil, &ConfigError{Path: "request.body", Code: "read_failed", Cause: err}
	}
	if len(body) > MaximumFormBodyBytes {
		return nil, &ConfigError{Path: "request.body", Code: "limit_exceeded"}
	}
	return parseSiteValues(string(body), rules)
}

func parseSiteValues(encoded string, rules inputRules) (url.Values, error) {
	values, err := url.ParseQuery(encoded)
	if err != nil {
		return nil, &ConfigError{Path: "request.values", Code: "malformed", Cause: err}
	}
	count := 0
	for name, submitted := range values {
		maximum, allowed := rules[name]
		formText := maximum < 0
		if formText {
			maximum = -maximum
		}
		if !allowed || maximum < 1 || len(submitted) > maximum {
			return nil, &ConfigError{Path: "request.values." + name, Code: "invalid_count"}
		}
		count += len(submitted)
		if count > MaximumInputValues {
			return nil, &ConfigError{Path: "request.values", Code: "limit_exceeded"}
		}
		for _, value := range submitted {
			if len(value) > MaximumInputBytes || !formText && (!utf8.ValidString(value) || strings.ContainsRune(value, 0)) {
				return nil, &ConfigError{Path: "request.values." + name, Code: "invalid_value"}
			}
		}
	}
	return values, nil
}

func modelFormRules(model registeredModel) inputRules {
	rules := make(inputRules, len(model.form.Fields())+1)
	for _, field := range model.form.Fields() {
		// Scalar duplicate handling belongs to forms so the user receives the
		// stable ordered "multiple" violation instead of a parser-level 400.
		// A negative internal limit keeps invalid UTF-8/NUL in model field raw
		// data so Forms can publish its stable ordered validation violations.
		// Display projection is separately sanitized before template rendering.
		rules[field.Name()] = -MaximumInputValues
	}
	// VerifyCSRF receives the complete slice and owns duplicate rejection.
	rules["csrfmiddlewaretoken"] = MaximumInputValues
	return rules
}

func modelData(model registeredModel, values url.Values) forms.Data {
	data := make(map[string][]string, len(model.form.Fields()))
	for _, field := range model.form.Fields() {
		if submitted, ok := values[field.Name()]; ok {
			data[field.Name()] = append([]string(nil), submitted...)
		}
	}
	return forms.NewData(data)
}

func exactValue(values url.Values, name string) (string, bool) {
	submitted, ok := values[name]
	returnValue := ""
	if ok && len(submitted) == 1 {
		returnValue = submitted[0]
		return returnValue, true
	}
	return "", false
}

func positiveID(values url.Values, name string) (int64, error) {
	raw, ok := exactValue(values, name)
	if !ok || raw == "" {
		return 0, &ConfigError{Path: "request.values." + name, Code: "required"}
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, &ConfigError{Path: "request.values." + name, Code: "invalid"}
	}
	return id, nil
}

func pageOffset(values url.Values, limit int) (int, int, bool) {
	raw, exists := exactValue(values, "p")
	if !exists || raw == "" {
		return 1, 0, false
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 1 || strconv.FormatInt(parsed, 10) != raw || limit < 1 ||
		parsed-1 > int64(MaximumListOffset/limit) {
		return 1, 0, true
	}
	page := int(parsed)
	return page, (page - 1) * limit, false
}

func selectedIDs(values url.Values) ([]int64, error) {
	raw := values["selected"]
	if len(raw) == 0 || len(raw) > MaximumSelectedIDs {
		return nil, &ConfigError{Path: "request.values.selected", Code: "invalid_count"}
	}
	result := make([]int64, len(raw))
	for index, value := range raw {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != value {
			return nil, &ConfigError{Path: fmt.Sprintf("request.values.selected[%d]", index), Code: "invalid"}
		}
		result[index] = parsed
	}
	return result, nil
}
