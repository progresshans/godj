"""Django reference scenarios for composable scalar Boolean predicates.

The scenarios observe public ``Q`` and ``QuerySet`` behavior only. They do not
read locked manifests, oracle payloads, or GoDj product fixtures, and they do
not claim compatibility with Django's internal ``Q`` object representation.
"""

from __future__ import annotations

import re
from collections.abc import Callable
from typing import Any

from django.db import connection
from django.db.models import Count, F, Max, Q

from .normalizer import PrimaryKey
from .query_breadth_scenarios import (
    _metric_step,
    _observed,
    _observed_steps,
    _step,
)
from .scenarios import Article, article_database


def _statement_shape(sql: str, params: Any) -> dict[str, Any]:
    rendered = " ".join(sql.strip().upper().split())
    statement_kind = rendered.split(None, 1)[0] if rendered else "EMPTY"
    return {
        "aggregate_functions": [
            function
            for function in ("COUNT", "MAX")
            if re.search(rf"\b{function}\s*\(", rendered)
        ],
        "derived_table": bool(re.search(r"\bFROM\s*\(", rendered)),
        "distinct": bool(re.search(r"\bSELECT\s+DISTINCT\b", rendered)),
        "has_limit": bool(re.search(r"\bLIMIT\b", rendered)),
        "has_offset": bool(re.search(r"\bOFFSET\b", rendered)),
        "logical_operators": {
            "and": len(re.findall(r"\bAND\b", rendered)),
            "not": len(re.findall(r"\bNOT\s*\(", rendered)),
            "or": len(re.findall(r"\bOR\b", rendered)),
        },
        "null_predicates": {
            "is_not_null": len(re.findall(r"\bIS\s+NOT\s+NULL\b", rendered)),
            "is_null": len(
                re.findall(r"\bIS\s+NULL\b", rendered)
            ),
        },
        "parameters": list(params or ()),
        "statement_kind": statement_kind,
    }


def _capture(operation: Callable[[], Any]) -> tuple[Any, dict[str, Any]]:
    statements: list[dict[str, Any]] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_shape(sql, params))
        return execute(sql, params, many, context)

    with connection.execute_wrapper(wrapper):
        result = operation()
    return result, {
        "query_count": len(statements),
        "statements": statements,
    }


def _primary_keys(queryset: Any) -> list[PrimaryKey]:
    return [
        PrimaryKey(primary_key)
        for primary_key in queryset.values_list("id", flat=True)
    ]


def _projected_rows(queryset: Any) -> list[list[Any]]:
    return [
        [PrimaryKey(primary_key), title]
        for primary_key, title in queryset.values_list("id", "title")
    ]


def _nullable_primary_key(value: Any) -> PrimaryKey | None:
    if value is None:
        return None
    return PrimaryKey(value)


def scalar_exact_or(contract_id: str) -> dict[str, Any]:
    with article_database():
        rows, metrics = _capture(
            lambda: _primary_keys(
                Article.objects.filter(
                    Q(title="Alpine Guide") | Q(title="Other")
                ).order_by("id")
            )
        )
        return _observed(
            contract_id,
            {"operator": "or", "rows": rows},
            metrics,
        )


def escaped_ascii_icontains_or(contract_id: str) -> dict[str, Any]:
    with article_database():
        Article.objects.create(
            id=5,
            title="100%_Coverage",
            published=True,
            summary="literal markers",
        )
        needle = "%_"
        before = needle
        rows, metrics = _capture(
            lambda: _primary_keys(
                Article.objects.filter(
                    Q(title__icontains=needle) | Q(summary__icontains="orm")
                ).order_by("id")
            )
        )
        return _observed(
            contract_id,
            {
                "input_after": needle,
                "input_before": before,
                "rows": rows,
            },
            metrics,
        )


def grouped_or_and_reuse(contract_id: str) -> dict[str, Any]:
    with article_database():
        predicate = Q(title__icontains="django") | Q(title="Other")
        source = Article.objects.filter(predicate)
        published, published_metrics = _capture(
            lambda: _primary_keys(source.filter(published=True).order_by("id"))
        )
        unpublished, unpublished_metrics = _capture(
            lambda: _primary_keys(source.filter(published=False).order_by("id"))
        )
        return _observed_steps(
            contract_id,
            [
                _step("published", published),
                _step("unpublished", unpublished),
            ],
            [
                _metric_step("published", published_metrics),
                _metric_step("unpublished", unpublished_metrics),
            ],
        )


def nonnull_scalar_not(contract_id: str) -> dict[str, Any]:
    with article_database():
        rows, metrics = _capture(
            lambda: _primary_keys(
                Article.objects.filter(
                    ~Q(title__icontains="django")
                ).order_by("id")
            )
        )
        return _observed(
            contract_id,
            {"operator": "not", "rows": rows},
            metrics,
        )


def _nullable_negation_case(
    name: str,
    predicate: Q,
) -> tuple[dict[str, Any], dict[str, Any]]:
    rows, metrics = _capture(
        lambda: _primary_keys(Article.objects.filter(~predicate).order_by("id"))
    )
    return _step(name, rows), _metric_step(name, metrics)


def nullable_negation_truth_table(contract_id: str) -> dict[str, Any]:
    with article_database():
        pairs = [
            _nullable_negation_case("not_exact_orm", Q(summary="ORM")),
            _nullable_negation_case(
                "not_icontains_orm",
                Q(summary__icontains="orm"),
            ),
            _nullable_negation_case(
                "not_isnull_true",
                Q(summary__isnull=True),
            ),
            _nullable_negation_case(
                "not_isnull_false",
                Q(summary__isnull=False),
            ),
        ]
        return _observed_steps(
            contract_id,
            [pair[0] for pair in pairs],
            [pair[1] for pair in pairs],
        )


def implicit_filter_and(contract_id: str) -> dict[str, Any]:
    with article_database():
        variadic, variadic_metrics = _capture(
            lambda: _primary_keys(
                Article.objects.filter(
                    Q(title__icontains="django"),
                    Q(published=True),
                ).order_by("id")
            )
        )
        repeated, repeated_metrics = _capture(
            lambda: _primary_keys(
                Article.objects.filter(Q(title__icontains="django"))
                .filter(Q(published=True))
                .order_by("id")
            )
        )
        return _observed_steps(
            contract_id,
            [
                _step("variadic_filter", variadic),
                _step("repeated_filter", repeated),
            ],
            [
                _metric_step("variadic_filter", variadic_metrics),
                _metric_step("repeated_filter", repeated_metrics),
            ],
        )


def nested_connector_order_and_source_independence(
    contract_id: str,
) -> dict[str, Any]:
    with article_database():
        predicate = Q(title__icontains="django") | Q(title="Other")
        base = Article.objects.filter(published=True)
        first = base.filter(predicate)
        second = base.filter(Q(title="Alpine Guide") | Q(title="Other"))

        first_rows, first_metrics = _capture(
            lambda: _primary_keys(first.order_by("id"))
        )
        second_rows, second_metrics = _capture(
            lambda: _primary_keys(second.order_by("id"))
        )
        base_rows, base_metrics = _capture(
            lambda: _primary_keys(base.order_by("id"))
        )
        reused_rows, reused_metrics = _capture(
            lambda: _primary_keys(
                Article.objects.filter(predicate, published=False).order_by("id")
            )
        )
        return _observed_steps(
            contract_id,
            [
                _step("first_derived", first_rows),
                _step("second_derived", second_rows),
                _step("base_after_derivation", base_rows),
                _step("reused_predicate", reused_rows),
            ],
            [
                _metric_step("first_derived", first_metrics),
                _metric_step("second_derived", second_metrics),
                _metric_step("base_after_derivation", base_metrics),
                _metric_step("reused_predicate", reused_metrics),
            ],
        )


def composite_distinct_stable_page(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = (
            Article.objects.filter(
                Q(title__icontains="django") | Q(published=True)
            )
            .order_by("id")
            .distinct()[1:3]
        )
        rows, metrics = _capture(lambda: _projected_rows(source))
        return _observed(
            contract_id,
            {"fields": ["id", "title"], "rows": rows},
            metrics,
        )


def projection_outside_predicate(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = Article.objects.filter(
            Q(summary__isnull=True) | Q(published=False)
        ).order_by("id")
        rows, metrics = _capture(lambda: _projected_rows(source))
        return _observed(
            contract_id,
            {
                "filter_fields": ["summary", "published"],
                "projection_fields": ["id", "title"],
                "rows": rows,
            },
            metrics,
        )


def _aggregate_values(source: Any) -> dict[str, Any]:
    aggregate = source.aggregate(row_count=Count("*"), latest_id=Max("id"))
    return {
        "fields": ["row_count", "latest_id"],
        "values": [
            aggregate["row_count"],
            _nullable_primary_key(aggregate["latest_id"]),
        ],
    }


def composite_count_max(contract_id: str) -> dict[str, Any]:
    with article_database():
        nonempty = Article.objects.filter(
            Q(title__icontains="django") | Q(summary__isnull=True)
        )
        empty = Article.objects.filter(
            Q(title="missing") | Q(summary="missing")
        )
        nonempty_result, nonempty_metrics = _capture(
            lambda: _aggregate_values(nonempty)
        )
        empty_result, empty_metrics = _capture(lambda: _aggregate_values(empty))
        return _observed_steps(
            contract_id,
            [
                _step("nonempty", nonempty_result),
                _step("empty", empty_result),
            ],
            [
                _metric_step("nonempty", nonempty_metrics),
                _metric_step("empty", empty_metrics),
            ],
        )


def _integer_literal_boundary(
    contract_id: str,
    lookup: str,
    rhs: int,
) -> dict[str, Any]:
    with article_database():
        rows, metrics = _capture(
            lambda: _primary_keys(
                Article.objects.filter(**{f"id__{lookup}": rhs}).order_by(
                    "id"
                )
            )
        )
        return _observed(
            contract_id,
            {"lookup": lookup, "rhs": rhs, "rows": rows},
            metrics,
        )


def integer_gt_literal_boundary(contract_id: str) -> dict[str, Any]:
    return _integer_literal_boundary(contract_id, "gt", 2)


def integer_gte_literal_boundary(contract_id: str) -> dict[str, Any]:
    return _integer_literal_boundary(contract_id, "gte", 2)


def integer_lt_literal_boundary(contract_id: str) -> dict[str, Any]:
    return _integer_literal_boundary(contract_id, "lt", 3)


def integer_lte_literal_boundary(contract_id: str) -> dict[str, Any]:
    return _integer_literal_boundary(contract_id, "lte", 3)


def range_composition_negation_and_reuse(
    contract_id: str,
) -> dict[str, Any]:
    with article_database():
        predicate = Q(id__gt=1) & Q(id__lte=3)
        cases = [
            (
                "explicit_q_range",
                Article.objects.filter(predicate),
            ),
            (
                "keyword_range",
                Article.objects.filter(id__gt=1, id__lte=3),
            ),
            (
                "negated_range",
                Article.objects.filter(~predicate),
            ),
            (
                "reused_published",
                Article.objects.filter(predicate, published=True),
            ),
        ]
        result_steps = []
        metric_steps = []
        for name, source in cases:
            rows, metrics = _capture(
                lambda source=source: _primary_keys(source.order_by("id"))
            )
            result_steps.append(_step(name, rows))
            metric_steps.append(_metric_step(name, metrics))
        return _observed_steps(contract_id, result_steps, metric_steps)


def same_field_reference_boundaries(contract_id: str) -> dict[str, Any]:
    with article_database():
        cases = [
            ("id_exact_id", Q(id=F("id"))),
            ("id_gt_id", Q(id__gt=F("id"))),
            ("id_gte_id", Q(id__gte=F("id"))),
            ("id_lt_id", Q(id__lt=F("id"))),
            ("id_lte_id", Q(id__lte=F("id"))),
        ]
        result_steps = []
        metric_steps = []
        for name, predicate in cases:
            rows, metrics = _capture(
                lambda predicate=predicate: _primary_keys(
                    Article.objects.filter(predicate).order_by("id")
                )
            )
            result_steps.append(_step(name, rows))
            metric_steps.append(_metric_step(name, metrics))
        return _observed_steps(contract_id, result_steps, metric_steps)


def same_model_field_reference_and_nullable_negation(
    contract_id: str,
) -> dict[str, Any]:
    with article_database():
        Article.objects.create(
            id=5,
            title="same",
            published=False,
            summary="same",
        )
        cases = [
            ("cross_field_exact", Q(title=F("summary"))),
            ("cross_field_not_exact", ~Q(title=F("summary"))),
            ("equal_row_gt", Q(id=5, title__gt=F("summary"))),
            ("nullable_rhs_direct", Q(id=1, title=F("summary"))),
        ]
        result_steps = []
        metric_steps = []
        for name, predicate in cases:
            rows, metrics = _capture(
                lambda predicate=predicate: _primary_keys(
                    Article.objects.filter(predicate).order_by("id")
                )
            )
            result_steps.append(_step(name, rows))
            metric_steps.append(_metric_step(name, metrics))
        return _observed_steps(contract_id, result_steps, metric_steps)


def nullable_ordering_negation_truth_table(
    contract_id: str,
) -> dict[str, Any]:
    with article_database():
        cases = [
            ("not_gt_empty", ~Q(summary__gt="")),
            ("not_gte_empty", ~Q(summary__gte="")),
            ("not_lt_orm", ~Q(summary__lt="ORM")),
            ("not_lte_orm", ~Q(summary__lte="ORM")),
        ]
        result_steps = []
        metric_steps = []
        for name, predicate in cases:
            rows, metrics = _capture(
                lambda predicate=predicate: _primary_keys(
                    Article.objects.filter(predicate).order_by("id")
                )
            )
            result_steps.append(_step(name, rows))
            metric_steps.append(_metric_step(name, metrics))
        return _observed_steps(contract_id, result_steps, metric_steps)


def _field_reference_source() -> Any:
    return Article.objects.filter(
        Q(id__gte=2)
        & (Q(summary__gte=F("summary")) | Q(summary__isnull=True))
    )


def field_reference_stable_projection(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = _field_reference_source().order_by("-id")
        rows, metrics = _capture(lambda: _projected_rows(source))
        return _observed(
            contract_id,
            {
                "filter_fields": ["id", "summary"],
                "order_fields": ["-id"],
                "projection_fields": ["id", "title"],
                "rows": rows,
            },
            metrics,
        )


def field_reference_count_max(contract_id: str) -> dict[str, Any]:
    with article_database():
        nonempty = _field_reference_source()
        empty = Article.objects.filter(id__gt=F("id"))
        nonempty_result, nonempty_metrics = _capture(
            lambda: _aggregate_values(nonempty)
        )
        empty_result, empty_metrics = _capture(lambda: _aggregate_values(empty))
        return _observed_steps(
            contract_id,
            [
                _step("nonempty", nonempty_result),
                _step("empty", empty_result),
            ],
            [
                _metric_step("nonempty", nonempty_metrics),
                _metric_step("empty", empty_metrics),
            ],
        )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.query.expression.scalar_exact_or": scalar_exact_or,
    "django.query.expression.escaped_ascii_icontains_or": (
        escaped_ascii_icontains_or
    ),
    "django.query.expression.grouped_or_and_reuse": grouped_or_and_reuse,
    "django.query.expression.nonnull_scalar_not": nonnull_scalar_not,
    "django.query.expression.nullable_negation_truth_table": (
        nullable_negation_truth_table
    ),
    "django.query.expression.implicit_filter_and": implicit_filter_and,
    "django.query.expression.nested_connector_order_and_source_independence": (
        nested_connector_order_and_source_independence
    ),
    "django.query.expression.composite_distinct_stable_page": (
        composite_distinct_stable_page
    ),
    "django.query.expression.projection_outside_predicate": (
        projection_outside_predicate
    ),
    "django.query.expression.composite_count_max": composite_count_max,
    "django.query.expression.integer_gt_literal_boundary": (
        integer_gt_literal_boundary
    ),
    "django.query.expression.integer_gte_literal_boundary": (
        integer_gte_literal_boundary
    ),
    "django.query.expression.integer_lt_literal_boundary": (
        integer_lt_literal_boundary
    ),
    "django.query.expression.integer_lte_literal_boundary": (
        integer_lte_literal_boundary
    ),
    "django.query.expression.range_composition_negation_and_reuse": (
        range_composition_negation_and_reuse
    ),
    "django.query.expression.same_field_reference_boundaries": (
        same_field_reference_boundaries
    ),
    "django.query.expression.same_model_field_reference_and_nullable_negation": (
        same_model_field_reference_and_nullable_negation
    ),
    "django.query.expression.nullable_ordering_negation_truth_table": (
        nullable_ordering_negation_truth_table
    ),
    "django.query.expression.field_reference_stable_projection": (
        field_reference_stable_projection
    ),
    "django.query.expression.field_reference_count_max": (
        field_reference_count_max
    ),
}
