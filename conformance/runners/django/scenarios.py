"""Explicit Django adapters for the initial GoDj compatibility contracts."""

from __future__ import annotations

from collections.abc import Callable, Iterator
from contextlib import contextmanager
from typing import Any

from django.conf import settings


_configured_by_godj = False


def configure_django() -> None:
    global _configured_by_godj

    if settings.configured:
        if _configured_by_godj:
            return
        raise RuntimeError(
            "refusing to use externally configured Django settings; "
            "the conformance runner requires its isolated in-memory default database"
        )

    settings.configure(
        DATABASES={
            "default": {
                "ENGINE": "django.db.backends.sqlite3",
                "NAME": ":memory:",
            }
        },
        DEFAULT_AUTO_FIELD="django.db.models.AutoField",
        INSTALLED_APPS=[
            "conformance.runners.django.migration_fixture.apps.GoDjMigrationFixtureConfig",
            "conformance.runners.django.migration_failure_fixture.apps.GoDjMigrationFailureFixtureConfig",
            "conformance.runners.django.migration_relation_fixture.apps.GoDjMigrationRelationFixtureConfig",
        ],
        LANGUAGE_CODE="en-us",
        SECRET_KEY="godj-conformance-not-a-secret",
        TIME_ZONE="UTC",
        USE_I18N=False,
        USE_TZ=True,
    )
    _configured_by_godj = True

    import django

    try:
        django.setup()
    except Exception:
        _configured_by_godj = False
        raise


configure_django()

from django.core.exceptions import FieldError  # noqa: E402
from django.db import connection, models  # noqa: E402

from .normalizer import PrimaryKey, normalize  # noqa: E402


class Article(models.Model):
    title = models.CharField(max_length=200)
    published = models.BooleanField(default=False)
    summary = models.CharField(max_length=200, null=True)

    class Meta:
        app_label = "godj_conformance"
        db_table = "godj_conformance_article"


FIXTURES = (
    Article(id=1, title="Alpine Guide", published=True, summary=None),
    Article(id=2, title="django Tips", published=False, summary="ORM"),
    Article(id=3, title="Django Deep Dive", published=True, summary=""),
    Article(id=4, title="Other", published=True, summary=None),
)


@contextmanager
def article_database() -> Iterator[None]:
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
        yield
    finally:
        with connection.schema_editor() as editor:
            editor.delete_model(Article)


def _rows(queryset: Any) -> list[dict[str, Any]]:
    if not queryset.totally_ordered:
        raise AssertionError("ordered result contract requires total ordering")
    return [
        {
            "id": PrimaryKey(pk),
            "published": published,
            "summary": summary,
            "title": title,
        }
        for pk, title, published, summary in queryset.values_list(
            "id", "title", "published", "summary"
        )
    ]


def _database_state() -> dict[str, Any]:
    return {
        "articles": _rows(Article.objects.order_by("id")),
    }


def _observed(
    contract_id: str,
    result: Any,
    *,
    phase: str = "evaluation",
    metrics: Any | None = None,
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": phase,
        "result": normalize(result),
        "error": None,
        "db_state": normalize(_database_state()),
        "metrics": normalize(metrics) if metrics is not None else None,
    }


def query_exact(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.filter(title="Alpine Guide").order_by("id")
        return _observed(contract_id, _rows(queryset))


def query_ascii_icontains(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.filter(title__icontains="django").order_by("id")
        return _observed(contract_id, _rows(queryset))


def query_chained_and(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = (
            Article.objects.filter(title__icontains="django")
            .filter(published=True)
            .order_by("id")
        )
        return _observed(contract_id, _rows(queryset))


def query_chain_preserves_source(contract_id: str) -> dict[str, Any]:
    with article_database():
        base = Article.objects.filter(published=True)
        derived = base.filter(title="Django Deep Dive")
        result = {
            "base": _rows(base.order_by("id")),
            "derived": _rows(derived.order_by("id")),
        }
        return _observed(contract_id, result)


def query_order_and_limit(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.order_by("-title", "id")[:2]
        return _observed(contract_id, _rows(queryset))


def query_empty_result(contract_id: str) -> dict[str, Any]:
    with article_database():
        queryset = Article.objects.filter(title="missing").order_by("id")
        return _observed(contract_id, _rows(queryset))


def query_isnull(contract_id: str) -> dict[str, Any]:
    with article_database():
        result = {
            "false": _rows(
                Article.objects.filter(summary__isnull=False).order_by("id")
            ),
            "true": _rows(
                Article.objects.filter(summary__isnull=True).order_by("id")
            ),
        }
        return _observed(contract_id, result)


def _field_error(
    contract_id: str,
    code: str,
    operation: Callable[[], Any],
) -> dict[str, Any]:
    with article_database():
        try:
            operation()
        except FieldError as exc:
            return {
                "id": contract_id,
                "status": "observed",
                "phase": "construction",
                "result": None,
                "error": {
                    "category": "field_error",
                    "code": code,
                    "python_type": f"{type(exc).__module__}.{type(exc).__qualname__}",
                    "message": str(exc),
                    "message_is_contract": False,
                },
                "db_state": normalize(_database_state()),
                "metrics": None,
            }
    raise AssertionError("expected django.core.exceptions.FieldError")


def query_unknown_field(contract_id: str) -> dict[str, Any]:
    return _field_error(
        contract_id,
        "unknown_field",
        lambda: Article.objects.filter(unknown_field="value"),
    )


def query_construction_has_no_io(contract_id: str) -> dict[str, Any]:
    with article_database():
        statements: list[str] = []

        def capture(execute, sql, params, many, context):
            statements.append(sql)
            return execute(sql, params, many, context)

        with connection.execute_wrapper(capture):
            queryset = Article.objects.filter(published=True).order_by("id")[:2]

        return _observed(
            contract_id,
            {"queryset_constructed": queryset is not None},
            phase="construction",
            metrics={"queries_during_construction": len(statements)},
        )


def query_unsupported_lookup(contract_id: str) -> dict[str, Any]:
    return _field_error(
        contract_id,
        "unsupported_lookup",
        lambda: Article.objects.filter(title__starts="Django"),
    )


def schema_model_metadata(contract_id: str) -> dict[str, Any]:
    fields = []
    for field in Article._meta.get_fields():
        fields.append(
            {
                "internal_type": field.get_internal_type(),
                "max_length": field.max_length,
                "name": field.name,
                "null": field.null,
                "primary_key": field.primary_key,
            }
        )
    return {
        "id": contract_id,
        "status": "observed",
        "phase": "metadata",
        "result": normalize(
            {
                "db_table": Article._meta.db_table,
                "fields": fields,
                "model_name": Article._meta.model_name,
            }
        ),
        "error": None,
        "db_state": None,
        "metrics": None,
    }


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.query.exact": query_exact,
    "django.query.ascii_icontains": query_ascii_icontains,
    "django.query.chained_and": query_chained_and,
    "django.query.chain_preserves_source": query_chain_preserves_source,
    "django.query.order_and_limit": query_order_and_limit,
    "django.query.empty_result": query_empty_result,
    "django.query.isnull": query_isnull,
    "django.query.unknown_field": query_unknown_field,
    "django.query.construction_has_no_io": query_construction_has_no_io,
    "django.query.unsupported_lookup": query_unsupported_lookup,
    "django.schema.model_metadata": schema_model_metadata,
}
