from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from django.db import connection, models
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.operations.fields import AddField
from django.db.migrations.recorder import MigrationRecorder

from conformance.runners.django import (
    migration_state_reconstruction_scenarios as scenarios,
)


ROOT = Path(__file__).resolve().parents[4]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = (
    ROOT / "conformance/contracts/migration-state-reconstruction-manifest.json"
)
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64"
    / "migration-state-reconstruction-oracle.json"
)
STATIC = (
    ROOT
    / "conformance/fixtures"
    / "godj-migration-state-reconstruction-not-implemented.json"
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
        "result": denormalize(observation["result"]),
        "db": denormalize(observation["db_state"]),
        "metrics": denormalize(observation["metrics"]),
    }


def app(state, label):
    return next(item for item in state["apps"] if item["label"] == label)


def model(state, app_label, model_name):
    return next(
        item
        for item in app(state, app_label)["models"]
        if item["name"] == model_name
    )


def field(state, app_label, model_name, field_name):
    return next(
        item
        for item in model(state, app_label, model_name)["fields"]
        if item["name"] == field_name
    )


def field_names(state, app_label, model_name):
    return [
        item["name"]
        for item in model(state, app_label, model_name)["fields"]
    ]


def applied_keys(value):
    return [(item["app"], item["name"]) for item in value]


class MigrationStateReconstructionScenarioTests(unittest.TestCase):
    def assert_clean_default_database(self) -> None:
        self.assertEqual(connection.introspection.table_names(), [])
        self.assertFalse(MigrationRecorder(connection).has_table())

    def assert_zero_mutation(self, value, boundary: str) -> None:
        self.assertEqual(value["db"]["before"], value["db"]["after"])
        self.assertEqual(value["metrics"]["capture_boundary"], boundary)
        self.assertEqual(value["metrics"]["ddl_statement_count"], 0)
        self.assertEqual(value["metrics"]["non_select_statement_count"], 0)
        self.assertEqual(value["metrics"]["write_statement_count"], 0)
        self.assertTrue(value["metrics"]["state_unchanged"])
        self.assertEqual(
            value["metrics"]["replay_source"],
            "loaded_migration_definitions",
        )

        state = value["result"]["state"]
        logical_tables = sorted(
            state_model["db_table"]
            for state_app in state["apps"]
            for state_model in state_app["models"]
        )
        live_tables = sorted(
            table["name"]
            for table in value["db"]["before"]["managed_schema"]
        )
        self.assertEqual(live_tables, [scenarios._DIVERGENT_TABLE])
        self.assertNotEqual(logical_tables, live_tables)

    def test_registry_order_matches_mig_037_through_046(self) -> None:
        self.assertEqual(
            list(scenarios.SCENARIOS),
            [
                "django.migration.state_reconstruction.explicit_empty",
                "django.migration.state_reconstruction.first_before",
                "django.migration.state_reconstruction.first_after",
                "django.migration.state_reconstruction.linear_middle_after",
                "django.migration.state_reconstruction.linear_middle_before",
                "django.migration.state_reconstruction.cross_app_dependency",
                (
                    "django.migration.state_reconstruction."
                    "multiple_targets_shared_dependency"
                ),
                "django.migration.state_reconstruction.latest_leaves",
                (
                    "django.migration.state_reconstruction."
                    "applied_prefix_startup"
                ),
                (
                    "django.migration.state_reconstruction."
                    "unrelated_known_unknown_startup"
                ),
            ],
        )

    def test_every_public_scenario_is_independent_of_contract_id(self) -> None:
        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 37):
            with self.subTest(scenario=scenario.__name__):
                actual_id = scenario(f"MIG-{number:03d}")
                arbitrary_id = scenario("MIG-untrusted")
                self.assertEqual(actual_id["id"], f"MIG-{number:03d}")
                self.assertEqual(arbitrary_id["id"], "MIG-untrusted")
                self.assertEqual(
                    {key: value for key, value in actual_id.items() if key != "id"},
                    {
                        key: value
                        for key, value in arbitrary_id.items()
                        if key != "id"
                    },
                )
        self.assert_clean_default_database()

    def test_manifest_locks_exact_mapping_and_pinned_provenance(self) -> None:
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        self.assertEqual(manifest["format_version"], 2)
        self.assertEqual(
            manifest["profile_id"],
            "django-6.1-sqlite-darwin-arm64",
        )
        self.assertEqual(
            [contract["id"] for contract in manifest["contracts"]],
            [f"MIG-{number:03d}" for number in range(37, 47)],
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
                    ["result", "db_state", "metrics"],
                )
                self.assertTrue(contract["provenance"])
                for provenance in contract["provenance"]:
                    self.assertIn(
                        "django@fe0a859f537d4238cf49fca39073513206f83122:",
                        provenance["reference"],
                    )
                    self.assertIs(provenance["derived"], False)
                    self.assertEqual(provenance["license"], "BSD-3-Clause")

        public_startup_reference = (
            "django@fe0a859f537d4238cf49fca39073513206f83122:"
            "django/db/migrations/executor.py::MigrationExecutor.migrate"
        )
        for contract in manifest["contracts"][-2:]:
            self.assertIn(
                public_startup_reference,
                [item["reference"] for item in contract["provenance"]],
            )

    def test_static_fixture_is_explicitly_not_implemented(self) -> None:
        fixture = json.loads(STATIC.read_text(encoding="utf-8"))
        self.assertEqual(fixture["format_version"], 2)
        self.assertEqual(
            [item["id"] for item in fixture["contracts"]],
            [f"MIG-{number:03d}" for number in range(37, 47)],
        )
        self.assertEqual(
            {item["status"] for item in fixture["contracts"]},
            {"not_implemented"},
        )
        self.assertEqual(
            {item["phase"] for item in fixture["contracts"]},
            {"evaluation"},
        )

    def test_explicit_empty_and_first_before_ignore_divergent_live_schema(self) -> None:
        empty = observed(scenarios.explicit_empty, "MIG-037")
        self.assertEqual(empty["result"]["state"], {"apps": [], "format_version": 1})
        self.assertEqual(
            empty["metrics"]["request"],
            {"mode": "explicit_nodes", "position": "after", "targets": []},
        )
        self.assert_zero_mutation(empty, "fresh_loader")
        self.assert_clean_default_database()

        before = observed(scenarios.first_before, "MIG-038")
        self.assertEqual(before["result"]["state"], {"apps": [], "format_version": 1})
        self.assertEqual(
            before["metrics"]["request"],
            {
                "mode": "explicit_nodes",
                "position": "before",
                "targets": [{"app": "alpha", "name": "0002_root"}],
            },
        )
        self.assert_zero_mutation(before, "fresh_loader")
        self.assert_clean_default_database()

    def test_first_after_normalizes_models_and_field_metadata(self) -> None:
        value = observed(scenarios.first_after, "MIG-039")
        state = value["result"]["state"]

        self.assertEqual([item["label"] for item in state["apps"]], ["alpha"])
        self.assertEqual(
            [item["name"] for item in app(state, "alpha")["models"]],
            ["entry", "zulu"],
        )
        self.assertEqual(
            model(state, "alpha", "entry")["db_table"],
            "godj_state_alpha_entry",
        )
        self.assertEqual(
            field_names(state, "alpha", "entry"),
            ["id", "headline"],
        )
        self.assertEqual(
            field(state, "alpha", "entry", "id"),
            {
                "column": "id",
                "default": {
                    "present": False,
                    "type": "absent",
                    "value": None,
                },
                "kind": "auto",
                "max_length": None,
                "name": "id",
                "nullable": False,
                "primary_key": True,
            },
        )
        self.assertEqual(
            field(state, "alpha", "entry", "headline"),
            {
                "column": "headline_text",
                "default": {
                    "present": True,
                    "type": "string",
                    "value": "",
                },
                "kind": "char",
                "max_length": 64,
                "name": "headline",
                "nullable": False,
                "primary_key": False,
            },
        )
        self.assertEqual(
            field(state, "alpha", "zulu", "active")["default"],
            {"present": True, "type": "bool", "value": True},
        )
        self.assert_zero_mutation(value, "fresh_loader")
        self.assert_clean_default_database()

    def test_middle_before_after_use_dependency_order_not_lexical_order(self) -> None:
        after = observed(scenarios.linear_middle_after, "MIG-040")
        before = observed(scenarios.linear_middle_before, "MIG-041")

        self.assertEqual(
            field_names(after["result"]["state"], "alpha", "entry"),
            ["id", "headline", "published"],
        )
        self.assertEqual(
            field(after["result"]["state"], "alpha", "entry", "published"),
            {
                "column": "published",
                "default": {
                    "present": True,
                    "type": "bool",
                    "value": False,
                },
                "kind": "boolean",
                "max_length": None,
                "name": "published",
                "nullable": False,
                "primary_key": False,
            },
        )
        self.assertNotIn(
            "summary",
            field_names(after["result"]["state"], "alpha", "entry"),
        )
        self.assertEqual(
            field_names(before["result"]["state"], "alpha", "entry"),
            ["id", "headline"],
        )

        nodes = applied_keys(after["metrics"]["graph"]["nodes"])
        self.assertLess(
            nodes.index(scenarios._ALPHA_MIDDLE),
            nodes.index(scenarios._ALPHA_ROOT),
        )
        dependencies = after["metrics"]["graph"]["dependencies"]
        self.assertIn(
            {
                "child": {"app": "alpha", "name": "0001_middle"},
                "parent": {"app": "alpha", "name": "0002_root"},
            },
            dependencies,
        )
        self.assert_zero_mutation(after, "fresh_loader")
        self.assert_zero_mutation(before, "fresh_loader")
        self.assert_clean_default_database()

    def test_cross_app_multiple_target_and_latest_leaf_projection(self) -> None:
        cross = observed(scenarios.cross_app_dependency, "MIG-042")
        self.assertEqual(
            [item["label"] for item in cross["result"]["state"]["apps"]],
            ["alpha", "beta"],
        )
        beta_code = field(cross["result"]["state"], "beta", "audit", "code")
        self.assertTrue(beta_code["nullable"])
        self.assertEqual(beta_code["max_length"], 32)
        self.assertEqual(
            beta_code["default"],
            {"present": False, "type": "absent", "value": None},
        )
        self.assert_zero_mutation(cross, "fresh_loader")
        self.assert_clean_default_database()

        multiple = observed(
            scenarios.multiple_targets_shared_dependency,
            "MIG-043",
        )
        self.assertEqual(
            [item["label"] for item in multiple["result"]["state"]["apps"]],
            ["alpha", "beta", "gamma"],
        )
        self.assertEqual(
            [
                item["name"]
                for item in app(multiple["result"]["state"], "alpha")["models"]
            ],
            ["entry", "zulu"],
        )
        self.assert_zero_mutation(multiple, "fresh_loader")
        self.assert_clean_default_database()

        latest = observed(scenarios.latest_leaves, "MIG-044")
        self.assertEqual(
            [item["label"] for item in latest["result"]["state"]["apps"]],
            ["alpha", "beta", "delta", "gamma"],
        )
        self.assertEqual(
            field_names(latest["result"]["state"], "alpha", "entry"),
            ["id", "headline", "published", "summary"],
        )
        summary = field(latest["result"]["state"], "alpha", "entry", "summary")
        self.assertTrue(summary["nullable"])
        self.assertEqual(summary["max_length"], 255)
        self.assertEqual(
            summary["default"],
            {"present": False, "type": "absent", "value": None},
        )
        self.assertEqual(
            latest["metrics"]["request"],
            {"mode": "latest", "position": "after", "targets": []},
        )
        self.assert_zero_mutation(latest, "fresh_loader")
        self.assert_clean_default_database()

    def test_applied_startup_replays_known_prefix_and_preserves_unknown_identity(self) -> None:
        prefix = observed(scenarios.applied_prefix_startup, "MIG-045")
        expected_prefix = [scenarios._ALPHA_MIDDLE, scenarios._ALPHA_ROOT]
        self.assertEqual(
            applied_keys(prefix["result"]["applied_migrations"]),
            expected_prefix,
        )
        self.assertEqual(
            applied_keys(prefix["result"]["known_applied_migrations"]),
            expected_prefix,
        )
        self.assertEqual(prefix["result"]["unknown_applied_migrations"], [])
        self.assertEqual(
            field_names(prefix["result"]["state"], "alpha", "entry"),
            ["id", "headline", "published"],
        )
        self.assertNotIn(
            "summary",
            field_names(prefix["result"]["state"], "alpha", "entry"),
        )
        self.assertEqual(
            applied_keys(prefix["db"]["before"]["applied_migrations"]),
            expected_prefix,
        )
        self.assert_zero_mutation(prefix, "fresh_executor")
        self.assert_clean_default_database()

        mixed = observed(
            scenarios.unrelated_known_unknown_startup,
            "MIG-046",
        )
        self.assertEqual(
            applied_keys(mixed["result"]["applied_migrations"]),
            [scenarios._ALPHA_ROOT, scenarios._DELTA_ROOT, scenarios._LEGACY],
        )
        self.assertEqual(
            applied_keys(mixed["result"]["known_applied_migrations"]),
            [scenarios._ALPHA_ROOT, scenarios._DELTA_ROOT],
        )
        self.assertEqual(
            applied_keys(mixed["result"]["unknown_applied_migrations"]),
            [scenarios._LEGACY],
        )
        self.assertEqual(
            [item["label"] for item in mixed["result"]["state"]["apps"]],
            ["alpha", "delta"],
        )
        self.assertNotIn(
            "legacy",
            [item["label"] for item in mixed["result"]["state"]["apps"]],
        )
        self.assert_zero_mutation(mixed, "fresh_executor")
        self.assert_clean_default_database()

    def test_applied_startup_uses_public_empty_migrate_route(self) -> None:
        original = MigrationExecutor.migrate
        calls = []

        def spy(executor, targets, plan=None, **kwargs):
            calls.append({"plan": plan, "targets": targets})
            return original(executor, targets, plan=plan, **kwargs)

        with patch.object(MigrationExecutor, "migrate", spy):
            scenarios.applied_prefix_startup("MIG-045")
        self.assertEqual(calls, [{"plan": [], "targets": []}])
        self.assert_clean_default_database()

    def test_capture_includes_fresh_loader_construction(self) -> None:
        original = scenarios._FixtureMigrationLoader.build_graph

        def mutating_build_graph(loader):
            original(loader)
            with connection.cursor() as cursor:
                cursor.execute("CREATE TABLE godj_state_capture_probe (id integer)")

        with patch.object(
            scenarios._FixtureMigrationLoader,
            "build_graph",
            mutating_build_graph,
        ):
            with self.assertRaisesRegex(
                AssertionError,
                "state reconstruction changed database state",
            ):
                scenarios.first_after("MIG-039")
        self.assert_clean_default_database()

    def test_result_is_derived_from_fixture_operations_not_contract_id(self) -> None:
        original = scenarios._fixture_migrations

        def mutated_fixture():
            migrations = original()
            migrations[scenarios._ALPHA_ROOT].operations.append(
                AddField(
                    model_name="entry",
                    name="fixture_probe",
                    field=models.BooleanField(default=True),
                )
            )
            return migrations

        baseline = observed(scenarios.first_after, "MIG-untrusted")
        with patch.object(
            scenarios,
            "_fixture_migrations",
            mutated_fixture,
        ):
            mutated = observed(scenarios.first_after, "MIG-untrusted")

        self.assertNotIn(
            "fixture_probe",
            field_names(baseline["result"]["state"], "alpha", "entry"),
        )
        self.assertIn(
            "fixture_probe",
            field_names(mutated["result"]["state"], "alpha", "entry"),
        )
        self.assertNotEqual(baseline["raw"]["result"], mutated["raw"]["result"])
        self.assert_clean_default_database()

    def test_projection_follows_target_and_dependency_inputs_not_contract_id(
        self,
    ) -> None:
        root = observed(
            lambda contract_id: scenarios._project_state_observation(
                contract_id,
                nodes=(scenarios._ALPHA_ROOT,),
                at_end=True,
                mode="explicit_nodes",
            ),
            "MIG-untrusted",
        )
        middle = observed(
            lambda contract_id: scenarios._project_state_observation(
                contract_id,
                nodes=(scenarios._ALPHA_MIDDLE,),
                at_end=True,
                mode="explicit_nodes",
            ),
            "MIG-untrusted",
        )
        self.assertNotEqual(root["raw"]["result"], middle["raw"]["result"])
        self.assertNotIn(
            "published",
            field_names(root["result"]["state"], "alpha", "entry"),
        )
        self.assertIn(
            "published",
            field_names(middle["result"]["state"], "alpha", "entry"),
        )

        original = scenarios._fixture_migrations

        def without_cross_app_dependency():
            migrations = original()
            migrations[scenarios._BETA_ROOT].dependencies = []
            return migrations

        baseline = observed(
            scenarios.cross_app_dependency,
            "MIG-untrusted",
        )
        with patch.object(
            scenarios,
            "_fixture_migrations",
            without_cross_app_dependency,
        ):
            mutated = observed(
                scenarios.cross_app_dependency,
                "MIG-untrusted",
            )
        self.assertEqual(
            [item["label"] for item in baseline["result"]["state"]["apps"]],
            ["alpha", "beta"],
        )
        self.assertEqual(
            [item["label"] for item in mutated["result"]["state"]["apps"]],
            ["beta"],
        )
        self.assertNotEqual(baseline["raw"]["result"], mutated["raw"]["result"])
        self.assertNotEqual(baseline["raw"]["metrics"], mutated["raw"]["metrics"])
        self.assert_clean_default_database()

    def test_applied_and_live_database_inputs_propagate_without_id_dispatch(
        self,
    ) -> None:
        public_prefix = observed(
            scenarios.applied_prefix_startup,
            "MIG-untrusted",
        )
        public_mixed = observed(
            scenarios.unrelated_known_unknown_startup,
            "MIG-untrusted",
        )
        expected_prefix = [scenarios._ALPHA_MIDDLE, scenarios._ALPHA_ROOT]
        self.assertEqual(
            applied_keys(public_prefix["result"]["applied_migrations"]),
            expected_prefix,
        )
        self.assertEqual(
            applied_keys(public_prefix["db"]["before"]["applied_migrations"]),
            expected_prefix,
        )
        self.assertEqual(
            applied_keys(public_mixed["result"]["applied_migrations"]),
            [scenarios._ALPHA_ROOT, scenarios._DELTA_ROOT, scenarios._LEGACY],
        )
        self.assertEqual(
            applied_keys(public_mixed["result"]["unknown_applied_migrations"]),
            [scenarios._LEGACY],
        )

        prefix = observed(
            lambda contract_id: scenarios._applied_state_observation(
                contract_id,
                (scenarios._ALPHA_ROOT,),
            ),
            "MIG-untrusted",
        )
        mixed = observed(
            lambda contract_id: scenarios._applied_state_observation(
                contract_id,
                (
                    scenarios._ALPHA_ROOT,
                    scenarios._DELTA_ROOT,
                    scenarios._LEGACY,
                ),
            ),
            "MIG-untrusted",
        )
        self.assertNotEqual(prefix["raw"]["result"], mixed["raw"]["result"])
        self.assertNotEqual(prefix["raw"]["db_state"], mixed["raw"]["db_state"])
        self.assertEqual(
            applied_keys(mixed["result"]["unknown_applied_migrations"]),
            [scenarios._LEGACY],
        )

        baseline = observed(scenarios.first_after, "MIG-untrusted")

        def changed_divergent_schema() -> None:
            with connection.cursor() as cursor:
                cursor.execute(
                    "CREATE TABLE godj_state_live_decoy "
                    "(changed text NULL)"
                )

        with patch.object(
            scenarios,
            "_setup_divergent_schema",
            changed_divergent_schema,
        ):
            mutated = observed(scenarios.first_after, "MIG-untrusted")
        self.assertEqual(baseline["raw"]["result"], mutated["raw"]["result"])
        self.assertNotEqual(baseline["raw"]["db_state"], mutated["raw"]["db_state"])
        self.assert_clean_default_database()

    def test_normalizer_rejects_out_of_scope_field_and_default_shapes(self) -> None:
        unsupported_kind = models.IntegerField()
        unsupported_kind.set_attributes_from_name("count")
        state = scenarios.ProjectState()
        migration = scenarios.Migration("probe", "probe")
        migration.operations = [
            scenarios.CreateModel(
                name="Probe",
                fields=[("count", unsupported_kind)],
                options={"db_table": "godj_state_probe"},
            )
        ]
        mutated = migration.mutate_state(state)
        with self.assertRaisesRegex(AssertionError, "unsupported field kind"):
            scenarios._state_value(mutated)

        invalid_default = models.CharField(default=False, max_length=8)
        with self.assertRaisesRegex(AssertionError, "unsupported default type"):
            scenarios._default_value(invalid_default, "char")

        callable_default = models.BooleanField(default=lambda: True)
        with self.assertRaisesRegex(AssertionError, "callable defaults"):
            scenarios._default_value(callable_default, "boolean")

    def test_payload_excludes_sql_select_counts_and_private_object_shape(self) -> None:
        keys: set[str] = set()

        def collect(value) -> None:
            if isinstance(value, dict):
                keys.update(value)
                for item in value.values():
                    collect(item)
            elif isinstance(value, list):
                for item in value:
                    collect(item)

        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 37):
            collect(scenario(f"MIG-{number:03d}"))
        self.assertNotIn("sql", keys)
        self.assertNotIn("select_statement_count", keys)
        self.assertNotIn("object_identity", keys)
        self.assertNotIn("cache_identity", keys)
        self.assertNotIn("apps_registry", keys)
        self.assertNotIn("go_name", keys)
        self.assert_clean_default_database()

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_two_independent_hashseed_processes_match_checked_in_oracle(self) -> None:
        outputs: list[bytes] = []
        with tempfile.TemporaryDirectory() as temporary_directory:
            for index, hash_seed in enumerate(("17", "982451653"), 1):
                output = Path(temporary_directory) / f"state-{index}.json"
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
