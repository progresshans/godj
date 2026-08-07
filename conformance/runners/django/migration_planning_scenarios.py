"""Django reference adapters for dependency and applied-state migration plans."""

from __future__ import annotations

from collections.abc import Callable, Iterator, Sequence
from contextlib import contextmanager
from typing import Any

from django.db import connection
from django.db.migrations.exceptions import (
    CircularDependencyError,
    InconsistentMigrationHistory,
    NodeNotFoundError,
)
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.loader import MigrationLoader
from django.db.migrations.migration import Migration
from django.db.migrations.recorder import MigrationRecorder

from .normalizer import normalize
from .scenarios import configure_django


configure_django()


NodeKey = tuple[str, str]
Target = tuple[str, str | None]
Dependency = tuple[NodeKey, NodeKey]

_DDL_KINDS = frozenset({"ALTER", "CREATE", "DROP", "TRUNCATE"})
_WRITE_KINDS = frozenset({"DELETE", "INSERT", "REPLACE", "UPDATE"})


def _key_value(key: tuple[str, str | None]) -> dict[str, str | None]:
    return {"app": key[0], "name": key[1]}


def _key_values(keys: Sequence[tuple[str, str | None]]) -> list[dict[str, Any]]:
    return [_key_value(key) for key in keys]


def _migration_key(migration: Migration) -> NodeKey:
    return (migration.app_label, migration.name)


def _ordered(values: Sequence[Any], variant: str) -> list[Any]:
    ordered = list(values)
    if variant == "normal":
        return ordered
    if variant == "reverse":
        return list(reversed(ordered))
    if variant == "rotate":
        return ordered[1:] + ordered[:1] if ordered else ordered
    raise ValueError(f"unknown insertion variant: {variant}")


def _statement_kind(sql: str) -> str:
    rendered = sql.lstrip().upper()
    return rendered.split(None, 1)[0] if rendered else "EMPTY"


def _capture(operation: Callable[[], Any]) -> tuple[Any, list[str]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_kind(sql))
        return execute(sql, params, many, context)

    with connection.execute_wrapper(wrapper):
        result = operation()
    return result, statements


def _capture_error(
    operation: Callable[[], Any],
    expected: type[BaseException],
) -> tuple[BaseException, list[str]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_kind(sql))
        return execute(sql, params, many, context)

    try:
        with connection.execute_wrapper(wrapper):
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
    if "date" in rendered or "time" in rendered:
        return "datetime"
    if "bool" in rendered:
        return "boolean"
    return rendered


def _schema_inventory(*, excluding: frozenset[str]) -> list[dict[str, Any]]:
    inventory: list[dict[str, Any]] = []
    with connection.cursor() as cursor:
        table_names = sorted(connection.introspection.table_names(cursor))
        for table in table_names:
            if table in excluding:
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
                        for column in description
                    ],
                    "name": table,
                }
            )
    return inventory


def _database_snapshot() -> dict[str, Any]:
    recorder = MigrationRecorder(connection)
    recorder_present = recorder.has_table()
    applied = sorted(recorder.applied_migrations()) if recorder_present else []
    return {
        "applied_migrations": _key_values(applied),
        "managed_schema_inventory": _schema_inventory(
            excluding=frozenset({recorder.Migration._meta.db_table})
        ),
        "recorder_present": recorder_present,
    }


@contextmanager
def _planning_database(applied: Sequence[NodeKey]) -> Iterator[None]:
    existing = connection.introspection.table_names()
    if existing:
        raise AssertionError(
            f"migration-planning scenario requires an empty database, got {existing!r}"
        )

    recorder = MigrationRecorder(connection)
    recorder.ensure_schema()
    for app, name in applied:
        recorder.record_applied(app, name)
    try:
        yield
    finally:
        cleanup_recorder = MigrationRecorder(connection)
        if cleanup_recorder.has_table():
            with connection.schema_editor() as editor:
                editor.delete_model(cleanup_recorder.Migration)
        remaining = connection.introspection.table_names()
        if remaining:
            raise AssertionError(
                f"migration-planning cleanup leaked tables: {remaining!r}"
            )


class _FixtureMigrationLoader(MigrationLoader):
    """Load independent in-memory Migration objects through Django's graph loader."""

    def __init__(
        self,
        database_connection: Any,
        entries: Sequence[tuple[NodeKey, Migration]],
    ) -> None:
        self._fixture_entries = tuple(entries)
        super().__init__(database_connection, load=False)

    def load_disk(self) -> None:
        self.disk_migrations = dict(self._fixture_entries)
        self.unmigrated_apps = set()
        self.migrated_apps = {key[0] for key, _migration in self._fixture_entries}


def _loader(
    nodes: Sequence[NodeKey],
    dependencies: Sequence[Dependency],
    *,
    insertion_variant: str = "normal",
    build: bool = True,
) -> _FixtureMigrationLoader:
    migrations = {key: Migration(key[1], key[0]) for key in nodes}
    for child, parent in _ordered(dependencies, insertion_variant):
        migrations[child].dependencies.append(parent)
    entries = [
        (key, migrations[key]) for key in _ordered(nodes, insertion_variant)
    ]
    loader = _FixtureMigrationLoader(connection, entries)
    if build:
        loader.build_graph()
    return loader


def _executor(loader: MigrationLoader) -> MigrationExecutor:
    executor = MigrationExecutor(connection)
    executor.loader = loader
    executor.recorder = MigrationRecorder(connection)
    return executor


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


def _plan_value(plan: Sequence[tuple[Migration, bool]]) -> list[dict[str, Any]]:
    return [
        {
            **_key_value(_migration_key(migration)),
            "direction": "backward" if backwards else "forward",
        }
        for migration, backwards in plan
    ]


def _mutation_metrics(
    statements: Sequence[str],
    before: dict[str, Any],
    after: dict[str, Any],
) -> dict[str, Any]:
    return {
        "ddl_statement_count": sum(kind in _DDL_KINDS for kind in statements),
        "non_select_statement_count": sum(kind != "SELECT" for kind in statements),
        "state_unchanged": before == after,
        "write_statement_count": sum(kind in _WRITE_KINDS for kind in statements),
    }


def _assert_zero_mutation(metrics: dict[str, Any]) -> None:
    if not metrics["state_unchanged"]:
        raise AssertionError("migration planning changed recorder or schema state")
    if metrics["ddl_statement_count"] != 0:
        raise AssertionError("migration planning executed DDL")
    if metrics["non_select_statement_count"] != 0:
        raise AssertionError("migration planning executed a non-SELECT statement")
    if metrics["write_statement_count"] != 0:
        raise AssertionError("migration planning wrote to the database")


def _plan_case(
    name: str,
    nodes: Sequence[NodeKey],
    dependencies: Sequence[Dependency],
    targets: Sequence[Target],
    applied: Sequence[NodeKey],
    *,
    insertion_variant: str = "normal",
) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    with _planning_database(applied):
        loader = _loader(
            nodes,
            dependencies,
            insertion_variant=insertion_variant,
        )
        executor = _executor(loader)
        before = _database_snapshot()
        plan, statements = _capture(lambda: executor.migration_plan(list(targets)))
        after = _database_snapshot()
        mutation = _mutation_metrics(statements, before, after)
        _assert_zero_mutation(mutation)
        return (
            {
                "applied": _key_values(sorted(applied)),
                "name": name,
                "plan": _plan_value(plan),
                "targets": _key_values(targets),
            },
            {"after": after, "before": before, "name": name},
            {"name": name, **mutation},
        )


def _success_observation(
    contract_id: str,
    cases: Sequence[tuple[dict[str, Any], dict[str, Any], dict[str, Any]]],
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": "evaluation",
        "result": normalize({"cases": [case[0] for case in cases]}),
        "error": None,
        "db_state": normalize({"cases": [case[1] for case in cases]}),
        "metrics": normalize({"cases": [case[2] for case in cases]}),
    }


def _error_observation(
    contract_id: str,
    *,
    phase: str,
    category: str,
    code: str,
    error: BaseException,
    before: dict[str, Any],
    after: dict[str, Any],
    statements: Sequence[str],
    facts: dict[str, Any],
) -> dict[str, Any]:
    mutation = _mutation_metrics(statements, before, after)
    _assert_zero_mutation(mutation)
    return {
        "id": contract_id,
        "status": "observed",
        "phase": phase,
        "result": None,
        "error": {
            "category": category,
            "code": code,
            "python_type": f"{type(error).__module__}.{type(error).__qualname__}",
            "message_is_contract": False,
        },
        "db_state": normalize({"after": after, "before": before}),
        "metrics": normalize({**facts, **mutation}),
    }


_A1 = ("alpha", "0001_initial")
_A2 = ("alpha", "0002_second")
_A3 = ("alpha", "0003_third")
_B1 = ("beta", "0001_initial")
_B2 = ("beta", "0002_second")
_G1 = ("gamma", "0001_initial")
_S1 = ("shared", "0001_initial")

_LINEAR_NODES = (_A1, _A2, _A3)
_LINEAR_DEPENDENCIES = ((_A2, _A1), (_A3, _A2))
_CROSS_NODES = (_A1, _A2, _B1, _B2)
_CROSS_DEPENDENCIES = ((_A2, _A1), (_B1, _A2), (_B2, _B1))


def linear_forward_plan(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        [
            _plan_case(
                "empty_history_to_linear_target",
                _LINEAR_NODES,
                _LINEAR_DEPENDENCIES,
                [_A3],
                [],
            )
        ],
    )


def applied_pruning_plans(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        [
            _plan_case(
                "partially_applied_prefix",
                _LINEAR_NODES,
                _LINEAR_DEPENDENCIES,
                [_A3],
                [_A1],
            ),
            _plan_case(
                "fully_applied_target",
                _LINEAR_NODES,
                _LINEAR_DEPENDENCIES,
                [_A3],
                [_A1, _A2, _A3],
            ),
        ],
    )


def missing_target(contract_id: str) -> dict[str, Any]:
    target = ("alpha", "9999_missing")
    nodes = (_A1,)
    dependencies: tuple[Dependency, ...] = ()
    with _planning_database([]):
        loader = _loader(nodes, dependencies)
        executor = _executor(loader)
        before = _database_snapshot()
        error, statements = _capture_error(
            lambda: executor.migration_plan([target]),
            NodeNotFoundError,
        )
        if not isinstance(error, NodeNotFoundError) or error.node != target:
            raise AssertionError("missing-target error did not identify the requested node")
        after = _database_snapshot()
        return _error_observation(
            contract_id,
            phase="evaluation",
            category="migration_plan_error",
            code="target_not_found",
            error=error,
            before=before,
            after=after,
            statements=statements,
            facts={
                "graph": _graph_facts(nodes, dependencies),
                "request": {"applied": [], "targets": _key_values([target])},
            },
        )


def prior_target_rollback(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        [
            _plan_case(
                "retain_prior_target",
                _LINEAR_NODES,
                _LINEAR_DEPENDENCIES,
                [_A1],
                [_A1, _A2, _A3],
            )
        ],
    )


def zero_with_dependents(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        [
            _plan_case(
                "zero_includes_cross_app_dependents",
                _CROSS_NODES,
                _CROSS_DEPENDENCIES,
                [("alpha", None)],
                [_A1, _A2, _B1, _B2],
            )
        ],
    )


def cross_app_forward(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        [
            _plan_case(
                "dependency_before_cross_app_target",
                _CROSS_NODES,
                _CROSS_DEPENDENCIES,
                [_B2],
                [],
            )
        ],
    )


def cross_app_backward(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        [
            _plan_case(
                "dependent_before_cross_app_dependency",
                _CROSS_NODES,
                _CROSS_DEPENDENCIES,
                [_A1],
                [_A1, _A2, _B1, _B2],
            )
        ],
    )


def ordered_targets_shared_dependency(contract_id: str) -> dict[str, Any]:
    nodes = (_S1, _A1, _B1)
    dependencies = ((_A1, _S1), (_B1, _S1))
    return _success_observation(
        contract_id,
        [
            _plan_case(
                "ordered_targets_share_one_dependency",
                nodes,
                dependencies,
                [_A1, _B1],
                [],
            )
        ],
    )


def retained_other_branches(contract_id: str) -> dict[str, Any]:
    nodes = (_A1, _A2, _A3, _B1, _G1)
    dependencies = ((_A2, _A1), (_A3, _A2), (_B1, _A1))
    return _success_observation(
        contract_id,
        [
            _plan_case(
                "same_app_descendants_with_retained_branches",
                nodes,
                dependencies,
                [_A1],
                [_A1, _A2, _A3, _B1, _G1],
            )
        ],
    )


def inconsistent_history(contract_id: str) -> dict[str, Any]:
    nodes = (_A1, _A2)
    dependencies = ((_A2, _A1),)
    applied = [_A2]
    with _planning_database(applied):
        loader = _loader(nodes, dependencies)
        before = _database_snapshot()
        error, statements = _capture_error(
            lambda: loader.check_consistent_history(connection),
            InconsistentMigrationHistory,
        )
        after = _database_snapshot()
        return _error_observation(
            contract_id,
            phase="evaluation",
            category="migration_history_error",
            code="inconsistent_applied_history",
            error=error,
            before=before,
            after=after,
            statements=statements,
            facts={
                "graph": _graph_facts(nodes, dependencies),
                "request": {
                    "applied": _key_values(sorted(applied)),
                    "operation": "validate_history_before_planning",
                },
            },
        )


def missing_dependency(contract_id: str) -> dict[str, Any]:
    missing = ("alpha", "0001_missing")
    nodes = (_A2,)
    dependencies = ((_A2, missing),)
    with _planning_database([]):
        loader = _loader(nodes, dependencies, build=False)
        before = _database_snapshot()
        error, statements = _capture_error(loader.build_graph, NodeNotFoundError)
        if not isinstance(error, NodeNotFoundError) or error.node != missing:
            raise AssertionError("missing-dependency error did not identify the parent")
        if not isinstance(error.origin, Migration) or _migration_key(error.origin) != _A2:
            raise AssertionError("missing-dependency error did not identify its origin")
        after = _database_snapshot()
        return _error_observation(
            contract_id,
            phase="construction",
            category="migration_graph_error",
            code="dependency_not_found",
            error=error,
            before=before,
            after=after,
            statements=statements,
            facts={
                "graph": _graph_facts(nodes, dependencies),
                "request": {"operation": "build_graph"},
            },
        )


def dependency_cycle(contract_id: str) -> dict[str, Any]:
    nodes = (_A1, _B1)
    dependencies = ((_A1, _B1), (_B1, _A1))
    with _planning_database([]):
        loader = _loader(nodes, dependencies, build=False)
        before = _database_snapshot()
        error, statements = _capture_error(
            loader.build_graph,
            CircularDependencyError,
        )
        after = _database_snapshot()
        return _error_observation(
            contract_id,
            phase="construction",
            category="migration_graph_error",
            code="dependency_cycle",
            error=error,
            before=before,
            after=after,
            statements=statements,
            facts={
                "graph": _graph_facts(nodes, dependencies),
                "request": {"operation": "build_graph"},
            },
        )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.migration.plan.linear_forward": linear_forward_plan,
    "django.migration.plan.applied_pruning": applied_pruning_plans,
    "django.migration.plan.missing_target": missing_target,
    "django.migration.plan.prior_target": prior_target_rollback,
    "django.migration.plan.zero_with_dependents": zero_with_dependents,
    "django.migration.plan.cross_app_forward": cross_app_forward,
    "django.migration.plan.cross_app_backward": cross_app_backward,
    "django.migration.plan.ordered_targets_shared_dependency": (
        ordered_targets_shared_dependency
    ),
    "django.migration.plan.retained_other_branches": retained_other_branches,
    "django.migration.plan.inconsistent_history": inconsistent_history,
    "django.migration.plan.missing_dependency": missing_dependency,
    "django.migration.plan.dependency_cycle": dependency_cycle,
}
