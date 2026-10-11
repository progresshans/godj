import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.identity_catalog_reference"


class IdentityCatalogReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_IDENTITY_CATALOG_REFERENCE_DATABASE", None)
        prefix = ""
        if mutation:
            body = "\n".join("    " + line for line in mutation.splitlines())
            prefix = "import django\noriginal_setup = django.setup\ndef setup():\n    original_setup()\n" + body + "\ndjango.setup = setup\n"
        result = subprocess.run(
            [sys.executable, "-W", "error", "-c", prefix + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__', alter_sys=True)"],
            cwd=ROOT, env=environment, capture_output=True, timeout=120,
        )
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_reference_matches_both_backends_and_hash_seeds(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("813"))
        for backend in ("sqlite", "postgres"):
            expected = json.loads((ROOT / f"internal/identitytest/testdata/catalog-django61-{backend}.json").read_text())
            for field in ("observations", "source_sha256"):
                self.assertEqual(actual[field], expected[field], (backend, field))
        for kind in ("group", "permission"):
            self.assertEqual(actual["observations"]["admission"][kind]["add"],
                             {"create": True, "view": False, "change": False, "delete": False})
            self.assertTrue(actual["observations"]["admission"][kind]["change"]["view"])
        self.assertEqual(actual["observations"]["permission_renamed"]["effective"], ["manage"])

    def test_mutated_admission_and_stored_code_change_observations(self):
        baseline = self.observe()["observations"]
        mutations = [
            "from django.contrib.admin.options import ModelAdmin\nModelAdmin.has_view_permission = lambda self, request, obj=None: request.user.has_perm(f'{self.opts.app_label}.view_{self.opts.model_name}')",
            "from django.contrib.auth.admin import GroupAdmin\nGroupAdmin.has_add_permission = lambda self, request: False",
            "from django.contrib.auth.models import Permission\noriginal_save = Permission.save\ndef save(self, *args, **kwargs):\n    if 'codename' in (kwargs.get('update_fields') or []):\n        self.codename = 'change_ticket'\n    return original_save(self, *args, **kwargs)\nPermission.save = save",
        ]
        for mutation in mutations:
            with self.subTest(mutation=mutation.splitlines()[0]):
                self.assertNotEqual(baseline, self.observe(mutation=mutation)["observations"])


if __name__ == "__main__":
    unittest.main()
