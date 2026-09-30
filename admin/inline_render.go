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
			value, err := inlineRowContext(entry, row, entry.identities[row.Index()], entry.revisions[row.Index()])
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
		empty := templates.List()
		if entry.empty != nil {
			prototype, prototypeErr := inlineRowContext(entry, *entry.empty, forms.Null(), 0)
			err = prototypeErr
			empty = templates.List(prototype)
			if err != nil {
				return templates.Value{}, err
			}
		}
		value, err := templateObject(map[string]templates.Value{"prefix": templates.String(entry.definition.prefix), "label": templates.String(entry.definition.label), "total": templates.Integer(int64(entry.set.TotalForms())), "initial": templates.Integer(int64(entry.set.InitialForms())), "rows": templates.List(rows...), "errors": templates.List(errors...), "can_add": templates.Bool(entry.empty != nil), "empty": empty, "minimum": templates.Integer(int64(entry.policy.MinForms)), "maximum": templates.Integer(int64(min(entry.policy.MaxForms, entry.policy.AbsoluteMax)))})
		if err != nil {
			return templates.Value{}, err
		}
		groups = append(groups, value)
	}
	return templates.List(groups...), nil
}

func inlineRowContext(entry inlineBound, row forms.SetForm, identityValue forms.Value, revisionValue int64) (templates.Value, error) {
	index, number := "__prefix__", "__prefix__"
	if row.Index() >= 0 {
		index = strconv.Itoa(row.Index())
		number = strconv.Itoa(row.Index() + 1)
	}
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
	if key, present := identityValue.AsInteger(); present {
		identity = strconv.FormatInt(key, 10)
	}
	revision := ""
	if revisionValue != 0 {
		revision = strconv.FormatInt(revisionValue, 10)
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
		"index": templates.String(index), "number": templates.String(number), "prefix": templates.String(row.Prefix()),
		"identity_name": templates.String(row.Prefix() + "-" + entry.definition.primary), "identity": templates.String(identity),
		"has_revision": templates.Bool(entry.definition.revision != ""), "revision": templates.String(revision),
		"readonly": templates.Bool(row.Form().ReadOnly()), "fields": templates.List(values...), "errors": templates.List(errors...),
		"can_remove": templates.Bool(entry.access.Add && (row.Index() < 0 || row.Index() >= entry.set.InitialForms())),
		"can_order":  templates.Bool(canOrder), "order": templates.String(order), "can_delete": templates.Bool(canDelete), "deleted": templates.Bool(row.DeletionRequested()),
	})
	if err != nil {
		return templates.Value{}, err
	}
	return value, nil
}

func addInlineContext(context map[string]templates.Value, submission InlineSubmission) error {
	value, err := inlineContext(submission)
	if err == nil {
		context["inlines"] = value
		for _, entry := range submission.entries {
			if entry.access.Add || entry.access.Change {
				for _, field := range entry.definition.fields {
					if field.IsFile() {
						context["multipart"] = templates.Bool(true)
					}
				}
			}
		}
	}
	return err
}
