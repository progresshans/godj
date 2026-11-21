import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.login_lifecycle_reference"


class LoginLifecycleReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_LOGIN_LIFECYCLE_DATABASE", None)
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup = django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup = setup\n"
        result = subprocess.run([sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__', alter_sys=True)"],
                                cwd=ROOT, env=environment, capture_output=True, check=True, timeout=120)
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_pinned_backends_and_hash_seeds(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("817"))
        self.assertEqual(len(actual["source_sha256"]), 9)
        for backend in ("sqlite", "postgres"):
            expected = json.loads((ROOT / f"internal/identitytest/testdata/login-lifecycle-django61-{backend}.json").read_text())
            for field in ("observations", "source_sha256"):
                self.assertEqual(actual[field], expected[field], (backend, field))

    def test_signal_authentication_side_effect_and_cross_account_isolation_controls(self):
        actual = self.observe()["observations"]
        mutations = [
            ("from django.contrib.auth.signals import user_logged_in\nuser_logged_in.disconnect(dispatch_uid='update_last_login')", "lifecycle", 1, "last_login"),
            ("from django.contrib.auth.backends import ModelBackend\nfrom django.utils import timezone\noriginal = ModelBackend.authenticate\ndef authenticate(self, *args, **kwargs):\n    user = original(self, *args, **kwargs)\n    if user is not None:\n        user.last_login = timezone.now()\n        user.save(update_fields=['last_login'])\n    return user\nModelBackend.authenticate = authenticate", "authentication_only", None, "last_login"),
            ("from django.contrib.sessions.backends.db import SessionStore\noriginal = SessionStore.flush\ndef flush(self):\n    payload = self.get('payload')\n    original(self)\n    if payload is not None:\n        self['payload'] = payload\nSessionStore.flush = flush", "lifecycle", 4, "payload"),
        ]
        for mutation, group, index, field in mutations:
            with self.subTest(group=group, field=field):
                changed = self.observe(mutation=mutation)["observations"]
                before, after = actual[group], changed[group]
                if index is not None:
                    before, after = before[index], after[index]
                self.assertNotEqual(before[field], after[field])
