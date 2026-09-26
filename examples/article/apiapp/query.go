package apiapp

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/validation"
)

const (
	codeDuplicate     validation.Code = "duplicate"
	codeInvalid       validation.Code = "invalid"
	codeInvalidChoice validation.Code = "invalid_choice"
)

type listQuery struct {
	search           string
	searchSupplied   bool
	published        articleapp.PublishedFilter
	publishedText    string
	ordering         articleapp.IDOrdering
	orderingText     string
	orderingSupplied bool
	page             int
}

type queryEntry struct {
	name  string
	value string
}

func parseListQuery(raw string) (listQuery, validation.Errors, bool) {
	result := listQuery{page: 1}
	entries, malformed := parseQueryEntries(raw)
	if malformed {
		return result, validation.NewErrors(validation.New(validation.NonField, codeInvalid)), false
	}
	unknown := make([]string, 0)
	for _, entry := range entries {
		if !allowedQueryName(entry.name) {
			unknown = append(unknown, entry.name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		diagnostics := validation.NewErrors()
		for index, name := range unknown {
			if index > 0 && name == unknown[index-1] {
				continue
			}
			diagnostics = diagnostics.Append(validation.NewErrors(validation.New(validation.Field(name), serializersUnknownCode)))
		}
		return result, diagnostics, false
	}

	for _, name := range []string{"search", "published", "ordering", "page"} {
		values := queryValues(entries, name)
		if len(values) > 1 {
			return result, validation.NewErrors(validation.New(validation.Field(name), codeDuplicate)), false
		}
		if len(values) == 0 {
			continue
		}
		value := values[0]
		switch name {
		case "search":
			if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || len(value) > maximumSearchBytes {
				return result, validation.NewErrors(validation.New(
					validation.Field(name),
					serializersMaxLengthCode,
					validation.NewParam("max_length", strconv.Itoa(maximumSearchBytes)),
				)), false
			}
			result.search = value
			result.searchSupplied = true
		case "published":
			switch value {
			case "true":
				result.published = articleapp.PublishedOnly
				result.publishedText = value
			case "false":
				result.published = articleapp.UnpublishedOnly
				result.publishedText = value
			default:
				return result, validation.NewErrors(validation.New(validation.Field(name), codeInvalid)), false
			}
		case "ordering":
			switch value {
			case "id":
				result.ordering = articleapp.IDAscending
				result.orderingText = value
			case "-id":
				result.ordering = articleapp.IDDescending
				result.orderingText = value
			default:
				return result, validation.NewErrors(validation.New(validation.Field(name), codeInvalidChoice)), false
			}
			result.orderingSupplied = true
		case "page":
			if value == "" {
				continue
			}
			page, ok := positivePage(value)
			if !ok {
				return result, validation.NewErrors(), true
			}
			result.page = page
		}
	}
	return result, validation.NewErrors(), false
}

func parseQueryEntries(raw string) ([]queryEntry, bool) {
	if raw == "" {
		return nil, false
	}
	if len(raw) > maximumQueryBytes {
		return nil, true
	}
	parts := strings.Split(raw, "&")
	entries := make([]queryEntry, 0, len(parts))
	for _, part := range parts {
		name, value, _ := strings.Cut(part, "=")
		name, err := url.QueryUnescape(name)
		if err != nil || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
			return nil, true
		}
		value, err = url.QueryUnescape(value)
		if err != nil || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return nil, true
		}
		entries = append(entries, queryEntry{name: name, value: value})
	}
	return entries, false
}

func allowedQueryName(name string) bool {
	return name == "search" || name == "published" || name == "ordering" || name == "page"
}

func queryValues(entries []queryEntry, name string) []string {
	values := make([]string, 0, 1)
	for _, entry := range entries {
		if entry.name == name {
			values = append(values, entry.value)
		}
	}
	return values
}

func positivePage(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return 0, false
		}
	}
	page, err := strconv.ParseUint(value, 10, 64)
	maximumPage := uint64(int(^uint(0)>>1)/pageSize + 1)
	if err != nil || page == 0 || page > maximumPage {
		return 0, false
	}
	return int(page), true
}

func (q listQuery) options() articleapp.ListOptions {
	return articleapp.ListOptions{
		Search:      q.search,
		Published:   q.published,
		Ordering:    q.ordering,
		SearchScope: articleapp.SearchTitleOnly,
		Offset:      (q.page - 1) * pageSize,
		Limit:       pageSize,
	}
}

func (q listQuery) relativeURI(page int) string {
	parts := make([]string, 0, 4)
	if q.searchSupplied {
		parts = append(parts, "search="+url.QueryEscape(q.search))
	}
	if q.publishedText != "" {
		parts = append(parts, "published="+q.publishedText)
	}
	if q.orderingSupplied {
		parts = append(parts, "ordering="+url.QueryEscape(q.orderingText))
	}
	if page > 1 {
		parts = append(parts, "page="+strconv.Itoa(page))
	}
	if len(parts) == 0 {
		return ListPath
	}
	return ListPath + "?" + strings.Join(parts, "&")
}

// Keep query diagnostic codes aligned with the shared serializer contract
// without exporting a second vocabulary from this concrete adapter.
const (
	serializersUnknownCode   validation.Code = "unknown"
	serializersMaxLengthCode validation.Code = "max_length"
)
