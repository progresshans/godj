package identitytest

import (
	"regexp"
	"testing"
)

var accountCSRFPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`)
var accountErrorPattern = regexp.MustCompile(`data-error-field="([^"]*)" data-error-code="([^"]*)"`)

func accountCSRF(t *testing.T, body string) string {
	t.Helper()
	values := accountCSRFPattern.FindStringSubmatch(body)
	if len(values) != 2 {
		t.Fatal("account form lacks CSRF input")
	}
	return values[1]
}
func accountErrors(body string) map[string][]string {
	result := map[string][]string{}
	for _, match := range accountErrorPattern.FindAllStringSubmatch(body, -1) {
		result[match[1]] = append(result[match[1]], match[2])
	}
	return result
}
