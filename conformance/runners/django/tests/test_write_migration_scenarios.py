from __future__ import annotations

import unittest
from unittest.mock import patch

from django.db import connection

from conformance.runners.django import write_migration_scenarios as scenarios


class WriteMigrationScenarioTests(unittest.TestCase):
    def normalized_field(self, value, name: str):
        self.assertEqual(value["type"], "object")
        for field in value["fields"]:
            if field["name"] == name:
                return field["value"]
        self.fail(f"normalized field {name!r} is missing")

    def test_mod_007_preserves_a_preexisting_sentinel_row(self) -> None:
        observation = scenarios.model_atomic_rollback("MOD-007")
        articles = self.normalized_field(observation["db_state"], "articles")

        self.assertEqual(len(articles["items"]), 1)
        title = self.normalized_field(articles["items"][0], "title")
        self.assertEqual(
            title,
            {"type": "string", "value": scenarios.ROLLBACK_SENTINEL_TITLE},
        )
        summary = self.normalized_field(articles["items"][0], "summary")
        self.assertEqual(
            summary,
            {"type": "string", "value": scenarios.ROLLBACK_SENTINEL_SUMMARY},
        )

    def test_mig_003_successful_reverse_is_a_committed_observation(self) -> None:
        observation = scenarios.migration_reverse_nullable_field("MIG-003")

        self.assertEqual(observation["phase"], "commit")

    def test_mig_004_rejects_a_generic_preflight_runtime_error(self) -> None:
        with (
            patch.object(
                scenarios, "_migrate", side_effect=RuntimeError("generic preflight")
            ),
            patch.object(scenarios, "_cleanup_migrations"),
        ):
            with self.assertRaisesRegex(RuntimeError, "generic preflight"):
                scenarios.migration_atomic_failure("MIG-004")

    def test_mig_004_accepts_only_the_unique_failure_operation(self) -> None:
        observation = scenarios.migration_atomic_failure("MIG-004")
        managed_tables = self.normalized_field(
            observation["db_state"], "managed_tables"
        )

        self.assertEqual(observation["error"]["code"], "operation_failed")
        self.assertIn(
            "ConformanceMigrationOperationFailure",
            observation["error"]["python_type"],
        )
        self.assertEqual(managed_tables, {"type": "list", "items": []})

    def test_migration_state_exposes_managed_leftover_tables(self) -> None:
        leftover = f"{scenarios.FAILURE_TABLE}__leftover"
        quoted = connection.ops.quote_name(leftover)
        with connection.cursor() as cursor:
            cursor.execute(f"CREATE TABLE {quoted} (id INTEGER PRIMARY KEY)")
        try:
            state = scenarios._migration_state(table=scenarios.FAILURE_TABLE)
            self.assertIn(leftover, state["managed_tables"])
        finally:
            with connection.cursor() as cursor:
                cursor.execute(f"DROP TABLE IF EXISTS {quoted}")


if __name__ == "__main__":
    unittest.main()
