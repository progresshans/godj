"""Django reference scenarios for typed read shapes and stable pagination.

The reference corpus locks result semantics and backend-I/O shape without
copying raw SQL. Go-only type and failure gates remain explicit requirements
for the product adapter; this module does not claim that those adapters exist.
"""

from __future__ import annotations

import re
from collections.abc import Callable
from typing import Any
from unittest.mock import patch

from django.db import OperationalError, connection, models
from django.db.models import Count, Max, Value

from .normalizer import PrimaryKey, normalize
from .scenarios import Article, _database_state, article_database


MAX_OFFSET = (1 << 31) - 1


def _statement_shape(sql: str) -> dict[str, Any]:
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
        "statement_kind": statement_kind,
    }


def _capture(operation: Callable[[], Any]) -> tuple[Any, dict[str, Any]]:
    statements: list[dict[str, Any]] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_shape(sql))
        return execute(sql, params, many, context)

    with connection.execute_wrapper(wrapper):
        result = operation()
    return result, {
        "query_count": len(statements),
        "statements": statements,
    }


def _step(name: str, value: Any) -> dict[str, Any]:
    return {"name": name, "value": value}


def _metric_step(name: str, metrics: dict[str, Any]) -> dict[str, Any]:
    return {"name": name, **metrics}


def _observed(
    contract_id: str,
    result: Any,
    metrics: Any,
    *,
    phase: str = "evaluation",
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": phase,
        "result": normalize(result),
        "error": None,
        "db_state": normalize(_database_state()),
        "metrics": normalize(metrics),
    }


def _observed_steps(
    contract_id: str,
    result_steps: list[dict[str, Any]],
    metric_steps: list[dict[str, Any]],
    *,
    phase: str = "evaluation",
) -> dict[str, Any]:
    result_names = [step["name"] for step in result_steps]
    metric_names = [step["name"] for step in metric_steps]
    if result_names != metric_names:
        raise AssertionError(
            f"result steps {result_names!r} do not match metric steps {metric_names!r}"
        )
    return _observed(
        contract_id,
        {"steps": result_steps},
        {"steps": metric_steps},
        phase=phase,
    )


def ordered_projection(contract_id: str) -> dict[str, Any]:
    with article_database():
        fields = ["title", "id", "summary", "published"]
        rows, metrics = _capture(
            lambda: [
                [title, PrimaryKey(primary_key), summary, published]
                for title, primary_key, summary, published in Article.objects.order_by(
                    "id"
                ).values_list(*fields)
            ]
        )
        return _observed(
            contract_id,
            {"fields": fields, "rows": rows},
            metrics,
        )


def source_fields_outside_projection(contract_id: str) -> dict[str, Any]:
    with article_database():
        rows, metrics = _capture(
            lambda: [
                [PrimaryKey(primary_key)]
                for primary_key in Article.objects.filter(published=True)
                .order_by("title", "id")
                .values_list("id", flat=True)
            ]
        )
        return _observed(
            contract_id,
            {
                "filter_fields": ["published"],
                "order_fields": ["title", "id"],
                "projection_fields": ["id"],
                "rows": rows,
            },
            metrics,
        )


def projection_cache_independence(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = Article.objects.order_by("id")
        empty, empty_metrics = _capture(
            lambda: list(
                source.filter(id__gt=999).values_list("title", flat=True)
            )
        )
        nonempty, nonempty_metrics = _capture(
            lambda: list(source.values_list("title", flat=True))
        )
        Article.objects.create(
            id=5,
            title="Projection five",
            published=True,
            summary=None,
        )
        model_rows, model_metrics = _capture(
            lambda: [PrimaryKey(article.pk) for article in list(source)]
        )
        Article.objects.create(
            id=6,
            title="Projection six",
            published=False,
            summary="fresh projection",
        )
        fresh_projection, fresh_projection_metrics = _capture(
            lambda: list(source.values_list("title", flat=True))
        )
        cached_models, cached_models_metrics = _capture(
            lambda: [PrimaryKey(article.pk) for article in list(source)]
        )
        return _observed_steps(
            contract_id,
            [
                _step("empty_projection", empty),
                _step("nonempty_projection", nonempty),
                _step("model_evaluation_after_first_insert", model_rows),
                _step("projection_after_second_insert", fresh_projection),
                _step("model_cache_after_second_insert", cached_models),
            ],
            [
                _metric_step("empty_projection", empty_metrics),
                _metric_step("nonempty_projection", nonempty_metrics),
                _metric_step("model_evaluation_after_first_insert", model_metrics),
                _metric_step(
                    "projection_after_second_insert",
                    fresh_projection_metrics,
                ),
                _metric_step(
                    "model_cache_after_second_insert",
                    cached_models_metrics,
                ),
            ],
        )


def distinct_projection(contract_id: str) -> dict[str, Any]:
    with article_database():
        values, metrics = _capture(
            lambda: list(
                Article.objects.order_by("published")
                .values_list("published", flat=True)
                .distinct()
            )
        )
        return _observed(
            contract_id,
            {"fields": ["published"], "rows": [[value] for value in values]},
            metrics,
        )


def stable_offset_limit(contract_id: str) -> dict[str, Any]:
    with article_database():
        page, page_metrics = _capture(
            lambda: [
                [PrimaryKey(primary_key), title]
                for primary_key, title in Article.objects.order_by("id")[
                    1:3
                ].values_list("id", "title")
            ]
        )
        out_of_range, out_of_range_metrics = _capture(
            lambda: [
                PrimaryKey(primary_key)
                for primary_key in Article.objects.order_by("id")[
                    20:22
                ].values_list("id", flat=True)
            ]
        )
        return _observed_steps(
            contract_id,
            [
                _step("offset_one_limit_two", page),
                _step("out_of_range", out_of_range),
            ],
            [
                _metric_step("offset_one_limit_two", page_metrics),
                _metric_step("out_of_range", out_of_range_metrics),
            ],
        )


def _validate_offset(value: int) -> dict[str, Any]:
    if value < 0:
        raise ValueError("negative offset")
    if value > MAX_OFFSET:
        raise OverflowError("offset exceeds int32")
    # Construct the Django slice to retain the reference's no-I/O behavior.
    Article.objects.order_by("id")[value:]
    return {"accepted": value}


def _offset_case(name: str, value: int) -> tuple[dict[str, Any], dict[str, Any]]:
    try:
        result, metrics = _capture(lambda: _validate_offset(value))
        return _step(name, result), _metric_step(name, metrics)
    except ValueError:
        return (
            _step(
                name,
                {"error": {"category": "query_error", "code": "invalid_offset"}},
            ),
            _metric_step(name, {"query_count": 0, "statements": []}),
        )
    except OverflowError:
        return (
            _step(
                name,
                {"error": {"category": "query_error", "code": "invalid_offset"}},
            ),
            _metric_step(name, {"query_count": 0, "statements": []}),
        )


def invalid_offset_pre_io(contract_id: str) -> dict[str, Any]:
    with article_database():
        pairs = [
            _offset_case("negative", -1),
            _offset_case("maximum", MAX_OFFSET),
            _offset_case("overflow", MAX_OFFSET + 1),
        ]
        return _observed_steps(
            contract_id,
            [pair[0] for pair in pairs],
            [pair[1] for pair in pairs],
            phase="construction",
        )


def cold_count_and_warm_cache(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = Article.objects.order_by("id")
        cold_count, cold_metrics = _capture(source.count)
        Article.objects.create(
            id=5,
            title="Count five",
            published=True,
            summary=None,
        )
        model_rows, model_metrics = _capture(
            lambda: [PrimaryKey(article.pk) for article in list(source)]
        )
        warm_count, warm_metrics = _capture(source.count)
        return _observed_steps(
            contract_id,
            [
                _step("cold_count", cold_count),
                _step("model_evaluation_after_insert", model_rows),
                _step("warm_count", warm_count),
            ],
            [
                _metric_step("cold_count", cold_metrics),
                _metric_step("model_evaluation_after_insert", model_metrics),
                _metric_step("warm_count", warm_metrics),
            ],
        )


def sliced_distinct_count(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = (
            Article.objects.filter(published=True)
            .order_by("id")
            .distinct()[1:3]
        )
        rows, rows_metrics = _capture(
            lambda: [
                PrimaryKey(primary_key)
                for primary_key in source.values_list("id", flat=True)
            ]
        )
        count, count_metrics = _capture(source.count)
        return _observed_steps(
            contract_id,
            [
                _step("logical_source_rows", rows),
                _step("logical_source_count", count),
            ],
            [
                _metric_step("logical_source_rows", rows_metrics),
                _metric_step("logical_source_count", count_metrics),
            ],
        )


def empty_count_and_nullable_max(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = Article.objects.filter(title="missing")
        count, count_metrics = _capture(source.count)
        maximum, maximum_metrics = _capture(
            lambda: source.aggregate(max_summary=Max("summary"))["max_summary"]
        )
        return _observed_steps(
            contract_id,
            [
                _step("empty_count", count),
                _step("empty_nullable_max", maximum),
            ],
            [
                _metric_step("empty_count", count_metrics),
                _metric_step("empty_nullable_max", maximum_metrics),
            ],
        )


def filtered_count_and_max(contract_id: str) -> dict[str, Any]:
    with article_database():
        aggregate, metrics = _capture(
            lambda: Article.objects.filter(published=True).aggregate(
                row_count=Count("*"),
                latest_id=Max("id"),
                max_summary=Max("summary"),
            )
        )
        return _observed(
            contract_id,
            {
                "fields": ["row_count", "latest_id", "max_summary"],
                "values": [
                    aggregate["row_count"],
                    PrimaryKey(aggregate["latest_id"]),
                    aggregate["max_summary"],
                ],
            },
            metrics,
        )


class _FailingIntegerField(models.IntegerField):
    def from_db_value(self, value, expression, database_connection):
        raise ValueError("forced projection decode failure")


class _CursorProbe:
    def __init__(
        self,
        cursor: Any,
        *,
        fail_fetch_call: int | None = None,
        fail_close: bool = False,
    ) -> None:
        self._cursor = cursor
        self._fail_fetch_call = fail_fetch_call
        self._fail_close = fail_close
        self.fetch_calls = 0
        self.close_attempts = 0

    def __getattr__(self, name: str) -> Any:
        return getattr(self._cursor, name)

    def fetchmany(self, *args, **kwargs):
        self.fetch_calls += 1
        if self.fetch_calls == self._fail_fetch_call:
            raise OperationalError("forced projection iteration failure")
        return self._cursor.fetchmany(*args, **kwargs)

    def close(self):
        self.close_attempts += 1
        result = self._cursor.close()
        if self._fail_close:
            raise OperationalError("forced projection close failure")
        return result


def _failure_case(name: str) -> tuple[dict[str, Any], dict[str, Any]]:
    original_cursor = connection.cursor
    probes: list[_CursorProbe] = []
    statements: list[dict[str, Any]] = []

    def cursor_factory():
        probe = _CursorProbe(
            original_cursor(),
            fail_fetch_call=2 if name == "iteration_failure" else None,
            fail_close=name == "close_failure",
        )
        probes.append(probe)
        return probe

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_shape(sql))
        return execute(sql, params, many, context)

    try:
        with (
            connection.execute_wrapper(wrapper),
            patch.object(connection, "cursor", side_effect=cursor_factory),
        ):
            if name == "consumer_stop":
                iterator = (
                    Article.objects.order_by("id")
                    .values_list("id", flat=True)
                    .iterator(chunk_size=1)
                )
                first = next(iterator)
                iterator.close()
                value: Any = {
                    "first": PrimaryKey(first),
                    "outcome": "consumer_stopped",
                }
            elif name == "decode_failure":
                list(
                    Article.objects.order_by("id")
                    .annotate(
                        fault=Value(
                            1,
                            output_field=_FailingIntegerField(),
                        )
                    )
                    .values_list("fault", flat=True)
                    .iterator(chunk_size=1)
                )
                raise AssertionError("expected forced decode failure")
            else:
                list(
                    Article.objects.order_by("id")
                    .values_list("id", flat=True)
                    .iterator(chunk_size=1)
                )
                raise AssertionError(f"expected forced {name}")
    except ValueError:
        if name != "decode_failure":
            raise
        value = {"error": {"category": "decode_error", "code": "conversion"}}
    except OperationalError:
        if name not in {"iteration_failure", "close_failure"}:
            raise
        value = {
            "error": {
                "category": "backend_error",
                "code": (
                    "iteration" if name == "iteration_failure" else "close"
                ),
            }
        }

    if len(probes) != 1:
        raise AssertionError(f"{name} opened {len(probes)} cursors, expected one")
    probe = probes[0]
    return (
        _step(name, value),
        _metric_step(
            name,
            {
                "close_attempts": probe.close_attempts,
                "query_count": len(statements),
                "statements": statements,
            },
        ),
    )


def terminal_failure_ownership(contract_id: str) -> dict[str, Any]:
    with article_database():
        pairs = [
            _failure_case("consumer_stop"),
            _failure_case("decode_failure"),
            _failure_case("iteration_failure"),
            _failure_case("close_failure"),
        ]
        return _observed_steps(
            contract_id,
            [pair[0] for pair in pairs],
            [pair[1] for pair in pairs],
        )


def backend_parity_reference(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = Article.objects.filter(published=True).order_by("id")[1:3]
        rows, rows_metrics = _capture(
            lambda: [
                [PrimaryKey(primary_key), title, published]
                for primary_key, title, published in source.values_list(
                    "id", "title", "published"
                )
            ]
        )
        aggregate, aggregate_metrics = _capture(
            lambda: source.aggregate(
                row_count=Count("*"),
                latest_id=Max("id"),
            )
        )
        ordered_aggregate = {
            "fields": ["row_count", "latest_id"],
            "values": [
                aggregate["row_count"],
                PrimaryKey(aggregate["latest_id"]),
            ],
        }
        return _observed_steps(
            contract_id,
            [
                _step("sqlite_reference_projection", rows),
                _step("sqlite_reference_aggregate", ordered_aggregate),
            ],
            [
                _metric_step("sqlite_reference_projection", rows_metrics),
                _metric_step("sqlite_reference_aggregate", aggregate_metrics),
            ],
        )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.query.breadth.ordered_projection": ordered_projection,
    "django.query.breadth.source_fields_outside_projection": (
        source_fields_outside_projection
    ),
    "django.query.breadth.projection_cache_independence": (
        projection_cache_independence
    ),
    "django.query.breadth.distinct_projection": distinct_projection,
    "django.query.breadth.stable_offset_limit": stable_offset_limit,
    "django.query.breadth.invalid_offset_pre_io": invalid_offset_pre_io,
    "django.query.breadth.cold_count_and_warm_cache": cold_count_and_warm_cache,
    "django.query.breadth.sliced_distinct_count": sliced_distinct_count,
    "django.query.breadth.empty_count_and_nullable_max": (
        empty_count_and_nullable_max
    ),
    "django.query.breadth.filtered_count_and_max": filtered_count_and_max,
    "django.query.breadth.terminal_failure_ownership": terminal_failure_ownership,
    "django.query.breadth.backend_parity_reference": backend_parity_reference,
}
