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


def _sql_tokens(sql: str) -> list[str]:
    """Return unquoted SQL words for the small conformance shape parser.

    The relation corpus compares statement and join *shape*, never raw SQL.
    Quoted identifiers, string literals, and comments therefore must not be
    able to introduce apparent JOIN keywords into the observation.
    """

    if not isinstance(sql, str):
        raise TypeError("SQL must be a string")

    tokens: list[str] = []
    index = 0
    length = len(sql)
    while index < length:
        character = sql[index]

        if character in {"'", '"', "`"}:
            quote = character
            index += 1
            while index < length:
                if sql[index] == quote:
                    if index + 1 < length and sql[index + 1] == quote:
                        index += 2
                        continue
                    index += 1
                    break
                index += 1
            continue

        if character == "[":
            index += 1
            while index < length:
                if sql[index] == "]":
                    if index + 1 < length and sql[index + 1] == "]":
                        index += 2
                        continue
                    index += 1
                    break
                index += 1
            continue

        if character == "-" and index + 1 < length and sql[index + 1] == "-":
            newline = sql.find("\n", index + 2)
            index = length if newline == -1 else newline + 1
            continue

        if character == "/" and index + 1 < length and sql[index + 1] == "*":
            terminator = sql.find("*/", index + 2)
            index = length if terminator == -1 else terminator + 2
            continue

        if character.isalpha() or character == "_":
            end = index + 1
            while end < length and (sql[end].isalnum() or sql[end] == "_"):
                end += 1
            tokens.append(sql[index:end].upper())
            index = end
            continue

        index += 1

    return tokens


def normalize_sql_shape(sql: str) -> dict[str, Any]:
    """Normalize one SQL statement to its statement kind and ordered joins."""

    tokens = _sql_tokens(sql)
    if not tokens:
        return {"statement_kind": "EMPTY", "join_kinds": []}

    join_kinds: list[str] = []
    for index, token in enumerate(tokens):
        if token != "JOIN":
            continue
        previous = tokens[index - 1] if index >= 1 else ""
        before_previous = tokens[index - 2] if index >= 2 else ""
        if previous == "OUTER" and before_previous in {"LEFT", "RIGHT", "FULL"}:
            join_kinds.append(f"{before_previous}_OUTER")
        elif previous in {"LEFT", "RIGHT", "FULL"}:
            join_kinds.append(f"{previous}_OUTER")
        elif previous in {"CROSS", "NATURAL"}:
            join_kinds.append(previous)
        else:
            # SQL's unqualified JOIN and explicit INNER JOIN are equivalent
            # for the relation shape comparison.
            join_kinds.append("INNER")

    return {
        "statement_kind": tokens[0],
        "join_kinds": join_kinds,
    }


def normalize_sql_in_predicate_columns(sql: str) -> list[str]:
    """Return ordered identifier names immediately preceding SQL IN.

    This is intentionally narrower than a SQL parser. It strips string
    literals and comments, preserves quoted or unquoted identifiers, and lets
    the relation corpus lock the semantic ForeignKey batch column without
    comparing a backend's full SQL text, aliases, or placeholder spelling.
    """

    if not isinstance(sql, str):
        raise TypeError("SQL must be a string")

    tokens: list[str] = []
    index = 0
    length = len(sql)
    while index < length:
        character = sql[index]

        if character == "'":
            index += 1
            while index < length:
                if sql[index] == "'":
                    if index + 1 < length and sql[index + 1] == "'":
                        index += 2
                        continue
                    index += 1
                    break
                index += 1
            continue

        if character in {'"', "`"}:
            quote = character
            index += 1
            value: list[str] = []
            while index < length:
                if sql[index] == quote:
                    if index + 1 < length and sql[index + 1] == quote:
                        value.append(quote)
                        index += 2
                        continue
                    index += 1
                    break
                value.append(sql[index])
                index += 1
            tokens.append("".join(value).upper())
            continue

        if character == "[":
            index += 1
            value = []
            while index < length:
                if sql[index] == "]":
                    if index + 1 < length and sql[index + 1] == "]":
                        value.append("]")
                        index += 2
                        continue
                    index += 1
                    break
                value.append(sql[index])
                index += 1
            tokens.append("".join(value).upper())
            continue

        if character == "-" and index + 1 < length and sql[index + 1] == "-":
            newline = sql.find("\n", index + 2)
            index = length if newline == -1 else newline + 1
            continue

        if character == "/" and index + 1 < length and sql[index + 1] == "*":
            terminator = sql.find("*/", index + 2)
            index = length if terminator == -1 else terminator + 2
            continue

        if character.isalpha() or character == "_":
            end = index + 1
            while end < length and (sql[end].isalnum() or sql[end] == "_"):
                end += 1
            tokens.append(sql[index:end].upper())
            index = end
            continue

        index += 1

    return [
        tokens[index - 1].lower()
        for index, token in enumerate(tokens)
        if token == "IN" and index > 0
    ]


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
