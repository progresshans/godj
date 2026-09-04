from __future__ import annotations

import json
import unittest
from pathlib import Path
from typing import Any

from conformance.runners.django import migration_writer_scenarios as scenarios
from conformance.runners.django.normalizer import canonical_json


EXPECTED_SCENARIOS = (
    "django.migration.writer.no_changes_clean",
    "django.migration.writer.fresh_initial",
    "django.migration.writer.repeat_after_initial_noop",
    "django.migration.writer.relation_dependency_topology",
    "django.migration.writer.additive_model_and_field_tail",
    "django.migration.writer.dry_run_no_mutation",
    "django.migration.writer.check_clean_and_drift",
)
ROOT = Path(__file__).resolve().parents[4]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = ROOT / "conformance/contracts/migration-writer-manifest.json"
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64"
    / "migration-writer-oracle.json"
)


def _semantic(value: Any) -> Any:
    if value is None or not isinstance(value, dict) or "type" not in value:
        return value
    kind = value["type"]
    if kind == "object":
        return {
            field["name"]: _semantic(field["value"])
            for field in value["fields"]
        }
    if kind == "list":
        return [_semantic(item) for item in value["items"]]
    if kind == "null":
        return None
    if kind == "int":
        return int(value["value"])
    return value["value"]


class MigrationWriterScenarioTests(unittest.TestCase):
    def test_registry_is_exact_and_observations_are_deterministic(self) -> None:
        self.assertEqual(tuple(scenarios.SCENARIOS), EXPECTED_SCENARIOS)
        contract_ids = (
            "MIG-099",
            "MIG-100",
            "MIG-101",
            "MIG-103",
            "MIG-104",
            "MIG-105",
            "MIG-106",
        )
        for contract_id, scenario in zip(
            contract_ids, scenarios.SCENARIOS.values(), strict=True
        ):
            with self.subTest(contract=contract_id):
                first = canonical_json(scenario(contract_id))
                second = canonical_json(scenario(contract_id))
                self.assertEqual(first, second)

    def test_no_change_initial_and_repeat_boundaries_are_exact(self) -> None:
        clean = scenarios.no_changes_clean("MIG-099")
        self.assertEqual(
            _semantic(clean["result"]),
            {"candidate_count": 0, "clean": True},
        )

        initial = _semantic(scenarios.fresh_initial("MIG-100")["result"])
        self.assertEqual(len(initial["migrations"]), 1)
        self.assertEqual(initial["migrations"][0]["app"], "blog")
        self.assertTrue(initial["migrations"][0]["initial"])
        self.assertEqual(
            [operation["kind"] for operation in initial["migrations"][0]["operations"]],
            ["CreateModel"],
        )

        repeat = _semantic(
            scenarios.repeat_after_initial_noop("MIG-101")["result"]
        )
        self.assertEqual(
            repeat,
            {
                "candidate_count": 0,
                "prior_source_mutated": False,
                "repeat_is_noop": True,
            },
        )

    def test_supported_relation_and_additive_topology_are_bounded(self) -> None:
        relation = _semantic(
            scenarios.relation_dependency_topology("MIG-103")["result"]
        )
        self.assertEqual(
            [case["case"] for case in relation["cases"]],
            ["same_app", "cross_app"],
        )
        cross = relation["cases"][1]["migrations"]
        self.assertEqual([migration["app"] for migration in cross], ["authors", "blog"])
        self.assertEqual(
            cross[1]["dependencies"],
            [{"app": "authors", "name": "0001_initial"}],
        )

        additive = _semantic(
            scenarios.additive_model_and_field_tail("MIG-104")["result"]
        )
        self.assertEqual(len(additive["migrations"]), 1)
        migration = additive["migrations"][0]
        self.assertEqual(
            migration["dependencies"],
            [{"app": "blog", "name": "0001_initial"}],
        )
        self.assertEqual(
            [operation["kind"] for operation in migration["operations"]],
            ["CreateModel", "AddField", "AddField"],
        )
        self.assertEqual(
            [
                operation["field"]["name"]
                for operation in migration["operations"]
                if operation["kind"] == "AddField"
            ],
            ["summary", "category"],
        )

    def test_command_modes_are_observed_in_isolated_processes_without_mutation(self) -> None:
        dry = _semantic(scenarios.dry_run_no_mutation("MIG-105")["result"])
        self.assertEqual(dry["action"], "dry_run")
        self.assertEqual(dry["exit_code"], 0)
        self.assertEqual(dry["files_before"], dry["files_after"])
        self.assertEqual(dry["tables_before"], dry["tables_after"])
        self.assertTrue(any("0001_initial.py" in line for line in dry["output"]))

        check = _semantic(
            scenarios.check_clean_and_drift("MIG-106")["result"]
        )
        clean, drift = check["cases"]
        self.assertEqual((clean["action"], clean["exit_code"]), ("check_clean", 0))
        self.assertEqual((drift["action"], drift["exit_code"]), ("check_drift", 1))
        for case in (clean, drift):
            self.assertEqual(case["files_before"], case["files_after"])
            self.assertEqual(case["tables_before"], case["tables_after"])

    def test_scenario_source_is_expected_artifact_blind(self) -> None:
        source = Path(scenarios.__file__).read_text(encoding="utf-8")
        for forbidden in (
            "conformance/contracts",
            "conformance/oracles",
            "conformance/fixtures",
            "not_implemented",
            "not-implemented",
        ):
            self.assertNotIn(forbidden, source)
        json.loads(canonical_json(scenarios.no_changes_clean("MIG-099")))

    def test_mixed_authority_registry_and_checked_oracle_are_exact(self) -> None:
        from conformance.runners.django import runner

        expected = (
            EXPECTED_SCENARIOS[0],
            EXPECTED_SCENARIOS[1],
            EXPECTED_SCENARIOS[2],
            "godj.migration.writer.deterministic_candidate",
            EXPECTED_SCENARIOS[3],
            EXPECTED_SCENARIOS[4],
            EXPECTED_SCENARIOS[5],
            EXPECTED_SCENARIOS[6],
            "godj.migration.writer.unsupported_delta_fail_closed",
            "godj.migration.writer.snapshot_and_protocol_boundary",
            "godj.migration.writer.atomic_concurrent_publication",
            "godj.migration.writer.interruption_recovery_and_roundtrip",
        )
        self.assertEqual(tuple(runner.MIGRATION_WRITER_SCENARIOS), expected)
        self.assertEqual(runner.DEFAULT_MIGRATION_WRITER_MANIFEST, MANIFEST)
        self.assertEqual(runner.DEFAULT_MIGRATION_WRITER_ORACLE, ORACLE)

        profile = json.loads(PROFILE.read_text(encoding="utf-8"))
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        suite = {
            "format_version": 2,
            "profile": {
                "id": profile["id"],
                "fingerprint": profile["fingerprint"],
                "lock": profile["lock"],
            },
            "contracts": [
                runner._run_contract(contract) for contract in manifest["contracts"]
            ],
        }
        self.assertEqual(canonical_json(suite), ORACLE.read_bytes())


if __name__ == "__main__":
    unittest.main()
