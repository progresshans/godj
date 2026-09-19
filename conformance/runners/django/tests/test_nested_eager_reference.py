import json
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class NestedEagerReferenceTests(unittest.TestCase):
    def test_independent_nested_eager_reference_matches_fixture(self):
        result = subprocess.run(
            [sys.executable, "-m", "conformance.runners.django.nested_eager_reference"],
            cwd=ROOT, check=True, capture_output=True, timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        actual = json.loads(result.stdout)
        expected = json.loads((ROOT / "orm/testdata/nested-eager-django61.json").read_text())
        self.assertEqual(actual, expected)
        observations = actual["observations"]
        self.assertEqual(len(observations), 440)
        self.assertEqual(len({item["name"] for item in observations}), 440)
        self.assertEqual(len({tuple(item["selected"]) for item in observations}), 8)
        for item in observations:
            self.assertEqual(item["count"], len(item["rows"]))
            self.assertEqual(item["warm_count"], item["count"])
            self.assertEqual(item["warm_first"], item["first"])
            self.assertEqual(item["warm_rows"], item["rows"])
            self.assertEqual(item["warm_queries"], 0)
            self.assertIn(len(item["all_sql"]), (0, 1))
            self.assertEqual(len(item["count_sql"]), len(item["all_sql"]))
            self.assertEqual(len(item["first_sql"]), len(item["all_sql"]))
            for row in item["rows"]:
                for path in item["selected"]:
                    parts = path.split("__")
                    for depth in range(1, len(parts) + 1):
                        prefix = "__".join(parts[:depth])
                        self.assertIn(prefix, row["targets"])
                        if depth > 1 and row["targets"]["__".join(parts[:depth - 1])] is None:
                            self.assertIsNone(row["targets"][prefix])
