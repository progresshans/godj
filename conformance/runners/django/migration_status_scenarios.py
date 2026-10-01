"""Pinned Django 6.1 observations for read-only migration status listing.

The fixture graph and recorder snapshot are in-memory inputs, but the output is
produced by Django's real ``Command.show_list`` implementation.  This keeps the
reference boundary on Django's observable app grouping, root-to-leaf ordering,
and ``[X]``/``[ ]`` markers without making filesystem discovery, a database, or
GoDj's additional unknown-history policy part of the Django authority.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from io import StringIO
from typing import Any
from unittest.mock import patch

from django.core.management.commands import showmigrations
from django.db.migrations.graph import MigrationGraph
from django.db.migrations.migration import Migration

from .normalizer import normalize


NodeKey = tuple[str, str]
Dependency = tuple[NodeKey, NodeKey]


@dataclass(frozen=True)
class _ShowListFixture:
    nodes: tuple[NodeKey, ...]
    dependencies: tuple[Dependency, ...]
    applied: frozenset[NodeKey]
    recorded: frozenset[NodeKey]


def _graph(fixture: _ShowListFixture) -> MigrationGraph:
    graph = MigrationGraph()
    dependencies_by_child: dict[NodeKey, list[NodeKey]] = {
        node: [] for node in fixture.nodes
    }
    for child, parent in fixture.dependencies:
        dependencies_by_child[child].append(parent)
    for app_label, name in fixture.nodes:
        key = (app_label, name)
        migration = Migration(name, app_label)
        migration.dependencies = list(dependencies_by_child[key])
        graph.add_node(key, migration)
    for child, parent in fixture.dependencies:
        graph.add_dependency(graph.nodes[child], child, parent)
    graph.validate_consistency()
    return graph


def _execute_show_list(
    fixture: _ShowListFixture,
) -> tuple[str, dict[str, int]]:
    """Execute one fresh Command/loader/recorder observation."""

    counters = {
        "command_invocations": 0,
        "loader_instances": 0,
        "recorder_instances": 0,
    }

    class FixtureLoader:
        def __init__(self, connection: object, *, ignore_no_migrations: bool) -> None:
            if connection is not fixture_connection:
                raise AssertionError("show_list replaced the supplied connection")
            if not ignore_no_migrations:
                raise AssertionError("show_list changed its loader policy")
            counters["loader_instances"] += 1
            self.graph = _graph(fixture)
            self.migrated_apps = {app for app, _ in fixture.nodes}
            self.applied_migrations = {
                key: self.graph.nodes[key] for key in fixture.applied
            }

    class FixtureRecorder:
        def __init__(self, connection: object) -> None:
            if connection is not fixture_connection:
                raise AssertionError("show_list replaced the supplied connection")
            counters["recorder_instances"] += 1

        def applied_migrations(self) -> dict[NodeKey, object]:
            return {key: object() for key in fixture.recorded}

    fixture_connection = object()
    stdout = StringIO()
    command = showmigrations.Command(stdout=stdout, no_color=True)
    command.verbosity = 0
    with (
        patch.object(showmigrations, "MigrationLoader", FixtureLoader),
        patch.object(showmigrations, "MigrationRecorder", FixtureRecorder),
    ):
        counters["command_invocations"] += 1
        command.show_list(fixture_connection)
    return stdout.getvalue(), counters


def _rows(stdout: str) -> tuple[list[str], list[dict[str, str]]]:
    app_order: list[str] = []
    rows: list[dict[str, str]] = []
    current_app: str | None = None
    markers = {
        " [X] ": "applied",
        " [ ] ": "unapplied",
        " [-] ": "recording_incomplete",
    }
    for line in stdout.splitlines():
        marker = next((value for prefix, value in markers.items() if line.startswith(prefix)), None)
        if marker is None:
            if line.startswith(" "):
                raise AssertionError(f"unexpected Django show_list line: {line!r}")
            current_app = line
            app_order.append(line)
            continue
        if current_app is None:
            raise AssertionError("Django emitted a migration before its app label")
        prefix = next(prefix for prefix in markers if line.startswith(prefix))
        rows.append(
            {
                "app": current_app,
                "name": line[len(prefix) :],
                "status": marker,
            }
        )
    return app_order, rows


def _observation(
    contract_id: str,
    result: dict[str, Any],
) -> dict[str, Any]:
    return {
        "db_state": None,
        "error": None,
        "id": contract_id,
        "metrics": None,
        "phase": "evaluation",
        "result": normalize(result),
        "status": "observed",
    }


def _article_fixture(applied_count: int) -> _ShowListFixture:
    nodes = (
        ("blog", "0002_publish"),
        ("authors", "0001_author"),
        ("blog", "0001_article"),
    )
    ordered = (
        ("authors", "0001_author"),
        ("blog", "0001_article"),
        ("blog", "0002_publish"),
    )
    selected = frozenset(ordered[:applied_count])
    return _ShowListFixture(
        nodes=nodes,
        dependencies=(
            (ordered[1], ordered[0]),
            (ordered[2], ordered[1]),
        ),
        applied=selected,
        recorded=selected,
    )


def _single_observation(
    contract_id: str,
    fixture: _ShowListFixture,
) -> dict[str, Any]:
    stdout, counters = _execute_show_list(fixture)
    if counters != {
        "command_invocations": 1,
        "loader_instances": 1,
        "recorder_instances": 1,
    }:
        raise AssertionError(f"unexpected fresh show_list ownership: {counters!r}")
    app_order, rows = _rows(stdout)
    return _observation(
        contract_id,
        {
            "app_order": app_order,
            "rows": rows,
            "stdout": stdout,
        },
    )


def fresh_unapplied(contract_id: str) -> dict[str, Any]:
    return _single_observation(contract_id, _article_fixture(0))


def applied_prefix(contract_id: str) -> dict[str, Any]:
    return _single_observation(contract_id, _article_fixture(2))


def fully_applied_restart(contract_id: str) -> dict[str, Any]:
    """Observe Django twice without claiming a fresh-process product proof."""

    fixture = _article_fixture(3)
    first_stdout, first_metrics = _execute_show_list(fixture)
    second_stdout, second_metrics = _execute_show_list(fixture)
    want_metrics = {
        "command_invocations": 1,
        "loader_instances": 1,
        "recorder_instances": 1,
    }
    if first_metrics != want_metrics or second_metrics != want_metrics:
        raise AssertionError(
            "independent show_list observations did not use fresh command ownership"
        )
    first_apps, first_rows = _rows(first_stdout)
    second_apps, second_rows = _rows(second_stdout)
    identical = first_stdout.encode("utf-8") == second_stdout.encode("utf-8")
    return _observation(
        contract_id,
        {
            "app_order": first_apps,
            "first_rows": first_rows,
            "first_stdout": first_stdout,
            "independent_observations_byte_identical": identical,
            "second_app_order": second_apps,
            "second_rows": second_rows,
            "second_stdout": second_stdout,
        },
    )


def cross_app_branch_order(contract_id: str) -> dict[str, Any]:
    zeta_base = ("zeta", "0001_root")
    alpha_parent = ("alpha", "0099_parent")
    alpha_child = ("alpha", "0001_child")
    fixture = _ShowListFixture(
        # Deliberately put zeta first. Command.show_list owns label sorting.
        # Deliberately name the child before its parent lexicographically.
        nodes=(zeta_base, alpha_child, alpha_parent),
        dependencies=(
            (alpha_parent, zeta_base),
            (alpha_child, alpha_parent),
        ),
        applied=frozenset(),
        recorded=frozenset(),
    )
    stdout, counters = _execute_show_list(fixture)
    if counters != {
        "command_invocations": 1,
        "loader_instances": 1,
        "recorder_instances": 1,
    }:
        raise AssertionError(f"unexpected cross-app show_list ownership: {counters!r}")
    app_order, rows = _rows(stdout)
    positions = {
        (row["app"], row["name"]): index for index, row in enumerate(rows)
    }
    same_app_dependencies = [
        {"child": child[1], "parent": parent[1]}
        for child, parent in fixture.dependencies
        if child[0] == parent[0]
    ]
    same_app_dependency_valid = all(
        positions[parent] < positions[child]
        for child, parent in fixture.dependencies
        if child[0] == parent[0]
    )
    return _observation(
        contract_id,
        {
            "app_order": app_order,
            "dependency_order_precedes_lexicographic_name": (
                positions[alpha_parent] < positions[alpha_child]
                and alpha_child[1] < alpha_parent[1]
            ),
            "global_topological_order_claimed": False,
            "label_grouping_can_precede_cross_app_dependency": (
                positions[alpha_parent] < positions[zeta_base]
            ),
            "per_app_dependency_valid": same_app_dependency_valid,
            "rows": rows,
            "same_app_dependencies": same_app_dependencies,
            "stdout": stdout,
        },
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.migration.status.fresh_unapplied": fresh_unapplied,
    "django.migration.status.applied_prefix": applied_prefix,
    "django.migration.status.fully_applied_restart": fully_applied_restart,
    "django.migration.status.cross_app_branch_order": cross_app_branch_order,
}
