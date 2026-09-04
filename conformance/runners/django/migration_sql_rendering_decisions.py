"""Independent GoDj decisions for deterministic migration SQL projection.

These observations do not import Django, execute a renderer, read checked-in
artifacts, or touch a project/database. They lock only the GoDj-owned command,
request, backend-profile, resource, cleanup, redaction, and publication policy
proposed by GDJ-0054 and ADR-0055.
"""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from .normalizer import normalize


SET_SLUG = "migration-sql-rendering"
_COMMAND_CATEGORY = "migration_project_command_error"
_INVALID_ARGUMENTS = "invalid_arguments"
_RENDER_CATEGORY = "migration_sql_render_error"
_RESOURCE_CATEGORY = "migration_sql_resource_error"
_CAPABILITY_CATEGORY = "migration_capability_error"
_MAX_STATEMENTS = 2_048
_MAX_BODY_BYTES = 16 << 20
_MAX_PRIVATE_RESPONSE_BYTES = 101 << 20


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


def _pre_io_rejection(name: str, argv: list[str]) -> dict[str, Any]:
    return {
        "argv": argv,
        "backend_opens": 0,
        "builds": 0,
        "case": name,
        "category": _COMMAND_CATEGORY,
        "code": _INVALID_ARGUMENTS,
        "project_discoveries": 0,
        "renderer_observations": 0,
        "source_loads": 0,
    }


def argv_and_pre_io_rejection(contract_id: str) -> dict[str, Any]:
    accepted = [
        {
            "app": "blog",
            "argv": ["sqlmigrate", "blog", "0002_render_sql"],
            "migration_name": "0002_render_sql",
            "project": "discovered_default",
        },
        {
            "app": "blog",
            "argv": [
                "sqlmigrate",
                "blog",
                "0002_render_sql",
                "--project",
                "./godj.toml",
            ],
            "migration_name": "0002_render_sql",
            "project": "./godj.toml",
        },
    ]
    rejected = [
        _pre_io_rejection("app_only", ["sqlmigrate", "blog"]),
        _pre_io_rejection(
            "project_before_identity",
            ["sqlmigrate", "--project", "./godj.toml", "blog", "0002_render_sql"],
        ),
        _pre_io_rejection(
            "missing_project_path",
            ["sqlmigrate", "blog", "0002_render_sql", "--project"],
        ),
        _pre_io_rejection(
            "latest_reserved",
            ["sqlmigrate", "blog", "latest"],
        ),
        _pre_io_rejection(
            "backwards_option",
            ["sqlmigrate", "blog", "0002_render_sql", "--backwards"],
        ),
        _pre_io_rejection(
            "reverse_option",
            ["sqlmigrate", "blog", "0002_render_sql", "--reverse"],
        ),
        _pre_io_rejection(
            "leading_dash_app",
            ["sqlmigrate", "--blog", "0002_render_sql"],
        ),
        _pre_io_rejection(
            "leading_dash_name",
            ["sqlmigrate", "blog", "--0002"],
        ),
        _pre_io_rejection(
            "unknown_trailing_option",
            ["sqlmigrate", "blog", "0002_render_sql", "--database", "other"],
        ),
    ]
    return _observed(
        contract_id,
        phase="environment",
        result={
            "accepted": accepted,
            "exact_public_forms": 2,
            "migration_name_resolution": "exact_only",
            "rejected": rejected,
            "zero_name_policy": "literal_exact_name",
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
            "rejected_forms": len(rejected),
            "renderer_observations_for_rejected": sum(
                case["renderer_observations"] for case in rejected
            ),
            "source_loads_for_rejected": sum(case["source_loads"] for case in rejected),
        },
    )


def complete_load_exact_lookup_and_request(contract_id: str) -> dict[str, Any]:
    stages = [
        "complete_definition_load",
        "graph_validation",
        "chronology_validation",
        "exact_target_lookup",
        "target_before_state_reconstruction",
        "forward_request_materialization",
        "renderer_validation",
        "render_once",
    ]
    request = {
        "app": "blog",
        "direction": "forward",
        "intent": {
            "operations": [
                {"kind": "CreateModel", "subject": "blog.Category"},
                {"kind": "AddField", "subject": "blog.Article.summary"},
            ],
            "state_basis": "target_dependency_before",
        },
        "name": "0002_render_sql",
    }
    failures = [
        {
            "case": "invalid_unrelated_definition",
            "failed_stage": "complete_definition_load",
            "renderer_calls": 0,
            "request_materializations": 0,
        },
        {
            "case": "prefix_looking_exact_miss",
            "failed_stage": "exact_target_lookup",
            "renderer_calls": 0,
            "request_materializations": 0,
            "requested_name": "0002",
        },
        {
            "case": "renderer_unavailable",
            "failed_stage": "renderer_validation",
            "renderer_calls": 0,
            "request_materializations": 1,
        },
    ]
    return _observed(
        contract_id,
        phase="construction",
        result={
            "detached_request": True,
            "failures": failures,
            "operation_order_preserved": True,
            "request": request,
            "request_zero_value_valid": False,
            "stages": stages,
            "target_identity_preserved": True,
        },
        metrics={
            "complete_catalog_loads": 1,
            "history_reads": 0,
            "renderer_calls": 1,
            "request_materializations": 1,
            "target_migrations": 1,
            "transactions": 0,
        },
    )


def postgres_current_projection(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        phase="construction",
        result={
            "configuration": {
                "immutable": True,
                "inputs": ["schema"],
                "schema": "public",
            },
            "forbidden_configuration_inputs": [
                "database_url",
                "credential",
                "database_handle",
                "server_connection",
            ],
            "normalized_operations": [
                {
                    "kind": "create_table",
                    "schema": "public",
                    "table": "blog_category",
                },
                {
                    "column": "summary",
                    "kind": "add_column",
                    "schema": "public",
                    "table": "blog_article",
                },
            ],
            "raw_sql_bytes_are_reference_contract": False,
            "schema_qualified": True,
        },
        metrics={
            "backend_opens": 0,
            "catalog_reads": 0,
            "credential_values": 0,
            "history_reads": 0,
            "network_calls": 0,
            "renderer_constructions": 1,
            "server_profile_reads": 0,
        },
    )


def canonical_deterministic_output(contract_id: str) -> dict[str, Any]:
    bodies = [
        'CREATE TABLE "blog_category" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "name" VARCHAR(80) NOT NULL)',
        'ALTER TABLE "blog_article" ADD COLUMN "summary" VARCHAR(120) NULL',
    ]
    output = "".join(f"{body};\n" for body in bodies)
    observations = [
        {"case": "first", "output": output},
        {"case": "repeat", "output": output},
        {"case": "parallel_a", "output": output},
        {"case": "parallel_b", "output": output},
        {"case": "fresh_process", "output": output},
    ]
    return _observed(
        contract_id,
        phase="evaluation",
        result={
            "bodies": bodies,
            "empty_intent": {
                "internal_result": "non_nil_empty",
                "output": "",
                "stdout_write_attempts": 0,
            },
            "exact_statement_cardinality": True,
            "global_terminator": ";\n",
            "observations": observations,
            "output_owner": "global_command",
            "renderer_bodies_contain_semicolon": False,
        },
        metrics={
            "distinct_nonempty_outputs": len({case["output"] for case in observations}),
            "fresh_process_observations": 1,
            "operations": len(bodies),
            "parallel_observations": 2,
            "repeat_observations": 2,
            "statements": len(bodies),
        },
    )


def _zero_call_case(name: str, outcome: str) -> dict[str, Any]:
    return {
        "backend_opens": 0,
        "case": name,
        "history_reads": 0,
        "migration_begins": 0,
        "outcome": outcome,
        "recorder_calls": 0,
        "revision_fence_calls": 0,
        "schema_editor_calls": 0,
        "schema_mutations": 0,
        "session_opens": 0,
        "transaction_begins": 0,
    }


def database_and_history_zero_calls(contract_id: str) -> dict[str, Any]:
    cases = [
        _zero_call_case("success", "success"),
        _zero_call_case("render_failure", "error"),
        _zero_call_case("canceled", "error"),
    ]
    zero_fields = (
        "backend_opens",
        "history_reads",
        "migration_begins",
        "recorder_calls",
        "revision_fence_calls",
        "schema_editor_calls",
        "schema_mutations",
        "session_opens",
        "transaction_begins",
    )
    return _observed(
        contract_id,
        phase="environment",
        result={
            "built_in_db_free_scope": (
                "framework_and_builtin_renderer_database_lifecycle_only"
            ),
            "cases": cases,
            "custom_renderer_io_is_proven_absent": False,
            "offline_or_sandboxed_claimed": False,
        },
        db_state={
            "after": {
                "database": "not_opened",
                "history": "not_read",
                "recorder": "not_observed",
                "schema": "not_observed",
            },
            "before": {
                "database": "not_opened",
                "history": "not_read",
                "recorder": "not_observed",
                "schema": "not_observed",
            },
        },
        metrics={
            "cases": len(cases),
            **{field: sum(case[field] for case in cases) for field in zero_fields},
        },
    )


def renderer_and_operation_fail_closed(contract_id: str) -> dict[str, Any]:
    cases = [
        {
            "case": "nil_renderer",
            "category": _RENDER_CATEGORY,
            "code": "renderer_unavailable",
            "exit_code": 3,
            "renderer_calls": 0,
        },
        {
            "case": "typed_nil_renderer",
            "category": _RENDER_CATEGORY,
            "code": "renderer_unavailable",
            "exit_code": 3,
            "renderer_calls": 0,
        },
        {
            "case": "unsupported_operation",
            "category": _CAPABILITY_CATEGORY,
            "code": "unsupported_operation",
            "exit_code": 1,
            "renderer_calls": 1,
        },
        {
            "case": "custom_data_operation",
            "category": _CAPABILITY_CATEGORY,
            "code": "unsupported_operation",
            "exit_code": 1,
            "renderer_calls": 1,
        },
        {
            "case": "renderer_returned_error",
            "category": _RENDER_CATEGORY,
            "code": "render_failed",
            "exit_code": 3,
            "partial_renderer_sql_returned": True,
            "raw_cause_contains_secret": True,
            "renderer_calls": 1,
        },
        {
            "case": "malformed_empty_body",
            "category": _RENDER_CATEGORY,
            "code": "invalid_rendered_sql",
            "exit_code": 3,
            "renderer_calls": 1,
        },
        {
            "case": "malformed_invalid_utf8_body",
            "category": _RENDER_CATEGORY,
            "code": "invalid_rendered_sql",
            "exit_code": 3,
            "renderer_calls": 1,
        },
        {
            "case": "malformed_leading_ascii_whitespace_body",
            "category": _RENDER_CATEGORY,
            "code": "invalid_rendered_sql",
            "exit_code": 3,
            "renderer_calls": 1,
        },
        {
            "case": "malformed_trailing_ascii_whitespace_body",
            "category": _RENDER_CATEGORY,
            "code": "invalid_rendered_sql",
            "exit_code": 3,
            "renderer_calls": 1,
        },
        {
            "case": "malformed_semicolon_body",
            "category": _RENDER_CATEGORY,
            "code": "invalid_rendered_sql",
            "exit_code": 3,
            "renderer_calls": 1,
        },
        {
            "case": "malformed_control_rune_body",
            "category": _RENDER_CATEGORY,
            "code": "invalid_rendered_sql",
            "exit_code": 3,
            "renderer_calls": 1,
        },
        {
            "case": "malformed_cardinality",
            "category": _RENDER_CATEGORY,
            "code": "invalid_rendered_sql",
            "exit_code": 3,
            "renderer_calls": 1,
        },
    ]
    cases = [
        {
            **case,
            "logical_sql_bytes_published": 0,
            "partial_renderer_sql_published": False,
            "raw_cause_retained": False,
            "unwrap_exposes_raw_cause": False,
            "partial_renderer_sql_returned": case.get(
                "partial_renderer_sql_returned", False
            ),
            "raw_cause_contains_secret": case.get("raw_cause_contains_secret", False),
        }
        for case in cases
    ]
    return _observed(
        contract_id,
        phase="evaluation",
        result={
            "cases": cases,
            "reverse_argv_owned_by": "MIG-129",
        },
        metrics={
            "cases": len(cases),
            "logical_sql_bytes_published": sum(
                case["logical_sql_bytes_published"] for case in cases
            ),
            "renderer_calls": sum(case["renderer_calls"] for case in cases),
            "typed_nil_method_calls": 0,
        },
    )


def resource_cleanup_redaction_and_write(contract_id: str) -> dict[str, Any]:
    resource_cases = [
        {
            "accepted": True,
            "case": "statement_count_exact_limit",
            "limit": _MAX_STATEMENTS,
            "observed": _MAX_STATEMENTS,
        },
        {
            "accepted": False,
            "case": "statement_count_one_over",
            "category": _RESOURCE_CATEGORY,
            "code": "rendered_sql_resource_limit",
            "limit": _MAX_STATEMENTS,
            "observed": _MAX_STATEMENTS + 1,
        },
        {
            "accepted": True,
            "case": "aggregate_body_bytes_exact_limit",
            "limit": _MAX_BODY_BYTES,
            "observed": _MAX_BODY_BYTES,
        },
        {
            "accepted": False,
            "case": "aggregate_body_bytes_one_over",
            "category": _RESOURCE_CATEGORY,
            "code": "rendered_sql_resource_limit",
            "limit": _MAX_BODY_BYTES,
            "observed": _MAX_BODY_BYTES + 1,
        },
        {
            "accepted": True,
            "case": "private_response_exact_limit",
            "limit": _MAX_PRIVATE_RESPONSE_BYTES,
            "observed": _MAX_PRIVATE_RESPONSE_BYTES,
        },
        {
            "accepted": False,
            "case": "private_response_one_over",
            "category": _RESOURCE_CATEGORY,
            "code": "rendered_sql_resource_limit",
            "limit": _MAX_PRIVATE_RESPONSE_BYTES,
            "observed": _MAX_PRIVATE_RESPONSE_BYTES + 1,
        },
    ]
    write_cases = [
        {
            "case": "success",
            "physical_prefix_may_be_visible": False,
            "retries": 0,
            "stderr_republications": 0,
            "write_attempts": 1,
        },
        {
            "case": "short_write",
            "physical_prefix_may_be_visible": True,
            "retries": 0,
            "stderr_republications": 0,
            "write_attempts": 1,
        },
        {
            "case": "write_error",
            "physical_prefix_may_be_visible": True,
            "retries": 0,
            "stderr_republications": 0,
            "write_attempts": 1,
        },
    ]
    return _observed(
        contract_id,
        phase="environment",
        result={
            "child_cleanup": {
                "bounded": True,
                "process_group_absence_verified": True,
            },
            "logical_output_validated_before_write": True,
            "os_atomic_write_claimed": False,
            "redaction": [
                {"field": "raw_renderer_error", "published": False},
                {"field": "partial_sql", "published": False},
                {"field": "definition_source", "published": False},
                {"field": "database_url_or_credential", "published": False},
                {"field": "child_stderr", "published": False},
            ],
            "resource_cases": resource_cases,
            "scan_order": ["resource_bounds", "semantic_shape"],
            "write_cases": write_cases,
        },
        metrics={
            "automatic_retries": sum(case["retries"] for case in write_cases),
            "cleanup_failures": 0,
            "one_over_rejections": sum(not case["accepted"] for case in resource_cases),
            "redacted_fields_published": 0,
            "stderr_republications": sum(
                case["stderr_republications"] for case in write_cases
            ),
            "write_attempts": sum(case["write_attempts"] for case in write_cases),
        },
    )


def external_project_configuration(contract_id: str) -> dict[str, Any]:
    compile_cases = [
        {
            "case": "sqlite_constructor_assignment",
            "compiles": True,
            "repository_external": True,
        },
        {
            "case": "postgres_schema_constructor_assignment",
            "compiles": True,
            "repository_external": True,
        },
        {
            "case": "keyed_project_config_literal",
            "compiles": True,
            "repository_external": True,
        },
        {
            "case": "unkeyed_project_config_source_impact",
            "current_only_source_change_observed": True,
            "repository_external": True,
        },
    ]
    return _observed(
        contract_id,
        phase="environment",
        result={
            "compile_cases": compile_cases,
            "custom_opener_renderer_coherence_proven": False,
            "direct_project_config_field": "MigrationSQLRenderer",
            "postgres_constructor_inputs": ["schema"],
            "renderer_and_opener_derived_from_one_builtin_selection": True,
            "sqlite_constructor_inputs": [],
            "supported_builtin_renderer_db_free": True,
        },
        db_state={
            "after": {
                "database": "not_opened",
                "history": "not_read",
                "schema": "not_observed",
            },
            "before": {
                "database": "not_opened",
                "history": "not_read",
                "schema": "not_observed",
            },
        },
        metrics={
            "backend_opens": 0,
            "compile_cases": len(compile_cases),
            "credential_values": 0,
            "database_handles": 0,
            "history_reads": 0,
            "network_calls": 0,
            "schema_editor_calls": 0,
        },
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "godj.migration.sql_rendering.argv_and_pre_io_rejection": (
        argv_and_pre_io_rejection
    ),
    "godj.migration.sql_rendering.complete_load_exact_lookup_and_request": (
        complete_load_exact_lookup_and_request
    ),
    "godj.migration.sql_rendering.postgres_current_projection": (
        postgres_current_projection
    ),
    "godj.migration.sql_rendering.canonical_deterministic_output": (
        canonical_deterministic_output
    ),
    "godj.migration.sql_rendering.database_and_history_zero_calls": (
        database_and_history_zero_calls
    ),
    "godj.migration.sql_rendering.renderer_and_operation_fail_closed": (
        renderer_and_operation_fail_closed
    ),
    "godj.migration.sql_rendering.resource_cleanup_redaction_and_write": (
        resource_cleanup_redaction_and_write
    ),
    "godj.migration.sql_rendering.external_project_configuration": (
        external_project_configuration
    ),
}
