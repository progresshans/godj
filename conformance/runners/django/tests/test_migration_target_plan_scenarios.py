from __future__ import annotations

import unittest
from typing import Any
from unittest.mock import patch

from django.db import connection
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.recorder import MigrationRecorder

from conformance.runners.django import migration_planning_scenarios as planning
from conformance.runners.django import migration_target_plan_scenarios as scenarios
from conformance.runners.django.normalizer import canonical_json


EXPECTED_SCENARIOS = (
    "django.migration.target_plan.named_forward_closure",
    "django.migration.target_plan.named_reverse_descendants",
    "django.migration.target_plan.app_zero_cross_app_dependents",
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


def _plan(observation: dict[str, Any]) -> list[tuple[str, str, str]]:
    result = _semantic(observation["result"])
    return [
        (row["app"], row["name"], row["direction"])
        for row in result["plan"]
    ]


class MigrationTargetPlanScenarioTests(unittest.TestCase):
    def assert_clean_database(self) -> None:
        self.assertEqual(connection.introspection.table_names(), [])
        self.assertFalse(MigrationRecorder(connection).has_table())

    def test_registry_and_set_slug_are_exact(self) -> None:
        self.assertEqual(scenarios.SET_SLUG, "migration-target-plan")
        self.assertEqual(tuple(scenarios.SCENARIOS), EXPECTED_SCENARIOS)

    def test_real_migration_executor_owns_all_three_ordered_plans(self) -> None:
        original = MigrationExecutor.migration_plan
        calls: list[list[tuple[str, str | None]]] = []

        def observed(
            executor: MigrationExecutor,
            targets: list[tuple[str, str | None]],
            *args: Any,
            **kwargs: Any,
        ):
            calls.append(list(targets))
            return original(executor, targets, *args, **kwargs)

        with patch.object(MigrationExecutor, "migration_plan", observed):
            observations = [
                scenarios.named_forward_closure("MIG-120"),
                scenarios.named_reverse_descendants("MIG-121"),
                scenarios.app_zero_cross_app_dependents("MIG-122"),
            ]

        self.assertEqual(len(calls), 3)
        self.assertEqual(calls[0], [("alpha", "0003_third")])
        self.assertEqual(calls[1], [("alpha", "0001_initial")])
        self.assertEqual(calls[2], [("alpha", None)])
        for observation in observations:
            self.assertEqual(observation["phase"], "evaluation")
            self.assertEqual(observation["status"], "observed")
            self.assertIsNone(observation["error"])
            self.assertIsNone(observation["db_state"])
            self.assertIsNone(observation["metrics"])
        self.assert_clean_database()

    def test_named_forward_and_reverse_results_are_exact(self) -> None:
        self.assertEqual(
            _plan(scenarios.named_forward_closure("MIG-120")),
            [
                ("alpha", "0001_initial", "forward"),
                ("alpha", "0002_second", "forward"),
                ("alpha", "0003_third", "forward"),
            ],
        )
        self.assertEqual(
            _plan(scenarios.named_reverse_descendants("MIG-121")),
            [
                ("charlie", "0001_descendant_dependent", "backward"),
                ("alpha", "0003_third", "backward"),
                ("alpha", "0002_second", "backward"),
            ],
        )
        self.assert_clean_database()

    def test_named_reverse_removes_descendant_dependent_but_retains_direct_child(self) -> None:
        observation = scenarios.named_reverse_descendants("MIG-121")
        result = _semantic(observation["result"])
        plan = [
            (row["app"], row["name"], row["direction"])
            for row in result["plan"]
        ]
        self.assertIn(
            ("charlie", "0001_descendant_dependent", "backward"),
            plan,
        )
        self.assertNotIn(
            ("beta", "0001_direct_dependent", "backward"),
            plan,
        )
        self.assertNotIn(
            ("gamma", "0001_unrelated", "backward"),
            plan,
        )
        self.assertIn(
            {"app": "beta", "name": "0001_direct_dependent"},
            result["applied"],
        )
        self.assertIn(
            {"app": "gamma", "name": "0001_unrelated"},
            result["applied"],
        )
        self.assert_clean_database()

    def test_app_zero_preserves_django_incomparable_sibling_order(self) -> None:
        observation = scenarios.app_zero_cross_app_dependents("MIG-122")
        self.assertEqual(
            _plan(observation),
            [
                ("beta", "0001_direct_dependent", "backward"),
                ("alpha", "0003_third", "backward"),
                ("alpha", "0002_second", "backward"),
                ("alpha", "0001_initial", "backward"),
            ],
        )
        result = _semantic(observation["result"])
        self.assertIn(
            {"app": "gamma", "name": "0001_unrelated"},
            result["applied"],
        )
        self.assertNotIn("gamma", [row["app"] for row in result["plan"]])
        self.assert_clean_database()

    def test_observations_are_fresh_deterministic_and_helper_backed(self) -> None:
        for contract_id, scenario in zip(
            ("MIG-120", "MIG-121", "MIG-122"),
            scenarios.SCENARIOS.values(),
            strict=True,
        ):
            with self.subTest(contract=contract_id):
                with patch.object(
                    planning,
                    "_plan_case",
                    wraps=planning._plan_case,
                ) as plan_case:
                    first = scenario(contract_id)
                self.assertEqual(plan_case.call_count, 1)
                second = scenario(contract_id)
                self.assertEqual(canonical_json(first), canonical_json(second))
                self.assertLess(len(canonical_json(first)), 8 * 1024)
                self.assert_clean_database()


if __name__ == "__main__":
    unittest.main()
