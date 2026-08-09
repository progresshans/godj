"""Independent reference observations for GoDj migration project checks.

MIG-065..074 are GoDj decisions from Proposed ADR-0021. They do not observe
or reproduce Django's database-aware pending-migration command, model-drift
command, Python migration-module discovery, or Python project layout. The
existing Django-named runner/profile namespace is reused only so all locked
reference sets share one deterministic protocol-v2 corpus and checksum gate.

This module intentionally models only the canonical base observations. The
filesystem, process, protocol, resource-limit, and mutation feasibility proof
lives in the Go ``conformance/projectcheck`` test harness.
"""

from __future__ import annotations

from typing import Any

from .normalizer import normalize


EMPTY_DEFINITION_SET_DIGEST = (
    "sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450"
)
ONE_MODEL_DEFINITION_SET_DIGEST = (
    "sha256:07e61f8d956002cff0d7fe2db10c16ea4a30829e9f0ced09c69c40ff2c2399bc"
)
TWO_SOURCE_DEFINITION_SET_DIGEST = (
    "sha256:5a73e03d3448f3f19f7646eed67f4e312610f4389f2e3e537c379e725f0b106d"
)

METRIC_FIELDS = (
    "build_calls",
    "runner_calls",
    "runner_response_writes",
    "source_reads",
    "load_calls",
    "documents_received",
    "headers_validated",
    "operations_decoded",
    "planner_construction",
    "definitions_published",
    "definition_sets_published",
    "direct_planner_calls",
    "godj_db_calls",
    "revision_lifecycle_calls",
    "user_stdout_writes",
    "user_stderr_writes",
    "partial_stdout_writes",
    "exit_code",
    "command_dispatches",
    "ancestor_directories_inspected",
    "descriptor_reads",
    "roots_opened",
    "directory_entries_seen",
    "failure",
)


def _metrics(**values: Any) -> dict[str, Any]:
    metrics: dict[str, Any] = {
        field: None if field == "failure" else 0 for field in METRIC_FIELDS
    }
    unknown = set(values) - set(METRIC_FIELDS)
    if unknown:
        raise AssertionError(f"unknown project-check metric fields: {sorted(unknown)}")
    metrics.update(values)
    if tuple(metrics) != METRIC_FIELDS:
        raise AssertionError("project-check metric field order changed")
    return metrics


def _result(
    *,
    source_count: int,
    definition_count: int,
    definition_set_digest: str,
) -> dict[str, Any]:
    return {
        "source_count": source_count,
        "definition_count": definition_count,
        "definition_set_digest": definition_set_digest,
    }


def _success_observation(
    contract_id: str,
    phase: str,
    result: dict[str, Any],
    metrics: dict[str, Any],
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


def _failure_observation(
    contract_id: str,
    phase: str,
    *,
    category: str,
    code: str,
    metrics: dict[str, Any],
) -> dict[str, Any]:
    return {
        "db_state": None,
        "error": {
            "category": category,
            "code": code,
            "message_is_contract": False,
        },
        "id": contract_id,
        "metrics": normalize(metrics),
        "phase": phase,
        "result": None,
        "status": "observed",
    }


def nested_project_success(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        "environment",
        _result(
            source_count=1,
            definition_count=1,
            definition_set_digest=ONE_MODEL_DEFINITION_SET_DIGEST,
        ),
        _metrics(
            build_calls=1,
            runner_calls=1,
            runner_response_writes=1,
            source_reads=1,
            load_calls=1,
            documents_received=1,
            headers_validated=1,
            operations_decoded=1,
            planner_construction=1,
            definitions_published=1,
            definition_sets_published=1,
            user_stdout_writes=1,
            command_dispatches=1,
            ancestor_directories_inspected=4,
            descriptor_reads=1,
            roots_opened=1,
            directory_entries_seen=1,
        ),
    )


def explicit_project_override(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        "environment",
        _result(
            source_count=1,
            definition_count=1,
            definition_set_digest=ONE_MODEL_DEFINITION_SET_DIGEST,
        ),
        _metrics(
            build_calls=1,
            runner_calls=1,
            runner_response_writes=1,
            source_reads=1,
            load_calls=1,
            documents_received=1,
            headers_validated=1,
            operations_decoded=1,
            planner_construction=1,
            definitions_published=1,
            definition_sets_published=1,
            user_stdout_writes=1,
            command_dispatches=1,
            descriptor_reads=1,
            roots_opened=1,
            directory_entries_seen=1,
        ),
    )


def empty_catalog(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        "construction",
        _result(
            source_count=0,
            definition_count=0,
            definition_set_digest=EMPTY_DEFINITION_SET_DIGEST,
        ),
        _metrics(
            build_calls=1,
            runner_calls=1,
            runner_response_writes=1,
            load_calls=1,
            planner_construction=1,
            definition_sets_published=1,
            user_stdout_writes=1,
            command_dispatches=1,
            ancestor_directories_inspected=1,
            descriptor_reads=1,
            roots_opened=1,
        ),
    )


def canonical_filesystem_order(contract_id: str) -> dict[str, Any]:
    return _success_observation(
        contract_id,
        "construction",
        _result(
            source_count=2,
            definition_count=2,
            definition_set_digest=TWO_SOURCE_DEFINITION_SET_DIGEST,
        ),
        _metrics(
            build_calls=1,
            runner_calls=1,
            runner_response_writes=1,
            source_reads=2,
            load_calls=1,
            documents_received=2,
            headers_validated=2,
            operations_decoded=3,
            planner_construction=1,
            definitions_published=2,
            definition_sets_published=1,
            user_stdout_writes=1,
            command_dispatches=1,
            ancestor_directories_inspected=1,
            descriptor_reads=1,
            roots_opened=2,
            directory_entries_seen=3,
        ),
    )


def unsafe_source_entry(contract_id: str) -> dict[str, Any]:
    return _failure_observation(
        contract_id,
        "construction",
        category="migration_definition_discovery_error",
        code="unsafe_source_entry",
        metrics=_metrics(
            build_calls=1,
            runner_calls=1,
            runner_response_writes=1,
            user_stderr_writes=1,
            exit_code=1,
            command_dispatches=1,
            ancestor_directories_inspected=1,
            descriptor_reads=1,
            roots_opened=1,
            directory_entries_seen=1,
        ),
    )


def project_not_found(contract_id: str) -> dict[str, Any]:
    return _failure_observation(
        contract_id,
        "environment",
        category="migration_project_selection_error",
        code="project_not_found",
        metrics=_metrics(
            user_stderr_writes=1,
            exit_code=2,
            ancestor_directories_inspected=4,
        ),
    )


def project_protocol_incompatible(contract_id: str) -> dict[str, Any]:
    return _failure_observation(
        contract_id,
        "environment",
        category="migration_project_protocol_error",
        code="project_protocol_incompatible",
        metrics=_metrics(
            build_calls=1,
            runner_calls=1,
            runner_response_writes=1,
            user_stderr_writes=1,
            exit_code=3,
            ancestor_directories_inspected=1,
            descriptor_reads=1,
        ),
    )


def project_build_failure_atomic(contract_id: str) -> dict[str, Any]:
    return _failure_observation(
        contract_id,
        "environment",
        category="migration_project_build_error",
        code="project_build_failed",
        metrics=_metrics(
            build_calls=1,
            user_stderr_writes=1,
            exit_code=3,
            ancestor_directories_inspected=1,
            descriptor_reads=1,
        ),
    )


def definition_load_failure(contract_id: str) -> dict[str, Any]:
    failure = {
        "actual": 0,
        "app": "",
        "graph_sources": [],
        "json_pointer": "/migration/name",
        "limit": "",
        "maximum": 0,
        "name": "",
        "operation_index": -1,
        "reason": "duplicate_key",
        "source_id": "migrations/broken.godj.json",
        "stage": "document",
    }
    return _failure_observation(
        contract_id,
        "construction",
        category="migration_definition_source_error",
        code="invalid_definition_document",
        metrics=_metrics(
            build_calls=1,
            runner_calls=1,
            runner_response_writes=1,
            source_reads=1,
            load_calls=1,
            documents_received=1,
            user_stderr_writes=1,
            exit_code=1,
            command_dispatches=1,
            ancestor_directories_inspected=1,
            descriptor_reads=1,
            roots_opened=1,
            directory_entries_seen=1,
            failure=failure,
        ),
    )


def invalid_runner_response(contract_id: str) -> dict[str, Any]:
    return _failure_observation(
        contract_id,
        "environment",
        category="migration_project_protocol_error",
        code="invalid_project_runner_response",
        metrics=_metrics(
            build_calls=1,
            runner_calls=1,
            runner_response_writes=1,
            user_stderr_writes=1,
            exit_code=3,
            ancestor_directories_inspected=1,
            descriptor_reads=1,
        ),
    )


SCENARIOS = {
    "godj.migration.project_check.nested_project_success": (
        nested_project_success
    ),
    "godj.migration.project_check.explicit_project_override": (
        explicit_project_override
    ),
    "godj.migration.project_check.empty_catalog": empty_catalog,
    "godj.migration.project_check.canonical_filesystem_order": (
        canonical_filesystem_order
    ),
    "godj.migration.project_check.unsafe_source_entry": unsafe_source_entry,
    "godj.migration.project_check.project_not_found": project_not_found,
    "godj.migration.project_check.project_protocol_incompatible": (
        project_protocol_incompatible
    ),
    "godj.migration.project_check.project_build_failure_atomic": (
        project_build_failure_atomic
    ),
    "godj.migration.project_check.definition_load_failure": (
        definition_load_failure
    ),
    "godj.migration.project_check.invalid_runner_response": (
        invalid_runner_response
    ),
}
