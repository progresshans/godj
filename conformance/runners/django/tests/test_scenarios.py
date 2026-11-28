from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

from django.db import connection
from django.db.migrations.recorder import MigrationRecorder

from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import (
    DEFAULT_DRF_PROFILE,
    DEFAULT_MANIFEST,
    SUITES,
    DEFAULT_PROFILE,
    ProfileMismatch,
    API_AUTHENTICATION_SCENARIOS,
    MIGRATION_WRITER_SCENARIOS,
    MIGRATION_STATUS_SCENARIOS,
    MIGRATION_TARGET_PLAN_SCENARIOS,
    MIGRATION_SQL_RENDERING_SCENARIOS,
    SCENARIOS as ALL_SCENARIOS,
    _load_json,
    _run_contract,
    _validate_manifest_basics,
    generate_suite,
    verify_profile,
)
from conformance.runners.django.save_lifecycle_scenarios import (
    SCENARIOS as SAVE_LIFECYCLE_SCENARIOS,
)
from conformance.runners.django.migration_planning_scenarios import (
    SCENARIOS as MIGRATION_PLANNING_SCENARIOS,
)
from conformance.runners.django.migration_execution_scenarios import (
    SCENARIOS as MIGRATION_EXECUTION_SCENARIOS,
)
from conformance.runners.django.migration_restart_scenarios import (
    SCENARIOS as MIGRATION_RESTART_SCENARIOS,
)
from conformance.runners.django.migration_state_reconstruction_scenarios import (
    SCENARIOS as MIGRATION_STATE_RECONSTRUCTION_SCENARIOS,
)
from conformance.runners.django.migration_lifecycle_scenarios import (
    SCENARIOS as MIGRATION_LIFECYCLE_SCENARIOS,
)
from conformance.runners.django.migration_definition_source_scenarios import (
    SCENARIOS as MIGRATION_DEFINITION_SOURCE_SCENARIOS,
)
from conformance.runners.django.migration_project_check_scenarios import (
    SCENARIOS as MIGRATION_PROJECT_CHECK_SCENARIOS,
)
from conformance.runners.django.migration_command_decisions import (
    SCENARIOS as MIGRATION_COMMAND_DECISION_SCENARIOS,
)
from conformance.runners.django.migration_relation_scenarios import (
    SCENARIOS as MIGRATION_RELATION_SCENARIOS,
)
from conformance.runners.django.query_cache_scenarios import (
    SCENARIOS as QUERY_CACHE_SCENARIOS,
)
from conformance.runners.django.query_breadth_scenarios import (
    SCENARIOS as QUERY_BREADTH_SCENARIOS,
)
from conformance.runners.django.query_expression_scenarios import (
    SCENARIOS as QUERY_EXPRESSION_SCENARIOS,
)
from conformance.runners.django.template_form_scenarios import (
    SCENARIOS as TEMPLATE_FORM_SCENARIOS,
)
from conformance.runners.django.auth_admin_proxy import (
    ADMIN_SCENARIOS,
    AUTH_SCENARIOS,
    SCENARIOS as AUTH_ADMIN_SCENARIOS,
)
from conformance.runners.django.article_api_proxy import (
    ARTICLE_API_SCENARIOS,
    PARAMETER_ROUTING_SCENARIOS,
    SCENARIOS as DRF_SCENARIOS,
)
from conformance.runners.django.runner import SYSTEM_STATE_SCENARIOS
from conformance.runners.django.relation_scenarios import (
    SCENARIOS as RELATION_SCENARIOS,
)
from conformance.runners.django.scenarios import SCENARIOS as QUERY_SCENARIOS
from conformance.runners.django import write_migration_scenarios
from conformance.runners.django.write_migration_scenarios import (
    SCENARIOS as WRITE_MIGRATION_SCENARIOS,
)


class ScenarioTests(unittest.TestCase):
    def assert_runner_baseline(self, scenario: str) -> None:
        self.assertTrue(
            connection.get_autocommit(),
            f"{scenario}: autocommit was not restored",
        )
        self.assertFalse(
            connection.in_atomic_block,
            f"{scenario}: atomic block leaked",
        )
        self.assertFalse(
            connection.needs_rollback,
            f"{scenario}: rollback state leaked",
        )

        tables = connection.introspection.table_names()
        managed_tables = [
            table
            for table in tables
            if table == "godj_conformance_article"
            or table.startswith(write_migration_scenarios.MANAGED_TABLE_PREFIXES)
        ]
        self.assertEqual(
            managed_tables,
            [],
            f"{scenario}: managed scenario tables leaked",
        )
        self.assertFalse(
            MigrationRecorder(connection).has_table(),
            f"{scenario}: migration recorder table leaked",
        )
        self.assertEqual(tables, [], f"{scenario}: database tables leaked")

    def test_all_scenarios_are_byte_deterministic(self) -> None:
        drf_available = importlib.util.find_spec("rest_framework") is not None
        for name, scenario in ALL_SCENARIOS.items():
            with self.subTest(scenario=name):
                if name.startswith("drf.") and not drf_available:
                    continue
                contract_id = f"TEST-{name}"
                first = canonical_json(scenario(contract_id))
                self.assert_runner_baseline(f"{name} first run")
                second = canonical_json(scenario(contract_id))
                self.assert_runner_baseline(f"{name} second run")
                self.assertEqual(first, second)

    def test_baseline_rejects_omitted_migration_recorder_cleanup(self) -> None:
        def cleanup_without_recorder_drop() -> None:
            write_migration_scenarios._migrate(
                write_migration_scenarios.FAILURE_APP,
                "zero",
            )
            write_migration_scenarios._migrate(
                write_migration_scenarios.MIGRATION_APP,
                "zero",
            )

        try:
            with patch.object(
                write_migration_scenarios,
                "_cleanup_migrations",
                cleanup_without_recorder_drop,
            ):
                write_migration_scenarios.migration_create_model("MIG-001")

            with self.assertRaisesRegex(
                AssertionError,
                "migration recorder table leaked",
            ):
                self.assert_runner_baseline("recorder cleanup mutation")
        finally:
            write_migration_scenarios._cleanup_migrations()

        self.assert_runner_baseline("recorder cleanup mutation recovery")

    def test_each_contract_set_count_is_within_protocol_bound(self) -> None:
        for scenarios in (
            QUERY_SCENARIOS,
            WRITE_MIGRATION_SCENARIOS,
            SAVE_LIFECYCLE_SCENARIOS,
            QUERY_CACHE_SCENARIOS,
            QUERY_BREADTH_SCENARIOS,
            QUERY_EXPRESSION_SCENARIOS,
            MIGRATION_PLANNING_SCENARIOS,
            MIGRATION_EXECUTION_SCENARIOS,
            MIGRATION_RESTART_SCENARIOS,
            MIGRATION_STATE_RECONSTRUCTION_SCENARIOS,
            MIGRATION_LIFECYCLE_SCENARIOS,
            MIGRATION_DEFINITION_SOURCE_SCENARIOS,
            MIGRATION_PROJECT_CHECK_SCENARIOS,
            MIGRATION_COMMAND_DECISION_SCENARIOS,
            MIGRATION_WRITER_SCENARIOS,
            MIGRATION_STATUS_SCENARIOS,
            MIGRATION_TARGET_PLAN_SCENARIOS,
            MIGRATION_SQL_RENDERING_SCENARIOS,
            RELATION_SCENARIOS,
            MIGRATION_RELATION_SCENARIOS,
            {name: DRF_SCENARIOS[name] for name in PARAMETER_ROUTING_SCENARIOS},
            {name: DRF_SCENARIOS[name] for name in ARTICLE_API_SCENARIOS},
            API_AUTHENTICATION_SCENARIOS,
            SYSTEM_STATE_SCENARIOS,
        ):
            with self.subTest(scenarios=sorted(scenarios)):
                self.assertGreaterEqual(len(scenarios), 8)
                if scenarios is QUERY_EXPRESSION_SCENARIOS:
                    self.assertEqual(len(scenarios), 20)
                elif scenarios is SYSTEM_STATE_SCENARIOS:
                    self.assertEqual(len(scenarios), 30)
                else:
                    self.assertLessEqual(len(scenarios), 12)

    def test_each_manifest_matches_its_scenario_registry_exactly(self) -> None:
        contract_sets = (
            (DEFAULT_MANIFEST, QUERY_SCENARIOS),
            (SUITES["write-migration"].manifest, WRITE_MIGRATION_SCENARIOS),
            (SUITES["save-lifecycle"].manifest, SAVE_LIFECYCLE_SCENARIOS),
            (SUITES["query-cache"].manifest, QUERY_CACHE_SCENARIOS),
            (SUITES["query-breadth"].manifest, QUERY_BREADTH_SCENARIOS),
            (SUITES["query-expression"].manifest, QUERY_EXPRESSION_SCENARIOS),
            (SUITES["migration-planning"].manifest, MIGRATION_PLANNING_SCENARIOS),
            (SUITES["migration-execution"].manifest, MIGRATION_EXECUTION_SCENARIOS),
            (SUITES["migration-restart"].manifest, MIGRATION_RESTART_SCENARIOS),
            (
                SUITES["migration-state-reconstruction"].manifest,
                MIGRATION_STATE_RECONSTRUCTION_SCENARIOS,
            ),
            (
                SUITES["migration-lifecycle"].manifest,
                MIGRATION_LIFECYCLE_SCENARIOS,
            ),
            (
                SUITES["migration-definition-source"].manifest,
                MIGRATION_DEFINITION_SOURCE_SCENARIOS,
            ),
            (
                SUITES["migration-project-check"].manifest,
                MIGRATION_PROJECT_CHECK_SCENARIOS,
            ),
            (
                SUITES["migration-command"].manifest,
                MIGRATION_COMMAND_DECISION_SCENARIOS,
            ),
            (SUITES["migration-writer"].manifest, MIGRATION_WRITER_SCENARIOS),
            (SUITES["migration-status"].manifest, MIGRATION_STATUS_SCENARIOS),
            (
                SUITES["migration-target-plan"].manifest,
                MIGRATION_TARGET_PLAN_SCENARIOS,
            ),
            (
                SUITES["migration-sql-rendering"].manifest,
                MIGRATION_SQL_RENDERING_SCENARIOS,
            ),
            (SUITES["relation"].manifest, RELATION_SCENARIOS),
            (
                SUITES["migration-relation"].manifest,
                MIGRATION_RELATION_SCENARIOS,
            ),
            (SUITES["template-form"].manifest, TEMPLATE_FORM_SCENARIOS),
            (
                SUITES["auth-session"].manifest,
                {name: AUTH_ADMIN_SCENARIOS[name] for name in AUTH_SCENARIOS},
            ),
            (
                SUITES["article-admin"].manifest,
                {name: AUTH_ADMIN_SCENARIOS[name] for name in ADMIN_SCENARIOS},
            ),
            (
                SUITES["parameter-routing"].manifest,
                {name: DRF_SCENARIOS[name] for name in PARAMETER_ROUTING_SCENARIOS},
            ),
            (
                SUITES["article-api"].manifest,
                {name: DRF_SCENARIOS[name] for name in ARTICLE_API_SCENARIOS},
            ),
            (SUITES["api-authentication"].manifest, API_AUTHENTICATION_SCENARIOS),
            (SUITES["system-state"].manifest, SYSTEM_STATE_SCENARIOS),
        )
        self.assertEqual(len(contract_sets), 27)
        selected_across_sets = []
        contract_ids_across_sets = []
        inventories = []
        for manifest_path, registry in contract_sets:
            with self.subTest(manifest=manifest_path.name):
                manifest = _load_json(manifest_path)
                selected = [
                    contract["scenario"] for contract in manifest["contracts"]
                ]
                self.assertEqual(len(selected), len(set(selected)))
                self.assertEqual(set(selected), set(registry))
                selected_across_sets.extend(selected)
                contract_ids = [
                    contract["id"] for contract in manifest["contracts"]
                ]
                self.assertEqual(len(contract_ids), len(set(contract_ids)))
                contract_ids_across_sets.extend(contract_ids)
                inventories.append(
                    (
                        manifest_path.name,
                        frozenset(selected),
                        frozenset(contract_ids),
                    )
                )
        self.assertEqual(len(selected_across_sets), 311)
        self.assertEqual(len(selected_across_sets), len(set(selected_across_sets)))
        self.assertEqual(set(selected_across_sets), set(ALL_SCENARIOS))
        self.assertEqual(len(contract_ids_across_sets), 311)
        self.assertEqual(
            len(contract_ids_across_sets), len(set(contract_ids_across_sets))
        )
        cross_bindings = 0
        for source_name, source_scenarios, source_contract_ids in inventories:
            for target_name, target_scenarios, target_contract_ids in inventories:
                if source_name == target_name:
                    continue
                with self.subTest(source=source_name, target=target_name):
                    self.assertTrue(source_scenarios.isdisjoint(target_scenarios))
                    self.assertTrue(
                        source_contract_ids.isdisjoint(target_contract_ids)
                    )
                cross_bindings += 1
        self.assertEqual(cross_bindings, 702)

    def test_one_manifest_does_not_require_other_set_scenarios(self) -> None:
        for profile_path, manifest_path in (
            (DEFAULT_PROFILE, DEFAULT_MANIFEST),
            (DEFAULT_PROFILE, SUITES["write-migration"].manifest),
            (DEFAULT_PROFILE, SUITES["save-lifecycle"].manifest),
            (DEFAULT_PROFILE, SUITES["query-cache"].manifest),
            (DEFAULT_PROFILE, SUITES["query-breadth"].manifest),
            (DEFAULT_PROFILE, SUITES["query-expression"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-planning"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-execution"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-restart"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-state-reconstruction"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-lifecycle"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-definition-source"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-project-check"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-command"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-writer"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-status"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-target-plan"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-sql-rendering"].manifest),
            (DEFAULT_PROFILE, SUITES["relation"].manifest),
            (DEFAULT_PROFILE, SUITES["migration-relation"].manifest),
            (DEFAULT_PROFILE, SUITES["template-form"].manifest),
            (DEFAULT_PROFILE, SUITES["auth-session"].manifest),
            (DEFAULT_PROFILE, SUITES["article-admin"].manifest),
            (DEFAULT_DRF_PROFILE, SUITES["parameter-routing"].manifest),
            (DEFAULT_DRF_PROFILE, SUITES["article-api"].manifest),
            (DEFAULT_DRF_PROFILE, SUITES["api-authentication"].manifest),
        ):
            profile = _load_json(profile_path)
            manifest = _load_json(manifest_path)
            self.assertEqual(
                len(_validate_manifest_basics(manifest, profile)),
                len(manifest["contracts"]),
            )

    def test_extended_query_expression_manifest_requires_exact_registry(
        self,
    ) -> None:
        profile = _load_json(DEFAULT_PROFILE)
        manifest = _load_json(SUITES["query-expression"].manifest)
        self.assertEqual(
            len(_validate_manifest_basics(manifest, profile)),
            20,
        )

        near_miss = deepcopy(manifest)
        near_miss["contracts"][-1]["scenario"] = near_miss["contracts"][0][
            "scenario"
        ]
        with self.assertRaisesRegex(RuntimeError, "exact query-expression registry"):
            _validate_manifest_basics(near_miss, profile)

    def test_locked_or_later_manifest_statuses_can_generate_oracle(self) -> None:
        profile = _load_json(DEFAULT_PROFILE)
        manifest = _load_json(DEFAULT_MANIFEST)
        for status in ("oracle_locked", "red", "passing", "deviation"):
            with self.subTest(status=status):
                candidate = deepcopy(manifest)
                for contract in candidate["contracts"]:
                    contract["status"] = status
                self.assertEqual(
                    len(_validate_manifest_basics(candidate, profile)),
                    len(candidate["contracts"]),
                )

        draft = deepcopy(manifest)
        draft["contracts"][0]["status"] = "draft"
        with self.assertRaisesRegex(RuntimeError, "locked-or-later"):
            _validate_manifest_basics(draft, profile)

    def test_manifest_requires_format_version_two_and_known_phase(self) -> None:
        profile = _load_json(DEFAULT_PROFILE)
        manifest = _load_json(DEFAULT_MANIFEST)

        old_format = deepcopy(manifest)
        old_format["format_version"] = 1
        with self.assertRaisesRegex(RuntimeError, "format_version must be 2"):
            _validate_manifest_basics(old_format, profile)

        unknown_phase = deepcopy(manifest)
        unknown_phase["contracts"][0]["phase"] = "future_phase"
        with self.assertRaisesRegex(RuntimeError, "unknown manifest phase"):
            _validate_manifest_basics(unknown_phase, profile)

    def test_profile_requires_format_version_two(self) -> None:
        old_format = deepcopy(_load_json(DEFAULT_PROFILE))
        old_format["format_version"] = 1

        with self.assertRaisesRegex(ProfileMismatch, "format_version must be 2"):
            verify_profile(old_format)

    def test_scenario_observation_phase_must_match_manifest(self) -> None:
        contract = deepcopy(_load_json(DEFAULT_MANIFEST)["contracts"][0])

        def wrong_phase(contract_id: str) -> dict[str, str]:
            return {
                "id": contract_id,
                "phase": "construction",
                "status": "observed",
            }

        with patch.dict(ALL_SCENARIOS, {contract["scenario"]: wrong_phase}):
            with self.assertRaisesRegex(RuntimeError, "does not match manifest phase"):
                _run_contract(contract)

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_locked_suite_is_byte_deterministic(self) -> None:
        first = canonical_json(generate_suite())
        second = canonical_json(generate_suite())
        self.assertEqual(first, second)

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_manifest_order_is_observation_order(self) -> None:
        suite = generate_suite()
        manifest_path = Path(__file__).resolve().parents[3] / "contracts/manifest.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_reference_suites_are_byte_deterministic_and_ordered(self) -> None:
        cases = (
            (SUITES["write-migration"].manifest, SUITES["write-migration"].oracle),
            (SUITES["save-lifecycle"].manifest, SUITES["save-lifecycle"].oracle),
            (SUITES["query-cache"].manifest, SUITES["query-cache"].oracle),
            (SUITES["query-breadth"].manifest, SUITES["query-breadth"].oracle),
            (SUITES["query-expression"].manifest, SUITES["query-expression"].oracle),
            (SUITES["migration-planning"].manifest, SUITES["migration-planning"].oracle),
            (SUITES["migration-execution"].manifest, SUITES["migration-execution"].oracle),
            (SUITES["migration-restart"].manifest, SUITES["migration-restart"].oracle),
            (SUITES["migration-state-reconstruction"].manifest, SUITES["migration-state-reconstruction"].oracle),
            (SUITES["migration-lifecycle"].manifest, SUITES["migration-lifecycle"].oracle),
            (SUITES["migration-definition-source"].manifest, SUITES["migration-definition-source"].oracle),
            (SUITES["migration-project-check"].manifest, SUITES["migration-project-check"].oracle),
            (SUITES["relation"].manifest, SUITES["relation"].oracle),
            (SUITES["migration-relation"].manifest, SUITES["migration-relation"].oracle),
        )
        for manifest_path, oracle_path in cases:
            with self.subTest(manifest=manifest_path.name):
                first = canonical_json(generate_suite(DEFAULT_PROFILE, manifest_path))
                second = canonical_json(generate_suite(DEFAULT_PROFILE, manifest_path))
                self.assertEqual(first, second)
                manifest = _load_json(manifest_path)
                suite = json.loads(first)
                self.assertEqual(
                    [contract["id"] for contract in suite["contracts"]],
                    [contract["id"] for contract in manifest["contracts"]],
                )
                self.assertEqual(first, oracle_path.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_migration_references_match_in_two_hashseed_processes(self) -> None:
        for manifest, oracle in (
            (SUITES["migration-lifecycle"].manifest, SUITES["migration-lifecycle"].oracle),
            (SUITES["migration-restart"].manifest, SUITES["migration-restart"].oracle),
            (SUITES["migration-state-reconstruction"].manifest, SUITES["migration-state-reconstruction"].oracle),
            (SUITES["migration-definition-source"].manifest, SUITES["migration-definition-source"].oracle),
            (SUITES["migration-relation"].manifest, SUITES["migration-relation"].oracle),
        ):
            with self.subTest(manifest=manifest.name):
                outputs: list[bytes] = []
                with tempfile.TemporaryDirectory() as temporary_directory:
                    for index, hash_seed in enumerate(("17", "982451653"), 1):
                        output = Path(temporary_directory) / f"reference-{index}.json"
                        environment = os.environ.copy()
                        environment.update(
                            {"LC_ALL": "C", "PYTHONHASHSEED": hash_seed, "TZ": "UTC"}
                        )
                        subprocess.run(
                            [
                                sys.executable,
                                "-m",
                                "conformance.runners.django",
                                "--profile", str(DEFAULT_PROFILE),
                                "--manifest", str(manifest),
                                "--output", str(output),
                            ],
                            cwd=Path(__file__).resolve().parents[4],
                            env=environment,
                            check=True,
                            capture_output=True,
                            text=True,
                        )
                        outputs.append(output.read_bytes())
                self.assertEqual(outputs[0], outputs[1])
                self.assertEqual(outputs[0], oracle.read_bytes())


if __name__ == "__main__":
    unittest.main()
