from __future__ import annotations

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
    DECISION_IDS,
    DJANGO_IDS,
    EXPECTED_IDS,
    EXPECTED_SCENARIOS,
    MANIFEST,
    ORACLE,
    _load,
    _validate_contract_authority,
    generate_suite,
)


class SystemStateScenarioTests(unittest.TestCase):
    def test_exact_mixed_authority_registry_and_provenance(self) -> None:
        self.assertEqual(len(DECISION_SCENARIOS), 6)
        self.assertEqual(len(DJANGO_SCENARIOS), 6)
        self.assertEqual(len(DECISION_IDS), 6)
        self.assertEqual(len(DJANGO_IDS), 6)
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

    def test_decision_authority_is_byte_deterministic(self) -> None:
        decision_ids = ("SYS-001", "SYS-002", "SYS-005", "SYS-006", "SYS-007", "SYS-012")
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
