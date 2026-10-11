package admin

import (
	"strings"
	"unicode/utf8"
)

// ActionNotice describes the public success message for a selected-row action.
// Text contains exactly one {count}, replaced by the confirmed matched count.
// All other text is literal and HTML-escaped by the Site. A zero value selects
// the generic action message. Notices never contain callback or request data.
type ActionNotice struct {
	Tag  string
	Text string
}

const actionNoticePrefix = "action:"

func prepareActionNotice(path string, notice ActionNotice) (ActionNotice, error) {
	if notice == (ActionNotice{}) {
		return ActionNotice{Tag: "action", Text: "{count} object(s) changed by the action."}, nil
	}
	if !validSlug(notice.Tag) {
		return ActionNotice{}, &ConfigError{Path: path + ".tag", Code: "invalid"}
	}
	if len(notice.Text) > MaximumDisplayBytes || !utf8.ValidString(notice.Text) ||
		containsUnsafeControl(notice.Text) || strings.Count(notice.Text, "{count}") != 1 {
		return ActionNotice{}, &ConfigError{Path: path + ".text", Code: "invalid"}
	}
	return notice, nil
}

func (model registeredModel) actionNotice(code string) (ActionNotice, bool) {
	name, ok := strings.CutPrefix(code, actionNoticePrefix)
	if ok {
		for _, action := range model.actions {
			if action.name == name {
				return action.successNotice, true
			}
		}
	}
	return ActionNotice{}, false
}
