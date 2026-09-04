"""Django-owned observations for bounded migration autodetection and commands."""

from __future__ import annotations

import json
import os
import subprocess
import sys
from collections.abc import Callable
from typing import Any

from django.db import models
from django.db.migrations.autodetector import MigrationAutodetector
from django.db.migrations.graph import MigrationGraph
from django.db.migrations.migration import Migration
from django.db.migrations.operations.fields import AddField
from django.db.migrations.operations.models import CreateModel
from django.db.migrations.questioner import MigrationQuestioner
from django.db.migrations.state import ModelState, ProjectState

from .normalizer import normalize
from .scenarios import configure_django


configure_django()


Node = tuple[str, str]


def _observed(
    contract_id: str,
    result: Any,
    *,
    phase: str,
    metrics: Any,
) -> dict[str, Any]:
    return {
        "db_state": None,
        "error": None,
        "id": contract_id,
        "metrics": normalize(metrics),
        "phase": phase,
        "result": normalize(result),
        "status": "observed",
    }


def _project_state(*model_states: ModelState) -> ProjectState:
    state = ProjectState()
    for model_state in model_states:
        state.add_model(model_state)
    return state


def _article_state(
    *,
    summary: bool = False,
    author: str | None = None,
    category: str | None = None,
) -> ModelState:
    fields: list[tuple[str, models.Field[Any, Any]]] = [
        ("id", models.AutoField(primary_key=True)),
        ("title", models.CharField(max_length=200)),
        ("published", models.BooleanField(default=False)),
    ]
    if summary:
        fields.append(("summary", models.CharField(max_length=200, null=True)))
    if author is not None:
        fields.append(
            (
                "author",
                models.ForeignKey(author, on_delete=models.CASCADE),
            )
        )
    if category is not None:
        fields.append(
            (
                "category",
                models.ForeignKey(
                    category,
                    null=True,
                    on_delete=models.SET_NULL,
                ),
            )
        )
    return ModelState("blog", "Article", fields=fields)


def _author_state(app_label: str = "authors") -> ModelState:
    return ModelState(
        app_label,
        "Author",
        fields=[
            ("id", models.AutoField(primary_key=True)),
            ("name", models.CharField(max_length=64)),
        ],
    )


def _category_state() -> ModelState:
    return ModelState(
        "blog",
        "Category",
        fields=[
            ("id", models.AutoField(primary_key=True)),
            ("name", models.CharField(max_length=64)),
        ],
    )


def _graph(*nodes: Node) -> MigrationGraph:
    graph = MigrationGraph()
    for app_label, name in nodes:
        graph.add_node((app_label, name), Migration(name, app_label))
    return graph


def _detect(
    old: ProjectState,
    new: ProjectState,
    *,
    graph: MigrationGraph | None = None,
) -> dict[str, list[Migration]]:
    apps = {app_label for app_label, _ in new.models}
    questioner = MigrationQuestioner(
        defaults={"ask_initial": True}, specified_apps=apps
    )
    return MigrationAutodetector(old, new, questioner).changes(
        graph=graph or MigrationGraph()
    )


def _field_value(name: str, field: models.Field[Any, Any]) -> dict[str, Any]:
    value: dict[str, Any] = {
        "name": name,
        "nullable": field.null,
    }
    if isinstance(field, models.AutoField):
        value.update({"kind": "auto", "primary_key": field.primary_key})
    elif isinstance(field, models.ForeignKey):
        target = field.remote_field.model
        value.update(
            {
                "kind": "foreign_key",
                "on_delete": field.remote_field.on_delete.__name__,
                "target": target if isinstance(target, str) else target._meta.label,
            }
        )
    elif isinstance(field, models.CharField):
        value.update({"kind": "char", "max_length": field.max_length})
    elif isinstance(field, models.BooleanField):
        value.update({"default": bool(field.default), "kind": "boolean"})
    else:
        raise TypeError(f"unsupported migration-writer field: {type(field).__name__}")
    return value


def _operation_value(operation: Any) -> dict[str, Any]:
    if isinstance(operation, CreateModel):
        return {
            "fields": [_field_value(name, field) for name, field in operation.fields],
            "kind": "CreateModel",
            "model": operation.name,
        }
    if isinstance(operation, AddField):
        return {
            "field": _field_value(operation.name, operation.field),
            "kind": "AddField",
            "model": operation.model_name,
        }
    raise TypeError(
        f"unsupported migration-writer operation: {type(operation).__name__}"
    )


def _migration_values(changes: dict[str, list[Migration]]) -> list[dict[str, Any]]:
    values: list[dict[str, Any]] = []
    for app_label in sorted(changes):
        for migration in changes[app_label]:
            values.append(
                {
                    "app": app_label,
                    "dependencies": [
                        {"app": app, "name": name}
                        for app, name in migration.dependencies
                    ],
                    "initial": bool(migration.initial),
                    "name": migration.name,
                    "operations": [
                        _operation_value(operation)
                        for operation in migration.operations
                    ],
                }
            )
    return values


def _run_worker(action: str) -> dict[str, Any]:
    environment = os.environ.copy()
    environment.update({"LC_ALL": "C", "PYTHONHASHSEED": "0", "TZ": "UTC"})
    process = subprocess.run(
        [
            sys.executable,
            "-m",
            "conformance.runners.django.migration_writer_worker",
            action,
        ],
        check=False,
        capture_output=True,
        env=environment,
        text=True,
        timeout=60,
    )
    if process.returncode != 0:
        raise RuntimeError(
            "migration-writer worker failed: "
            + (process.stderr.strip() or f"exit {process.returncode}")
        )
    if process.stderr:
        raise RuntimeError("migration-writer worker emitted stderr")
    value = json.loads(process.stdout)
    if not isinstance(value, dict):
        raise RuntimeError("migration-writer worker result must be an object")
    return value


def no_changes_clean(contract_id: str) -> dict[str, Any]:
    changes = _detect(_project_state(_article_state()), _project_state(_article_state()))
    return _observed(
        contract_id,
        {"candidate_count": len(_migration_values(changes)), "clean": not changes},
        phase="construction",
        metrics={"database_opens": 0, "detector_calls": 1, "writes": 0},
    )


def fresh_initial(contract_id: str) -> dict[str, Any]:
    changes = _detect(ProjectState(), _project_state(_article_state()))
    migrations = _migration_values(changes)
    return _observed(
        contract_id,
        {"migrations": migrations},
        phase="construction",
        metrics={
            "candidate_count": len(migrations),
            "database_opens": 0,
            "operation_count": sum(len(item["operations"]) for item in migrations),
        },
    )


def repeat_after_initial_noop(contract_id: str) -> dict[str, Any]:
    changes = _detect(
        _project_state(_article_state()),
        _project_state(_article_state()),
        graph=_graph(("blog", "0001_initial")),
    )
    return _observed(
        contract_id,
        {
            "candidate_count": len(_migration_values(changes)),
            "prior_source_mutated": False,
            "repeat_is_noop": not changes,
        },
        phase="construction",
        metrics={"database_opens": 0, "detector_calls": 1, "writes": 0},
    )


def relation_dependency_topology(contract_id: str) -> dict[str, Any]:
    same_app = _migration_values(
        _detect(
            ProjectState(),
            _project_state(_author_state("blog"), _article_state(author="blog.Author")),
        )
    )
    cross_app = _migration_values(
        _detect(
            ProjectState(),
            _project_state(_author_state(), _article_state(author="authors.Author")),
        )
    )
    return _observed(
        contract_id,
        {"cases": [{"case": "same_app", "migrations": same_app}, {"case": "cross_app", "migrations": cross_app}]},
        phase="construction",
        metrics={
            "cases": 2,
            "database_opens": 0,
            "migrations": len(same_app) + len(cross_app),
        },
    )


def additive_model_and_field_tail(contract_id: str) -> dict[str, Any]:
    changes = _detect(
        _project_state(_article_state()),
        _project_state(
            _article_state(summary=True, category="blog.Category"),
            _category_state(),
        ),
        graph=_graph(("blog", "0001_initial")),
    )
    migrations = _migration_values(changes)
    return _observed(
        contract_id,
        {"migrations": migrations},
        phase="construction",
        metrics={
            "candidate_count": len(migrations),
            "database_opens": 0,
            "operation_count": sum(len(item["operations"]) for item in migrations),
        },
    )


def dry_run_no_mutation(contract_id: str) -> dict[str, Any]:
    result = _run_worker("dry_run")
    return _observed(
        contract_id,
        result,
        phase="environment",
        metrics={
            "database_schema_mutations": int(
                result["tables_before"] != result["tables_after"]
            ),
            "filesystem_mutations": int(
                result["files_before"] != result["files_after"]
            ),
            "worker_processes": 1,
        },
    )


def check_clean_and_drift(contract_id: str) -> dict[str, Any]:
    clean = _run_worker("check_clean")
    drift = _run_worker("check_drift")
    return _observed(
        contract_id,
        {"cases": [clean, drift]},
        phase="environment",
        metrics={
            "database_schema_mutations": sum(
                int(case["tables_before"] != case["tables_after"])
                for case in (clean, drift)
            ),
            "filesystem_mutations": sum(
                int(case["files_before"] != case["files_after"])
                for case in (clean, drift)
            ),
            "worker_processes": 2,
        },
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.migration.writer.no_changes_clean": no_changes_clean,
    "django.migration.writer.fresh_initial": fresh_initial,
    "django.migration.writer.repeat_after_initial_noop": repeat_after_initial_noop,
    "django.migration.writer.relation_dependency_topology": relation_dependency_topology,
    "django.migration.writer.additive_model_and_field_tail": additive_model_and_field_tail,
    "django.migration.writer.dry_run_no_mutation": dry_run_no_mutation,
    "django.migration.writer.check_clean_and_drift": check_clean_and_drift,
}
