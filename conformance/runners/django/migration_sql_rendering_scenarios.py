"""Pinned Django 6.1 result observations for forward migration SQL.

Only two overlapping results are published: the target-before state and
forward operation order used by Django's real ``sqlmigrate`` path, and the
normalized SQLite meaning of one ``CreateModel`` followed by one ``AddField``.
Raw SQL bytes, comments, transaction wrappers, prefix matching, backwards SQL,
and every GoDj-owned process/resource boundary are intentionally excluded.
"""

from __future__ import annotations

import re
from collections.abc import Callable, Sequence
from io import StringIO
from typing import Any
from unittest.mock import patch

import django

from .scenarios import configure_django


configure_django()

from django.core.management.commands import sqlmigrate  # noqa: E402
from django.db import connection, models  # noqa: E402
from django.db.migrations.graph import MigrationGraph  # noqa: E402
from django.db.migrations.loader import MigrationLoader  # noqa: E402
from django.db.migrations.migration import Migration  # noqa: E402
from django.db.migrations.operations.fields import AddField  # noqa: E402
from django.db.migrations.operations.models import CreateModel  # noqa: E402
from django.db.migrations.recorder import MigrationRecorder  # noqa: E402

from .normalizer import normalize  # noqa: E402


SET_SLUG = "migration-sql-rendering"
DJANGO_61_COMMIT = "fe0a859f537d4238cf49fca39073513206f83122"
DJANGO_61_GIT_BLOBS = {
    "django/core/management/base.py": "8f2447905064bf3838a16ecee25f8e31a5feb472",
    "django/core/management/commands/sqlmigrate.py": (
        "3c2e25eeeaff217e7bf001b5d6d45a882908d3eb"
    ),
    "django/db/backends/base/schema.py": ("9857eea57107c37a8c45d4aa0276ca775e70d162"),
    "django/db/backends/sqlite3/schema.py": (
        "47edec8f1ccc5b8c9309a41aabac9414a4e9e079"
    ),
    "django/db/migrations/loader.py": ("af2d521d893f1a657d6a8edda72ae590831a60ee"),
    "django/db/migrations/migration.py": ("2041a28780bc8f0d4e3556688fa414051dee7244"),
    "django/db/migrations/operations/fields.py": (
        "72b54382ef4902d599d7b62900cd677aac208f0c"
    ),
    "django/db/migrations/operations/models.py": (
        "1b241230df922b9bc2350858da3604c9d1b01eef"
    ),
    "tests/migrations/test_commands.py": ("61336f55332844bfd372b97aa6a7b1fad6cca027"),
}
DJANGO_61_SOURCE_RANGES = {
    "BaseCommand.execute": (441, 476),
    "sqlmigrate.Command.execute": (34, 38),
    "sqlmigrate.Command.handle": (40, 83),
    "MigrationLoader.project_state": (402, 411),
    "MigrationLoader.collect_sql": (413, 433),
    "Migration.apply": (94, 137),
    "CreateModel.database_forwards": (97, 111),
    "AddField.database_forwards": (111, 123),
    "BaseDatabaseSchemaEditor.create_model": (516, 549),
    "BaseDatabaseSchemaEditor.add_field": (760, 847),
    "SQLiteDatabaseSchemaEditor.add_field": (302, 331),
    "MigrateTests.test_sqlmigrate_forwards": (908, 964),
}
DJANGO_61_CHECKOUT_ONLY_SOURCE = {
    "tests/migrations/test_commands.py": {
        "bytes": 153_217,
        "git_blob": "61336f55332844bfd372b97aa6a7b1fad6cca027",
        "sha256": ("15b8ca276a4aca3237cbadb062947c1b052fa095c6425d02b4dd7dffb455bcca"),
        "symbol": "MigrateTests.test_sqlmigrate_forwards",
    }
}
_TARGET = ("blog", "0002_render_sql")
_CREATE_TABLE = re.compile(
    r'^CREATE TABLE (?P<table>"(?:[^"]|"")+") \((?P<columns>.*)\);$'
)
_ADD_COLUMN = re.compile(
    r'^ALTER TABLE (?P<table>"(?:[^"]|"")+") ADD COLUMN '
    r"(?P<column>.*);$"
)
_REFERENCE = re.compile(
    r' REFERENCES (?P<table>"(?:[^"]|"")+") '
    r'\((?P<column>"(?:[^"]|"")+")\)'
)
_CONSTRAINT_START = re.compile(
    r" (?=(?:NOT NULL|NULL|PRIMARY KEY|AUTOINCREMENT|UNIQUE|REFERENCES|"
    r"DEFAULT|CHECK|COLLATE|DEFERRABLE)\b)"
)


def _observed(contract_id: str, result: dict[str, Any]) -> dict[str, Any]:
    return {
        "db_state": None,
        "error": None,
        "id": contract_id,
        "metrics": None,
        "phase": "construction",
        "result": normalize(result),
        "status": "observed",
    }


def _migration_fixture() -> tuple[MigrationLoader, Migration]:
    """Build one fresh real Django graph without reading migration files."""

    loader = MigrationLoader(connection, load=False)
    loader.graph = MigrationGraph()
    loader.unmigrated_apps = set()
    loader.migrated_apps = {"authors", "blog"}

    authors_initial = Migration("0001_initial", "authors")
    authors_initial.operations = [
        CreateModel(
            name="Author",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                ("name", models.CharField(max_length=80)),
            ],
            options={"db_table": "authors_author"},
        )
    ]

    blog_initial = Migration("0001_initial", "blog")
    blog_initial.dependencies = [("authors", "0001_initial")]
    blog_initial.operations = [
        CreateModel(
            name="Article",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                ("title", models.CharField(max_length=200)),
                (
                    "author",
                    models.ForeignKey(
                        "authors.Author",
                        on_delete=models.CASCADE,
                    ),
                ),
            ],
            options={"db_table": "blog_article"},
        )
    ]

    target = Migration(_TARGET[1], _TARGET[0])
    target.dependencies = [("blog", "0001_initial")]
    target.operations = [
        CreateModel(
            name="Category",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                ("name", models.CharField(max_length=80)),
            ],
            options={"db_table": "blog_category"},
        ),
        AddField(
            model_name="article",
            name="summary",
            field=models.CharField(max_length=120, null=True),
        ),
    ]

    migrations = (authors_initial, blog_initial, target)
    for migration in migrations:
        loader.graph.add_node(
            (migration.app_label, migration.name),
            migration,
        )
    loader.graph.add_dependency(
        blog_initial,
        ("blog", "0001_initial"),
        ("authors", "0001_initial"),
    )
    loader.graph.add_dependency(
        target,
        _TARGET,
        ("blog", "0001_initial"),
    )
    loader.graph.validate_consistency()
    loader.disk_migrations = {
        (migration.app_label, migration.name): migration for migration in migrations
    }
    return loader, target


def _state_snapshot(state: Any) -> dict[str, Any]:
    models_snapshot = []
    for (app_label, model_name), model_state in sorted(state.models.items()):
        models_snapshot.append(
            {
                "app": app_label,
                "fields": [
                    {
                        "kind": field.__class__.__name__,
                        "name": name,
                    }
                    for name, field in model_state.fields.items()
                ],
                "model": model_name,
                "table": model_state.options.get("db_table"),
            }
        )
    return {"models": models_snapshot}


def _operation_subject(operation: Any) -> str:
    if isinstance(operation, CreateModel):
        return f"blog.{operation.name}"
    if isinstance(operation, AddField):
        return f"blog.{operation.model_name}.{operation.name}"
    raise AssertionError(f"unexpected operation type: {type(operation)!r}")


def _unquote_identifier(value: str) -> str:
    if len(value) < 2 or value[0] != '"' or value[-1] != '"':
        raise AssertionError(f"expected quoted SQLite identifier: {value!r}")
    return value[1:-1].replace('""', '"')


def _split_columns(value: str) -> list[str]:
    columns: list[str] = []
    start = 0
    depth = 0
    quoted = False
    index = 0
    while index < len(value):
        character = value[index]
        if character == '"':
            if quoted and index + 1 < len(value) and value[index + 1] == '"':
                index += 2
                continue
            quoted = not quoted
        elif not quoted:
            if character == "(":
                depth += 1
            elif character == ")":
                depth -= 1
            elif character == "," and depth == 0:
                columns.append(value[start:index].strip())
                start = index + 1
        index += 1
    if quoted or depth != 0:
        raise AssertionError(f"unbalanced SQLite column list: {value!r}")
    columns.append(value[start:].strip())
    if any(not column for column in columns):
        raise AssertionError(f"empty SQLite column definition: {value!r}")
    return columns


def _column_shape(definition: str) -> dict[str, Any]:
    match = re.match(r'^(?P<name>"(?:[^"]|"")+") (?P<body>.+)$', definition)
    if match is None:
        raise AssertionError(f"unexpected SQLite column definition: {definition!r}")
    body = match.group("body")
    parts = _CONSTRAINT_START.split(body, maxsplit=1)
    sql_type = parts[0].strip().lower()
    constraints = "" if len(parts) == 1 else body[len(parts[0]) :]
    reference_match = _REFERENCE.search(constraints)
    reference = None
    if reference_match is not None:
        reference = {
            "column": _unquote_identifier(reference_match.group("column")),
            "table": _unquote_identifier(reference_match.group("table")),
        }
    return {
        "autoincrement": " AUTOINCREMENT" in constraints,
        "name": _unquote_identifier(match.group("name")),
        "nullability": ("not_null" if " NOT NULL" in constraints else "nullable"),
        "primary_key": " PRIMARY KEY" in constraints,
        "reference": reference,
        "sql_type": sql_type,
    }


def _statement_shape(statement: str) -> dict[str, Any]:
    create = _CREATE_TABLE.fullmatch(statement)
    if create is not None:
        return {
            "columns": [
                _column_shape(column)
                for column in _split_columns(create.group("columns"))
            ],
            "kind": "create_table",
            "table": _unquote_identifier(create.group("table")),
        }
    add = _ADD_COLUMN.fullmatch(statement)
    if add is not None:
        return {
            "column": _column_shape(add.group("column")),
            "kind": "add_column",
            "table": _unquote_identifier(add.group("table")),
        }
    raise AssertionError(f"unexpected SQLite forward statement: {statement!r}")


def _run_forward_sqlmigrate() -> dict[str, Any]:
    if django.get_version() != "6.1":
        raise AssertionError("migration SQL reference requires exact Django 6.1")
    if connection.vendor != "sqlite":
        raise AssertionError("migration SQL reference requires Django SQLite")
    if connection.introspection.table_names():
        raise AssertionError("migration SQL reference requires a clean database")
    if MigrationRecorder(connection).has_table():
        raise AssertionError("migration SQL reference must not have recorder state")

    loader, target = _migration_fixture()
    trace: dict[str, Any] = {
        "before_states": [],
        "constructor_calls": [],
        "operation_state": [],
        "operation_sql": [],
        "plans": [],
    }

    original_project_state = loader.project_state

    def observed_project_state(
        nodes: Any = None,
        at_end: bool = True,
    ) -> Any:
        state = original_project_state(nodes, at_end=at_end)
        trace["before_states"].append(
            {
                "at_end": at_end,
                "nodes": list(nodes) if isinstance(nodes, tuple) else nodes,
                "state": _state_snapshot(state),
            }
        )
        return state

    loader.project_state = observed_project_state  # type: ignore[method-assign]

    originals: list[tuple[Any, Any, Any]] = []
    for ordinal, operation in enumerate(target.operations):
        original_state_forwards = operation.state_forwards
        original_database_forwards = operation.database_forwards
        originals.append(
            (operation, original_state_forwards, original_database_forwards)
        )

        def observed_state_forwards(
            app_label: str,
            state: Any,
            *,
            _ordinal: int = ordinal,
            _operation: Any = operation,
            _original: Any = original_state_forwards,
        ) -> Any:
            before = _state_snapshot(state)
            result = _original(app_label, state)
            trace["operation_state"].append(
                {
                    "after": _state_snapshot(state),
                    "before": before,
                    "kind": _operation.__class__.__name__,
                    "ordinal": _ordinal,
                    "subject": _operation_subject(_operation),
                }
            )
            return result

        def observed_database_forwards(
            app_label: str,
            schema_editor: Any,
            from_state: Any,
            to_state: Any,
            *,
            _ordinal: int = ordinal,
            _operation: Any = operation,
            _original: Any = original_database_forwards,
        ) -> Any:
            before = len(schema_editor.collected_sql)
            result = _original(
                app_label,
                schema_editor,
                from_state,
                to_state,
            )
            trace["operation_sql"].append(
                {
                    "kind": _operation.__class__.__name__,
                    "ordinal": _ordinal,
                    "statements": list(schema_editor.collected_sql[before:]),
                    "subject": _operation_subject(_operation),
                }
            )
            return result

        operation.state_forwards = observed_state_forwards  # type: ignore[method-assign]
        operation.database_forwards = observed_database_forwards  # type: ignore[method-assign]

    original_collect_sql = loader.collect_sql

    def observed_collect_sql(
        plan: Sequence[tuple[Migration, bool]],
    ) -> list[str]:
        materialized_plan = list(plan)
        trace["plans"].append(
            [
                {
                    "app": migration.app_label,
                    "backwards": backwards,
                    "name": migration.name,
                }
                for migration, backwards in materialized_plan
            ]
        )
        statements = original_collect_sql(materialized_plan)
        trace["collected_statements"] = list(statements)
        return statements

    loader.collect_sql = observed_collect_sql  # type: ignore[method-assign]

    def fixture_loader(
        supplied_connection: Any,
        *,
        replace_migrations: bool,
    ) -> MigrationLoader:
        trace["constructor_calls"].append(
            {
                "connection_is_default": supplied_connection is connection,
                "replace_migrations": replace_migrations,
            }
        )
        return loader

    command = sqlmigrate.Command(
        stdout=StringIO(),
        stderr=StringIO(),
        no_color=True,
    )
    try:
        with (
            patch.object(sqlmigrate, "MigrationLoader", side_effect=fixture_loader),
            patch.object(sqlmigrate.apps, "get_app_config", return_value=object()),
        ):
            output = command.handle(
                app_label=_TARGET[0],
                migration_name=_TARGET[1],
                database="default",
                backwards=False,
                verbosity=0,
            )
    finally:
        for operation, state_forwards, database_forwards in originals:
            operation.state_forwards = state_forwards
            operation.database_forwards = database_forwards

    statements = trace["collected_statements"]
    if output != "\n".join(statements):
        raise AssertionError("Command.handle changed collect_sql output")
    if connection.introspection.table_names():
        raise AssertionError("collect-SQL unexpectedly mutated the database")
    if MigrationRecorder(connection).has_table():
        raise AssertionError("collect-SQL unexpectedly created recorder state")
    return trace


def forward_before_state_order(contract_id: str) -> dict[str, Any]:
    trace = _run_forward_sqlmigrate()
    if len(trace["before_states"]) != 1:
        raise AssertionError("collect_sql did not reconstruct exactly one before-state")
    if len(trace["plans"]) != 1 or len(trace["plans"][0]) != 1:
        raise AssertionError("sqlmigrate did not collect exactly one migration")
    state_steps = sorted(trace["operation_state"], key=lambda row: row["ordinal"])
    if [row["ordinal"] for row in state_steps] != list(range(len(state_steps))):
        raise AssertionError("forward operation order was not contiguous")
    before_state = trace["before_states"][0]
    return _observed(
        contract_id,
        {
            "before_state": before_state["state"],
            "before_state_at_end": before_state["at_end"],
            "before_state_target": {
                "app": before_state["nodes"][0],
                "name": before_state["nodes"][1],
            },
            "direction": "forward",
            "final_state": state_steps[-1]["after"],
            "operation_order": [
                {
                    "kind": row["kind"],
                    "ordinal": row["ordinal"],
                    "subject": row["subject"],
                }
                for row in state_steps
            ],
            "plan": [
                {
                    "app": row["app"],
                    "direction": "backward" if row["backwards"] else "forward",
                    "name": row["name"],
                }
                for row in trace["plans"][0]
            ],
        },
    )


def sqlite_create_add_semantics(contract_id: str) -> dict[str, Any]:
    trace = _run_forward_sqlmigrate()
    operation_sql = sorted(trace["operation_sql"], key=lambda row: row["ordinal"])
    if [row["ordinal"] for row in operation_sql] != list(range(len(operation_sql))):
        raise AssertionError("SQLite SQL order was not contiguous")
    return _observed(
        contract_id,
        {
            "backend": "django.db.backends.sqlite3",
            "comments_compared": False,
            "normalized_operations": [
                {
                    "kind": row["kind"],
                    "ordinal": row["ordinal"],
                    "statements": [
                        _statement_shape(statement) for statement in row["statements"]
                    ],
                    "subject": row["subject"],
                }
                for row in operation_sql
            ],
            "raw_sql_bytes_compared": False,
            "transaction_wrapper_compared": False,
        },
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.migration.sql_rendering.forward_before_state_order": (
        forward_before_state_order
    ),
    "django.migration.sql_rendering.sqlite_create_add_semantics": (
        sqlite_create_add_semantics
    ),
}
