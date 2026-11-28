import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.identity_reference"


class IdentityReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_IDENTITY_REFERENCE_DATABASE", None)
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup = django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup = setup\n"
        result = subprocess.run(
            [sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__')"],
            cwd=ROOT, env=environment, capture_output=True, check=True, timeout=120,
        )
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_captures_match_pinned_code_and_ignore_hash_seed(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("813"))
        for backend in ("sqlite", "postgres"):
            expected = json.loads((ROOT / f"internal/identitytest/testdata/identity-django61-{backend}.json").read_text())
            for field in ("observations", "source_sha256"):
                self.assertEqual(actual[field], expected[field], (backend, field))
        observations = actual["observations"]
        self.assertEqual(observations["grant_union"], {
            "direct": ["view"], "group": ["change", "view"],
            "effective": ["change", "view"], "group_memberships": 1,
        })
        self.assertEqual(observations["instance_permission_cache"], {"held": ["change", "view"], "fresh": ["export", "view"]})
        self.assertEqual(observations["group_deletion"], {"effective": ["view"], "memberships": 0})

    def test_all_active_staff_superuser_combinations_are_observed(self):
        roles = self.observe()["observations"]["roles"]
        self.assertEqual(set(roles), {f"{a}{s}{u}" for a in (0, 1) for s in (0, 1) for u in (0, 1)})
        for key, row in roles.items():
            active, staff, superuser = (digit == "1" for digit in key)
            self.assertEqual(row["backend_may_authenticate"], active)
            self.assertEqual(row["admin_entry"], active and staff)
            self.assertEqual(row["registered_grant"], active)
            for field in ("ungranted_permission", "unregistered_permission", "malformed_permission"):
                self.assertEqual(row[field], active and superuser, (key, field))
            if not active:
                self.assertEqual(row["effective"], [])

    def test_removing_group_loading_active_or_staff_controls_changes_observations(self):
        original = self.observe()["observations"]
        mutations = (
            "from django.contrib.auth.backends import ModelBackend\nModelBackend._get_group_permissions = lambda self, user: user.user_permissions.none()",
            "from django.contrib.auth.backends import ModelBackend\nModelBackend.user_can_authenticate = lambda self, user: True",
            "from django.contrib.admin.sites import AdminSite\nAdminSite.has_permission = lambda self, request: request.user.is_active",
        )
        for mutation in mutations:
            with self.subTest(mutation=mutation):
                self.assertNotEqual(original, self.observe(mutation=mutation)["observations"])


if __name__ == "__main__":
    unittest.main()
