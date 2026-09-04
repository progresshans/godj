"""Pinned migration-target plan observations owned by Django's real planner.

Only the ordered, portable plan result is published here. Database snapshots
and mutation counters remain implementation evidence of the reused planning
helper rather than part of MIG-120..122's comparison surface.
"""

from __future__ import annotations

from collections.abc import Callable, Sequence
from typing import Any

from . import migration_planning_scenarios as planning
from .normalizer import normalize


SET_SLUG = "migration-target-plan"


def _observed(contract_id: str, case: dict[str, Any]) -> dict[str, Any]:
    return {
        "db_state": None,
        "error": None,
        "id": contract_id,
        "metrics": None,
        "phase": "evaluation",
        "result": normalize(case),
        "status": "observed",
    }


def _plan_result(
    contract_id: str,
    *,
    name: str,
    nodes: Sequence[planning.NodeKey],
    dependencies: Sequence[planning.Dependency],
    target: planning.Target,
    applied: Sequence[planning.NodeKey],
) -> dict[str, Any]:
    case, _database_state, _metrics = planning._plan_case(
        name,
        nodes,
        dependencies,
        [target],
        applied,
    )
    return _observed(contract_id, case)


_A1 = ("alpha", "0001_initial")
_A2 = ("alpha", "0002_second")
_A3 = ("alpha", "0003_third")
_B1 = ("beta", "0001_direct_dependent")
_C1 = ("charlie", "0001_descendant_dependent")
_G1 = ("gamma", "0001_unrelated")

_BRANCHED_NODES = (_A1, _A2, _A3, _B1, _G1)
_BRANCHED_DEPENDENCIES = ((_A2, _A1), (_A3, _A2), (_B1, _A1))
_NAMED_REVERSE_NODES = (_A1, _A2, _A3, _B1, _C1, _G1)
_NAMED_REVERSE_DEPENDENCIES = (
    (_A2, _A1),
    (_A3, _A2),
    (_B1, _A1),
    (_C1, _A3),
)


def named_forward_closure(contract_id: str) -> dict[str, Any]:
    return _plan_result(
        contract_id,
        name="named_forward_closure",
        nodes=_BRANCHED_NODES,
        dependencies=_BRANCHED_DEPENDENCIES,
        target=_A3,
        applied=(),
    )


def named_reverse_descendants(contract_id: str) -> dict[str, Any]:
    return _plan_result(
        contract_id,
        name="named_reverse_descendants",
        nodes=_NAMED_REVERSE_NODES,
        dependencies=_NAMED_REVERSE_DEPENDENCIES,
        target=_A1,
        applied=_NAMED_REVERSE_NODES,
    )


def app_zero_cross_app_dependents(contract_id: str) -> dict[str, Any]:
    # Preserve Django's exact B1, A3, A2, A1 traversal. B1 and A3 are
    # incomparable reverse siblings; sorting this result would hide DEV-0002.
    return _plan_result(
        contract_id,
        name="app_zero_cross_app_dependents",
        nodes=_BRANCHED_NODES,
        dependencies=_BRANCHED_DEPENDENCIES,
        target=("alpha", None),
        applied=_BRANCHED_NODES,
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "django.migration.target_plan.named_forward_closure": named_forward_closure,
    "django.migration.target_plan.named_reverse_descendants": (
        named_reverse_descendants
    ),
    "django.migration.target_plan.app_zero_cross_app_dependents": (
        app_zero_cross_app_dependents
    ),
}
