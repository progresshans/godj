"""Reference observations for GoDj migration-definition source contracts.

The JSON envelope, compatibility tuple, canonical digest, and failure order in
this module are GoDj decisions from ADR-0019. Django is used only where the
contract intentionally preserves named migrations, ordered operations,
dependency graph construction, and the public executor lifecycle handoff.

Every source is supplied by the caller as immutable bytes. This module never
interprets a source ID as a path and never performs migration module discovery.
"""

from __future__ import annotations

import hashlib
import json
import re
import tempfile
from collections.abc import Iterator, Sequence
from contextlib import contextmanager
from copy import deepcopy
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from unittest.mock import patch

from django.db import connections, models
from django.db.migrations import executor as executor_module
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.loader import MigrationLoader
from django.db.migrations.migration import Migration
from django.db.migrations.operations.fields import AddField
from django.db.migrations.operations.models import CreateModel
from django.db.migrations.recorder import MigrationRecorder
from django.db.migrations.state import ProjectState

from .normalizer import normalize
from .scenarios import configure_django


configure_django()


NodeKey = tuple[str, str]

_COMPATIBILITY = {
    "definition_format": 1,
    "loader_abi": 1,
    "operation_codec": 1,
    "schema_ir": 2,
}
_COMPATIBILITY_ORDER = (
    "definition_format",
    "loader_abi",
    "operation_codec",
    "schema_ir",
)
_COMPATIBILITY_CODES = {
    "definition_format": "definition_format_incompatible",
    "loader_abi": "loader_abi_incompatible",
    "operation_codec": "operation_codec_incompatible",
    "schema_ir": "schema_ir_incompatible",
}
_DIGEST_DOMAIN = "godj:migration-definition-set:v1"
_EMPTY_DIGEST = (
    "sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450"
)
_SOURCE_ERROR_CATEGORY = "migration_definition_source_error"
_GRAPH_ERROR_CATEGORY = "migration_graph_error"
_DATABASE_ALIAS = "godj_definition_source_reference"
_TABLE_PREFIX = "godj_definition_"
_DATABASE_IDENTIFIER = re.compile(r"^[a-z_][a-z0-9_]*$")
_MAX_LENGTH_MAX = (1 << 31) - 1
_INT64_MIN = -(1 << 63)
_INT64_MAX = (1 << 63) - 1
_SOURCE_REASON_RANK = {
    "empty_source_id": 0,
    "invalid_source_id_utf8": 1,
    "duplicate_source_id": 2,
    "invalid_source": 3,
}
_FIELD_REASON_RANK = {"unknown_field": 0, "missing_field": 1}
_DOCUMENT_REASON_RANK = {
    "invalid_utf8": 0,
    "syntax": 1,
    "duplicate_key": 2,
    "lone_surrogate": 3,
    "unknown_field": 4,
    "missing_field": 5,
    "wrong_type": 6,
    "out_of_range": 7,
    "trailing_value": 8,
}
_SEMANTIC_REASON_RANK = {
    "unsupported_operation": 0,
    "invalid_operation": 1,
    "invalid_ir": 2,
    "wrong_type": 3,
    "out_of_range": 4,
}
_OPERATION_WIRE_FIELDS = frozenset(
    {"app_label", "field", "kind", "model", "model_name"}
)


@dataclass(frozen=True)
class SourceDocument:
    source_id: str | bytes
    document: bytes


@dataclass(frozen=True)
class _SourceSnapshot:
    source_id: str
    source_id_bytes: bytes
    document: bytes


@dataclass(frozen=True)
class _ParsedDocument:
    source: _SourceSnapshot
    value: dict[str, Any]


@dataclass(frozen=True)
class _DecodedDefinition:
    source_id: str
    value: dict[str, Any]


@dataclass(frozen=True)
class _LoadedDefinitionSet:
    definitions: tuple[dict[str, Any], ...]
    digest: str
    sources: tuple[dict[str, str], ...]


class _JSONObject(list[tuple[str, Any]]):
    """Keep object pairs until duplicate-key validation is complete."""


@dataclass(frozen=True)
class _JSONNonInteger:
    lexical: str


class _DefinitionSourceError(RuntimeError):
    def __init__(
        self,
        category: str,
        code: str,
        *,
        context: dict[str, Any] | None = None,
    ) -> None:
        super().__init__(code)
        self.category = category
        self.code = code
        self.context = dict(context or {})
        self.metrics: dict[str, Any] = {}


def _source_error(
    code: str,
    *,
    source_id: str | None = None,
    json_path: str | None = None,
    app: str | None = None,
    name: str | None = None,
    operation_index: int | None = None,
    stage: str | None = None,
    reason: str | None = None,
) -> _DefinitionSourceError:
    if stage is None:
        if code == "invalid_definition_source":
            stage = "source"
        elif code.endswith("_incompatible"):
            stage = "compatibility"
        elif code in {
            "unsupported_definition_operation",
            "invalid_definition_operation",
            "invalid_definition_ir",
        }:
            stage = "semantic"
        else:
            stage = "document"
    if reason is None:
        reason = {
            "definition_format_incompatible": "definition_format",
            "loader_abi_incompatible": "loader_abi",
            "operation_codec_incompatible": "operation_codec",
            "schema_ir_incompatible": "schema_ir",
            "unsupported_definition_operation": "unsupported_operation",
            "invalid_definition_operation": "invalid_operation",
            "invalid_definition_ir": "invalid_ir",
            "invalid_definition_source": "invalid_source",
        }.get(code, "invalid_document")
    context: dict[str, Any] = {
        "app": app or "",
        "json_pointer": _json_path_to_pointer(json_path or "$"),
        "name": name or "",
        "operation_index": operation_index if operation_index is not None else -1,
        "reason": reason,
        "source_id": source_id or "",
        "stage": stage,
    }
    return _DefinitionSourceError(
        _SOURCE_ERROR_CATEGORY,
        code,
        context=context,
    )


def _graph_error(
    code: str,
    *,
    source_id: str,
    app: str,
    name: str,
    json_path: str,
) -> _DefinitionSourceError:
    return _DefinitionSourceError(
        _GRAPH_ERROR_CATEGORY,
        code,
        context={
            "app": app,
            "json_pointer": _json_path_to_pointer(json_path),
            "name": name,
            "operation_index": -1,
            "reason": code,
            "source_id": source_id,
            "stage": "graph",
        },
    )


def _json_path_to_pointer(path: str) -> str:
    if path in {"", "$"}:
        return ""
    if path.startswith("/"):
        return path
    tokens = re.findall(r"\.([^\.\[]+)|\[(\d+)\]", path.removeprefix("$"))
    parts = [left or right for left, right in tokens]
    escaped = [part.replace("~", "~0").replace("/", "~1") for part in parts]
    return "/" + "/".join(escaped)


def _json_member_path(path: str, member: str) -> str:
    escaped = member.replace("~", "~0").replace("/", "~1")
    return _json_path_to_pointer(path) + "/" + escaped


def _json_index_path(path: str, index: int) -> str:
    return _json_path_to_pointer(path) + f"/{index}"


def _utf8_bytes(value: str, *, code: str, context: dict[str, Any]) -> bytes:
    try:
        return value.encode("utf-8")
    except UnicodeEncodeError as error:
        raise _source_error(
            code,
            source_id=context.get("source_id"),
            json_path=context.get("json_path"),
            reason=(
                "invalid_source_id_utf8"
                if code == "invalid_definition_source"
                else "lone_surrogate"
            ),
        ) from error


def _canonical_text_key(value: str) -> bytes:
    return value.encode("utf-8")


def _canonical_identity_key(value: NodeKey) -> tuple[bytes, bytes]:
    return (_canonical_text_key(value[0]), _canonical_text_key(value[1]))


def _base_metrics(documents_received: int) -> dict[str, Any]:
    return {
        "definition_sets_published": 0,
        "definitions_published": 0,
        "documents_received": documents_received,
        "handoff_calls": 0,
        "headers_validated": 0,
        "operations_decoded": 0,
        "source_reads_after_snapshot": 0,
        "failure": None,
    }


def _snapshot_sources(
    sources: Sequence[SourceDocument],
) -> tuple[_SourceSnapshot, ...]:
    snapshots: list[_SourceSnapshot] = []
    candidates: list[tuple[int, bytes, _DefinitionSourceError]] = []
    for source in sources:
        if not isinstance(source, SourceDocument):
            error = _source_error(
                "invalid_definition_source",
                reason="invalid_source",
            )
            candidates.append(
                (_SOURCE_REASON_RANK["invalid_source"], b"", error)
            )
            continue
        raw_id = source.source_id
        if isinstance(raw_id, str):
            try:
                source_id_bytes = raw_id.encode("utf-8")
            except UnicodeEncodeError:
                raw_bytes = raw_id.encode("utf-8", errors="surrogatepass")
                error = _source_error(
                    "invalid_definition_source",
                    source_id="hex:" + raw_bytes.hex(),
                    reason="invalid_source_id_utf8",
                )
                candidates.append(
                    (
                        _SOURCE_REASON_RANK["invalid_source_id_utf8"],
                        raw_bytes,
                        error,
                    )
                )
                continue
            source_id = raw_id
        elif isinstance(raw_id, bytes):
            source_id_bytes = bytes(raw_id)
            try:
                source_id = source_id_bytes.decode("utf-8")
            except UnicodeDecodeError:
                error = _source_error(
                    "invalid_definition_source",
                    source_id="hex:" + source_id_bytes.hex(),
                    reason="invalid_source_id_utf8",
                )
                candidates.append(
                    (
                        _SOURCE_REASON_RANK["invalid_source_id_utf8"],
                        source_id_bytes,
                        error,
                    )
                )
                continue
        else:
            error = _source_error(
                "invalid_definition_source",
                reason="invalid_source",
            )
            candidates.append(
                (_SOURCE_REASON_RANK["invalid_source"], b"", error)
            )
            continue
        if not source_id_bytes:
            error = _source_error(
                "invalid_definition_source",
                reason="empty_source_id",
            )
            candidates.append(
                (_SOURCE_REASON_RANK["empty_source_id"], b"", error)
            )
            continue
        if not isinstance(source.document, bytes):
            error = _source_error(
                "invalid_definition_source",
                source_id=source_id,
                reason="invalid_source",
            )
            candidates.append(
                (
                    _SOURCE_REASON_RANK["invalid_source"],
                    source_id_bytes,
                    error,
                )
            )
            continue
        snapshots.append(
            _SourceSnapshot(
                source_id=source_id,
                source_id_bytes=source_id_bytes,
                document=bytes(source.document),
            )
        )

    snapshots.sort(key=lambda item: item.source_id_bytes)
    for previous, current in zip(snapshots, snapshots[1:]):
        if previous.source_id_bytes == current.source_id_bytes:
            error = _source_error(
                "invalid_definition_source",
                source_id=current.source_id,
                reason="duplicate_source_id",
            )
            candidates.append(
                (
                    _SOURCE_REASON_RANK["duplicate_source_id"],
                    current.source_id_bytes,
                    error,
                )
            )
    if candidates:
        candidates.sort(key=lambda item: (item[0], item[1]))
        raise candidates[0][2]
    return tuple(snapshots)


def _reject_non_integer(_value: str) -> Any:
    raise ValueError("non-integer JSON number")


def _preserve_non_integer(value: str) -> _JSONNonInteger:
    return _JSONNonInteger(value)


def _plain_json(
    value: Any,
    path: str,
    source_id: str,
) -> tuple[Any, list[_DefinitionSourceError]]:
    errors: list[_DefinitionSourceError] = []

    def convert(current: Any, current_path: str) -> Any:
        if isinstance(current, _JSONObject):
            grouped: dict[str, list[Any]] = {}
            for key, child in current:
                if not isinstance(key, str):
                    errors.append(
                        _source_error(
                            "invalid_definition_document",
                            source_id=source_id,
                            json_path=current_path,
                            reason="wrong_type",
                        )
                    )
                    continue
                try:
                    key.encode("utf-8")
                except UnicodeEncodeError:
                    errors.append(
                        _source_error(
                            "invalid_definition_document",
                            source_id=source_id,
                            json_path=current_path,
                            reason="lone_surrogate",
                        )
                    )
                    continue
                grouped.setdefault(key, []).append(child)

            rendered: dict[str, Any] = {}
            for key in sorted(grouped, key=_canonical_text_key):
                member_path = _json_member_path(current_path, key)
                children = grouped[key]
                if len(children) > 1:
                    errors.append(
                        _source_error(
                            "invalid_definition_document",
                            source_id=source_id,
                            json_path=member_path,
                            reason="duplicate_key",
                        )
                    )
                converted = [convert(child, member_path) for child in children]
                rendered[key] = converted[0]
            return rendered
        if isinstance(current, list):
            return [
                convert(child, _json_index_path(current_path, index))
                for index, child in enumerate(current)
            ]
        if isinstance(current, _JSONNonInteger):
            errors.append(
                _source_error(
                    "invalid_definition_document",
                    source_id=source_id,
                    json_path=current_path,
                    reason="wrong_type",
                )
            )
            return current
        if isinstance(current, str):
            try:
                current.encode("utf-8")
            except UnicodeEncodeError:
                errors.append(
                    _source_error(
                        "invalid_definition_document",
                        source_id=source_id,
                        json_path=current_path,
                        reason="lone_surrogate",
                    )
                )
        return current

    return convert(value, path), errors


def _require_object_fields(
    value: Any,
    fields: frozenset[str],
    *,
    source_id: str,
    path: str,
    code: str,
    app: str | None = None,
    name: str | None = None,
    operation_index: int | None = None,
) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise _source_error(
            code,
            source_id=source_id,
            json_path=path,
            app=app,
            name=name,
            operation_index=operation_index,
        )
    unknown = sorted(set(value) - fields, key=_canonical_text_key)
    missing = sorted(fields - set(value), key=_canonical_text_key)
    if unknown or missing:
        candidates = [
            (field, "unknown_field") for field in unknown
        ] + [(field, "missing_field") for field in missing]
        field, reason = min(
            candidates,
            key=lambda item: (
                _canonical_text_key(item[0]),
                _FIELD_REASON_RANK[item[1]],
            ),
        )
        reason = {
            "invalid_definition_operation": "invalid_operation",
            "invalid_definition_ir": "invalid_ir",
        }.get(code, reason)
        raise _source_error(
            code,
            source_id=source_id,
            json_path=_json_member_path(path, field),
            app=app,
            name=name,
            operation_index=operation_index,
            reason=reason,
        )
    return value


def _collect_object_shape_errors(
    value: Any,
    fields: frozenset[str],
    *,
    source_id: str,
    path: str,
    errors: list[_DefinitionSourceError],
) -> dict[str, Any] | None:
    if not isinstance(value, dict):
        errors.append(
            _source_error(
                "invalid_definition_document",
                source_id=source_id,
                json_path=path,
                reason="wrong_type",
            )
        )
        return None
    for field in sorted(set(value) - fields, key=_canonical_text_key):
        errors.append(
            _source_error(
                "invalid_definition_document",
                source_id=source_id,
                json_path=_json_member_path(path, field),
                reason="unknown_field",
            )
        )
    for field in sorted(fields - set(value), key=_canonical_text_key):
        errors.append(
            _source_error(
                "invalid_definition_document",
                source_id=source_id,
                json_path=_json_member_path(path, field),
                reason="missing_field",
            )
        )
    return value


def _raise_first_document_error(
    errors: list[_DefinitionSourceError],
) -> None:
    if not errors:
        return
    errors.sort(
        key=lambda error: (
            _canonical_text_key(error.context["json_pointer"]),
            _DOCUMENT_REASON_RANK[error.context["reason"]],
        )
    )
    raise errors[0]


def _collect_field_integer_domain_error(
    value: Any,
    *,
    source_id: str,
    path: str,
    errors: list[_DefinitionSourceError],
) -> None:
    if not isinstance(value, dict) or "max_length" not in value:
        return
    max_length = value["max_length"]
    if (
        isinstance(max_length, int)
        and not isinstance(max_length, bool)
        and not _INT64_MIN <= max_length <= _INT64_MAX
    ):
        errors.append(
            _source_error(
                "invalid_definition_document",
                source_id=source_id,
                json_path=f"{path}.max_length",
                reason="out_of_range",
            )
        )


def _collect_wire_integer_domain_errors(
    root: dict[str, Any],
    *,
    source_id: str,
    errors: list[_DefinitionSourceError],
) -> None:
    migration = root.get("migration")
    if not isinstance(migration, dict):
        return
    operations = migration.get("operations")
    if not isinstance(operations, list):
        return
    for operation_index, operation in enumerate(operations):
        if not isinstance(operation, dict):
            continue
        operation_path = f"$.migration.operations[{operation_index}]"
        kind = operation.get("kind")
        if kind == "add_field":
            _collect_field_integer_domain_error(
                operation.get("field"),
                source_id=source_id,
                path=f"{operation_path}.field",
                errors=errors,
            )
            continue
        if kind != "create_model":
            continue
        model = operation.get("model")
        if not isinstance(model, dict):
            continue
        fields = model.get("fields")
        if not isinstance(fields, list):
            continue
        for field_index, field in enumerate(fields):
            _collect_field_integer_domain_error(
                field,
                source_id=source_id,
                path=f"{operation_path}.model.fields[{field_index}]",
                errors=errors,
            )


def _parse_outer(source: _SourceSnapshot) -> _ParsedDocument:
    try:
        text = source.document.decode("utf-8")
    except UnicodeDecodeError as error:
        raise _source_error(
            "invalid_definition_document",
            source_id=source.source_id,
            json_path="$",
            reason="invalid_utf8",
        ) from error

    decoder = json.JSONDecoder(
        object_pairs_hook=_JSONObject,
        parse_float=_preserve_non_integer,
        parse_constant=_reject_non_integer,
    )
    try:
        leading = len(text) - len(text.lstrip())
        parsed, end = decoder.raw_decode(text, leading)
    except (json.JSONDecodeError, ValueError) as error:
        raise _source_error(
            "invalid_definition_document",
            source_id=source.source_id,
            json_path="$",
            reason="syntax",
        ) from error
    if text[end:].strip():
        raise _source_error(
            "invalid_definition_document",
            source_id=source.source_id,
            json_path="$",
            reason="trailing_value",
        )

    value, errors = _plain_json(parsed, "$", source.source_id)
    root = _collect_object_shape_errors(
        value,
        frozenset({"compatibility", "migration", "producer"}),
        source_id=source.source_id,
        path="$",
        errors=errors,
    )
    if root is not None:
        compatibility = None
        if "compatibility" in root:
            compatibility = _collect_object_shape_errors(
                root["compatibility"],
                frozenset(_COMPATIBILITY_ORDER),
                source_id=source.source_id,
                path="$.compatibility",
                errors=errors,
            )
        if compatibility is not None:
            for coordinate in _COMPATIBILITY_ORDER:
                if coordinate not in compatibility:
                    continue
                coordinate_value = compatibility[coordinate]
                if isinstance(coordinate_value, bool) or not isinstance(
                    coordinate_value, int
                ):
                    errors.append(
                        _source_error(
                            "invalid_definition_document",
                            source_id=source.source_id,
                            json_path=f"$.compatibility.{coordinate}",
                            reason="wrong_type",
                        )
                    )
                elif not _INT64_MIN <= coordinate_value <= _INT64_MAX:
                    errors.append(
                        _source_error(
                            "invalid_definition_document",
                            source_id=source.source_id,
                            json_path=f"$.compatibility.{coordinate}",
                            reason="out_of_range",
                        )
                    )

        if "migration" in root:
            _collect_object_shape_errors(
                root["migration"],
                frozenset({"app", "dependencies", "name", "operations"}),
                source_id=source.source_id,
                path="$.migration",
                errors=errors,
            )

        producer = None
        if "producer" in root:
            producer = _collect_object_shape_errors(
                root["producer"],
                frozenset({"name", "version"}),
                source_id=source.source_id,
                path="$.producer",
                errors=errors,
            )
        if producer is not None:
            for field in ("name", "version"):
                if field in producer and (
                    not isinstance(producer[field], str) or not producer[field]
                ):
                    errors.append(
                        _source_error(
                            "invalid_definition_document",
                            source_id=source.source_id,
                            json_path=f"$.producer.{field}",
                            reason="wrong_type",
                        )
                    )
        _collect_wire_integer_domain_errors(
            root,
            source_id=source.source_id,
            errors=errors,
        )
    _raise_first_document_error(errors)
    assert root is not None
    return _ParsedDocument(source=source, value=root)


def _check_compatibility(documents: Sequence[_ParsedDocument]) -> None:
    for coordinate in _COMPATIBILITY_ORDER:
        expected = _COMPATIBILITY[coordinate]
        for document in documents:
            actual = document.value["compatibility"][coordinate]
            if actual != expected:
                raise _source_error(
                    _COMPATIBILITY_CODES[coordinate],
                    source_id=document.source.source_id,
                    json_path=f"$.compatibility.{coordinate}",
                    reason=coordinate,
                )


def _is_exported_go_identifier(value: str) -> bool:
    if not value:
        return False
    first = value[0]
    if not first.isupper():
        return False
    return all(
        character == "_"
        or character.isalpha()
        or character.isdecimal()
        for character in value[1:]
    )


def _require_string(
    value: Any,
    *,
    source_id: str,
    path: str,
    code: str,
    app: str | None = None,
    name: str | None = None,
    operation_index: int | None = None,
) -> str:
    if not isinstance(value, str):
        raise _source_error(
            code,
            source_id=source_id,
            json_path=path,
            app=app,
            name=name,
            operation_index=operation_index,
        )
    _utf8_bytes(
        value,
        code=code,
        context={"json_path": path, "source_id": source_id},
    )
    return value


def _append_semantic_error(
    errors: list[_DefinitionSourceError],
    code: str,
    *,
    source_id: str,
    path: str,
    app: str = "",
    name: str = "",
    operation_index: int = -1,
    reason: str | None = None,
) -> None:
    reason = {
        "unsupported_definition_operation": "unsupported_operation",
        "invalid_definition_operation": "invalid_operation",
        "invalid_definition_ir": "invalid_ir",
    }.get(code, reason)
    errors.append(
        _source_error(
            code,
            source_id=source_id,
            json_path=path,
            app=app,
            name=name,
            operation_index=operation_index,
            stage=(
                "semantic" if code == "invalid_definition_document" else None
            ),
            reason=reason,
        )
    )


def _collect_semantic_shape(
    value: Any,
    fields: frozenset[str],
    *,
    code: str,
    source_id: str,
    path: str,
    app: str,
    name: str,
    operation_index: int,
    errors: list[_DefinitionSourceError],
) -> dict[str, Any] | None:
    if not isinstance(value, dict):
        _append_semantic_error(
            errors,
            code,
            source_id=source_id,
            path=path,
            app=app,
            name=name,
            operation_index=operation_index,
        )
        return None
    for field in sorted(set(value) - fields, key=_canonical_text_key):
        _append_semantic_error(
            errors,
            code,
            source_id=source_id,
            path=_json_member_path(path, field),
            app=app,
            name=name,
            operation_index=operation_index,
            reason="unknown_field",
        )
    for field in sorted(fields - set(value), key=_canonical_text_key):
        _append_semantic_error(
            errors,
            code,
            source_id=source_id,
            path=_json_member_path(path, field),
            app=app,
            name=name,
            operation_index=operation_index,
            reason="missing_field",
        )
    return value


def _collect_default_candidates(
    value: Any,
    *,
    source_id: str,
    path: str,
    app: str,
    name: str,
    operation_index: int,
    errors: list[_DefinitionSourceError],
) -> bool:
    start = len(errors)
    if value is None:
        return True
    if not isinstance(value, dict):
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=path,
            app=app,
            name=name,
            operation_index=operation_index,
        )
        return False

    kind = value.get("kind")
    common_fields = frozenset({"boolean", "kind", "string"})
    if "kind" not in value or not isinstance(kind, str):
        for field in sorted(
            set(value) - common_fields,
            key=_canonical_text_key,
        ):
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=_json_member_path(path, field),
                app=app,
                name=name,
                operation_index=operation_index,
            )
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=f"{path}.kind",
            app=app,
            name=name,
            operation_index=operation_index,
        )
        return False

    member = {"string": "string", "boolean": "boolean"}.get(kind)
    if member is None:
        for field in sorted(
            set(value) - common_fields,
            key=_canonical_text_key,
        ):
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=_json_member_path(path, field),
                app=app,
                name=name,
                operation_index=operation_index,
            )
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=f"{path}.kind",
            app=app,
            name=name,
            operation_index=operation_index,
        )
        return False

    expected = frozenset({"kind", member})
    default = _collect_semantic_shape(
        value,
        expected,
        code="invalid_definition_ir",
        source_id=source_id,
        path=path,
        app=app,
        name=name,
        operation_index=operation_index,
        errors=errors,
    )
    assert default is not None
    if member in default:
        scalar = default[member]
        if member == "string":
            valid_scalar = isinstance(scalar, str)
            if valid_scalar:
                try:
                    scalar.encode("utf-8")
                except UnicodeEncodeError:
                    valid_scalar = False
        else:
            valid_scalar = isinstance(scalar, bool)
        if not valid_scalar:
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=f"{path}.{member}",
                app=app,
                name=name,
                operation_index=operation_index,
            )
    return len(errors) == start


def _known_default_core(
    value: Any,
) -> tuple[bool, dict[str, Any] | None]:
    if value is None:
        return True, None
    if not isinstance(value, dict):
        return False, None
    kind = value.get("kind")
    if kind == "string" and isinstance(value.get("string"), str):
        return True, {"kind": kind, "string": value["string"]}
    if kind == "boolean" and isinstance(value.get("boolean"), bool):
        return True, {"boolean": value["boolean"], "kind": kind}
    return False, None


def _collect_field_candidates(
    value: Any,
    *,
    source_id: str,
    path: str,
    app: str,
    name: str,
    operation_index: int,
    errors: list[_DefinitionSourceError],
) -> bool:
    start = len(errors)
    expected = frozenset(
        {
            "column",
            "default",
            "go_name",
            "kind",
            "max_length",
            "name",
            "nullable",
            "primary_key",
        }
    )
    field = _collect_semantic_shape(
        value,
        expected,
        code="invalid_definition_ir",
        source_id=source_id,
        path=path,
        app=app,
        name=name,
        operation_index=operation_index,
        errors=errors,
    )
    if field is None:
        return False

    valid: dict[str, bool] = {}
    for member in ("column", "go_name", "kind", "name"):
        if member not in field:
            valid[member] = False
            continue
        member_value = field[member]
        valid[member] = isinstance(member_value, str)
        if valid[member]:
            try:
                member_value.encode("utf-8")
            except UnicodeEncodeError:
                valid[member] = False
        if not valid[member]:
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=f"{path}.{member}",
                app=app,
                name=name,
                operation_index=operation_index,
            )

    if valid.get("column") and not _DATABASE_IDENTIFIER.fullmatch(
        field["column"]
    ):
        valid["column"] = False
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=f"{path}.column",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    if valid.get("go_name") and not _is_exported_go_identifier(
        field["go_name"]
    ):
        valid["go_name"] = False
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=f"{path}.go_name",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    if valid.get("name") and not _DATABASE_IDENTIFIER.fullmatch(field["name"]):
        valid["name"] = False
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=f"{path}.name",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    default_core_known = False
    default_core: dict[str, Any] | None = None
    if "default" in field:
        _collect_default_candidates(
            field["default"],
            source_id=source_id,
            path=f"{path}.default",
            app=app,
            name=name,
            operation_index=operation_index,
            errors=errors,
        )
        default_core_known, default_core = _known_default_core(
            field["default"]
        )

    max_length = field.get("max_length")
    valid["max_length"] = (
        isinstance(max_length, int) and not isinstance(max_length, bool)
    )
    if "max_length" in field and not valid["max_length"]:
        _append_semantic_error(
            errors,
            "invalid_definition_document",
            source_id=source_id,
            path=f"{path}.max_length",
            app=app,
            name=name,
            operation_index=operation_index,
            reason="wrong_type",
        )
    elif valid["max_length"] and not 0 <= max_length <= _MAX_LENGTH_MAX:
        valid["max_length"] = False
        _append_semantic_error(
            errors,
            "invalid_definition_document",
            source_id=source_id,
            path=f"{path}.max_length",
            app=app,
            name=name,
            operation_index=operation_index,
            reason="out_of_range",
        )

    for member in ("nullable", "primary_key"):
        member_value = field.get(member)
        valid[member] = isinstance(member_value, bool)
        if member in field and not valid[member]:
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=f"{path}.{member}",
                app=app,
                name=name,
                operation_index=operation_index,
            )

    if valid.get("kind"):
        kind = field["kind"]
        invariant_paths: list[str] = []
        if kind == "auto":
            if default_core_known and default_core is not None:
                invariant_paths.append(f"{path}.default")
            if valid.get("max_length") and max_length != 0:
                invariant_paths.append(f"{path}.max_length")
            if valid.get("nullable") and field["nullable"]:
                invariant_paths.append(f"{path}.nullable")
            if valid.get("primary_key") and not field["primary_key"]:
                invariant_paths.append(f"{path}.primary_key")
        elif kind == "char":
            if default_core_known and default_core is not None:
                if default_core.get("kind") != "string" or (
                    valid.get("max_length")
                    and len(default_core.get("string", "")) > max_length
                ):
                    invariant_paths.append(f"{path}.default")
            if valid.get("max_length") and max_length <= 0:
                invariant_paths.append(f"{path}.max_length")
            if valid.get("primary_key") and field["primary_key"]:
                invariant_paths.append(f"{path}.primary_key")
        elif kind == "boolean":
            if (
                default_core_known
                and default_core is not None
                and default_core.get("kind") != "boolean"
            ):
                invariant_paths.append(f"{path}.default")
            if valid.get("max_length") and max_length != 0:
                invariant_paths.append(f"{path}.max_length")
            if valid.get("nullable") and field["nullable"]:
                invariant_paths.append(f"{path}.nullable")
            if valid.get("primary_key") and field["primary_key"]:
                invariant_paths.append(f"{path}.primary_key")
        else:
            invariant_paths.append(f"{path}.kind")
        for invariant_path in invariant_paths:
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=invariant_path,
                app=app,
                name=name,
                operation_index=operation_index,
            )
    return len(errors) == start


def _collect_model_candidates(
    value: Any,
    *,
    source_id: str,
    path: str,
    app: str,
    name: str,
    operation_index: int,
    errors: list[_DefinitionSourceError],
) -> bool:
    start = len(errors)
    expected = frozenset({"db_table", "fields", "go_name", "name"})
    model = _collect_semantic_shape(
        value,
        expected,
        code="invalid_definition_ir",
        source_id=source_id,
        path=path,
        app=app,
        name=name,
        operation_index=operation_index,
        errors=errors,
    )
    if model is None:
        return False

    valid: dict[str, bool] = {}
    for member in ("db_table", "go_name", "name"):
        member_value = model.get(member)
        valid[member] = isinstance(member_value, str)
        if member in model and not valid[member]:
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=f"{path}.{member}",
                app=app,
                name=name,
                operation_index=operation_index,
            )
    if valid.get("db_table") and not _DATABASE_IDENTIFIER.fullmatch(
        model["db_table"]
    ):
        valid["db_table"] = False
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=f"{path}.db_table",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    if valid.get("go_name") and not _is_exported_go_identifier(
        model["go_name"]
    ):
        valid["go_name"] = False
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=f"{path}.go_name",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    if valid.get("name") and not _DATABASE_IDENTIFIER.fullmatch(model["name"]):
        valid["name"] = False
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=f"{path}.name",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    fields = model.get("fields")
    valid_fields = isinstance(fields, list) and bool(fields)
    field_validity: list[bool] = []
    if "fields" in model and not valid_fields:
        _append_semantic_error(
            errors,
            "invalid_definition_ir",
            source_id=source_id,
            path=f"{path}.fields",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    if valid_fields:
        for field_index, field in enumerate(fields):
            field_validity.append(
                _collect_field_candidates(
                    field,
                    source_id=source_id,
                    path=f"{path}.fields[{field_index}]",
                    app=app,
                    name=name,
                    operation_index=operation_index,
                    errors=errors,
                )
            )

    aggregate_valid = True
    if valid_fields:
        for member in ("name", "go_name", "column"):
            values = [
                field[member]
                for field in fields
                if isinstance(field, dict)
                and isinstance(field.get(member), str)
            ]
            if len(values) != len(set(values)):
                aggregate_valid = False
                _append_semantic_error(
                    errors,
                    "invalid_definition_ir",
                    source_id=source_id,
                    path=f"{path}.fields",
                    app=app,
                    name=name,
                    operation_index=operation_index,
                )
        if all(
            isinstance(field, dict)
            and isinstance(field.get("kind"), str)
            for field in fields
        ) and not any(field["kind"] == "auto" for field in fields):
            aggregate_valid = False
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=f"{path}.fields",
                app=app,
                name=name,
                operation_index=operation_index,
            )
        primary_keys = [
            field["primary_key"]
            for field in fields
            if isinstance(field, dict)
            and isinstance(field.get("primary_key"), bool)
        ]
        primary_key_count = sum(primary_keys)
        if primary_key_count > 1 or (
            len(primary_keys) == len(fields) and primary_key_count != 1
        ):
            aggregate_valid = False
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=f"{path}.fields",
                app=app,
                name=name,
                operation_index=operation_index,
            )

    if (
        set(model) == expected
        and all(valid.get(member) for member in ("db_table", "go_name", "name"))
        and valid_fields
        and all(field_validity)
        and aggregate_valid
    ):
        wrapper = {
            "app_label": app,
            "format_version": 2,
            "models": [deepcopy(model)],
        }
        if _normalize_model_wrapper(app, model) != wrapper:
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=path,
                app=app,
                name=name,
                operation_index=operation_index,
            )
    return len(errors) == start


def _collect_operation_candidates(
    value: Any,
    *,
    source_id: str,
    app: str,
    name: str,
    operation_index: int,
    errors: list[_DefinitionSourceError],
) -> None:
    path = f"$.migration.operations[{operation_index}]"
    if not isinstance(value, dict):
        _append_semantic_error(
            errors,
            "invalid_definition_operation",
            source_id=source_id,
            path=path,
            app=app,
            name=name,
            operation_index=operation_index,
        )
        return

    kind = value.get("kind")
    supported = isinstance(kind, str) and kind in {
        "create_model",
        "add_field",
    }
    if supported:
        expected = (
            frozenset({"app_label", "kind", "model"})
            if kind == "create_model"
            else frozenset({"app_label", "field", "kind", "model_name"})
        )
        _collect_semantic_shape(
            value,
            expected,
            code="invalid_definition_operation",
            source_id=source_id,
            path=path,
            app=app,
            name=name,
            operation_index=operation_index,
            errors=errors,
        )
    else:
        for field in sorted(
            set(value) - _OPERATION_WIRE_FIELDS,
            key=_canonical_text_key,
        ):
            _append_semantic_error(
                errors,
                "invalid_definition_operation",
                source_id=source_id,
                path=_json_member_path(path, field),
                app=app,
                name=name,
                operation_index=operation_index,
                reason="invalid_operation",
            )
        if "kind" not in value or not isinstance(kind, str):
            _append_semantic_error(
                errors,
                "invalid_definition_operation",
                source_id=source_id,
                path=f"{path}.kind",
                app=app,
                name=name,
                operation_index=operation_index,
                reason="invalid_operation",
            )
        else:
            _append_semantic_error(
                errors,
                "unsupported_definition_operation",
                source_id=source_id,
                path=f"{path}.kind",
                app=app,
                name=name,
                operation_index=operation_index,
            )
        return

    app_label_valid = False
    if "app_label" in value:
        app_label = value["app_label"]
        app_label_valid = isinstance(app_label, str)
        if not app_label_valid or app_label != app:
            app_label_valid = False
            _append_semantic_error(
                errors,
                "invalid_definition_operation",
                source_id=source_id,
                path=f"{path}.app_label",
                app=app,
                name=name,
                operation_index=operation_index,
            )
        elif not _DATABASE_IDENTIFIER.fullmatch(app_label):
            app_label_valid = False
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=f"{path}.app_label",
                app=app,
                name=name,
                operation_index=operation_index,
            )

    field_valid = False
    add_field_restrictions_valid = True
    if "field" in value and (kind == "add_field" or not supported):
        field_valid = _collect_field_candidates(
            value["field"],
            source_id=source_id,
            path=f"{path}.field",
            app=app,
            name=name,
            operation_index=operation_index,
            errors=errors,
        )
        field_value = value["field"]
        if kind == "add_field" and isinstance(field_value, dict):
            field_kind = field_value.get("kind")
            if isinstance(field_kind, str) and field_kind not in {
                "char",
                "boolean",
            }:
                add_field_restrictions_valid = False
                _append_semantic_error(
                    errors,
                    "invalid_definition_ir",
                    source_id=source_id,
                    path=f"{path}.field.kind",
                    app=app,
                    name=name,
                    operation_index=operation_index,
                )
            if field_value.get("primary_key") is True:
                add_field_restrictions_valid = False
                _append_semantic_error(
                    errors,
                    "invalid_definition_ir",
                    source_id=source_id,
                    path=f"{path}.field.primary_key",
                    app=app,
                    name=name,
                    operation_index=operation_index,
                )
    if "model" in value and (kind == "create_model" or not supported):
        _collect_model_candidates(
            value["model"],
            source_id=source_id,
            path=f"{path}.model",
            app=app,
            name=name,
            operation_index=operation_index,
            errors=errors,
        )

    if "model_name" in value and (kind == "add_field" or not supported):
        model_name = value["model_name"]
        if not isinstance(model_name, str) or not _DATABASE_IDENTIFIER.fullmatch(
            model_name
        ):
            _append_semantic_error(
                errors,
                "invalid_definition_operation",
                source_id=source_id,
                path=f"{path}.model_name",
                app=app,
                name=name,
                operation_index=operation_index,
            )
    if (
        kind == "add_field"
        and app_label_valid
        and field_valid
        and add_field_restrictions_valid
    ):
        normalized_field = _normalize_add_field_wrapper(app, value["field"])
        if normalized_field is None or normalized_field != value["field"]:
            _append_semantic_error(
                errors,
                "invalid_definition_ir",
                source_id=source_id,
                path=f"{path}.field",
                app=app,
                name=name,
                operation_index=operation_index,
            )


def _preflight_document_semantics(document: _ParsedDocument) -> None:
    source_id = document.source.source_id
    migration = document.value["migration"]
    producer = document.value["producer"]
    errors: list[_DefinitionSourceError] = []

    raw_app = migration["app"]
    app = raw_app if isinstance(raw_app, str) else ""
    raw_name = migration["name"]
    name = raw_name if isinstance(raw_name, str) else ""
    if not isinstance(raw_app, str):
        _append_semantic_error(
            errors,
            "invalid_definition_operation",
            source_id=source_id,
            path="$.migration.app",
        )

    dependencies = migration["dependencies"]
    if not isinstance(dependencies, list):
        _append_semantic_error(
            errors,
            "invalid_definition_operation",
            source_id=source_id,
            path="$.migration.dependencies",
            app=app,
            name=name,
        )
    else:
        for dependency_index, dependency in enumerate(dependencies):
            dependency_path = f"$.migration.dependencies[{dependency_index}]"
            decoded_dependency = _collect_semantic_shape(
                dependency,
                frozenset({"app", "name"}),
                code="invalid_definition_operation",
                source_id=source_id,
                path=dependency_path,
                app=app,
                name=name,
                operation_index=-1,
                errors=errors,
            )
            if decoded_dependency is None:
                continue
            for member in ("app", "name"):
                if member in decoded_dependency and not isinstance(
                    decoded_dependency[member], str
                ):
                    _append_semantic_error(
                        errors,
                        "invalid_definition_operation",
                        source_id=source_id,
                        path=f"{dependency_path}.{member}",
                        app=app,
                        name=name,
                    )

    if not isinstance(raw_name, str):
        _append_semantic_error(
            errors,
            "invalid_definition_operation",
            source_id=source_id,
            path="$.migration.name",
            app=app,
        )

    operations = migration["operations"]
    if not isinstance(operations, list):
        _append_semantic_error(
            errors,
            "invalid_definition_operation",
            source_id=source_id,
            path="$.migration.operations",
            app=app,
            name=name,
        )
    else:
        for operation_index, operation in enumerate(operations):
            _collect_operation_candidates(
                operation,
                source_id=source_id,
                app=app,
                name=name,
                operation_index=operation_index,
                errors=errors,
            )

    for member in ("name", "version"):
        producer_value = producer[member]
        if not isinstance(producer_value, str) or not producer_value:
            _append_semantic_error(
                errors,
                "invalid_definition_document",
                source_id=source_id,
                path=f"$.producer.{member}",
                app=app,
                name=name,
                reason="wrong_type",
            )

    if errors:
        errors.sort(
            key=lambda error: (
                _canonical_text_key(error.context["json_pointer"]),
                _SEMANTIC_REASON_RANK.get(error.context["reason"], 1),
            )
        )
        raise errors[0]


def _decode_default(
    value: Any,
    *,
    source_id: str,
    path: str,
    app: str,
    name: str,
    operation_index: int,
) -> dict[str, Any] | None:
    if value is None:
        return None
    if not isinstance(value, dict):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=path,
            app=app,
            name=name,
            operation_index=operation_index,
        )
    if not isinstance(value.get("kind"), str):
        _require_object_fields(
            value,
            frozenset({"kind"}),
            source_id=source_id,
            path=path,
            code="invalid_definition_ir",
            app=app,
            name=name,
            operation_index=operation_index,
        )
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.kind",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    kind = value["kind"]
    members = {"string": "string", "boolean": "boolean"}
    member = members.get(kind)
    if member is None:
        _require_object_fields(
            value,
            frozenset({"kind"}),
            source_id=source_id,
            path=path,
            code="invalid_definition_ir",
            app=app,
            name=name,
            operation_index=operation_index,
        )
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.kind",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    _require_object_fields(
        value,
        frozenset({"kind", member}),
        source_id=source_id,
        path=path,
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    scalar = value[member]
    if kind == "string":
        _require_string(
            scalar,
            source_id=source_id,
            path=f"{path}.string",
            code="invalid_definition_ir",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    elif kind == "boolean":
        if not isinstance(scalar, bool):
            raise _source_error(
                "invalid_definition_ir",
                source_id=source_id,
                json_path=f"{path}.boolean",
                app=app,
                name=name,
                operation_index=operation_index,
            )
    return deepcopy(value)


def _normalize_model_wrapper(
    app_label: str,
    model: dict[str, Any],
) -> dict[str, Any]:
    """Mirror the current one-model Schema IR normalization defaults."""

    normalized = {
        "app_label": app_label,
        "format_version": 2,
        "models": [deepcopy(model)],
    }
    normalized_model = normalized["models"][0]
    if not normalized_model["db_table"]:
        normalized_model["db_table"] = (
            f"{app_label}_{normalized_model['name']}"
        )
    fields = normalized_model["fields"]
    if not any(field["kind"] == "auto" for field in fields):
        fields.insert(0, _auto_field())
    for field in fields:
        if not field["column"]:
            field["column"] = field["name"]
    return normalized


def _normalize_add_field_wrapper(
    app_label: str,
    field: dict[str, Any],
) -> dict[str, Any] | None:
    """Normalize an AddField candidate inside an explicit sentinel model."""

    sentinel_name = "_godj_loader_pk"
    sentinel_go_name = "GodjLoaderPK"
    sentinel_column = "_godj_loader_pk"
    while (
        sentinel_name == field["name"]
        or sentinel_go_name == field["go_name"]
        or sentinel_column == field["column"]
    ):
        sentinel_name += "_"
        sentinel_go_name += "X"
        sentinel_column += "_"
    sentinel_field = _auto_field()
    sentinel_field.update(
        {
            "column": sentinel_column,
            "go_name": sentinel_go_name,
            "name": sentinel_name,
        }
    )
    sentinel = {
        "db_table": "_godj_loader_validation",
        "fields": [sentinel_field, deepcopy(field)],
        "go_name": "GodjLoaderValidation",
        "name": "_godj_loader_validation",
    }
    wrapper = {
        "app_label": app_label,
        "format_version": 2,
        "models": [sentinel],
    }
    normalized = _normalize_model_wrapper(app_label, sentinel)
    if normalized != wrapper:
        return None
    normalized_fields = normalized["models"][0]["fields"]
    if sum(item["primary_key"] for item in normalized_fields) != 1:
        return None
    if normalized_fields[1] != field:
        return None
    return normalized_fields[1]


def _decode_field(
    value: Any,
    *,
    source_id: str,
    path: str,
    app: str,
    name: str,
    operation_index: int,
) -> dict[str, Any]:
    field = _require_object_fields(
        value,
        frozenset(
            {
                "column",
                "default",
                "go_name",
                "kind",
                "max_length",
                "name",
                "nullable",
                "primary_key",
            }
        ),
        source_id=source_id,
        path=path,
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    column = _require_string(
        field["column"],
        source_id=source_id,
        path=f"{path}.column",
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    if not _DATABASE_IDENTIFIER.fullmatch(column):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.column",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    default = _decode_default(
        field["default"],
        source_id=source_id,
        path=f"{path}.default",
        app=app,
        name=name,
        operation_index=operation_index,
    )

    go_name = _require_string(
        field["go_name"],
        source_id=source_id,
        path=f"{path}.go_name",
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    if not _is_exported_go_identifier(go_name):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.go_name",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    kind = _require_string(
        field["kind"],
        source_id=source_id,
        path=f"{path}.kind",
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )

    max_length = field["max_length"]
    if isinstance(max_length, bool) or not isinstance(max_length, int):
        raise _source_error(
            "invalid_definition_document",
            source_id=source_id,
            json_path=f"{path}.max_length",
            app=app,
            name=name,
            operation_index=operation_index,
            reason="wrong_type",
        )
    if not 0 <= max_length <= _MAX_LENGTH_MAX:
        raise _source_error(
            "invalid_definition_document",
            source_id=source_id,
            json_path=f"{path}.max_length",
            app=app,
            name=name,
            operation_index=operation_index,
            stage="semantic",
            reason="out_of_range",
        )

    field_name = _require_string(
        field["name"],
        source_id=source_id,
        path=f"{path}.name",
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    if not _DATABASE_IDENTIFIER.fullmatch(field_name):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.name",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    if not isinstance(field["nullable"], bool):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.nullable",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    if not isinstance(field["primary_key"], bool):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.primary_key",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    invariant_paths: list[str] = []
    if kind == "auto":
        if default is not None:
            invariant_paths.append(f"{path}.default")
        if max_length != 0:
            invariant_paths.append(f"{path}.max_length")
        if field["nullable"]:
            invariant_paths.append(f"{path}.nullable")
        if not field["primary_key"]:
            invariant_paths.append(f"{path}.primary_key")
    elif kind == "char":
        if default is not None and (
            default["kind"] != "string"
            or len(default["string"]) > max_length
        ):
            invariant_paths.append(f"{path}.default")
        if max_length <= 0:
            invariant_paths.append(f"{path}.max_length")
        if field["primary_key"]:
            invariant_paths.append(f"{path}.primary_key")
    elif kind == "boolean":
        if default is not None and default["kind"] != "boolean":
            invariant_paths.append(f"{path}.default")
        if max_length != 0:
            invariant_paths.append(f"{path}.max_length")
        if field["nullable"]:
            invariant_paths.append(f"{path}.nullable")
        if field["primary_key"]:
            invariant_paths.append(f"{path}.primary_key")
    else:
        invariant_paths.append(f"{path}.kind")
    if invariant_paths:
        invariant_paths.sort(
            key=lambda candidate: _canonical_text_key(
                _json_path_to_pointer(candidate)
            )
        )
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=invariant_paths[0],
            app=app,
            name=name,
            operation_index=operation_index,
        )
    return deepcopy(field)


def _decode_model(
    value: Any,
    *,
    source_id: str,
    path: str,
    app: str,
    name: str,
    operation_index: int,
) -> dict[str, Any]:
    model = _require_object_fields(
        value,
        frozenset({"db_table", "fields", "go_name", "name"}),
        source_id=source_id,
        path=path,
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    db_table = _require_string(
        model["db_table"],
        source_id=source_id,
        path=f"{path}.db_table",
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    if not _DATABASE_IDENTIFIER.fullmatch(db_table):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.db_table",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    if not isinstance(model["fields"], list) or not model["fields"]:
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.fields",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    fields: list[dict[str, Any] | None] = [None for _field in model["fields"]]
    field_indices = sorted(
        range(len(model["fields"])),
        key=lambda index: _canonical_text_key(str(index)),
    )
    for index in field_indices:
        fields[index] = _decode_field(
            model["fields"][index],
            source_id=source_id,
            path=f"{path}.fields[{index}]",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    decoded_fields = [field for field in fields if field is not None]

    go_name = _require_string(
        model["go_name"],
        source_id=source_id,
        path=f"{path}.go_name",
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    if not _is_exported_go_identifier(go_name):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.go_name",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    model_name = _require_string(
        model["name"],
        source_id=source_id,
        path=f"{path}.name",
        code="invalid_definition_ir",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    if not _DATABASE_IDENTIFIER.fullmatch(model_name):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.name",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    for member in ("name", "go_name", "column"):
        values = [field[member] for field in decoded_fields]
        if len(values) != len(set(values)):
            raise _source_error(
                "invalid_definition_ir",
                source_id=source_id,
                json_path=f"{path}.fields",
                app=app,
                name=name,
                operation_index=operation_index,
            )
    if sum(field["primary_key"] for field in decoded_fields) != 1 or not any(
        field["kind"] == "auto" for field in decoded_fields
    ):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.fields",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    decoded = deepcopy(model)
    decoded["fields"] = decoded_fields
    wrapper = {
        "app_label": app,
        "format_version": 2,
        "models": [deepcopy(decoded)],
    }
    if _normalize_model_wrapper(app, decoded) != wrapper:
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=path,
            app=app,
            name=name,
            operation_index=operation_index,
        )
    return decoded


def _decode_operation(
    value: Any,
    *,
    source_id: str,
    app: str,
    name: str,
    operation_index: int,
) -> dict[str, Any]:
    path = f"$.migration.operations[{operation_index}]"
    if not isinstance(value, dict):
        raise _source_error(
            "invalid_definition_operation",
            source_id=source_id,
            json_path=path,
            app=app,
            name=name,
            operation_index=operation_index,
        )
    kind = value.get("kind")
    if not isinstance(kind, str) or kind not in {
        "create_model",
        "add_field",
    }:
        candidates = [
            _source_error(
                "invalid_definition_operation",
                source_id=source_id,
                json_path=_json_member_path(path, field),
                app=app,
                name=name,
                operation_index=operation_index,
                reason="invalid_operation",
            )
            for field in sorted(
                set(value) - _OPERATION_WIRE_FIELDS,
                key=_canonical_text_key,
            )
        ]
        if "kind" not in value or not isinstance(kind, str):
            candidates.append(
                _source_error(
                    "invalid_definition_operation",
                    source_id=source_id,
                    json_path=f"{path}.kind",
                    app=app,
                    name=name,
                    operation_index=operation_index,
                    reason="invalid_operation",
                )
            )
        else:
            candidates.append(
                _source_error(
                    "unsupported_definition_operation",
                    source_id=source_id,
                    json_path=f"{path}.kind",
                    app=app,
                    name=name,
                    operation_index=operation_index,
                )
            )
        candidates.sort(
            key=lambda error: (
                _canonical_text_key(error.context["json_pointer"]),
                _SEMANTIC_REASON_RANK[error.context["reason"]],
            )
        )
        raise candidates[0]
    expected = (
        frozenset({"app_label", "kind", "model"})
        if kind == "create_model"
        else frozenset({"app_label", "field", "kind", "model_name"})
    )
    operation = _require_object_fields(
        value,
        expected,
        source_id=source_id,
        path=path,
        code="invalid_definition_operation",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    app_label = _require_string(
        operation["app_label"],
        source_id=source_id,
        path=f"{path}.app_label",
        code="invalid_definition_operation",
        app=app,
        name=name,
        operation_index=operation_index,
    )
    if app_label != app:
        raise _source_error(
            "invalid_definition_operation",
            source_id=source_id,
            json_path=f"{path}.app_label",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    if not _DATABASE_IDENTIFIER.fullmatch(app_label):
        raise _source_error(
            "invalid_definition_ir",
            source_id=source_id,
            json_path=f"{path}.app_label",
            app=app,
            name=name,
            operation_index=operation_index,
        )

    decoded = deepcopy(operation)
    if kind == "create_model":
        decoded["model"] = _decode_model(
            operation["model"],
            source_id=source_id,
            path=f"{path}.model",
            app=app,
            name=name,
            operation_index=operation_index,
        )
    else:
        decoded["field"] = _decode_field(
            operation["field"],
            source_id=source_id,
            path=f"{path}.field",
            app=app,
            name=name,
            operation_index=operation_index,
        )
        normalized_field = _normalize_add_field_wrapper(app, decoded["field"])
        if normalized_field is None or normalized_field != decoded["field"]:
            raise _source_error(
                "invalid_definition_ir",
                source_id=source_id,
                json_path=f"{path}.field",
                app=app,
                name=name,
                operation_index=operation_index,
            )
        model_name = _require_string(
            operation["model_name"],
            source_id=source_id,
            path=f"{path}.model_name",
            code="invalid_definition_operation",
            app=app,
            name=name,
            operation_index=operation_index,
        )
        if not _DATABASE_IDENTIFIER.fullmatch(model_name):
            raise _source_error(
                "invalid_definition_operation",
                source_id=source_id,
                json_path=f"{path}.model_name",
                app=app,
                name=name,
                operation_index=operation_index,
            )
    return decoded


def _decode_document(
    document: _ParsedDocument,
    metrics: dict[str, Any],
) -> _DecodedDefinition:
    _preflight_document_semantics(document)
    migration = document.value["migration"]
    source_id = document.source.source_id
    if not isinstance(migration["app"], str):
        raise _source_error(
            "invalid_definition_operation",
            source_id=source_id,
            json_path="$.migration.app",
        )
    app = migration["app"]
    if not isinstance(migration["dependencies"], list):
        raise _source_error(
            "invalid_definition_operation",
            source_id=source_id,
            json_path="$.migration.dependencies",
            app=app,
        )

    raw_name = migration["name"]
    name = raw_name if isinstance(raw_name, str) else ""
    dependencies: list[dict[str, str]] = []
    dependency_indices = sorted(
        range(len(migration["dependencies"])),
        key=lambda index: _canonical_text_key(str(index)),
    )
    for index in dependency_indices:
        dependency_value = migration["dependencies"][index]
        dependency = _require_object_fields(
            dependency_value,
            frozenset({"app", "name"}),
            source_id=source_id,
            path=f"$.migration.dependencies[{index}]",
            code="invalid_definition_operation",
            app=app,
            name=name,
        )
        dependency_app = _require_string(
            dependency["app"],
            source_id=source_id,
            path=f"$.migration.dependencies[{index}].app",
            code="invalid_definition_operation",
            app=app,
            name=name,
        )
        dependency_name = _require_string(
            dependency["name"],
            source_id=source_id,
            path=f"$.migration.dependencies[{index}].name",
            code="invalid_definition_operation",
            app=app,
            name=name,
        )
        dependencies.append({"app": dependency_app, "name": dependency_name})

    if not isinstance(raw_name, str):
        raise _source_error(
            "invalid_definition_operation",
            source_id=source_id,
            json_path="$.migration.name",
            app=app,
        )
    name = raw_name

    if not isinstance(migration["operations"], list):
        raise _source_error(
            "invalid_definition_operation",
            source_id=source_id,
            json_path="$.migration.operations",
            app=app,
            name=name,
        )

    operations: list[dict[str, Any] | None] = [
        None for _operation in migration["operations"]
    ]
    operation_indices = sorted(
        range(len(migration["operations"])),
        key=lambda index: _canonical_text_key(str(index)),
    )
    for index in operation_indices:
        operations[index] = _decode_operation(
            migration["operations"][index],
            source_id=source_id,
            app=app,
            name=name,
            operation_index=index,
        )
        metrics["operations_decoded"] += 1

    producer = document.value["producer"]
    if not producer["name"] or not producer["version"]:
        member = "name" if not producer["name"] else "version"
        raise _source_error(
            "invalid_definition_document",
            source_id=source_id,
            json_path=f"$.producer.{member}",
            app=app,
            name=name,
            stage="semantic",
            reason="wrong_type",
        )

    dependencies.sort(
        key=lambda item: _canonical_identity_key((item["app"], item["name"]))
    )
    return _DecodedDefinition(
        source_id=source_id,
        value={
            "app": app,
            "dependencies": dependencies,
            "name": name,
            "operations": [
                operation for operation in operations if operation is not None
            ],
        },
    )


def _validate_graph(
    definitions: Sequence[_DecodedDefinition],
) -> tuple[_DecodedDefinition, ...]:
    """Lock only the duplicate-node reference observation in Python.

    The complete graph taxonomy and precedence belong to GoDj's existing
    NewPlanner and are exercised by the GDJ-0019 Go test-only gate. Other
    malformed graph shapes are rejected here as non-oracle probes rather than
    synthesizing Python diagnostics that could drift from NewPlanner.
    """

    ordered = sorted(
        definitions,
        key=lambda item: (
            _canonical_identity_key((item.value["app"], item.value["name"])),
            _canonical_text_key(item.source_id),
        ),
    )

    for definition in ordered:
        identity = (definition.value["app"], definition.value["name"])
        if not all(identity):
            raise AssertionError(
                "invalid-node diagnostics require the NewPlanner test gate"
            )

    for previous, current in zip(ordered, ordered[1:]):
        previous_identity = (
            previous.value["app"],
            previous.value["name"],
        )
        current_identity = (current.value["app"], current.value["name"])
        if previous_identity == current_identity:
            raise _graph_error(
                "duplicate_node",
                source_id=current.source_id,
                app=current_identity[0],
                name=current_identity[1],
                json_path="$.migration",
            )

    nodes = {
        (definition.value["app"], definition.value["name"]): definition
        for definition in ordered
    }
    for identity in sorted(nodes, key=_canonical_identity_key):
        definition = nodes[identity]
        dependencies = [
            (item["app"], item["name"])
            for item in definition.value["dependencies"]
        ]
        for dependency in dependencies:
            if not all(dependency):
                raise AssertionError(
                    "invalid-dependency diagnostics require the NewPlanner "
                    "test gate"
                )
        if len(dependencies) != len(set(dependencies)):
            raise AssertionError(
                "duplicate-dependency diagnostics require the NewPlanner "
                "test gate"
            )
        for dependency in dependencies:
            if dependency not in nodes:
                raise AssertionError(
                    "missing-dependency diagnostics require the NewPlanner "
                    "test gate"
                )

    visiting: set[NodeKey] = set()
    visited: set[NodeKey] = set()

    def visit(identity: NodeKey) -> None:
        if identity in visiting:
            raise AssertionError(
                "cycle diagnostics require the NewPlanner test gate"
            )
        if identity in visited:
            return
        visiting.add(identity)
        for dependency in sorted(
            (
                (item["app"], item["name"])
                for item in nodes[identity].value["dependencies"]
            ),
            key=_canonical_identity_key,
        ):
            visit(dependency)
        visiting.remove(identity)
        visited.add(identity)

    for identity in sorted(nodes, key=_canonical_identity_key):
        visit(identity)
    return tuple(ordered)


def _canonical_json(value: Any) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        allow_nan=False,
        separators=(",", ":"),
        sort_keys=True,
    ).encode("utf-8")


def _definition_digest(definitions: Sequence[dict[str, Any]]) -> str:
    digest_document = {
        "compatibility": deepcopy(_COMPATIBILITY),
        "definitions": list(definitions),
        "domain": _DIGEST_DOMAIN,
    }
    return "sha256:" + hashlib.sha256(_canonical_json(digest_document)).hexdigest()


def _load(
    sources: Sequence[SourceDocument],
) -> tuple[_LoadedDefinitionSet, dict[str, Any]]:
    documents_received = len(sources) if isinstance(sources, Sequence) else 0
    metrics = _base_metrics(documents_received)
    try:
        if isinstance(sources, (str, bytes, bytearray)):
            raise _source_error("invalid_definition_source")
        snapshots = _snapshot_sources(sources)
        parsed: list[_ParsedDocument] = []
        for source in snapshots:
            parsed.append(_parse_outer(source))
            metrics["headers_validated"] += 1
        _check_compatibility(parsed)
        decoded = [_decode_document(document, metrics) for document in parsed]
        ordered = _validate_graph(decoded)
        definitions = tuple(deepcopy(item.value) for item in ordered)
        digest = _definition_digest(definitions)
        if not definitions and digest != _EMPTY_DIGEST:
            raise AssertionError("empty definition-set digest vector changed")
        source_inventory = tuple(
            {
                "app": document.value["migration"]["app"],
                "name": document.value["migration"]["name"],
                "producer": deepcopy(document.value["producer"]),
                "source_id": document.source.source_id,
            }
            for document in parsed
        )
        metrics["definitions_published"] = len(definitions)
        metrics["definition_sets_published"] = 1
        return (
            _LoadedDefinitionSet(
                definitions=definitions,
                digest=digest,
                sources=source_inventory,
            ),
            metrics,
        )
    except _DefinitionSourceError as error:
        failed_metrics = dict(metrics)
        failed_metrics["failure"] = dict(error.context)
        error.metrics = failed_metrics
        raise


def _success_result(
    loaded: _LoadedDefinitionSet,
    *,
    attempted: bool = False,
    calls: int = 0,
) -> dict[str, Any]:
    return {
        "compatibility": deepcopy(_COMPATIBILITY),
        "definition_set": {
            "definitions": deepcopy(list(loaded.definitions)),
            "digest": loaded.digest,
        },
        "handoff": {
            "attempted": attempted,
            "calls": calls,
            "observed_digest": loaded.digest if attempted else None,
        },
        "sources": deepcopy(list(loaded.sources)),
    }


def _success_observation(
    contract_id: str,
    phase: str,
    result: dict[str, Any],
    metrics: dict[str, Any],
    *,
    db_state: dict[str, Any] | None = None,
) -> dict[str, Any]:
    return {
        "db_state": normalize(db_state) if db_state is not None else None,
        "error": None,
        "id": contract_id,
        "metrics": normalize(metrics),
        "phase": phase,
        "result": normalize(result),
        "status": "observed",
    }


def _failure_observation(
    contract_id: str,
    phase: str,
    sources: Sequence[SourceDocument],
) -> dict[str, Any]:
    try:
        _load(sources)
    except _DefinitionSourceError as error:
        return {
            "db_state": None,
            "error": {
                "category": error.category,
                "code": error.code,
                "message_is_contract": False,
            },
            "id": contract_id,
            "metrics": normalize(error.metrics),
            "phase": phase,
            "result": None,
            "status": "observed",
        }
    raise AssertionError("failure scenario unexpectedly loaded definitions")


def _auto_field() -> dict[str, Any]:
    return {
        "column": "id",
        "default": None,
        "go_name": "ID",
        "kind": "auto",
        "max_length": 0,
        "name": "id",
        "nullable": False,
        "primary_key": True,
    }


def _char_field(
    name: str,
    go_name: str,
    *,
    max_length: int,
    nullable: bool = False,
    default: str | None = None,
) -> dict[str, Any]:
    return {
        "column": name,
        "default": (
            None if default is None else {"kind": "string", "string": default}
        ),
        "go_name": go_name,
        "kind": "char",
        "max_length": max_length,
        "name": name,
        "nullable": nullable,
        "primary_key": False,
    }


def _boolean_field(
    name: str,
    go_name: str,
    *,
    default: bool | None = None,
) -> dict[str, Any]:
    return {
        "column": name,
        "default": (
            None
            if default is None
            else {"boolean": default, "kind": "boolean"}
        ),
        "go_name": go_name,
        "kind": "boolean",
        "max_length": 0,
        "name": name,
        "nullable": False,
        "primary_key": False,
    }


def _root_document() -> dict[str, Any]:
    return {
        "compatibility": deepcopy(_COMPATIBILITY),
        "migration": {
            "app": "alpha",
            "dependencies": [],
            "name": "0001_initial",
            "operations": [
                {
                    "app_label": "alpha",
                    "kind": "create_model",
                    "model": {
                        "db_table": "godj_definition_alpha_entry",
                        "fields": [
                            _auto_field(),
                            _char_field(
                                "title",
                                "Title",
                                max_length=64,
                                default="untitled",
                            ),
                        ],
                        "go_name": "Entry",
                        "name": "entry",
                    },
                }
            ],
        },
        "producer": {"name": "godj-reference", "version": "0.1.0"},
    }


def _tail_document() -> dict[str, Any]:
    return {
        "compatibility": deepcopy(_COMPATIBILITY),
        "migration": {
            "app": "alpha",
            "dependencies": [{"app": "alpha", "name": "0001_initial"}],
            "name": "0002_fields",
            "operations": [
                {
                    "app_label": "alpha",
                    "field": _boolean_field(
                        "published",
                        "Published",
                        default=False,
                    ),
                    "kind": "add_field",
                    "model_name": "entry",
                },
                {
                    "app_label": "alpha",
                    "field": _char_field(
                        "summary",
                        "Summary",
                        max_length=255,
                        nullable=True,
                    ),
                    "kind": "add_field",
                    "model_name": "entry",
                },
            ],
        },
        "producer": {"name": "godj-reference", "version": "0.1.0"},
    }


def _encode_document(
    value: dict[str, Any],
    *,
    pretty: bool = False,
    sort_keys: bool = False,
) -> bytes:
    if pretty:
        rendered = json.dumps(
            value,
            ensure_ascii=False,
            indent=2,
            sort_keys=sort_keys,
        )
    else:
        rendered = json.dumps(
            value,
            ensure_ascii=False,
            separators=(",", ":"),
            sort_keys=sort_keys,
        )
    return rendered.encode("utf-8")


def _fixture_sources() -> tuple[SourceDocument, SourceDocument]:
    root = SourceDocument("opaque-z-root", _encode_document(_root_document()))
    tail = SourceDocument(
        "opaque-a-tail",
        _encode_document(_tail_document(), pretty=True, sort_keys=True),
    )
    return (tail, root)


def canonical_batch(contract_id: str) -> dict[str, Any]:
    loaded, metrics = _load(_fixture_sources())
    return _success_observation(
        contract_id,
        "construction",
        _success_result(loaded),
        metrics,
    )


def empty_source(contract_id: str) -> dict[str, Any]:
    loaded, metrics = _load(())
    return _success_observation(
        contract_id,
        "construction",
        _success_result(loaded),
        metrics,
    )


def canonical_syntax_and_order(contract_id: str) -> dict[str, Any]:
    baseline, metrics = _load(_fixture_sources())

    root = _root_document()
    tail = _tail_document()
    root["producer"]["version"] = "9.9.9"
    equivalent, _equivalent_metrics = _load(
        (
            SourceDocument(
                "relabel-b",
                _encode_document(root, pretty=True, sort_keys=True),
            ),
            SourceDocument(
                "relabel-a",
                _encode_document(tail, sort_keys=False),
            ),
        )
    )

    reordered_tail = _tail_document()
    reordered_tail["migration"]["operations"].reverse()
    changed, _changed_metrics = _load(
        (
            SourceDocument("changed-root", _encode_document(_root_document())),
            SourceDocument("changed-tail", _encode_document(reordered_tail)),
        )
    )
    result = _success_result(baseline)
    result["canonicality"] = {
        "equivalent_definition_set": (
            baseline.definitions == equivalent.definitions
        ),
        "equivalent_digest": equivalent.digest,
        "operation_order_changed_digest": changed.digest,
        "operation_order_is_semantic": changed.digest != baseline.digest,
        "source_relabel_preserved_digest": equivalent.digest == baseline.digest,
    }
    return _success_observation(
        contract_id,
        "construction",
        result,
        metrics,
    )


def incompatible_tuple(contract_id: str) -> dict[str, Any]:
    format_mismatch = _root_document()
    format_mismatch["compatibility"]["definition_format"] = 2
    return _failure_observation(
        contract_id,
        "environment",
        (SourceDocument("a-version", _encode_document(format_mismatch)),),
    )


def malformed_atomic_batch(contract_id: str) -> dict[str, Any]:
    duplicate = _encode_document(_tail_document())
    duplicate = duplicate.replace(
        b'"name":"0002_fields"',
        b'"name":"0002_fields","name":"shadow"',
        1,
    )
    return _failure_observation(
        contract_id,
        "construction",
        (
            SourceDocument("a-valid", _encode_document(_root_document())),
            SourceDocument("b-invalid", duplicate),
        ),
    )


def duplicate_identity(contract_id: str) -> dict[str, Any]:
    first = _root_document()
    second = _root_document()
    second["producer"]["version"] = "2.0.0"
    second["migration"]["operations"][0]["model"]["fields"][1][
        "default"
    ] = {"kind": "string", "string": "other"}
    return _failure_observation(
        contract_id,
        "construction",
        (
            SourceDocument("z-duplicate", _encode_document(second)),
            SourceDocument("a-original", _encode_document(first)),
        ),
    )


def closed_codec(contract_id: str) -> dict[str, Any]:
    unsupported = _root_document()
    unsupported["migration"]["operations"] = [
        {"app_label": "alpha", "kind": "run_python"}
    ]
    return _failure_observation(
        contract_id,
        "construction",
        (SourceDocument("a-operation", _encode_document(unsupported)),),
    )


def _django_field(value: dict[str, Any]) -> models.Field[Any, Any]:
    default = value["default"]
    kwargs: dict[str, Any] = {
        "db_column": value["column"],
        "null": value["nullable"],
        "primary_key": value["primary_key"],
    }
    if default is not None:
        kwargs["default"] = default[default["kind"]]
    if value["kind"] == "auto":
        return models.AutoField(**kwargs)
    if value["kind"] == "char":
        return models.CharField(max_length=value["max_length"], **kwargs)
    if value["kind"] == "boolean":
        return models.BooleanField(**kwargs)
    raise AssertionError("validated definition carried an unknown field kind")


def _django_migrations(
    definitions: Sequence[dict[str, Any]],
) -> tuple[tuple[NodeKey, Migration], ...]:
    migrations: list[tuple[NodeKey, Migration]] = []
    for definition in definitions:
        migration = Migration(definition["name"], definition["app"])
        migration.dependencies = [
            (dependency["app"], dependency["name"])
            for dependency in definition["dependencies"]
        ]
        operations = []
        for operation in definition["operations"]:
            if operation["kind"] == "create_model":
                model = operation["model"]
                operations.append(
                    CreateModel(
                        name=model["go_name"],
                        fields=[
                            (field["name"], _django_field(field))
                            for field in model["fields"]
                        ],
                        options={"db_table": model["db_table"]},
                    )
                )
            elif operation["kind"] == "add_field":
                field = operation["field"]
                operations.append(
                    AddField(
                        model_name=operation["model_name"],
                        name=field["name"],
                        field=_django_field(field),
                    )
                )
            else:
                raise AssertionError("validated operation kind changed")
        migration.operations = operations
        migrations.append(((definition["app"], definition["name"]), migration))
    return tuple(migrations)


class _ExplicitMigrationLoader(MigrationLoader):
    def __init__(
        self,
        database_connection: Any,
        entries: Sequence[tuple[NodeKey, Migration]],
    ) -> None:
        self._explicit_entries = tuple(entries)
        super().__init__(database_connection, load=False)
        self.build_graph()

    def load_disk(self) -> None:
        self.disk_migrations = dict(self._explicit_entries)
        self.unmigrated_apps = set()
        self.migrated_apps = {
            key[0] for key, _migration in self._explicit_entries
        }


def _public_executor(
    database_connection: Any,
    entries: Sequence[tuple[NodeKey, Migration]],
) -> MigrationExecutor:
    frozen_entries = tuple(entries)

    class BoundExplicitLoader(_ExplicitMigrationLoader):
        def __init__(self, connection: Any) -> None:
            super().__init__(connection, frozen_entries)

    with patch.object(executor_module, "MigrationLoader", BoundExplicitLoader):
        return MigrationExecutor(database_connection)


@contextmanager
def _isolated_database() -> Iterator[Any]:
    if _DATABASE_ALIAS in connections.databases:
        raise AssertionError("definition-source database alias already exists")
    with tempfile.TemporaryDirectory(prefix="godj-definition-source-") as directory:
        configuration = dict(connections.databases["default"])
        configuration["NAME"] = str(Path(directory) / "handoff.sqlite3")
        connections.databases[_DATABASE_ALIAS] = configuration
        database_connection = connections[_DATABASE_ALIAS]
        try:
            if database_connection.introspection.table_names():
                raise AssertionError("definition-source database did not start empty")
            yield database_connection
        finally:
            if database_connection.in_atomic_block:
                raise AssertionError("definition-source scenario leaked transaction")
            database_connection.close()
            del connections[_DATABASE_ALIAS]
            del connections.databases[_DATABASE_ALIAS]


def _database_snapshot(database_connection: Any) -> dict[str, Any]:
    recorder = MigrationRecorder(database_connection)
    recorder_present = recorder.has_table()
    records = (
        [
            {"app": app, "name": name}
            for app, name in sorted(recorder.applied_migrations())
        ]
        if recorder_present
        else []
    )
    managed_schema = []
    for table_name in sorted(database_connection.introspection.table_names()):
        if not table_name.startswith(_TABLE_PREFIX):
            continue
        with database_connection.cursor() as cursor:
            description = database_connection.introspection.get_table_description(
                cursor,
                table_name,
            )
        managed_schema.append(
            {
                "columns": [
                    {
                        "name": column.name,
                        "nullable": column.null_ok,
                        "primary_key": column.pk,
                    }
                    for column in sorted(description, key=lambda item: item.name)
                ],
                "name": table_name,
            }
        )
    return {
        "managed_schema": managed_schema,
        "migration_records": records,
        "recorder_present": recorder_present,
    }


def _project_state_value(state: ProjectState) -> dict[str, Any]:
    return {
        "models": [
            {
                "app": app,
                "db_table": model.options.get("db_table"),
                "fields": list(model.fields),
                "name": name,
            }
            for (app, name), model in sorted(state.models.items())
        ]
    }


def public_lifecycle_handoff(contract_id: str) -> dict[str, Any]:
    loaded, metrics = _load(_fixture_sources())
    frozen_digest = loaded.digest
    frozen_definitions = deepcopy(list(loaded.definitions))
    entries = _django_migrations(frozen_definitions)

    with _isolated_database() as database_connection:
        before = _database_snapshot(database_connection)
        executor = _public_executor(database_connection, entries)
        executor.loader.check_consistent_history(database_connection)
        targets = list(executor.loader.graph.leaf_nodes())
        plan = executor.migration_plan(targets)
        metrics["handoff_calls"] += 1
        state = executor.migrate(targets, plan=plan)
        after = _database_snapshot(database_connection)

    if metrics["handoff_calls"] != 1:
        raise AssertionError("loaded definition set was not handed off exactly once")
    if loaded.digest != frozen_digest or list(loaded.definitions) != frozen_definitions:
        raise AssertionError("handoff mutated the loaded definition snapshot")

    result = _success_result(loaded, attempted=True, calls=1)
    result["lifecycle"] = {
        "plan": [
            {
                "app": migration.app_label,
                "direction": "backward" if backwards else "forward",
                "name": migration.name,
            }
            for migration, backwards in plan
        ],
        "returned_state": _project_state_value(state),
        "targets": [{"app": app, "name": name} for app, name in targets],
    }
    metrics["handoff"] = {
        "definitions_unchanged": True,
        "digest": frozen_digest,
        "graph_node_count": len(executor.loader.graph.nodes),
        "plan_step_count": len(plan),
        "route": "explicit_graph_public_executor",
    }
    return _success_observation(
        contract_id,
        "commit",
        result,
        {**metrics, "source_reads_after_snapshot": 0},
        db_state={"after": after, "before": before},
    )


SCENARIOS = {
    "godj.migration.definition_source.canonical_batch": canonical_batch,
    "godj.migration.definition_source.empty_source": empty_source,
    (
        "godj.migration.definition_source.canonical_syntax_and_order"
    ): canonical_syntax_and_order,
    "godj.migration.definition_source.incompatible_tuple": incompatible_tuple,
    (
        "godj.migration.definition_source.malformed_atomic_batch"
    ): malformed_atomic_batch,
    "godj.migration.definition_source.duplicate_identity": duplicate_identity,
    "godj.migration.definition_source.closed_codec": closed_codec,
    (
        "django.migration.definition_source.public_lifecycle_handoff"
    ): public_lifecycle_handoff,
}
