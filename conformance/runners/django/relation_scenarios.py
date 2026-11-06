"""Pinned Django observations for the first ForeignKey relation contracts.

These scenarios are independently authored behavioral observations. They use a
fresh two-app model registry and disposable SQLite tables for every contract;
the locked oracle and static GoDj fixture are never read by this module.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from django.core.exceptions import FieldError
from django.db import connection
from django.db.models.deletion import ProtectedError

from .normalizer import (
    PrimaryKey,
    normalize,
    normalize_sql_in_predicate_columns,
    normalize_sql_shape,
)
from .relation_fixture import relation_database
from .scenarios import configure_django


configure_django()


AUTHOR_NAME_PREDICATE = "Ada"
AUTHOR_ID_PREDICATE = 1
REVERSE_TITLE_PREDICATE = "Alpha"


@dataclass(frozen=True)
class _Statement:
    sql: str
    kind: str
    join_kinds: tuple[str, ...]
    parameters: tuple[Any, ...]
    affected_rows: int | None


class _StatementCapture:
    """Capture one explicit operation window without setup/teardown traffic."""

    def __init__(self) -> None:
        self.statements: list[_Statement] = []

    def _wrapper(self, execute, sql, params, many, context):
        shape = normalize_sql_shape(sql)
        result = execute(sql, params, many, context)
        kind = shape["statement_kind"]
        affected_rows = None
        if kind in {"INSERT", "UPDATE", "DELETE"}:
            rowcount = context["cursor"].rowcount
            affected_rows = rowcount if rowcount >= 0 else None
        if params is None:
            parameters: tuple[Any, ...] = ()
        elif many:
            parameters = tuple(tuple(item) for item in params)
        else:
            parameters = tuple(params)
        self.statements.append(
            _Statement(
                sql=sql,
                kind=kind,
                join_kinds=tuple(shape["join_kinds"]),
                parameters=parameters,
                affected_rows=affected_rows,
            )
        )
        return result

    def run(self, operation: Callable[[], Any]) -> Any:
        with connection.execute_wrapper(self._wrapper):
            return operation()

    def semantic_statements(self) -> list[_Statement]:
        return [
            statement
            for statement in self.statements
            if statement.kind in {"SELECT", "INSERT", "UPDATE", "DELETE"}
        ]

    def query_metrics(self) -> dict[str, Any]:
        return _query_metrics_for(self.semantic_statements())

    def mutation_metrics(self) -> dict[str, Any]:
        mutations = [
            statement
            for statement in self.semantic_statements()
            if statement.kind in {"UPDATE", "DELETE"}
        ]
        return {
            "transaction_count": sum(
                statement.kind == "BEGIN" for statement in self.statements
            ),
            "mutation_order": [statement.kind for statement in mutations],
            "mutation_rows": [
                {
                    "kind": statement.kind,
                    "affected_rows": statement.affected_rows,
                }
                for statement in mutations
            ],
            "update_statement_count": sum(
                statement.kind == "UPDATE" for statement in mutations
            ),
            "delete_statement_count": sum(
                statement.kind == "DELETE" for statement in mutations
            ),
        }


def _query_metrics_for(statements: list[_Statement]) -> dict[str, Any]:
    joins = [join for statement in statements for join in statement.join_kinds]
    return {
        "query_count": sum(statement.kind == "SELECT" for statement in statements),
        "statement_kinds": [statement.kind for statement in statements],
        "join_kinds": joins,
        "inner_join_count": joins.count("INNER"),
        "left_outer_join_count": joins.count("LEFT_OUTER"),
    }


def _combined_query_metrics(*captures: _StatementCapture) -> dict[str, Any]:
    statements = [
        statement
        for capture in captures
        for statement in capture.semantic_statements()
    ]
    return _query_metrics_for(statements)


def _assert_query_shape(
    capture: _StatementCapture,
    *,
    query_count: int,
    statement_kinds: list[str],
    join_kinds: list[str],
) -> None:
    metrics = capture.query_metrics()
    actual = {
        "query_count": metrics["query_count"],
        "statement_kinds": metrics["statement_kinds"],
        "join_kinds": metrics["join_kinds"],
    }
    expected = {
        "query_count": query_count,
        "statement_kinds": statement_kinds,
        "join_kinds": join_kinds,
    }
    if actual != expected:
        raise AssertionError(f"unexpected relation SQL shape: {actual!r} != {expected!r}")


def _ordered_rows(queryset: Any, fields: tuple[str, ...]) -> list[tuple[Any, ...]]:
    ordered = queryset.order_by("id")
    if tuple(ordered.query.order_by) != ("id",):
        raise AssertionError("relation result must use explicit total id ordering")
    return list(ordered.values_list(*fields))


def _author_value(author: Any | None) -> dict[str, Any] | None:
    if author is None:
        return None
    return {"id": PrimaryKey(author.pk), "name": author.name}


def _database_state(models: Any) -> dict[str, Any]:
    authors = [
        {"id": PrimaryKey(identifier), "name": name}
        for identifier, name in _ordered_rows(models.Author.objects, ("id", "name"))
    ]
    posts = [
        {
            "id": PrimaryKey(identifier),
            "title": title,
            "author_id": PrimaryKey(author_id),
            "reviewer_id": (
                PrimaryKey(reviewer_id) if reviewer_id is not None else None
            ),
        }
        for identifier, title, author_id, reviewer_id in _ordered_rows(
            models.Post.objects,
            ("id", "title", "author_id", "reviewer_id"),
        )
    ]
    return {"authors": authors, "posts": posts}


def _observed(
    contract_id: str,
    *,
    phase: str,
    result: Any,
    db_state: Any | None,
    metrics: Any | None,
    error: dict[str, Any] | None = None,
) -> dict[str, Any]:
    if (result is None) == (error is None):
        raise AssertionError("relation observation requires exactly one of result/error")
    return {
        "id": contract_id,
        "status": "observed",
        "phase": phase,
        "result": normalize(result) if result is not None else None,
        "error": error,
        "db_state": normalize(db_state) if db_state is not None else None,
        "metrics": normalize(metrics) if metrics is not None else None,
    }


def _normalized_error(error: BaseException, category: str, code: str) -> dict[str, Any]:
    return {
        "category": category,
        "code": code,
        "python_type": f"{type(error).__module__}.{type(error).__qualname__}",
        "message": str(error),
        "message_is_contract": False,
    }


def cross_app_metadata(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        forward = []
        for name in ("author", "reviewer"):
            field = fixture.Post._meta.get_field(name)
            target = field.remote_field.model._meta
            forward.append(
                {
                    "name": field.name,
                    "column": field.column,
                    "target": {
                        "app": target.app_label,
                        "model": target.model_name,
                    },
                    "nullable": field.null,
                    "reverse": field.remote_field.related_name,
                    "many_to_one": field.many_to_one,
                    "on_delete": field.remote_field.on_delete.__name__,
                }
            )

        reverse = []
        for name in ("posts", "reviewed_posts"):
            relation = fixture.Author._meta.get_field(name)
            source = relation.related_model._meta
            reverse.append(
                {
                    "name": relation.name,
                    "field": relation.field.name,
                    "target": {
                        "app": source.app_label,
                        "model": source.model_name,
                    },
                    "one_to_many": relation.one_to_many,
                }
            )

        return _observed(
            contract_id,
            phase="metadata",
            result={"forward": forward, "reverse": reverse},
            db_state=None,
            metrics=None,
        )


def unsaved_related_target(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        before = _database_state(fixture)
        post = fixture.Post(
            title="Unsaved",
            author=fixture.Author(name="Unsaved"),
        )
        capture = _StatementCapture()
        try:
            capture.run(post.save)
        except ValueError as error:
            _assert_query_shape(
                capture,
                query_count=0,
                statement_kinds=[],
                join_kinds=[],
            )
            after = _database_state(fixture)
            if after != before:
                raise AssertionError("unsaved relation failure mutated the fixture")
            return _observed(
                contract_id,
                phase="evaluation",
                result=None,
                error=_normalized_error(
                    error,
                    "model_state_error",
                    "unsaved_related_object",
                ),
                db_state=after,
                metrics={
                    **capture.query_metrics(),
                    "row_delta": {"authors": 0, "posts": 0},
                },
            )
        raise AssertionError("saving an unsaved related target must fail")


def forward_lazy_cache(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        post = fixture.Post.objects.get(pk=10)
        cold_capture = _StatementCapture()
        cold = cold_capture.run(lambda: _author_value(post.author))
        warm_capture = _StatementCapture()
        warm = warm_capture.run(lambda: _author_value(post.author))
        _assert_query_shape(
            cold_capture,
            query_count=1,
            statement_kinds=["SELECT"],
            join_kinds=[],
        )
        _assert_query_shape(
            warm_capture,
            query_count=0,
            statement_kinds=[],
            join_kinds=[],
        )
        if cold != warm:
            raise AssertionError("cold and warm relation access diverged")
        return _observed(
            contract_id,
            phase="evaluation",
            result={"cold": cold, "warm": warm},
            db_state=_database_state(fixture),
            metrics={
                "steps": [
                    {"name": "cold_access", **cold_capture.query_metrics()},
                    {"name": "warm_access", **warm_capture.query_metrics()},
                ]
            },
        )


def forward_lookup_join_reuse(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        cases = (
            (
                "one_predicate",
                {"author__name": AUTHOR_NAME_PREDICATE},
            ),
            (
                "two_predicates",
                {
                    "author__name": AUTHOR_NAME_PREDICATE,
                    "author__id": AUTHOR_ID_PREDICATE,
                },
            ),
        )
        results = []
        metrics = []
        for name, predicates in cases:
            construction = _StatementCapture()
            queryset = construction.run(
                lambda predicates=predicates: fixture.Post.objects.filter(
                    **predicates
                ).order_by("id")
            )
            evaluation = _StatementCapture()
            identifiers = evaluation.run(
                lambda queryset=queryset: [
                    PrimaryKey(value)
                    for value in queryset.values_list("id", flat=True)
                ]
            )
            _assert_query_shape(
                construction,
                query_count=0,
                statement_kinds=[],
                join_kinds=[],
            )
            _assert_query_shape(
                evaluation,
                query_count=1,
                statement_kinds=["SELECT"],
                join_kinds=["INNER"],
            )
            results.append({"name": name, "post_ids": identifiers})
            metrics.append(
                {
                    "name": name,
                    "construction": construction.query_metrics(),
                    "evaluation": evaluation.query_metrics(),
                }
            )
        if results[0]["post_ids"] != results[1]["post_ids"]:
            raise AssertionError("equivalent forward predicates diverged")
        return _observed(
            contract_id,
            phase="evaluation",
            result={"cases": results},
            db_state=_database_state(fixture),
            metrics={"cases": metrics},
        )


def reverse_accessor_and_lookup(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        ada = fixture.Author.objects.get(pk=1)
        accessor_capture = _StatementCapture()
        accessor = accessor_capture.run(
            lambda: [
                PrimaryKey(value)
                for value in ada.posts.order_by("id").values_list("id", flat=True)
            ]
        )
        lookup_capture = _StatementCapture()
        lookup = lookup_capture.run(
            lambda: [
                PrimaryKey(value)
                for value in fixture.Author.objects.filter(
                    posts__title=REVERSE_TITLE_PREDICATE
                )
                .order_by("id")
                .values_list("id", flat=True)
            ]
        )
        _assert_query_shape(
            accessor_capture,
            query_count=1,
            statement_kinds=["SELECT"],
            join_kinds=[],
        )
        _assert_query_shape(
            lookup_capture,
            query_count=1,
            statement_kinds=["SELECT"],
            join_kinds=["INNER"],
        )
        return _observed(
            contract_id,
            phase="evaluation",
            result={"accessor_post_ids": accessor, "lookup_author_ids": lookup},
            db_state=_database_state(fixture),
            metrics={
                "accessor": accessor_capture.query_metrics(),
                "lookup": lookup_capture.query_metrics(),
            },
        )


def nullable_access_and_isnull(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        post = fixture.Post.objects.get(pk=11)
        access_capture = _StatementCapture()
        reviewer = access_capture.run(lambda: _author_value(post.reviewer))
        queryset_capture = _StatementCapture()
        queryset = queryset_capture.run(
            lambda: fixture.Post.objects.filter(reviewer__isnull=True).order_by("id")
        )
        evaluation_capture = _StatementCapture()
        identifiers = evaluation_capture.run(
            lambda: [
                PrimaryKey(value) for value in queryset.values_list("id", flat=True)
            ]
        )
        _assert_query_shape(
            access_capture,
            query_count=0,
            statement_kinds=[],
            join_kinds=[],
        )
        _assert_query_shape(
            queryset_capture,
            query_count=0,
            statement_kinds=[],
            join_kinds=[],
        )
        _assert_query_shape(
            evaluation_capture,
            query_count=1,
            statement_kinds=["SELECT"],
            join_kinds=[],
        )
        if reviewer is not None:
            raise AssertionError("nullable relation access must return null")
        return _observed(
            contract_id,
            phase="evaluation",
            result={"reviewer": reviewer, "isnull_post_ids": identifiers},
            db_state=_database_state(fixture),
            metrics={
                "null_access": access_capture.query_metrics(),
                "isnull_construction": queryset_capture.query_metrics(),
                "isnull_evaluation": evaluation_capture.query_metrics(),
            },
        )


def protect_delete(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        before = _database_state(fixture)
        author = fixture.Author.objects.get(pk=1)
        capture = _StatementCapture()
        try:
            capture.run(author.delete)
        except ProtectedError as error:
            protected = len(error.protected_objects)
            after = _database_state(fixture)
            if after != before:
                raise AssertionError("PROTECT failure changed relation database state")
            mutation = capture.mutation_metrics()
            if mutation["mutation_order"]:
                raise AssertionError("PROTECT failure executed a mutation")
            return _observed(
                contract_id,
                phase="evaluation",
                result=None,
                error=_normalized_error(
                    error,
                    "integrity_error",
                    "protected_foreign_key",
                ),
                db_state=after,
                metrics={
                    "protected_source_rows": protected,
                    "update_statement_count": mutation["update_statement_count"],
                    "delete_statement_count": mutation["delete_statement_count"],
                },
            )
        raise AssertionError("deleting a protected relation target must fail")


def set_null_delete(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        author = fixture.Author.objects.get(pk=2)
        capture = _StatementCapture()
        deleted_total, deleted_by_model = capture.run(author.delete)
        metrics = capture.mutation_metrics()
        mutation_rows = metrics["mutation_rows"]
        if metrics["mutation_order"] != ["UPDATE", "DELETE"]:
            raise AssertionError("SET_NULL mutation order must be UPDATE then DELETE")
        if [row["affected_rows"] for row in mutation_rows] != [2, 1]:
            raise AssertionError("SET_NULL affected row counts changed")
        if metrics["transaction_count"] != 1:
            raise AssertionError("SET_NULL delete must execute in one transaction")
        state = _database_state(fixture)
        reviewer_values = [post["reviewer_id"] for post in state["posts"]]
        if reviewer_values != [None, None, None]:
            raise AssertionError("SET_NULL did not clear every matching reviewer")
        label = fixture.Author._meta.label
        return _observed(
            contract_id,
            phase="commit",
            result={
                "deleted_total": deleted_total,
                "target_deleted": deleted_by_model.get(label, 0),
            },
            db_state=state,
            metrics={
                **metrics,
                "affected_source_rows": mutation_rows[0]["affected_rows"],
                "deleted_target_rows": mutation_rows[1]["affected_rows"],
            },
        )


def required_select_related(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        plain_query_capture = _StatementCapture()
        plain_posts = plain_query_capture.run(
            lambda: list(fixture.Post.objects.order_by("id"))
        )
        plain_access_capture = _StatementCapture()
        plain = plain_access_capture.run(
            lambda: [
                (PrimaryKey(post.id), post.author.name) for post in plain_posts
            ]
        )
        eager_query_capture = _StatementCapture()
        eager_posts = eager_query_capture.run(
            lambda: list(
                fixture.Post.objects.select_related("author").order_by("id")
            )
        )
        eager_access_capture = _StatementCapture()
        eager = eager_access_capture.run(
            lambda: [
                (PrimaryKey(post.id), post.author.name) for post in eager_posts
            ]
        )
        _assert_query_shape(
            plain_query_capture,
            query_count=1,
            statement_kinds=["SELECT"],
            join_kinds=[],
        )
        _assert_query_shape(
            plain_access_capture,
            query_count=3,
            statement_kinds=["SELECT", "SELECT", "SELECT"],
            join_kinds=[],
        )
        _assert_query_shape(
            eager_query_capture,
            query_count=1,
            statement_kinds=["SELECT"],
            join_kinds=["INNER"],
        )
        _assert_query_shape(
            eager_access_capture,
            query_count=0,
            statement_kinds=[],
            join_kinds=[],
        )
        if plain != eager:
            raise AssertionError("plain and eager required relation results diverged")
        plain_metrics = _combined_query_metrics(
            plain_query_capture,
            plain_access_capture,
        )
        eager_metrics = _combined_query_metrics(
            eager_query_capture,
            eager_access_capture,
        )
        return _observed(
            contract_id,
            phase="evaluation",
            result={"plain": plain, "eager": eager},
            db_state=_database_state(fixture),
            metrics={
                "plain": {
                    **plain_metrics,
                    "access_extra_queries": plain_access_capture.query_metrics()[
                        "query_count"
                    ],
                },
                "eager": {
                    **eager_metrics,
                    "access_extra_queries": eager_access_capture.query_metrics()[
                        "query_count"
                    ],
                },
            },
        )


def nullable_select_related(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        query_capture = _StatementCapture()
        posts = query_capture.run(
            lambda: list(
                fixture.Post.objects.select_related("reviewer").order_by("id")
            )
        )
        access_capture = _StatementCapture()
        result = access_capture.run(
            lambda: [
                (
                    PrimaryKey(post.id),
                    post.reviewer.name if post.reviewer is not None else None,
                )
                for post in posts
            ]
        )
        _assert_query_shape(
            query_capture,
            query_count=1,
            statement_kinds=["SELECT"],
            join_kinds=["LEFT_OUTER"],
        )
        _assert_query_shape(
            access_capture,
            query_count=0,
            statement_kinds=[],
            join_kinds=[],
        )
        return _observed(
            contract_id,
            phase="evaluation",
            result={"rows": result},
            db_state=_database_state(fixture),
            metrics={
                **_combined_query_metrics(query_capture, access_capture),
                "access_extra_queries": access_capture.query_metrics()[
                    "query_count"
                ],
            },
        )


def invalid_reverse_select_related(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        before = _database_state(fixture)
        capture = _StatementCapture()
        try:
            capture.run(
                lambda: list(
                    fixture.Author.objects.select_related("posts").order_by("id")
                )
            )
        except FieldError as error:
            _assert_query_shape(
                capture,
                query_count=0,
                statement_kinds=[],
                join_kinds=[],
            )
            after = _database_state(fixture)
            if before != after:
                raise AssertionError("invalid select_related path mutated database state")
            return _observed(
                contract_id,
                phase="evaluation",
                result=None,
                error=_normalized_error(
                    error,
                    "field_error",
                    "invalid_related_path",
                ),
                db_state=after,
                metrics={**capture.query_metrics(), "mutation_count": 0},
            )
        raise AssertionError("reverse multi-valued select_related path must fail")


def reverse_prefetch(contract_id: str) -> dict[str, Any]:
    with relation_database() as fixture:
        if tuple(fixture.Post._meta.ordering) != ("id",):
            raise AssertionError("reverse prefetch requires total post id ordering")
        query_capture = _StatementCapture()
        authors = query_capture.run(
            lambda: list(
                fixture.Author.objects.prefetch_related("posts").order_by("id")
            )
        )
        access_capture = _StatementCapture()
        result = access_capture.run(
            lambda: [
                (
                    PrimaryKey(author.id),
                    [PrimaryKey(post.id) for post in author.posts.all()],
                )
                for author in authors
            ]
        )
        _assert_query_shape(
            query_capture,
            query_count=2,
            statement_kinds=["SELECT", "SELECT"],
            join_kinds=[],
        )
        _assert_query_shape(
            access_capture,
            query_count=0,
            statement_kinds=[],
            join_kinds=[],
        )
        selects = [
            statement
            for statement in query_capture.semantic_statements()
            if statement.kind == "SELECT"
        ]
        batch_parameters = selects[1].parameters
        if tuple(batch_parameters) != (1, 2, 3):
            raise AssertionError(
                f"reverse prefetch batch keys changed: {batch_parameters!r}"
            )
        batch_columns = normalize_sql_in_predicate_columns(selects[1].sql)
        if batch_columns != ["author_id"]:
            raise AssertionError(
                f"reverse prefetch batch predicate changed: {batch_columns!r}"
            )
        batch_query_count = sum(
            normalize_sql_in_predicate_columns(statement.sql) == ["author_id"]
            for statement in selects
        )
        return _observed(
            contract_id,
            phase="evaluation",
            result={"authors": result},
            db_state=_database_state(fixture),
            metrics={
                **_combined_query_metrics(query_capture, access_capture),
                "primary_query_count": len(selects) - batch_query_count,
                "batch_query_count": batch_query_count,
                "batch_predicate_column": batch_columns[0],
                "batch_key_count": len(batch_parameters),
                "related_access_extra_queries": access_capture.query_metrics()[
                    "query_count"
                ],
            },
        )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.relation.cross_app_metadata": cross_app_metadata,
    "django.relation.unsaved_related_target": unsaved_related_target,
    "django.relation.forward_lazy_cache": forward_lazy_cache,
    "django.relation.forward_lookup_join_reuse": forward_lookup_join_reuse,
    "django.relation.reverse_accessor_and_lookup": reverse_accessor_and_lookup,
    "django.relation.nullable_access_and_isnull": nullable_access_and_isnull,
    "django.relation.protect_delete": protect_delete,
    "django.relation.set_null_delete": set_null_delete,
    "django.relation.required_select_related": required_select_related,
    "django.relation.nullable_select_related": nullable_select_related,
    "django.relation.invalid_reverse_select_related": invalid_reverse_select_related,
    "django.relation.reverse_prefetch": reverse_prefetch,
}
