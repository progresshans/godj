import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.identity_management_reference"


class IdentityManagementReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_IDENTITY_MANAGEMENT_REFERENCE_DATABASE", None)
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup = django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup = setup\n"
        result = subprocess.run([sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__', alter_sys=True)"],
                                cwd=ROOT, env=environment, capture_output=True, check=True, timeout=120)
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_reference_matches_both_backends_and_hash_seeds(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("813"))
        for backend in ("sqlite", "postgres"):
            expected = json.loads((ROOT / f"internal/identitytest/testdata/management-django61-{backend}.json").read_text())
            for field in ("observations", "source_sha256"):
                self.assertEqual(actual[field], expected[field], (backend, field))
        self.assertEqual(actual["observations"]["created"]["username"], "Fred")
        self.assertEqual(actual["observations"]["created"]["email"], "Mixed@example.com")
        self.assertEqual(actual["observations"]["email_normalization"], ["Upper@i̇.example", "x@ος", "x@οσ.example", "Upper@example.com"])
        self.assertFalse(actual["observations"]["admission"]["add"]["create"])
        self.assertTrue(actual["observations"]["admission"]["add_change"]["create"])
        self.assertEqual(actual["observations"]["admission"]["change"], {"create": False, "view": True, "add": False, "change": True, "delete": False})

    def test_removed_normalization_or_view_change_fallback_changes_observations(self):
        actual = self.observe()["observations"]
        for mutation in (
            "from django.contrib.auth.base_user import AbstractBaseUser\nAbstractBaseUser.normalize_username = classmethod(lambda cls, username: username)",
            "from django.contrib.auth.base_user import BaseUserManager\nBaseUserManager.normalize_email = classmethod(lambda cls, email: email)",
            "from django.contrib.admin.options import ModelAdmin\nModelAdmin.has_view_permission = lambda self, request, obj=None: request.user.has_perm('auth.view_user')",
        ):
            with self.subTest(mutation=mutation):
                self.assertNotEqual(actual, self.observe(mutation=mutation)["observations"])


if __name__ == "__main__":
    unittest.main()
