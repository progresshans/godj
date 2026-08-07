"""Django reference scenarios for QuerySet evaluation and result-cache behavior."""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from django.db import OperationalError, connection

from .normalizer import PrimaryKey, normalize
from .scenarios import (
    Article,
    FIXTURES,
    _database_state,
    article_database,
)


def _statement_kind(sql: str) -> str:
    rendered = sql.lstrip().upper()
    return rendered.split(None, 1)[0] if rendered else "EMPTY"


def _capture(operation: Callable[[], Any]) -> tuple[Any, dict[str, Any]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_kind(sql))
        return execute(sql, params, many, context)

    with connection.execute_wrapper(wrapper):
        result = operation()
    return result, {
        "query_count": len(statements),
        "statement_kinds": statements,
    }


def _capture_missing_table(
    operation: Callable[[], Any],
) -> tuple[dict[str, str], dict[str, Any]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_kind(sql))
        return execute(sql, params, many, context)

    try:
        with connection.execute_wrapper(wrapper):
            operation()
    except OperationalError as error:
        if Article._meta.db_table not in str(error):
            raise AssertionError(
                "expected the failed evaluation to name the missing Article table"
            ) from error
        return {
            "category": "backend_error",
            "code": "missing_table",
        }, {
            "query_count": len(statements),
            "statement_kinds": statements,
        }
    raise AssertionError("expected QuerySet evaluation against a missing table to fail")


def _article_value(article: Article | None) -> dict[str, Any]:
    if article is None:
        raise AssertionError("scenario expected one Article row")
    return {
        "id": PrimaryKey(article.pk),
        "published": article.published,
        "summary": article.summary,
        "title": article.title,
    }


def _materialize(queryset: Any) -> list[dict[str, Any]]:
    """Evaluate the selected QuerySet itself instead of a values-list clone."""

    return [_article_value(article) for article in list(queryset)]


def _result_step(name: str, value: Any) -> dict[str, Any]:
    return {"name": name, "value": value}


def _error_step(name: str, error: dict[str, str]) -> dict[str, Any]:
    return {"error": error, "name": name}


def _metric_step(name: str, metrics: dict[str, Any]) -> dict[str, Any]:
    return {"name": name, **metrics}


def _observed(
    contract_id: str,
    result_steps: list[dict[str, Any]],
    metric_steps: list[dict[str, Any]],
) -> dict[str, Any]:
    result_names = [step["name"] for step in result_steps]
    metric_names = [step["name"] for step in metric_steps]
    if result_names != metric_names:
        raise AssertionError(
            f"result steps {result_names!r} do not match metric steps {metric_names!r}"
        )
    return {
        "id": contract_id,
        "status": "observed",
        "phase": "evaluation",
        "result": normalize({"steps": result_steps}),
        "error": None,
        "db_state": normalize(_database_state()),
        "metrics": normalize({"steps": metric_steps}),
    }


def repeated_full_evaluation(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.order_by("id")
        first, first_metrics = _capture(lambda: _materialize(queryset))
        second, second_metrics = _capture(lambda: _materialize(queryset))
        return _observed(
            contract_id,
            [
                _result_step("first_full_evaluation", first),
                _result_step("second_full_evaluation", second),
            ],
            [
                _metric_step("first_full_evaluation", first_metrics),
                _metric_step("second_full_evaluation", second_metrics),
            ],
        )


def empty_full_evaluation(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.filter(title="Later").order_by("id")
        first, first_metrics = _capture(lambda: _materialize(queryset))
        Article.objects.create(id=5, title="Later", published=False, summary=None)
        second, second_metrics = _capture(lambda: _materialize(queryset))
        return _observed(
            contract_id,
            [
                _result_step("empty_evaluation", first),
                _result_step("same_queryset_after_matching_insert", second),
            ],
            [
                _metric_step("empty_evaluation", first_metrics),
                _metric_step(
                    "same_queryset_after_matching_insert",
                    second_metrics,
                ),
            ],
        )


def stale_snapshot_and_fresh_queryset(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.order_by("id")
        before, before_metrics = _capture(lambda: _materialize(queryset))
        Article.objects.create(id=5, title="New", published=True, summary="fresh")
        stale, stale_metrics = _capture(lambda: _materialize(queryset))
        fresh, fresh_metrics = _capture(
            lambda: _materialize(Article.objects.order_by("id"))
        )
        return _observed(
            contract_id,
            [
                _result_step("source_before_insert", before),
                _result_step("source_after_insert", stale),
                _result_step("fresh_queryset_after_insert", fresh),
            ],
            [
                _metric_step("source_before_insert", before_metrics),
                _metric_step("source_after_insert", stale_metrics),
                _metric_step("fresh_queryset_after_insert", fresh_metrics),
            ],
        )


def chained_queryset_independence(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = Article.objects.filter(published=True).order_by("id")
        source_before, source_before_metrics = _capture(
            lambda: _materialize(source)
        )
        Article.objects.create(
            id=5,
            title="New Django",
            published=True,
            summary="chain",
        )
        derived = source.filter(title__icontains="django")
        derived_after, derived_after_metrics = _capture(
            lambda: _materialize(derived)
        )
        source_after, source_after_metrics = _capture(
            lambda: _materialize(source)
        )
        return _observed(
            contract_id,
            [
                _result_step("source_before_insert", source_before),
                _result_step("derived_after_insert", derived_after),
                _result_step("source_after_insert", source_after),
            ],
            [
                _metric_step("source_before_insert", source_before_metrics),
                _metric_step("derived_after_insert", derived_after_metrics),
                _metric_step("source_after_insert", source_after_metrics),
            ],
        )


def count_cold_and_warm(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.order_by("id")
        count_before, count_before_metrics = _capture(queryset.count)
        Article.objects.create(id=5, title="Counted", published=False, summary=None)
        full_after, full_after_metrics = _capture(
            lambda: _materialize(queryset)
        )
        count_cached, count_cached_metrics = _capture(queryset.count)
        return _observed(
            contract_id,
            [
                _result_step("count_before_insert", count_before),
                _result_step("full_evaluation_after_insert", full_after),
                _result_step("count_from_full_cache", count_cached),
            ],
            [
                _metric_step("count_before_insert", count_before_metrics),
                _metric_step(
                    "full_evaluation_after_insert",
                    full_after_metrics,
                ),
                _metric_step("count_from_full_cache", count_cached_metrics),
            ],
        )


def exists_cold_and_warm(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.filter(title="Later").order_by("id")
        exists_before, exists_before_metrics = _capture(queryset.exists)
        Article.objects.create(id=5, title="Later", published=True, summary=None)
        full_after, full_after_metrics = _capture(
            lambda: _materialize(queryset)
        )
        exists_cached, exists_cached_metrics = _capture(queryset.exists)
        return _observed(
            contract_id,
            [
                _result_step("exists_before_insert", exists_before),
                _result_step("full_evaluation_after_insert", full_after),
                _result_step("exists_from_full_cache", exists_cached),
            ],
            [
                _metric_step("exists_before_insert", exists_before_metrics),
                _metric_step(
                    "full_evaluation_after_insert",
                    full_after_metrics,
                ),
                _metric_step("exists_from_full_cache", exists_cached_metrics),
            ],
        )


def iterator_bypass(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.order_by("id")
        cached_before, cached_before_metrics = _capture(
            lambda: _materialize(queryset)
        )
        Article.objects.create(id=5, title="Iterator", published=True, summary=None)
        iterator_after, iterator_after_metrics = _capture(
            lambda: [_article_value(article) for article in queryset.iterator()]
        )
        cached_after, cached_after_metrics = _capture(
            lambda: _materialize(queryset)
        )
        return _observed(
            contract_id,
            [
                _result_step("cached_before_insert", cached_before),
                _result_step("iterator_after_insert", iterator_after),
                _result_step("source_after_iterator", cached_after),
            ],
            [
                _metric_step("cached_before_insert", cached_before_metrics),
                _metric_step("iterator_after_insert", iterator_after_metrics),
                _metric_step("source_after_iterator", cached_after_metrics),
            ],
        )


def index_partial_evaluation(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.order_by("-id")
        index_before, index_before_metrics = _capture(
            lambda: _article_value(queryset[0])
        )
        Article.objects.create(
            id=5,
            title="Index five",
            published=False,
            summary=None,
        )
        index_after, index_after_metrics = _capture(
            lambda: _article_value(queryset[0])
        )
        full_after, full_after_metrics = _capture(
            lambda: _materialize(queryset)
        )
        Article.objects.create(
            id=6,
            title="Index six",
            published=True,
            summary=None,
        )
        index_cached, index_cached_metrics = _capture(
            lambda: _article_value(queryset[0])
        )
        return _observed(
            contract_id,
            [
                _result_step("index_before_insert", index_before),
                _result_step("index_after_first_insert", index_after),
                _result_step("full_evaluation_after_first_insert", full_after),
                _result_step(
                    "index_from_full_cache_after_second_insert",
                    index_cached,
                ),
            ],
            [
                _metric_step("index_before_insert", index_before_metrics),
                _metric_step("index_after_first_insert", index_after_metrics),
                _metric_step(
                    "full_evaluation_after_first_insert",
                    full_after_metrics,
                ),
                _metric_step(
                    "index_from_full_cache_after_second_insert",
                    index_cached_metrics,
                ),
            ],
        )


def failed_evaluation_retry(contract_id: str) -> dict[str, Any]:
    if Article._meta.db_table in connection.introspection.table_names():
        raise AssertionError("failed-evaluation scenario requires a missing table")

    queryset = Article.objects.order_by("id")
    failure, failure_metrics = _capture_missing_table(
        lambda: _materialize(queryset)
    )
    with connection.schema_editor() as editor:
        editor.create_model(Article)
    try:
        Article.objects.bulk_create(
            [
                Article(
                    id=row.id,
                    title=row.title,
                    published=row.published,
                    summary=row.summary,
                )
                for row in FIXTURES
            ]
        )
        retry, retry_metrics = _capture(lambda: _materialize(queryset))
        repeated, repeated_metrics = _capture(lambda: _materialize(queryset))
        return _observed(
            contract_id,
            [
                _error_step("failed_evaluation", failure),
                _result_step("retry_after_schema_repair", retry),
                _result_step("repeat_after_success", repeated),
            ],
            [
                _metric_step("failed_evaluation", failure_metrics),
                _metric_step("retry_after_schema_repair", retry_metrics),
                _metric_step("repeat_after_success", repeated_metrics),
            ],
        )
    finally:
        with connection.schema_editor() as editor:
            editor.delete_model(Article)


def all_fresh_clone(contract_id: str) -> dict[str, Any]:
    with article_database():
        source = Article.objects.order_by("id")
        source_before, source_before_metrics = _capture(
            lambda: _materialize(source)
        )
        Article.objects.create(id=5, title="Clone", published=True, summary=None)
        clone, clone_construction_metrics = _capture(source.all)
        clone_after, clone_after_metrics = _capture(
            lambda: _materialize(clone)
        )
        source_after, source_after_metrics = _capture(
            lambda: _materialize(source)
        )
        return _observed(
            contract_id,
            [
                _result_step("source_before_insert", source_before),
                _result_step("fresh_copy_request", {"completed": True}),
                _result_step("clone_after_insert", clone_after),
                _result_step("source_after_insert", source_after),
            ],
            [
                _metric_step("source_before_insert", source_before_metrics),
                _metric_step("fresh_copy_request", clone_construction_metrics),
                _metric_step("clone_after_insert", clone_after_metrics),
                _metric_step("source_after_insert", source_after_metrics),
            ],
        )


def first_cold_and_warm(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.order_by("-id")
        first_before, first_before_metrics = _capture(
            lambda: _article_value(queryset.first())
        )
        Article.objects.create(
            id=5,
            title="First five",
            published=False,
            summary=None,
        )
        first_after, first_after_metrics = _capture(
            lambda: _article_value(queryset.first())
        )
        full_after, full_after_metrics = _capture(
            lambda: _materialize(queryset)
        )
        Article.objects.create(
            id=6,
            title="First six",
            published=True,
            summary=None,
        )
        first_cached, first_cached_metrics = _capture(
            lambda: _article_value(queryset.first())
        )
        return _observed(
            contract_id,
            [
                _result_step("first_before_insert", first_before),
                _result_step("first_after_first_insert", first_after),
                _result_step("full_evaluation_after_first_insert", full_after),
                _result_step(
                    "first_from_full_cache_after_second_insert",
                    first_cached,
                ),
            ],
            [
                _metric_step("first_before_insert", first_before_metrics),
                _metric_step("first_after_first_insert", first_after_metrics),
                _metric_step(
                    "full_evaluation_after_first_insert",
                    full_after_metrics,
                ),
                _metric_step(
                    "first_from_full_cache_after_second_insert",
                    first_cached_metrics,
                ),
            ],
        )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.query.cache.repeated_full_evaluation": repeated_full_evaluation,
    "django.query.cache.empty_full_evaluation": empty_full_evaluation,
    "django.query.cache.stale_snapshot_and_fresh_queryset": (
        stale_snapshot_and_fresh_queryset
    ),
    "django.query.cache.chained_queryset_independence": chained_queryset_independence,
    "django.query.cache.count_cold_and_warm": count_cold_and_warm,
    "django.query.cache.exists_cold_and_warm": exists_cold_and_warm,
    "django.query.cache.iterator_bypass": iterator_bypass,
    "django.query.cache.index_partial_evaluation": index_partial_evaluation,
    "django.query.cache.failed_evaluation_retry": failed_evaluation_retry,
    "django.query.cache.all_fresh_clone": all_fresh_clone,
    "django.query.cache.first_cold_and_warm": first_cold_and_warm,
}
