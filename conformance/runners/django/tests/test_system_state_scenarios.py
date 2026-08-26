from __future__ import annotations

import hashlib
import json
import unittest
from copy import deepcopy

from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.system_state_decisions import (
    SCENARIOS as DECISION_SCENARIOS,
)
from conformance.runners.django.system_state_scenarios import (
    SCENARIOS as DJANGO_SCENARIOS,
)
from conformance.systemstate.reference import (
    ADR_0048_IDS,
    DECISION_IDS,
    DJANGO_IDS,
    EXPECTED_IDS,
    EXPECTED_SCENARIOS,
    LEGACY_IDS,
    MANIFEST,
    ORACLE,
    _load,
    _validate_contract_authority,
    generate_suite,
)


class SystemStateScenarioTests(unittest.TestCase):
    def test_exact_mixed_authority_registry_and_provenance(self) -> None:
        self.assertEqual(len(DECISION_SCENARIOS), 14)
        self.assertEqual(len(DJANGO_SCENARIOS), 6)
        self.assertEqual(len(DECISION_IDS), 14)
        self.assertEqual(len(DJANGO_IDS), 6)
        self.assertEqual(len(LEGACY_IDS), 12)
        self.assertEqual(len(ADR_0048_IDS), 8)
        manifest = _load(MANIFEST)
        contracts = manifest["contracts"]
        self.assertEqual(tuple(item["id"] for item in contracts), EXPECTED_IDS)
        self.assertEqual(
            tuple(item["scenario"] for item in contracts), EXPECTED_SCENARIOS
        )
        _validate_contract_authority(contracts)

        escaped = deepcopy(contracts)
        escaped[0]["provenance"].append(
            {
                "kind": "source",
                "reference": "django@fe0a859f537d4238cf49fca39073513206f83122:django/conf/__init__.py",
                "derived": False,
                "license": "BSD-3-Clause",
            }
        )
        with self.assertRaisesRegex(
            RuntimeError, "decision authority carries Django provenance"
        ):
            _validate_contract_authority(escaped)

        missing = deepcopy(contracts)
        missing[2]["provenance"] = [missing[2]["provenance"][0]]
        with self.assertRaisesRegex(RuntimeError, "exact Django authority"):
            _validate_contract_authority(missing)

        stale_adr = deepcopy(contracts)
        stale_adr[0]["provenance"][0]["kind"] = "proposal"
        with self.assertRaisesRegex(RuntimeError, "current ADR-0047 documentation"):
            _validate_contract_authority(stale_adr)

        stale_proposal = deepcopy(contracts)
        stale_proposal[12]["provenance"][0]["kind"] = "documentation"
        with self.assertRaisesRegex(RuntimeError, "exact Proposed ADR-0048"):
            _validate_contract_authority(stale_proposal)

        stale_deviation = deepcopy(contracts)
        stale_deviation[8]["provenance"][1]["kind"] = "proposal"
        with self.assertRaisesRegex(RuntimeError, "DEV-0008 decision"):
            _validate_contract_authority(stale_deviation)

    def test_decision_authority_is_byte_deterministic(self) -> None:
        decision_ids = (
            "SYS-001",
            "SYS-002",
            "SYS-005",
            "SYS-006",
            "SYS-007",
            "SYS-012",
            "SYS-013",
            "SYS-014",
            "SYS-015",
            "SYS-016",
            "SYS-017",
            "SYS-018",
            "SYS-019",
            "SYS-020",
        )
        first = [
            scenario(contract_id)
            for contract_id, scenario in zip(
                decision_ids, DECISION_SCENARIOS.values(), strict=True
            )
        ]
        second = [
            scenario(contract_id)
            for contract_id, scenario in zip(
                decision_ids, DECISION_SCENARIOS.values(), strict=True
            )
        ]
        self.assertEqual(canonical_json(first), canonical_json(second))

    def test_full_reference_is_deterministic_and_secret_free(self) -> None:
        first = canonical_json(generate_suite())
        second = canonical_json(generate_suite())
        self.assertEqual(first, second)
        self.assertEqual(first, ORACLE.read_bytes())
        suite = json.loads(first)
        self.assertEqual(
            [contract["id"] for contract in suite["contracts"]],
            list(EXPECTED_IDS),
        )
        legacy_manifest = _load(MANIFEST)
        legacy_manifest["contracts"] = legacy_manifest["contracts"][:12]
        self.assertEqual(
            hashlib.sha256(canonical_json(legacy_manifest)).hexdigest(),
            "40a91f1bb18bb5541f2d74270c8b64b416b9af0e63a0563988cdd7b1dd2b0bd7",
        )
        legacy_suite = deepcopy(suite)
        legacy_suite["contracts"] = legacy_suite["contracts"][:12]
        legacy_bytes = canonical_json(legacy_suite)
        self.assertEqual(len(legacy_bytes), 13099)
        self.assertEqual(
            hashlib.sha256(legacy_bytes).hexdigest(),
            "4b1cf9a63308c2f9ad9ac385c24e35ffec8f94546d80ed933dcf32edcb5a34bb",
        )
        logout = suite["contracts"][7]
        self.assertEqual(logout["id"], "SYS-008")
        self.assertEqual(
            next(
                field["value"]["value"]
                for field in logout["result"]["fields"]
                if field["name"] == "api_status"
            ),
            "403",
        )
        serialized = first.decode("utf-8").lower()
        for forbidden in (
            "system-state-reference-credential",
            "csrfmiddlewaretoken",
            "sessionid",
            "set-cookie",
            "<!doctype",
        ):
            self.assertNotIn(forbidden, serialized)


if __name__ == "__main__":
    unittest.main()
