from __future__ import annotations

import importlib.util
import json
import os
import unittest
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

from django.db import connection
from django.db.migrations.recorder import MigrationRecorder

from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import (
    DEFAULT_ARTICLE_ADMIN_MANIFEST,
    DEFAULT_ARTICLE_API_MANIFEST,
    DEFAULT_API_AUTHENTICATION_MANIFEST,
    DEFAULT_AUTH_SESSION_MANIFEST,
    DEFAULT_DRF_PROFILE,
    DEFAULT_MANIFEST,
    DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST,
    DEFAULT_MIGRATION_DEFINITION_SOURCE_ORACLE,
    DEFAULT_MIGRATION_COMMAND_MANIFEST,
    DEFAULT_MIGRATION_PROJECT_CHECK_MANIFEST,
    DEFAULT_MIGRATION_PROJECT_CHECK_ORACLE,
    DEFAULT_MIGRATION_RELATION_MANIFEST,
    DEFAULT_MIGRATION_RELATION_ORACLE,
    DEFAULT_MIGRATION_LIFECYCLE_MANIFEST,
    DEFAULT_MIGRATION_LIFECYCLE_ORACLE,
    DEFAULT_MIGRATION_EXECUTION_MANIFEST,
    DEFAULT_MIGRATION_EXECUTION_ORACLE,
    DEFAULT_MIGRATION_PLANNING_MANIFEST,
    DEFAULT_MIGRATION_PLANNING_ORACLE,
    DEFAULT_MIGRATION_RESTART_MANIFEST,
    DEFAULT_MIGRATION_RESTART_ORACLE,
    DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST,
    DEFAULT_MIGRATION_STATE_RECONSTRUCTION_ORACLE,
    DEFAULT_PROFILE,
    DEFAULT_PARAMETER_ROUTING_MANIFEST,
    DEFAULT_QUERY_BREADTH_MANIFEST,
    DEFAULT_QUERY_BREADTH_ORACLE,
    DEFAULT_QUERY_EXPRESSION_MANIFEST,
    DEFAULT_QUERY_EXPRESSION_ORACLE,
    DEFAULT_QUERY_CACHE_MANIFEST,
    DEFAULT_QUERY_CACHE_ORACLE,
    DEFAULT_RELATION_MANIFEST,
    DEFAULT_RELATION_ORACLE,
    DEFAULT_SAVE_LIFECYCLE_MANIFEST,
    DEFAULT_SAVE_LIFECYCLE_ORACLE,
    DEFAULT_TEMPLATE_FORM_MANIFEST,
    DEFAULT_SYSTEM_STATE_MANIFEST,
    DEFAULT_WRITE_MIGRATION_MANIFEST,
    DEFAULT_WRITE_MIGRATION_ORACLE,
    ProfileMismatch,
    API_AUTHENTICATION_SCENARIOS,
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
            RELATION_SCENARIOS,
            MIGRATION_RELATION_SCENARIOS,
            {name: DRF_SCENARIOS[name] for name in PARAMETER_ROUTING_SCENARIOS},
            {name: DRF_SCENARIOS[name] for name in ARTICLE_API_SCENARIOS},
            API_AUTHENTICATION_SCENARIOS,
            SYSTEM_STATE_SCENARIOS,
        ):
            with self.subTest(scenarios=sorted(scenarios)):
                self.assertGreaterEqual(len(scenarios), 8)
                if (
                    scenarios is QUERY_EXPRESSION_SCENARIOS
                    or scenarios is SYSTEM_STATE_SCENARIOS
                ):
                    self.assertEqual(len(scenarios), 20)
                else:
                    self.assertLessEqual(len(scenarios), 12)

    def test_each_manifest_matches_its_scenario_registry_exactly(self) -> None:
        contract_sets = (
            (DEFAULT_MANIFEST, QUERY_SCENARIOS),
            (DEFAULT_WRITE_MIGRATION_MANIFEST, WRITE_MIGRATION_SCENARIOS),
            (DEFAULT_SAVE_LIFECYCLE_MANIFEST, SAVE_LIFECYCLE_SCENARIOS),
            (DEFAULT_QUERY_CACHE_MANIFEST, QUERY_CACHE_SCENARIOS),
            (DEFAULT_QUERY_BREADTH_MANIFEST, QUERY_BREADTH_SCENARIOS),
            (DEFAULT_QUERY_EXPRESSION_MANIFEST, QUERY_EXPRESSION_SCENARIOS),
            (DEFAULT_MIGRATION_PLANNING_MANIFEST, MIGRATION_PLANNING_SCENARIOS),
            (DEFAULT_MIGRATION_EXECUTION_MANIFEST, MIGRATION_EXECUTION_SCENARIOS),
            (DEFAULT_MIGRATION_RESTART_MANIFEST, MIGRATION_RESTART_SCENARIOS),
            (
                DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST,
                MIGRATION_STATE_RECONSTRUCTION_SCENARIOS,
            ),
            (
                DEFAULT_MIGRATION_LIFECYCLE_MANIFEST,
                MIGRATION_LIFECYCLE_SCENARIOS,
            ),
            (
                DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST,
                MIGRATION_DEFINITION_SOURCE_SCENARIOS,
            ),
            (
                DEFAULT_MIGRATION_PROJECT_CHECK_MANIFEST,
                MIGRATION_PROJECT_CHECK_SCENARIOS,
            ),
            (
                DEFAULT_MIGRATION_COMMAND_MANIFEST,
                MIGRATION_COMMAND_DECISION_SCENARIOS,
            ),
            (DEFAULT_RELATION_MANIFEST, RELATION_SCENARIOS),
            (
                DEFAULT_MIGRATION_RELATION_MANIFEST,
                MIGRATION_RELATION_SCENARIOS,
            ),
            (DEFAULT_TEMPLATE_FORM_MANIFEST, TEMPLATE_FORM_SCENARIOS),
            (
                DEFAULT_AUTH_SESSION_MANIFEST,
                {name: AUTH_ADMIN_SCENARIOS[name] for name in AUTH_SCENARIOS},
            ),
            (
                DEFAULT_ARTICLE_ADMIN_MANIFEST,
                {name: AUTH_ADMIN_SCENARIOS[name] for name in ADMIN_SCENARIOS},
            ),
            (
                DEFAULT_PARAMETER_ROUTING_MANIFEST,
                {name: DRF_SCENARIOS[name] for name in PARAMETER_ROUTING_SCENARIOS},
            ),
            (
                DEFAULT_ARTICLE_API_MANIFEST,
                {name: DRF_SCENARIOS[name] for name in ARTICLE_API_SCENARIOS},
            ),
            (DEFAULT_API_AUTHENTICATION_MANIFEST, API_AUTHENTICATION_SCENARIOS),
            (DEFAULT_SYSTEM_STATE_MANIFEST, SYSTEM_STATE_SCENARIOS),
        )
        self.assertEqual(len(contract_sets), 23)
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
        self.assertEqual(len(selected_across_sets), 261)
        self.assertEqual(len(selected_across_sets), len(set(selected_across_sets)))
        self.assertEqual(set(selected_across_sets), set(ALL_SCENARIOS))
        self.assertEqual(len(contract_ids_across_sets), 261)
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
        self.assertEqual(cross_bindings, 506)

    def test_one_manifest_does_not_require_other_set_scenarios(self) -> None:
        for profile_path, manifest_path in (
            (DEFAULT_PROFILE, DEFAULT_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_WRITE_MIGRATION_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_SAVE_LIFECYCLE_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_QUERY_CACHE_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_QUERY_BREADTH_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_QUERY_EXPRESSION_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_MIGRATION_PLANNING_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_MIGRATION_EXECUTION_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_MIGRATION_RESTART_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_MIGRATION_LIFECYCLE_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_MIGRATION_PROJECT_CHECK_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_MIGRATION_COMMAND_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_RELATION_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_MIGRATION_RELATION_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_TEMPLATE_FORM_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_AUTH_SESSION_MANIFEST),
            (DEFAULT_PROFILE, DEFAULT_ARTICLE_ADMIN_MANIFEST),
            (DEFAULT_DRF_PROFILE, DEFAULT_PARAMETER_ROUTING_MANIFEST),
            (DEFAULT_DRF_PROFILE, DEFAULT_ARTICLE_API_MANIFEST),
            (DEFAULT_DRF_PROFILE, DEFAULT_API_AUTHENTICATION_MANIFEST),
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
        manifest = _load_json(DEFAULT_QUERY_EXPRESSION_MANIFEST)
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
    def test_write_migration_suite_is_byte_deterministic_and_ordered(self) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_WRITE_MIGRATION_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_WRITE_MIGRATION_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_WRITE_MIGRATION_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_WRITE_MIGRATION_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_save_lifecycle_suite_is_byte_deterministic_and_ordered(self) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_SAVE_LIFECYCLE_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_SAVE_LIFECYCLE_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_SAVE_LIFECYCLE_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_SAVE_LIFECYCLE_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_query_cache_suite_is_byte_deterministic_and_ordered(self) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_QUERY_CACHE_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_QUERY_CACHE_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_QUERY_CACHE_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_QUERY_CACHE_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_query_breadth_suite_is_byte_deterministic_and_ordered(self) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_QUERY_BREADTH_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_QUERY_BREADTH_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_QUERY_BREADTH_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_QUERY_BREADTH_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_query_expression_suite_is_byte_deterministic_and_ordered(self) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_QUERY_EXPRESSION_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_QUERY_EXPRESSION_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_QUERY_EXPRESSION_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_QUERY_EXPRESSION_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_migration_planning_suite_is_byte_deterministic_and_ordered(self) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_PLANNING_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_PLANNING_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_MIGRATION_PLANNING_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_MIGRATION_PLANNING_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_migration_execution_suite_is_byte_deterministic_and_ordered(self) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_EXECUTION_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_EXECUTION_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_MIGRATION_EXECUTION_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_MIGRATION_EXECUTION_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_migration_restart_suite_is_byte_deterministic_and_ordered(self) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_RESTART_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_RESTART_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_MIGRATION_RESTART_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_MIGRATION_RESTART_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_migration_state_reconstruction_suite_is_byte_deterministic_and_ordered(
        self,
    ) -> None:
        first = canonical_json(
            generate_suite(
                DEFAULT_PROFILE,
                DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST,
            )
        )
        second = canonical_json(
            generate_suite(
                DEFAULT_PROFILE,
                DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST,
            )
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(
            first,
            DEFAULT_MIGRATION_STATE_RECONSTRUCTION_ORACLE.read_bytes(),
        )

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_migration_lifecycle_suite_is_byte_deterministic_and_ordered(
        self,
    ) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_LIFECYCLE_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_LIFECYCLE_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_MIGRATION_LIFECYCLE_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_MIGRATION_LIFECYCLE_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_migration_definition_source_suite_is_byte_deterministic_and_ordered(
        self,
    ) -> None:
        first = canonical_json(
            generate_suite(
                DEFAULT_PROFILE,
                DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST,
            )
        )
        second = canonical_json(
            generate_suite(
                DEFAULT_PROFILE,
                DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST,
            )
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(
            first,
            DEFAULT_MIGRATION_DEFINITION_SOURCE_ORACLE.read_bytes(),
        )

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_migration_project_check_suite_is_byte_deterministic_and_ordered(
        self,
    ) -> None:
        first = canonical_json(
            generate_suite(
                DEFAULT_PROFILE,
                DEFAULT_MIGRATION_PROJECT_CHECK_MANIFEST,
            )
        )
        second = canonical_json(
            generate_suite(
                DEFAULT_PROFILE,
                DEFAULT_MIGRATION_PROJECT_CHECK_MANIFEST,
            )
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_MIGRATION_PROJECT_CHECK_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(
            first,
            DEFAULT_MIGRATION_PROJECT_CHECK_ORACLE.read_bytes(),
        )

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_relation_suite_is_byte_deterministic_and_ordered(self) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_RELATION_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_RELATION_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_RELATION_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_RELATION_ORACLE.read_bytes())

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_migration_relation_suite_is_byte_deterministic_and_ordered(
        self,
    ) -> None:
        first = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_RELATION_MANIFEST)
        )
        second = canonical_json(
            generate_suite(DEFAULT_PROFILE, DEFAULT_MIGRATION_RELATION_MANIFEST)
        )
        self.assertEqual(first, second)
        manifest = _load_json(DEFAULT_MIGRATION_RELATION_MANIFEST)
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )
        self.assertEqual(first, DEFAULT_MIGRATION_RELATION_ORACLE.read_bytes())


if __name__ == "__main__":
    unittest.main()
