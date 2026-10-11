import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.credential_session_reference"


class CredentialSessionReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_CREDENTIAL_SESSION_DATABASE", None)
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup = django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup = setup\n"
        process = subprocess.run(
            [sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__')"],
            cwd=ROOT, env=environment, capture_output=True, check=True, timeout=120,
        )
        self.assertEqual(process.stderr, b"")
        return json.loads(process.stdout)

    def test_password_permission_and_username_lifecycles_are_independent(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("813"))
        expected = json.loads((ROOT / "web/sessionauth/testdata/credential-session-django61-sqlite.json").read_text())
        for field in ("observations", "source_sha256"):
            self.assertEqual(actual[field], expected[field], field)
        rows = actual["observations"]
        self.assertEqual(len(rows), 7)
        for mode in ("password", "rehash", "inactive", "legacy_id_only", "forged_stamp"):
            self.assertFalse(rows[mode]["after"]["authenticated"], mode)
        self.assertEqual(rows["permissions"]["after"], {"authenticated": True, "has_view": False})
        self.assertEqual(rows["username"]["after"], {"authenticated": True, "has_view": True})
        # Django does not flush merely because its backend returns no active
        # user. Keep this visible; GoDj's existing invalid-identity flush is
        # a separate server-side cleanup policy.
        self.assertTrue(rows["inactive"]["old_session_retained"])

    def test_disabled_session_binding_and_permission_loading_change_results(self):
        original = self.observe()["observations"]
        mutations = (
            "from django.contrib.auth.base_user import AbstractBaseUser\nAbstractBaseUser._get_session_auth_hash = lambda self, secret=None: 'constant-reference-mutation'\n",
            "from django.contrib.auth.backends import ModelBackend\nModelBackend._get_user_permissions = lambda self, user: user.user_permissions.none()\n",
        )
        for mutation in mutations:
            changed = self.observe(mutation=mutation)["observations"]
            self.assertNotEqual(original, changed)


if __name__ == "__main__":
    unittest.main()
