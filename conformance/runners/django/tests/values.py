"""Fresh decoding helpers for reference tests, independent of actual generators."""
from typing import Any


def denormalize(value):
    value_type = value["type"]
    if value_type == "null":
        return None
    if value_type in {"bool", "string"}:
        return value["value"]
    if value_type == "int":
        return int(value["value"])
    if value_type == "list":
        return [denormalize(item) for item in value["items"]]
    if value_type == "object":
        return {
            field["name"]: denormalize(field["value"])
            for field in value["fields"]
        }
    raise AssertionError(f"unexpected normalized value type: {value_type!r}")


def semantic(value: Any) -> Any:
    if value is None or not isinstance(value, dict) or "type" not in value:
        return value
    kind = value["type"]
    if kind == "object":
        return {
            field["name"]: semantic(field["value"])
            for field in value["fields"]
        }
    if kind == "list":
        return [semantic(item) for item in value["items"]]
    if kind == "null":
        return None
    if kind == "int":
        return int(value["value"])
    return value["value"]


def observed(scenario, contract_id):
    observation = scenario(contract_id)
    return {
        "raw": observation,
        "result": (
            denormalize(observation["result"])
            if observation["result"] is not None
            else None
        ),
        "db": denormalize(observation["db_state"]),
        "metrics": denormalize(observation["metrics"]),
    }


def decode_primary_keys(value: dict[str, Any]) -> Any:
    kind = value["type"]
    if kind == "null":
        return None
    if kind == "bool":
        return value["value"]
    if kind == "int":
        return int(value["value"])
    if kind == "string":
        return value["value"]
    if kind == "pk":
        return decode_primary_keys(value["value"])
    if kind == "list":
        return [decode_primary_keys(item) for item in value["items"]]
    if kind == "object":
        return {
            field["name"]: decode_primary_keys(field["value"])
            for field in value["fields"]
        }
    raise AssertionError(f"unsupported primary-key test value kind {kind!r}")
