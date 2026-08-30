"""Independent GoDj decisions for targeted migrate and read-only plan.

These payloads record only GoDj-owned command, protocol, lifecycle, cleanup,
and publication boundaries. They do not consult a checked-in expected result
or an external framework implementation.
"""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from .normalizer import normalize


SET_SLUG = "migration-target-plan"

_COMMAND_CATEGORY = "migration_project_command_error"
_INVALID_ARGUMENTS = "invalid_arguments"
_PLAN_CATEGORY = "migration_plan_error"
_TARGET_NOT_FOUND = "target_not_found"
_MAX_REQUEST_BYTES = 16 << 20
_MAX_RESPONSE_BYTES = 101 << 20
_MAX_PLAN_ROWS = 2_048
_MAX_IDENTITY_BYTES = 1 << 20
_MAX_IDENTITY_AGGREGATE_BYTES = 16 << 20


def _observed(
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


def _invalid_argv(name: str, argv: list[str]) -> dict[str, Any]:
    return {
        "argv": argv,
        "backend_opens": 0,
        "builds": 0,
        "case": name,
        "category": _COMMAND_CATEGORY,
        "code": _INVALID_ARGUMENTS,
        "project_discoveries": 0,
    }


def target_argv_and_pre_io_rejection(contract_id: str) -> dict[str, Any]:
    accepted = [
        {
            "argv": ["migrate"],
            "mode": "execute",
            "target": {"kind": "latest"},
        },
        {
            "argv": ["migrate", "--project", "./godj.toml"],
            "mode": "execute",
            "target": {"kind": "latest"},
        },
        {
            "argv": ["migrate", "--plan"],
            "mode": "plan",
            "target": {"kind": "latest"},
        },
        {
            "argv": ["migrate", "--plan", "--project", "./godj.toml"],
            "mode": "plan",
            "target": {"kind": "latest"},
        },
        {
            "argv": ["migrate", "blog", "0001_article"],
            "mode": "execute",
            "target": {
                "app": "blog",
                "kind": "named",
                "name": "0001_article",
            },
        },
        {
            "argv": [
                "migrate",
                "blog",
                "0001_article",
                "--project",
                "./godj.toml",
            ],
            "mode": "execute",
            "target": {
                "app": "blog",
                "kind": "named",
                "name": "0001_article",
            },
        },
        {
            "argv": ["migrate", "blog", "0001_article", "--plan"],
            "mode": "plan",
            "target": {
                "app": "blog",
                "kind": "named",
                "name": "0001_article",
            },
        },
        {
            "argv": [
                "migrate",
                "blog",
                "0001_article",
                "--plan",
                "--project",
                "./godj.toml",
            ],
            "mode": "plan",
            "target": {
                "app": "blog",
                "kind": "named",
                "name": "0001_article",
            },
        },
    ]
    rejected = [
        _invalid_argv("app_only", ["migrate", "blog"]),
        _invalid_argv(
            "permuted_project_plan",
            ["migrate", "--project", "./godj.toml", "--plan"],
        ),
        _invalid_argv("repeated_plan", ["migrate", "--plan", "--plan"]),
        _invalid_argv("double_dash", ["migrate", "--", "blog", "0001_article"]),
        _invalid_argv("unknown_option", ["migrate", "--database", "other"]),
        _invalid_argv("leading_dash_app", ["migrate", "--blog", "0001_article"]),
        _invalid_argv("leading_dash_name", ["migrate", "blog", "--0001"]),
    ]
    post_discovery_rejections = [
        {
            "argv": ["migrate", "blog", "0001"],
            "backend_opens": 1,
            "builds": 1,
            "case": "prefix_looking_exact_miss",
            "catalog_exact_name": "0001_initial",
            "category": _PLAN_CATEGORY,
            "code": _TARGET_NOT_FOUND,
            "history_reads": 1,
            "migration_begins": 0,
            "project_discoveries": 1,
            "requested_name": "0001",
        }
    ]
    return _observed(
        contract_id,
        phase="environment",
        result={
            "accepted": accepted,
            "exact_public_families": 8,
            "migration_name_resolution": "exact_only",
            "option_permutations": "rejected",
            "post_discovery_rejections": post_discovery_rejections,
            "rejected": rejected,
            "zero_reserved_spelling": "zero",
        },
        metrics={
            "accepted_forms": len(accepted),
            "backend_opens_for_rejected": sum(
                case["backend_opens"] for case in rejected
            ),
            "builds_for_rejected": sum(case["builds"] for case in rejected),
            "project_discoveries_for_rejected": sum(
                case["project_discoveries"] for case in rejected
            ),
            "post_discovery_target_not_found_cases": len(
                post_discovery_rejections
            ),
            "rejected_forms": len(rejected),
        },
    )


def target_noop_and_legacy_zero(contract_id: str) -> dict[str, Any]:
    cases = [
        {
            "begin_calls": 0,
            "case": "applied_named_leaf",
            "category": None,
            "code": None,
            "plan": [],
        },
        {
            "begin_calls": 0,
            "case": "legacy_zero_unknown_app",
            "category": None,
            "code": None,
            "plan": [],
        },
        {
            "begin_calls": 0,
            "case": "public_known_zero_unknown_app",
            "category": _PLAN_CATEGORY,
            "code": _TARGET_NOT_FOUND,
            "plan": None,
        },
    ]
    return _observed(
        contract_id,
        phase="evaluation",
        result={
            "cases": cases,
            "legacy_zero_unknown_contract": "empty_plan",
            "public_zero_requires_known_app": True,
        },
        metrics={
            "begin_calls": sum(case["begin_calls"] for case in cases),
            "history_reads": len(cases),
            "target_not_found_cases": sum(
                case["code"] == _TARGET_NOT_FOUND for case in cases
            ),
        },
    )


def plan_exact_and_no_mutation(contract_id: str) -> dict[str, Any]:
    nonempty_rows = [
        {"app": "blog", "direction": "backward", "name": "0002_editor"}
    ]
    cases = [
        {
            "case": "nonempty",
            "plan": nonempty_rows,
            "stdout": (
                '{"plan":[{"app":"blog","name":"0002_editor",'
                '"direction":"backward"}]}\n'
            ),
        },
        {"case": "empty", "plan": [], "stdout": '{"plan":[]}\n'},
    ]
    before = {
        "application": "unchanged",
        "history": ["blog.0001_article", "blog.0002_editor"],
        "revision": 2,
        "schema": "unchanged",
    }
    return _observed(
        contract_id,
        phase="evaluation",
        result={"cases": cases, "plan_is_execution_authority": False},
        db_state={
            "cases": [
                {"after": before, "before": before, "case": case["case"]}
                for case in cases
            ]
        },
        metrics={
            "application_mutations": 0,
            "history_reads": len(cases),
            "migration_begins": 0,
            "recorder_mutations": 0,
            "revision_mutations": 0,
            "schema_mutations": 0,
            "session_closes": len(cases),
        },
    )


def preview_drift_fresh_execute(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        phase="commit",
        result={
            "execute_plan": [],
            "preview_plan": [
                {"app": "blog", "direction": "forward", "name": "0002_editor"}
            ],
            "preview_token_accepted": False,
            "replanned_from_fresh_history": True,
        },
        db_state={
            "after_execute_history": ["blog.0001_article", "blog.0002_editor"],
            "after_preview_history": ["blog.0001_article"],
            "after_writer_drift_history": [
                "blog.0001_article",
                "blog.0002_editor",
            ],
            "preview_mutations": 0,
        },
        metrics={
            "automatic_retries": 0,
            "execute_history_reads": 1,
            "execute_migration_begins": 0,
            "preview_history_reads": 1,
            "preview_migration_begins": 0,
        },
    )


def reverse_middle_failure_resume(contract_id: str) -> dict[str, Any]:
    initial_history = [
        "blog.0001_article",
        "blog.0002_editor",
        "blog.0003_publish",
        "blog.0004_archive",
    ]
    cases = [
        {
            "case": "first_process",
            "category": "migration_execution_error",
            "code": "operation_failed",
            "committed": ["blog.0004_archive"],
            "plan": [
                "blog.0004_archive",
                "blog.0003_publish",
                "blog.0002_editor",
            ],
            "rolled_back": ["blog.0003_publish"],
            "unstarted": ["blog.0002_editor"],
        },
        {
            "case": "fresh_resume",
            "category": None,
            "code": None,
            "committed": ["blog.0003_publish", "blog.0002_editor"],
            "plan": ["blog.0003_publish", "blog.0002_editor"],
            "rolled_back": [],
            "unstarted": [],
        },
    ]
    return _observed(
        contract_id,
        phase="rollback",
        result={"cases": cases, "unstarted_tail_started": False},
        db_state={
            "after_failure_history": [
                "blog.0001_article",
                "blog.0002_editor",
                "blog.0003_publish",
            ],
            "after_resume_history": ["blog.0001_article"],
            "durable_prefix_preserved": True,
            "initial_history": initial_history,
            "rolled_back_step_preserved": True,
            "unstarted_tail_preserved": True,
        },
        metrics={
            "automatic_retries": 0,
            "first_process_reverse_commits": 1,
            "first_process_reverse_rollbacks": 1,
            "first_process_unstarted_steps": 1,
            "fresh_processes": 1,
            "fresh_resume_reverse_commits": 2,
            "fresh_resume_reverse_rollbacks": 0,
            "reverse_commits": 3,
            "reverse_rollbacks": 1,
            "started_steps": 4,
            "unstarted_steps": 1,
        },
    )


def reverse_commit_outcomes(contract_id: str) -> dict[str, Any]:
    cases = [
        {
            "automatic_retries": 0,
            "case": "commit_outcome_unknown",
            "category": "migration_transaction_error",
            "code": "commit_outcome_unknown",
            "history": "unknown",
            "reported_success": False,
            "rollback_after_outcome": 0,
        },
        {
            "automatic_retries": 0,
            "case": "confirmed_rollback",
            "category": "migration_execution_error",
            "code": "operation_failed",
            "history": "preserved_before_step",
            "reported_success": False,
            "rollback_after_outcome": 1,
        },
        {
            "automatic_retries": 0,
            "case": "committed_cleanup_failure",
            "category": "migration_transaction_error",
            "code": "commit_cleanup_failed",
            "history": "committed_successor",
            "reported_success": False,
            "rollback_after_outcome": 0,
        },
    ]
    return _observed(
        contract_id,
        phase="commit",
        result={"cases": cases, "reconciliation_required_after_unknown": True},
        db_state={
            "committed_cleanup_history_preserved": True,
            "confirmed_rollback_history_preserved": True,
            "unknown_history_guessed": False,
        },
        metrics={
            "automatic_retries": sum(case["automatic_retries"] for case in cases),
            "cases": len(cases),
            "unknown_rollbacks": cases[0]["rollback_after_outcome"],
        },
    )


def project_protocol_and_ownership(contract_id: str) -> dict[str, Any]:
    ownership_cases = [
        {
            "backend_closes": 1,
            "backend_opens": 1,
            "case": "execute_success",
            "category": None,
            "code": None,
            "lifecycle_calls": 1,
            "mode": "execute",
            "private_response_writes": 1,
            "public_result_published": True,
        },
        {
            "backend_closes": 1,
            "backend_opens": 1,
            "case": "plan_success",
            "category": None,
            "code": None,
            "lifecycle_calls": 1,
            "mode": "plan",
            "private_response_writes": 1,
            "public_plan_published": True,
        },
        {
            "backend_closes": 1,
            "backend_opens": 1,
            "case": "outer_close_failure",
            "category": "migration_backend_error",
            "cleanup_failed": True,
            "code": "backend_close_failed",
            "lifecycle_calls": 1,
            "mode": "plan",
            "private_response_writes": 1,
            "public_plan_published": False,
        },
        {
            "case": "cancellation_cleanup",
            "category": "migration_project_process_error",
            "child_started": True,
            "cleanup_failed": False,
            "code": "project_canceled",
            "direct_reaps": 1,
            "mode": "plan",
            "partial_response_republished": False,
            "process_group_terminations": 1,
            "process_groups_remaining": 0,
            "public_plan_published": False,
            "sigint_attempts_maximum": 1,
            "sigkill_attempts_maximum": 1,
        },
        {
            "case": "partial_output_non_publication",
            "category": "migration_project_protocol_error",
            "child_started": True,
            "cleanup_failed": False,
            "code": "invalid_project_migrate_runner_response",
            "complete_private_documents": 0,
            "direct_reaps": 1,
            "mode": "plan",
            "partial_private_chunks": 1,
            "partial_response_republished": False,
            "public_plan_published": False,
        },
        {
            "backend_closes": 1,
            "backend_opens": 1,
            "case": "terminal_short_write",
            "category": "migration_project_internal_error",
            "code": "project_internal_error",
            "lifecycle_calls": 1,
            "mode": "plan",
            "private_response_write_attempts": 1,
            "private_response_writes_completed": 0,
            "public_plan_published": False,
        },
    ]
    wire_rejections = [
        {
            "accepted": False,
            "boundary": boundary,
            "case": f"{boundary}_{name}",
            "category": "migration_project_protocol_error",
            "code": (
                "invalid_project_migrate_runner_request"
                if boundary == "request"
                else "invalid_project_migrate_runner_response"
            ),
        }
        for boundary in ("request", "response")
        for name in (
            "duplicate_key",
            "unknown_key",
            "trailing_bytes",
            "noncanonical_number",
            "invalid_utf8",
            "unpaired_utf16_surrogate",
        )
    ]
    wire_rejections.extend(
        [
            {
                "accepted": False,
                "boundary": "request",
                "case": "request_retired_protocol_version",
                "category": "migration_project_protocol_error",
                "code": "project_migrate_protocol_incompatible",
            },
            {
                "accepted": False,
                "boundary": "response",
                "case": "response_mode_result_mismatch",
                "category": "migration_project_protocol_error",
                "code": "invalid_project_migrate_runner_response",
            },
            {
                "accepted": False,
                "boundary": "request",
                "case": "request_invalid_mode",
                "category": "migration_project_protocol_error",
                "code": "invalid_project_migrate_runner_request",
            },
            {
                "accepted": False,
                "boundary": "request",
                "case": "request_invalid_target_kind",
                "category": "migration_project_protocol_error",
                "code": "invalid_project_migrate_runner_request",
            },
            {
                "accepted": False,
                "boundary": "response",
                "case": "response_invalid_direction",
                "category": "migration_project_protocol_error",
                "code": "invalid_project_migrate_runner_response",
            },
        ]
    )
    resource_limits = [
        {
            "boundary": "request",
            "case": "request_bytes",
            "maximum": _MAX_REQUEST_BYTES,
            "overflow": "rejected",
            "unit": "bytes",
        },
        {
            "boundary": "response",
            "case": "response_bytes",
            "maximum": _MAX_RESPONSE_BYTES,
            "overflow": "rejected",
            "unit": "bytes",
        },
        {
            "boundary": "request_and_response",
            "case": "identity_bytes",
            "maximum": _MAX_IDENTITY_BYTES,
            "overflow": "rejected",
            "unit": "bytes",
        },
        {
            "boundary": "request_and_response",
            "case": "identity_aggregate_bytes",
            "maximum": _MAX_IDENTITY_AGGREGATE_BYTES,
            "overflow": "rejected",
            "unit": "bytes",
        },
        {
            "boundary": "response",
            "case": "plan_rows",
            "maximum": _MAX_PLAN_ROWS,
            "overflow": "rejected",
            "unit": "rows",
        },
    ]
    return _observed(
        contract_id,
        phase="environment",
        result={
            "cases": ownership_cases,
            "current_private_protocol_version": 2,
            "identity_normalization": "none",
            "legacy_private_reader": False,
            "load_before_backend_open": True,
            "plan_invariants": {
                "closed_directions": ["forward", "backward"],
                "duplicate_identity": "rejected",
                "maximum_unique_rows": _MAX_PLAN_ROWS,
                "mixed_direction": "rejected",
                "row_order": "preserved",
            },
            "private_argument": "__godj_project_migrate_runner_v2",
            "raw_causes_published": False,
            "redaction": {
                "published_raw_causes": 0,
                "published_secret_values": 0,
                "sensitive_classes": [
                    "backend_dsn",
                    "raw_error_cause",
                    "runner_stderr",
                ],
            },
            "resource_limits": resource_limits,
            "result_union_bound_to_mode": True,
            "valid_replacement_rune_preserved": True,
            "wire_rejections": wire_rejections,
        },
        db_state={
            "canceled_process_groups_remaining": 0,
            "failed_plan_published": False,
            "partial_response_republished": False,
            "plan_mutations": 0,
            "secret_values_published": 0,
        },
        metrics={
            "automatic_retries": 0,
            "cancellation_direct_reaps": 1,
            "cancellation_process_group_terminations": 1,
            "legacy_reader_paths": 0,
            "ownership_cases": len(ownership_cases),
            "partial_responses_republished": 0,
            "raw_secret_occurrences": 0,
            "resource_limit_cases": len(resource_limits),
            "strict_wire_rejection_cases": len(wire_rejections),
            "successful_mode_calls": 2,
        },
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "godj.migration.target_plan.target_argv_and_pre_io_rejection": (
        target_argv_and_pre_io_rejection
    ),
    "godj.migration.target_plan.target_noop_and_legacy_zero": (
        target_noop_and_legacy_zero
    ),
    "godj.migration.target_plan.plan_exact_and_no_mutation": (
        plan_exact_and_no_mutation
    ),
    "godj.migration.target_plan.preview_drift_fresh_execute": (
        preview_drift_fresh_execute
    ),
    "godj.migration.target_plan.reverse_middle_failure_resume": (
        reverse_middle_failure_resume
    ),
    "godj.migration.target_plan.reverse_commit_outcomes": reverse_commit_outcomes,
    "godj.migration.target_plan.project_protocol_and_ownership": (
        project_protocol_and_ownership
    ),
}
