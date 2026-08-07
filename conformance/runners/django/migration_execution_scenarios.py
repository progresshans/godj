"""Django reference adapters for explicit multi-migration plan execution.

The fixtures in this module are intentionally in-memory ``Migration`` objects.
They exercise ``MigrationExecutor.migrate(plan=...)`` without making migration
file layout, a loader CLI, or serialized operation classes part of the
compatibility contract.
"""

from __future__ import annotations

from collections.abc import Callable, Iterator, Sequence
from contextlib import contextmanager
from dataclasses import dataclass
from typing import Any

from django.db import connection, models
from django.db.migrations.exceptions import InvalidMigrationPlan
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
Plan = Sequence[tuple[Migration, bool]]

_A1 = ("alpha", "0001_initial")
_A2 = ("alpha", "0002_second")
_A3 = ("alpha", "0003_third")
_B1 = ("beta", "0001_initial")

_EXECUTION_APPS = frozenset({"alpha", "beta"})
_TABLE_PREFIX = "godj_exec_"


class ConformanceMigrationOperationFailure(RuntimeError):
    """Stable sentinel raised by a fixture operation, not by raw SQL."""


class ConformanceMigrationRecorderFailure(RuntimeError):
    """Stable sentinel raised immediately before the selected recorder write."""


def _key_value(key: NodeKey) -> dict[str, str]:
    return {"app": key[0], "name": key[1]}


def _migration_key(migration: Migration) -> NodeKey:
    return (migration.app_label, migration.name)


def _plan_value(plan: Plan) -> list[dict[str, str]]:
    return [
        {
            **_key_value(_migration_key(migration)),
            "direction": "backward" if backwards else "forward",
        }
        for migration, backwards in plan
    ]


def _state_summary(state: ProjectState) -> dict[str, Any]:
    return {
        "models": [
            {
                "app": app_label,
                "fields": sorted(model_state.fields),
                "name": model_name,
            }
            for (app_label, model_name), model_state in sorted(state.models.items())
            if app_label in _EXECUTION_APPS
        ]
    }


def _type_family(type_code: Any) -> str:
    rendered = str(type_code).lower()
    if "int" in rendered:
        return "integer"
    if "char" in rendered or "clob" in rendered or "text" in rendered:
        return "text"
    if "bool" in rendered:
        return "boolean"
    return rendered


def _managed_schema() -> list[dict[str, Any]]:
    inventory: list[dict[str, Any]] = []
    with connection.cursor() as cursor:
        for table in sorted(connection.introspection.table_names(cursor)):
            if not table.startswith(_TABLE_PREFIX):
                continue
            description = connection.introspection.get_table_description(cursor, table)
            constraints = connection.introspection.get_constraints(cursor, table)
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


def _database_snapshot() -> dict[str, Any]:
    recorder = MigrationRecorder(connection)
    recorder_present = recorder.has_table()
    records = sorted(recorder.applied_migrations()) if recorder_present else []
    return {
        "managed_schema": _managed_schema(),
        "migration_records": [_key_value(key) for key in records],
        "recorder_present": recorder_present,
    }


def _connection_facts() -> dict[str, bool]:
    select_usable = False
    try:
        with connection.cursor() as cursor:
            cursor.execute("SELECT 1")
            select_usable = cursor.fetchone() == (1,)
    except Exception:  # pragma: no cover - a failed fact is returned, not hidden.
        select_usable = False
    return {
        "autocommit_restored": connection.get_autocommit(),
        "outside_atomic_block": not connection.in_atomic_block,
        "select_usable": select_usable,
    }


@contextmanager
def _isolated_database(*, recorder_present: bool = True) -> Iterator[None]:
    existing = connection.introspection.table_names()
    if existing:
        raise AssertionError(
            f"migration-execution scenario requires an empty database, got {existing!r}"
        )
    if recorder_present:
        MigrationRecorder(connection).ensure_schema()
    try:
        yield
    finally:
        if connection.in_atomic_block:
            raise AssertionError("migration-execution scenario leaked an atomic block")
        for table in connection.introspection.table_names():
            with connection.cursor() as cursor:
                cursor.execute(
                    f"DROP TABLE {connection.ops.quote_name(table)}"
                )
        remaining = connection.introspection.table_names()
        if remaining:
            raise AssertionError(
                f"migration-execution cleanup leaked tables: {remaining!r}"
            )


class _ExecutionTrace:
    def __init__(self) -> None:
        self.events: list[dict[str, Any]] = []
        self.active_migration: NodeKey | None = None
        self.active_direction: str | None = None

    def clear(self) -> None:
        self.events.clear()
        self.active_migration = None
        self.active_direction = None

    def append(self, kind: str, action: str, **facts: Any) -> None:
        event = {
            "action": action,
            "kind": kind,
            "sequence": len(self.events) + 1,
            **facts,
        }
        self.events.append(event)

    def progress(self, action: str, *args: Any) -> None:
        facts: dict[str, Any] = {}
        if action in {"apply_start", "apply_success", "unapply_start", "unapply_success"}:
            migration = args[0]
            key = _migration_key(migration)
            direction = "backward" if action.startswith("unapply") else "forward"
            facts = {"direction": direction, "migration": _key_value(key)}
            if action.endswith("_start"):
                self.active_migration = key
                self.active_direction = direction
        self.append("progress", action, **facts)
        if action in {"apply_success", "unapply_success"}:
            self.active_migration = None
            self.active_direction = None

    def transaction_sql(self, statement: str) -> None:
        kind = statement.lstrip().split(None, 1)[0].upper() if statement.strip() else ""
        action = {
            "BEGIN": "begin",
            "COMMIT": "commit",
            "ROLLBACK": "rollback",
        }.get(kind)
        if action is None:
            return
        facts: dict[str, Any] = {}
        if self.active_migration is not None:
            facts["direction"] = self.active_direction
            facts["migration"] = _key_value(self.active_migration)
        self.append("transaction", action, **facts)


@contextmanager
def _capture_transactions(trace: _ExecutionTrace) -> Iterator[None]:
    connection.ensure_connection()
    raw_connection = connection.connection
    if raw_connection is None or not hasattr(raw_connection, "set_trace_callback"):
        raise AssertionError("exact SQLite profile must expose set_trace_callback")
    raw_connection.set_trace_callback(trace.transaction_sql)
    try:
        yield
    finally:
        raw_connection.set_trace_callback(None)


class _TracedOperation(Operation):
    """Delegate a real Django operation while exposing stable state sentinels."""

    def __init__(
        self,
        migration_key: NodeKey,
        operation_name: str,
        delegate: Operation,
        trace: _ExecutionTrace,
    ) -> None:
        self.migration_key = migration_key
        self.operation_name = operation_name
        self.delegate = delegate
        self.trace = trace

    @property
    def reversible(self) -> bool:
        return self.delegate.reversible

    @property
    def atomic(self) -> bool | None:
        return self.delegate.atomic

    @property
    def reduces_to_sql(self) -> bool:
        return self.delegate.reduces_to_sql

    def state_forwards(self, app_label: str, state: ProjectState) -> None:
        self.delegate.state_forwards(app_label, state)

    def _start(
        self,
        direction: str,
        from_state: ProjectState,
        to_state: ProjectState,
    ) -> None:
        self.trace.append(
            "operation",
            "start",
            direction=direction,
            migration=_key_value(self.migration_key),
            operation=self.operation_name,
            state_after=_state_summary(to_state),
            state_before=_state_summary(from_state),
        )

    def database_forwards(
        self,
        app_label: str,
        schema_editor: Any,
        from_state: ProjectState,
        to_state: ProjectState,
    ) -> None:
        self._start("forward", from_state, to_state)
        try:
            self.delegate.database_forwards(
                app_label, schema_editor, from_state, to_state
            )
        except Exception:
            self.trace.append(
                "operation",
                "failure",
                direction="forward",
                migration=_key_value(self.migration_key),
                operation=self.operation_name,
            )
            raise
        self.trace.append(
            "operation",
            "success",
            direction="forward",
            migration=_key_value(self.migration_key),
            operation=self.operation_name,
        )

    def database_backwards(
        self,
        app_label: str,
        schema_editor: Any,
        from_state: ProjectState,
        to_state: ProjectState,
    ) -> None:
        self._start("backward", from_state, to_state)
        try:
            self.delegate.database_backwards(
                app_label, schema_editor, from_state, to_state
            )
        except Exception:
            self.trace.append(
                "operation",
                "failure",
                direction="backward",
                migration=_key_value(self.migration_key),
                operation=self.operation_name,
            )
            raise
        self.trace.append(
            "operation",
            "success",
            direction="backward",
            migration=_key_value(self.migration_key),
            operation=self.operation_name,
        )

    def describe(self) -> str:
        return self.operation_name


@dataclass
class _FaultControl:
    direction: str
    enabled: bool = False


class _FaultOperation(Operation):
    reduces_to_sql = False

    def __init__(
        self,
        migration_key: NodeKey,
        operation_name: str,
        trace: _ExecutionTrace,
        control: _FaultControl,
    ) -> None:
        self.migration_key = migration_key
        self.operation_name = operation_name
        self.trace = trace
        self.control = control

    def state_forwards(self, app_label: str, state: ProjectState) -> None:
        return None

    def _run(
        self,
        direction: str,
        from_state: ProjectState,
        to_state: ProjectState,
    ) -> None:
        self.trace.append(
            "operation",
            "start",
            direction=direction,
            migration=_key_value(self.migration_key),
            operation=self.operation_name,
            state_after=_state_summary(to_state),
            state_before=_state_summary(from_state),
        )
        if self.control.enabled and self.control.direction == direction:
            self.trace.append(
                "operation",
                "failure",
                direction=direction,
                migration=_key_value(self.migration_key),
                operation=self.operation_name,
            )
            raise ConformanceMigrationOperationFailure(
                f"forced {direction} operation failure"
            )
        self.trace.append(
            "operation",
            "success",
            direction=direction,
            migration=_key_value(self.migration_key),
            operation=self.operation_name,
        )

    def database_forwards(
        self,
        app_label: str,
        schema_editor: Any,
        from_state: ProjectState,
        to_state: ProjectState,
    ) -> None:
        self._run("forward", from_state, to_state)

    def database_backwards(
        self,
        app_label: str,
        schema_editor: Any,
        from_state: ProjectState,
        to_state: ProjectState,
    ) -> None:
        self._run("backward", from_state, to_state)


class _FixtureMigrationLoader(MigrationLoader):
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


class _TracingExecutor(MigrationExecutor):
    def __init__(
        self,
        loader: MigrationLoader,
        trace: _ExecutionTrace,
        *,
        recorder_fault: tuple[NodeKey, str] | None = None,
    ) -> None:
        super().__init__(connection, progress_callback=trace.progress)
        self.loader = loader
        self.recorder = MigrationRecorder(connection)
        self._trace = trace
        self._recorder_fault = recorder_fault

    def record_migration(
        self,
        app_label: str,
        name: str,
        forward: bool = True,
    ) -> None:
        key = (app_label, name)
        direction = "forward" if forward else "backward"
        self._trace.append(
            "recorder",
            "start",
            direction=direction,
            migration=_key_value(key),
        )
        if self._recorder_fault == (key, direction):
            self._trace.append(
                "recorder",
                "failure",
                direction=direction,
                migration=_key_value(key),
            )
            raise ConformanceMigrationRecorderFailure(
                f"forced {direction} recorder failure"
            )
        super().record_migration(app_label, name, forward=forward)
        self._trace.append(
            "recorder",
            "success",
            direction=direction,
            migration=_key_value(key),
        )


def _migration_fixture(
    trace: _ExecutionTrace,
    *,
    include_branch: bool = False,
    operation_fault: str | None = None,
) -> tuple[dict[NodeKey, Migration], _FaultControl | None]:
    fault_control = (
        _FaultControl(operation_fault) if operation_fault is not None else None
    )

    a1 = Migration(_A1[1], _A1[0])
    a1.operations = [
        _TracedOperation(
            _A1,
            "create_alpha_entry",
            CreateModel(
                name="Entry",
                fields=[
                    ("id", models.AutoField(primary_key=True)),
                    ("a1_marker", models.CharField(default="a1", max_length=16)),
                ],
                options={"db_table": "godj_exec_alpha"},
            ),
            trace,
        )
    ]

    a2 = Migration(_A2[1], _A2[0])
    a2.dependencies = [_A1]
    add_a2 = _TracedOperation(
        _A2,
        "add_a2_marker",
        AddField(
            model_name="entry",
            name="a2_marker",
            field=models.BooleanField(default=False),
        ),
        trace,
    )
    if operation_fault == "forward":
        a2.operations = [
            add_a2,
            _FaultOperation(
                _A2, "fail_a2_forward", trace, fault_control  # type: ignore[arg-type]
            ),
        ]
    elif operation_fault == "backward":
        a2.operations = [
            _FaultOperation(
                _A2, "fail_a2_backward", trace, fault_control  # type: ignore[arg-type]
            ),
            add_a2,
        ]
    else:
        a2.operations = [add_a2]

    a3 = Migration(_A3[1], _A3[0])
    a3.dependencies = [_A2]
    a3.operations = [
        _TracedOperation(
            _A3,
            "add_a3_marker",
            AddField(
                model_name="entry",
                name="a3_marker",
                field=models.CharField(max_length=16, null=True),
            ),
            trace,
        )
    ]

    migrations = {_A1: a1, _A2: a2, _A3: a3}
    if include_branch:
        b1 = Migration(_B1[1], _B1[0])
        b1.dependencies = [_A1]
        b1.operations = [
            _TracedOperation(
                _B1,
                "create_beta_branch",
                CreateModel(
                    name="Branch",
                    fields=[
                        ("id", models.AutoField(primary_key=True)),
                        (
                            "b1_marker",
                            models.CharField(default="b1", max_length=16),
                        ),
                    ],
                    options={"db_table": "godj_exec_beta"},
                ),
                trace,
            )
        ]
        migrations[_B1] = b1
    return migrations, fault_control


def _loader(migrations: dict[NodeKey, Migration]) -> _FixtureMigrationLoader:
    return _FixtureMigrationLoader(connection, list(migrations.items()))


def _executor(
    migrations: dict[NodeKey, Migration],
    trace: _ExecutionTrace,
    *,
    recorder_fault: tuple[NodeKey, str] | None = None,
) -> _TracingExecutor:
    return _TracingExecutor(
        _loader(migrations),
        trace,
        recorder_fault=recorder_fault,
    )


def _seed(
    migrations: dict[NodeKey, Migration],
    keys: Sequence[NodeKey],
    trace: _ExecutionTrace,
) -> None:
    plan = [(migrations[key], False) for key in keys]
    _executor(migrations, trace).migrate(targets=[], plan=plan)
    trace.clear()


def _step_metrics(
    trace: _ExecutionTrace,
    migration: Migration,
    backwards: bool,
    after_records: set[NodeKey],
    *,
    include_historical_transition: bool,
) -> dict[str, Any]:
    key = _migration_key(migration)
    key_value = _key_value(key)
    direction = "backward" if backwards else "forward"
    base = {**key_value, "direction": direction}
    indexed_events = [
        (index, event)
        for index, event in enumerate(trace.events)
        if event.get("migration") == key_value
        and event.get("direction") == direction
    ]
    start_action = "unapply_start" if backwards else "apply_start"
    success_action = "unapply_success" if backwards else "apply_success"
    progress_starts = [
        index
        for index, event in indexed_events
        if event["kind"] == "progress" and event["action"] == start_action
    ]
    if not progress_starts:
        if indexed_events:
            raise AssertionError(
                f"{key!r} emitted execution facts without a progress start"
            )
        return {
            **base,
            "recorder_outcome": "not_started",
            "schema_outcome": "not_started",
            "status": "not_started",
            "transaction_model": "none",
        }
    if len(progress_starts) != 1:
        raise AssertionError(f"{key!r} started more than once")

    operation_starts = [
        event
        for _index, event in indexed_events
        if event["kind"] == "operation" and event["action"] == "start"
    ]
    if not operation_starts:
        raise AssertionError(f"{key!r} started without an operation state boundary")
    operation_failures = [
        index
        for index, event in indexed_events
        if event["kind"] == "operation" and event["action"] == "failure"
    ]
    recorder_starts = [
        index
        for index, event in indexed_events
        if event["kind"] == "recorder" and event["action"] == "start"
    ]
    recorder_successes = [
        index
        for index, event in indexed_events
        if event["kind"] == "recorder" and event["action"] == "success"
    ]
    recorder_failures = [
        index
        for index, event in indexed_events
        if event["kind"] == "recorder" and event["action"] == "failure"
    ]
    commits = [
        index
        for index, event in indexed_events
        if event["kind"] == "transaction" and event["action"] == "commit"
    ]
    rollbacks = [
        index
        for index, event in indexed_events
        if event["kind"] == "transaction" and event["action"] == "rollback"
    ]
    progress_successes = [
        index
        for index, event in indexed_events
        if event["kind"] == "progress" and event["action"] == success_action
    ]

    if len(operation_failures) > 1 or len(recorder_failures) > 1:
        raise AssertionError(f"{key!r} emitted more than one failure boundary")
    if operation_failures and recorder_failures:
        raise AssertionError(f"{key!r} failed in both operation and recorder")

    transaction_model = "schema_then_record" if backwards else "schema_and_record"
    step = {
        **base,
        "transaction_model": transaction_model,
    }
    if include_historical_transition:
        step.update(
            historical_state_after=operation_starts[-1]["state_after"],
            historical_state_before=operation_starts[0]["state_before"],
        )

    if operation_failures:
        if len(rollbacks) != 1 or commits or recorder_starts:
            raise AssertionError(
                f"{key!r} operation failure did not stay inside one rollback boundary"
            )
        step.update(
            recorder_outcome="not_started",
            schema_outcome="rolled_back",
            status="rolled_back",
        )
        return step

    if recorder_failures:
        if len(recorder_starts) != 1 or recorder_successes or progress_successes:
            raise AssertionError(f"{key!r} recorder failure trace is inconsistent")
        step["fault_point"] = "before_record_write"
        if backwards:
            if not commits or commits[0] > recorder_failures[0] or rollbacks:
                raise AssertionError(
                    f"{key!r} backward recorder failure lost its schema commit"
                )
            if key not in after_records:
                raise AssertionError(
                    f"{key!r} backward recorder failure did not retain its record"
                )
            step.update(
                recorder_outcome="retained",
                schema_outcome="reversed",
                status="schema_committed_record_failed",
            )
        else:
            if commits or len(rollbacks) != 1 or key in after_records:
                raise AssertionError(
                    f"{key!r} forward recorder failure did not roll back atomically"
                )
            step.update(
                recorder_outcome="failed",
                schema_outcome="rolled_back",
                status="rolled_back",
            )
        return step

    if len(progress_successes) != 1 or len(recorder_successes) != 1:
        raise AssertionError(f"{key!r} has no recognized terminal execution outcome")
    if not commits or rollbacks:
        raise AssertionError(f"{key!r} completed without a schema commit")
    if backwards:
        if not recorder_starts or commits[0] > recorder_starts[0]:
            raise AssertionError(
                f"{key!r} backward recorder ran before its schema commit"
            )
        if key in after_records:
            raise AssertionError(f"{key!r} backward record was not removed")
        recorder_outcome = "unapplied"
    else:
        if not recorder_starts or commits[0] < recorder_successes[0]:
            raise AssertionError(
                f"{key!r} forward schema committed before its recorder write"
            )
        if key not in after_records:
            raise AssertionError(f"{key!r} forward record was not applied")
        recorder_outcome = "applied"
    step.update(
        recorder_outcome=recorder_outcome,
        schema_outcome="reversed" if backwards else "applied",
        status="committed",
    )
    return step


def _metrics(
    trace: _ExecutionTrace,
    plan: Plan,
    after: dict[str, Any],
    *,
    include_historical_transitions: bool = False,
) -> dict[str, Any]:
    after_records = {
        (item["app"], item["name"])
        for item in after["migration_records"]
    }
    return {
        "connection": _connection_facts(),
        "steps": [
            _step_metrics(
                trace,
                migration,
                backwards,
                after_records,
                include_historical_transition=include_historical_transitions,
            )
            for migration, backwards in plan
        ],
    }


def _success_observation(
    contract_id: str,
    plan: Plan,
    state: ProjectState,
    before: dict[str, Any],
    after: dict[str, Any],
    trace: _ExecutionTrace,
    *,
    include_historical_transitions: bool,
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": "commit",
        "result": normalize(
            {"plan": _plan_value(plan), "returned_state": _state_summary(state)}
        ),
        "error": None,
        "db_state": normalize({"after": after, "before": before}),
        "metrics": normalize(
            _metrics(
                trace,
                plan,
                after,
                include_historical_transitions=include_historical_transitions,
            )
        ),
    }


def _error_observation(
    contract_id: str,
    plan: Plan,
    error: BaseException,
    *,
    phase: str,
    category: str,
    code: str,
    before: dict[str, Any],
    after: dict[str, Any],
    trace: _ExecutionTrace,
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": phase,
        "result": None,
        "error": {
            "category": category,
            "code": code,
            "message_is_contract": False,
            "python_type": f"{type(error).__module__}.{type(error).__qualname__}",
        },
        "db_state": normalize({"after": after, "before": before}),
        "metrics": normalize(_metrics(trace, plan, after)),
    }


def _run_success(
    contract_id: str,
    migrations: dict[NodeKey, Migration],
    keys: Sequence[tuple[NodeKey, bool]],
    trace: _ExecutionTrace,
    *,
    include_historical_transitions: bool = False,
) -> dict[str, Any]:
    plan = [(migrations[key], backwards) for key, backwards in keys]
    executor = _executor(migrations, trace)
    before = _database_snapshot()
    with _capture_transactions(trace):
        state = executor.migrate(targets=[], plan=plan)
    after = _database_snapshot()
    return _success_observation(
        contract_id,
        plan,
        state,
        before,
        after,
        trace,
        include_historical_transitions=include_historical_transitions,
    )


def _run_error(
    contract_id: str,
    migrations: dict[NodeKey, Migration],
    keys: Sequence[tuple[NodeKey, bool]],
    trace: _ExecutionTrace,
    expected: type[BaseException],
    *,
    phase: str,
    category: str,
    code: str,
    recorder_fault: tuple[NodeKey, str] | None = None,
) -> dict[str, Any]:
    plan = [(migrations[key], backwards) for key, backwards in keys]
    executor = _executor(migrations, trace, recorder_fault=recorder_fault)
    before = _database_snapshot()
    try:
        with _capture_transactions(trace):
            executor.migrate(targets=[], plan=plan)
    except expected as error:
        after = _database_snapshot()
        return _error_observation(
            contract_id,
            plan,
            error,
            phase=phase,
            category=category,
            code=code,
            before=before,
            after=after,
            trace=trace,
        )
    raise AssertionError(f"expected {expected.__name__}")


def linear_forward_execution(contract_id: str) -> dict[str, Any]:
    with _isolated_database():
        trace = _ExecutionTrace()
        migrations, _fault = _migration_fixture(trace)
        return _run_success(
            contract_id,
            migrations,
            [(_A1, False), (_A2, False), (_A3, False)],
            trace,
        )


def linear_backward_execution(contract_id: str) -> dict[str, Any]:
    with _isolated_database():
        trace = _ExecutionTrace()
        migrations, _fault = _migration_fixture(trace)
        _seed(migrations, [_A1, _A2, _A3], trace)
        return _run_success(
            contract_id,
            migrations,
            [(_A3, True), (_A2, True), (_A1, True)],
            trace,
        )


def applied_prefix_tail_execution(contract_id: str) -> dict[str, Any]:
    with _isolated_database():
        trace = _ExecutionTrace()
        migrations, _fault = _migration_fixture(trace)
        _seed(migrations, [_A1], trace)
        return _run_success(
            contract_id,
            migrations,
            [(_A2, False), (_A3, False)],
            trace,
            include_historical_transitions=True,
        )


def rollback_branch_preserves_unrelated(contract_id: str) -> dict[str, Any]:
    with _isolated_database():
        trace = _ExecutionTrace()
        migrations, _fault = _migration_fixture(trace, include_branch=True)
        _seed(migrations, [_A1, _A2, _A3, _B1], trace)
        return _run_success(
            contract_id,
            migrations,
            [(_A3, True), (_A2, True)],
            trace,
        )


def forward_operation_failure(contract_id: str) -> dict[str, Any]:
    with _isolated_database():
        trace = _ExecutionTrace()
        migrations, fault = _migration_fixture(trace, operation_fault="forward")
        if fault is None:
            raise AssertionError("forward fault fixture is missing its controller")
        fault.enabled = True
        return _run_error(
            contract_id,
            migrations,
            [(_A1, False), (_A2, False), (_A3, False)],
            trace,
            ConformanceMigrationOperationFailure,
            phase="rollback",
            category="migration_execution_error",
            code="operation_failed",
        )


def backward_operation_failure(contract_id: str) -> dict[str, Any]:
    with _isolated_database():
        trace = _ExecutionTrace()
        migrations, fault = _migration_fixture(trace, operation_fault="backward")
        if fault is None:
            raise AssertionError("backward fault fixture is missing its controller")
        _seed(migrations, [_A1, _A2, _A3], trace)
        fault.enabled = True
        return _run_error(
            contract_id,
            migrations,
            [(_A3, True), (_A2, True), (_A1, True)],
            trace,
            ConformanceMigrationOperationFailure,
            phase="rollback",
            category="migration_execution_error",
            code="operation_failed",
        )


def forward_recorder_failure(contract_id: str) -> dict[str, Any]:
    with _isolated_database():
        trace = _ExecutionTrace()
        migrations, _fault = _migration_fixture(trace)
        return _run_error(
            contract_id,
            migrations,
            [(_A1, False), (_A2, False), (_A3, False)],
            trace,
            ConformanceMigrationRecorderFailure,
            phase="rollback",
            category="migration_recorder_error",
            code="record_failed",
            recorder_fault=(_A2, "forward"),
        )


def backward_recorder_failure(contract_id: str) -> dict[str, Any]:
    with _isolated_database():
        trace = _ExecutionTrace()
        migrations, _fault = _migration_fixture(trace)
        _seed(migrations, [_A1, _A2, _A3], trace)
        return _run_error(
            contract_id,
            migrations,
            [(_A3, True), (_A2, True), (_A1, True)],
            trace,
            ConformanceMigrationRecorderFailure,
            phase="commit",
            category="migration_recorder_error",
            code="record_failed",
            recorder_fault=(_A2, "backward"),
        )


def mixed_direction_rejected(contract_id: str) -> dict[str, Any]:
    with _isolated_database():
        trace = _ExecutionTrace()
        migrations, _fault = _migration_fixture(trace)
        return _run_error(
            contract_id,
            migrations,
            [(_A1, False), (_A2, True)],
            trace,
            InvalidMigrationPlan,
            phase="evaluation",
            category="migration_execution_error",
            code="mixed_directions",
        )


def empty_plan_noop(contract_id: str) -> dict[str, Any]:
    with _isolated_database(recorder_present=False):
        trace = _ExecutionTrace()
        migrations, _fault = _migration_fixture(trace)
        return _run_success(contract_id, migrations, [], trace)


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.migration.execute.linear_forward": linear_forward_execution,
    "django.migration.execute.linear_backward": linear_backward_execution,
    "django.migration.execute.applied_prefix_tail": applied_prefix_tail_execution,
    "django.migration.execute.rollback_branch_preserves_unrelated": (
        rollback_branch_preserves_unrelated
    ),
    "django.migration.execute.forward_operation_failure": (
        forward_operation_failure
    ),
    "django.migration.execute.backward_operation_failure": (
        backward_operation_failure
    ),
    "django.migration.execute.forward_recorder_failure": forward_recorder_failure,
    "django.migration.execute.backward_recorder_failure": (
        backward_recorder_failure
    ),
    "django.migration.execute.mixed_direction_rejected": mixed_direction_rejected,
    "django.migration.execute.empty_plan": empty_plan_noop,
}
