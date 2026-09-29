package admin

import (
	"net/url"
	"strconv"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

func inlineContext(submission InlineSubmission) (templates.Value, error) {
	groups := make([]templates.Value, 0, len(submission.entries))
	for _, entry := range submission.entries {
		rows := make([]templates.Value, 0, entry.set.TotalForms())
		for _, row := range entry.set.Forms() {
			var fields []forms.Field
			canOrder, canDelete := false, false
			for _, field := range row.Fields() {
				switch field.Name() {
				case "ORDER":
					canOrder = true
				case "DELETE":
					canDelete = true
				default:
					fields = append(fields, field)
				}
			}
			submitted := url.Values{}
			for _, field := range fields {
				if values, present := row.Form().Submitted().Get(field.Name()); present {
					submitted[field.Name()] = values
				}
			}
			values, err := formFieldContext(fields, row.Form(), submitted, row.Prefix()+"-", false)
			if err != nil {
				return templates.Value{}, err
			}
			identity := ""
			if key, present := entry.identities[row.Index()].AsInteger(); present {
				identity = strconv.FormatInt(key, 10)
			}
			revision := ""
			if entry.revisions[row.Index()] != 0 {
				revision = strconv.FormatInt(entry.revisions[row.Index()], 10)
			}
			order := ""
			if row.Form().Bound() {
				if raw, present := row.Form().Submitted().Get("ORDER"); present && len(raw) > 0 {
					order = safeDisplayText(raw[0])
				}
			} else if number, present := row.Form().Initial().Integer("ORDER"); present {
				order = strconv.FormatInt(number, 10)
			}
			failures := validation.Join(row.Form().Errors().ByField(validation.NonField), row.Form().Errors().ByField("ORDER"), row.Form().Errors().ByField("DELETE"))
			errors, err := violationValues(failures)
			if err != nil {
				return templates.Value{}, err
			}
			value, err := templateObject(map[string]templates.Value{
				"index": templates.Integer(int64(row.Index())), "number": templates.Integer(int64(row.Index() + 1)), "prefix": templates.String(row.Prefix()),
				"identity_name": templates.String(row.Prefix() + "-" + entry.definition.primary), "identity": templates.String(identity),
				"has_revision": templates.Bool(entry.definition.revision != ""), "revision": templates.String(revision),
				"readonly": templates.Bool(row.Form().ReadOnly()), "fields": templates.List(values...), "errors": templates.List(errors...),
				"can_order": templates.Bool(canOrder), "order": templates.String(order), "can_delete": templates.Bool(canDelete), "deleted": templates.Bool(row.DeletionRequested()),
			})
			if err != nil {
				return templates.Value{}, err
			}
			rows = append(rows, value)
		}
		failures := validation.Join(entry.set.NonFormErrors(), entry.set.Management().Errors())
		errors, err := violationValues(failures)
		if err != nil {
			return templates.Value{}, err
		}
		value, err := templateObject(map[string]templates.Value{"prefix": templates.String(entry.definition.prefix), "label": templates.String(entry.definition.label), "total": templates.Integer(int64(entry.set.TotalForms())), "initial": templates.Integer(int64(entry.set.InitialForms())), "rows": templates.List(rows...), "errors": templates.List(errors...)})
		if err != nil {
			return templates.Value{}, err
		}
		groups = append(groups, value)
	}
	return templates.List(groups...), nil
}

func addInlineContext(context map[string]templates.Value, submission InlineSubmission) error {
	value, err := inlineContext(submission)
	if err == nil {
		context["inlines"] = value
	}
	return err
}
