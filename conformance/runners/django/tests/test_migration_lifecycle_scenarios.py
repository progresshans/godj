from __future__ import annotations

import ast
import inspect
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from django.db import connection, connections
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.recorder import MigrationRecorder

from conformance.runners.django import migration_lifecycle_scenarios as scenarios


ROOT = Path(__file__).resolve().parents[4]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = ROOT / "conformance/contracts/migration-lifecycle-manifest.json"
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64"
    / "migration-lifecycle-oracle.json"
)
STATIC = (
    ROOT
    / "conformance/fixtures"
    / "godj-migration-lifecycle-not-implemented.json"
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
        "raw": observation,
        "result": (
            denormalize(observation["result"])
            if observation["result"] is not None
            else None
        ),
        "db": denormalize(observation["db_state"]),
        "metrics": denormalize(observation["metrics"]),
    }


def keys(values):
    return [(item["app"], item["name"]) for item in values]


def records(snapshot):
    return keys(snapshot["migration_records"])


def plan(result):
    return [
        (item["app"], item["name"], item["direction"])
        for item in result["plan"]
    ]


def steps(metrics):
    return [
        (item["app"], item["name"], item["direction"], item["outcome"])
        for item in metrics["steps"]
    ]


def table(snapshot, name):
    return next(
        item for item in snapshot["managed_schema"] if item["name"] == name
    )


def columns(snapshot, name):
    return [item["name"] for item in table(snapshot, name)["columns"]]


def state_app(state, label):
    return next(item for item in state["apps"] if item["label"] == label)


def state_model(state, app_label, model_name):
    return next(
        item
        for item in state_app(state, app_label)["models"]
        if item["name"] == model_name
    )


def state_fields(state, app_label, model_name):
    return [
        item["name"]
        for item in state_model(state, app_label, model_name)["fields"]
    ]


class MigrationLifecycleScenarioTests(unittest.TestCase):
    def assert_environment_clean(self) -> None:
        self.assertNotIn(scenarios._DATABASE_ALIAS, connections.databases)
        self.assertEqual(connection.introspection.table_names(), [])
        self.assertFalse(MigrationRecorder(connection).has_table())

    def assert_latest_state(self, state) -> None:
        self.assertEqual(
            [item["label"] for item in state["apps"]],
            ["alpha", "beta"],
        )
        self.assertEqual(
            state_fields(state, "alpha", "entry"),
            ["id", "a1_marker", "a2_marker", "a3_marker"],
        )
        self.assertEqual(
            state_fields(state, "beta", "branch"),
            ["id", "b1_marker"],
        )

    def test_registry_order_matches_mig_047_through_056(self) -> None:
        self.assertEqual(
            list(scenarios.SCENARIOS),
            [
                "django.migration.lifecycle.fresh_latest",
                "django.migration.lifecycle.applied_prefix_latest",
                "django.migration.lifecycle.fully_applied_latest_noop",
                "django.migration.lifecycle.named_forward_target",
                "django.migration.lifecycle.named_reverse_target",
                "django.migration.lifecycle.zero_target",
                "django.migration.lifecycle.unknown_legacy_tail",
                "django.migration.lifecycle.inconsistent_history_preflight",
                "django.migration.lifecycle.middle_forward_failure",
                "django.migration.lifecycle.restart_after_failure",
            ],
        )

    def test_manifest_locks_mapping_phases_and_pinned_provenance(self) -> None:
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        self.assertEqual(manifest["format_version"], 2)
        self.assertEqual(
            manifest["profile_id"],
            "django-6.1-sqlite-darwin-arm64",
        )
        self.assertEqual(
            [contract["id"] for contract in manifest["contracts"]],
            [f"MIG-{number:03d}" for number in range(47, 57)],
        )
        self.assertEqual(
            [contract["scenario"] for contract in manifest["contracts"]],
            list(scenarios.SCENARIOS),
        )
        self.assertEqual(
            [contract["phase"] for contract in manifest["contracts"]],
            [
                "commit",
                "commit",
                "commit",
                "commit",
                "commit",
                "commit",
                "commit",
                "evaluation",
                "rollback",
                "commit",
            ],
        )
        for contract in manifest["contracts"]:
            with self.subTest(contract=contract["id"]):
                self.assertEqual(
                    contract["status"],
                    "deviation" if contract["id"] == "MIG-052" else "passing",
                )
                self.assertEqual(
                    contract["comparison"],
                    (
                        ["error", "db_state", "metrics"]
                        if contract["id"] in {"MIG-054", "MIG-055"}
                        else ["result", "db_state", "metrics"]
                    ),
                )
                self.assertTrue(contract["provenance"])
                decision_provenance = [
                    provenance
                    for provenance in contract["provenance"]
                    if provenance["kind"] == "decision"
                ]
                self.assertEqual(
                    decision_provenance,
                    (
                        [
                            {
                                "kind": "decision",
                                "reference": "DEV-0002",
                                "derived": False,
                            }
                        ]
                        if contract["id"] == "MIG-052"
                        else []
                    ),
                )
                for provenance in contract["provenance"]:
                    if provenance["kind"] == "decision":
                        continue
                    self.assertIn(
                        "django@fe0a859f537d4238cf49fca39073513206f83122:",
                        provenance["reference"],
                    )
                    self.assertIs(provenance["derived"], False)
                    self.assertEqual(provenance["license"], "BSD-3-Clause")

    def test_static_fixture_is_explicitly_not_implemented(self) -> None:
        fixture = json.loads(STATIC.read_text(encoding="utf-8"))
        self.assertEqual(fixture["format_version"], 2)
        self.assertEqual(
            [item["id"] for item in fixture["contracts"]],
            [f"MIG-{number:03d}" for number in range(47, 57)],
        )
        self.assertEqual(
            {item["status"] for item in fixture["contracts"]},
            {"not_implemented"},
        )
        self.assertEqual(
            [item["phase"] for item in fixture["contracts"]],
            [
                "commit",
                "commit",
                "commit",
                "commit",
                "commit",
                "commit",
                "commit",
                "evaluation",
                "rollback",
                "commit",
            ],
        )

    def test_scenario_source_uses_only_public_executor_orchestration(self) -> None:
        source = inspect.getsource(scenarios)
        syntax = ast.parse(source)
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
        self.assertTrue(
            {"check_consistent_history", "migration_plan", "migrate"}
            <= called_attributes
        )
        self.assertTrue(
            {
                "_create_project_state",
                "_migrate_all_forwards",
                "_migrate_all_backwards",
                "apply_migration",
                "unapply_migration",
            }.isdisjoint(called_attributes)
        )
        self.assertTrue(
            {"read_text", "read_bytes", "write_text", "write_bytes"}.isdisjoint(
                called_attributes
            )
        )
        self.assertNotIn("open", called_names)
        for forbidden_literal in {
            "MIG-",
            "migration-lifecycle-oracle",
            "godj-migration-lifecycle-not-implemented",
        }:
            self.assertNotIn(forbidden_literal, source)

    def test_every_scenario_is_independent_of_contract_id(self) -> None:
        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 47):
            with self.subTest(scenario=scenario.__name__):
                expected = scenario(f"MIG-{number:03d}")
                arbitrary = scenario("MIG-untrusted")
                self.assertEqual(expected["id"], f"MIG-{number:03d}")
                self.assertEqual(arbitrary["id"], "MIG-untrusted")
                self.assertEqual(
                    {key: value for key, value in expected.items() if key != "id"},
                    {
                        key: value
                        for key, value in arbitrary.items()
                        if key != "id"
                    },
                )
        self.assert_environment_clean()

    def test_fresh_prefix_and_fully_applied_latest_lifecycles(self) -> None:
        fresh = observed(scenarios.fresh_latest, "MIG-047")
        self.assertEqual(
            plan(fresh["result"]),
            [
                ("alpha", "0001_initial", "forward"),
                ("alpha", "0002_second", "forward"),
                ("alpha", "0003_third", "forward"),
                ("beta", "0001_initial", "forward"),
            ],
        )
        self.assertEqual(records(fresh["db"]["before"]), [])
        self.assertEqual(
            records(fresh["db"]["after"]),
            [scenarios._A1, scenarios._A2, scenarios._A3, scenarios._B1],
        )
        self.assertEqual(fresh["metrics"]["recorder_bootstrap"], "created")
        self.assert_latest_state(fresh["result"]["returned_state"])
        self.assert_environment_clean()

        prefix = observed(scenarios.applied_prefix_latest, "MIG-048")
        self.assertEqual(records(prefix["db"]["before"]), [scenarios._A1])
        self.assertEqual(
            plan(prefix["result"]),
            [
                ("alpha", "0002_second", "forward"),
                ("alpha", "0003_third", "forward"),
                ("beta", "0001_initial", "forward"),
            ],
        )
        self.assertNotIn(
            scenarios._A1,
            [(item[0], item[1]) for item in steps(prefix["metrics"])],
        )
        self.assertEqual(prefix["metrics"]["recorder_bootstrap"], "existing")
        self.assert_latest_state(prefix["result"]["returned_state"])
        self.assert_environment_clean()

        complete = observed(scenarios.fully_applied_latest_noop, "MIG-049")
        self.assertEqual(complete["result"]["plan"], [])
        self.assertEqual(complete["metrics"]["steps"], [])
        self.assertEqual(complete["db"]["before"], complete["db"]["after"])
        self.assertEqual(
            complete["metrics"]["effects"],
            {
                "database_state_changed": False,
                "ddl_observed": False,
                "transaction_observed": False,
                "write_observed": False,
            },
        )
        self.assert_latest_state(complete["result"]["returned_state"])
        self.assert_environment_clean()

    def test_named_forward_reverse_and_zero_target_meaning(self) -> None:
        forward = observed(scenarios.named_forward_target, "MIG-050")
        self.assertEqual(
            plan(forward["result"]),
            [
                ("alpha", "0001_initial", "forward"),
                ("alpha", "0002_second", "forward"),
            ],
        )
        self.assertEqual(
            columns(forward["db"]["after"], "godj_lifecycle_alpha"),
            ["a1_marker", "a2_marker", "id"],
        )
        self.assertEqual(
            state_fields(
                forward["result"]["returned_state"], "alpha", "entry"
            ),
            ["id", "a1_marker", "a2_marker"],
        )
        self.assertEqual(
            [item["label"] for item in forward["result"]["returned_state"]["apps"]],
            ["alpha"],
        )
        self.assert_environment_clean()

        reverse = observed(scenarios.named_reverse_target, "MIG-051")
        self.assertEqual(
            plan(reverse["result"]),
            [
                ("alpha", "0003_third", "backward"),
                ("alpha", "0002_second", "backward"),
            ],
        )
        self.assertEqual(
            records(reverse["db"]["after"]),
            [scenarios._A1, scenarios._B1],
        )
        self.assertEqual(
            columns(reverse["db"]["after"], "godj_lifecycle_alpha"),
            ["a1_marker", "id"],
        )
        self.assertEqual(
            columns(reverse["db"]["after"], "godj_lifecycle_beta"),
            ["b1_marker", "id"],
        )
        self.assertNotIn("transaction_model", reverse["metrics"])
        self.assert_environment_clean()

        zero = observed(scenarios.zero_target, "MIG-052")
        self.assertEqual(
            plan(zero["result"]),
            [
                ("beta", "0001_initial", "backward"),
                ("alpha", "0003_third", "backward"),
                ("alpha", "0002_second", "backward"),
                ("alpha", "0001_initial", "backward"),
            ],
        )
        self.assertEqual(records(zero["db"]["after"]), [])
        self.assertEqual(zero["db"]["after"]["managed_schema"], [])
        self.assertTrue(zero["db"]["after"]["recorder_present"])
        self.assertEqual(
            zero["result"]["returned_state"],
            {"apps": [], "format_version": 1},
        )
        self.assert_environment_clean()

    def test_unknown_legacy_is_preserved_and_inconsistent_history_stops_early(
        self,
    ) -> None:
        legacy = observed(scenarios.unknown_legacy_tail, "MIG-053")
        self.assertEqual(
            records(legacy["db"]["before"]),
            [scenarios._A1, scenarios._LEGACY],
        )
        self.assertEqual(
            plan(legacy["result"]),
            [
                ("alpha", "0002_second", "forward"),
                ("alpha", "0003_third", "forward"),
                ("beta", "0001_initial", "forward"),
            ],
        )
        self.assertEqual(
            records(legacy["db"]["after"]),
            [
                scenarios._A1,
                scenarios._A2,
                scenarios._A3,
                scenarios._B1,
                scenarios._LEGACY,
            ],
        )
        self.assertNotIn(
            "legacy",
            [
                item["label"]
                for item in legacy["result"]["returned_state"]["apps"]
            ],
        )
        self.assert_environment_clean()

        with patch.object(
            scenarios,
            "_latest_targets",
            side_effect=AssertionError("target selection must not run"),
        ), patch.object(
            MigrationExecutor,
            "migration_plan",
            side_effect=AssertionError("planning must not run"),
        ), patch.object(
            MigrationExecutor,
            "migrate",
            side_effect=AssertionError("execution must not run"),
        ):
            invalid = observed(
                scenarios.inconsistent_history_preflight,
                "MIG-054",
            )
        self.assertIsNone(invalid["result"])
        self.assertEqual(
            invalid["raw"]["error"],
            {
                "category": "migration_history_error",
                "code": "inconsistent_applied_history",
                "message_is_contract": False,
                "python_type": (
                    "django.db.migrations.exceptions.InconsistentMigrationHistory"
                ),
            },
        )
        self.assertEqual(
            invalid["metrics"]["history_preflight"],
            {
                "history_check_invoked": True,
                "history_valid": False,
                "migrate_invoked": False,
                "plan_invoked": False,
            },
        )
        self.assertEqual(
            invalid["metrics"]["effects"],
            {
                "database_state_changed": False,
                "ddl_observed": False,
                "transaction_observed": False,
                "write_observed": False,
            },
        )
        self.assertEqual(invalid["db"]["before"], invalid["db"]["after"])
        self.assertEqual(records(invalid["db"]["after"]), [scenarios._A2])
        self.assert_environment_clean()

    def test_middle_failure_and_fresh_connection_restart(self) -> None:
        failed = observed(scenarios.middle_forward_failure, "MIG-055")
        self.assertIsNone(failed["result"])
        self.assertEqual(
            failed["raw"]["error"]["code"],
            "operation_failed",
        )
        self.assertEqual(
            steps(failed["metrics"]),
            [
                ("alpha", "0001_initial", "forward", "committed"),
                ("alpha", "0002_second", "forward", "rolled_back"),
                ("alpha", "0003_third", "forward", "not_started"),
                ("beta", "0001_initial", "forward", "not_started"),
            ],
        )
        self.assertEqual(failed["metrics"]["unstarted_tail_count"], 2)
        self.assertEqual(records(failed["db"]["after"]), [scenarios._A1])
        self.assertEqual(
            columns(failed["db"]["after"], "godj_lifecycle_alpha"),
            ["a1_marker", "id"],
        )
        self.assertEqual(failed["metrics"]["recorder_bootstrap"], "created")
        self.assert_environment_clean()

        restarted = observed(scenarios.restart_after_failure, "MIG-056")
        self.assertEqual(records(restarted["db"]["before"]), [scenarios._A1])
        self.assertEqual(
            plan(restarted["result"]),
            [
                ("alpha", "0002_second", "forward"),
                ("alpha", "0003_third", "forward"),
                ("beta", "0001_initial", "forward"),
            ],
        )
        self.assertEqual(
            records(restarted["db"]["after"]),
            [scenarios._A1, scenarios._A2, scenarios._A3, scenarios._B1],
        )
        restart = restarted["metrics"]["restart"]
        self.assertTrue(restart["connection_reopened"])
        self.assertEqual(restart["database_kind"], "temporary_file")
        self.assertEqual(
            [item["outcome"] for item in restart["setup"]["steps"]],
            ["committed", "rolled_back", "not_started", "not_started"],
        )
        self.assertEqual(
            keys(restart["setup"]["durable_prefix"]),
            [scenarios._A1],
        )
        self.assert_latest_state(restarted["result"]["returned_state"])
        self.assert_environment_clean()

    def test_definition_target_and_fault_mutations_cannot_false_green(self) -> None:
        baseline = observed(scenarios.fresh_latest, "MIG-047")
        with patch.object(scenarios, "_A3_FIELD_NAME", "changed_marker"):
            changed = observed(scenarios.fresh_latest, "MIG-047")
        self.assertNotEqual(baseline["raw"], changed["raw"])
        self.assertIn(
            "changed_marker",
            state_fields(changed["result"]["returned_state"], "alpha", "entry"),
        )

        with patch.object(
            scenarios,
            "_latest_targets",
            return_value=(scenarios._A2,),
        ):
            retargeted = observed(scenarios.fresh_latest, "MIG-047")
        self.assertEqual(
            plan(retargeted["result"]),
            [
                ("alpha", "0001_initial", "forward"),
                ("alpha", "0002_second", "forward"),
            ],
        )
        self.assertNotEqual(baseline["raw"], retargeted["raw"])

        with patch.object(
            scenarios._FailureOperation,
            "database_forwards",
            return_value=None,
        ):
            with self.assertRaisesRegex(
                AssertionError,
                "expected ConformanceLifecycleOperationFailure",
            ):
                scenarios.middle_forward_failure("MIG-055")
        self.assert_environment_clean()

    def test_recorder_request_and_seed_mutations_propagate_live(self) -> None:
        prefix = observed(scenarios.applied_prefix_latest, "MIG-048")
        with patch.object(
            scenarios,
            "_PREFIX_SEED_TARGETS",
            (scenarios._A2,),
        ):
            longer_prefix = observed(
                scenarios.applied_prefix_latest,
                "MIG-048",
            )
        self.assertEqual(
            records(longer_prefix["db"]["before"]),
            [scenarios._A1, scenarios._A2],
        )
        self.assertEqual(
            plan(longer_prefix["result"]),
            [
                ("alpha", "0003_third", "forward"),
                ("beta", "0001_initial", "forward"),
            ],
        )
        self.assertNotEqual(prefix["raw"], longer_prefix["raw"])

        legacy = observed(scenarios.unknown_legacy_tail, "MIG-053")
        changed_legacy_key = ("retired", "0042_old")
        with patch.object(scenarios, "_LEGACY", changed_legacy_key):
            changed_legacy = observed(
                scenarios.unknown_legacy_tail,
                "MIG-053",
            )
        self.assertIn(
            changed_legacy_key,
            records(changed_legacy["db"]["before"]),
        )
        self.assertIn(
            changed_legacy_key,
            records(changed_legacy["db"]["after"]),
        )
        self.assertNotEqual(legacy["raw"], changed_legacy["raw"])

        named = observed(scenarios.named_forward_target, "MIG-050")
        with patch.object(
            scenarios,
            "_NAMED_FORWARD_TARGETS",
            (scenarios._A3,),
        ):
            later_named = observed(
                scenarios.named_forward_target,
                "MIG-050",
            )
        self.assertEqual(
            plan(later_named["result"]),
            [
                ("alpha", "0001_initial", "forward"),
                ("alpha", "0002_second", "forward"),
                ("alpha", "0003_third", "forward"),
            ],
        )
        self.assertNotEqual(named["raw"], later_named["raw"])

        zero = observed(scenarios.zero_target, "MIG-052")
        with patch.object(
            scenarios,
            "_ZERO_TARGETS",
            (("beta", None),),
        ):
            beta_zero = observed(scenarios.zero_target, "MIG-052")
        self.assertEqual(
            plan(beta_zero["result"]),
            [("beta", "0001_initial", "backward")],
        )
        self.assertEqual(
            records(beta_zero["db"]["after"]),
            [scenarios._A1, scenarios._A2, scenarios._A3],
        )
        self.assertNotEqual(zero["raw"], beta_zero["raw"])
        self.assert_environment_clean()

    def test_payload_excludes_sql_counts_timestamps_paths_and_reverse_topology(
        self,
    ) -> None:
        keys_seen: set[str] = set()

        def collect(value) -> None:
            if isinstance(value, dict):
                keys_seen.update(value)
                for item in value.values():
                    collect(item)
            elif isinstance(value, list):
                for item in value:
                    collect(item)

        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 47):
            collect(scenario(f"MIG-{number:03d}"))

        for forbidden in {
            "sql",
            "select_statement_count",
            "ddl_statement_count",
            "write_statement_count",
            "applied_timestamp",
            "database_path",
            "object_identity",
            "transaction_model",
        }:
            self.assertNotIn(forbidden, keys_seen)
        self.assert_environment_clean()

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_two_hashseed_processes_match_checked_in_oracle(self) -> None:
        outputs: list[bytes] = []
        with tempfile.TemporaryDirectory() as temporary_directory:
            for index, hash_seed in enumerate(("17", "982451653"), 1):
                output = Path(temporary_directory) / f"lifecycle-{index}.json"
                environment = os.environ.copy()
                environment.update(
                    {
                        "LC_ALL": "C",
                        "PYTHONHASHSEED": hash_seed,
                        "TZ": "UTC",
                    }
                )
                subprocess.run(
                    [
                        sys.executable,
                        "-m",
                        "conformance.runners.django",
                        "--profile",
                        str(PROFILE),
                        "--manifest",
                        str(MANIFEST),
                        "--output",
                        str(output),
                    ],
                    cwd=ROOT,
                    env=environment,
                    check=True,
                    capture_output=True,
                    text=True,
                )
                outputs.append(output.read_bytes())
        self.assertEqual(outputs[0], outputs[1])
        self.assertEqual(outputs[0], ORACLE.read_bytes())


if __name__ == "__main__":
    unittest.main()
