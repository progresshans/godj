"""Independent Django adapters for the M2 write and migration contracts."""

from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager
from typing import Any

from django.core.management import call_command
from django.db import connection, transaction
from django.db.migrations.recorder import MigrationRecorder

from .migration_failure_fixture.failure import (
    FAILURE_OPERATION_SENTINEL,
    ConformanceMigrationOperationFailure,
)
from .normalizer import PrimaryKey, normalize
from .scenarios import Article, _database_state, _rows


MIGRATION_APP = "godj_migration"
MIGRATION_TABLE = "godj_migration_article"
FAILURE_APP = "godj_failure"
FAILURE_TABLE = "godj_failure_broken"
MANAGED_TABLE_PREFIXES = ("godj_failure_", "godj_migration_")
ROLLBACK_SENTINEL_TITLE = "Rollback sentinel"
ROLLBACK_SENTINEL_SUMMARY = "Preserved before transaction"


@contextmanager
def empty_article_database() -> Iterator[None]:
    with connection.schema_editor() as editor:
        editor.create_model(Article)
    try:
        yield
    finally:
        with connection.schema_editor() as editor:
            editor.delete_model(Article)


def _article_observed(
    contract_id: str,
    result: Any,
    *,
    phase: str = "commit",
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": phase,
        "result": normalize(result),
        "error": None,
        "db_state": normalize(_database_state()),
        "metrics": None,
    }


def model_create_auto_pk(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        article = Article.objects.create(
            title="Created",
            published=True,
            summary="Written",
        )
        return _article_observed(
            contract_id,
            {
                "pk": PrimaryKey(article.pk),
                "row": _rows(
                    Article.objects.filter(pk=article.pk).order_by("id")
                )[0],
            },
        )


def model_nullable_create_variants(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        omitted = Article.objects.create(title="Omitted")
        explicit_null = Article.objects.create(title="Explicit NULL", summary=None)
        empty = Article.objects.create(title="Empty", summary="")
        return _article_observed(
            contract_id,
            {
                "empty": _rows(
                    Article.objects.filter(pk=empty.pk).order_by("id")
                )[0],
                "explicit_null": _rows(
                    Article.objects.filter(pk=explicit_null.pk).order_by("id")
                )[0],
                "omitted": _rows(
                    Article.objects.filter(pk=omitted.pk).order_by("id")
                )[0],
            },
        )


def model_partial_update(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        article = Article.objects.create(
            title="Before",
            published=False,
            summary="Persisted",
        )
        article.title = "After"
        article.published = True
        article.summary = "Memory only"
        article.save(update_fields=["title"])
        article.refresh_from_db()
        return _article_observed(
            contract_id,
            {
                "persisted": _rows(
                    Article.objects.filter(pk=article.pk).order_by("id")
                )[0],
            },
        )


def model_nullable_update(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        article = Article.objects.create(title="Nullable", summary="Before")
        article.summary = None
        article.save(update_fields=["summary"])
        article.refresh_from_db()
        return _article_observed(
            contract_id,
            {
                "persisted": _rows(
                    Article.objects.filter(pk=article.pk).order_by("id")
                )[0],
            },
        )


def model_instance_delete(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        Article.objects.create(title="Keep me")
        article = Article.objects.create(title="Delete me")
        primary_key_before = PrimaryKey(article.pk)
        deleted_count, _ = article.delete()
        return _article_observed(
            contract_id,
            {
                "deleted_count": deleted_count,
                "pk_after": article.pk,
                "pk_before": primary_key_before,
            },
        )


def model_atomic_commit(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        Article.objects.create(title="Before transaction")
        with transaction.atomic():
            article = Article.objects.create(title="Committed")
            count_inside = Article.objects.count()
        return _article_observed(
            contract_id,
            {
                "count_after": Article.objects.count(),
                "count_inside": count_inside,
                "pk": PrimaryKey(article.pk),
            },
        )


class _ForcedRollback(RuntimeError):
    pass


def model_atomic_rollback(contract_id: str) -> dict[str, Any]:
    with empty_article_database():
        sentinel = Article.objects.create(
            title=ROLLBACK_SENTINEL_TITLE,
            summary=ROLLBACK_SENTINEL_SUMMARY,
        )
        try:
            with transaction.atomic():
                sentinel.title = "Mutated inside transaction"
                sentinel.summary = "Must roll back"
                sentinel.save(update_fields=["title", "summary"])
                Article.objects.create(title="Rolled back")
                raise _ForcedRollback("forced rollback")
        except _ForcedRollback as error:
            return {
                "id": contract_id,
                "status": "observed",
                "phase": "rollback",
                "result": None,
                "error": {
                    "category": "application_error",
                    "code": "forced_rollback",
                    "python_type": (
                        f"{type(error).__module__}.{type(error).__qualname__}"
                    ),
                    "message": str(error),
                    "message_is_contract": False,
                },
                "db_state": normalize(_database_state()),
                "metrics": None,
            }
        raise AssertionError("expected transaction rollback")


def _type_family(type_code: Any) -> str:
    rendered = str(type_code).lower()
    if "int" in rendered:
        return "integer"
    if "char" in rendered or "clob" in rendered or "text" in rendered:
        return "text"
    if "bool" in rendered:
        return "boolean"
    return rendered


def _table_schema(table: str) -> dict[str, Any]:
    if table not in connection.introspection.table_names():
        return {"columns": [], "exists": False, "name": table}
    with connection.cursor() as cursor:
        description = connection.introspection.get_table_description(cursor, table)
        constraints = connection.introspection.get_constraints(cursor, table)
    primary_key_columns = {
        column
        for constraint in constraints.values()
        if constraint["primary_key"]
        for column in constraint["columns"]
    }
    return {
        "columns": [
            {
                "has_database_default": column.default is not None,
                "name": column.name,
                "nullable": column.null_ok,
                "primary_key": column.name in primary_key_columns,
                "type_family": _type_family(column.type_code),
            }
            for column in description
        ],
        "exists": True,
        "name": table,
    }


def _migration_records() -> list[dict[str, str]]:
    recorder = MigrationRecorder(connection)
    if not recorder.has_table():
        return []
    return [
        {"app": app, "name": name}
        for app, name in sorted(recorder.applied_migrations())
    ]


def _table_rows(table: str, fields: tuple[str, ...]) -> list[dict[str, Any]]:
    quoted_fields = [connection.ops.quote_name("id")]
    quoted_fields.extend(connection.ops.quote_name(field) for field in fields)
    with connection.cursor() as cursor:
        cursor.execute(
            f"SELECT {', '.join(quoted_fields)} "
            f"FROM {connection.ops.quote_name(table)} "
            f"ORDER BY {connection.ops.quote_name('id')}"
        )
        values_list = cursor.fetchall()
    rows = []
    for values in values_list:
        row: dict[str, Any] = {"id": PrimaryKey(values[0])}
        row.update(zip(fields, values[1:], strict=True))
        rows.append(row)
    return rows


def _migration_state(
    *,
    fields: tuple[str, ...] = (),
    table: str = MIGRATION_TABLE,
) -> dict[str, Any]:
    schema = _table_schema(table)
    return {
        "managed_tables": sorted(
            candidate
            for candidate in connection.introspection.table_names()
            if candidate.startswith(MANAGED_TABLE_PREFIXES)
        ),
        "migration_records": _migration_records(),
        "rows": _table_rows(table, fields) if schema["exists"] and fields else [],
        "schema": schema,
    }


def _migration_observed(
    contract_id: str,
    db_state: dict[str, Any],
    *,
    phase: str,
    result: Any | None = None,
    error: dict[str, Any] | None = None,
    metrics: Any | None = None,
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": phase,
        "result": normalize(result) if result is not None else None,
        "error": error,
        "db_state": normalize(db_state),
        "metrics": normalize(metrics) if metrics is not None else None,
    }


def _migrate(app_label: str, target: str) -> None:
    call_command(
        "migrate",
        app_label,
        target,
        database=connection.alias,
        interactive=False,
        verbosity=0,
    )


def _insert_migration_article(
    title: str,
    *,
    published: bool = False,
    summary: str | None = None,
    include_summary: bool = False,
) -> None:
    columns = ["title", "published"]
    values: list[Any] = [title, published]
    if include_summary:
        columns.append("summary")
        values.append(summary)
    quoted_columns = ", ".join(connection.ops.quote_name(column) for column in columns)
    placeholders = ", ".join(["%s"] * len(values))
    with connection.cursor() as cursor:
        cursor.execute(
            f"INSERT INTO {connection.ops.quote_name(MIGRATION_TABLE)} "
            f"({quoted_columns}) VALUES ({placeholders})",
            values,
        )


def _cleanup_migrations() -> None:
    _migrate(FAILURE_APP, "zero")
    _migrate(MIGRATION_APP, "zero")
    recorder = MigrationRecorder(connection)
    if recorder.has_table():
        with connection.schema_editor() as editor:
            editor.delete_model(recorder.Migration)


def migration_create_model(contract_id: str) -> dict[str, Any]:
    try:
        _migrate(MIGRATION_APP, "0001_initial")
        return _migration_observed(
            contract_id,
            _migration_state(
                fields=("title", "published"),
            ),
            phase="commit",
        )
    finally:
        _cleanup_migrations()


def migration_add_nullable_field(contract_id: str) -> dict[str, Any]:
    try:
        _migrate(MIGRATION_APP, "0001_initial")
        _insert_migration_article("Existing")
        _migrate(MIGRATION_APP, "0002_summary")
        return _migration_observed(
            contract_id,
            _migration_state(
                fields=("title", "published", "summary"),
            ),
            phase="commit",
        )
    finally:
        _cleanup_migrations()


def migration_reverse_nullable_field(contract_id: str) -> dict[str, Any]:
    try:
        _migrate(MIGRATION_APP, "0001_initial")
        _insert_migration_article("Existing")
        _migrate(MIGRATION_APP, "0002_summary")
        _insert_migration_article(
            "After add",
            summary="Discarded column",
            include_summary=True,
        )
        _migrate(MIGRATION_APP, "0001_initial")
        return _migration_observed(
            contract_id,
            _migration_state(
                fields=("title", "published"),
            ),
            phase="commit",
        )
    finally:
        _cleanup_migrations()


def migration_atomic_failure(contract_id: str) -> dict[str, Any]:
    try:
        try:
            _migrate(FAILURE_APP, "0001_failure")
        except ConformanceMigrationOperationFailure as error:
            if error.operation_sentinel != FAILURE_OPERATION_SENTINEL:
                raise AssertionError("migration failure operation sentinel mismatch")
            with connection.cursor() as cursor:
                cursor.execute("SELECT 1")
                query_result = cursor.fetchone()[0]
            with transaction.atomic():
                with connection.cursor() as cursor:
                    cursor.execute("SELECT 2")
                    transaction_result = cursor.fetchone()[0]
            return _migration_observed(
                contract_id,
                _migration_state(
                    table=FAILURE_TABLE,
                ),
                phase="rollback",
                error={
                    "category": "migration_execution_error",
                    "code": "operation_failed",
                    "python_type": f"{type(error).__module__}.{type(error).__qualname__}",
                    "message": str(error),
                    "message_is_contract": False,
                },
                metrics={
                    "connection_recovery": {
                        "autocommit_restored": connection.get_autocommit(),
                        "outside_atomic": not connection.in_atomic_block,
                        "subsequent_query_result": query_result,
                        "subsequent_transaction_result": transaction_result,
                    }
                },
            )
        raise AssertionError("expected migration operation failure")
    finally:
        _cleanup_migrations()


SCENARIOS = {
    "django.migration.add_nullable_field": migration_add_nullable_field,
    "django.migration.atomic_failure": migration_atomic_failure,
    "django.migration.create_model": migration_create_model,
    "django.migration.reverse_nullable_field": migration_reverse_nullable_field,
    "django.model.create_auto_pk": model_create_auto_pk,
    "django.model.create_nullable_variants": model_nullable_create_variants,
    "django.model.instance_delete": model_instance_delete,
    "django.model.partial_update_explicit_null": model_nullable_update,
    "django.model.partial_update_omits_changed_field": model_partial_update,
    "django.transaction.atomic_commit": model_atomic_commit,
    "django.transaction.atomic_rollback": model_atomic_rollback,
}
