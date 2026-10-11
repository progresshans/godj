from __future__ import annotations

import json
import unittest

from conformance.authadmin.reference import generate_suite
from conformance.runners.django.auth_admin_proxy import (
    ADMIN_SCENARIOS,
    AUTH_SCENARIOS,
    SCENARIOS,
)


class AuthAdminScenarioTests(unittest.TestCase):
    def test_registries_and_observations_are_exact_and_secret_free(self) -> None:
        self.assertEqual(len(AUTH_SCENARIOS), 8)
        self.assertEqual(len(ADMIN_SCENARIOS), 10)
        self.assertEqual(len(SCENARIOS), 18)
        expected = [
            *[(f"AUT-{index:03d}", name) for index, name in enumerate(AUTH_SCENARIOS, 1)],
            *[(f"ADM-{index:03d}", name) for index, name in enumerate(ADMIN_SCENARIOS, 1)],
        ]
        forbidden_keys = {
            "cookie_value",
            "csrf_token",
            "html",
            "password",
            "password_hash",
            "session_id",
            "session_key",
            "token",
        }

        def assert_safe(value: object) -> None:
            if isinstance(value, dict):
                for key, item in value.items():
                    self.assertNotIn(key, forbidden_keys)
                    assert_safe(item)
                return
            if isinstance(value, list):
                for item in value:
                    assert_safe(item)
                return
            if isinstance(value, str):
                self.assertNotIn("reference-password", value)
                self.assertNotIn("<html", value.lower())
                self.assertNotIn("<form", value.lower())

        for contract_id, name in expected:
            observation = SCENARIOS[name](contract_id)
            self.assertEqual(observation["id"], contract_id)
            self.assertEqual(observation["status"], "observed")
            assert_safe(observation)

    def test_full_reference_suites_are_byte_deterministic(self) -> None:
        for set_name in ("auth-session", "article-admin"):
            first = json.dumps(
                generate_suite(set_name), sort_keys=True, separators=(",", ":")
            )
            second = json.dumps(
                generate_suite(set_name), sort_keys=True, separators=(",", ":")
            )
            self.assertEqual(first, second)


if __name__ == "__main__":
    unittest.main()
