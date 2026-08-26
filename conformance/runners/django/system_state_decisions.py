"""Independent GoDj decision observations for durable system-state contracts."""

from __future__ import annotations

from typing import Any

from .normalizer import normalize


def _observed(
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


def explicit_migration_gate(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "migration_identity": "godj_system.0001_initial",
            "ready_after_explicit_migrate": True,
            "ready_without_schema": False,
            "startup_auto_migrate": False,
        },
        phase="environment",
        db_state={
            "after_explicit_migrate": {
                "models": ["admin_credential", "audit_event", "session"],
                "system_tables": 3,
            },
            "after_missing_schema_startup": {
                "bootstrap_rows": 0,
                "system_tables": 0,
            },
        },
        metrics={
            "auto_ddl_statements": 0,
            "bootstrap_writes_before_ready": 0,
            "listeners_started_before_ready": 0,
        },
    )


def admin_bootstrap_gate(contract_id: str) -> dict[str, Any]:
    cases = [
        {"case": "empty", "outcome": "bootstrapped", "writes": 1},
        {"case": "identical_restart", "outcome": "ready", "writes": 0},
        {"case": "credential_mismatch", "outcome": "fail_closed", "writes": 0},
        {"case": "corrupt", "outcome": "fail_closed", "writes": 0},
        {"case": "duplicate", "outcome": "fail_closed", "writes": 0},
    ]
    return _observed(
        contract_id,
        {"cases": cases, "repair_attempted": False},
        phase="commit",
        db_state={
            "corrupt_rows_preserved": 1,
            "duplicate_rows_preserved": 2,
            "rows_after_identical_restart": 1,
        },
        metrics={"bootstrap_writes": 1, "restart_verification_writes": 0},
    )


def session_expiry_and_touch(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "absolute_expiry_denied": True,
            "idle_expiry_denied": True,
            "nonadvancing_touch_writes": 0,
            "touch_monotonic": True,
        },
        phase="commit",
        db_state={
            "absolute_expired_rows": 0,
            "idle_expired_rows": 0,
            "live_rows": 1,
        },
        metrics={"expired_rows_deleted": 2, "touch_writes": 1},
    )


def capacity_reap_and_rotate_rollback(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "capacity_after_bounded_reap": "available",
            "capacity_without_reap": "rejected",
            "rotate_fault": {
                "new_session_visible": False,
                "old_session_preserved": True,
                "outcome": "rolled_back",
            },
        },
        phase="rollback",
        db_state={
            "after_rotate_fault": {"live_rows": 1, "old_rows": 1},
            "reap_limit": 2,
            "rows_reaped": 2,
        },
        metrics={"nested_transactions": 0, "rotate_retries": 0},
    )


def digest_only_current_codec(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "bearer_storage": "domain_separated_sha256_digest",
            "codec": {
                "canonical": True,
                "current_versions": ["admin.v1", "audit.v1", "session.v1"],
                "unknown_version": "rejected",
            },
            "raw_bearer_persisted": False,
        },
        phase="evaluation",
        db_state={
            "digest_hex_length": 64,
            "raw_bearer_columns": 0,
            "secret_payload_fields": 0,
        },
        metrics={
            "raw_bearers_observed": 0,
            "raw_credentials_observed": 0,
            "raw_csrf_values_observed": 0,
        },
    )


def commit_outcome_unknown(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "outcome": "commit_outcome_unknown",
            "reconciliation_required": True,
            "reported_success": False,
            "verified_commit": False,
        },
        phase="commit",
        db_state={
            "article_state": "unknown",
            "audit_state": "unknown",
            "synthetic_audit_rows": 0,
        },
        metrics={"automatic_retries": 0, "rollback_after_unknown": 0},
    )


def coordinated_atomic_fence(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "acquire_before_callback": True,
            "automatic_retry": False,
            "callback_invocations": {
                "acquire_cancelled": 0,
                "acquire_failed": 0,
                "acquire_succeeded": 1,
            },
            "callback_cancellation": "rolled_back",
            "commit_failure": "commit_outcome_unknown",
            "confirmed_callback_error": "rolled_back",
            "rollback_uncertainty": "transaction_outcome_unknown",
        },
        phase="commit",
        db_state={
            "coordination_scope": "backend_database_or_schema",
            "cross_domain_nesting": "rejected",
            "ordinary_atomic_semantics_changed": False,
        },
        metrics={
            "callback_retries": 0,
            "coordination_fences": 1,
            "secret_values_serialized": 0,
        },
    )


def concurrent_admin_bootstrap(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "concurrent_empty": "identical_material_success",
            "duplicate_publications": 0,
            "mismatched_material": "fail_closed",
        },
        phase="commit",
        db_state={
            "credential_rows": 1,
            "mismatch_writes": 0,
            "published_materials": 1,
        },
        metrics={
            "bootstrap_winners": 1,
            "coordination_retries": 0,
            "secret_values_serialized": 0,
        },
    )


def concurrent_session_capacity(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "capacity_overshoot": False,
            "concurrent_create": "linearized",
            "digest_collision": False,
            "reap_scope": "global",
        },
        phase="commit",
        db_state={
            "capacity_bound_preserved": True,
            "duplicate_digests": 0,
            "unbounded_reap": False,
        },
        metrics={
            "capacity_overshoots": 0,
            "coordination_retries": 0,
            "raw_bearers_observed": 0,
        },
    )


def concurrent_touch_monotonicity(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "accessed_at_monotonic": True,
            "idle_expiry_monotonic": True,
            "out_of_order_touch": "newest_state_preserved",
        },
        phase="commit",
        db_state={
            "accessed_at_regressions": 0,
            "idle_expiry_regressions": 0,
            "live_rows": 1,
        },
        metrics={
            "coordination_retries": 0,
            "stale_overwrites": 0,
            "touch_winners": 1,
        },
    )


def concurrent_session_rotation(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "logout_first": "later_rotate_denied",
            "old_bearer_resurrected": False,
            "rotate_first_stale_old_id_touch": {
                "old_bearer_resurrected": False,
                "outcome": "not_found",
            },
            "rotate_first_old_id_logout": "replacement_preserved",
            "rotation_publication": "exactly_one_winner",
            "touch_first_then_rotate": {
                "old_rows": 0,
                "replacement_rows": 1,
            },
        },
        phase="commit",
        db_state={
            "duplicate_replacements": 0,
            "old_rows_after_rotation": 0,
            "replacement_rows": 1,
        },
        metrics={
            "automatic_retries": 0,
            "rotation_winners": 1,
            "resurrection_writes": 0,
        },
    )


def concurrent_article_audit(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "article_and_audit_atomic": True,
            "fault_outcome": "rolled_back",
            "global_history_bound_preserved": True,
        },
        phase="rollback",
        db_state={
            "article_rows_after_fault": 0,
            "audit_rows_after_fault": 0,
            "orphan_audit_rows": 0,
        },
        metrics={
            "automatic_retries": 0,
            "partial_commits": 0,
            "prune_bound_escapes": 0,
        },
    )


def shared_csrf_key_ring(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "active_key_signs_new_values": True,
            "cross_runtime_handoff": "accepted",
            "removed_key": "rejected",
            "staged_rotation": "old_and_new_accepted",
            "unrelated_key": "rejected",
        },
        phase="evaluation",
        db_state={
            "key_material_persisted": False,
            "provider_state_owned_by_framework": False,
            "ring_mutable": False,
        },
        metrics={
            "secret_values_serialized": 0,
            "unbounded_verification_paths": 0,
            "verification_key_set_bounded": True,
        },
    )


def two_process_backend_restart(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "backend_cases": [
                {
                    "backend": "postgresql_17_10",
                    "clean_restart_preserved": True,
                    "same_schema": True,
                },
                {
                    "backend": "sqlite",
                    "clean_restart_preserved": True,
                    "same_database": True,
                },
            ],
            "barrier_race": "linearized",
        },
        phase="environment",
        db_state={
            "cross_process_state_divergence": 0,
            "restart_state_loss": 0,
            "schema_drift": False,
        },
        metrics={
            "distinct_processes": 2,
            "required_backend_cases": 2,
            "secret_values_serialized": 0,
            "skipped_required_cases": 0,
        },
    )


SCENARIOS = {
    "godj.system_state.explicit_migration_gate": explicit_migration_gate,
    "godj.system_state.admin_bootstrap_gate": admin_bootstrap_gate,
    "godj.system_state.session_expiry_and_touch": session_expiry_and_touch,
    "godj.system_state.capacity_reap_and_rotate_rollback": capacity_reap_and_rotate_rollback,
    "godj.system_state.digest_only_current_codec": digest_only_current_codec,
    "godj.system_state.commit_outcome_unknown": commit_outcome_unknown,
    "godj.system_state.coordinated_atomic_fence": coordinated_atomic_fence,
    "godj.system_state.concurrent_admin_bootstrap": concurrent_admin_bootstrap,
    "godj.system_state.concurrent_session_capacity": concurrent_session_capacity,
    "godj.system_state.concurrent_touch_monotonicity": concurrent_touch_monotonicity,
    "godj.system_state.concurrent_session_rotation": concurrent_session_rotation,
    "godj.system_state.concurrent_article_audit": concurrent_article_audit,
    "godj.system_state.shared_csrf_key_ring": shared_csrf_key_ring,
    "godj.system_state.two_process_backend_restart": two_process_backend_restart,
}
