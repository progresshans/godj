import json
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class ChoicesReferenceTests(unittest.TestCase):
    def test_independent_choices_reference_matches_fixture(self):
        result = subprocess.run(
            [sys.executable, "-m", "conformance.runners.django.choices_reference"],
            cwd=ROOT, check=True, capture_output=True, timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        actual = json.loads(result.stdout)
        expected = json.loads((ROOT / "schema/testdata/choices-django61.json").read_text())
        self.assertEqual(actual, expected)
        self.assertEqual(sum(len(item.get("cases", [])) for item in actual["observations"]), 19)
        self.assertEqual(actual["observations"][-1]["state_operations"], ["AlterField"])
        self.assertEqual(actual["observations"][-1]["choices_only_sql"], [])
