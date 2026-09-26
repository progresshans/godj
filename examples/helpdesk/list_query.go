package helpdesk

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/validation"
)

const maximumTicketSearchBytes = 64

type ticketListQuery struct{ search, source string }

func parseTicketListQuery(raw string) (ticketListQuery, validation.Errors) {
	result := ticketListQuery{}
	invalid := func(path validation.Field, code validation.Code) (ticketListQuery, validation.Errors) {
		return ticketListQuery{}, validation.NewErrors(validation.New(path, code))
	}
	if len(raw) > 2048 {
		return invalid(validation.NonField, "invalid")
	}
	if raw == "" {
		return result, validation.NewErrors()
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, "&") {
		key, value, _ := strings.Cut(part, "=")
		key, err := url.QueryUnescape(key)
		if err != nil || (key != "search" && key != "source") {
			return invalid(validation.NonField, "unknown")
		}
		if seen[key] {
			return invalid(validation.Field(key), "duplicate")
		}
		seen[key] = true
		value, err = url.QueryUnescape(value)
		if err != nil || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return invalid(validation.Field(key), "invalid")
		}
		if len(value) > maximumTicketSearchBytes {
			return ticketListQuery{}, validation.NewErrors(validation.New(validation.Field(key), "max_length", validation.NewParam("max_length", strconv.Itoa(maximumTicketSearchBytes))))
		}
		if key == "search" {
			result.search = value
		} else {
			result.source = value
		}
	}
	return result, validation.NewErrors()
}
