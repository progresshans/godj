from __future__ import annotations

import ast
import inspect
import unittest
from unittest.mock import patch

import django
from django.core.management.commands import showmigrations

from conformance.runners.django import migration_status_scenarios as scenarios
from conformance.runners.django.normalizer import canonical_json


EXPECTED_SCENARIOS = (
    "django.migration.status.fresh_unapplied",
    "django.migration.status.applied_prefix",
    "django.migration.status.fully_applied_restart",
    "django.migration.status.cross_app_branch_order",
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


class MigrationStatusScenarioTests(unittest.TestCase):
    def test_reference_runtime_is_exact_pinned_django_6_1(self) -> None:
        self.assertEqual(django.get_version(), "6.1")

    def test_registry_is_exact_and_all_observations_are_evaluation(self) -> None:
        self.assertEqual(tuple(scenarios.SCENARIOS), EXPECTED_SCENARIOS)
        for number, scenario in zip(range(112, 116), scenarios.SCENARIOS.values()):
            observation = scenario(f"MIG-{number:03d}")
            self.assertEqual(observation["id"], f"MIG-{number:03d}")
            self.assertEqual(observation["phase"], "evaluation")
            self.assertEqual(observation["status"], "observed")
            self.assertIsNone(observation["error"])
            self.assertIsNone(observation["db_state"])
            self.assertIsNone(observation["metrics"])

    def test_fresh_and_prefix_execute_real_show_list_markers(self) -> None:
        original = showmigrations.Command.show_list
        with patch.object(
            showmigrations.Command,
            "show_list",
            autospec=True,
            side_effect=original,
        ) as observed_show_list:
            fresh = scenarios.fresh_unapplied("MIG-112")
            prefix = scenarios.applied_prefix("MIG-113")
        self.assertEqual(observed_show_list.call_count, 2)

        fresh_result = denormalize(fresh["result"])
        self.assertEqual(
            fresh_result["stdout"],
            "authors\n"
            " [ ] 0001_author\n"
            "blog\n"
            " [ ] 0001_article\n"
            " [ ] 0002_publish\n",
        )
        self.assertEqual(
            [row["status"] for row in fresh_result["rows"]],
            ["unapplied", "unapplied", "unapplied"],
        )

        prefix_result = denormalize(prefix["result"])
        self.assertEqual(
            [row["status"] for row in prefix_result["rows"]],
            ["applied", "applied", "unapplied"],
        )
        self.assertEqual(
            [row["name"] for row in prefix_result["rows"]],
            ["0001_author", "0001_article", "0002_publish"],
        )

    def test_fully_applied_is_two_observations_not_fresh_process_proof(self) -> None:
        observation = scenarios.fully_applied_restart("MIG-114")
        result = denormalize(observation["result"])
        self.assertEqual(result["first_stdout"], result["second_stdout"])
        self.assertTrue(result["independent_observations_byte_identical"])
        self.assertEqual(
            [row["status"] for row in result["first_rows"]],
            ["applied", "applied", "applied"],
        )
        self.assertIsNone(observation["db_state"])
        self.assertIsNone(observation["metrics"])

    def test_cross_app_output_is_label_grouped_and_only_per_app_topological(self) -> None:
        observation = scenarios.cross_app_branch_order("MIG-115")
        result = denormalize(observation["result"])
        self.assertEqual(result["app_order"], ["alpha", "zeta"])
        self.assertEqual(
            [(row["app"], row["name"]) for row in result["rows"]],
            [
                ("alpha", "0099_parent"),
                ("alpha", "0001_child"),
                ("zeta", "0001_root"),
            ],
        )
        self.assertTrue(result["dependency_order_precedes_lexicographic_name"])
        self.assertTrue(result["per_app_dependency_valid"])
        self.assertTrue(result["label_grouping_can_precede_cross_app_dependency"])
        self.assertFalse(result["global_topological_order_claimed"])
        self.assertEqual(
            result["same_app_dependencies"],
            [{"child": "0001_child", "parent": "0099_parent"}],
        )

    def test_observations_are_byte_deterministic_and_contract_id_agnostic(self) -> None:
        for number, scenario in zip(range(112, 116), scenarios.SCENARIOS.values()):
            with self.subTest(scenario=scenario.__name__):
                expected = scenario(f"MIG-{number:03d}")
                repeated = scenario(f"MIG-{number:03d}")
                arbitrary = scenario("untrusted-contract")
                self.assertEqual(canonical_json(expected), canonical_json(repeated))
                self.assertEqual(arbitrary["id"], "untrusted-contract")
                self.assertEqual(
                    {key: value for key, value in expected.items() if key != "id"},
                    {key: value for key, value in arbitrary.items() if key != "id"},
                )

    def test_source_is_artifact_blind_and_has_no_io_or_process_probe(self) -> None:
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
        for forbidden in (
            "conformance/contracts",
            "conformance/oracles",
            "conformance/fixtures",
            "not_implemented",
            "not-implemented",
            "sqlite3",
        ):
            self.assertNotIn(forbidden, source)
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


if __name__ == "__main__":
    unittest.main()
