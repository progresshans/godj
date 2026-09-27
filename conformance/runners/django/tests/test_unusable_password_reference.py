import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.unusable_password_reference"


class UnusablePasswordReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_UNUSABLE_PASSWORD_DATABASE", None)
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
        self.assertEqual(actual, self.observe("817"))
        for backend in ("sqlite", "postgres"):
            expected = json.loads((ROOT / f"internal/identitytest/testdata/unusable-password-django61-{backend}.json").read_text())
            for field in ("observations", "source_sha256"):
                self.assertEqual(actual[field], expected[field], (backend, field))
        self.assertEqual(actual["observations"]["checks"], [False] * 4)
        self.assertEqual(actual["observations"]["work"], {key: {"authenticated": False, "password_work": 1} for key in ("unusable", "unknown")})

    def test_constant_marker_missing_work_or_password_only_resolution_changes_observations(self):
        actual = self.observe()["observations"]
        for mutation in (
            "from django.contrib.auth.forms import AdminUserCreationForm\noriginal = AdminUserCreationForm.__init__\ndef required(self, *args, **kwargs):\n    original(self, *args, **kwargs)\n    self.fields['password1'].required = True\n    self.fields['password2'].required = True\nAdminUserCreationForm.__init__ = required",
            "from django.contrib.auth.forms import SetUnusablePasswordMixin, SetPasswordMixin\nSetUnusablePasswordMixin.set_password_and_save = SetPasswordMixin.set_password_and_save",
            "from django.contrib.auth.models import User\nUser.set_unusable_password = lambda self: setattr(self, 'password', '!constant')",
            "from django.contrib.auth.base_user import AbstractBaseUser\nAbstractBaseUser.get_session_auth_hash = lambda self: 'constant'",
            "from django.contrib.auth import hashers\nreal_verify = hashers.verify_password\nhashers.verify_password = lambda password, encoded, preferred='default': (False, False) if encoded.startswith('!') else real_verify(password, encoded, preferred)",
            "from django.contrib.auth.backends import ModelBackend\noriginal = ModelBackend.get_user\nModelBackend.get_user = lambda self, pk: (lambda user: user if user is None or user.has_usable_password() else None)(original(self, pk))",
        ):
            with self.subTest(mutation=mutation):
                self.assertNotEqual(actual, self.observe(mutation=mutation)["observations"])


if __name__ == "__main__":
    unittest.main()
