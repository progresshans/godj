from __future__ import annotations

import unittest

from conformance.runners.django import save_lifecycle_scenarios as scenarios


class SaveLifecycleScenarioTests(unittest.TestCase):
    def normalized_field(self, value, name: str):
        self.assertEqual(value["type"], "object")
        for field in value["fields"]:
            if field["name"] == name:
                return field["value"]
        self.fail(f"normalized field {name!r} is missing")

    def string_value(self, value) -> str:
        self.assertEqual(value["type"], "string")
        return value["value"]

    def metric_statement_kinds(self, observation) -> list[str]:
        kinds = self.normalized_field(observation["metrics"], "statement_kinds")
        self.assertEqual(kinds["type"], "list")
        return [self.string_value(item) for item in kinds["items"]]

    def database_articles(self, observation):
        articles = self.normalized_field(observation["db_state"], "articles")
        self.assertEqual(articles["type"], "list")
        return articles["items"]

    def test_instance_payload_uses_public_fields_not_private_state(self) -> None:
        observation = scenarios.model_save_new_auto_pk("MOD-008")
        before = self.normalized_field(observation["result"], "before")
        after = self.normalized_field(observation["result"], "after")

        for state in (before, after):
            names = {field["name"] for field in state["fields"]}
            self.assertEqual(names, {"pk", "published", "summary", "title"})
            self.assertNotIn("adding", names)
        self.assertEqual(self.normalized_field(before, "pk"), {"type": "null"})
        self.assertEqual(
            self.normalized_field(after, "pk"),
            {"type": "pk", "value": {"type": "int", "value": "1"}},
        )
        self.assertEqual(self.metric_statement_kinds(observation), ["INSERT"])

    def test_default_save_overwrites_concurrent_values_from_loaded_instance(self) -> None:
        observation = scenarios.model_save_loaded_all_fields("MOD-009")
        articles = self.database_articles(observation)

        self.assertEqual(len(articles), 1)
        self.assertEqual(
            self.string_value(self.normalized_field(articles[0], "title")),
            "After default save",
        )
        self.assertFalse(self.normalized_field(articles[0], "published")["value"])
        self.assertEqual(
            self.string_value(self.normalized_field(articles[0], "summary")),
            "Loaded summary",
        )
        self.assertEqual(self.metric_statement_kinds(observation), ["UPDATE"])

    def test_named_update_fields_keeps_omitted_changes_only_in_memory(self) -> None:
        observation = scenarios.model_save_update_fields_named("MOD-010")
        instance = self.normalized_field(observation["result"], "instance_after")
        persisted = self.database_articles(observation)[0]

        self.assertTrue(self.normalized_field(instance, "published")["value"])
        self.assertEqual(
            self.string_value(self.normalized_field(instance, "summary")),
            "Memory only",
        )
        self.assertFalse(self.normalized_field(persisted, "published")["value"])
        self.assertEqual(
            self.string_value(self.normalized_field(persisted, "summary")),
            "Database preserved",
        )
        self.assertEqual(self.metric_statement_kinds(observation), ["UPDATE"])

    def test_empty_update_fields_is_a_zero_io_noop(self) -> None:
        observation = scenarios.model_save_update_fields_empty("MOD-011")
        instance = self.normalized_field(observation["result"], "instance_after")
        persisted = self.database_articles(observation)[0]

        self.assertEqual(self.metric_statement_kinds(observation), [])
        self.assertEqual(
            self.string_value(self.normalized_field(instance, "title")),
            "Memory only",
        )
        self.assertEqual(
            self.string_value(self.normalized_field(persisted, "title")),
            "Persisted",
        )

    def test_error_contracts_preserve_timing_and_statement_kind(self) -> None:
        tests = (
            (
                scenarios.model_save_update_fields_primary_key,
                "MOD-012",
                "field_error",
                "primary_key_update_field",
                [],
            ),
            (
                scenarios.model_save_force_insert_conflict,
                "MOD-013",
                "integrity_error",
                "unique_primary_key",
                ["INSERT"],
            ),
            (
                scenarios.model_save_force_update_without_pk,
                "MOD-014",
                "model_state_error",
                "force_update_without_primary_key",
                [],
            ),
            (
                scenarios.model_save_force_update_missing_row,
                "MOD-015",
                "not_updated",
                "force_update_missing_row",
                ["UPDATE"],
            ),
            (
                scenarios.model_save_mutually_exclusive_force_flags,
                "MOD-016",
                "argument_error",
                "mutually_exclusive_force_flags",
                [],
            ),
        )
        for scenario, contract_id, category, code, statement_kinds in tests:
            with self.subTest(contract_id=contract_id):
                observation = scenario(contract_id)
                self.assertIsNone(observation["result"])
                self.assertEqual(observation["error"]["category"], category)
                self.assertEqual(observation["error"]["code"], code)
                self.assertFalse(observation["error"]["message_is_contract"])
                self.assertEqual(
                    self.metric_statement_kinds(observation),
                    statement_kinds,
                )

    def test_explicit_primary_key_distinguishes_update_from_insert_fallback(self) -> None:
        existing = scenarios.model_save_explicit_pk_existing("MOD-017")
        missing = scenarios.model_save_explicit_pk_missing("MOD-018")

        self.assertEqual(self.metric_statement_kinds(existing), ["UPDATE"])
        self.assertEqual(
            self.metric_statement_kinds(missing),
            ["UPDATE", "INSERT"],
        )
        self.assertEqual(len(self.database_articles(existing)), 1)
        self.assertEqual(len(self.database_articles(missing)), 1)

    def test_atomic_rollback_keeps_mutated_objects_but_restores_database(self) -> None:
        observation = scenarios.model_save_atomic_rollback_instance_state("MOD-019")
        created = self.normalized_field(
            observation["result"], "created_instance_after"
        )
        sentinel = self.normalized_field(
            observation["result"], "sentinel_instance_after"
        )
        persisted = self.database_articles(observation)

        self.assertEqual(observation["phase"], "rollback")
        self.assertEqual(
            self.normalized_field(created, "pk"),
            {"type": "pk", "value": {"type": "int", "value": "2"}},
        )
        self.assertEqual(
            self.string_value(self.normalized_field(sentinel, "title")),
            "Memory after rollback",
        )
        self.assertEqual(len(persisted), 1)
        self.assertEqual(
            self.string_value(self.normalized_field(persisted[0], "title")),
            "Persisted before transaction",
        )
        self.assertEqual(
            self.metric_statement_kinds(observation),
            ["BEGIN", "UPDATE", "INSERT"],
        )


if __name__ == "__main__":
    unittest.main()
