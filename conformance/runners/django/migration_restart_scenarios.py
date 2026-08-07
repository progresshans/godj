"""Django reference adapters for recorder-backed restart planning.

The fixtures are in-memory ``Migration`` objects, while every applied-state
input is read from a disposable SQLite database by a newly constructed
recorder or loader. This keeps migration file discovery and Django's private
object caches outside the compatibility surface.
"""

from __future__ import annotations

import tempfile
from collections.abc import Callable, Iterator, Mapping, Sequence
from contextlib import ExitStack, contextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from django.db import connection, connections, models
from django.db.migrations.exceptions import InconsistentMigrationHistory
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.loader import MigrationLoader
from django.db.migrations.migration import Migration
from django.db.migrations.operations.base import Operation
from django.db.migrations.operations.fields import AddField
from django.db.migrations.operations.models import CreateModel
from django.db.migrations.recorder import MigrationRecorder
from django.db.migrations.state import ProjectState

from .normalizer import normalize
from .scenarios import configure_django


configure_django()


NodeKey = tuple[str, str]
Dependency = tuple[NodeKey, NodeKey]
Plan = Sequence[tuple[Migration, bool]]

_A1 = ("alpha", "0001_initial")
_A2 = ("alpha", "0002_second")
_A3 = ("alpha", "0003_third")
_B1 = ("beta", "0001_initial")
_LEGACY = ("legacy", "0099_retired")

_LINEAR_NODES = (_A1, _A2, _A3)
_LINEAR_DEPENDENCIES = ((_A2, _A1), (_A3, _A2))
_MANAGED_TABLE_PREFIX = "godj_restart_"

_DDL_KINDS = frozenset({"ALTER", "CREATE", "DROP", "TRUNCATE"})
_WRITE_KINDS = frozenset({"DELETE", "INSERT", "REPLACE", "UPDATE"})


class ConformanceRestartOperationFailure(RuntimeError):
    """Stable sentinel used to leave a durable prefix after a failed step."""


def _key_value(key: tuple[str, str | None]) -> dict[str, str | None]:
    return {"app": key[0], "name": key[1]}


def _key_values(keys: Sequence[tuple[str, str | None]]) -> list[dict[str, Any]]:
    return [_key_value(key) for key in keys]


def _migration_key(migration: Migration) -> NodeKey:
    return (migration.app_label, migration.name)


def _plan_value(plan: Plan) -> list[dict[str, str]]:
    return [
        {
            "app": migration.app_label,
            "direction": "backward" if backwards else "forward",
            "name": migration.name,
        }
        for migration, backwards in plan
    ]


def _graph_facts(
    nodes: Sequence[NodeKey],
    dependencies: Sequence[Dependency],
) -> dict[str, Any]:
    return {
        "dependencies": [
            {"child": _key_value(child), "parent": _key_value(parent)}
            for child, parent in sorted(dependencies)
        ],
        "nodes": _key_values(sorted(nodes)),
    }


def _statement_kind(sql: str) -> str:
    rendered = sql.lstrip().upper()
    return rendered.split(None, 1)[0] if rendered else "EMPTY"


def _capture(
    database_connections: Sequence[Any],
    operation: Callable[[], Any],
) -> tuple[Any, list[str]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_kind(sql))
        return execute(sql, params, many, context)

    with ExitStack() as stack:
        for database_connection in database_connections:
            stack.enter_context(database_connection.execute_wrapper(wrapper))
        result = operation()
    return result, statements


def _capture_error(
    database_connections: Sequence[Any],
    operation: Callable[[], Any],
    expected: type[BaseException],
) -> tuple[BaseException, list[str]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_kind(sql))
        return execute(sql, params, many, context)

    try:
        with ExitStack() as stack:
            for database_connection in database_connections:
                stack.enter_context(database_connection.execute_wrapper(wrapper))
            operation()
    except expected as error:
        return error, statements
    raise AssertionError(f"expected {expected.__name__}")


def _type_family(type_code: Any) -> str:
    rendered = str(type_code).lower()
    if "int" in rendered:
        return "integer"
    if "char" in rendered or "clob" in rendered or "text" in rendered:
        return "text"
    if "bool" in rendered:
        return "boolean"
    if "date" in rendered or "time" in rendered:
        return "datetime"
    return rendered


def _managed_schema(database_connection: Any) -> list[dict[str, Any]]:
    inventory: list[dict[str, Any]] = []
    with database_connection.cursor() as cursor:
        for table in sorted(database_connection.introspection.table_names(cursor)):
            if not table.startswith(_MANAGED_TABLE_PREFIX):
                continue
            description = database_connection.introspection.get_table_description(
                cursor, table
            )
            constraints = database_connection.introspection.get_constraints(
                cursor, table
            )
            primary_key_columns = {
                column
                for constraint in constraints.values()
                if constraint["primary_key"]
                for column in constraint["columns"]
            }
            inventory.append(
                {
                    "columns": [
                        {
                            "name": column.name,
                            "nullable": column.null_ok,
                            "primary_key": column.name in primary_key_columns,
                            "type_family": _type_family(column.type_code),
                        }
                        for column in sorted(description, key=lambda item: item.name)
                    ],
                    "name": table,
                }
            )
    return inventory


def _database_snapshot(database_connection: Any) -> dict[str, Any]:
    recorder = MigrationRecorder(database_connection)
    applied = sorted(recorder.applied_migrations())
    return {
        "applied_migrations": _key_values(applied),
        "managed_schema": _managed_schema(database_connection),
        "recorder_present": recorder.has_table(),
    }


def _database_set_snapshot(
    database_connections: Mapping[str, Any],
) -> dict[str, Any]:
    return {
        "databases": [
            {"alias": alias, **_database_snapshot(database_connection)}
            for alias, database_connection in sorted(database_connections.items())
        ]
    }


def _drop_tables(database_connection: Any) -> None:
    for table in database_connection.introspection.table_names():
        with database_connection.cursor() as cursor:
            cursor.execute(
                f"DROP TABLE {database_connection.ops.quote_name(table)}"
            )


@contextmanager
def _isolated_default_database() -> Iterator[None]:
    existing = connection.introspection.table_names()
    if existing:
        raise AssertionError(
            f"migration-restart scenario requires an empty database, got {existing!r}"
        )
    try:
        yield
    finally:
        if connection.in_atomic_block:
            raise AssertionError("migration-restart scenario leaked an atomic block")
        _drop_tables(connection)
        remaining = connection.introspection.table_names()
        if remaining:
            raise AssertionError(
                f"migration-restart cleanup leaked tables: {remaining!r}"
            )


@contextmanager
def _secondary_database() -> Iterator[Any]:
    alias = "other"
    if alias in connections.databases:
        raise AssertionError(f"database alias {alias!r} is already configured")
    with tempfile.TemporaryDirectory(prefix="godj-restart-other-") as directory:
        configuration = dict(connections.databases["default"])
        configuration["NAME"] = str(Path(directory) / "other.sqlite3")
        connections.databases[alias] = configuration
        other = connections[alias]
        try:
            yield other
        finally:
            if other.in_atomic_block:
                raise AssertionError("secondary database leaked an atomic block")
            _drop_tables(other)
            other.close()
            del connections[alias]
            del connections.databases[alias]


class _FixtureMigrationLoader(MigrationLoader):
    """Build a graph from fresh in-memory migration values and durable rows."""

    def __init__(
        self,
        database_connection: Any,
        entries: Sequence[tuple[NodeKey, Migration]],
    ) -> None:
        self._fixture_entries = tuple(entries)
        super().__init__(database_connection, load=False)
        self.build_graph()

    def load_disk(self) -> None:
        self.disk_migrations = dict(self._fixture_entries)
        self.unmigrated_apps = set()
        self.migrated_apps = {
            key[0] for key, _migration in self._fixture_entries
        }


@dataclass
class _FaultControl:
    enabled: bool = False


class _FailAfterSchemaOperation(Operation):
    reduces_to_sql = False

    def __init__(self, control: _FaultControl) -> None:
        self.control = control

    def state_forwards(self, app_label: str, state: ProjectState) -> None:
        return None

    def database_forwards(
        self,
        app_label: str,
        schema_editor: Any,
        from_state: ProjectState,
        to_state: ProjectState,
    ) -> None:
        if self.control.enabled:
            raise ConformanceRestartOperationFailure(
                "forced middle migration failure"
            )

    def database_backwards(
        self,
        app_label: str,
        schema_editor: Any,
        from_state: ProjectState,
        to_state: ProjectState,
    ) -> None:
        return None


def _linear_migrations(
    *, with_middle_failure: bool = False
) -> tuple[dict[NodeKey, Migration], _FaultControl | None]:
    a1 = Migration(_A1[1], _A1[0])
    a1.operations = [
        CreateModel(
            name="Entry",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                ("a1_marker", models.CharField(default="a1", max_length=16)),
            ],
            options={"db_table": "godj_restart_alpha"},
        )
    ]

    a2 = Migration(_A2[1], _A2[0])
    a2.dependencies = [_A1]
    a2.operations = [
        AddField(
            model_name="entry",
            name="a2_marker",
            field=models.BooleanField(default=False),
        )
    ]
    fault = _FaultControl() if with_middle_failure else None
    if fault is not None:
        a2.operations.append(_FailAfterSchemaOperation(fault))

    a3 = Migration(_A3[1], _A3[0])
    a3.dependencies = [_A2]
    a3.operations = [
        AddField(
            model_name="entry",
            name="a3_marker",
            field=models.CharField(max_length=16, null=True),
        )
    ]
    return {_A1: a1, _A2: a2, _A3: a3}, fault


def _executor_for(
    database_connection: Any,
    migrations: Mapping[NodeKey, Migration],
) -> MigrationExecutor:
    executor = MigrationExecutor(database_connection)
    executor.loader = _FixtureMigrationLoader(
        database_connection, list(migrations.items())
    )
    executor.recorder = MigrationRecorder(database_connection)
    return executor


def _apply_keys(keys: Sequence[NodeKey]) -> MigrationExecutor:
    migrations, _fault = _linear_migrations()
    executor = _executor_for(connection, migrations)
    plan = [(migrations[key], False) for key in keys]
    executor.migrate(targets=[], plan=plan)
    return executor


def _restart_plan(
    target: NodeKey,
) -> tuple[MigrationExecutor, dict[NodeKey, Migration], Plan]:
    migrations, _fault = _linear_migrations()
    executor = _executor_for(connection, migrations)
    executor.loader.check_consistent_history(connection)
    plan = executor.migration_plan([target])
    return executor, migrations, plan


def _mutation_metrics(
    statements: Sequence[str],
    before: dict[str, Any],
    after: dict[str, Any],
    *,
    restart_boundary: str,
    facts: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    metrics = {
        "ddl_statement_count": sum(kind in _DDL_KINDS for kind in statements),
        "non_select_statement_count": sum(kind != "SELECT" for kind in statements),
        "restart_boundary": restart_boundary,
        "state_unchanged": before == after,
        "write_statement_count": sum(kind in _WRITE_KINDS for kind in statements),
    }
    if facts is not None:
        metrics.update(facts)
    if not metrics["state_unchanged"]:
        raise AssertionError("restart read or planning changed database state")
    if metrics["ddl_statement_count"] != 0:
        raise AssertionError("restart read or planning executed DDL")
    if metrics["non_select_statement_count"] != 0:
        raise AssertionError("restart read or planning executed a non-SELECT statement")
    if metrics["write_statement_count"] != 0:
        raise AssertionError("restart read or planning wrote to the database")
    return metrics


def _success_observation(
    contract_id: str,
    result: Any,
    before: dict[str, Any],
    after: dict[str, Any],
    statements: Sequence[str],
    *,
    restart_boundary: str,
    facts: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": "evaluation",
        "result": normalize(result),
        "error": None,
        "db_state": normalize({"after": after, "before": before}),
        "metrics": normalize(
            _mutation_metrics(
                statements,
                before,
                after,
                restart_boundary=restart_boundary,
                facts=facts,
            )
        ),
    }


def _error_observation(
    contract_id: str,
    error: BaseException,
    before: dict[str, Any],
    after: dict[str, Any],
    statements: Sequence[str],
    *,
    facts: Mapping[str, Any],
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": "evaluation",
        "result": None,
        "error": {
            "category": "migration_history_error",
            "code": "inconsistent_applied_history",
            "message_is_contract": False,
            "python_type": f"{type(error).__module__}.{type(error).__qualname__}",
        },
        "db_state": normalize({"after": after, "before": before}),
        "metrics": normalize(
            _mutation_metrics(
                statements,
                before,
                after,
                restart_boundary="fresh_executor",
                facts=facts,
            )
        ),
    }


def absent_recorder_read(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database():
        before = _database_snapshot(connection)
        reader = MigrationRecorder(connection)
        applied, statements = _capture([connection], reader.applied_migrations)
        after = _database_snapshot(connection)
        if applied:
            raise AssertionError("an absent recorder returned applied migrations")
        if after["recorder_present"]:
            raise AssertionError("reading an absent recorder created its table")
        return _success_observation(
            contract_id,
            {"applied_migrations": _key_values(sorted(applied))},
            before,
            after,
            statements,
            restart_boundary="fresh_recorder",
        )


def empty_recorder_read(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database():
        writer = MigrationRecorder(connection)
        writer.ensure_schema()
        before = _database_snapshot(connection)
        reader = MigrationRecorder(connection)
        if reader is writer:
            raise AssertionError("restart reader reused the setup recorder")
        applied, statements = _capture([connection], reader.applied_migrations)
        after = _database_snapshot(connection)
        return _success_observation(
            contract_id,
            {"applied_migrations": _key_values(sorted(applied))},
            before,
            after,
            statements,
            restart_boundary="fresh_recorder",
        )


def record_visible_to_fresh_reader(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database():
        writer = MigrationRecorder(connection)
        writer.record_applied(*_A1)
        before = _database_snapshot(connection)
        reader = MigrationRecorder(connection)
        if reader is writer:
            raise AssertionError("restart reader reused the setup recorder")
        applied, statements = _capture([connection], reader.applied_migrations)
        after = _database_snapshot(connection)
        return _success_observation(
            contract_id,
            {
                "applied_migrations": _key_values(sorted(applied)),
                "recorded_migration": _key_value(_A1),
            },
            before,
            after,
            statements,
            restart_boundary="fresh_recorder",
            facts={
                "setup": {
                    "migration": _key_value(_A1),
                    "transition": "recorded",
                }
            },
        )


def unrecord_hidden_from_fresh_reader(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database():
        writer = MigrationRecorder(connection)
        writer.record_applied(*_A1)
        if _A1 not in writer.applied_migrations():
            raise AssertionError("setup record wasn't durable before unrecord")
        writer.record_unapplied(*_A1)
        before = _database_snapshot(connection)
        reader = MigrationRecorder(connection)
        if reader is writer:
            raise AssertionError("restart reader reused the setup recorder")
        applied, statements = _capture([connection], reader.applied_migrations)
        after = _database_snapshot(connection)
        return _success_observation(
            contract_id,
            {
                "applied_migrations": _key_values(sorted(applied)),
                "unrecorded_migration": _key_value(_A1),
            },
            before,
            after,
            statements,
            restart_boundary="fresh_recorder",
            facts={
                "setup": {
                    "migration": _key_value(_A1),
                    "transition": "recorded_then_unrecorded",
                }
            },
        )


def database_alias_isolation(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database(), _secondary_database() as other:
        default_writer = MigrationRecorder(connection)
        other_writer = MigrationRecorder(other)
        default_writer.record_applied(*_A1)
        other_writer.record_applied(*_B1)
        databases = {"default": connection, "other": other}
        before = _database_set_snapshot(databases)
        default_reader = MigrationRecorder(connection)
        other_reader = MigrationRecorder(other)
        if default_reader is default_writer or other_reader is other_writer:
            raise AssertionError("restart reader reused a setup recorder")

        def read_both() -> dict[str, Any]:
            return {
                "databases": [
                    {
                        "alias": "default",
                        "applied_migrations": _key_values(
                            sorted(default_reader.applied_migrations())
                        ),
                    },
                    {
                        "alias": "other",
                        "applied_migrations": _key_values(
                            sorted(other_reader.applied_migrations())
                        ),
                    },
                ]
            }

        result, statements = _capture([connection, other], read_both)
        after = _database_set_snapshot(databases)
        return _success_observation(
            contract_id,
            result,
            before,
            after,
            statements,
            restart_boundary="fresh_recorder",
        )


def applied_prefix_tail(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database():
        setup_executor = _apply_keys([_A1])
        before = _database_snapshot(connection)

        def restart() -> dict[str, Any]:
            executor, _migrations, plan = _restart_plan(_A3)
            if executor is setup_executor:
                raise AssertionError("restart reused the setup executor")
            return {
                "applied_migrations": _key_values(
                    sorted(executor.loader.applied_migrations)
                ),
                "plan": _plan_value(plan),
                "target": _key_value(_A3),
            }

        result, statements = _capture([connection], restart)
        after = _database_snapshot(connection)
        return _success_observation(
            contract_id,
            result,
            before,
            after,
            statements,
            restart_boundary="fresh_executor",
            facts={"graph": _graph_facts(_LINEAR_NODES, _LINEAR_DEPENDENCIES)},
        )


def fully_applied_empty_plan(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database():
        setup_executor = _apply_keys([_A1, _A2, _A3])
        before = _database_snapshot(connection)

        def restart() -> dict[str, Any]:
            executor, _migrations, plan = _restart_plan(_A3)
            if executor is setup_executor:
                raise AssertionError("restart reused the setup executor")
            return {
                "applied_migrations": _key_values(
                    sorted(executor.loader.applied_migrations)
                ),
                "plan": _plan_value(plan),
                "target": _key_value(_A3),
            }

        result, statements = _capture([connection], restart)
        after = _database_snapshot(connection)
        return _success_observation(
            contract_id,
            result,
            before,
            after,
            statements,
            restart_boundary="fresh_executor",
            facts={"graph": _graph_facts(_LINEAR_NODES, _LINEAR_DEPENDENCIES)},
        )


def unknown_legacy_record(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database():
        writer = MigrationRecorder(connection)
        writer.record_applied(*_LEGACY)
        before = _database_snapshot(connection)

        def restart() -> dict[str, Any]:
            executor, _migrations, plan = _restart_plan(_A3)
            applied = sorted(executor.loader.applied_migrations)
            known_nodes = set(executor.loader.graph.nodes)
            return {
                "applied_migrations": _key_values(applied),
                "known_applied": _key_values(
                    [key for key in applied if key in known_nodes]
                ),
                "plan": _plan_value(plan),
                "target": _key_value(_A3),
                "unknown_applied": _key_values(
                    [key for key in applied if key not in known_nodes]
                ),
            }

        result, statements = _capture([connection], restart)
        after = _database_snapshot(connection)
        return _success_observation(
            contract_id,
            result,
            before,
            after,
            statements,
            restart_boundary="fresh_executor",
            facts={"graph": _graph_facts(_LINEAR_NODES, _LINEAR_DEPENDENCIES)},
        )


def inconsistent_known_history(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database():
        MigrationRecorder(connection).record_applied(*_A2)
        before = _database_snapshot(connection)

        plan_invoked = False

        def validate_before_planning() -> None:
            nonlocal plan_invoked
            migrations, _fault = _linear_migrations()
            executor = _executor_for(connection, migrations)
            # MigrationExecutor doesn't call this itself. Django's migrate
            # command performs this explicit preflight before target planning.
            executor.loader.check_consistent_history(connection)
            plan_invoked = True
            executor.migration_plan([_A3])

        error, statements = _capture_error(
            [connection],
            validate_before_planning,
            InconsistentMigrationHistory,
        )
        after = _database_snapshot(connection)
        return _error_observation(
            contract_id,
            error,
            before,
            after,
            statements,
            facts={
                "graph": _graph_facts(_LINEAR_NODES, _LINEAR_DEPENDENCIES),
                "request": {
                    "applied_migrations": _key_values([_A2]),
                    "operation": "validate_history_before_planning",
                    "plan_invoked": plan_invoked,
                    "target": _key_value(_A3),
                },
            },
        )


def failure_tail(contract_id: str) -> dict[str, Any]:
    with _isolated_default_database():
        failed_migrations, fault = _linear_migrations(with_middle_failure=True)
        if fault is None:
            raise AssertionError("middle-failure fixture is missing its controller")
        setup_executor = _executor_for(connection, failed_migrations)
        fault.enabled = True
        initial_plan = [
            (failed_migrations[key], False) for key in (_A1, _A2, _A3)
        ]
        try:
            setup_executor.migrate(targets=[], plan=initial_plan)
        except ConformanceRestartOperationFailure:
            pass
        else:
            raise AssertionError("middle migration failure did not occur")

        before = _database_snapshot(connection)
        before_keys = [
            (item["app"], item["name"])
            for item in before["applied_migrations"]
        ]
        if before_keys != [_A1]:
            raise AssertionError(
                f"failed execution did not leave only the durable prefix: {before_keys!r}"
            )

        def restart() -> dict[str, Any]:
            executor, fresh_migrations, plan = _restart_plan(_A3)
            if executor is setup_executor:
                raise AssertionError("restart reused the failed executor")
            if any(
                fresh_migrations[key] is failed_migrations[key]
                for key in _LINEAR_NODES
            ):
                raise AssertionError("restart reused a migration fixture object")
            return {
                "applied_migrations": _key_values(
                    sorted(executor.loader.applied_migrations)
                ),
                "failed_migration": _key_value(_A2),
                "plan": _plan_value(plan),
                "target": _key_value(_A3),
            }

        result, statements = _capture([connection], restart)
        after = _database_snapshot(connection)
        return _success_observation(
            contract_id,
            result,
            before,
            after,
            statements,
            restart_boundary="fresh_executor",
            facts={"graph": _graph_facts(_LINEAR_NODES, _LINEAR_DEPENDENCIES)},
        )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.migration.restart.absent_recorder_read": absent_recorder_read,
    "django.migration.restart.empty_recorder_read": empty_recorder_read,
    "django.migration.restart.record_visible_to_fresh_reader": (
        record_visible_to_fresh_reader
    ),
    "django.migration.restart.unrecord_hidden_from_fresh_reader": (
        unrecord_hidden_from_fresh_reader
    ),
    "django.migration.restart.database_alias_isolation": database_alias_isolation,
    "django.migration.restart.applied_prefix_tail": applied_prefix_tail,
    "django.migration.restart.fully_applied_empty_plan": fully_applied_empty_plan,
    "django.migration.restart.unknown_legacy_record": unknown_legacy_record,
    "django.migration.restart.inconsistent_known_history": (
        inconsistent_known_history
    ),
    "django.migration.restart.failure_tail": failure_tail,
}
