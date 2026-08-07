from __future__ import annotations

import os
import unittest
from copy import deepcopy
from pathlib import Path

from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import (
    DEFAULT_MANIFEST,
    DEFAULT_PROFILE,
    _load_json,
    _validate_manifest_basics,
    generate_suite,
)
from conformance.runners.django.scenarios import SCENARIOS


class ScenarioTests(unittest.TestCase):
    def test_all_scenarios_are_byte_deterministic(self) -> None:
        for name, scenario in SCENARIOS.items():
            with self.subTest(scenario=name):
                contract_id = f"TEST-{name}"
                first = canonical_json(scenario(contract_id))
                second = canonical_json(scenario(contract_id))
                self.assertEqual(first, second)

    def test_initial_scenario_count_is_within_m0_bound(self) -> None:
        self.assertGreaterEqual(len(SCENARIOS), 8)
        self.assertLessEqual(len(SCENARIOS), 12)

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
        import json

        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            [contract["id"] for contract in manifest["contracts"]],
        )


if __name__ == "__main__":
    unittest.main()
