import json
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class NestedForwardReferenceTests(unittest.TestCase):
    def test_independent_nested_forward_reference_matches_fixture(self):
        result = subprocess.run(
            [sys.executable, "-m", "conformance.runners.django.nested_forward_reference"],
            cwd=ROOT, check=True, capture_output=True, timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        actual = json.loads(result.stdout)
        expected = json.loads((ROOT / "orm/testdata/nested-forward-django61.json").read_text())
        self.assertEqual(actual, expected)
        self.assertEqual(len(actual["observations"]), 146)
        self.assertEqual(len({item["name"] for item in actual["observations"]}), 146)
        for item in actual["observations"]:
            self.assertEqual(item["count"], len(item["ids"]))
            self.assertEqual(item["warm_count"], item["count"])
            self.assertEqual(item["warm_first"], item["first"])
            self.assertEqual(item["warm_queries"], 0)
            self.assertEqual(len(item["targets"]), len(item["ids"]))
            self.assertIn(len(item["all_sql"]), (0, 1))
            self.assertEqual(len(item["count_sql"]), len(item["all_sql"]))
            self.assertEqual(len(item["first_sql"]), len(item["all_sql"]))
