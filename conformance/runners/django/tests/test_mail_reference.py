import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.mail_reference"


class MailReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup=django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup=setup\n"
        result = subprocess.run(
            [sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r},run_name='__main__',alter_sys=True)"],
            cwd=ROOT, env=os.environ | {"PYTHONHASHSEED": seed}, capture_output=True, check=True, timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        self.assertNotIn(b"independent-mail-reference", result.stdout)
        return json.loads(result.stdout)

    def test_pinned_observations_and_seed(self):
        observed = self.observe()
        self.assertEqual(observed, self.observe("817"))
        fixture = json.loads((ROOT / "mail/testdata/django61.json").read_text())
        self.assertEqual(observed["django"], "6.1")
        # Django sources are fixed; Python's email source changes across the
        # separately owned supported-interpreter matrix.
        self.assertEqual(observed["observations"], fixture["observations"])
        for name in ("django_mail", "message", "locmem", "smtp"):
            self.assertEqual(observed["source_sha256"][name], fixture["source_sha256"][name])

    def test_recipient_and_delivery_controls(self):
        expected = self.observe()["observations"]
        controls = [
            ("from django.core.mail import EmailMessage\nEmailMessage.recipients=lambda self: self.to + self.cc", ["mime", "recipients"]),
            ("from django.core.mail.backends.locmem import EmailBackend\nEmailBackend.send_messages=lambda self,messages: 0", ["memory", "sent"]),
        ]
        for mutation, keys in controls:
            with self.subTest(observation=keys):
                before, after = expected, self.observe(mutation=mutation)["observations"]
                for key in keys:
                    before, after = before[key], after[key]
                self.assertNotEqual(before, after)
