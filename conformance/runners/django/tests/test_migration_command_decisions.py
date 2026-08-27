from __future__ import annotations

import ast
import json
import unittest
from pathlib import Path
from typing import Any

from conformance.runners.django import migration_command_decisions as decisions
from conformance.runners.django.normalizer import canonical_json


ROOT = Path(__file__).resolve().parents[4]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64"
    / "migration-command-oracle.json"
)

EXPECTED_SCENARIOS = (
    "godj.migration.command.fresh_latest",
    "godj.migration.command.applied_prefix_tail",
    "godj.migration.command.fully_applied_fresh_noop",
    "godj.migration.command.definition_preflight_before_backend",
    "godj.migration.command.inconsistent_history_preflight",
    "godj.migration.command.capability_preflight_before_begin",
    "godj.migration.command.middle_failure_durable_prefix",
    "godj.migration.command.fresh_resume_after_failure",
    "godj.migration.command.commit_outcome_unknown",
    "godj.migration.command.concurrent_latest_fenced",
    "godj.migration.command.backend_configuration_secret_boundary",
    "godj.migration.command.interrupt_rollback_cleanup",
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


class MigrationCommandDecisionTests(unittest.TestCase):
    def test_registry_is_exact_ordered_mig_087_through_098(self) -> None:
        self.assertEqual(tuple(decisions.SCENARIOS), EXPECTED_SCENARIOS)
        profile = json.loads(PROFILE.read_text(encoding="utf-8"))
        profile.pop("format_version")
        suite = decisions.generate_suite(profile)
        self.assertEqual(suite["format_version"], 2)
        self.assertEqual(suite["profile"], profile)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [f"MIG-{number:03d}" for number in range(87, 99)],
        )
        self.assertEqual(
            [contract["phase"] for contract in suite["contracts"]],
            [
                "commit",
                "commit",
                "commit",
                "evaluation",
                "evaluation",
                "evaluation",
                "rollback",
                "commit",
                "commit",
                "commit",
                "environment",
                "rollback",
            ],
        )
        self.assertEqual(
            {contract["status"] for contract in suite["contracts"]},
            {"observed"},
        )

    def test_decisions_are_deterministic_fresh_and_bounded(self) -> None:
        profile = json.loads(PROFILE.read_text(encoding="utf-8"))
        profile.pop("format_version")
        first = decisions.generate_suite(profile)
        first["profile"]["id"] = "caller-mutated"
        second = decisions.generate_suite(profile)
        third = decisions.generate_suite(profile)
        self.assertEqual(second["profile"]["id"], profile["id"])
        encoded = canonical_json(second)
        self.assertEqual(encoded, canonical_json(third))
        self.assertLess(len(encoded), 32 * 1024)
        self.assertNotIn(b"postgres://", encoded)
        self.assertNotIn(b"raw cause", encoded)

    def test_checked_in_oracle_is_exact_decision_output(self) -> None:
        profile = json.loads(PROFILE.read_text(encoding="utf-8"))
        profile.pop("format_version")
        expected = canonical_json(decisions.generate_suite(profile))
        self.assertEqual(ORACLE.read_bytes(), expected)

    def test_central_runner_registers_exact_scenarios(self) -> None:
        from conformance.runners.django import runner

        self.assertEqual(
            runner.DEFAULT_MIGRATION_COMMAND_MANIFEST,
            ROOT / "conformance/contracts/migration-command-manifest.json",
        )
        self.assertEqual(runner.DEFAULT_MIGRATION_COMMAND_ORACLE, ORACLE)
        self.assertIs(
            runner.KNOWN_MANIFEST_ORACLES[
                runner.DEFAULT_MIGRATION_COMMAND_MANIFEST.resolve()
            ],
            runner.DEFAULT_MIGRATION_COMMAND_ORACLE,
        )
        for name, scenario in decisions.SCENARIOS.items():
            self.assertIs(runner.SCENARIOS[name], scenario)

    def test_preflight_unknown_and_cleanup_boundaries_are_explicit(self) -> None:
        preflight = decisions.definition_preflight_before_backend("MIG-090")
        cases = _semantic(preflight["result"])["cases"]
        self.assertEqual(
            [case["code"] for case in cases],
            ["invalid_definition_document", "definition_format_incompatible"],
        )
        self.assertEqual({case["backend_opens"] for case in cases}, {0})

        unknown = decisions.commit_outcome_unknown("MIG-095")
        self.assertEqual(unknown["error"]["code"], "commit_outcome_unknown")
        self.assertIsNone(unknown["result"])
        unknown_state = _semantic(unknown["db_state"])
        unknown_metrics = _semantic(unknown["metrics"])
        self.assertFalse(unknown_state["reported_success"])
        self.assertEqual(unknown_metrics["automatic_retries"], 0)
        self.assertEqual(unknown_metrics["rollback_after_unknown"], 0)

        interrupted = decisions.interrupt_rollback_cleanup("MIG-098")
        self.assertIsNone(interrupted["result"])
        interrupted_state = _semantic(interrupted["db_state"])
        interrupted_metrics = _semantic(interrupted["metrics"])
        self.assertEqual(interrupted_state["rollback"], "completed")
        self.assertEqual(interrupted_state["session_close"], "completed")
        self.assertEqual(interrupted_state["backend_close"], "completed")
        self.assertEqual(interrupted_state["child_reap"], "direct")
        self.assertEqual(interrupted_metrics["force_kills"], 0)

    def test_secret_and_concurrency_decisions_are_fail_closed(self) -> None:
        secret = decisions.backend_configuration_secret_boundary("MIG-097")
        result = _semantic(secret["result"])
        metrics = _semantic(secret["metrics"])
        self.assertFalse(result["global_cli_parses_secret"])
        self.assertFalse(result["raw_causes_published"])
        for channel in ("artifact", "protocol", "stderr", "stdout"):
            self.assertEqual(metrics[f"{channel}_secret_occurrences"], 0)

        concurrent = decisions.concurrent_latest_fenced("MIG-096")
        result = _semantic(concurrent["result"])
        metrics = _semantic(concurrent["metrics"])
        self.assertFalse(result["duplicate_history"])
        self.assertFalse(result["corrupt_history"])
        self.assertEqual(metrics["child_processes"], 2)
        self.assertEqual(metrics["automatic_retries"], 0)

    def test_source_has_no_django_or_artifact_dependency(self) -> None:
        source_path = Path(decisions.__file__)
        source = source_path.read_text(encoding="utf-8")
        tree = ast.parse(source)
        imports: set[str] = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imports.update(alias.name for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module is not None:
                imports.add(node.module)
        self.assertFalse(
            any(name == "django" or name.startswith("django.") for name in imports)
        )
        for forbidden in (
            "conformance/contracts",
            "conformance/oracles",
            "conformance/fixtures",
            "not_implemented",
            "not-implemented",
            "sqlite3",
        ):
            self.assertNotIn(forbidden, source)


if __name__ == "__main__":
    unittest.main()
