from __future__ import annotations

import os
import subprocess
import sys
import textwrap
import unittest
from pathlib import Path
from unittest.mock import patch

from django.db import connection
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.loader import MigrationLoader
from django.db.migrations.recorder import MigrationRecorder

from conformance.runners.django import migration_planning_scenarios as scenarios
from conformance.runners.django.normalizer import canonical_json


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


def plan_tuples(observation) -> list[list[tuple[str, str, str]]]:
    result = denormalize(observation["result"])
    return [
        [
            (item["app"], item["name"], item["direction"])
            for item in case["plan"]
        ]
        for case in result["cases"]
    ]


class MigrationPlanningScenarioTests(unittest.TestCase):
    def assert_clean_database(self) -> None:
        self.assertEqual(connection.introspection.table_names(), [])
        self.assertFalse(MigrationRecorder(connection).has_table())

    def assert_zero_mutation(self, observation) -> None:
        metrics = denormalize(observation["metrics"])
        cases = metrics.get("cases", [metrics])
        for metric in cases:
            self.assertEqual(metric["ddl_statement_count"], 0)
            self.assertEqual(metric["non_select_statement_count"], 0)
            self.assertEqual(metric["write_statement_count"], 0)
            self.assertTrue(metric["state_unchanged"])

        database_state = denormalize(observation["db_state"])
        states = database_state.get("cases", [database_state])
        for state in states:
            self.assertEqual(state["before"], state["after"])
            self.assertTrue(state["before"]["recorder_present"])
            self.assertEqual(state["before"]["managed_schema_inventory"], [])

    def test_success_plans_preserve_dependency_and_direction_order(self) -> None:
        expected = (
            (
                scenarios.linear_forward_plan,
                "MIG-005",
                [[
                    ("alpha", "0001_initial", "forward"),
                    ("alpha", "0002_second", "forward"),
                    ("alpha", "0003_third", "forward"),
                ]],
            ),
            (
                scenarios.applied_pruning_plans,
                "MIG-006",
                [
                    [
                        ("alpha", "0002_second", "forward"),
                        ("alpha", "0003_third", "forward"),
                    ],
                    [],
                ],
            ),
            (
                scenarios.prior_target_rollback,
                "MIG-008",
                [[
                    ("alpha", "0003_third", "backward"),
                    ("alpha", "0002_second", "backward"),
                ]],
            ),
            (
                scenarios.zero_with_dependents,
                "MIG-009",
                [[
                    ("beta", "0002_second", "backward"),
                    ("beta", "0001_initial", "backward"),
                    ("alpha", "0002_second", "backward"),
                    ("alpha", "0001_initial", "backward"),
                ]],
            ),
            (
                scenarios.cross_app_forward,
                "MIG-010",
                [[
                    ("alpha", "0001_initial", "forward"),
                    ("alpha", "0002_second", "forward"),
                    ("beta", "0001_initial", "forward"),
                    ("beta", "0002_second", "forward"),
                ]],
            ),
            (
                scenarios.cross_app_backward,
                "MIG-011",
                [[
                    ("beta", "0002_second", "backward"),
                    ("beta", "0001_initial", "backward"),
                    ("alpha", "0002_second", "backward"),
                ]],
            ),
            (
                scenarios.ordered_targets_shared_dependency,
                "MIG-012",
                [[
                    ("shared", "0001_initial", "forward"),
                    ("alpha", "0001_initial", "forward"),
                    ("beta", "0001_initial", "forward"),
                ]],
            ),
            (
                scenarios.retained_other_branches,
                "MIG-013",
                [[
                    ("alpha", "0003_third", "backward"),
                    ("alpha", "0002_second", "backward"),
                ]],
            ),
        )
        for scenario, contract_id, wanted in expected:
            with self.subTest(contract_id=contract_id):
                observation = scenario(contract_id)
                self.assertEqual(plan_tuples(observation), wanted)
                self.assert_zero_mutation(observation)
                self.assert_clean_database()

    def test_pruning_keeps_partial_and_fully_applied_cases_ordered(self) -> None:
        observation = scenarios.applied_pruning_plans("MIG-006")
        cases = denormalize(observation["result"])["cases"]

        self.assertEqual(
            [case["name"] for case in cases],
            ["partially_applied_prefix", "fully_applied_target"],
        )
        self.assertEqual(
            cases[0]["applied"],
            [{"app": "alpha", "name": "0001_initial"}],
        )
        self.assertEqual(
            cases[1]["applied"],
            [
                {"app": "alpha", "name": "0001_initial"},
                {"app": "alpha", "name": "0002_second"},
                {"app": "alpha", "name": "0003_third"},
            ],
        )
        self.assertEqual(cases[1]["plan"], [])

    def test_ordered_target_list_is_an_explicit_plan_input(self) -> None:
        nodes = (scenarios._S1, scenarios._A1, scenarios._B1)
        dependencies = (
            (scenarios._A1, scenarios._S1),
            (scenarios._B1, scenarios._S1),
        )
        normal = scenarios._plan_case(
            "target_order",
            nodes,
            dependencies,
            [scenarios._A1, scenarios._B1],
            [],
        )[0]
        reversed_targets = scenarios._plan_case(
            "target_order",
            nodes,
            dependencies,
            [scenarios._B1, scenarios._A1],
            [],
        )[0]

        self.assertEqual(
            [item["app"] for item in normal["plan"]],
            ["shared", "alpha", "beta"],
        )
        self.assertEqual(
            [item["app"] for item in reversed_targets["plan"]],
            ["shared", "beta", "alpha"],
        )
        self.assertNotEqual(normal["targets"], reversed_targets["targets"])
        self.assert_clean_database()

    def test_error_taxonomy_is_bound_to_stable_request_and_graph_facts(self) -> None:
        expected = (
            (
                scenarios.missing_target,
                "MIG-007",
                "evaluation",
                "migration_plan_error",
                "target_not_found",
            ),
            (
                scenarios.inconsistent_history,
                "MIG-014",
                "evaluation",
                "migration_history_error",
                "inconsistent_applied_history",
            ),
            (
                scenarios.missing_dependency,
                "MIG-015",
                "construction",
                "migration_graph_error",
                "dependency_not_found",
            ),
            (
                scenarios.dependency_cycle,
                "MIG-016",
                "construction",
                "migration_graph_error",
                "dependency_cycle",
            ),
        )
        for scenario, contract_id, phase, category, code in expected:
            with self.subTest(contract_id=contract_id):
                observation = scenario(contract_id)
                self.assertEqual(observation["phase"], phase)
                self.assertIsNone(observation["result"])
                self.assertEqual(observation["error"]["category"], category)
                self.assertEqual(observation["error"]["code"], code)
                self.assertFalse(observation["error"]["message_is_contract"])
                self.assertNotIn("message", observation["error"])
                metrics = denormalize(observation["metrics"])
                self.assertIn("graph", metrics)
                self.assertIn("request", metrics)
                self.assert_zero_mutation(observation)
                self.assert_clean_database()

    def test_cycle_exposes_sorted_graph_facts_not_raw_traversal_message(self) -> None:
        observation = scenarios.dependency_cycle("MIG-016")
        metrics = denormalize(observation["metrics"])

        self.assertNotIn("message", observation["error"])
        self.assertEqual(
            metrics["graph"]["nodes"],
            [
                {"app": "alpha", "name": "0001_initial"},
                {"app": "beta", "name": "0001_initial"},
            ],
        )
        self.assertEqual(len(metrics["graph"]["dependencies"]), 2)

    def test_dependency_and_ordered_targets_ignore_graph_insertion_order(self) -> None:
        variants = [
            scenarios._plan_case(
                "insertion_order",
                scenarios._CROSS_NODES,
                scenarios._CROSS_DEPENDENCIES,
                [scenarios._A1],
                [scenarios._A1, scenarios._A2, scenarios._B1, scenarios._B2],
                insertion_variant=variant,
            )
            for variant in ("normal", "reverse", "rotate")
        ]
        self.assertEqual(canonical_json(variants[0]), canonical_json(variants[1]))
        self.assertEqual(canonical_json(variants[0]), canonical_json(variants[2]))

        reversed_records = scenarios._plan_case(
            "insertion_order",
            scenarios._CROSS_NODES,
            scenarios._CROSS_DEPENDENCIES,
            [scenarios._A1],
            [scenarios._B2, scenarios._B1, scenarios._A2, scenarios._A1],
        )
        self.assertEqual(
            variants[0][0]["plan"],
            reversed_records[0]["plan"],
        )
        self.assertEqual(
            variants[0][1]["before"],
            reversed_records[1]["before"],
        )

        shared_nodes = (scenarios._S1, scenarios._A1, scenarios._B1)
        shared_dependencies = (
            (scenarios._A1, scenarios._S1),
            (scenarios._B1, scenarios._S1),
        )
        ordered_target_variants = [
            scenarios._plan_case(
                "ordered_targets",
                shared_nodes,
                shared_dependencies,
                [scenarios._A1, scenarios._B1],
                [],
                insertion_variant=variant,
            )
            for variant in ("normal", "reverse", "rotate")
        ]
        self.assertEqual(
            canonical_json(ordered_target_variants[0]),
            canonical_json(ordered_target_variants[1]),
        )
        self.assertEqual(
            canonical_json(ordered_target_variants[0]),
            canonical_json(ordered_target_variants[2]),
        )
        self.assert_clean_database()

    def test_hash_seed_does_not_change_canonical_scenario_bytes(self) -> None:
        script = textwrap.dedent(
            """
            import sys
            from conformance.runners.django.migration_planning_scenarios import SCENARIOS
            from conformance.runners.django.normalizer import canonical_json

            observations = [
                scenario(f"MIG-{index:03d}")
                for index, scenario in enumerate(SCENARIOS.values(), start=5)
            ]
            sys.stdout.buffer.write(canonical_json(observations))
            """
        )
        root = Path(__file__).resolve().parents[4]
        outputs = []
        for seed in ("1", "2", "99991"):
            environment = os.environ.copy()
            environment.update({"LC_ALL": "C", "PYTHONHASHSEED": seed, "TZ": "UTC"})
            completed = subprocess.run(
                [sys.executable, "-c", script],
                cwd=root,
                env=environment,
                check=False,
                capture_output=True,
                timeout=15,
            )
            self.assertEqual(completed.returncode, 0, completed.stderr.decode())
            outputs.append(completed.stdout)
        self.assertEqual(outputs[0], outputs[1])
        self.assertEqual(outputs[0], outputs[2])

    def test_scenarios_are_live_and_not_hardcoded_plan_payloads(self) -> None:
        baseline = canonical_json(scenarios.linear_forward_plan("MIG-005"))
        with patch.object(MigrationExecutor, "migration_plan", return_value=[]):
            changed = scenarios.linear_forward_plan("MIG-005")

        self.assertEqual(plan_tuples(changed), [[]])
        self.assertNotEqual(baseline, canonical_json(changed))
        self.assert_clean_database()

    def test_unexpected_database_write_fails_closed_and_still_cleans_up(self) -> None:
        original = MigrationExecutor.migration_plan

        def mutating_plan(executor, targets, clean_start=False):
            MigrationRecorder(connection).record_applied("intruder", "0001")
            return original(executor, targets, clean_start=clean_start)

        with patch.object(MigrationExecutor, "migration_plan", mutating_plan):
            with self.assertRaisesRegex(
                AssertionError,
                "changed recorder or schema state|wrote to the database",
            ):
                scenarios.linear_forward_plan("MIG-005")
        self.assert_clean_database()

    def test_unexpected_non_select_statement_fails_closed(self) -> None:
        original = MigrationExecutor.migration_plan

        def pragma_plan(executor, targets, clean_start=False):
            with connection.cursor() as cursor:
                cursor.execute("PRAGMA user_version")
            return original(executor, targets, clean_start=clean_start)

        with patch.object(MigrationExecutor, "migration_plan", pragma_plan):
            with self.assertRaisesRegex(AssertionError, "non-SELECT"):
                scenarios.linear_forward_plan("MIG-005")
        self.assert_clean_database()

    def test_inconsistent_history_uses_public_loader_preflight(self) -> None:
        original = MigrationLoader.check_consistent_history
        calls = []

        def tracked(loader, database_connection):
            calls.append(database_connection.alias)
            return original(loader, database_connection)

        with patch.object(MigrationLoader, "check_consistent_history", tracked):
            observation = scenarios.inconsistent_history("MIG-014")

        self.assertEqual(calls, [connection.alias])
        self.assertEqual(
            observation["error"]["python_type"],
            "django.db.migrations.exceptions.InconsistentMigrationHistory",
        )
        self.assert_clean_database()

    def test_every_scenario_repeats_byte_identically_and_cleans_global_state(self) -> None:
        for index, (name, scenario) in enumerate(scenarios.SCENARIOS.items(), start=5):
            with self.subTest(name=name):
                contract_id = f"MIG-{index:03d}"
                first = canonical_json(scenario(contract_id))
                self.assert_clean_database()
                second = canonical_json(scenario(contract_id))
                self.assert_clean_database()
                self.assertEqual(first, second)


if __name__ == "__main__":
    unittest.main()
