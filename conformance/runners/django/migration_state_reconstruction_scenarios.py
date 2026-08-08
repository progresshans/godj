"""Django reference adapters for historical ProjectState reconstruction.

The logical result is replayed from fresh in-memory migration definitions.
Every scenario keeps a deliberately divergent live SQLite schema in place so
database introspection cannot accidentally satisfy the compatibility contract.
"""

from __future__ import annotations

from collections.abc import Callable, Iterator, Mapping, Sequence
from contextlib import ExitStack, contextmanager
from typing import Any

from django.db import connection, models
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.loader import MigrationLoader
from django.db.migrations.migration import Migration
from django.db.migrations.operations.fields import AddField
from django.db.migrations.operations.models import CreateModel
from django.db.migrations.recorder import MigrationRecorder
from django.db.migrations.state import ProjectState
from django.db.models.fields import NOT_PROVIDED

from .normalizer import normalize
from .scenarios import configure_django


configure_django()


NodeKey = tuple[str, str]
Dependency = tuple[NodeKey, NodeKey]

_ALPHA_ROOT = ("alpha", "0002_root")
_ALPHA_MIDDLE = ("alpha", "0001_middle")
_ALPHA_LEAF = ("alpha", "0003_leaf")
_BETA_ROOT = ("beta", "0001_root")
_GAMMA_ROOT = ("gamma", "0001_root")
_DELTA_ROOT = ("delta", "0001_root")
_LEGACY = ("legacy", "0099_retired")

_DIVERGENT_TABLE = "godj_state_live_decoy"
_MANAGED_TABLE_PREFIX = "godj_state_"
_DDL_KINDS = frozenset({"ALTER", "CREATE", "DROP", "TRUNCATE"})
_WRITE_KINDS = frozenset({"DELETE", "INSERT", "REPLACE", "UPDATE"})
_FIELD_KINDS = {
    "AutoField": "auto",
    "BooleanField": "boolean",
    "CharField": "char",
}


def _key_value(key: NodeKey) -> dict[str, str]:
    return {"app": key[0], "name": key[1]}


def _key_values(keys: Sequence[NodeKey]) -> list[dict[str, str]]:
    return [_key_value(key) for key in keys]


def _fixture_migrations() -> dict[NodeKey, Migration]:
    alpha_root = Migration(_ALPHA_ROOT[1], _ALPHA_ROOT[0])
    alpha_root.operations = [
        CreateModel(
            name="Zulu",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                ("active", models.BooleanField(default=True)),
            ],
            options={"db_table": "godj_state_alpha_zulu"},
        ),
        CreateModel(
            name="Entry",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                (
                    "headline",
                    models.CharField(
                        db_column="headline_text",
                        default="",
                        max_length=64,
                    ),
                ),
            ],
            options={"db_table": "godj_state_alpha_entry"},
        )
    ]

    # The child key sorts before its parent key. A lexical replay therefore
    # fails, while dependency-order replay creates Entry before adding a field.
    alpha_middle = Migration(_ALPHA_MIDDLE[1], _ALPHA_MIDDLE[0])
    alpha_middle.dependencies = [_ALPHA_ROOT]
    alpha_middle.operations = [
        AddField(
            model_name="entry",
            name="published",
            field=models.BooleanField(default=False),
        )
    ]

    alpha_leaf = Migration(_ALPHA_LEAF[1], _ALPHA_LEAF[0])
    alpha_leaf.dependencies = [_ALPHA_MIDDLE]
    alpha_leaf.operations = [
        AddField(
            model_name="entry",
            name="summary",
            field=models.CharField(max_length=255, null=True),
        )
    ]

    beta_root = Migration(_BETA_ROOT[1], _BETA_ROOT[0])
    beta_root.dependencies = [_ALPHA_ROOT]
    beta_root.operations = [
        CreateModel(
            name="Audit",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                (
                    "code",
                    models.CharField(max_length=32, null=True),
                ),
            ],
            options={"db_table": "godj_state_beta_audit"},
        )
    ]

    gamma_root = Migration(_GAMMA_ROOT[1], _GAMMA_ROOT[0])
    gamma_root.dependencies = [_ALPHA_ROOT]
    gamma_root.operations = [
        CreateModel(
            name="Flag",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                ("enabled", models.BooleanField(default=True)),
            ],
            options={"db_table": "godj_state_gamma_flag"},
        )
    ]

    delta_root = Migration(_DELTA_ROOT[1], _DELTA_ROOT[0])
    delta_root.operations = [
        CreateModel(
            name="Archive",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                (
                    "label",
                    models.CharField(default="archive", max_length=48),
                ),
            ],
            options={"db_table": "godj_state_delta_archive"},
        )
    ]

    return {
        _ALPHA_ROOT: alpha_root,
        _ALPHA_MIDDLE: alpha_middle,
        _ALPHA_LEAF: alpha_leaf,
        _BETA_ROOT: beta_root,
        _GAMMA_ROOT: gamma_root,
        _DELTA_ROOT: delta_root,
    }


class _FixtureMigrationLoader(MigrationLoader):
    """Build a graph from fresh fixture values and optional durable rows."""

    def __init__(
        self,
        database_connection: Any | None,
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


def _graph_facts(migrations: Mapping[NodeKey, Migration]) -> dict[str, Any]:
    dependencies: list[Dependency] = []
    for child, migration in migrations.items():
        dependencies.extend((child, parent) for parent in migration.dependencies)
    return {
        "dependencies": [
            {"child": _key_value(child), "parent": _key_value(parent)}
            for child, parent in sorted(dependencies)
        ],
        "nodes": _key_values(sorted(migrations)),
    }


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
            f"unsupported default type in state fixture: {type(default).__name__}"
        )
    return {"present": True, "type": default_type, "value": default}


def _state_value(state: ProjectState) -> dict[str, Any]:
    apps: dict[str, list[dict[str, Any]]] = {}
    for (app_label, model_key), model_state in sorted(state.models.items()):
        if model_state.name_lower != model_key:
            raise AssertionError("ProjectState model key/name_lower mismatch")
        db_table = model_state.options.get("db_table")
        if not isinstance(db_table, str) or not db_table:
            raise AssertionError("state fixture models must declare an explicit db_table")
        fields = []
        for field_name, field in model_state.fields.items():
            max_length = field.max_length
            internal_type = field.get_internal_type()
            try:
                field_kind = _FIELD_KINDS[internal_type]
            except KeyError as error:
                raise AssertionError(
                    f"unsupported field kind in state fixture: {internal_type}"
                ) from error
            if field_kind == "char":
                if (
                    isinstance(max_length, bool)
                    or not isinstance(max_length, int)
                    or max_length <= 0
                ):
                    raise AssertionError(
                        "char field max_length must be a positive int"
                    )
            elif max_length is not None:
                raise AssertionError(
                    "non-char field max_length must be None"
                )
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
                "models": sorted(models_value, key=lambda item: item["name"]),
            }
            for app_label, models_value in sorted(apps.items())
        ],
        "format_version": 1,
    }


def _statement_kind(sql: str) -> str:
    rendered = sql.lstrip().upper()
    return rendered.split(None, 1)[0] if rendered else "EMPTY"


def _capture(
    operation: Callable[[], Any],
) -> tuple[Any, list[str]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(_statement_kind(sql))
        return execute(sql, params, many, context)

    with ExitStack() as stack:
        stack.enter_context(connection.execute_wrapper(wrapper))
        result = operation()
    return result, statements


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
            if not table.startswith(_MANAGED_TABLE_PREFIX):
                continue
            description = connection.introspection.get_table_description(
                cursor, table
            )
            inventory.append(
                {
                    "columns": [
                        {
                            "name": column.name,
                            "nullable": column.null_ok,
                            "type_family": _type_family(column.type_code),
                        }
                        for column in sorted(
                            description, key=lambda item: item.name
                        )
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
        "managed_schema": _managed_schema(),
        "recorder_present": recorder_present,
    }


def _drop_tables() -> None:
    for table in connection.introspection.table_names():
        with connection.cursor() as cursor:
            cursor.execute(f"DROP TABLE {connection.ops.quote_name(table)}")


@contextmanager
def _isolated_default_database() -> Iterator[None]:
    existing = connection.introspection.table_names()
    if existing:
        raise AssertionError(
            "state reconstruction scenario requires an empty database, "
            f"got {existing!r}"
        )
    try:
        yield
    finally:
        if connection.in_atomic_block:
            raise AssertionError("state reconstruction leaked an atomic block")
        _drop_tables()
        remaining = connection.introspection.table_names()
        if remaining:
            raise AssertionError(
                f"state reconstruction cleanup leaked tables: {remaining!r}"
            )


def _setup_divergent_schema() -> None:
    with connection.cursor() as cursor:
        cursor.execute(
            f"CREATE TABLE {connection.ops.quote_name(_DIVERGENT_TABLE)} "
            "(wrong integer NOT NULL)"
        )


def _assert_live_schema_is_divergent(
    state: Mapping[str, Any], database: Mapping[str, Any]
) -> None:
    logical_tables = sorted(
        model["db_table"]
        for app in state["apps"]
        for model in app["models"]
    )
    live_tables = sorted(table["name"] for table in database["managed_schema"])
    if logical_tables == live_tables:
        raise AssertionError("live schema unexpectedly matches historical state")


def _metrics(
    statements: Sequence[str],
    before: dict[str, Any],
    after: dict[str, Any],
    *,
    capture_boundary: str,
    graph: dict[str, Any],
    request: dict[str, Any],
) -> dict[str, Any]:
    metrics = {
        "capture_boundary": capture_boundary,
        "ddl_statement_count": sum(kind in _DDL_KINDS for kind in statements),
        "graph": graph,
        "non_select_statement_count": sum(
            kind != "SELECT" for kind in statements
        ),
        "replay_source": "loaded_migration_definitions",
        "request": request,
        "state_unchanged": before == after,
        "write_statement_count": sum(
            kind in _WRITE_KINDS for kind in statements
        ),
    }
    if not metrics["state_unchanged"]:
        raise AssertionError("state reconstruction changed database state")
    if metrics["ddl_statement_count"] != 0:
        raise AssertionError("state reconstruction executed DDL")
    if metrics["non_select_statement_count"] != 0:
        raise AssertionError("state reconstruction executed a non-SELECT statement")
    if metrics["write_statement_count"] != 0:
        raise AssertionError("state reconstruction wrote to the database")
    return metrics


def _success_observation(
    contract_id: str,
    result: dict[str, Any],
    before: dict[str, Any],
    after: dict[str, Any],
    statements: Sequence[str],
    *,
    capture_boundary: str,
    graph: dict[str, Any],
    request: dict[str, Any],
) -> dict[str, Any]:
    return {
        "id": contract_id,
        "status": "observed",
        "phase": "evaluation",
        "result": normalize(result),
        "error": None,
        "db_state": normalize({"after": after, "before": before}),
        "metrics": normalize(
            _metrics(
                statements,
                before,
                after,
                capture_boundary=capture_boundary,
                graph=graph,
                request=request,
            )
        ),
    }


def _project_state_observation(
    contract_id: str,
    *,
    nodes: Sequence[NodeKey] | None,
    at_end: bool,
    mode: str,
) -> dict[str, Any]:
    with _isolated_default_database():
        _setup_divergent_schema()
        before = _database_snapshot()

        def reconstruct() -> tuple[dict[str, Any], dict[str, Any]]:
            migrations = _fixture_migrations()
            loader = _FixtureMigrationLoader(None, list(migrations.items()))
            state = loader.project_state(nodes=nodes, at_end=at_end)
            return _state_value(state), _graph_facts(migrations)

        (state_value, graph), statements = _capture(reconstruct)
        after = _database_snapshot()
        _assert_live_schema_is_divergent(state_value, after)
        request = {
            "mode": mode,
            "position": "after" if at_end else "before",
            "targets": _key_values(list(nodes) if nodes is not None else []),
        }
        return _success_observation(
            contract_id,
            {"state": state_value},
            before,
            after,
            statements,
            capture_boundary="fresh_loader",
            graph=graph,
            request=request,
        )


def _fixture_executor(
    migrations: Mapping[NodeKey, Migration],
) -> MigrationExecutor:
    executor = MigrationExecutor(connection)
    executor.loader = _FixtureMigrationLoader(
        connection, list(migrations.items())
    )
    executor.recorder = MigrationRecorder(connection)
    return executor


def _applied_state_observation(
    contract_id: str,
    applied_keys: Sequence[NodeKey],
) -> dict[str, Any]:
    with _isolated_default_database():
        _setup_divergent_schema()
        writer = MigrationRecorder(connection)
        for app_label, migration_name in applied_keys:
            writer.record_applied(app_label, migration_name)
        before = _database_snapshot()

        def reconstruct() -> tuple[dict[str, Any], dict[str, Any]]:
            migrations = _fixture_migrations()
            executor = _fixture_executor(migrations)
            # The empty public migrate path reconstructs startup state from
            # recorder-backed applied history without executing a migration.
            state = executor.migrate(targets=[], plan=[])
            applied = sorted(executor.loader.applied_migrations)
            known_nodes = set(executor.loader.graph.nodes)
            result = {
                "applied_migrations": _key_values(applied),
                "known_applied_migrations": _key_values(
                    [key for key in applied if key in known_nodes]
                ),
                "state": _state_value(state),
                "unknown_applied_migrations": _key_values(
                    [key for key in applied if key not in known_nodes]
                ),
            }
            return result, _graph_facts(migrations)

        (result, graph), statements = _capture(reconstruct)
        after = _database_snapshot()
        _assert_live_schema_is_divergent(result["state"], after)
        request = {
            "mode": "applied_history",
            "position": "after",
            "targets": [],
        }
        return _success_observation(
            contract_id,
            result,
            before,
            after,
            statements,
            capture_boundary="fresh_executor",
            graph=graph,
            request=request,
        )


def explicit_empty(contract_id: str) -> dict[str, Any]:
    return _project_state_observation(
        contract_id, nodes=(), at_end=True, mode="explicit_nodes"
    )


def first_before(contract_id: str) -> dict[str, Any]:
    return _project_state_observation(
        contract_id,
        nodes=(_ALPHA_ROOT,),
        at_end=False,
        mode="explicit_nodes",
    )


def first_after(contract_id: str) -> dict[str, Any]:
    return _project_state_observation(
        contract_id,
        nodes=(_ALPHA_ROOT,),
        at_end=True,
        mode="explicit_nodes",
    )


def linear_middle_after(contract_id: str) -> dict[str, Any]:
    return _project_state_observation(
        contract_id,
        nodes=(_ALPHA_MIDDLE,),
        at_end=True,
        mode="explicit_nodes",
    )


def linear_middle_before(contract_id: str) -> dict[str, Any]:
    return _project_state_observation(
        contract_id,
        nodes=(_ALPHA_MIDDLE,),
        at_end=False,
        mode="explicit_nodes",
    )


def cross_app_dependency(contract_id: str) -> dict[str, Any]:
    return _project_state_observation(
        contract_id,
        nodes=(_BETA_ROOT,),
        at_end=True,
        mode="explicit_nodes",
    )


def multiple_targets_shared_dependency(contract_id: str) -> dict[str, Any]:
    return _project_state_observation(
        contract_id,
        nodes=(_BETA_ROOT, _GAMMA_ROOT),
        at_end=True,
        mode="explicit_nodes",
    )


def latest_leaves(contract_id: str) -> dict[str, Any]:
    return _project_state_observation(
        contract_id, nodes=None, at_end=True, mode="latest"
    )


def applied_prefix_startup(contract_id: str) -> dict[str, Any]:
    return _applied_state_observation(
        contract_id, (_ALPHA_ROOT, _ALPHA_MIDDLE)
    )


def unrelated_known_unknown_startup(contract_id: str) -> dict[str, Any]:
    return _applied_state_observation(
        contract_id, (_ALPHA_ROOT, _DELTA_ROOT, _LEGACY)
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.migration.state_reconstruction.explicit_empty": explicit_empty,
    "django.migration.state_reconstruction.first_before": first_before,
    "django.migration.state_reconstruction.first_after": first_after,
    "django.migration.state_reconstruction.linear_middle_after": (
        linear_middle_after
    ),
    "django.migration.state_reconstruction.linear_middle_before": (
        linear_middle_before
    ),
    "django.migration.state_reconstruction.cross_app_dependency": (
        cross_app_dependency
    ),
    "django.migration.state_reconstruction.multiple_targets_shared_dependency": (
        multiple_targets_shared_dependency
    ),
    "django.migration.state_reconstruction.latest_leaves": latest_leaves,
    "django.migration.state_reconstruction.applied_prefix_startup": (
        applied_prefix_startup
    ),
    "django.migration.state_reconstruction.unrelated_known_unknown_startup": (
        unrelated_known_unknown_startup
    ),
}
