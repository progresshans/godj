"""Independent Django adapters for mutable model ``save()`` contracts."""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from django.db import IntegrityError, connection, transaction

from .normalizer import PrimaryKey, normalize
from .scenarios import Article, _database_state
from .write_migration_scenarios import empty_article_database


class _ForcedRollback(RuntimeError):
    pass


def _instance_state(article: Article) -> dict[str, Any]:
    """Return public field values without exposing Django's private state object."""

    return {
        "pk": PrimaryKey(article.pk) if article.pk is not None else None,
        "published": article.published,
        "summary": article.summary,
        "title": article.title,
    }


def _statement_kind(sql: str) -> str:
    rendered = sql.lstrip().upper()
    for prefix, kind in (
        ("ROLLBACK TO SAVEPOINT", "ROLLBACK_TO_SAVEPOINT"),
        ("RELEASE SAVEPOINT", "RELEASE_SAVEPOINT"),
        ("SAVEPOINT", "SAVEPOINT"),
    ):
        if rendered.startswith(prefix):
            return kind
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


def _observed(
    contract_id: str,
    *,
    phase: str,
    result: Any | None = None,
    error: dict[str, Any] | None = None,
    metrics: dict[str, Any],
) -> dict[str, Any]:
    if (result is None) == (error is None):
        raise AssertionError("save observation requires exactly one of result or error")
    return {
        "id": contract_id,
        "status": "observed",
        "phase": phase,
        "result": normalize(result) if result is not None else None,
        "error": error,
        "db_state": normalize(_database_state()),
        "metrics": normalize(metrics),
    }


def _capture_expected_error(
    operation: Callable[[], Any],
    expected: type[BaseException] | tuple[type[BaseException], ...],
) -> tuple[BaseException, dict[str, Any]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_kind(sql))
        return execute(sql, params, many, context)

    try:
        with connection.execute_wrapper(wrapper):
            operation()
    except expected as error:
        return error, {
            "query_count": len(statements),
            "statement_kinds": statements,
        }
    raise AssertionError("expected save operation to fail")


def _save_error_observed(
    contract_id: str,
    *,
    phase: str,
    category: str,
    code: str,
    expected: type[BaseException] | tuple[type[BaseException], ...],
    operation: Callable[[], Any],
) -> dict[str, Any]:
    error, metrics = _capture_expected_error(operation, expected)
    return _observed(
        contract_id,
        phase=phase,
        error={
            "category": category,
            "code": code,
            "python_type": f"{type(error).__module__}.{type(error).__qualname__}",
            "message": str(error),
            "message_is_contract": False,
        },
        metrics=metrics,
    )


def model_save_new_auto_pk(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        article = Article(title="New save", published=True, summary="Created")
        before = _instance_state(article)
        _, metrics = _capture(article.save)
        return _observed(
            contract_id,
            phase="commit",
            result={"after": _instance_state(article), "before": before},
            metrics=metrics,
        )


def model_save_loaded_all_fields(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        created = Article.objects.create(
            title="Before",
            published=False,
            summary="Loaded summary",
        )
        loaded = Article.objects.get(pk=created.pk)
        Article.objects.filter(pk=created.pk).update(
            published=True,
            summary="Concurrent database value",
        )
        loaded.title = "After default save"
        _, metrics = _capture(loaded.save)
        return _observed(
            contract_id,
            phase="commit",
            result={"instance_after": _instance_state(loaded)},
            metrics=metrics,
        )


def model_save_update_fields_named(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        created = Article.objects.create(
            title="Before",
            published=False,
            summary="Loaded summary",
        )
        loaded = Article.objects.get(pk=created.pk)
        Article.objects.filter(pk=created.pk).update(
            published=False,
            summary="Database preserved",
        )
        loaded.title = "Only title persists"
        loaded.published = True
        loaded.summary = "Memory only"
        _, metrics = _capture(lambda: loaded.save(update_fields=["title"]))
        return _observed(
            contract_id,
            phase="commit",
            result={"instance_after": _instance_state(loaded)},
            metrics=metrics,
        )


def model_save_update_fields_empty(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        loaded = Article.objects.create(
            title="Persisted",
            published=False,
            summary="Database",
        )
        loaded.title = "Memory only"
        loaded.published = True
        loaded.summary = "Also memory only"
        _, metrics = _capture(lambda: loaded.save(update_fields=[]))
        return _observed(
            contract_id,
            phase="evaluation",
            result={"instance_after": _instance_state(loaded)},
            metrics=metrics,
        )


def model_save_update_fields_primary_key(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        article = Article.objects.create(title="Unchanged")
        article.title = "Rejected"
        return _save_error_observed(
            contract_id,
            phase="evaluation",
            category="field_error",
            code="primary_key_update_field",
            expected=ValueError,
            operation=lambda: article.save(update_fields=["id"]),
        )


def model_save_force_insert_conflict(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        inserted = Article.objects.create(title="Existing")
        conflicting = Article(id=inserted.pk, title="Force insert conflict")
        return _save_error_observed(
            contract_id,
            phase="evaluation",
            category="integrity_error",
            code="unique_primary_key",
            expected=IntegrityError,
            operation=lambda: conflicting.save(force_insert=True),
        )


def model_save_force_update_without_pk(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        article = Article(title="No primary key")
        return _save_error_observed(
            contract_id,
            phase="evaluation",
            category="model_state_error",
            code="force_update_without_primary_key",
            expected=ValueError,
            operation=lambda: article.save(force_update=True),
        )


def model_save_force_update_missing_row(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        article = Article(id=999, title="Missing row")
        return _save_error_observed(
            contract_id,
            phase="evaluation",
            category="not_updated",
            code="force_update_missing_row",
            expected=Article.NotUpdated,
            operation=lambda: article.save(force_update=True),
        )


def model_save_mutually_exclusive_force_flags(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        article = Article(title="Mutually exclusive")
        return _save_error_observed(
            contract_id,
            phase="evaluation",
            category="argument_error",
            code="mutually_exclusive_force_flags",
            expected=ValueError,
            operation=lambda: article.save(force_insert=True, force_update=True),
        )


def model_save_explicit_pk_existing(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        Article.objects.create(id=41, title="Existing")
        article = Article(id=41, title="Updated existing", published=True)
        _, metrics = _capture(article.save)
        return _observed(
            contract_id,
            phase="commit",
            result={"instance_after": _instance_state(article)},
            metrics=metrics,
        )


def model_save_explicit_pk_missing(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        article = Article(
            id=42,
            title="Inserted missing",
            summary="Fallback insert",
        )
        _, metrics = _capture(article.save)
        return _observed(
            contract_id,
            phase="commit",
            result={"instance_after": _instance_state(article)},
            metrics=metrics,
        )


def model_save_atomic_rollback_instance_state(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        sentinel = Article.objects.create(
            title="Persisted before transaction",
            summary="Original",
        )
        created = Article(title="Rolled back new instance", published=True)

        def rollback_operation() -> None:
            with transaction.atomic():
                sentinel.title = "Memory after rollback"
                sentinel.summary = "Memory summary after rollback"
                sentinel.save(update_fields=["title", "summary"])
                created.save()
                raise _ForcedRollback("forced rollback")

        _, metrics = _capture_expected_error(rollback_operation, _ForcedRollback)
        return _observed(
            contract_id,
            phase="rollback",
            result={
                "created_instance_after": _instance_state(created),
                "rollback_triggered": True,
                "sentinel_instance_after": _instance_state(sentinel),
            },
            metrics=metrics,
        )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.model.save.atomic_rollback_instance_state": (
        model_save_atomic_rollback_instance_state
    ),
    "django.model.save.explicit_pk_existing": model_save_explicit_pk_existing,
    "django.model.save.explicit_pk_missing": model_save_explicit_pk_missing,
    "django.model.save.force_insert_conflict": model_save_force_insert_conflict,
    "django.model.save.force_update_missing_row": model_save_force_update_missing_row,
    "django.model.save.force_update_without_pk": model_save_force_update_without_pk,
    "django.model.save.loaded_all_fields": model_save_loaded_all_fields,
    "django.model.save.mutually_exclusive_force_flags": (
        model_save_mutually_exclusive_force_flags
    ),
    "django.model.save.new_auto_pk": model_save_new_auto_pk,
    "django.model.save.update_fields_empty": model_save_update_fields_empty,
    "django.model.save.update_fields_named": model_save_update_fields_named,
    "django.model.save.update_fields_primary_key": (
        model_save_update_fields_primary_key
    ),
}
