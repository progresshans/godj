import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.password_reset_http_reference"


class PasswordResetHTTPReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_PASSWORD_RESET_HTTP_DATABASE", None)
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup=django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup=setup\n"
        result = subprocess.run([sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r},run_name='__main__',alter_sys=True)"],
                                cwd=ROOT, env=environment, capture_output=True, check=True, timeout=180)
        self.assertEqual(result.stderr, b"")
        for secret in (b"original HTTP reset password", b"replacement HTTP reset password", b"independent-http-reset-reference-key",
                       b"private session failure", b"private delivery failure", b"https://reset.example.test/reset/"):
            self.assertNotIn(secret, result.stdout)
        return json.loads(result.stdout)

    def test_pinned_backends_and_hash_seeds(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("817"))
        self.assertEqual((actual["django"], actual["python"]), ("6.1", "3.14.3"))
        self.assertEqual(len(actual["source_sha256"]), 8)
        for backend in ("sqlite", "postgres"):
            expected = json.loads((ROOT / f"internal/identitytest/testdata/password-reset-http-django61-{backend}.json").read_text())
            for field in ("observations", "source_sha256"):
                self.assertEqual(actual[field], expected[field], (backend, field))

    def test_csrf_cleanup_and_login_mutations(self):
        expected = self.observe()["observations"]
        controls = [
            ("from django.middleware.csrf import CsrfViewMiddleware\nCsrfViewMiddleware.process_view=lambda self,*args,**kwargs: None", ["csrf", "missing", "status"]),
            ("from django.contrib.auth.views import PasswordResetConfirmView\nfrom django.views.generic.edit import FormView\ndef form_valid(self,form):\n    form.save()\n    return FormView.form_valid(self,form)\nPasswordResetConfirmView.form_valid=form_valid", ["success", "anonymous", "token_removed"]),
            ("from django.contrib.auth.views import PasswordResetConfirmView\nPasswordResetConfirmView.post_reset_login=True", ["success", "anonymous", "authenticated"]),
        ]
        for mutation, keys in controls:
            with self.subTest(observation=keys):
                before, after = expected, self.observe(mutation=mutation)["observations"]
                for key in keys:
                    before, after = before[key], after[key]
                self.assertNotEqual(before, after)
