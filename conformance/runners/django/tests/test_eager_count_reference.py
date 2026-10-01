import json
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class EagerCountReferenceTests(unittest.TestCase):
    def test_independent_eager_count_reference_matches_fixture(self):
        result = subprocess.run(
            [sys.executable, "-m", "conformance.runners.django.eager_count_reference"],
            cwd=ROOT, check=True, capture_output=True, timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        actual = json.loads(result.stdout)
        expected = json.loads((ROOT / "orm/testdata/eager-count-django61.json").read_text())
        self.assertEqual(actual, expected)
        self.assertEqual(len(actual["observations"]), 22)
        for item in actual["observations"]:
            self.assertEqual(item["count"], len(item["all_ids"]))
            self.assertEqual(item["warm_count"], item["count"])
            self.assertEqual(item["warm_queries"], 0)
            self.assertFalse(item["cold_populated_cache"])
