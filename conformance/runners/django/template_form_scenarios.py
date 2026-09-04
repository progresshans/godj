"""Secret-free Django observations for templates and Article form validation."""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from django import forms
from django.core.exceptions import NON_FIELD_ERRORS, ValidationError
from django.db import connection
from django.template import Context, Engine, TemplateSyntaxError
from django.utils.safestring import mark_safe

from .normalizer import PrimaryKey, normalize
from .scenarios import Article, article_database, configure_django


configure_django()


_ENGINE = Engine(debug=False)


def _observed(
    contract_id: str,
    result: Any,
    *,
    phase: str = "evaluation",
    db_state: Any | None = None,
    metrics: Any | None = None,
) -> dict[str, Any]:
    return {
        "db_state": normalize(db_state) if db_state is not None else None,
        "error": None,
        "id": contract_id,
        "metrics": normalize(metrics) if metrics is not None else None,
        "phase": phase,
        "result": normalize(result),
        "status": "observed",
    }


def _render(source: str, values: dict[str, Any]) -> str:
    return _ENGINE.from_string(source).render(Context(values, autoescape=True))


def scalar_and_missing(contract_id: str) -> dict[str, Any]:
    rendered = _render("{{ title }}|{{ missing }}", {"title": "Article"})
    scalar, missing = rendered.split("|", 1)
    return _observed(
        contract_id,
        {
            "missing_is_empty": missing == "",
            "scalar": scalar,
        },
        metrics={"rendered_bytes": len(rendered.encode("utf-8"))},
    )


class _LookupProbe:
    name = "attribute-value"

    def __init__(self) -> None:
        self.dictionary_lookups = 0

    def __getitem__(self, key: str) -> str:
        self.dictionary_lookups += 1
        if key != "name":
            raise KeyError(key)
        return "dictionary-value"


def dotted_lookup_precedence(contract_id: str) -> dict[str, Any]:
    probe = _LookupProbe()
    rendered = _render(
        "{{ mapping.name }}|{{ probe.name }}|{{ values.1 }}",
        {
            "mapping": {"name": "mapping-value"},
            "probe": probe,
            "values": ["zero", "one"],
        },
    )
    mapping_value, probe_value, list_value = rendered.split("|")
    return _observed(
        contract_id,
        {
            "attribute_fallback_shadowed": probe_value != _LookupProbe.name,
            "dictionary": mapping_value,
            "list_index": list_value,
            "object_dictionary": probe_value,
        },
        metrics={
            "callable_invocations": 0,
            "object_dictionary_lookups": probe.dictionary_lookups,
        },
    )


def autoescape_and_safe(contract_id: str) -> dict[str, Any]:
    unsafe = "<b>&"
    trusted = "<i>trusted</i>"
    rendered = _render(
        "{{ unsafe }}|{{ trusted }}",
        {"unsafe": unsafe, "trusted": mark_safe(trusted)},
    )
    unsafe_rendered, trusted_rendered = rendered.split("|", 1)
    return _observed(
        contract_id,
        {
            "ordinary_markup_escaped": unsafe_rendered == "&lt;b&gt;&amp;",
            "ordinary_markup_literal_absent": unsafe not in unsafe_rendered,
            "trusted_markup_preserved": trusted_rendered == trusted,
        },
        metrics={
            "rendered_bytes": len(rendered.encode("utf-8")),
            "safe_capabilities": 1,
        },
    )


def if_for_and_empty(contract_id: str) -> dict[str, Any]:
    source = (
        "{% if enabled %}enabled{% else %}disabled{% endif %}|"
        "{% for item in items %}{{ forloop.counter }}={{ item }};"
        "{% empty %}empty{% endfor %}"
    )
    populated = _render(source, {"enabled": True, "items": ["alpha", "beta"]})
    empty = _render(source, {"enabled": False, "items": []})
    return _observed(
        contract_id,
        {
            "empty_branch": empty.split("|", 1)[1],
            "false_branch": empty.split("|", 1)[0],
            "ordered_loop": populated.split("|", 1)[1],
            "true_branch": populated.split("|", 1)[0],
        },
        metrics={"loop_items": 2, "renders": 2},
    )


def closed_filters(contract_id: str) -> dict[str, Any]:
    rendered = _render(
        "{{ missing|default:'fallback' }}|{{ items|length }}|{{ label|lower }}",
        {"items": [1, 2, 3], "label": "GoDJ"},
    )
    default_value, length_value, lower_value = rendered.split("|")
    return _observed(
        contract_id,
        {
            "default": default_value,
            "length": int(length_value),
            "lower": lower_value,
        },
        metrics={"filters_evaluated": 3},
    )


def construction_failures(contract_id: str) -> dict[str, Any]:
    cases = (
        ("unknown_tag", "{% unknown_tag %}"),
        ("unknown_filter", "{{ value|unknown_filter }}"),
        ("private_lookup", "{{ value._private }}"),
        ("unclosed_if", "{% if value %}open"),
    )
    observations = []
    for code, source in cases:
        try:
            _ENGINE.from_string(source)
        except TemplateSyntaxError as error:
            observations.append(
                {
                    "accepted": False,
                    "code": code,
                    "python_type": f"{type(error).__module__}.{type(error).__qualname__}",
                }
            )
        else:
            observations.append({"accepted": True, "code": code, "python_type": None})
    return _observed(
        contract_id,
        {"cases": observations},
        phase="construction",
        metrics={
            "accepted": sum(case["accepted"] for case in observations),
            "rejected": sum(not case["accepted"] for case in observations),
        },
    )


class _CallableProbe:
    def __init__(self) -> None:
        self.calls = 0

    def exposed(self) -> str:
        self.calls += 1
        return "callable-return"


def callable_exposure(contract_id: str) -> dict[str, Any]:
    probe = _CallableProbe()
    rendered = _render("{{ probe.exposed }}", {"probe": probe})
    return _observed(
        contract_id,
        {
            "auto_called": probe.calls == 1,
            "rendered_return_category": (
                "callable_return" if rendered == "callable-return" else "other"
            ),
        },
        metrics={"callable_invocations": probe.calls},
    )


class _ArticleForm(forms.Form):
    title = forms.CharField(max_length=200)
    published = forms.BooleanField(required=False)
    summary = forms.CharField(required=False, empty_value=None)

    def clean(self) -> dict[str, Any]:
        cleaned = super().clean()
        if cleaned.get("published") and cleaned.get("summary") in {None, ""}:
            raise ValidationError(
                "A published Article requires a summary.",
                code="summary_required_when_published",
            )
        return cleaned


class _ArticleModelForm(forms.ModelForm):
    summary = forms.CharField(required=False, empty_value=None)

    class Meta:
        model = Article
        fields = ("title", "published", "summary")


def _error_codes(form: forms.BaseForm) -> list[dict[str, str]]:
    errors = form.errors.as_data()
    ordered: list[dict[str, str]] = []
    for field_name, field_errors in errors.items():
        normalized_name = "non_field" if field_name == NON_FIELD_ERRORS else field_name
        for error in field_errors:
            ordered.append({"field": normalized_name, "code": error.code or "unknown"})
    return ordered


def unbound_and_bound_empty(contract_id: str) -> dict[str, Any]:
    unbound = _ArticleForm()
    unbound_errors = _error_codes(unbound)
    bound = _ArticleForm(data={})
    bound_valid = bound.is_valid()
    return _observed(
        contract_id,
        {
            "bound_empty": {
                "errors": _error_codes(bound),
                "is_bound": bound.is_bound,
                "valid": bound_valid,
            },
            "unbound": {
                "errors": unbound_errors,
                "is_bound": unbound.is_bound,
                "valid_property": unbound.is_valid(),
            },
        },
        metrics={"forms_bound": 1, "forms_constructed": 2},
    )


def valid_article_clean(contract_id: str) -> dict[str, Any]:
    form = _ArticleForm(
        data={"title": "  Clean title  ", "published": "", "summary": ""}
    )
    valid = form.is_valid()
    if not valid:
        raise AssertionError("valid Article form fixture failed validation")
    cleaned = form.cleaned_data
    return _observed(
        contract_id,
        {
            "cleaned": {
                "published": cleaned["published"],
                "summary": cleaned["summary"],
                "title": cleaned["title"],
            },
            "cleaned_order": list(cleaned),
            "valid": valid,
        },
        metrics={"cleaned_fields": len(cleaned), "validation_errors": 0},
    )


def field_error_codes(contract_id: str) -> dict[str, Any]:
    cases = []
    for name, title in (
        ("required", ""),
        ("max_length", "x" * 201),
        ("nul", "ok\x00bad"),
    ):
        form = _ArticleForm(data={"title": title, "published": "", "summary": ""})
        valid = form.is_valid()
        cases.append({"case": name, "errors": _error_codes(form), "valid": valid})
    return _observed(
        contract_id,
        {"cases": cases},
        metrics={"cases": len(cases), "valid_cases": sum(case["valid"] for case in cases)},
    )


def cross_field_validation(contract_id: str) -> dict[str, Any]:
    form = _ArticleForm(
        data={"title": "x" * 201, "published": "on", "summary": ""}
    )
    valid = form.is_valid()
    return _observed(
        contract_id,
        {
            "changed_fields": form.changed_data,
            "cleaned_fields": list(form.cleaned_data),
            "errors": _error_codes(form),
            "invalid_title_excluded": "title" not in form.cleaned_data,
            "valid": valid,
        },
        metrics={"cross_field_validators": 1, "validation_errors": len(_error_codes(form))},
    )


def _article_rows() -> list[dict[str, Any]]:
    return [
        {
            "id": PrimaryKey(primary_key),
            "published": published,
            "summary": summary,
            "title": title,
        }
        for primary_key, title, published, summary in Article.objects.order_by(
            "id"
        ).values_list("id", "title", "published", "summary")
    ]


def _capture_write_count(operation: Callable[[], Any]) -> tuple[Any, int]:
    writes = 0

    def wrapper(
        execute: Callable[..., Any],
        sql: str,
        params: Any,
        many: bool,
        context: dict[str, Any],
    ) -> Any:
        nonlocal writes
        token = sql.lstrip().split(None, 1)[0].upper() if sql.strip() else ""
        if token in {"INSERT", "UPDATE", "DELETE"}:
            writes += 1
        return execute(sql, params, many, context)

    with connection.execute_wrapper(wrapper):
        result = operation()
    return result, writes


def model_form_write_boundary(contract_id: str) -> dict[str, Any]:
    with article_database():
        before = _article_rows()
        invalid = _ArticleModelForm(
            data={"title": "", "published": "on", "summary": "invalid"}
        )
        invalid_valid = invalid.is_valid()
        _, invalid_writes = _capture_write_count(
            lambda: invalid.save() if invalid_valid else None
        )

        created_form = _ArticleModelForm(
            data={"title": "Created", "published": "on", "summary": "Summary"}
        )
        if not created_form.is_valid():
            raise AssertionError("valid create ModelForm failed validation")
        created, create_writes = _capture_write_count(created_form.save)

        existing = Article.objects.get(pk=1)
        update_form = _ArticleModelForm(
            data={"title": "Updated", "published": "", "summary": "Changed"},
            instance=existing,
        )
        if not update_form.is_valid():
            raise AssertionError("valid update ModelForm failed validation")
        updated, update_writes = _capture_write_count(update_form.save)
        after = _article_rows()

        return _observed(
            contract_id,
            {
                "create": {
                    "changed_fields": created_form.changed_data,
                    "primary_key": PrimaryKey(created.pk),
                },
                "invalid": {
                    "errors": _error_codes(invalid),
                    "writes": invalid_writes,
                },
                "update": {
                    "changed_fields": update_form.changed_data,
                    "primary_key": PrimaryKey(updated.pk),
                },
            },
            phase="commit",
            db_state={"after": after, "before": before},
            metrics={
                "create_writes": create_writes,
                "invalid_writes": invalid_writes,
                "update_writes": update_writes,
            },
        )


SCENARIOS = {
    "django.template_form.scalar_and_missing": scalar_and_missing,
    "django.template_form.dotted_lookup_precedence": dotted_lookup_precedence,
    "django.template_form.autoescape_and_safe": autoescape_and_safe,
    "django.template_form.if_for_and_empty": if_for_and_empty,
    "django.template_form.closed_filters": closed_filters,
    "django.template_form.construction_failures": construction_failures,
    "django.template_form.callable_exposure": callable_exposure,
    "django.template_form.unbound_and_bound_empty": unbound_and_bound_empty,
    "django.template_form.valid_article_clean": valid_article_clean,
    "django.template_form.field_error_codes": field_error_codes,
    "django.template_form.cross_field_validation": cross_field_validation,
    "django.template_form.model_form_write_boundary": model_form_write_boundary,
}
