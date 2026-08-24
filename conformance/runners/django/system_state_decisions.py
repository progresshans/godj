"""Independent GoDj decision observations for the first durable system-state slice."""

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


SCENARIOS = {
    "godj.system_state.explicit_migration_gate": explicit_migration_gate,
    "godj.system_state.admin_bootstrap_gate": admin_bootstrap_gate,
    "godj.system_state.session_expiry_and_touch": session_expiry_and_touch,
    "godj.system_state.capacity_reap_and_rotate_rollback": capacity_reap_and_rotate_rollback,
    "godj.system_state.digest_only_current_codec": digest_only_current_codec,
    "godj.system_state.commit_outcome_unknown": commit_outcome_unknown,
}
