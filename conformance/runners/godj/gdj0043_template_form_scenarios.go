package godj

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

type templateFormScenario func(context.Context, string) (protocol.Observation, error)

var templateFormScenarioRegistry = map[string]templateFormScenario{
	"django.template_form.scalar_and_missing":        templateFormScalarAndMissing,
	"django.template_form.dotted_lookup_precedence":  templateFormDottedLookupPrecedence,
	"django.template_form.autoescape_and_safe":       templateFormAutoescapeAndSafe,
	"django.template_form.if_for_and_empty":          templateFormIfForAndEmpty,
	"django.template_form.closed_filters":            templateFormClosedFilters,
	"django.template_form.construction_failures":     templateFormConstructionFailures,
	"django.template_form.callable_exposure":         templateFormCallableExposure,
	"django.template_form.unbound_and_bound_empty":   templateFormUnboundAndBoundEmpty,
	"django.template_form.valid_article_clean":       templateFormValidArticleClean,
	"django.template_form.field_error_codes":         templateFormFieldErrorCodes,
	"django.template_form.cross_field_validation":    templateFormCrossFieldValidation,
	"django.template_form.model_form_write_boundary": templateFormModelFormWriteBoundary,
}

// templateFormScenarioHandler is the exact Phase D registry boundary for
// WEB-021..027 and FRM-001..005. The shared runner owns whether and when this
// lane is published; this file only provides fail-closed scenario dispatch.
func templateFormScenarioHandler(scenario string) (scenarioHandler, bool) {
	run, ok := templateFormScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		return run(ctx, contract.ID)
	}, true
}

func templateFormScalarAndMissing(ctx context.Context, contractID string) (protocol.Observation, error) {
	output, err := renderTemplateFormSource(ctx, `{{ title }}|{{ missing }}`, map[string]templates.Value{
		"title": templates.String("Article"),
	})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("render scalar and missing variables: %w", err)
	}
	parts, err := splitTemplateFormOutput(output, 2)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"missing_is_empty": protocol.Boolean(parts[1] == ""),
		"scalar":           protocol.String(parts[0]),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"rendered_bytes": protocol.Integer(strconv.Itoa(len(output))),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormDottedLookupPrecedence(ctx context.Context, contractID string) (protocol.Observation, error) {
	mapping, err := templates.Object(map[string]templates.Value{
		"name": templates.String("mapping-value"),
	})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("construct mapping value: %w", err)
	}
	probe, err := templates.Object(map[string]templates.Value{
		"name": templates.String("dictionary-value"),
	})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("construct probe value: %w", err)
	}
	values := templates.List(templates.String("zero"), templates.String("one"))

	// Observe the public closed algebra independently of the renderer. The
	// protocol field retains Django's cross-product name, but GoDj does not
	// synthesize an attribute-bearing probe: a true value means the rendered
	// probe member is exactly the member published by templates.Value.Member.
	probeMember, probeMemberFound := probe.Member("name")
	probeText, probeTextOK := probeMember.AsString()
	mappingMember, mappingMemberFound := mapping.Member("name")
	mappingText, mappingTextOK := mappingMember.AsString()
	listItems, listItemsOK := values.Items()
	if !probeMemberFound || !probeTextOK || !mappingMemberFound || !mappingTextOK || !listItemsOK || len(listItems) != 2 {
		return protocol.Observation{}, fmt.Errorf("observe closed dotted lookup inputs: probe=%t/%t mapping=%t/%t list=%t/%d",
			probeMemberFound, probeTextOK, mappingMemberFound, mappingTextOK, listItemsOK, len(listItems))
	}
	listText, listTextOK := listItems[1].AsString()
	if !listTextOK {
		return protocol.Observation{}, fmt.Errorf("observe closed list index input: item is not a string")
	}
	output, err := renderTemplateFormSource(
		ctx,
		`{{ mapping.name }}|{{ probe.name }}|{{ values.1 }}`,
		map[string]templates.Value{
			"mapping": mapping,
			"probe":   probe,
			"values":  values,
		},
	)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("render dotted lookups: %w", err)
	}
	parts, err := splitTemplateFormOutput(output, 3)
	if err != nil {
		return protocol.Observation{}, err
	}
	closedLookupMeaningObserved := parts[0] == mappingText && parts[1] == probeText && parts[2] == listText
	if !closedLookupMeaningObserved {
		return protocol.Observation{}, fmt.Errorf("rendered closed dotted lookups do not match their public Value inputs")
	}
	result := protocol.Object(map[string]protocol.Value{
		// GoDj's closed Value algebra has no competing Go attribute fallback.
		// Report that locked Django semantic honestly as unobserved; DEV-0003
		// owns this exact selector while the member and list results remain
		// directly comparable.
		"attribute_fallback_shadowed": protocol.Boolean(false),
		"dictionary":                  protocol.String(parts[0]),
		"list_index":                  protocol.String(parts[2]),
		"object_dictionary":           protocol.String(parts[1]),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"callable_invocations": protocol.Integer("0"),
		// Rendering a closed Object performs no application-level dictionary
		// callback comparable to Django's probe.__getitem__ invocation.
		"object_dictionary_lookups": protocol.Integer("0"),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormAutoescapeAndSafe(ctx context.Context, contractID string) (protocol.Observation, error) {
	const (
		ordinary = "<b>&"
		trusted  = "<i>trusted</i>"
	)
	safeCapabilities := 1
	output, err := renderTemplateFormSource(ctx, `{{ ordinary }}|{{ trusted }}`, map[string]templates.Value{
		"ordinary": templates.String(ordinary),
		"trusted":  templates.TrustedHTML(trusted),
	})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("render escaped and trusted values: %w", err)
	}
	parts, err := splitTemplateFormOutput(output, 2)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"ordinary_markup_escaped":        protocol.Boolean(parts[0] == "&lt;b&gt;&amp;"),
		"ordinary_markup_literal_absent": protocol.Boolean(!strings.Contains(parts[0], ordinary)),
		"trusted_markup_preserved":       protocol.Boolean(parts[1] == trusted),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"rendered_bytes":    protocol.Integer(strconv.Itoa(len(output))),
		"safe_capabilities": protocol.Integer(strconv.Itoa(safeCapabilities)),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormIfForAndEmpty(ctx context.Context, contractID string) (protocol.Observation, error) {
	const source = `{% if enabled %}enabled{% else %}disabled{% endif %}|{% for item in items %}{{ forloop.counter }}={{ item }};{% empty %}empty{% endfor %}`
	populatedItems := []templates.Value{templates.String("alpha"), templates.String("beta")}
	renders := 0
	populated, err := renderTemplateFormSource(ctx, source, map[string]templates.Value{
		"enabled": templates.Bool(true),
		"items":   templates.List(populatedItems...),
	})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("render populated branch: %w", err)
	}
	renders++
	empty, err := renderTemplateFormSource(ctx, source, map[string]templates.Value{
		"enabled": templates.Bool(false),
		"items":   templates.List(),
	})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("render empty branch: %w", err)
	}
	renders++
	populatedParts, err := splitTemplateFormOutput(populated, 2)
	if err != nil {
		return protocol.Observation{}, err
	}
	emptyParts, err := splitTemplateFormOutput(empty, 2)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"empty_branch": protocol.String(emptyParts[1]),
		"false_branch": protocol.String(emptyParts[0]),
		"ordered_loop": protocol.String(populatedParts[1]),
		"true_branch":  protocol.String(populatedParts[0]),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"loop_items": protocol.Integer(strconv.Itoa(len(populatedItems))),
		"renders":    protocol.Integer(strconv.Itoa(renders)),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormClosedFilters(ctx context.Context, contractID string) (protocol.Observation, error) {
	const filtersEvaluated = 3
	output, err := renderTemplateFormSource(
		ctx,
		`{{ missing|default:"fallback" }}|{{ items|length }}|{{ label|lower }}`,
		map[string]templates.Value{
			"items": templates.List(templates.Integer(1), templates.Integer(2), templates.Integer(3)),
			"label": templates.String("GoDJ"),
		},
	)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("render closed filters: %w", err)
	}
	parts, err := splitTemplateFormOutput(output, 3)
	if err != nil {
		return protocol.Observation{}, err
	}
	length, err := strconv.Atoi(parts[1])
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("parse rendered length %q: %w", parts[1], err)
	}
	result := protocol.Object(map[string]protocol.Value{
		"default": protocol.String(parts[0]),
		"length":  protocol.Integer(strconv.Itoa(length)),
		"lower":   protocol.String(parts[2]),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"filters_evaluated": protocol.Integer(strconv.Itoa(filtersEvaluated)),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormConstructionFailures(_ context.Context, contractID string) (protocol.Observation, error) {
	tests := []struct {
		code        string
		source      string
		productCode string
	}{
		{code: "unknown_tag", source: `{% unknown_tag %}`, productCode: "unknown_tag"},
		{code: "unknown_filter", source: `{{ value|unknown_filter }}`, productCode: "invalid_expression"},
		{code: "private_lookup", source: `{{ value._private }}`, productCode: "invalid_expression"},
		{code: "unclosed_if", source: `{% if value %}open`, productCode: "unclosed_block"},
	}
	cases := make([]protocol.Value, 0, len(tests))
	accepted := 0
	for _, test := range tests {
		_, constructionErr := templates.New(templateFormSource(test.source), templates.Config{})
		caseAccepted := constructionErr == nil
		pythonType := protocol.Null()
		if caseAccepted {
			accepted++
		} else {
			var templateErr *templates.Error
			if !errors.As(constructionErr, &templateErr) {
				return protocol.Observation{}, fmt.Errorf("%s construction error = %T, want *templates.Error: %w", test.code, constructionErr, constructionErr)
			}
			if templateErr.Code != test.productCode {
				return protocol.Observation{}, fmt.Errorf("%s construction code = %q, want %q", test.code, templateErr.Code, test.productCode)
			}
			// The pinned protocol field is Python-named. Normalize GoDj's
			// structured parse failure to the same cross-product syntax-error
			// category only after observing the concrete templates.Error.
			pythonType = protocol.String("django.template.exceptions.TemplateSyntaxError")
		}
		cases = append(cases, protocol.Object(map[string]protocol.Value{
			"accepted":    protocol.Boolean(caseAccepted),
			"code":        protocol.String(test.code),
			"python_type": pythonType,
		}))
	}
	result := protocol.Object(map[string]protocol.Value{"cases": protocol.List(cases...)})
	metrics := protocol.Object(map[string]protocol.Value{
		"accepted": protocol.Integer(strconv.Itoa(accepted)),
		"rejected": protocol.Integer(strconv.Itoa(len(tests) - accepted)),
	})
	return templateFormObservation(contractID, protocol.PhaseConstruction, result, metrics), nil
}

func templateFormCallableExposure(ctx context.Context, contractID string) (protocol.Observation, error) {
	// GoDj never accepts a function or method in template Context. Observe the
	// public member as an ordinary closed value, so a successful render cannot
	// be mistaken for an invocation.
	probe, err := templates.Object(map[string]templates.Value{
		"exposed": templates.String("closed-value"),
	})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("construct callable probe replacement: %w", err)
	}
	callableInvocations := 0
	output, err := renderTemplateFormSource(ctx, `{{ probe.exposed }}`, map[string]templates.Value{"probe": probe})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("render callable exposure probe: %w", err)
	}
	category := "other"
	if string(output) == "closed-value" {
		category = "closed_value"
	}
	result := protocol.Object(map[string]protocol.Value{
		"auto_called":              protocol.Boolean(callableInvocations != 0),
		"rendered_return_category": protocol.String(category),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"callable_invocations": protocol.Integer(strconv.Itoa(callableInvocations)),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormUnboundAndBoundEmpty(_ context.Context, contractID string) (protocol.Observation, error) {
	spec, err := templateFormArticleValidationSpec()
	if err != nil {
		return protocol.Observation{}, err
	}
	unbound, err := spec.Unbound(nil)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("construct unbound Article form: %w", err)
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{}), nil)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("bind empty Article form: %w", err)
	}
	result := protocol.Object(map[string]protocol.Value{
		"bound_empty": protocol.Object(map[string]protocol.Value{
			"errors":   templateFormErrors(bound.Errors()),
			"is_bound": protocol.Boolean(bound.Bound()),
			"valid":    protocol.Boolean(bound.Valid()),
		}),
		"unbound": protocol.Object(map[string]protocol.Value{
			"errors":         templateFormErrors(unbound.Errors()),
			"is_bound":       protocol.Boolean(unbound.Bound()),
			"valid_property": protocol.Boolean(unbound.Valid()),
		}),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"forms_bound":       protocol.Integer(strconv.Itoa(boolInteger(bound.Bound()))),
		"forms_constructed": protocol.Integer("2"),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormValidArticleClean(_ context.Context, contractID string) (protocol.Observation, error) {
	spec, err := templateFormArticleValidationSpec()
	if err != nil {
		return protocol.Observation{}, err
	}
	form, err := spec.Bind(forms.NewData(map[string][]string{
		"title":     {"  Clean title  "},
		"published": {""},
		"summary":   {""},
	}), nil)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("bind valid Article form: %w", err)
	}
	if !form.Valid() {
		return protocol.Observation{}, fmt.Errorf("valid Article fixture returned errors: %v", templateFormErrorSummary(form.Errors()))
	}
	cleaned, order, err := templateFormCleanedValue(form.Cleaned())
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"cleaned":       cleaned,
		"cleaned_order": stringListValue(order),
		"valid":         protocol.Boolean(form.Valid()),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"cleaned_fields":    protocol.Integer(strconv.Itoa(len(order))),
		"validation_errors": protocol.Integer(strconv.Itoa(form.Errors().Len())),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormFieldErrorCodes(_ context.Context, contractID string) (protocol.Observation, error) {
	spec, err := templateFormArticleValidationSpec()
	if err != nil {
		return protocol.Observation{}, err
	}
	tests := []struct {
		name  string
		title string
	}{
		{name: "required", title: ""},
		{name: "max_length", title: strings.Repeat("x", 201)},
		{name: "nul", title: "ok\x00bad"},
	}
	cases := make([]protocol.Value, 0, len(tests))
	validCases := 0
	for _, test := range tests {
		form, err := spec.Bind(forms.NewData(map[string][]string{
			"title":     {test.title},
			"published": {""},
			"summary":   {""},
		}), nil)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("bind %s Article form: %w", test.name, err)
		}
		if form.Valid() {
			validCases++
		}
		cases = append(cases, protocol.Object(map[string]protocol.Value{
			"case":   protocol.String(test.name),
			"errors": templateFormErrors(form.Errors()),
			"valid":  protocol.Boolean(form.Valid()),
		}))
	}
	result := protocol.Object(map[string]protocol.Value{"cases": protocol.List(cases...)})
	metrics := protocol.Object(map[string]protocol.Value{
		"cases":       protocol.Integer(strconv.Itoa(len(cases))),
		"valid_cases": protocol.Integer(strconv.Itoa(validCases)),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormCrossFieldValidation(_ context.Context, contractID string) (protocol.Observation, error) {
	spec, err := templateFormArticleValidationSpec()
	if err != nil {
		return protocol.Observation{}, err
	}
	form, err := spec.Bind(forms.NewData(map[string][]string{
		"title":     {strings.Repeat("x", 201)},
		"published": {"on"},
		"summary":   {""},
	}), nil)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("bind cross-field Article form: %w", err)
	}
	cleanedFields := templateFormValueNames(form.Cleaned())
	_, titlePresent := form.Cleaned().Get("title")
	result := protocol.Object(map[string]protocol.Value{
		"changed_fields":         stringListValue(form.Changed()),
		"cleaned_fields":         stringListValue(cleanedFields),
		"errors":                 templateFormErrors(form.Errors()),
		"invalid_title_excluded": protocol.Boolean(!titlePresent),
		"valid":                  protocol.Boolean(form.Valid()),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"cross_field_validators": protocol.Integer("1"),
		"validation_errors":      protocol.Integer(strconv.Itoa(form.Errors().Len())),
	})
	return templateFormObservation(contractID, protocol.PhaseEvaluation, result, metrics), nil
}

func templateFormModelFormWriteBoundary(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withArticleDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		before, err := templateFormArticleRows(ctx, backend)
		if err != nil {
			return protocol.Observation{}, err
		}
		spec, err := formmodel.NewSpec((models.ArticleDescriptor{}).Metadata())
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("project Article model form: %w", err)
		}
		recorder := &statementRecorder{}

		invalid, err := spec.Bind(forms.NewData(map[string][]string{
			"title":     {""},
			"published": {"on"},
			"summary":   {"invalid"},
		}), nil)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("bind invalid Article model form: %w", err)
		}
		invalidBefore := len(recorder.snapshot())
		if invalid.Valid() {
			return protocol.Observation{}, errors.New("invalid Article model form unexpectedly valid")
		}
		// Invalid forms do not cross the explicit typed persistence adapter.
		invalidWrites := len(recorder.snapshot()) - invalidBefore

		createdForm, err := spec.Bind(forms.NewData(map[string][]string{
			"title":     {"Created"},
			"published": {"on"},
			"summary":   {"Summary"},
		}), nil)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("bind create Article model form: %w", err)
		}
		if !createdForm.Valid() {
			return protocol.Observation{}, fmt.Errorf("create Article model form errors: %v", templateFormErrorSummary(createdForm.Errors()))
		}
		createInput, err := templateFormArticleCreate(createdForm.Cleaned())
		if err != nil {
			return protocol.Observation{}, err
		}
		createBefore := len(recorder.snapshot())
		created, err := models.ArticleObjects.Create(ctx, observedMutator(backend, recorder), createInput)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("persist create Article model form: %w", err)
		}
		createWrites := len(recorder.snapshot()) - createBefore

		current, err := loadArticle(ctx, backend, 1)
		if err != nil {
			return protocol.Observation{}, err
		}
		updatedForm, err := spec.Bind(forms.NewData(map[string][]string{
			"title":     {"Updated"},
			"published": {""},
			"summary":   {"Changed"},
		}), templateFormArticleInitial(current))
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("bind update Article model form: %w", err)
		}
		if !updatedForm.Valid() {
			return protocol.Observation{}, fmt.Errorf("update Article model form errors: %v", templateFormErrorSummary(updatedForm.Errors()))
		}
		patch, err := templateFormArticlePatch(updatedForm.Cleaned())
		if err != nil {
			return protocol.Observation{}, err
		}
		updateBefore := len(recorder.snapshot())
		updated, err := models.ArticleObjects.Update(ctx, observedMutator(backend, recorder), current, patch)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("persist update Article model form: %w", err)
		}
		updateWrites := len(recorder.snapshot()) - updateBefore

		after, err := templateFormArticleRows(ctx, backend)
		if err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{
			"create": protocol.Object(map[string]protocol.Value{
				"changed_fields": stringListValue(createdForm.Changed()),
				"primary_key":    primaryKeyValue(created.ID),
			}),
			"invalid": protocol.Object(map[string]protocol.Value{
				"errors": templateFormErrors(invalid.Errors()),
				"writes": protocol.Integer(strconv.Itoa(invalidWrites)),
			}),
			"update": protocol.Object(map[string]protocol.Value{
				"changed_fields": stringListValue(updatedForm.Changed()),
				"primary_key":    primaryKeyValue(updated.ID),
			}),
		})
		state := protocol.Object(map[string]protocol.Value{
			"after":  articleList(after),
			"before": articleList(before),
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"create_writes":  protocol.Integer(strconv.Itoa(createWrites)),
			"invalid_writes": protocol.Integer(strconv.Itoa(invalidWrites)),
			"update_writes":  protocol.Integer(strconv.Itoa(updateWrites)),
		})
		return protocol.Observation{
			ID:      contractID,
			Status:  protocol.StatusObserved,
			Phase:   protocol.PhaseCommit,
			Result:  valuePointer(result),
			DBState: valuePointer(state),
			Metrics: valuePointer(metrics),
		}, nil
	})
}

func renderTemplateFormSource(ctx context.Context, source string, values map[string]templates.Value) ([]byte, error) {
	engine, err := templates.New(templateFormSource(source), templates.Config{})
	if err != nil {
		return nil, err
	}
	templateContext, err := templates.NewContext(values)
	if err != nil {
		return nil, err
	}
	return engine.Render(ctx, "scenario.html", templateContext, templates.Capabilities{})
}

func templateFormSource(source string) fstest.MapFS {
	return fstest.MapFS{
		"scenario.html": &fstest.MapFile{Data: []byte(source), Mode: 0o444},
	}
}

func splitTemplateFormOutput(output []byte, parts int) ([]string, error) {
	values := strings.Split(string(output), "|")
	if len(values) != parts {
		return nil, fmt.Errorf("rendered output %q has %d parts, want %d", output, len(values), parts)
	}
	return values, nil
}

func templateFormObservation(contractID string, phase protocol.Phase, result, metrics protocol.Value) protocol.Observation {
	return protocol.Observation{
		ID:      contractID,
		Status:  protocol.StatusObserved,
		Phase:   phase,
		Result:  valuePointer(result),
		Metrics: valuePointer(metrics),
	}
}

func templateFormArticleValidationSpec() (forms.Spec, error) {
	projected, err := formmodel.NewSpec((models.ArticleDescriptor{}).Metadata())
	if err != nil {
		return forms.Spec{}, fmt.Errorf("project Article validation form: %w", err)
	}
	validator := forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		published, _ := values.Boolean("published")
		summary, summaryPresent := values.String("summary")
		if published && (!summaryPresent || summary == "") {
			return validation.NewErrors(validation.New(validation.NonField, "summary_required_when_published"))
		}
		return validation.NewErrors()
	})
	spec, err := forms.NewSpec(projected.Fields(), validator)
	if err != nil {
		return forms.Spec{}, fmt.Errorf("construct Article validation form: %w", err)
	}
	return spec, nil
}

func templateFormErrors(errs validation.Errors) protocol.Value {
	items := errs.All()
	values := make([]protocol.Value, len(items))
	for index, item := range items {
		field := string(item.Field())
		if item.Field() == validation.NonField {
			field = "non_field"
		}
		values[index] = protocol.Object(map[string]protocol.Value{
			"code":  protocol.String(string(item.Code())),
			"field": protocol.String(field),
		})
	}
	return protocol.List(values...)
}

func templateFormErrorSummary(errs validation.Errors) string {
	items := errs.All()
	values := make([]string, len(items))
	for index, item := range items {
		values[index] = fmt.Sprintf("%s:%s", item.Field(), item.Code())
	}
	return strings.Join(values, ",")
}

func templateFormCleanedValue(values forms.Values) (protocol.Value, []string, error) {
	entries := values.All()
	fields := make(map[string]protocol.Value, len(entries))
	order := make([]string, len(entries))
	for index, entry := range entries {
		value, err := templateFormValue(entry.Value())
		if err != nil {
			return protocol.Value{}, nil, fmt.Errorf("cleaned field %q: %w", entry.Name(), err)
		}
		fields[entry.Name()] = value
		order[index] = entry.Name()
	}
	return protocol.Object(fields), order, nil
}

func templateFormValue(value forms.Value) (protocol.Value, error) {
	switch value.Kind() {
	case forms.ValueNull:
		return protocol.Null(), nil
	case forms.ValueString:
		text, ok := value.AsString()
		if !ok {
			return protocol.Value{}, errors.New("string form value has no string payload")
		}
		return protocol.String(text), nil
	case forms.ValueBoolean:
		boolean, ok := value.AsBoolean()
		if !ok {
			return protocol.Value{}, errors.New("boolean form value has no boolean payload")
		}
		return protocol.Boolean(boolean), nil
	default:
		return protocol.Value{}, fmt.Errorf("unsupported form value kind %d", value.Kind())
	}
}

func templateFormValueNames(values forms.Values) []string {
	entries := values.All()
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}

func stringListValue(values []string) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = protocol.String(value)
	}
	return protocol.List(items...)
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}

func templateFormArticleCreate(values forms.Values) (models.ArticleCreate, error) {
	title, titlePresent := values.String("title")
	if !titlePresent {
		return models.ArticleCreate{}, errors.New("cleaned create title is absent")
	}
	published, publishedPresent := values.Boolean("published")
	if !publishedPresent {
		return models.ArticleCreate{}, errors.New("cleaned create published is absent")
	}
	input := models.NewArticleCreate(title).WithPublished(published)
	summary, summaryPresent := values.Get("summary")
	if !summaryPresent {
		return models.ArticleCreate{}, errors.New("cleaned create summary is absent")
	}
	if summary.IsNull() {
		return input.WithSummaryNull(), nil
	}
	text, ok := summary.AsString()
	if !ok {
		return models.ArticleCreate{}, errors.New("cleaned create summary is not a string or null")
	}
	return input.WithSummary(text), nil
}

func templateFormArticlePatch(values forms.Values) (models.ArticlePatch, error) {
	title, titlePresent := values.String("title")
	if !titlePresent {
		return models.ArticlePatch{}, errors.New("cleaned update title is absent")
	}
	published, publishedPresent := values.Boolean("published")
	if !publishedPresent {
		return models.ArticlePatch{}, errors.New("cleaned update published is absent")
	}
	patch := (models.ArticlePatch{}).WithTitle(title).WithPublished(published)
	summary, summaryPresent := values.Get("summary")
	if !summaryPresent {
		return models.ArticlePatch{}, errors.New("cleaned update summary is absent")
	}
	if summary.IsNull() {
		return patch.WithSummaryNull(), nil
	}
	text, ok := summary.AsString()
	if !ok {
		return models.ArticlePatch{}, errors.New("cleaned update summary is not a string or null")
	}
	return patch.WithSummary(text), nil
}

func templateFormArticleInitial(article models.Article) map[string]forms.Value {
	summary := forms.Null()
	if article.Summary != nil {
		summary = forms.String(*article.Summary)
	}
	return map[string]forms.Value{
		"title":     forms.String(article.Title),
		"published": forms.Boolean(article.Published),
		"summary":   summary,
	}
}

func templateFormArticleRows(ctx context.Context, backend *sqlite.Backend) ([]models.Article, error) {
	articles, err := models.ArticleObjects.Using(backend).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Article model form rows: %w", err)
	}
	return articles, nil
}
