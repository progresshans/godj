"""Canonical, type-preserving values for differential observations."""

from __future__ import annotations

import base64
import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal
from typing import Any
from uuid import UUID


@dataclass(frozen=True)
class PrimaryKey:
    """Mark a scalar value as a model primary key."""

    value: Any


def normalize(value: Any) -> dict[str, Any]:
    """Convert a supported Python value to the protocol's tagged value algebra."""

    if isinstance(value, PrimaryKey):
        normalized = normalize(value.value)
        if normalized["type"] not in {
            "bool",
            "bytes",
            "datetime",
            "decimal",
            "int",
            "string",
            "uuid",
        }:
            raise TypeError("primary keys must normalize to one non-null scalar")
        return {"type": "pk", "value": normalized}
    if value is None:
        return {"type": "null"}
    if isinstance(value, bool):
        return {"type": "bool", "value": value}
    if isinstance(value, int):
        return {"type": "int", "value": str(value)}
    if isinstance(value, str):
        return {"type": "string", "value": value}
    if isinstance(value, Decimal):
        if not value.is_finite():
            raise ValueError("non-finite decimals are not canonical")
        return {"type": "decimal", "value": format(value, "f")}
    if isinstance(value, datetime):
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("naive datetimes are not canonical")
        rendered = value.astimezone(timezone.utc).isoformat(
            timespec="microseconds"
        )
        return {"type": "datetime", "value": rendered.replace("+00:00", "Z")}
    if isinstance(value, UUID):
        return {"type": "uuid", "value": str(value)}
    if isinstance(value, bytes):
        encoded = base64.b64encode(value).decode("ascii")
        return {"type": "bytes", "encoding": "base64", "value": encoded}
    if isinstance(value, Mapping):
        if any(not isinstance(key, str) for key in value):
            raise TypeError("object keys must be strings")
        fields = [
            {"name": key, "value": normalize(value[key])}
            for key in sorted(value)
        ]
        return {"type": "object", "fields": fields}
    if isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
        return {"type": "list", "items": [normalize(item) for item in value]}
    raise TypeError(f"unsupported observation value: {type(value).__qualname__}")


def canonical_json(value: Any) -> bytes:
    """Encode JSON with deterministic keys, separators, UTF-8, and a final newline."""

    rendered = json.dumps(
        value,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
        allow_nan=False,
    )
    return (rendered + "\n").encode("utf-8")
