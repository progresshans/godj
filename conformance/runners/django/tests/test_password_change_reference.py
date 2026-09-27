import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.password_change_reference"


class PasswordChangeReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_SELF_PASSWORD_DATABASE", None)
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup = django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup = setup\n"
        result = subprocess.run([sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__', alter_sys=True)"],
                                cwd=ROOT, env=environment, capture_output=True, check=True, timeout=180)
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_pinned_backends_and_hash_seeds(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("817"))
        self.assertEqual(actual["django"], "6.1")
        self.assertEqual(len(actual["source_sha256"]), 9)
        for backend in ("sqlite", "postgres"):
            expected = json.loads((ROOT / f"internal/identitytest/testdata/password-change-django61-{backend}.json").read_text())
            for field in ("observations", "source_sha256"):
                self.assertEqual(actual[field], expected[field], (backend, field))

    def test_rotation_stamp_and_last_login_controls(self):
        expected = self.observe()["observations"]
        controls = [
            ("from django.contrib.sessions.backends.db import SessionStore\nSessionStore.cycle_key = lambda self: None", ["success", "session_rotated"]),
            ("from django.contrib.auth.base_user import AbstractBaseUser\nAbstractBaseUser.get_session_auth_hash = lambda self: 'constant'", ["success", "other", "authenticated"]),
            ("from django.contrib import auth\nfrom django.utils import timezone\noriginal = auth.update_session_auth_hash\ndef update(request,user):\n    original(request,user)\n    user.last_login = timezone.now()\n    user.save(update_fields=['last_login'])\nauth.update_session_auth_hash = update", ["success", "last_login"]),
        ]
        for mutation, keys in controls:
            with self.subTest(observation=keys):
                before, after = expected, self.observe(mutation=mutation)["observations"]
                for key in keys:
                    before, after = before[key], after[key]
                self.assertNotEqual(before, after)
