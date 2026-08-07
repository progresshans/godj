from __future__ import annotations

import json
import os
import unittest
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import (
    DEFAULT_MANIFEST,
    DEFAULT_PROFILE,
    DEFAULT_WRITE_MIGRATION_MANIFEST,
    DEFAULT_WRITE_MIGRATION_ORACLE,
    ProfileMismatch,
    SCENARIOS as ALL_SCENARIOS,
    _load_json,
    _run_contract,
    _validate_manifest_basics,
    generate_suite,
    verify_profile,
)
from conformance.runners.django.scenarios import SCENARIOS as QUERY_SCENARIOS
from conformance.runners.django.write_migration_scenarios import (
    SCENARIOS as WRITE_MIGRATION_SCENARIOS,
)


class ScenarioTests(unittest.TestCase):
    def test_all_scenarios_are_byte_deterministic(self) -> None:
        for name, scenario in ALL_SCENARIOS.items():
            with self.subTest(scenario=name):
                contract_id = f"TEST-{name}"
                first = canonical_json(scenario(contract_id))
                second = canonical_json(scenario(contract_id))
                self.assertEqual(first, second)

    def test_each_contract_set_count_is_within_protocol_bound(self) -> None:
        for scenarios in (QUERY_SCENARIOS, WRITE_MIGRATION_SCENARIOS):
            with self.subTest(scenarios=sorted(scenarios)):
                self.assertGreaterEqual(len(scenarios), 8)
                self.assertLessEqual(len(scenarios), 12)

    def test_each_manifest_matches_its_scenario_registry_exactly(self) -> None:
        contract_sets = (
            (DEFAULT_MANIFEST, QUERY_SCENARIOS),
            (DEFAULT_WRITE_MIGRATION_MANIFEST, WRITE_MIGRATION_SCENARIOS),
        )
        selected_across_sets = []
        for manifest_path, registry in contract_sets:
            with self.subTest(manifest=manifest_path.name):
                manifest = _load_json(manifest_path)
                selected = [
                    contract["scenario"] for contract in manifest["contracts"]
                ]
                self.assertEqual(len(selected), len(set(selected)))
                self.assertEqual(set(selected), set(registry))
                selected_across_sets.extend(selected)
        self.assertEqual(len(selected_across_sets), len(set(selected_across_sets)))
        self.assertEqual(set(selected_across_sets), set(ALL_SCENARIOS))

    def test_one_manifest_does_not_require_other_set_scenarios(self) -> None:
        profile = _load_json(DEFAULT_PROFILE)
        for manifest_path in (DEFAULT_MANIFEST, DEFAULT_WRITE_MIGRATION_MANIFEST):
            manifest = _load_json(manifest_path)
            self.assertEqual(
                len(_validate_manifest_basics(manifest, profile)),
                len(manifest["contracts"]),
            )

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


if __name__ == "__main__":
    unittest.main()
