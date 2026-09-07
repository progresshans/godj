"""Observed-value formatting, independent of checked-in reference bytes."""

from typing import Any

from .normalizer import PrimaryKey, normalize


def observed(
    contract_id: str,
    result: Any,
    *,
    phase: str = "evaluation",
    db_state: Any | None = None,
    metrics: Any | None = None,
) -> dict[str, Any]:
    return {
        "db_state": normalize(db_state) if db_state is not None else None,
        "error": None,
        "id": contract_id,
        "metrics": normalize(metrics) if metrics is not None else None,
        "phase": phase,
        "result": normalize(result),
        "status": "observed",
    }


def observed_command(
    contract_id: str,
    *,
    phase: str,
    result: Any,
    db_state: Any | None = None,
    metrics: Any | None = None,
) -> dict[str, Any]:
    return {
        "db_state": normalize(db_state) if db_state is not None else None,
        "error": None,
        "id": contract_id,
        "metrics": normalize(metrics) if metrics is not None else None,
        "phase": phase,
        "result": normalize(result),
        "status": "observed",
    }


def observed_state(
    contract_id: str,
    result: Any,
    *,
    phase: str,
    db_state: Any,
    metrics: Any,
) -> dict[str, Any]:
    return {
        "db_state": normalize(db_state),
        "error": None,
        "id": contract_id,
        "metrics": normalize(metrics),
        "phase": phase,
        "result": normalize(result),
        "status": "observed",
    }


def observed_writer(
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


def article_rows(model: Any) -> list[dict[str, Any]]:
    return [
        {
            "id": PrimaryKey(primary_key),
            "published": published,
            "summary": summary,
            "title": title,
        }
        for primary_key, title, published, summary in model.objects.order_by(
            "id"
        ).values_list("id", "title", "published", "summary")
    ]
