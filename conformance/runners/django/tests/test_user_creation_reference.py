import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.user_creation_reference"


class UserCreationReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed, "PYTHONDONTWRITEBYTECODE": "1"}
        environment.pop("GODJ_USER_CREATION_DATABASE", None)
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup = django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup = setup\n"
        result = subprocess.run([sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__', alter_sys=True)"],
                                cwd=ROOT, env=environment, capture_output=True, timeout=120)
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_native_reference_and_hash_seed(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("817"))
        for backend in ("sqlite", "postgres"):
            expected = json.loads((ROOT / f"internal/identitytest/testdata/user-creation-django61-{backend}.json").read_text())
            for field in ("observations", "lifecycle", "source_sha256", "input_sha256", "django"):
                self.assertEqual(actual[field], expected[field], (backend, field))
        self.assertTrue(all(case["user_delta"] == 0 for mode in actual["observations"].values() for case in mode.values()))

    def test_missing_phases_change_observations(self):
        actual = self.observe()["observations"]
        for mutation in (
            "from django.contrib.auth.forms import SetPasswordMixin\nSetPasswordMixin.validate_password_for_user = lambda *a, **k: None",
            "from django.contrib.auth.forms import UserCreationForm\nUserCreationForm.clean_username = lambda self: self.cleaned_data.get('username')",
            "from django.contrib.auth.forms import SetPasswordMixin\nSetPasswordMixin.validate_passwords = lambda *a, **k: None",
            "from django.contrib.auth.forms import SetUnusablePasswordMixin, SetPasswordMixin\nSetUnusablePasswordMixin.validate_password_for_user = SetPasswordMixin.validate_password_for_user",
        ):
            with self.subTest(mutation=mutation):
                self.assertNotEqual(actual, self.observe(mutation=mutation)["observations"])

    def test_deferred_save_and_hash_work_are_observed(self):
        actual = self.observe()["lifecycle"]
        for mode in actual.values():
            self.assertEqual(mode["deferred"]["checked"], {"user_delta": 0, "hashes": 0})
            self.assertEqual(mode["deferred"]["prepared"]["user_delta"], 0)
            self.assertEqual(mode["deferred"]["user_delta"], 1)
            self.assertEqual(mode["abandoned"]["user_delta"], 0)
            self.assertTrue(mode["invalid"]["rejected"])
        for mutation in (
            "from django.contrib.auth.forms import SetPasswordMixin\noriginal = SetPasswordMixin.set_password_and_save\ndef force_save(self, user, **kwargs):\n    kwargs['commit'] = True\n    return original(self, user, **kwargs)\nSetPasswordMixin.set_password_and_save = force_save",
            "from django.contrib.auth.forms import SetPasswordMixin\noriginal = SetPasswordMixin.set_password_and_save\ndef double_hash(self, user, **kwargs):\n    user.set_password(self.cleaned_data['password1'])\n    return original(self, user, **kwargs)\nSetPasswordMixin.set_password_and_save = double_hash",
        ):
            with self.subTest(mutation=mutation):
                self.assertNotEqual(actual, self.observe(mutation=mutation)["lifecycle"])


if __name__ == "__main__":
    unittest.main()
