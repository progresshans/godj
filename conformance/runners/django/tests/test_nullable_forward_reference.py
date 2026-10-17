import json
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class NullableForwardReferenceTests(unittest.TestCase):
    def test_independent_nullable_forward_reference_matches_fixture(self):
        result = subprocess.run(
            [sys.executable, "-m", "conformance.runners.django.nullable_forward_reference"],
            cwd=ROOT, check=True, capture_output=True, timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        actual = json.loads(result.stdout)
        expected = json.loads((ROOT / "orm/testdata/nullable-forward-django61.json").read_text())
        self.assertEqual(actual, expected)
        self.assertEqual(len(actual["observations"]), 73)
        for item in actual["observations"]:
            self.assertEqual(item["count"], len(item["ids"]))
            self.assertEqual(len(item["sql"]), 1)
            self.assertEqual(len(item["count_sql"]), 1)
