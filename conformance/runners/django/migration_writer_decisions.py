"""Independent GoDj decisions for deterministic migration publication."""

from __future__ import annotations

import hashlib
import json
from collections.abc import Callable
from typing import Any

from .normalizer import normalize


def _observed(
    contract_id: str,
    result: Any,
    *,
    phase: str,
    metrics: Any,
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


def _sample_document() -> bytes:
    document = {
        "format_version": 1,
        "producer": {"name": "godj-makemigrations", "version": "1"},
        "migration": {
            "app": "blog",
            "name": "0002_article_summary",
            "dependencies": [{"app": "blog", "name": "0001_initial"}],
            "operations": [
                {
                    "kind": "add_field",
                    "app_label": "blog",
                    "model_name": "article",
                    "field": {
                        "name": "summary",
                        "go_name": "Summary",
                        "column": "summary",
                        "kind": "char",
                        "primary_key": False,
                        "nullable": True,
                        "max_length": 200,
                        "default": None,
                    },
                }
            ],
        },
    }
    return (
        json.dumps(document, ensure_ascii=False, separators=(",", ":")) + "\n"
    ).encode("utf-8")


def deterministic_candidate(contract_id: str) -> dict[str, Any]:
    document = _sample_document()
    digest = "sha256:" + hashlib.sha256(document).hexdigest()
    cases = [
        {"case": "normal", "document": document, "sha256": digest},
        {"case": "reverse_input", "document": document, "sha256": digest},
        {"case": "different_process", "document": document, "sha256": digest},
        {"case": "different_time", "document": document, "sha256": digest},
    ]
    return _observed(
        contract_id,
        {
            "cases": cases,
            "filename": "blog_0002_article_summary.godj.json",
            "roster": ["blog_0002_article_summary.godj.json"],
            "timestamp_fields": 0,
        },
        phase="construction",
        metrics={
            "candidate_documents": 1,
            "distinct_documents": len({case["document"] for case in cases}),
            "input_permutations": len(cases),
            "random_values": 0,
        },
    )


def unsupported_delta_fail_closed(contract_id: str) -> dict[str, Any]:
    cases = [
        {"case": "model_removal", "code": "unsupported_delta"},
        {"case": "field_removal", "code": "unsupported_delta"},
        {"case": "field_reorder", "code": "unsupported_delta"},
        {"case": "field_rename", "code": "unsupported_delta"},
        {"case": "field_alter", "code": "unsupported_delta"},
        {"case": "self_or_cyclic_relation", "code": "relation_cycle"},
        {"case": "noncanonical_leaf", "code": "noncanonical_leaf"},
    ]
    return _observed(
        contract_id,
        {
            "cases": [
                {
                    **case,
                    "candidate_count": 0,
                    "category": "migration_autodetect_error",
                    "existing_sources_mutated": False,
                }
                for case in cases
            ],
            "partial_success": False,
        },
        phase="construction",
        metrics={
            "database_opens": 0,
            "failures": len(cases),
            "published_files": 0,
            "writes": 0,
        },
    )


def snapshot_and_protocol_boundary(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "catalog_and_schema_snapshot": "one_private_request",
            "existing_protocol_bytes_changed": False,
            "request_format_version": 1,
            "response_contains_candidate_bytes": True,
            "response_contains_database_configuration": False,
            "strict_unknown_member_policy": "reject",
        },
        phase="environment",
        metrics={
            "catalog_snapshots": 1,
            "database_opens": 0,
            "private_requests": 1,
            "schema_snapshots": 1,
            "secret_values_serialized": 0,
        },
    )


def atomic_concurrent_publication(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "cases": [
                {
                    "case": "writer_a_wins",
                    "complete_visible_files": 1,
                    "overwrites": 0,
                    "published": 1,
                },
                {
                    "case": "writer_b_replans",
                    "complete_visible_files": 1,
                    "overwrites": 0,
                    "published": 0,
                },
            ],
            "final_catalog": "strict_loadable",
            "stale_false_success": False,
        },
        phase="commit",
        metrics={
            "lock_acquisitions": 2,
            "replans_under_lock": 2,
            "stale_publications": 0,
            "writers": 2,
        },
    )


def interruption_recovery_and_roundtrip(contract_id: str) -> dict[str, Any]:
    return _observed(
        contract_id,
        {
            "cases": [
                {
                    "case": "cancel_before_rename",
                    "durable_prefix": 0,
                    "strict_loadable": True,
                    "visible_partial_files": 0,
                },
                {
                    "case": "fault_after_first_candidate",
                    "durable_prefix": 1,
                    "strict_loadable": True,
                    "visible_partial_files": 0,
                },
                {
                    "case": "fresh_resume",
                    "desired_state_equal": True,
                    "remaining_candidates_published": 1,
                    "strict_loadable": True,
                },
            ],
            "existing_sources_mutated": False,
            "unsafe_residue": 0,
        },
        phase="rollback",
        metrics={
            "automatic_retries": 0,
            "fresh_invocations": 1,
            "resume_plans": 1,
            "temporary_residue": 0,
        },
    )


SCENARIOS: dict[str, Callable[[str], dict[str, Any]]] = {
    "godj.migration.writer.deterministic_candidate": deterministic_candidate,
    "godj.migration.writer.unsupported_delta_fail_closed": unsupported_delta_fail_closed,
    "godj.migration.writer.snapshot_and_protocol_boundary": snapshot_and_protocol_boundary,
    "godj.migration.writer.atomic_concurrent_publication": atomic_concurrent_publication,
    "godj.migration.writer.interruption_recovery_and_roundtrip": interruption_recovery_and_roundtrip,
}
