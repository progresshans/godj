from __future__ import annotations

import json
import os
import subprocess
import sys
import textwrap
import unittest
from pathlib import Path
from unittest.mock import patch

from django.db import connection
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.operations.fields import AddField
from django.db.migrations.recorder import MigrationRecorder

from conformance.runners.django import migration_execution_scenarios as scenarios
from conformance.runners.django.normalizer import canonical_json


ROOT = Path(__file__).resolve().parents[4]
MANIFEST = ROOT / "conformance/contracts/migration-execution-manifest.json"


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


def records(snapshot):
    return [
        (item["app"], item["name"])
        for item in snapshot["migration_records"]
    ]


def columns(snapshot, table="godj_exec_alpha"):
    for schema in snapshot["managed_schema"]:
        if schema["name"] == table:
            return [column["name"] for column in schema["columns"]]
    return []


def compact_steps(metrics):
    return [
        (
            step["name"],
            step["direction"],
            step["status"],
            step["transaction_model"],
            step["schema_outcome"],
            step["recorder_outcome"],
        )
        for step in metrics["steps"]
    ]


class MigrationExecutionScenarioTests(unittest.TestCase):
    def assert_clean_database(self) -> None:
        self.assertEqual(connection.introspection.table_names(), [])
        self.assertFalse(MigrationRecorder(connection).has_table())

    def assert_recovered(self, metrics) -> None:
        self.assertEqual(
            metrics["connection"],
            {
                "autocommit_restored": True,
                "outside_atomic_block": True,
                "select_usable": True,
            },
        )

    def test_registry_order_matches_mig_017_through_026(self) -> None:
        self.assertEqual(
            list(scenarios.SCENARIOS),
            [
                "django.migration.execute.linear_forward",
                "django.migration.execute.linear_backward",
                "django.migration.execute.applied_prefix_tail",
                "django.migration.execute.rollback_branch_preserves_unrelated",
                "django.migration.execute.forward_operation_failure",
                "django.migration.execute.backward_operation_failure",
                "django.migration.execute.forward_recorder_failure",
                "django.migration.execute.backward_recorder_failure",
                "django.migration.execute.mixed_direction_rejected",
                "django.migration.execute.empty_plan",
            ],
        )

    def test_linear_forward_commits_each_step_and_returns_final_state(self) -> None:
        value = observed(scenarios.linear_forward_execution, "MIG-017")

        self.assertEqual(
            [item["name"] for item in value["result"]["plan"]],
            ["0001_initial", "0002_second", "0003_third"],
        )
        self.assertEqual(
            value["result"]["returned_state"]["models"][0]["fields"],
            ["a1_marker", "a2_marker", "a3_marker", "id"],
        )
        self.assertEqual(value["db"]["before"]["managed_schema"], [])
        self.assertEqual(
            columns(value["db"]["after"]),
            ["a1_marker", "a2_marker", "a3_marker", "id"],
        )
        self.assertEqual(
            records(value["db"]["after"]),
            [scenarios._A1, scenarios._A2, scenarios._A3],
        )
        self.assertEqual(
            compact_steps(value["metrics"]),
            [
                (
                    "0001_initial",
                    "forward",
                    "committed",
                    "schema_and_record",
                    "applied",
                    "applied",
                ),
                (
                    "0002_second",
                    "forward",
                    "committed",
                    "schema_and_record",
                    "applied",
                    "applied",
                ),
                (
                    "0003_third",
                    "forward",
                    "committed",
                    "schema_and_record",
                    "applied",
                    "applied",
                ),
            ],
        )
        self.assert_recovered(value["metrics"])
        self.assert_clean_database()

    def test_linear_backward_commits_every_unapply_and_reaches_empty_state(self) -> None:
        value = observed(scenarios.linear_backward_execution, "MIG-018")

        self.assertEqual(
            [item["name"] for item in value["result"]["plan"]],
            ["0003_third", "0002_second", "0001_initial"],
        )
        self.assertTrue(
            all(item["direction"] == "backward" for item in value["result"]["plan"])
        )
        self.assertEqual(value["result"]["returned_state"], {"models": []})
        self.assertEqual(value["db"]["after"]["managed_schema"], [])
        self.assertEqual(value["db"]["after"]["migration_records"], [])
        self.assertTrue(
            all(
                step[2:]
                == ("committed", "schema_then_record", "reversed", "unapplied")
                for step in compact_steps(value["metrics"])
            )
        )
        self.assert_recovered(value["metrics"])
        self.assert_clean_database()

    def test_applied_prefix_historical_state_advances_for_tail_only(self) -> None:
        value = observed(scenarios.applied_prefix_tail_execution, "MIG-019")
        steps = value["metrics"]["steps"]

        self.assertEqual(records(value["db"]["before"]), [scenarios._A1])
        self.assertEqual(
            [item["name"] for item in steps],
            ["0002_second", "0003_third"],
        )
        self.assertEqual(
            steps[0]["historical_state_before"]["models"][0]["fields"],
            ["a1_marker", "id"],
        )
        self.assertEqual(
            steps[0]["historical_state_after"]["models"][0]["fields"],
            ["a1_marker", "a2_marker", "id"],
        )
        self.assertEqual(
            steps[1]["historical_state_before"]["models"][0]["fields"],
            ["a1_marker", "a2_marker", "id"],
        )
        self.assertEqual(
            steps[1]["historical_state_after"]["models"][0]["fields"],
            ["a1_marker", "a2_marker", "a3_marker", "id"],
        )
        self.assert_clean_database()

    def test_backward_branch_preserves_unrelated_schema_record_and_state(self) -> None:
        value = observed(
            scenarios.rollback_branch_preserves_unrelated,
            "MIG-020",
        )

        self.assertEqual(
            records(value["db"]["after"]),
            [scenarios._A1, scenarios._B1],
        )
        self.assertEqual(
            [schema["name"] for schema in value["db"]["after"]["managed_schema"]],
            ["godj_exec_alpha", "godj_exec_beta"],
        )
        self.assertEqual(
            value["result"]["returned_state"]["models"],
            [
                {
                    "app": "alpha",
                    "fields": ["a1_marker", "id"],
                    "name": "entry",
                },
                {
                    "app": "beta",
                    "fields": ["b1_marker", "id"],
                    "name": "branch",
                },
            ],
        )
        self.assert_clean_database()

    def test_operation_failures_preserve_prior_step_and_stop_the_tail(self) -> None:
        cases = (
            (
                scenarios.forward_operation_failure,
                "MIG-021",
                [scenarios._A1],
                ["a1_marker", "id"],
                [
                    ("0001_initial", "committed", "applied", "applied"),
                    (
                        "0002_second",
                        "rolled_back",
                        "rolled_back",
                        "not_started",
                    ),
                    (
                        "0003_third",
                        "not_started",
                        "not_started",
                        "not_started",
                    ),
                ],
            ),
            (
                scenarios.backward_operation_failure,
                "MIG-022",
                [scenarios._A1, scenarios._A2],
                ["a1_marker", "a2_marker", "id"],
                [
                    ("0003_third", "committed", "reversed", "unapplied"),
                    (
                        "0002_second",
                        "rolled_back",
                        "rolled_back",
                        "not_started",
                    ),
                    (
                        "0001_initial",
                        "not_started",
                        "not_started",
                        "not_started",
                    ),
                ],
            ),
        )
        for (
            scenario,
            contract_id,
            wanted_records,
            wanted_columns,
            wanted_steps,
        ) in cases:
            with self.subTest(contract_id=contract_id):
                value = observed(scenario, contract_id)
                raw = value["raw"]
                self.assertEqual(raw["phase"], "rollback")
                self.assertEqual(raw["error"]["category"], "migration_execution_error")
                self.assertEqual(raw["error"]["code"], "operation_failed")
                self.assertEqual(records(value["db"]["after"]), wanted_records)
                self.assertEqual(columns(value["db"]["after"]), wanted_columns)
                self.assertEqual(
                    [
                        (
                            step["name"],
                            step["status"],
                            step["schema_outcome"],
                            step["recorder_outcome"],
                        )
                        for step in value["metrics"]["steps"]
                    ],
                    wanted_steps,
                )
                failed_step = value["metrics"]["steps"][1]
                self.assertNotIn("historical_state_before", failed_step)
                self.assertNotIn("historical_state_after", failed_step)
                self.assertNotIn("historical_state_before", value["metrics"]["steps"][2])
                self.assert_recovered(value["metrics"])
                self.assert_clean_database()

    def test_forward_recorder_failure_rolls_back_schema_and_record_together(self) -> None:
        value = observed(scenarios.forward_recorder_failure, "MIG-023")

        self.assertEqual(value["raw"]["phase"], "rollback")
        self.assertEqual(value["raw"]["error"]["category"], "migration_recorder_error")
        self.assertEqual(value["raw"]["error"]["code"], "record_failed")
        self.assertEqual(records(value["db"]["after"]), [scenarios._A1])
        self.assertEqual(columns(value["db"]["after"]), ["a1_marker", "id"])
        self.assertEqual(
            [
                (
                    step["name"],
                    step["status"],
                    step["schema_outcome"],
                    step["recorder_outcome"],
                )
                for step in value["metrics"]["steps"]
            ],
            [
                ("0001_initial", "committed", "applied", "applied"),
                ("0002_second", "rolled_back", "rolled_back", "failed"),
                (
                    "0003_third",
                    "not_started",
                    "not_started",
                    "not_started",
                ),
            ],
        )
        self.assertEqual(
            value["metrics"]["steps"][1]["fault_point"],
            "before_record_write",
        )
        self.assert_recovered(value["metrics"])
        self.assert_clean_database()

    def test_backward_recorder_failure_commits_schema_but_keeps_record(self) -> None:
        value = observed(scenarios.backward_recorder_failure, "MIG-024")

        self.assertEqual(value["raw"]["phase"], "commit")
        self.assertEqual(value["raw"]["error"]["category"], "migration_recorder_error")
        self.assertEqual(records(value["db"]["after"]), [scenarios._A1, scenarios._A2])
        self.assertEqual(columns(value["db"]["after"]), ["a1_marker", "id"])
        self.assertEqual(
            [
                (
                    step["name"],
                    step["status"],
                    step["transaction_model"],
                    step["schema_outcome"],
                    step["recorder_outcome"],
                )
                for step in value["metrics"]["steps"]
            ],
            [
                (
                    "0003_third",
                    "committed",
                    "schema_then_record",
                    "reversed",
                    "unapplied",
                ),
                (
                    "0002_second",
                    "schema_committed_record_failed",
                    "schema_then_record",
                    "reversed",
                    "retained",
                ),
                (
                    "0001_initial",
                    "not_started",
                    "none",
                    "not_started",
                    "not_started",
                ),
            ],
        )
        self.assertEqual(
            value["metrics"]["steps"][1]["fault_point"],
            "before_record_write",
        )
        self.assert_recovered(value["metrics"])
        self.assert_clean_database()

    def test_mixed_plan_fails_before_domain_events_and_preserves_precreated_recorder(self) -> None:
        value = observed(scenarios.mixed_direction_rejected, "MIG-025")

        self.assertEqual(value["raw"]["phase"], "evaluation")
        self.assertEqual(value["raw"]["error"]["category"], "migration_execution_error")
        self.assertEqual(value["raw"]["error"]["code"], "mixed_directions")
        self.assertEqual(value["db"]["before"], value["db"]["after"])
        self.assertTrue(value["db"]["after"]["recorder_present"])
        self.assertEqual(
            compact_steps(value["metrics"]),
            [
                (
                    "0001_initial",
                    "forward",
                    "not_started",
                    "none",
                    "not_started",
                    "not_started",
                ),
                (
                    "0002_second",
                    "backward",
                    "not_started",
                    "none",
                    "not_started",
                    "not_started",
                ),
            ],
        )
        self.assertTrue(
            all(
                "historical_state_before" not in step
                for step in value["metrics"]["steps"]
            )
        )
        self.assert_recovered(value["metrics"])
        self.assert_clean_database()

    def test_empty_plan_is_noop_without_creating_recorder(self) -> None:
        value = observed(scenarios.empty_plan_noop, "MIG-026")

        self.assertEqual(value["result"], {"plan": [], "returned_state": {"models": []}})
        self.assertEqual(value["db"]["before"], value["db"]["after"])
        self.assertFalse(value["db"]["after"]["recorder_present"])
        self.assertEqual(value["metrics"]["steps"], [])
        self.assert_clean_database()

    def test_every_execution_uses_an_explicit_plan_argument(self) -> None:
        original = MigrationExecutor.migrate
        seen_plans = []

        def tracked(executor, targets, plan=None, state=None, fake=False, fake_initial=False):
            seen_plans.append(plan)
            return original(
                executor,
                targets,
                plan=plan,
                state=state,
                fake=fake,
                fake_initial=fake_initial,
            )

        with patch.object(MigrationExecutor, "migrate", tracked):
            scenarios.applied_prefix_tail_execution("MIG-019")

        self.assertEqual([len(plan) for plan in seen_plans], [1, 2])
        self.assertTrue(all(plan is not None for plan in seen_plans))
        self.assert_clean_database()

    def test_schema_and_recorder_payloads_are_live_not_plan_echoes(self) -> None:
        baseline = canonical_json(scenarios.linear_forward_execution("MIG-017"))
        original = AddField.database_forwards

        def skip_a2(operation, app_label, schema_editor, from_state, to_state):
            if operation.name == "a2_marker":
                return None
            return original(operation, app_label, schema_editor, from_state, to_state)

        with patch.object(AddField, "database_forwards", skip_a2):
            changed = scenarios.linear_forward_execution("MIG-017")

        self.assertNotEqual(baseline, canonical_json(changed))
        changed_db = denormalize(changed["db_state"])
        self.assertNotIn("a2_marker", columns(changed_db["after"]))
        self.assert_clean_database()

        original_record = MigrationRecorder.record_applied

        def skip_a2_record(recorder, app, name):
            if (app, name) == scenarios._A2:
                return None
            return original_record(recorder, app, name)

        with patch.object(MigrationRecorder, "record_applied", skip_a2_record):
            with self.assertRaisesRegex(
                AssertionError,
                "forward record was not applied",
            ):
                scenarios.linear_forward_execution("MIG-017")
        self.assert_clean_database()

    def test_compact_metrics_hide_raw_choreography_and_require_live_commit_trace(self) -> None:
        value = observed(scenarios.linear_forward_execution, "MIG-017")

        self.assertEqual(set(value["metrics"]), {"connection", "steps"})
        for step in value["metrics"]["steps"]:
            self.assertTrue(
                {
                    "sequence",
                    "kind",
                    "action",
                    "operation",
                    "historical_state_before",
                    "historical_state_after",
                }.isdisjoint(step)
            )

        original = scenarios._ExecutionTrace.transaction_sql

        def omit_commits(trace, statement):
            if statement.lstrip().upper().startswith("COMMIT"):
                return None
            return original(trace, statement)

        with patch.object(scenarios._ExecutionTrace, "transaction_sql", omit_commits):
            with self.assertRaisesRegex(
                AssertionError,
                "completed without a schema commit",
            ):
                scenarios.linear_forward_execution("MIG-017")
        self.assert_clean_database()

    def test_fault_sentinels_are_executed_instead_of_synthesized(self) -> None:
        with patch.object(scenarios._FaultOperation, "_run", return_value=None):
            with self.assertRaisesRegex(
                AssertionError,
                "expected ConformanceMigrationOperationFailure",
            ):
                scenarios.forward_operation_failure("MIG-021")
        self.assert_clean_database()

        original = scenarios._TracingExecutor.record_migration

        def ignore_fault(executor, app_label, name, forward=True):
            saved = executor._recorder_fault
            executor._recorder_fault = None
            try:
                return original(executor, app_label, name, forward=forward)
            finally:
                executor._recorder_fault = saved

        with patch.object(scenarios._TracingExecutor, "record_migration", ignore_fault):
            with self.assertRaisesRegex(
                AssertionError,
                "expected ConformanceMigrationRecorderFailure",
            ):
                scenarios.forward_recorder_failure("MIG-023")
        self.assert_clean_database()

    def test_manifest_is_pinned_and_one_to_one_with_registry(self) -> None:
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))

        self.assertEqual(
            [contract["id"] for contract in manifest["contracts"]],
            [f"MIG-{index:03d}" for index in range(17, 27)],
        )
        self.assertEqual(
            [contract["scenario"] for contract in manifest["contracts"]],
            list(scenarios.SCENARIOS),
        )
        for contract in manifest["contracts"]:
            self.assertEqual(contract["status"], "oracle_locked")
            self.assertGreaterEqual(len(contract["provenance"]), 2)
            for provenance in contract["provenance"]:
                self.assertIn(
                    "django@fe0a859f537d4238cf49fca39073513206f83122:",
                    provenance["reference"],
                )
                self.assertFalse(provenance["derived"])
                self.assertEqual(provenance["license"], "BSD-3-Clause")

    def test_hash_seed_does_not_change_canonical_scenario_bytes(self) -> None:
        script = textwrap.dedent(
            """
            import sys
            from conformance.runners.django.migration_execution_scenarios import SCENARIOS
            from conformance.runners.django.normalizer import canonical_json

            observations = [
                scenario(f"MIG-{index:03d}")
                for index, scenario in enumerate(SCENARIOS.values(), start=17)
            ]
            sys.stdout.buffer.write(canonical_json(observations))
            """
        )
        outputs = []
        for seed in ("1", "2", "99991"):
            environment = os.environ.copy()
            environment.update({"LC_ALL": "C", "PYTHONHASHSEED": seed, "TZ": "UTC"})
            completed = subprocess.run(
                [sys.executable, "-c", script],
                cwd=ROOT,
                env=environment,
                check=False,
                capture_output=True,
                timeout=30,
            )
            self.assertEqual(completed.returncode, 0, completed.stderr.decode())
            outputs.append(completed.stdout)
        self.assertEqual(outputs[0], outputs[1])
        self.assertEqual(outputs[0], outputs[2])

    def test_every_scenario_repeats_byte_identically_and_cleans_state(self) -> None:
        for index, (name, scenario) in enumerate(scenarios.SCENARIOS.items(), start=17):
            with self.subTest(name=name):
                contract_id = f"MIG-{index:03d}"
                first = canonical_json(scenario(contract_id))
                self.assert_clean_database()
                second = canonical_json(scenario(contract_id))
                self.assert_clean_database()
                self.assertEqual(first, second)


if __name__ == "__main__":
    unittest.main()
