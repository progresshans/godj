import json
import os
import platform
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.string_routing_reference"


class StringRoutingReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        command = mutation + f"\nimport runpy; runpy.run_module({RUNNER!r},run_name='__main__')"
        result = subprocess.run([sys.executable, "-W", "error", "-c", command], cwd=ROOT,
                                env=os.environ | {"PYTHONHASHSEED": seed}, capture_output=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr.decode(errors="replace"))
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_fixture_and_seed(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("817"))
        expected = json.loads((ROOT / "web/testdata/string-routing-django61.json").read_text())
        self.assertEqual(expected["python"], "3.14.3")
        self.assertEqual(actual["python"], platform.python_version())
        self.assertEqual({key: value for key, value in actual.items() if key != "python"},
                         {key: value for key, value in expected.items() if key != "python"})
        self.assertEqual(len(actual["observations"]), 24)
        self.assertEqual(len(actual["source_sha256"]), 3)

    def test_converter_mutation_changes_observations(self):
        expected = self.observe()
        actual = self.observe(mutation="from django.urls.converters import StringConverter\nStringConverter.regex='[0-9]+'")
        before = {o["name"]: o for o in expected["observations"]}
        after = {o["name"]: o for o in actual["observations"]}
        for name in ("reset_token", "reset_uid", "confirmation", "unicode", "percent"):
            self.assertNotEqual(before[name], after[name])
