"""Independent decisions for the bounded GoDj migration command.

This module does not import or execute Django and does not read checked-in
manifests, oracles, or product fixtures.  It records the GoDj-only command,
preflight, transaction, cleanup, concurrency, and secret boundaries proposed
by GDJ-0049 and ADR-0051.
"""

from __future__ import annotations

from collections.abc import Callable, Mapping
from copy import deepcopy
from typing import Any

from .normalizer import normalize


def _observed(
    contract_id: str,
    result: Any,
    *,
    phase: str,
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
        "result": normalize(result) if result is not None else None,
        "status": "observed",
    }


def _error(category: str, code: str) -> dict[str, Any]:
    return {
        "category": category,
        "code": code,
        "message_is_contract": False,
    }


def fresh_latest(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "definition_snapshot": "loaded_once_before_backend_open",
            "history_digest_matches_loaded": True,
            "outcome": "latest",
            "target": "latest_leaves",
        },
        phase="commit",
        db_state={
            "history": "exact_latest",
            "schema": "exact_latest",
        },
        metrics={
            "backend_closes": 1,
            "backend_opens": 1,
            "definition_loads": 1,
            "migrate_calls": 1,
        },
    )


def applied_prefix_tail(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "committed_prefix_preserved": True,
            "outcome": "latest",
            "prefix_reapplied": False,
            "remaining_tail_applied": True,
        },
        phase="commit",
        db_state={
            "duplicate_history_rows": 0,
            "history": "exact_latest",
            "schema": "exact_latest",
        },
        metrics={
            "backend_closes": 1,
            "backend_opens": 1,
            "definition_loads": 1,
            "migrate_calls": 1,
            "prefix_writes": 0,
        },
    )


def fully_applied_fresh_noop(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "fresh_process": True,
            "history_digest_matches_loaded": True,
            "outcome": "no_op",
        },
        phase="commit",
        db_state={
            "duplicate_history_rows": 0,
            "history": "unchanged_latest",
            "schema": "unchanged_latest",
        },
        metrics={
            "backend_closes": 1,
            "backend_opens": 1,
            "definition_loads": 1,
            "history_writes": 0,
            "migrate_calls": 1,
            "schema_mutations": 0,
        },
    )


def definition_preflight_before_backend(contract_id: str) -> dict[str, Any]:
    cases = [
        {
            "backend_opens": 0,
            "case": "invalid_definition_document",
            "category": "migration_definition_source_error",
            "code": "invalid_definition_document",
        },
        {
            "backend_opens": 0,
            "case": "unknown_current_format",
            "category": "migration_definition_source_error",
            "code": "definition_format_incompatible",
        },
    ]
    return _observed(
        contract_id,
        {
            "cases": cases,
            "causes_published": False,
            "definition_publication": "atomic",
        },
        phase="evaluation",
        metrics={
            "backend_opens": 0,
            "cases": len(cases),
            "definition_loads": len(cases),
            "migrate_calls": 0,
        },
    )


def inconsistent_history_preflight(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        None,
        phase="evaluation",
        error=_error(
            "migration_history_error", "inconsistent_applied_history"
        ),
        db_state={
            "history": "preserved_inconsistent",
            "reconciliation_required": True,
            "schema": "unchanged",
        },
        metrics={
            "backend_closes": 1,
            "backend_opens": 1,
            "migration_begins": 0,
            "recorder_writes": 0,
            "schema_mutations": 0,
        },
    )


def capability_preflight_before_begin(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        None,
        phase="evaluation",
        error=_error(
            "migration_capability_error", "unsupported_operation"
        ),
        db_state={"history": "unchanged", "schema": "unchanged"},
        metrics={
            "backend_closes": 1,
            "backend_opens": 1,
            "migration_begins": 0,
            "recorder_writes": 0,
            "schema_mutations": 0,
            "unsupported_operation_ignored": False,
        },
    )


def middle_failure_durable_prefix(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        None,
        phase="rollback",
        error=_error("migration_execution_error", "operation_failed"),
        db_state={
            "committed_prefix_preserved": True,
            "current_step": "rolled_back",
            "current_step_effects": "absent",
            "history": "exact_committed_prefix",
            "schema": "exact_committed_prefix",
            "tail_executed": False,
        },
        metrics={
            "automatic_retries": 0,
            "rollbacks": 1,
            "tail_migration_begins": 0,
        },
    )


def fresh_resume_after_failure(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "committed_prefix_reapplied": False,
            "fresh_process": True,
            "outcome": "latest",
            "resume_point": "failed_migration",
        },
        phase="commit",
        db_state={
            "duplicate_history_rows": 0,
            "history": "exact_latest",
            "schema": "exact_latest",
        },
        metrics={
            "automatic_retries": 0,
            "fresh_invocations": 1,
            "prefix_writes": 0,
        },
    )


def commit_outcome_unknown(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        None,
        phase="commit",
        error=_error(
            "migration_transaction_error", "commit_outcome_unknown"
        ),
        db_state={
            "history": "unknown",
            "reconciliation_required": True,
            "reported_success": False,
            "schema": "unknown",
            "verified_commit": False,
        },
        metrics={
            "automatic_retries": 0,
            "rollback_after_unknown": 0,
            "success_publications": 0,
        },
    )


def concurrent_latest_fenced(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "corrupt_history": False,
            "duplicate_history": False,
            "fresh_reconciliation": "latest",
            "outcome": "fenced",
        },
        phase="commit",
        db_state={
            "history": "exact_latest",
            "schema": "exact_latest",
        },
        metrics={
            "automatic_retries": 0,
            "child_processes": 2,
            "corrupt_history_rows": 0,
            "duplicate_history_rows": 0,
            "fresh_reconciliation_invocations": 1,
        },
    )


def backend_configuration_secret_boundary(contract_id: str) -> dict[str, Any]:
    cases = [
        {
            "case": "missing_configuration",
            "category": "migration_backend_error",
            "code": "backend_open_failed",
            "exit": "nonzero",
        },
        {
            "case": "invalid_configuration",
            "category": "migration_backend_error",
            "code": "backend_open_failed",
            "exit": "nonzero",
        },
        {
            "case": "typed_nil_backend",
            "category": "migration_backend_error",
            "code": "invalid_backend",
            "exit": "nonzero",
        },
    ]
    return _observed(
        contract_id,
        {
            "cases": cases,
            "global_cli_parses_secret": False,
            "raw_causes_published": False,
        },
        phase="environment",
        metrics={
            "artifact_secret_occurrences": 0,
            "cases": len(cases),
            "protocol_secret_occurrences": 0,
            "stderr_secret_occurrences": 0,
            "stdout_secret_occurrences": 0,
        },
    )


def interrupt_rollback_cleanup(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        None,
        phase="rollback",
        error=_error(
            "migration_project_process_error", "project_interrupted"
        ),
        db_state={
            "backend_close": "completed",
            "child_reap": "direct",
            "current_step_effects": "absent",
            "force_kill": False,
            "history": "exact_committed_prefix",
            "process_residue": 0,
            "rollback": "completed",
            "session_close": "completed",
        },
        metrics={
            "backend_close_calls": 1,
            "child_reaps": 1,
            "force_kills": 0,
            "rollback_calls": 1,
            "session_close_calls": 1,
            "signal_context_cancellations": 1,
        },
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "godj.migration.command.fresh_latest": fresh_latest,
    "godj.migration.command.applied_prefix_tail": applied_prefix_tail,
    "godj.migration.command.fully_applied_fresh_noop": fully_applied_fresh_noop,
    "godj.migration.command.definition_preflight_before_backend": (
        definition_preflight_before_backend
    ),
    "godj.migration.command.inconsistent_history_preflight": (
        inconsistent_history_preflight
    ),
    "godj.migration.command.capability_preflight_before_begin": (
        capability_preflight_before_begin
    ),
    "godj.migration.command.middle_failure_durable_prefix": (
        middle_failure_durable_prefix
    ),
    "godj.migration.command.fresh_resume_after_failure": fresh_resume_after_failure,
    "godj.migration.command.commit_outcome_unknown": commit_outcome_unknown,
    "godj.migration.command.concurrent_latest_fenced": concurrent_latest_fenced,
    "godj.migration.command.backend_configuration_secret_boundary": (
        backend_configuration_secret_boundary
    ),
    "godj.migration.command.interrupt_rollback_cleanup": interrupt_rollback_cleanup,
}


def generate_suite(profile: Mapping[str, Any]) -> dict[str, Any]:
    """Return a fresh ordered suite for an externally supplied profile snapshot."""

    return {
        "format_version": 2,
        "profile": deepcopy(dict(profile)),
        "contracts": [
            scenario(f"MIG-{number:03d}")
            for number, scenario in zip(range(87, 99), SCENARIOS.values())
        ],
    }
