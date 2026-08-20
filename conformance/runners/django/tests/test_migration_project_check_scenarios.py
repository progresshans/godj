from __future__ import annotations

import ast
import inspect
import json
import unittest
from pathlib import Path

from conformance.runners.django import migration_project_check_scenarios as scenarios
from conformance.runners.django.normalizer import canonical_json


ROOT = Path(__file__).resolve().parents[4]
MANIFEST = ROOT / "conformance/contracts/migration-project-check-manifest.json"
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64"
    / "migration-project-check-oracle.json"
)
STATIC = (
    ROOT
    / "conformance/fixtures/godj-migration-project-check-not-implemented.json"
)


def denormalize(value):
    value_type = value["type"]
    if value_type == "null":
        return None
    if value_type in {"bool", "string"}:
        return value["value"]
    if value_type == "int":
        return int(value["value"])
    if value_type == "list":
        return [denormalize(item) for item in value["items"]]
    if value_type == "object":
        return {
            field["name"]: denormalize(field["value"])
            for field in value["fields"]
        }
    raise AssertionError(f"unexpected normalized value type: {value_type!r}")


def observed(scenario, contract_id):
    observation = scenario(contract_id)
    return {
        "error": observation["error"],
        "metrics": denormalize(observation["metrics"]),
        "phase": observation["phase"],
        "raw": observation,
        "result": (
            denormalize(observation["result"])
            if observation["result"] is not None
            else None
        ),
    }


def metric_row(**updates):
    row = {
        field: None if field == "failure" else 0
        for field in scenarios.METRIC_FIELDS
    }
    row.update(updates)
    return row


class MigrationProjectCheckScenarioTests(unittest.TestCase):
    def test_registry_order_matches_mig_065_through_074(self) -> None:
        self.assertEqual(
            list(scenarios.SCENARIOS),
            [
                "godj.migration.project_check.nested_project_success",
                "godj.migration.project_check.explicit_project_override",
                "godj.migration.project_check.empty_catalog",
                "godj.migration.project_check.canonical_filesystem_order",
                "godj.migration.project_check.unsafe_source_entry",
                "godj.migration.project_check.project_not_found",
                "godj.migration.project_check.project_protocol_incompatible",
                "godj.migration.project_check.project_build_failure_atomic",
                "godj.migration.project_check.definition_load_failure",
                "godj.migration.project_check.invalid_runner_response",
            ],
        )

    def test_manifest_locks_mapping_phases_comparisons_and_decision_provenance(
        self,
    ) -> None:
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        self.assertEqual(manifest["format_version"], 2)
        self.assertEqual(
            manifest["profile_id"],
            "django-6.1-sqlite-darwin-arm64",
        )
        self.assertEqual(
            [contract["id"] for contract in manifest["contracts"]],
            [f"MIG-{number:03d}" for number in range(65, 75)],
        )
        self.assertEqual(
            [contract["scenario"] for contract in manifest["contracts"]],
            list(scenarios.SCENARIOS),
        )
        self.assertEqual(
            [contract["phase"] for contract in manifest["contracts"]],
            [
                "environment",
                "environment",
                "construction",
                "construction",
                "construction",
                "environment",
                "environment",
                "environment",
                "construction",
                "environment",
            ],
        )
        self.assertEqual(
            [contract["comparison"] for contract in manifest["contracts"]],
            [["result", "metrics"]] * 4 + [["error", "metrics"]] * 6,
        )
        current_format_ids = {"MIG-065", "MIG-066", "MIG-067", "MIG-068", "MIG-073"}
        for contract in manifest["contracts"]:
            with self.subTest(contract=contract["id"]):
                self.assertEqual(contract["status"], "passing")
                references = ["ADR-0021"]
                if contract["id"] in current_format_ids:
                    references.append("ADR-0035")
                self.assertEqual(
                    contract["provenance"],
                    [
                        {"kind": "decision", "reference": reference, "derived": False}
                        for reference in references
                    ],
                )

    def test_static_fixture_is_explicitly_not_implemented(self) -> None:
        fixture = json.loads(STATIC.read_text(encoding="utf-8"))
        self.assertEqual(fixture["format_version"], 2)
        self.assertEqual(
            fixture["profile"]["id"],
            "django-6.1-sqlite-darwin-arm64",
        )
        self.assertEqual(
            [item["id"] for item in fixture["contracts"]],
            [f"MIG-{number:03d}" for number in range(65, 75)],
        )
        self.assertEqual(
            {item["status"] for item in fixture["contracts"]},
            {"not_implemented"},
        )
        self.assertEqual(
            [item["phase"] for item in fixture["contracts"]],
            [
                "environment",
                "environment",
                "construction",
                "construction",
                "construction",
                "environment",
                "environment",
                "environment",
                "construction",
                "environment",
            ],
        )

    def test_every_public_scenario_is_byte_deterministic_and_contract_id_agnostic(
        self,
    ) -> None:
        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 65):
            with self.subTest(scenario=scenario.__name__):
                expected = scenario(f"MIG-{number:03d}")
                arbitrary = scenario("untrusted-contract")
                self.assertEqual(expected["id"], f"MIG-{number:03d}")
                self.assertEqual(arbitrary["id"], "untrusted-contract")
                self.assertEqual(
                    {key: value for key, value in expected.items() if key != "id"},
                    {
                        key: value
                        for key, value in arbitrary.items()
                        if key != "id"
                    },
                )
                self.assertEqual(
                    canonical_json(scenario(f"MIG-{number:03d}")),
                    canonical_json(scenario(f"MIG-{number:03d}")),
                )

    def test_scenario_source_has_no_artifact_dispatch_or_runtime_probe(self) -> None:
        text = inspect.getsource(scenarios)
        syntax = ast.parse(text)
        called_attributes = {
            node.func.attr
            for node in ast.walk(syntax)
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
        }
        called_names = {
            node.func.id
            for node in ast.walk(syntax)
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
        }
        for forbidden in {
            "migration-project-check-oracle",
            "godj-migration-project-check-not-implemented",
            "migrate --check",
            "makemigrations --check",
        }:
            self.assertNotIn(forbidden, text)
        self.assertTrue(
            {
                "open",
                "read_bytes",
                "read_text",
                "write_bytes",
                "write_text",
                "run",
                "Popen",
            }.isdisjoint(called_attributes | called_names)
        )
        self.assertTrue({"eval", "exec", "compile"}.isdisjoint(called_names))

    def test_base_results_errors_and_exact_24_field_metrics(self) -> None:
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
        cases = [
            (
                scenarios.nested_project_success,
                "environment",
                {
                    "source_count": 1,
                    "definition_count": 1,
                    "definition_set_digest": scenarios.ONE_MODEL_DEFINITION_SET_DIGEST,
                },
                None,
                metric_row(
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
            ),
            (
                scenarios.explicit_project_override,
                "environment",
                {
                    "source_count": 1,
                    "definition_count": 1,
                    "definition_set_digest": scenarios.ONE_MODEL_DEFINITION_SET_DIGEST,
                },
                None,
                metric_row(
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
            ),
            (
                scenarios.empty_catalog,
                "construction",
                {
                    "source_count": 0,
                    "definition_count": 0,
                    "definition_set_digest": scenarios.EMPTY_DEFINITION_SET_DIGEST,
                },
                None,
                metric_row(
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
            ),
            (
                scenarios.canonical_filesystem_order,
                "construction",
                {
                    "source_count": 2,
                    "definition_count": 2,
                    "definition_set_digest": scenarios.TWO_SOURCE_DEFINITION_SET_DIGEST,
                },
                None,
                metric_row(
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
            ),
            (
                scenarios.unsafe_source_entry,
                "construction",
                None,
                (
                    "migration_definition_discovery_error",
                    "unsafe_source_entry",
                ),
                metric_row(
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
            ),
            (
                scenarios.project_not_found,
                "environment",
                None,
                ("migration_project_selection_error", "project_not_found"),
                metric_row(
                    user_stderr_writes=1,
                    exit_code=2,
                    ancestor_directories_inspected=4,
                ),
            ),
            (
                scenarios.project_protocol_incompatible,
                "environment",
                None,
                (
                    "migration_project_protocol_error",
                    "project_protocol_incompatible",
                ),
                metric_row(
                    build_calls=1,
                    runner_calls=1,
                    runner_response_writes=1,
                    user_stderr_writes=1,
                    exit_code=3,
                    ancestor_directories_inspected=1,
                    descriptor_reads=1,
                ),
            ),
            (
                scenarios.project_build_failure_atomic,
                "environment",
                None,
                ("migration_project_build_error", "project_build_failed"),
                metric_row(
                    build_calls=1,
                    user_stderr_writes=1,
                    exit_code=3,
                    ancestor_directories_inspected=1,
                    descriptor_reads=1,
                ),
            ),
            (
                scenarios.definition_load_failure,
                "construction",
                None,
                (
                    "migration_definition_source_error",
                    "invalid_definition_document",
                ),
                metric_row(
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
            ),
            (
                scenarios.invalid_runner_response,
                "environment",
                None,
                (
                    "migration_project_protocol_error",
                    "invalid_project_runner_response",
                ),
                metric_row(
                    build_calls=1,
                    runner_calls=1,
                    runner_response_writes=1,
                    user_stderr_writes=1,
                    exit_code=3,
                    ancestor_directories_inspected=1,
                    descriptor_reads=1,
                ),
            ),
        ]

        for number, case in enumerate(cases, 65):
            scenario, phase, expected_result, expected_error, expected_metrics = case
            with self.subTest(contract=f"MIG-{number:03d}"):
                value = observed(scenario, f"MIG-{number:03d}")
                self.assertEqual(value["phase"], phase)
                self.assertEqual(value["result"], expected_result)
                self.assertEqual(value["metrics"], expected_metrics)
                self.assertEqual(
                    tuple(expected_metrics),
                    scenarios.METRIC_FIELDS,
                )
                if expected_error is None:
                    self.assertIsNone(value["error"])
                else:
                    self.assertEqual(
                        (
                            value["error"]["category"],
                            value["error"]["code"],
                        ),
                        expected_error,
                    )
                    self.assertIs(value["error"]["message_is_contract"], False)

    def test_machine_oracle_has_only_the_exact_base_observation_fields(self) -> None:
        oracle = json.loads(ORACLE.read_text(encoding="utf-8"))
        self.assertEqual(
            [contract["id"] for contract in oracle["contracts"]],
            [f"MIG-{number:03d}" for number in range(65, 75)],
        )
        for contract in oracle["contracts"]:
            with self.subTest(contract=contract["id"]):
                self.assertEqual(
                    set(contract),
                    {
                        "db_state",
                        "error",
                        "id",
                        "metrics",
                        "phase",
                        "result",
                        "status",
                    },
                )
                metrics = denormalize(contract["metrics"])
                self.assertEqual(set(metrics), set(scenarios.METRIC_FIELDS))
                self.assertEqual(len(metrics), 24)
                self.assertNotIn("temp_created", metrics)
                self.assertNotIn("retained_bytes", metrics)
                self.assertNotIn("truncated", metrics)


if __name__ == "__main__":
    unittest.main()
