"""Reference observations for relation-capable migration contracts.

The Django-facing scenarios execute fresh migration values against disposable
file-backed SQLite databases.  The GoDj current definition/state/preflight and
commit-policy values are deliberately marked as decision observations: Django
doesn't define GoDj's JSON format, digest domain, revision
fence, or unknown commit outcome.  Scenarios never read checked-in manifests,
oracles, or static fixtures.
"""

from __future__ import annotations

import hashlib
import tempfile
from collections import Counter
from collections.abc import Callable, Iterator, Sequence
from contextlib import contextmanager
from copy import deepcopy
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from unittest.mock import patch

from django.db import connections
from django.db.backends.sqlite3.schema import DatabaseSchemaEditor
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.recorder import MigrationRecorder

from . import migration_definition_source_scenarios as current_definition
from .normalizer import normalize
from .scenarios import configure_django


configure_django()


_APP = "godj_migration_relation"
_M1 = (_APP, "0001_initial")
_M2 = (_APP, "0002_nullable_relation")
_LATEST = (_M2,)
_DATABASE_ALIAS = "godj_migration_relation_reference"
_TABLE_PREFIX = "godj_migration_relation_"
_AUTHOR_TABLE = _TABLE_PREFIX + "author"
_ARTICLE_TABLE = _TABLE_PREFIX + "article"
_CURRENT_FORMAT = current_definition._FORMAT_VERSION
_CURRENT_SCHEMA_IR = current_definition._SCHEMA_IR_VERSION
_CURRENT_STATE_FORMAT = current_definition._STATE_FORMAT_VERSION
_CURRENT_DIGEST_DOMAIN = current_definition._DIGEST_DOMAIN


class RelationMigrationDDLFailure(RuntimeError):
    """Stable local sentinel raised before AddField DDL is delegated."""


class RelationMigrationRecorderFailure(RuntimeError):
    """Stable local sentinel raised at the 0002 recorder write."""


def _success(
    contract_id: str,
    phase: str,
    result: Any,
    *,
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


def _canonical_json(value: Any) -> bytes:
    return current_definition._canonical_json(value)


def _sha256(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def _decision_error(code: str, *, stage: str, reason: str) -> dict[str, Any]:
    return {
        "category": "migration_relation_decision_error",
        "code": code,
        "message_is_contract": False,
        "reason": reason,
        "stage": stage,
    }


def _current_vector() -> dict[str, Any]:
    loaded, report = current_definition._load(
        current_definition._fixture_sources()
    )
    definitions = deepcopy(list(loaded.definitions))
    definitions.sort(key=lambda item: (item["app"], item["name"]))
    for definition in definitions:
        definition["dependencies"].sort(
            key=lambda item: (item["app"], item["name"])
        )
    digest_document = {
        "definitions": definitions,
        "domain": _CURRENT_DIGEST_DOMAIN,
        "format_version": _CURRENT_FORMAT,
    }
    canonical = _canonical_json(digest_document)
    if _sha256(canonical) != loaded.digest:
        raise AssertionError("current canonical bytes no longer match digest")
    return {
        "canonical_bytes": len(canonical),
        "canonical_sha256": _sha256(canonical),
        "definition_count": len(loaded.definitions),
        "definition_set_digest": loaded.digest,
        "digest_domain": _CURRENT_DIGEST_DOMAIN,
        "format": {
            "definition": _CURRENT_FORMAT,
            "schema_ir": _CURRENT_SCHEMA_IR,
            "state": _CURRENT_STATE_FORMAT,
        },
        "operation_count": report["operations_decoded"],
    }


def current_abi(contract_id: str) -> dict[str, Any]:
    first = _current_vector()
    second = _current_vector()
    return _success(
        contract_id,
        "construction",
        {
            "classification": "current_only_decision_reference",
            "current": first,
            "retired_compatibility_tuple_present": False,
            "repeat_equal": first == second,
            "scalar_and_relation_share_format": True,
        },
        metrics={
            "artifact_reads": 0,
            "definition_loads": 2,
            "published_sets": 2,
            "compatibility_upgrades": 0,
        },
    )


def _format_case(name: str, document: dict[str, Any]) -> dict[str, Any]:
    source = current_definition.SourceDocument(
        f"case-{name}", current_definition._encode_document(document)
    )
    try:
        loaded, _ = current_definition._load((source,))
    except current_definition._DefinitionSourceError as error:
        return {
            "accepted": False,
            "case": name,
            "error": {
                "category": error.category,
                "code": error.code,
                "json_pointer": error.context["json_pointer"],
                "reason": error.context["reason"],
                "stage": error.context["stage"],
            },
            "published_definitions": 0,
        }
    return {
        "accepted": True,
        "case": name,
        "error": None,
        "published_definitions": len(loaded.definitions),
    }


def current_format_validation(contract_id: str) -> dict[str, Any]:
    exact = current_definition._root_document()
    missing = current_definition._root_document()
    missing.pop("format_version")
    unknown = current_definition._root_document()
    unknown["format_version"] = _CURRENT_FORMAT + 1
    wrong_type = current_definition._root_document()
    wrong_type["format_version"] = str(_CURRENT_FORMAT)
    overflow = current_definition._root_document()
    overflow["format_version"] = 1 << 63
    retired_tuple = current_definition._root_document()
    retired_tuple["compatibility"] = {
        "definition_format": 1,
        "loader_abi": 2,
        "operation_codec": 2,
        "schema_ir": 3,
    }
    observations = [
        _format_case("exact_current", exact),
        _format_case("missing_format_version", missing),
        _format_case("unknown_format_version", unknown),
        _format_case("wrong_type_format_version", wrong_type),
        _format_case("overflow_format_version", overflow),
        _format_case("retired_compatibility_tuple", retired_tuple),
    ]
    return _success(
        contract_id,
        "environment",
        {
            "cases": observations,
            "classification": "current_only_decision_reference",
            "publication_atomic": True,
        },
        metrics={
            "accepted_documents": sum(item["accepted"] for item in observations),
            "artifact_reads": 0,
            "database_io": 0,
            "documents_checked": len(observations),
            "rejected_documents": sum(
                not item["accepted"] for item in observations
            ),
        },
    )


def _relation_definition() -> dict[str, Any]:
    return {
        "app": "blog",
        "dependencies": [{"app": "authors", "name": "0001_initial"}],
        "name": "0002_article_author",
        "operations": [
            {
                "app_label": "blog",
                "field": {
                    "column": "author_id",
                    "default": None,
                    "go_name": "AuthorID",
                    "kind": "foreign_key",
                    "max_length": 0,
                    "name": "author",
                    "nullable": False,
                    "primary_key": False,
                    "relation": {
                        "cardinality": "many_to_one",
                        "on_delete": "protect",
                        "reverse": {"disabled": False, "name": "articles"},
                        "target": {
                            "app_label": "authors",
                            "model_name": "author",
                        },
                    },
                },
                "kind": "add_field",
                "model_name": "article",
            }
        ],
    }


def _current_digest(
    definitions: Sequence[dict[str, Any]],
) -> tuple[str, list[dict[str, Any]]]:
    canonical_items = deepcopy(list(definitions))
    canonical_items.sort(key=lambda item: (item["app"], item["name"]))
    for definition in canonical_items:
        definition["dependencies"].sort(
            key=lambda item: (item["app"], item["name"])
        )
    document = {
        "definitions": canonical_items,
        "domain": _CURRENT_DIGEST_DOMAIN,
        "format_version": _CURRENT_FORMAT,
    }
    return _sha256(_canonical_json(document)), canonical_items


def current_digest(contract_id: str) -> dict[str, Any]:
    loaded, _ = current_definition._load(current_definition._fixture_sources())
    scalar_definition = deepcopy(loaded.definitions[0])
    relation = _relation_definition()
    scalar_digest, scalar_items = _current_digest((scalar_definition,))
    relation_digest, relation_items = _current_digest((relation,))
    combined_digest, combined_items = _current_digest(
        (relation, scalar_definition)
    )
    permuted_digest, _ = _current_digest(
        (scalar_definition, relation)
    )
    return _success(
        contract_id,
        "construction",
        {
            "classification": "current_only_decision_reference",
            "combined": {
                "canonical_definitions": combined_items,
                "digest": combined_digest,
                "domain": _CURRENT_DIGEST_DOMAIN,
                "permutation_equal": combined_digest == permuted_digest,
            },
            "relation_only": {
                "canonical_definitions": relation_items,
                "digest": relation_digest,
                "domain": _CURRENT_DIGEST_DOMAIN,
            },
            "scalar_only": {
                "canonical_definitions": scalar_items,
                "digest": scalar_digest,
                "domain": _CURRENT_DIGEST_DOMAIN,
            },
            "profile_metadata_present": False,
        },
        metrics={
            "artifact_reads": 0,
            "database_io": 0,
            "digest_computations": 4,
            "compatibility_branches": 0,
        },
    )


def _scalar_state() -> dict[str, Any]:
    return {
        "format_version": _CURRENT_STATE_FORMAT,
        "models": [
            {
                "app": "blog",
                "fields": [
                    {"column": "id", "kind": "auto", "name": "id"},
                    {"column": "title", "kind": "char", "name": "title"},
                ],
                "name": "article",
                "table": "blog_article",
            }
        ],
    }


def current_state(contract_id: str) -> dict[str, Any]:
    scalar = _scalar_state()
    relation_state = deepcopy(scalar)
    relation_state["models"][0]["fields"].append(
        deepcopy(_relation_definition()["operations"][0]["field"])
    )
    cloned = deepcopy(relation_state)
    cloned["models"][0]["fields"][0]["name"] = "changed_only_in_clone"
    return _success(
        contract_id,
        "construction",
        {
            "alias_free": relation_state["models"][0]["fields"][0]["name"] == "id",
            "classification": "current_only_decision_reference",
            "format_transition_required": False,
            "relation_state": relation_state,
            "scalar_state": scalar,
            "single_format": (
                scalar["format_version"]
                == relation_state["format_version"]
                == _CURRENT_STATE_FORMAT
            ),
        },
        metrics={
            "artifact_reads": 0,
            "database_io": 0,
            "format_transitions": 0,
            "state_publications": 2,
        },
    )


def structural_preflight(contract_id: str) -> dict[str, Any]:
    lanes = [
        {
            "error": _decision_error(
                "target_model_not_found",
                stage="static_preflight",
                reason="target_model_missing",
            ),
            "lane": "static_invalid",
            "trace": [],
            "trace_events": 0,
        },
        {
            "error": _decision_error(
                "applied_history_not_in_graph",
                stage="history_preflight",
                reason="history_requires_missing_ancestor",
            ),
            "lane": "history_invalid",
            "allowed_trace": [
                "capabilities",
                "open_session",
                "read_history",
                "close_session",
            ],
            "forbidden_trace": [
                "begin_migration",
                "ddl",
                "record",
                "revision",
            ],
        },
        {
            "durable_unchanged": True,
            "error": _decision_error(
                "required_foreign_key_requires_backfill",
                stage="physical_preflight",
                reason="populated_table",
            ),
            "lane": "physical_populated_required",
            "mutation_events": 0,
            "reads_allowed": True,
        },
    ]
    return _success(
        contract_id,
        "evaluation",
        {
            "classification": "current_only_staged_preflight_decision_reference",
            "lanes": lanes,
            "mandatory_backend_capability": {
                "optional_relation_port_retired": True,
                "replacement_error": "migration_capability_unavailable",
            },
        },
        metrics={
            "artifact_reads": 0,
            "lanes_checked": len(lanes),
            "mutation_events": 0,
            "rejected_lanes": len(lanes),
        },
    )


def _plan_value(plan: Sequence[tuple[Any, bool]]) -> list[dict[str, str]]:
    return [
        {
            "app": migration.app_label,
            "direction": "backward" if backwards else "forward",
            "name": migration.name,
        }
        for migration, backwards in plan
    ]


@dataclass
class _DatabaseSession:
    alias: str
    connection: Any

    def reopen(self) -> bool:
        previous = self.connection
        previous.close()
        del connections[self.alias]
        self.connection = connections[self.alias]
        return self.connection is not previous


@contextmanager
def _isolated_database() -> Iterator[_DatabaseSession]:
    if _DATABASE_ALIAS in connections.databases:
        raise AssertionError(f"database alias {_DATABASE_ALIAS!r} leaked")
    with tempfile.TemporaryDirectory(prefix="godj-migration-relation-") as directory:
        configuration = dict(connections.databases["default"])
        configuration["NAME"] = str(Path(directory) / "relation.sqlite3")
        connections.databases[_DATABASE_ALIAS] = configuration
        session = _DatabaseSession(
            alias=_DATABASE_ALIAS,
            connection=connections[_DATABASE_ALIAS],
        )
        try:
            if session.connection.introspection.table_names():
                raise AssertionError("relation migration database did not start empty")
            yield session
        finally:
            if session.connection.in_atomic_block:
                raise AssertionError("relation migration scenario leaked atomic block")
            session.connection.close()
            if _DATABASE_ALIAS in connections:
                del connections[_DATABASE_ALIAS]
            del connections.databases[_DATABASE_ALIAS]


def _executor(connection: Any) -> MigrationExecutor:
    return MigrationExecutor(connection)


def _statement_kind(sql: str) -> str:
    rendered = sql.lstrip().upper()
    return rendered.split(None, 1)[0] if rendered else "EMPTY"


def _capture(connection: Any, operation: Callable[[], Any]) -> tuple[Any, list[str]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_kind(sql))
        return execute(sql, params, many, context)

    with connection.execute_wrapper(wrapper):
        result = operation()
    return result, statements


def _statement_metrics(statements: Sequence[str]) -> dict[str, Any]:
    counts = Counter(statements)
    return {
        "statement_count": len(statements),
        "statement_kinds": [
            {"count": counts[kind], "kind": kind} for kind in sorted(counts)
        ],
    }


def _quote_pragma_identifier(name: str) -> str:
    return '"' + name.replace('"', '""') + '"'


def _managed_tables(connection: Any) -> list[str]:
    return sorted(
        table
        for table in connection.introspection.table_names()
        if table.startswith(_TABLE_PREFIX)
    )


def _table_columns(connection: Any, table: str) -> list[dict[str, Any]]:
    with connection.cursor() as cursor:
        cursor.execute(f"PRAGMA table_info({_quote_pragma_identifier(table)})")
        rows = cursor.fetchall()
    return [
        {
            "default": row[4],
            "name": row[1],
            "nullable": not bool(row[3]),
            "primary_key_ordinal": row[5],
            "type": row[2].lower(),
        }
        for row in sorted(rows, key=lambda item: item[0])
    ]


def _foreign_keys(connection: Any, table: str) -> list[dict[str, Any]]:
    with connection.cursor() as cursor:
        cursor.execute(
            f"PRAGMA foreign_key_list({_quote_pragma_identifier(table)})"
        )
        rows = cursor.fetchall()
    return [
        {
            "from": row[3],
            "id": row[0],
            "match": row[7],
            "on_delete": row[6],
            "on_update": row[5],
            "sequence": row[1],
            "table": row[2],
            "to": row[4],
        }
        for row in sorted(rows, key=lambda item: (item[0], item[1], item[3]))
    ]


def _table_rows(connection: Any, table: str) -> list[dict[str, Any]]:
    columns = [item["name"] for item in _table_columns(connection, table)]
    if not columns:
        return []
    quoted_columns = ", ".join(
        connection.ops.quote_name(column) for column in columns
    )
    with connection.cursor() as cursor:
        cursor.execute(
            f"SELECT {quoted_columns} FROM {connection.ops.quote_name(table)} "
            f"ORDER BY {connection.ops.quote_name('id')}"
        )
        rows = cursor.fetchall()
    return [dict(zip(columns, row, strict=True)) for row in rows]


def _sequences(connection: Any) -> list[dict[str, Any]]:
    with connection.cursor() as cursor:
        cursor.execute(
            "SELECT 1 FROM sqlite_schema "
            "WHERE type = 'table' AND name = 'sqlite_sequence'"
        )
        if cursor.fetchone() is None:
            return []
        cursor.execute(
            "SELECT name, seq FROM sqlite_sequence "
            "WHERE name LIKE %s ORDER BY name",
            [_TABLE_PREFIX + "%"],
        )
        rows = cursor.fetchall()
    return [{"sequence": row[1], "table": row[0]} for row in rows]


def _records(connection: Any) -> list[dict[str, str]]:
    recorder = MigrationRecorder(connection)
    if not recorder.has_table():
        return []
    return [
        {"app": app, "name": name}
        for app, name in sorted(recorder.applied_migrations())
        if app == _APP
    ]


def _snapshot(connection: Any) -> dict[str, Any]:
    tables = _managed_tables(connection)
    return {
        "foreign_keys": [
            {"constraints": _foreign_keys(connection, table), "table": table}
            for table in tables
        ],
        "migration_records": _records(connection),
        "rows": [
            {"rows": _table_rows(connection, table), "table": table}
            for table in tables
        ],
        "sequences": _sequences(connection),
        "tables": [
            {"columns": _table_columns(connection, table), "name": table}
            for table in tables
        ],
    }


def _pragma_foreign_keys(connection: Any) -> int:
    with connection.cursor() as cursor:
        cursor.execute("PRAGMA foreign_keys")
        row = cursor.fetchone()
    if row is None or row[0] not in {0, 1}:
        raise AssertionError(f"unexpected PRAGMA foreign_keys row: {row!r}")
    return row[0]


def _seed_0001(connection: Any) -> None:
    with connection.cursor() as cursor:
        cursor.executemany(
            f"INSERT INTO {connection.ops.quote_name(_AUTHOR_TABLE)} (id, name) VALUES (%s, %s)",
            [(2, "Ada"), (5, "Grace")],
        )
        cursor.executemany(
            f"INSERT INTO {connection.ops.quote_name(_ARTICLE_TABLE)} (id, title, author_id) VALUES (%s, %s, %s)",
            [(3, "Compiler", 2), (8, "Database", 5)],
        )


def _set_editors(connection: Any) -> None:
    with connection.cursor() as cursor:
        cursor.execute(
            f"UPDATE {connection.ops.quote_name(_ARTICLE_TABLE)} "
            "SET editor_id = CASE id WHEN 3 THEN 5 ELSE 2 END"
        )


def create_lifecycle(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        transitions = []
        statement_groups = []
        for label, target in (
            ("apply", _M1),
            ("unapply", (_APP, None)),
            ("reapply", _M1),
        ):
            executor = _executor(session.connection)
            plan = executor.migration_plan([target])
            _state, statements = _capture(
                session.connection,
                lambda executor=executor, target=target, plan=plan: executor.migrate(
                    [target], plan=plan
                ),
            )
            transitions.append(
                {
                    "label": label,
                    "plan": _plan_value(plan),
                    "state": _snapshot(session.connection),
                }
            )
            statement_groups.append({"label": label, **_statement_metrics(statements)})
        final = _snapshot(session.connection)
    return _success(
        contract_id,
        "commit",
        {
            "classification": "django_observed",
            "transitions": transitions,
        },
        db_state=final,
        metrics={"artifact_reads": 0, "statement_groups": statement_groups},
    )


def add_nullable_populated(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        executor = _executor(session.connection)
        executor.migrate([_M1])
        _seed_0001(session.connection)
        before = _snapshot(session.connection)
        executor = _executor(session.connection)
        plan = executor.migration_plan([_M2])
        _state, statements = _capture(
            session.connection,
            lambda: executor.migrate([_M2], plan=plan),
        )
        after = _snapshot(session.connection)
    return _success(
        contract_id,
        "commit",
        {
            "classification": "django_observed_plus_current_godj_decision",
            "django_observation": {
                "existing_rows_received_null": all(
                    row["editor_id"] is None
                    for row in next(
                        item["rows"] for item in after["rows"]
                        if item["table"] == _ARTICLE_TABLE
                    )
                ),
                "plan": _plan_value(plan),
            },
            "gdj_required_populated_policy": {
                "error": _decision_error(
                    "required_foreign_key_requires_backfill",
                    stage="preflight",
                    reason="populated_table_without_default",
                ),
                "mutation_count": 0,
                "pre_ddl": True,
            },
        },
        db_state={"after": after, "before": before},
        metrics={
            "artifact_reads": 0,
            "required_policy_database_io": 0,
            **_statement_metrics(statements),
        },
    )


def remove_remake(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        _executor(session.connection).migrate([_M1])
        _seed_0001(session.connection)
        _executor(session.connection).migrate([_M2])
        _set_editors(session.connection)
        before = _snapshot(session.connection)
        executor = _executor(session.connection)
        plan = executor.migration_plan([_M1])
        _state, statements = _capture(
            session.connection,
            lambda: executor.migrate([_M1], plan=plan),
        )
        after = _snapshot(session.connection)
    before_article = next(
        item["rows"] for item in before["rows"] if item["table"] == _ARTICLE_TABLE
    )
    after_article = next(
        item["rows"] for item in after["rows"] if item["table"] == _ARTICLE_TABLE
    )
    return _success(
        contract_id,
        "commit",
        {
            "classification": "django_observed",
            "plan": _plan_value(plan),
            "preservation": {
                "article_ids": [row["id"] for row in after_article],
                "article_ids_preserved": [row["id"] for row in before_article]
                == [row["id"] for row in after_article],
                "sequence_after": after["sequences"],
                "sequence_before": before["sequences"],
            },
        },
        db_state={"after": after, "before": before},
        metrics={"artifact_reads": 0, **_statement_metrics(statements)},
    )


def physical_fk_policy(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        pragma_before = _pragma_foreign_keys(session.connection)
        with session.connection.schema_editor():
            pragma_inside_editor = _pragma_foreign_keys(session.connection)
        pragma_after_editor = _pragma_foreign_keys(session.connection)
        executor = _executor(session.connection)
        _state, statements = _capture(
            session.connection,
            lambda: executor.migrate(list(_LATEST)),
        )
        after = _snapshot(session.connection)
        pragma_after_migrate = _pragma_foreign_keys(session.connection)
    constraints = [
        constraint
        for table in after["foreign_keys"]
        for constraint in table["constraints"]
    ]
    return _success(
        contract_id,
        "commit",
        {
            "classification": "django_observed_plus_current_godj_decision",
            "django_observation": {
                "constraint_actions": [
                    {
                        "column": item["from"],
                        "on_delete": item["on_delete"],
                        "target_table": item["table"],
                    }
                    for item in constraints
                ],
                "pragma_sequence": [
                    {"point": "before_editor", "value": pragma_before},
                    {"point": "inside_editor", "value": pragma_inside_editor},
                    {"point": "after_editor", "value": pragma_after_editor},
                    {"point": "after_migrate", "value": pragma_after_migrate},
                ],
            },
            "gdj_pinned_policy": {
                "begin_after_fk_on": True,
                "physical_actions": {"protect": "NO ACTION", "set_null": "NO ACTION"},
                "same_physical_connection_required": True,
            },
        },
        db_state=after,
        metrics={
            "artifact_reads": 0,
            "django_connection_sessions": 1,
            "gdj_policy_connection_pins": 1,
            **_statement_metrics(statements),
        },
    )


def file_restart(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        _executor(session.connection).migrate([_M1])
        _seed_0001(session.connection)
        _executor(session.connection).migrate([_M2])
        _set_editors(session.connection)
        before = _snapshot(session.connection)
        connection_replaced = session.reopen()
        executor = _executor(session.connection)
        plan = executor.migration_plan([_M2])
        returned_state, statements = _capture(
            session.connection,
            lambda: executor.migrate([_M2], plan=plan),
        )
        after = _snapshot(session.connection)
        state_models = [
            {"app": app, "model": model}
            for app, model in sorted(returned_state.models)
            if app == _APP
        ]
    return _success(
        contract_id,
        "commit",
        {
            "classification": "django_observed",
            "connection_replaced": connection_replaced,
            "fresh_plan": _plan_value(plan),
            "reconstructed_models": state_models,
        },
        db_state={"after_reopen": after, "before_close": before},
        metrics={
            "artifact_reads": 0,
            "connection_opens": 2,
            "fresh_executors": 1,
            "fresh_loaders": 1,
            **_statement_metrics(statements),
        },
    )


def _fault_case(kind: str) -> dict[str, Any]:
    with _isolated_database() as session:
        _executor(session.connection).migrate([_M1])
        _seed_0001(session.connection)
        before = _snapshot(session.connection)
        executor = _executor(session.connection)
        plan = executor.migration_plan([_M2])
        statements: list[str] = []

        def capture(execute, sql, params, many, context):
            statements.append(_statement_kind(sql))
            return execute(sql, params, many, context)

        if kind == "ddl":
            original = DatabaseSchemaEditor.add_field

            def fail_add_field(editor, model, field):
                if field.name == "editor":
                    raise RelationMigrationDDLFailure("forced relation DDL failure")
                return original(editor, model, field)

            controlled = patch.object(
                DatabaseSchemaEditor,
                "add_field",
                fail_add_field,
            )
            expected = RelationMigrationDDLFailure
            error_token = "relation_ddl_failure"
        elif kind == "recorder":
            original_record = MigrationRecorder.record_applied

            def fail_record(recorder, app, name):
                if (app, name) == _M2:
                    raise RelationMigrationRecorderFailure(
                        "forced relation recorder failure"
                    )
                return original_record(recorder, app, name)

            controlled = patch.object(
                MigrationRecorder,
                "record_applied",
                fail_record,
            )
            expected = RelationMigrationRecorderFailure
            error_token = "relation_recorder_failure"
        else:
            raise AssertionError(f"unknown fault kind {kind!r}")

        try:
            with controlled, session.connection.execute_wrapper(capture):
                executor.migrate([_M2], plan=plan)
        except expected:
            pass
        else:
            raise AssertionError(f"{kind} fault did not fail")
        after = _snapshot(session.connection)
        connection_replaced = session.reopen()
        after_reopen = _snapshot(session.connection)
    record_published = {"app": _APP, "name": _M2[1]} in after[
        "migration_records"
    ]
    fully_rolled_back = after == before
    return {
        "after": after,
        "after_reopen": after_reopen,
        "before": before,
        "connection_replaced": connection_replaced,
        "error_token": error_token,
        "fault": kind,
        "failed_migration_record_published": record_published,
        "fresh_reopen_durable": after_reopen == after,
        "fully_rolled_back": fully_rolled_back,
        "plan": _plan_value(plan),
        "schema_changed": after["tables"] != before["tables"],
        "transaction_boundary": (
            "rolled_back_before_ddl"
            if fully_rolled_back
            else "schema_committed_before_recorder_failure"
        ),
        "statement_metrics": _statement_metrics(statements),
    }


def precommit_faults(contract_id: str) -> dict[str, Any]:
    ddl = _fault_case("ddl")
    recorder = _fault_case("recorder")
    return _success(
        contract_id,
        "rollback",
        {
            "classification": "django_observed_plus_current_godj_decision",
            "django_faults": [
                {
                    key: value
                    for key, value in case.items()
                    if key not in {"after", "after_reopen", "before"}
                }
                for case in (ddl, recorder)
            ],
            "gdj_revision_fault_policy": {
                "cause_order": ["revision_write", "rollback_if_present"],
                "published_successor_revision": False,
                "same_transaction": True,
            },
        },
        db_state={
            "ddl": {
                "after": ddl["after"],
                "after_reopen": ddl["after_reopen"],
                "before": ddl["before"],
            },
            "recorder": {
                "after": recorder["after"],
                "after_reopen": recorder["after_reopen"],
                "before": recorder["before"],
            },
        },
        metrics={
            "artifact_reads": 0,
            "faults_attempted": 2,
            "faults_fully_rolled_back": sum(
                case["fully_rolled_back"] for case in (ddl, recorder)
            ),
            "fresh_reopens": 2,
            "schema_committed_record_missing": sum(
                case["schema_changed"]
                and not case["failed_migration_record_published"]
                for case in (ddl, recorder)
            ),
            "revision_policy_retry_calls": 0,
        },
    )


def commit_outcomes(contract_id: str) -> dict[str, Any]:
    cases = [
        {
            "commit_calls": 1,
            "durable_result_known": True,
            "error": None,
            "outcome": "success",
            "retry_calls": 0,
            "state_published": True,
        },
        {
            "commit_calls": 1,
            "durable_result_known": True,
            "error": _decision_error(
                "commit_definite_failure",
                stage="commit",
                reason="not_committed",
            ),
            "outcome": "definite_failure",
            "retry_calls": 0,
            "state_published": False,
        },
        {
            "commit_calls": 1,
            "durable_result_known": False,
            "error": _decision_error(
                "commit_outcome_unknown",
                stage="commit",
                reason="durability_unknown",
            ),
            "outcome": "unknown",
            "retry_calls": 0,
            "state_published": False,
        },
    ]
    return _success(
        contract_id,
        "commit",
        {
            "cases": cases,
            "classification": "accepted_no_retry_plus_current_godj_decision",
            "retained_connection_policy": "outside_compatibility_reset_scope",
        },
        metrics={
            "artifact_reads": 0,
            "commit_calls": sum(case["commit_calls"] for case in cases),
            "outcomes_checked": len(cases),
            "retry_calls": sum(case["retry_calls"] for case in cases),
        },
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "godj.migration.relation.current_abi": current_abi,
    "godj.migration.relation.current_format_validation": current_format_validation,
    "godj.migration.relation.current_digest": current_digest,
    "godj.migration.relation.current_state": current_state,
    "godj.migration.relation.structural_preflight": structural_preflight,
    "django.migration.relation.create_lifecycle": create_lifecycle,
    "django.migration.relation.add_nullable_populated": add_nullable_populated,
    "django.migration.relation.remove_remake": remove_remake,
    "django.migration.relation.physical_fk_policy": physical_fk_policy,
    "django.migration.relation.file_restart": file_restart,
    "django.migration.relation.precommit_faults": precommit_faults,
    "godj.migration.relation.commit_outcomes": commit_outcomes,
}
