import json
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class ForwardLookupReferenceTests(unittest.TestCase):
    def test_independent_forward_lookup_reference_matches_fixture(self):
        result = subprocess.run(
            [sys.executable, "-m", "conformance.runners.django.forward_lookup_reference"],
            cwd=ROOT, check=True, capture_output=True, timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        actual = json.loads(result.stdout)
        expected = json.loads((ROOT / "orm/testdata/forward-lookups-django61.json").read_text())
        self.assertEqual(actual, expected)
        self.assertEqual(len(actual["observations"]), 748)
        for item in actual["observations"]:
            self.assertEqual(item["count"], len(item["ids"]))
            self.assertIn(len(item["sql"]), (0, 1))
            self.assertEqual(len(item["count_sql"]), len(item["sql"]))
