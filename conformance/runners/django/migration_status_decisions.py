"""Independent GoDj decisions for the bounded migration status command.

These decisions intentionally do not import Django and do not read checked-in
manifests, oracles, or product fixtures.  They record only the GoDj-owned
global-empty, fail-visible unknown history, fail-closed consistency, and exact
project cleanup/redaction boundaries proposed by GDJ-0051 and ADR-0053.
"""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from .normalizer import normalize


def _error(category: str, code: str) -> dict[str, Any]:
    return {
        "category": category,
        "code": code,
        "message_is_contract": False,
    }


def _observed(
    contract_id: str,
    *,
    phase: str,
    result: Any | None,
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


def _read_only_state(history: str) -> dict[str, Any]:
    return {
        "application_mutations": 0,
        "history": history,
        "recorder_mutations": 0,
        "revision_mutations": 0,
        "schema_mutations": 0,
    }


def _read_metrics(*, stdout_writes: int) -> dict[str, Any]:
    return {
        "application_mutations": 0,
        "applied_history_reads": 1,
        "backend_closes": 1,
        "backend_opens": 1,
        "recorder_mutations": 0,
        "revision_mutations": 0,
        "revision_session_closes": 1,
        "revision_session_opens": 1,
        "schema_mutations": 0,
        "stdout_writes": stdout_writes,
    }


def empty_catalog(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        phase="evaluation",
        result={
            "point_in_time_snapshot": True,
            "rows": [],
            "stdout": "(no migrations)\n",
        },
        db_state=_read_only_state("empty"),
        metrics=_read_metrics(stdout_writes=1),
    )


def unknown_record_visible(contract_id: str) -> dict[str, Any]:
    recorded_unknown_input_order = [
        {"app": "blog", "name": "9999_removed"},
        {"app": "blog", "name": "0000_removed"},
        {"app": "legacy", "name": "0001_gone"},
    ]
    rows = [
        {"app": "authors", "name": "0001_author", "status": "applied"},
        {"app": "blog", "name": "0001_article", "status": "applied"},
        {"app": "blog", "name": "0002_publish", "status": "unapplied"},
        {"app": "blog", "name": "0000_removed", "status": "unknown"},
        {"app": "blog", "name": "9999_removed", "status": "unknown"},
        {"app": "legacy", "name": "0001_gone", "status": "unknown"},
    ]
    return _observed(
        contract_id,
        phase="evaluation",
        result={
            "known_rows_preserved": True,
            "recorded_unknown_input_order": recorded_unknown_input_order,
            "rows": rows,
            "stdout": (
                "authors\n"
                " [X] 0001_author\n"
                "blog\n"
                " [X] 0001_article\n"
                " [ ] 0002_publish\n"
                " [?] 0000_removed\n"
                " [?] 9999_removed\n"
                "legacy\n"
                " [?] 0001_gone\n"
            ),
            "unknown_tail_names_sorted": True,
            "unknown_only_apps_visible": True,
            "unknown_rows_fail_visible": True,
        },
        db_state=_read_only_state("known_and_valid_unknown_records"),
        metrics={
            **_read_metrics(stdout_writes=1),
            "known_rows": 3,
            "unknown_rows": 3,
        },
    )


def inconsistent_known_history(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        phase="evaluation",
        result=None,
        error=_error(
            "migration_history_error",
            "inconsistent_applied_history",
        ),
        db_state=_read_only_state("preserved_inconsistent_known_history"),
        metrics={
            **_read_metrics(stdout_writes=0),
            "migration_begins": 0,
            "stderr_writes": 1,
        },
    )


def _boundary_case(
    name: str,
    *,
    outcome: str,
    category: str | None = None,
    code: str | None = None,
    cleanup_failed: bool = False,
    definition_loads: int = 0,
    backend_open_calls: int = 0,
    backend_acquisitions: int = 0,
    session_open_calls: int = 0,
    session_acquisitions: int = 0,
    history_reads: int = 0,
    session_closes: int = 0,
    backend_closes: int = 0,
    snapshot_published: bool = False,
    exit_code: int | None = None,
    project_selections: int = 0,
    build_calls: int = 0,
    stdout_write_attempts: int = 0,
    partial_stdout_writes: int = 0,
    stdout_write_errors: int = 0,
) -> dict[str, Any]:
    if exit_code is None:
        exit_code = 0 if outcome == "success" else 3
    return {
        "automatic_retries": 0,
        "backend_acquisitions": backend_acquisitions,
        "backend_closes": backend_closes,
        "backend_open_calls": backend_open_calls,
        "build_calls": build_calls,
        "category": category,
        "cleanup_failed": cleanup_failed,
        "code": code,
        "definition_loads": definition_loads,
        "exit_code": exit_code,
        "history_reads": history_reads,
        "migration_begins": 0,
        "name": name,
        "outcome": outcome,
        "project_selections": project_selections,
        "session_acquisitions": session_acquisitions,
        "session_closes": session_closes,
        "session_open_calls": session_open_calls,
        "snapshot_published": snapshot_published,
        "stderr_republications": 0,
        "partial_stdout_writes": partial_stdout_writes,
        "stdout_write_attempts": stdout_write_attempts,
        "stdout_write_errors": stdout_write_errors,
    }


def project_boundary(contract_id: str) -> dict[str, Any]:
    cases = [
        _boundary_case(
            "invalid_arguments",
            outcome="error",
            category="migration_project_command_error",
            code="invalid_arguments",
            exit_code=2,
        ),
        _boundary_case(
            "invalid_definition",
            outcome="error",
            category="migration_definition_source_error",
            code="invalid_definition_document",
            definition_loads=1,
            exit_code=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "pre_acquisition_cancel",
            outcome="error",
            category="migration_project_process_error",
            code="project_canceled",
        ),
        _boundary_case(
            "success",
            outcome="success",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            snapshot_published=True,
            project_selections=1,
            build_calls=1,
            stdout_write_attempts=1,
        ),
        _boundary_case(
            "partial_backend_acquisition",
            outcome="error",
            category="migration_backend_error",
            code="backend_open_failed",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            backend_closes=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "partial_session_acquisition",
            outcome="error",
            category="migration_recorder_error",
            code="read_failed",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            session_closes=1,
            backend_closes=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "history_read_failure",
            outcome="error",
            category="migration_recorder_error",
            code="read_failed",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "revision_fence_adoption_required",
            outcome="error",
            category="migration_capability_error",
            code="revision_fence_adoption_required",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            exit_code=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "stale_history_revision",
            outcome="error",
            category="migration_conflict_error",
            code="stale_history_revision",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "history_revision_contended",
            outcome="error",
            category="migration_transaction_error",
            code="history_revision_contended",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "history_revision_integrity",
            outcome="error",
            category="migration_history_error",
            code="history_revision_integrity",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            exit_code=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "session_close_failure",
            outcome="error",
            category="migration_backend_error",
            code="backend_close_failed",
            cleanup_failed=True,
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "outer_close_failure",
            outcome="error",
            category="migration_backend_error",
            code="backend_close_failed",
            cleanup_failed=True,
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            project_selections=1,
            build_calls=1,
        ),
        _boundary_case(
            "closed_snapshot_then_cancel",
            outcome="success",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            snapshot_published=True,
            project_selections=1,
            build_calls=1,
            stdout_write_attempts=1,
        ),
        _boundary_case(
            "terminal_stdout_short_write",
            outcome="error",
            category="migration_project_internal_error",
            code="project_internal_error",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            project_selections=1,
            build_calls=1,
            stdout_write_attempts=1,
            partial_stdout_writes=1,
        ),
        _boundary_case(
            "terminal_stdout_error",
            outcome="error",
            category="migration_project_internal_error",
            code="project_internal_error",
            definition_loads=1,
            backend_open_calls=1,
            backend_acquisitions=1,
            session_open_calls=1,
            session_acquisitions=1,
            history_reads=1,
            session_closes=1,
            backend_closes=1,
            project_selections=1,
            build_calls=1,
            stdout_write_attempts=1,
            stdout_write_errors=1,
        ),
    ]
    return _observed(
        contract_id,
        phase="environment",
        result={
            "cases": cases,
            "closed_snapshot_survives_later_cancel": True,
            "failure_precedence": [
                "argument_validation",
                "definition_load",
                "backend_open",
                "revision_session_open",
                "history_read",
                "revision_session_close",
                "backend_close",
                "response_publication",
            ],
            "private_causes_published": False,
        },
        db_state={
            "application_mutations": 0,
            "all_cases_preserve_schema": True,
            "recorder_mutations": 0,
            "revision_mutations": 0,
            "schema_mutations": 0,
            "successful_snapshot_closed_before_publication": True,
        },
        metrics={
            "artifact_secret_occurrences": 0,
            "cases": len(cases),
            "cleanup_failure_cases": sum(case["cleanup_failed"] for case in cases),
            "protocol_secret_occurrences": 0,
            "stderr_secret_occurrences": 0,
            "stdout_secret_occurrences": 0,
            "successful_snapshot_cases": sum(
                case["snapshot_published"] for case in cases
            ),
            "revision_fence_failure_cases": 4,
            "terminal_publication_failure_cases": 2,
        },
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "godj.migration.status.empty_catalog": empty_catalog,
    "godj.migration.status.unknown_record_visible": unknown_record_visible,
    "godj.migration.status.inconsistent_known_history": inconsistent_known_history,
    "godj.migration.status.project_boundary": project_boundary,
}
