import json
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class EagerJoinReferenceTests(unittest.TestCase):
    def test_independent_eager_join_reference_matches_fixture(self):
        result = subprocess.run(
            [sys.executable, "-m", "conformance.runners.django.eager_join_reference"],
            cwd=ROOT, check=True, capture_output=True, timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        actual = json.loads(result.stdout)
        expected = json.loads((ROOT / "orm/testdata/eager-joins-django61.json").read_text())
        self.assertEqual(actual, expected)
        self.assertEqual(len(actual["observations"]), 88)
        self.assertEqual(len({item["name"] for item in actual["observations"]}), 88)
        for item in actual["observations"]:
            self.assertEqual(item["count"], len(item["rows"]))
            self.assertEqual(item["warm_count"], item["count"])
            self.assertEqual(item["warm_first"], item["first"])
            self.assertEqual(item["warm_queries"], 0)
            self.assertIn(len(item["all_sql"]), (0, 1))
            self.assertEqual(len(item["count_sql"]), len(item["all_sql"]))
            self.assertEqual(len(item["first_sql"]), len(item["all_sql"]))
