from __future__ import annotations

import hashlib
import importlib.util
import json
import unittest
from typing import Any
from unittest.mock import patch

from conformance.articleapi.reference import generate_suite
from conformance.runners.django.api_authentication_decisions import (
    SCENARIOS as DECISION_SCENARIOS,
)
from conformance.runners.django.article_api_proxy import (
    API_AUTHENTICATION_DRF_SCENARIOS,
    SCENARIOS as DRF_SCENARIOS,
)
from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import (
    API_AUTHENTICATION_SCENARIOS,
    DEFAULT_API_AUTHENTICATION_MANIFEST,
    DEFAULT_DRF_PROFILE,
)


_RAW_BEARER_CANARY = "gdj-phase-a-raw-bearer-canary"
_DRF_AVAILABLE = importlib.util.find_spec("rest_framework") is not None


EXPECTED_SCENARIOS = (
    "godj.api_authentication.common_authentication_boundary",
    "godj.api_authentication.bounded_bearer_header",
    "drf.api_authentication.missing_and_unsupported",
    "drf.api_authentication.invalid_and_valid_token",
    "drf.api_authentication.permission_denial",
    "drf.api_authentication.unsafe_without_csrf",
    "drf.api_authentication.profile_isolation",
    "godj.api_authentication.secret_and_failure_boundary",
    "godj.api_authentication.article_route_reuse",
    "godj.api_authentication.denial_mutation_boundary",
)


def _semantic(value: Any) -> Any:
    if not isinstance(value, dict) or "type" not in value:
        return value
    kind = value["type"]
    if kind == "object":
        return {
            field["name"]: _semantic(field["value"])
            for field in value["fields"]
        }
    if kind == "list":
        return [_semantic(item) for item in value["items"]]
    if kind == "null":
        return None
    if kind == "bool":
        return value["value"]
    if kind in {"int", "pk"}:
        nested = value["value"]
        if isinstance(nested, dict):
            return _semantic(nested)
        return int(nested)
    return value.get("value")


def _observation(name: str, contract_id: str) -> dict[str, Any]:
    observation = API_AUTHENTICATION_SCENARIOS[name](contract_id)
    return {
        key: _semantic(value)
        if key in {"db_state", "metrics", "result"}
        else value
        for key, value in observation.items()
    }


class APIAuthenticationScenarioTests(unittest.TestCase):
    def test_exact_mixed_registry_and_manifest_order(self) -> None:
        manifest = json.loads(
            DEFAULT_API_AUTHENTICATION_MANIFEST.read_text(encoding="utf-8")
        )
        self.assertEqual(
            tuple(contract["scenario"] for contract in manifest["contracts"]),
            EXPECTED_SCENARIOS,
        )
        self.assertEqual(set(API_AUTHENTICATION_SCENARIOS), set(EXPECTED_SCENARIOS))
        self.assertEqual(len(DECISION_SCENARIOS), 5)
        self.assertEqual(len(API_AUTHENTICATION_DRF_SCENARIOS), 5)
        self.assertTrue(set(DECISION_SCENARIOS).isdisjoint(DRF_SCENARIOS))

    def test_decision_observations_are_deterministic_and_bounded(self) -> None:
        first = canonical_json(
            [
                scenario(f"TEST-{index}")
                for index, scenario in enumerate(DECISION_SCENARIOS.values())
            ]
        )
        second = canonical_json(
            [
                scenario(f"TEST-{index}")
                for index, scenario in enumerate(DECISION_SCENARIOS.values())
            ]
        )
        self.assertEqual(first, second)
        self.assertLess(len(first), 32 * 1024)

        bounded = _observation(
            "godj.api_authentication.bounded_bearer_header", "AUT-010"
        )
        cases = {case["case"]: case for case in bounded["result"]["cases"]}
        self.assertEqual(cases["token_bytes_4096"]["outcome"], "accepted")
        self.assertEqual(cases["token_bytes_4097"]["outcome"], "invalid_request")
        self.assertEqual(cases["duplicate_fields"]["verifier_calls"], 0)
        self.assertEqual(cases["rfc_alphabet"]["verifier_calls"], 1)
        self.assertEqual(cases["tab_separator"]["outcome"], "invalid_request")

    @unittest.skipUnless(_DRF_AVAILABLE, "requires Django REST framework")
    def test_drf_missing_and_invalid_token_semantics(self) -> None:
        missing = _observation(
            "drf.api_authentication.missing_and_unsupported", "AUT-011"
        )
        self.assertEqual(
            [(case["response"]["status"], case["response"]["www_authenticate"])
             for case in missing["result"]],
            [(401, "Bearer"), (401, "Bearer"), (401, "Bearer")],
        )
        self.assertEqual(missing["metrics"]["credential_verifications"], 0)

        tokens = _observation(
            "drf.api_authentication.invalid_and_valid_token", "AUT-012"
        )
        cases = {case["case"]: case["response"] for case in tokens["result"]}
        self.assertEqual(cases["unknown"]["status"], 401)
        self.assertEqual(cases["inactive"]["status"], 401)
        self.assertEqual(cases["valid"]["status"], 200)
        self.assertTrue(cases["valid"]["authenticated"])
        self.assertFalse(cases["valid"]["csrf_header"])
        self.assertEqual(cases["valid"]["response_cookies"], 0)
        self.assertIsNone(tokens["db_state"])
        self.assertEqual(tokens["metrics"]["credential_verifications"], 3)

    @unittest.skipUnless(_DRF_AVAILABLE, "requires Django REST framework")
    def test_drf_permission_csrf_and_profile_isolation(self) -> None:
        denied = _observation(
            "drf.api_authentication.permission_denial", "AUT-013"
        )
        self.assertEqual(denied["result"]["status"], 403)
        self.assertIsNone(denied["result"]["www_authenticate"])
        self.assertEqual(denied["metrics"]["article_mutations"], 0)

        unsafe = _observation(
            "drf.api_authentication.unsafe_without_csrf", "AUT-014"
        )
        self.assertEqual(unsafe["result"]["status"], 201)
        self.assertFalse(unsafe["result"]["csrf_header"])
        self.assertEqual(unsafe["result"]["response_cookies"], 0)
        self.assertEqual(unsafe["metrics"]["article_row_delta"], 1)
        self.assertEqual(unsafe["metrics"]["csrf_credentials_supplied"], 0)

        isolated = _observation(
            "drf.api_authentication.profile_isolation", "AUT-015"
        )
        self.assertEqual(
            [case["response"]["status"] for case in isolated["result"]],
            [401, 401, 401, 401],
        )
        self.assertEqual(isolated["metrics"]["session_cookie_present"], 1)
        self.assertEqual(isolated["metrics"]["fallback_authentications"], 0)
        self.assertEqual(isolated["metrics"]["article_mutations"], 0)

    @unittest.skipUnless(_DRF_AVAILABLE, "requires Django REST framework")
    def test_full_reference_is_deterministic_and_raw_bearer_free(self) -> None:
        self.assertEqual(_RAW_BEARER_CANARY, "gdj-phase-a-raw-bearer-canary")
        with patch("conformance.runners.django.runner.verify_profile"):
            first = canonical_json(
                generate_suite(
                    DEFAULT_DRF_PROFILE, DEFAULT_API_AUTHENTICATION_MANIFEST
                )
            )
            second = canonical_json(
                generate_suite(
                    DEFAULT_DRF_PROFILE, DEFAULT_API_AUTHENTICATION_MANIFEST
                )
            )
        self.assertEqual(first, second)
        self.assertNotIn(_RAW_BEARER_CANARY.encode("utf-8"), first)
        self.assertEqual(
            [item["id"] for item in json.loads(first)["contracts"]],
            [
                "AUT-009",
                "AUT-010",
                "AUT-011",
                "AUT-012",
                "AUT-013",
                "AUT-014",
                "AUT-015",
                "AUT-016",
                "API-011",
                "API-012",
            ],
        )
        self.assertEqual(len(hashlib.sha256(first).hexdigest()), 64)


if __name__ == "__main__":
    unittest.main()
