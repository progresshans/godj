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


def explicit_operator_provisioning(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "current_api": ["OpenExisting", "ProvisionOperator"],
            "current_only": True,
            "implicit_bootstrap_open": "removed",
            "open_existing_raw_secret_input": False,
            "provision_intent": "explicit",
        },
        phase="construction",
        db_state={
            "open_existing_writes": 0,
            "schema_or_migration_bytes_changed": False,
            "startup_credential_inserts": 0,
        },
        metrics={
            "compatibility_shims": 0,
            "provision_entrypoints": 1,
            "raw_secret_inputs_to_open_existing": 0,
        },
    )


def createsuperuser_argv_and_pre_io(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "accepted_argv": [
                ["createsuperuser"],
                ["createsuperuser", "--project", "PATH"],
            ],
            "invalid_forms": "rejected_before_io",
            "rejected_classes": [
                "identity_or_secret_flag",
                "noncanonical_permutation",
                "positional_identity",
            ],
        },
        phase="environment",
        db_state={
            "backend_opens_on_rejection": 0,
            "project_reads_on_rejection": 0,
            "writes_on_rejection": 0,
        },
        metrics={
            "accepted_forms": 2,
            "child_starts_on_rejection": 0,
            "project_builds_on_rejection": 0,
            "project_discoveries_on_rejection": 0,
            "secret_bearing_forms_accepted": 0,
            "terminal_reads_on_rejection": 0,
        },
    )


def tty_secret_transport(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "confirmation": "required_and_parent_verified",
            "echo_disabled_for_secret_input": True,
            "input_mode": "actual_terminal_only",
            "terminal_restore": ["success", "error", "interrupt"],
            "transport": {
                "confirmation_forwarded": False,
                "encoding": "big_endian_bounded_binary",
                "magic": "GODJCSU1",
                "one_shot": True,
            },
        },
        phase="environment",
        db_state={
            "argv_secret_occurrences": 0,
            "environment_secret_occurrences": 0,
            "filesystem_secret_occurrences": 0,
        },
        metrics={
            "frame_max_bytes": 1292,
            "pipe_writes": 1,
            "secret_max_bytes": 1024,
            "terminal_reads_before_project_build": 0,
            "terminal_reads_before_project_selection": 0,
            "username_max_bytes": 256,
        },
    )


def project_provision_ownership(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "gates": ["exact_system_migration", "system_state_readiness"],
            "ordering": [
                "request_validation",
                "project_selection",
                "backend_open",
                "provision",
                "backend_close",
                "response_publication",
            ],
            "project_owns_backend_and_policy": True,
        },
        phase="environment",
        db_state={
            "mutation_without_exact_migration": 0,
            "open_before_validation": 0,
            "provision_before_readiness": 0,
        },
        metrics={
            "backend_closes": 1,
            "backend_opens": 1,
            "provision_calls": 1,
        },
    )


def operator_provision_cardinality(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "cases": [
                {"case": "empty", "outcome": "created", "writes": 1},
                {
                    "case": "concurrent_empty",
                    "loser_outcome": "credential_already_exists",
                    "outcome": "exactly_one_winner",
                    "writes": 1,
                },
                {
                    "case": "already_one",
                    "outcome": "credential_already_exists",
                    "writes": 0,
                },
                {
                    "case": "cardinality_two_or_more",
                    "outcome": "invalid_cardinality",
                    "writes": 0,
                },
                {
                    "case": "malformed_or_profile_invalid",
                    "outcome": "corrupt_state",
                    "writes": 0,
                },
                {
                    "case": "policy_mismatch",
                    "outcome": "credential_policy_mismatch",
                    "writes": 0,
                },
            ],
            "existing_secret_compared": False,
        },
        phase="commit",
        db_state={
            "credential_rows_after_winner": 1,
            "existing_rows_deleted": 0,
            "existing_rows_updated": 0,
        },
        metrics={
            "automatic_retries": 0,
            "concurrent_winners": 1,
            "loser_mutations": 0,
        },
    )


def provision_outcome_ownership(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "cases": [
                {
                    "case": "confirmed_rollback",
                    "creation": "not_committed",
                    "retry": False,
                },
                {
                    "case": "commit_outcome_unknown",
                    "creation": "unknown",
                    "reconciliation": "fresh_open_existing_or_login",
                    "retry": False,
                },
                {
                    "case": "known_created_backend_close_failure",
                    "creation": "preserved",
                    "known_created": True,
                    "retry": False,
                },
                {
                    "case": "known_created_workspace_cleanup_failure",
                    "creation": "preserved",
                    "known_created": True,
                    "retry": False,
                },
                {
                    "case": "known_created_output_failure",
                    "creation": "preserved",
                    "known_created": True,
                    "retry": False,
                },
            ],
            "synthetic_success": False,
        },
        phase="commit",
        db_state={
            "commit_unknown_rows": "unknown",
            "confirmed_rollback_rows": 0,
            "known_created_rows": 1,
        },
        metrics={
            "automatic_retries": 0,
            "creation_attempts": 1,
            "synthetic_successes": 0,
        },
    )


def open_existing_authenticator(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "raw_secret_required": False,
            "startup": "authenticator_ready_from_stored_state",
            "validation_cases": [
                {"case": "valid_stored_state", "outcome": "authenticator_ready"},
                {
                    "case": "malformed_or_profile_invalid",
                    "outcome": "corrupt_state",
                },
                {
                    "case": "policy_mismatch",
                    "outcome": "credential_policy_mismatch",
                },
            ],
            "validated_fields": [
                "username",
                "encoded_credential",
                "hash_profile",
                "principal",
                "active",
                "permissions",
                "definition_digest",
            ],
        },
        phase="evaluation",
        db_state={
            "credential_rows": 1,
            "open_existing_writes": 0,
            "stored_encoded_credential_preserved": True,
        },
        metrics={
            "authenticator_constructions": 1,
            "credential_mismatch_code_occurrences": 0,
            "raw_secret_reads": 0,
            "startup_writes": 0,
        },
    )


def credential_absent_public_only(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "cases": [
                {
                    "case": "exact_migration_and_all_system_state_empty",
                    "outcome": "credential_absent",
                    "public_only": True,
                },
                {
                    "case": "missing_or_wrong_migration",
                    "outcome": "schema_unavailable",
                    "public_only": False,
                },
                {
                    "case": "unavailable_table",
                    "outcome": "startup_failure",
                    "public_only": False,
                },
                {
                    "case": "dependent_rows_without_credential",
                    "outcome": "startup_failure",
                    "public_only": False,
                },
                {
                    "case": "corrupt_state",
                    "outcome": "corrupt_state",
                    "public_only": False,
                },
                {
                    "case": "policy_mismatch",
                    "outcome": "credential_policy_mismatch",
                    "public_only": False,
                },
            ],
            "failure_downgrade": False,
        },
        phase="environment",
        db_state={
            "downgraded_failure_cases": 0,
            "public_only_mutations": 0,
            "required_empty_stores": ["credential", "session", "audit"],
        },
        metrics={
            "credential_absent_branches": 1,
            "failure_downgrades": 0,
            "startup_writes": 0,
        },
    )


def operator_backend_login_restart(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "backend_cases": [
                {
                    "admin_authenticated": True,
                    "api_authenticated": True,
                    "backend": "postgresql_17_10",
                    "distinct_process_restart": True,
                    "provisioned": True,
                    "provision_process_distinct_from_runtime": True,
                },
                {
                    "admin_authenticated": True,
                    "api_authenticated": True,
                    "backend": "sqlite",
                    "distinct_process_restart": True,
                    "provisioned": True,
                    "provision_process_distinct_from_runtime": True,
                },
            ],
            "django_login_semantics_reused": True,
            "restart_raw_secret_input": False,
        },
        phase="environment",
        db_state={
            "credential_rows_per_backend": 1,
            "restart_state_loss": 0,
            "schema_drift": False,
        },
        metrics={
            "distinct_processes_per_backend": 3,
            "provision_calls_per_backend": 1,
            "provision_processes_per_backend": 1,
            "raw_secret_occurrences": 0,
            "required_backend_cases": 2,
            "runtime_processes_per_backend": 2,
            "skipped_required_cases": 0,
        },
    )


def sensitive_child_cleanup(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "bounded_response": True,
            "cancellation": "process_group_interrupt_then_kill",
            "direct_child_reaped": True,
            "held_pipe_descendant_cleaned": True,
            "public_diagnostics_redacted": True,
        },
        phase="environment",
        db_state={
            "child_processes_after_cleanup": 0,
            "partial_private_response_published": False,
            "secret_artifacts": 0,
        },
        metrics={
            "direct_child_reaps": 1,
            "private_response_max_bytes": 4096,
            "raw_child_stderr_publications": 0,
            "secret_occurrences": 0,
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
    "godj.system_state.explicit_operator_provisioning": explicit_operator_provisioning,
    "godj.system_state.createsuperuser_argv_and_pre_io": createsuperuser_argv_and_pre_io,
    "godj.system_state.tty_secret_transport": tty_secret_transport,
    "godj.system_state.project_provision_ownership": project_provision_ownership,
    "godj.system_state.operator_provision_cardinality": operator_provision_cardinality,
    "godj.system_state.provision_outcome_ownership": provision_outcome_ownership,
    "godj.system_state.open_existing_authenticator": open_existing_authenticator,
    "godj.system_state.credential_absent_public_only": credential_absent_public_only,
    "godj.system_state.operator_backend_login_restart": operator_backend_login_restart,
    "godj.system_state.sensitive_child_cleanup": sensitive_child_cleanup,
}
