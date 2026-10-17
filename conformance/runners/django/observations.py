"""Observed-value formatting, independent of checked-in reference bytes."""

from typing import Any

from .normalizer import PrimaryKey, normalize


_ABSENT = object()


def observed(
    contract_id: str,
    result: Any = _ABSENT,
    *,
    phase: str = "evaluation",
    error: dict[str, Any] | None = None,
    db_state: Any | None = None,
    metrics: Any | None = None,
) -> dict[str, Any]:
    return {
        "db_state": normalize(db_state) if db_state is not None else None,
        "error": error,
        "id": contract_id,
        "metrics": normalize(metrics) if metrics is not None else None,
        "phase": phase,
        "result": None if result is _ABSENT else normalize(result),
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
