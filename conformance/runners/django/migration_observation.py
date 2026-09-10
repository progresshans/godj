"""Shared Django-only observation mechanics for migration reference adapters.

Scenario modules retain recorder access, transaction boundaries and fixture
selection. This module neither loads expected artifacts nor observes GoDj.
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from django.db import models
from django.db.migrations.state import ProjectState
from django.db.models.fields import NOT_PROVIDED


NodeKey = tuple[str, str]
Dependency = tuple[NodeKey, NodeKey]


def key_value(key: tuple[str, str | None]) -> dict[str, str | None]:
    return {"app": key[0], "name": key[1]}


def key_values(keys: Sequence[tuple[str, str | None]]) -> list[dict[str, Any]]:
    return [key_value(key) for key in keys]


def graph_facts(nodes: Sequence[NodeKey], dependencies: Sequence[Dependency]) -> dict[str, Any]:
    return {
        "dependencies": [
            {"child": key_value(child), "parent": key_value(parent)}
            for child, parent in sorted(dependencies)
        ],
        "nodes": key_values(sorted(nodes)),
    }


_FIELD_KINDS = {
    "AutoField": "auto",
    "BooleanField": "boolean",
    "CharField": "char",
}


def type_family(type_code: Any, *, datetime_types: bool) -> str:
    rendered = str(type_code).lower()
    if "int" in rendered:
        return "integer"
    if "char" in rendered or "clob" in rendered or "text" in rendered:
        return "text"
    if "bool" in rendered:
        return "boolean"
    if datetime_types and ("date" in rendered or "time" in rendered):
        return "datetime"
    return rendered


def managed_schema(
    database_connection: Any,
    table_prefix: str,
    *,
    primary_keys: bool,
    datetime_types: bool,
) -> list[dict[str, Any]]:
    inventory = []
    with database_connection.cursor() as cursor:
        for table in sorted(database_connection.introspection.table_names(cursor)):
            if not table.startswith(table_prefix):
                continue
            description = database_connection.introspection.get_table_description(
                cursor, table
            )
            primary_key_columns = set()
            if primary_keys:
                constraints = database_connection.introspection.get_constraints(cursor, table)
                primary_key_columns = {
                    column
                    for constraint in constraints.values()
                    if constraint["primary_key"]
                    for column in constraint["columns"]
                }
            columns = []
            for column in sorted(description, key=lambda item: item.name):
                value = {
                    "name": column.name,
                    "nullable": column.null_ok,
                    "type_family": type_family(column.type_code, datetime_types=datetime_types),
                }
                if primary_keys:
                    value["primary_key"] = column.name in primary_key_columns
                columns.append(value)
            inventory.append({"columns": columns, "name": table})
    return inventory


def default_value(field: models.Field[Any, Any], field_kind: str) -> dict[str, Any]:
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
        raise AssertionError(f"unsupported default type in migration state: {type(default).__name__}")
    return {"present": True, "type": default_type, "value": default}


def state_value(state: ProjectState) -> dict[str, Any]:
    apps: dict[str, list[dict[str, Any]]] = {}
    for (app_label, model_key), model_state in sorted(state.models.items()):
        if model_state.name_lower != model_key:
            raise AssertionError("ProjectState model key/name_lower mismatch")
        db_table = model_state.options.get("db_table")
        if not isinstance(db_table, str) or not db_table:
            raise AssertionError("migration state models must declare an explicit db_table")
        fields = []
        for field_name, field in model_state.fields.items():
            internal_type = field.get_internal_type()
            try:
                field_kind = _FIELD_KINDS[internal_type]
            except KeyError as error:
                raise AssertionError(f"unsupported field kind in migration state: {internal_type}") from error
            max_length = field.max_length
            if field_kind == "char":
                if isinstance(max_length, bool) or not isinstance(max_length, int) or max_length <= 0:
                    raise AssertionError("char field max_length must be a positive int")
            elif max_length is not None:
                raise AssertionError("non-char field max_length must be None")
            fields.append({
                "column": field.db_column or field_name,
                "default": default_value(field, field_kind),
                "kind": field_kind,
                "max_length": max_length,
                "name": field_name,
                "nullable": field.null,
                "primary_key": field.primary_key,
            })
        apps.setdefault(app_label, []).append({"db_table": db_table, "fields": fields, "name": model_key})
    return {
        "apps": [{"label": app_label, "models": sorted(app_models, key=lambda item: item["name"])}
                 for app_label, app_models in sorted(apps.items())],
        "format_version": 1,
    }
