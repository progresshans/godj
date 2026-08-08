"""Django reference adapters for end-to-end migration lifecycle contracts.

Every scenario uses a disposable file-backed SQLite database. Durable setup is
completed before the observation capture, then the connection, migration
values, loader, recorder, and executor are replaced. The observed route is the
public orchestration used by Django's migrate command: explicit history
preflight, public planning, and ``MigrationExecutor.migrate()``.

Migration file discovery is intentionally outside this contract. A fixture
``MigrationLoader.load_disk()`` seam injects fresh in-memory definitions while
the real loader graph, recorder history, executor, schema editor, and SQLite
connection remain in use.
"""

from __future__ import annotations

import tempfile
from collections.abc import Callable, Iterator, Mapping, Sequence
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from django.db import connections, models
from django.db.migrations.exceptions import InconsistentMigrationHistory
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.loader import MigrationLoader
from django.db.migrations.migration import Migration
from django.db.migrations.operations.base import Operation
from django.db.migrations.operations.fields import AddField
from django.db.migrations.operations.models import CreateModel
from django.db.migrations.recorder import MigrationRecorder
from django.db.migrations.state import ProjectState
from django.db.models.fields import NOT_PROVIDED

from .normalizer import normalize
from .scenarios import configure_django


configure_django()


NodeKey = tuple[str, str]
Plan = Sequence[tuple[Migration, bool]]
TargetSelector = Callable[[MigrationExecutor], Sequence[tuple[str, str | None]]]

_A1 = ("alpha", "0001_initial")
_A2 = ("alpha", "0002_second")
_A3 = ("alpha", "0003_third")
_B1 = ("beta", "0001_initial")
_LEGACY = ("legacy", "0099_retired")

_NODES = (_A1, _A2, _A3, _B1)
_A3_FIELD_NAME = "a3_marker"
_LATEST_TARGETS = (_A3, _B1)
_PREFIX_SEED_TARGETS = (_A1,)
_FULL_SEED_TARGETS = (_A3, _B1)
_NAMED_FORWARD_TARGETS = (_A2,)
_NAMED_REVERSE_TARGETS = (_A1,)
_ZERO_TARGETS = (("alpha", None),)
_MANAGED_TABLE_PREFIX = "godj_lifecycle_"
_DATABASE_ALIAS = "godj_lifecycle_reference"
_DDL_KINDS = frozenset({"ALTER", "CREATE", "DROP", "TRUNCATE"})
_WRITE_KINDS = frozenset({"DELETE", "INSERT", "REPLACE", "UPDATE"})
_FIELD_KINDS = {
    "AutoField": "auto",
    "BooleanField": "boolean",
    "CharField": "char",
}


class ConformanceLifecycleOperationFailure(RuntimeError):
    """Stable fixture failure raised after A2 has attempted its schema change."""


def _key_value(key: tuple[str, str | None]) -> dict[str, str | None]:
    return {"app": key[0], "name": key[1]}


def _key_values(
    keys: Sequence[tuple[str, str | None]],
) -> list[dict[str, str | None]]:
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


@dataclass
class _FaultControl:
    enabled: bool = False


class _FailureOperation(Operation):
    """No-state fixture operation that can fail after A2's AddField."""

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
            raise ConformanceLifecycleOperationFailure(
                "forced middle lifecycle failure"
            )

    def database_backwards(
        self,
        app_label: str,
        schema_editor: Any,
        from_state: ProjectState,
        to_state: ProjectState,
    ) -> None:
        return None


def _fixture_migrations() -> tuple[dict[NodeKey, Migration], _FaultControl]:
    control = _FaultControl()

    a1 = Migration(_A1[1], _A1[0])
    a1.operations = [
        CreateModel(
            name="Entry",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                (
                    "a1_marker",
                    models.CharField(default="a1", max_length=16),
                ),
            ],
            options={"db_table": "godj_lifecycle_alpha"},
        )
    ]

    a2 = Migration(_A2[1], _A2[0])
    a2.dependencies = [_A1]
    a2.operations = [
        AddField(
            model_name="entry",
            name="a2_marker",
            field=models.BooleanField(default=False),
        ),
        _FailureOperation(control),
    ]

    a3 = Migration(_A3[1], _A3[0])
    a3.dependencies = [_A2]
    a3.operations = [
        AddField(
            model_name="entry",
            name=_A3_FIELD_NAME,
            field=models.CharField(max_length=16, null=True),
        )
    ]

    b1 = Migration(_B1[1], _B1[0])
    b1.dependencies = [_A1]
    b1.operations = [
        CreateModel(
            name="Branch",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                (
                    "b1_marker",
                    models.CharField(default="b1", max_length=16),
                ),
            ],
            options={"db_table": "godj_lifecycle_beta"},
        )
    ]

    return {_A1: a1, _A2: a2, _A3: a3, _B1: b1}, control


class _FixtureMigrationLoader(MigrationLoader):
    """Use real graph/history loading with fresh in-memory definitions."""

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


def _fixture_executor(
    database_connection: Any,
    migrations: Mapping[NodeKey, Migration],
    trace: _ExecutionTrace | None = None,
) -> MigrationExecutor:
    executor = MigrationExecutor(
        database_connection,
        progress_callback=trace.progress if trace is not None else None,
    )
    executor.loader = _FixtureMigrationLoader(
        database_connection,
        list(migrations.items()),
    )
    executor.recorder = MigrationRecorder(database_connection)
    return executor


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
        raise AssertionError(
            f"lifecycle database alias {_DATABASE_ALIAS!r} is already configured"
        )
    with tempfile.TemporaryDirectory(prefix="godj-lifecycle-") as directory:
        configuration = dict(connections.databases["default"])
        configuration["NAME"] = str(Path(directory) / "lifecycle.sqlite3")
        connections.databases[_DATABASE_ALIAS] = configuration
        session = _DatabaseSession(
            alias=_DATABASE_ALIAS,
            connection=connections[_DATABASE_ALIAS],
        )
        try:
            if session.connection.introspection.table_names():
                raise AssertionError("lifecycle database did not start empty")
            yield session
        finally:
            if session.connection.in_atomic_block:
                raise AssertionError("lifecycle scenario leaked an atomic block")
            session.connection.close()
            del connections[_DATABASE_ALIAS]
            del connections.databases[_DATABASE_ALIAS]


def _default_value(
    field: models.Field[Any, Any], field_kind: str
) -> dict[str, Any]:
    default = field.default
    if default is NOT_PROVIDED:
        return {"present": False, "type": "absent", "value": None}
    if callable(default):
        raise AssertionError("callable defaults are outside this contract")
    if field_kind == "boolean" and isinstance(default, bool):
        default_type = "bool"
    elif field_kind == "char" and isinstance(default, str):
        default_type = "string"
    else:
        raise AssertionError(
            f"unsupported lifecycle default: {type(default).__name__}"
        )
    return {"present": True, "type": default_type, "value": default}


def _state_value(state: ProjectState) -> dict[str, Any]:
    apps: dict[str, list[dict[str, Any]]] = {}
    for (app_label, model_key), model_state in sorted(state.models.items()):
        db_table = model_state.options.get("db_table")
        if not isinstance(db_table, str) or not db_table:
            raise AssertionError("lifecycle fixture requires explicit db_table")
        fields = []
        for field_name, field in model_state.fields.items():
            try:
                field_kind = _FIELD_KINDS[field.get_internal_type()]
            except KeyError as error:
                raise AssertionError(
                    f"unsupported lifecycle field: {field.get_internal_type()}"
                ) from error
            max_length = field.max_length
            if field_kind == "char":
                if (
                    isinstance(max_length, bool)
                    or not isinstance(max_length, int)
                    or max_length <= 0
                ):
                    raise AssertionError("char max_length must be positive")
            elif max_length is not None:
                raise AssertionError("non-char max_length must be null")
            fields.append(
                {
                    "column": field.db_column or field_name,
                    "default": _default_value(field, field_kind),
                    "kind": field_kind,
                    "max_length": max_length,
                    "name": field_name,
                    "nullable": field.null,
                    "primary_key": field.primary_key,
                }
            )
        apps.setdefault(app_label, []).append(
            {
                "db_table": db_table,
                "fields": fields,
                "name": model_key,
            }
        )
    return {
        "apps": [
            {
                "label": app_label,
                "models": sorted(app_models, key=lambda item: item["name"]),
            }
            for app_label, app_models in sorted(apps.items())
        ],
        "format_version": 1,
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


def _database_snapshot(database_connection: Any) -> dict[str, Any]:
    recorder = MigrationRecorder(database_connection)
    recorder_present = recorder.has_table()
    records = (
        sorted(recorder.applied_migrations()) if recorder_present else []
    )
    managed_schema: list[dict[str, Any]] = []
    with database_connection.cursor() as cursor:
        for table in sorted(
            database_connection.introspection.table_names(cursor)
        ):
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
            managed_schema.append(
                {
                    "columns": [
                        {
                            "name": column.name,
                            "nullable": column.null_ok,
                            "primary_key": column.name in primary_key_columns,
                            "type_family": _type_family(column.type_code),
                        }
                        for column in sorted(
                            description, key=lambda item: item.name
                        )
                    ],
                    "name": table,
                }
            )
    return {
        "managed_schema": managed_schema,
        "migration_records": _key_values(records),
        "recorder_present": recorder_present,
    }


def _statement_kind(sql: str) -> str:
    rendered = sql.lstrip().upper()
    return rendered.split(None, 1)[0] if rendered else "EMPTY"


class _ExecutionTrace:
    def __init__(self) -> None:
        self.active: tuple[NodeKey, str] | None = None
        self.started: list[tuple[NodeKey, str]] = []
        self.succeeded: list[tuple[NodeKey, str]] = []
        self.transactions: list[
            tuple[str, tuple[NodeKey, str] | None]
        ] = []
        self.statement_kinds: list[str] = []

    def progress(self, action: str, *args: Any) -> None:
        if action in {"apply_start", "unapply_start"}:
            migration = args[0]
            direction = "backward" if action.startswith("un") else "forward"
            self.active = (_migration_key(migration), direction)
            self.started.append(self.active)
        elif action in {"apply_success", "unapply_success"}:
            if self.active is None:
                raise AssertionError("lifecycle success has no active migration")
            self.succeeded.append(self.active)
            self.active = None

    def transaction_sql(self, statement: str) -> None:
        kind = _statement_kind(statement)
        action = {
            "BEGIN": "begin",
            "COMMIT": "commit",
            "ROLLBACK": "rollback",
        }.get(kind)
        if action is not None:
            self.transactions.append((action, self.active))

    def execute_wrapper(
        self,
        execute: Callable[..., Any],
        sql: str,
        params: Any,
        many: bool,
        context: Any,
    ) -> Any:
        self.statement_kinds.append(_statement_kind(sql))
        return execute(sql, params, many, context)


@contextmanager
def _capture(
    database_connection: Any,
    trace: _ExecutionTrace,
) -> Iterator[None]:
    database_connection.ensure_connection()
    raw_connection = database_connection.connection
    if raw_connection is None or not hasattr(
        raw_connection, "set_trace_callback"
    ):
        raise AssertionError("exact SQLite profile requires set_trace_callback")
    raw_connection.set_trace_callback(trace.transaction_sql)
    try:
        with database_connection.execute_wrapper(trace.execute_wrapper):
            yield
    finally:
        raw_connection.set_trace_callback(None)


def _connection_facts(database_connection: Any) -> dict[str, bool]:
    select_usable = False
    try:
        with database_connection.cursor() as cursor:
            cursor.execute("SELECT 1")
            select_usable = cursor.fetchone() == (1,)
    except Exception:  # pragma: no cover - returned as a fact, not hidden.
        select_usable = False
    return {
        "autocommit_restored": database_connection.get_autocommit(),
        "outside_atomic_block": not database_connection.in_atomic_block,
        "select_usable": select_usable,
    }


def _step_values(
    plan: Plan,
    trace: _ExecutionTrace,
    after: Mapping[str, Any],
) -> list[dict[str, str]]:
    expected = [
        (_migration_key(migration), "backward" if backwards else "forward")
        for migration, backwards in plan
    ]
    if trace.started != expected[: len(trace.started)]:
        raise AssertionError("lifecycle execution did not start a plan prefix")
    if trace.succeeded != trace.started[: len(trace.succeeded)]:
        raise AssertionError("lifecycle success order differs from starts")
    if len(trace.started) - len(trace.succeeded) > 1:
        raise AssertionError("more than one lifecycle step remained active")

    after_records = {
        (item["app"], item["name"])
        for item in after["migration_records"]
    }
    started = set(trace.started)
    succeeded = set(trace.succeeded)
    steps = []
    for key, direction in expected:
        identity = (key, direction)
        if identity not in started:
            outcome = "not_started"
            schema_outcome = "not_started"
            recorder_outcome = "not_started"
        elif identity not in succeeded:
            outcome = "rolled_back"
            schema_outcome = "rolled_back"
            recorder_outcome = "not_started"
        else:
            outcome = "committed"
            schema_outcome = "reversed" if direction == "backward" else "applied"
            recorder_outcome = (
                "unapplied" if direction == "backward" else "applied"
            )

        if outcome == "committed":
            if direction == "forward" and key not in after_records:
                raise AssertionError(f"committed forward step {key!r} is unrecorded")
            if direction == "backward" and key in after_records:
                raise AssertionError(f"committed backward step {key!r} is recorded")
        if outcome == "rolled_back" and key in after_records:
            raise AssertionError(f"rolled-back step {key!r} was recorded")

        steps.append(
            {
                "app": key[0],
                "direction": direction,
                "name": key[1],
                "outcome": outcome,
                "recorder_outcome": recorder_outcome,
                "schema_outcome": schema_outcome,
            }
        )
    return steps


def _assert_forward_transaction_outcomes(
    steps: Sequence[Mapping[str, str]],
    trace: _ExecutionTrace,
) -> None:
    for step in steps:
        if step["direction"] != "forward" or step["outcome"] == "not_started":
            continue
        identity = ((step["app"], step["name"]), "forward")
        actions = [
            action
            for action, active in trace.transactions
            if active == identity
        ]
        if step["outcome"] == "committed":
            if "commit" not in actions or "rollback" in actions:
                raise AssertionError(
                    f"forward step {identity!r} did not commit cleanly"
                )
        elif step["outcome"] == "rolled_back":
            if "rollback" not in actions or "commit" in actions:
                raise AssertionError(
                    f"forward step {identity!r} did not roll back cleanly"
                )


def _effects(
    trace: _ExecutionTrace,
    before: Mapping[str, Any],
    after: Mapping[str, Any],
) -> dict[str, bool]:
    return {
        "database_state_changed": before != after,
        "ddl_observed": any(
            kind in _DDL_KINDS for kind in trace.statement_kinds
        ),
        "transaction_observed": bool(trace.transactions),
        "write_observed": any(
            kind in _WRITE_KINDS for kind in trace.statement_kinds
        ),
    }


def _recorder_bootstrap(
    before: Mapping[str, Any], after: Mapping[str, Any]
) -> str:
    if before["recorder_present"]:
        return "existing"
    if after["recorder_present"]:
        return "created"
    return "absent"


def _seed(
    database_connection: Any,
    targets: Sequence[tuple[str, str | None]],
) -> None:
    migrations, _control = _fixture_migrations()
    executor = _fixture_executor(database_connection, migrations)
    executor.loader.check_consistent_history(database_connection)
    plan = executor.migration_plan(list(targets))
    executor.migrate(list(targets), plan=plan)


def _latest_targets(executor: MigrationExecutor) -> Sequence[NodeKey]:
    return executor.loader.graph.leaf_nodes()


def _fixed_targets(
    targets: Sequence[tuple[str, str | None]],
) -> TargetSelector:
    frozen = tuple(targets)
    return lambda _executor: frozen


def _error_value(
    error: BaseException,
    *,
    category: str,
    code: str,
) -> dict[str, Any]:
    return {
        "category": category,
        "code": code,
        "message_is_contract": False,
        "python_type": f"{type(error).__module__}.{type(error).__qualname__}",
    }


def _run_lifecycle(
    contract_id: str,
    database_connection: Any,
    *,
    phase: str,
    target_mode: str,
    target_selector: TargetSelector,
    request_targets: Sequence[tuple[str, str | None]],
    fail_middle: bool = False,
    expected_error: type[BaseException] | None = None,
    error_category: str | None = None,
    error_code: str | None = None,
    extra_metrics: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    before = _database_snapshot(database_connection)
    trace = _ExecutionTrace()
    history_check_invoked = False
    history_valid = False
    plan_invoked = False
    migrate_invoked = False
    plan: Plan = []
    targets = list(request_targets)
    state: ProjectState | None = None
    caught: BaseException | None = None

    try:
        with _capture(database_connection, trace):
            migrations, control = _fixture_migrations()
            executor = _fixture_executor(database_connection, migrations, trace)
            history_check_invoked = True
            executor.loader.check_consistent_history(database_connection)
            history_valid = True
            targets = list(target_selector(executor))
            plan_invoked = True
            plan = executor.migration_plan(targets)
            control.enabled = fail_middle
            migrate_invoked = True
            state = executor.migrate(targets, plan=plan)
    except BaseException as error:
        if expected_error is None or not isinstance(error, expected_error):
            raise
        caught = error

    if expected_error is not None and caught is None:
        raise AssertionError(f"expected {expected_error.__name__}")

    after = _database_snapshot(database_connection)
    steps = _step_values(plan, trace, after)
    _assert_forward_transaction_outcomes(steps, trace)
    effects = _effects(trace, before, after)

    if expected_error is InconsistentMigrationHistory:
        if plan_invoked or migrate_invoked or steps:
            raise AssertionError("invalid history reached planning or execution")
        if any(effects.values()):
            raise AssertionError("history preflight produced a durable effect")

    metrics: dict[str, Any] = {
        "capture_boundary": "fresh_file_connection_loader_executor",
        "connection": _connection_facts(database_connection),
        "effects": effects,
        "history_preflight": {
            "history_check_invoked": history_check_invoked,
            "history_valid": history_valid,
            "migrate_invoked": migrate_invoked,
            "plan_invoked": plan_invoked,
        },
        "recorder_bootstrap": _recorder_bootstrap(before, after),
        "request": {
            "mode": target_mode,
            "targets": _key_values(targets),
        },
        "steps": steps,
        "unstarted_tail_count": sum(
            step["outcome"] == "not_started" for step in steps
        ),
    }
    if extra_metrics is not None:
        metrics.update(extra_metrics)

    if caught is None:
        if state is None:
            raise AssertionError("successful lifecycle returned no state")
        result: dict[str, Any] | None = {
            "plan": _plan_value(plan),
            "returned_state": _state_value(state),
        }
        error_value = None
    else:
        if error_category is None or error_code is None:
            raise AssertionError("error observation requires category and code")
        result = None
        error_value = _error_value(
            caught,
            category=error_category,
            code=error_code,
        )

    return {
        "id": contract_id,
        "status": "observed",
        "phase": phase,
        "result": normalize(result) if result is not None else None,
        "error": error_value,
        "db_state": normalize({"after": after, "before": before}),
        "metrics": normalize(metrics),
    }


def _failed_middle_setup(database_connection: Any) -> dict[str, Any]:
    before = _database_snapshot(database_connection)
    trace = _ExecutionTrace()
    migrations, control = _fixture_migrations()
    executor = _fixture_executor(database_connection, migrations, trace)
    executor.loader.check_consistent_history(database_connection)
    targets = list(executor.loader.graph.leaf_nodes())
    plan = executor.migration_plan(targets)
    control.enabled = True
    try:
        with _capture(database_connection, trace):
            executor.migrate(targets, plan=plan)
    except ConformanceLifecycleOperationFailure:
        pass
    else:
        raise AssertionError("middle lifecycle setup did not fail")

    after = _database_snapshot(database_connection)
    steps = _step_values(plan, trace, after)
    _assert_forward_transaction_outcomes(steps, trace)
    wanted_outcomes = [
        "committed",
        "rolled_back",
        "not_started",
        "not_started",
    ]
    if [step["outcome"] for step in steps] != wanted_outcomes:
        raise AssertionError("failed setup did not preserve the expected prefix")
    if after["migration_records"] != [_key_value(_A1)]:
        raise AssertionError("failed setup durable recorder prefix differs")
    alpha_columns = [
        column["name"]
        for table in after["managed_schema"]
        if table["name"] == "godj_lifecycle_alpha"
        for column in table["columns"]
    ]
    if alpha_columns != ["a1_marker", "id"]:
        raise AssertionError("failed A2 schema mutation was not rolled back")
    return {
        "durable_prefix": [_key_value(_A1)],
        "error_code": "operation_failed",
        "plan": _plan_value(plan),
        "recorder_bootstrap": _recorder_bootstrap(before, after),
        "steps": steps,
    }


def fresh_latest(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="commit",
            target_mode="latest",
            target_selector=_latest_targets,
            request_targets=_LATEST_TARGETS,
        )


def applied_prefix_latest(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        _seed(session.connection, _PREFIX_SEED_TARGETS)
        if not session.reopen():
            raise AssertionError("prefix lifecycle reused its setup connection")
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="commit",
            target_mode="latest",
            target_selector=_latest_targets,
            request_targets=_LATEST_TARGETS,
        )


def fully_applied_latest_noop(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        _seed(session.connection, _FULL_SEED_TARGETS)
        if not session.reopen():
            raise AssertionError("no-op lifecycle reused its setup connection")
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="commit",
            target_mode="latest",
            target_selector=_latest_targets,
            request_targets=_LATEST_TARGETS,
        )


def named_forward_target(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="commit",
            target_mode="named",
            target_selector=_fixed_targets(_NAMED_FORWARD_TARGETS),
            request_targets=_NAMED_FORWARD_TARGETS,
        )


def named_reverse_target(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        _seed(session.connection, _FULL_SEED_TARGETS)
        if not session.reopen():
            raise AssertionError("reverse lifecycle reused its setup connection")
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="commit",
            target_mode="named",
            target_selector=_fixed_targets(_NAMED_REVERSE_TARGETS),
            request_targets=_NAMED_REVERSE_TARGETS,
        )


def zero_target(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        _seed(session.connection, _FULL_SEED_TARGETS)
        if not session.reopen():
            raise AssertionError("zero lifecycle reused its setup connection")
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="commit",
            target_mode="app_zero",
            target_selector=_fixed_targets(_ZERO_TARGETS),
            request_targets=_ZERO_TARGETS,
        )


def unknown_legacy_tail(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        _seed(session.connection, _PREFIX_SEED_TARGETS)
        MigrationRecorder(session.connection).record_applied(*_LEGACY)
        if not session.reopen():
            raise AssertionError("legacy lifecycle reused its setup connection")
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="commit",
            target_mode="latest",
            target_selector=_latest_targets,
            request_targets=_LATEST_TARGETS,
        )


def inconsistent_history_preflight(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        setup_recorder = MigrationRecorder(session.connection)
        setup_recorder.ensure_schema()
        setup_recorder.record_applied(*_A2)
        if not session.reopen():
            raise AssertionError("history lifecycle reused its setup connection")
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="evaluation",
            target_mode="latest",
            target_selector=_latest_targets,
            request_targets=_LATEST_TARGETS,
            expected_error=InconsistentMigrationHistory,
            error_category="migration_history_error",
            error_code="inconsistent_applied_history",
        )


def middle_forward_failure(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="rollback",
            target_mode="latest",
            target_selector=_latest_targets,
            request_targets=_LATEST_TARGETS,
            fail_middle=True,
            expected_error=ConformanceLifecycleOperationFailure,
            error_category="migration_execution_error",
            error_code="operation_failed",
            extra_metrics={"failure_step": _key_value(_A2)},
        )


def restart_after_failure(contract_id: str) -> dict[str, Any]:
    with _isolated_database() as session:
        setup = _failed_middle_setup(session.connection)
        connection_reopened = session.reopen()
        if not connection_reopened:
            raise AssertionError("restart lifecycle reused its failed connection")
        return _run_lifecycle(
            contract_id,
            session.connection,
            phase="commit",
            target_mode="latest",
            target_selector=_latest_targets,
            request_targets=_LATEST_TARGETS,
            extra_metrics={
                "restart": {
                    "connection_reopened": connection_reopened,
                    "database_kind": "temporary_file",
                    "setup": setup,
                }
            },
        )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.migration.lifecycle.fresh_latest": fresh_latest,
    "django.migration.lifecycle.applied_prefix_latest": applied_prefix_latest,
    "django.migration.lifecycle.fully_applied_latest_noop": (
        fully_applied_latest_noop
    ),
    "django.migration.lifecycle.named_forward_target": named_forward_target,
    "django.migration.lifecycle.named_reverse_target": named_reverse_target,
    "django.migration.lifecycle.zero_target": zero_target,
    "django.migration.lifecycle.unknown_legacy_tail": unknown_legacy_tail,
    "django.migration.lifecycle.inconsistent_history_preflight": (
        inconsistent_history_preflight
    ),
    "django.migration.lifecycle.middle_forward_failure": middle_forward_failure,
    "django.migration.lifecycle.restart_after_failure": restart_after_failure,
}
