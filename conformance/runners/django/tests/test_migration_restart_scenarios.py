from __future__ import annotations

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

from conformance.runners.django import migration_restart_scenarios as scenarios


ROOT = Path(__file__).resolve().parents[4]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = ROOT / "conformance/contracts/migration-restart-manifest.json"
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json"
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


def applied(snapshot):
    return [
        (item["app"], item["name"])
        for item in snapshot["applied_migrations"]
    ]


def plan(value):
    return [
        (item["app"], item["name"], item["direction"])
        for item in value["plan"]
    ]


def columns(snapshot):
    if not snapshot["managed_schema"]:
        return []
    return [
        column["name"]
        for column in snapshot["managed_schema"][0]["columns"]
    ]


class MigrationRestartScenarioTests(unittest.TestCase):
    def assert_clean_default_database(self) -> None:
        self.assertEqual(connection.introspection.table_names(), [])
        self.assertFalse(MigrationRecorder(connection).has_table())

    def assert_zero_mutation(self, value, boundary: str) -> None:
        self.assertEqual(value["db"]["before"], value["db"]["after"])
        self.assertEqual(value["metrics"]["ddl_statement_count"], 0)
        self.assertEqual(value["metrics"]["non_select_statement_count"], 0)
        self.assertEqual(value["metrics"]["write_statement_count"], 0)
        self.assertTrue(value["metrics"]["state_unchanged"])
        self.assertEqual(value["metrics"]["restart_boundary"], boundary)

    def test_registry_order_matches_mig_027_through_036(self) -> None:
        self.assertEqual(
            list(scenarios.SCENARIOS),
            [
                "django.migration.restart.absent_recorder_read",
                "django.migration.restart.empty_recorder_read",
                "django.migration.restart.record_visible_to_fresh_reader",
                "django.migration.restart.unrecord_hidden_from_fresh_reader",
                "django.migration.restart.database_alias_isolation",
                "django.migration.restart.applied_prefix_tail",
                "django.migration.restart.fully_applied_empty_plan",
                "django.migration.restart.unknown_legacy_record",
                "django.migration.restart.inconsistent_known_history",
                "django.migration.restart.failure_tail",
            ],
        )

    def test_manifest_locks_exact_mapping_and_pinned_provenance(self) -> None:
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        self.assertEqual(manifest["format_version"], 2)
        self.assertEqual(
            manifest["profile_id"],
            "django-6.1-sqlite-darwin-arm64",
        )
        self.assertEqual(
            [contract["id"] for contract in manifest["contracts"]],
            [f"MIG-{number:03d}" for number in range(27, 37)],
        )
        self.assertEqual(
            [contract["scenario"] for contract in manifest["contracts"]],
            list(scenarios.SCENARIOS),
        )
        for contract in manifest["contracts"]:
            with self.subTest(contract=contract["id"]):
                self.assertEqual(contract["phase"], "evaluation")
                self.assertEqual(contract["status"], "oracle_locked")
                self.assertEqual(
                    contract["comparison"],
                    (
                        ["error", "db_state", "metrics"]
                        if contract["id"] == "MIG-035"
                        else ["result", "db_state", "metrics"]
                    ),
                )
                self.assertTrue(contract["provenance"])
                for provenance in contract["provenance"]:
                    self.assertIn(
                        "django@fe0a859f537d4238cf49fca39073513206f83122:",
                        provenance["reference"],
                    )
                    self.assertIs(provenance["derived"], False)
                    self.assertEqual(provenance["license"], "BSD-3-Clause")

    def test_absent_recorder_read_returns_empty_without_creating_table(self) -> None:
        with patch.object(
            MigrationRecorder,
            "ensure_schema",
            side_effect=AssertionError("read path attempted schema creation"),
        ):
            value = observed(scenarios.absent_recorder_read, "MIG-027")

        self.assertEqual(value["result"], {"applied_migrations": []})
        self.assertFalse(value["db"]["before"]["recorder_present"])
        self.assertFalse(value["db"]["after"]["recorder_present"])
        self.assert_zero_mutation(value, "fresh_recorder")
        self.assert_clean_default_database()

    def test_empty_record_unrecord_and_fresh_reads_preserve_identity_meaning(self) -> None:
        cases = (
            (
                scenarios.empty_recorder_read,
                "MIG-028",
                [],
                True,
            ),
            (
                scenarios.record_visible_to_fresh_reader,
                "MIG-029",
                [scenarios._A1],
                True,
            ),
            (
                scenarios.unrecord_hidden_from_fresh_reader,
                "MIG-030",
                [],
                True,
            ),
        )
        for scenario, contract_id, wanted, recorder_present in cases:
            with self.subTest(contract=contract_id):
                value = observed(scenario, contract_id)
                self.assertEqual(
                    [
                        (item["app"], item["name"])
                        for item in value["result"]["applied_migrations"]
                    ],
                    wanted,
                )
                self.assertEqual(
                    value["db"]["after"]["recorder_present"],
                    recorder_present,
                )
                self.assertEqual(applied(value["db"]["after"]), wanted)
                self.assert_zero_mutation(value, "fresh_recorder")
                self.assert_clean_default_database()

        recorded = observed(
            scenarios.record_visible_to_fresh_reader,
            "MIG-029",
        )
        self.assertEqual(
            recorded["result"]["recorded_migration"],
            {"app": "alpha", "name": "0001_initial"},
        )
        self.assertEqual(
            recorded["metrics"]["setup"],
            {
                "migration": {"app": "alpha", "name": "0001_initial"},
                "transition": "recorded",
            },
        )
        self.assert_clean_default_database()

        events: list[tuple[str, str, str]] = []
        original_record_applied = MigrationRecorder.record_applied
        original_record_unapplied = MigrationRecorder.record_unapplied

        def spy_record_applied(recorder, app, name):
            events.append(("record", app, name))
            return original_record_applied(recorder, app, name)

        def spy_record_unapplied(recorder, app, name):
            events.append(("unrecord", app, name))
            return original_record_unapplied(recorder, app, name)

        with patch.object(
            MigrationRecorder,
            "record_applied",
            spy_record_applied,
        ), patch.object(
            MigrationRecorder,
            "record_unapplied",
            spy_record_unapplied,
        ):
            unrecorded = observed(
                scenarios.unrecord_hidden_from_fresh_reader,
                "MIG-030",
            )
        self.assertEqual(
            events,
            [
                ("record", "alpha", "0001_initial"),
                ("unrecord", "alpha", "0001_initial"),
            ],
        )
        self.assertEqual(
            unrecorded["result"]["unrecorded_migration"],
            {"app": "alpha", "name": "0001_initial"},
        )
        self.assertEqual(
            unrecorded["metrics"]["setup"],
            {
                "migration": {"app": "alpha", "name": "0001_initial"},
                "transition": "recorded_then_unrecorded",
            },
        )
        self.assert_clean_default_database()

    def test_database_aliases_read_only_their_own_durable_rows(self) -> None:
        value = observed(scenarios.database_alias_isolation, "MIG-031")

        self.assertEqual(
            value["result"]["databases"],
            [
                {
                    "alias": "default",
                    "applied_migrations": [
                        {"app": "alpha", "name": "0001_initial"}
                    ],
                },
                {
                    "alias": "other",
                    "applied_migrations": [
                        {"app": "beta", "name": "0001_initial"}
                    ],
                },
            ],
        )
        self.assert_zero_mutation(value, "fresh_recorder")
        self.assertNotIn("other", connections)
        self.assert_clean_default_database()

    def test_applied_prefix_and_full_target_use_fresh_executor_state(self) -> None:
        prefix = observed(scenarios.applied_prefix_tail, "MIG-032")
        self.assertEqual(applied(prefix["db"]["before"]), [scenarios._A1])
        self.assertEqual(
            plan(prefix["result"]),
            [
                ("alpha", "0002_second", "forward"),
                ("alpha", "0003_third", "forward"),
            ],
        )
        self.assertEqual(
            columns(prefix["db"]["before"]),
            ["a1_marker", "id"],
        )
        self.assert_zero_mutation(prefix, "fresh_executor")
        self.assert_clean_default_database()

        complete = observed(scenarios.fully_applied_empty_plan, "MIG-033")
        self.assertEqual(
            applied(complete["db"]["before"]),
            [scenarios._A1, scenarios._A2, scenarios._A3],
        )
        self.assertEqual(complete["result"]["plan"], [])
        self.assertEqual(
            columns(complete["db"]["before"]),
            ["a1_marker", "a2_marker", "a3_marker", "id"],
        )
        self.assert_zero_mutation(complete, "fresh_executor")
        self.assert_clean_default_database()

    def test_unknown_legacy_record_is_preserved_while_known_target_plans(self) -> None:
        value = observed(scenarios.unknown_legacy_record, "MIG-034")

        self.assertEqual(
            value["result"]["unknown_applied"],
            [{"app": "legacy", "name": "0099_retired"}],
        )
        self.assertEqual(value["result"]["known_applied"], [])
        self.assertEqual(
            plan(value["result"]),
            [
                ("alpha", "0001_initial", "forward"),
                ("alpha", "0002_second", "forward"),
                ("alpha", "0003_third", "forward"),
            ],
        )
        self.assertEqual(applied(value["db"]["after"]), [scenarios._LEGACY])
        self.assert_zero_mutation(value, "fresh_executor")
        self.assert_clean_default_database()

    def test_inconsistent_known_history_runs_explicit_preflight_not_plan(self) -> None:
        with patch.object(
            MigrationExecutor,
            "migration_plan",
            side_effect=AssertionError("plan must not run after invalid history"),
        ):
            value = observed(scenarios.inconsistent_known_history, "MIG-035")

        self.assertIsNone(value["result"])
        self.assertEqual(
            value["raw"]["error"],
            {
                "category": "migration_history_error",
                "code": "inconsistent_applied_history",
                "message_is_contract": False,
                "python_type": (
                    "django.db.migrations.exceptions.InconsistentMigrationHistory"
                ),
            },
        )
        self.assertEqual(applied(value["db"]["after"]), [scenarios._A2])
        self.assertEqual(
            value["metrics"]["request"],
            {
                "applied_migrations": [
                    {"app": "alpha", "name": "0002_second"}
                ],
                "operation": "validate_history_before_planning",
                "plan_invoked": False,
                "target": {"app": "alpha", "name": "0003_third"},
            },
        )
        self.assert_zero_mutation(value, "fresh_executor")
        self.assert_clean_default_database()

    def test_middle_failure_restart_reads_prefix_and_replans_failed_tail(self) -> None:
        value = observed(scenarios.failure_tail, "MIG-036")

        self.assertEqual(applied(value["db"]["before"]), [scenarios._A1])
        self.assertEqual(
            columns(value["db"]["before"]),
            ["a1_marker", "id"],
        )
        self.assertEqual(
            value["result"]["failed_migration"],
            {"app": "alpha", "name": "0002_second"},
        )
        self.assertEqual(
            plan(value["result"]),
            [
                ("alpha", "0002_second", "forward"),
                ("alpha", "0003_third", "forward"),
            ],
        )
        self.assert_zero_mutation(value, "fresh_executor")
        self.assert_clean_default_database()

    def test_capture_includes_fresh_loader_construction(self) -> None:
        original = scenarios._FixtureMigrationLoader.build_graph
        build_count = 0

        def mutating_build_graph(loader):
            nonlocal build_count
            build_count += 1
            original(loader)
            if build_count == 2:
                with loader.connection.cursor() as cursor:
                    cursor.execute("CREATE TABLE godj_restart_probe (id integer)")

        with patch.object(
            scenarios._FixtureMigrationLoader,
            "build_graph",
            mutating_build_graph,
        ):
            with self.assertRaisesRegex(
                AssertionError,
                "restart read or planning changed database state",
            ):
                scenarios.applied_prefix_tail("MIG-032")
        self.assert_clean_default_database()

    def test_payload_excludes_sql_select_count_timestamp_and_private_cache(self) -> None:
        keys: set[str] = set()

        def collect(value) -> None:
            if isinstance(value, dict):
                keys.update(value)
                for item in value.values():
                    collect(item)
            elif isinstance(value, list):
                for item in value:
                    collect(item)

        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 27):
            collect(scenario(f"MIG-{number:03d}"))
        self.assertNotIn("sql", keys)
        self.assertNotIn("select_statement_count", keys)
        self.assertNotIn("applied_timestamp", keys)
        self.assertNotIn("cache_identity", keys)

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_two_independent_hashseed_processes_match_checked_in_oracle(self) -> None:
        outputs: list[bytes] = []
        with tempfile.TemporaryDirectory() as temporary_directory:
            for index, hash_seed in enumerate(("17", "982451653"), 1):
                output = Path(temporary_directory) / f"restart-{index}.json"
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
