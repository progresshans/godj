import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.password_reset_reference"


class PasswordResetReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_PASSWORD_RESET_DATABASE", None)
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup=django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup=setup\n"
        result = subprocess.run([sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r},run_name='__main__',alter_sys=True)"],
                                cwd=ROOT, env=environment, capture_output=True, check=True, timeout=180)
        self.assertEqual(result.stderr, b"")
        for secret in (b"original reset password", b"replacement reset password", b"independent-reset-reference-key", b"https://reset.example.test/reset/"):
            self.assertNotIn(secret, result.stdout)
        return json.loads(result.stdout)

    def test_pinned_backends_and_hash_seeds(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("817"))
        self.assertEqual(actual["django"], "6.1")
        self.assertEqual(len(actual["source_sha256"]), 8)
        for backend in ("sqlite", "postgres"):
            expected = json.loads((ROOT / f"internal/identitytest/testdata/password-reset-django61-{backend}.json").read_text())
            for field in ("observations", "source_sha256"):
                self.assertEqual(actual[field], expected[field], (backend, field))

    def test_binding_selection_and_delivery_mutations(self):
        expected = self.observe()["observations"]
        controls = [
            ("from django.contrib.auth.tokens import PasswordResetTokenGenerator\nPasswordResetTokenGenerator._make_hash_value=lambda self,user,timestamp: f'{user.pk}{timestamp}{user.email}'", ["bindings", "password", "valid"]),
            ("from django.contrib.auth.forms import PasswordResetForm\nfrom django.contrib.auth.models import User\noriginal=PasswordResetForm.get_users\ndef get_users(self,email):\n    if email=='only-inactive@example.test':\n        return User.objects.filter(email=email)\n    return original(self,email)\nPasswordResetForm.get_users=get_users", ["request", "inactive_only", "messages"]),
            ("from django.core.mail.backends.locmem import EmailBackend\nEmailBackend.send_messages=lambda self,messages: 0", ["request", "casefold", "messages"]),
        ]
        for mutation, keys in controls:
            with self.subTest(observation=keys):
                before, after = expected, self.observe(mutation=mutation)["observations"]
                for key in keys:
                    before, after = before[key], after[key]
                self.assertNotEqual(before, after)
