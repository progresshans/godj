import json
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class MembershipReferenceTests(unittest.TestCase):
    def test_scalar_membership_results_match_the_independent_fixture(self):
        result = subprocess.run(
            [sys.executable, "-m", "conformance.runners.django.membership_reference"],
            cwd=ROOT,
            check=True,
            capture_output=True,
            timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        actual = json.loads(result.stdout)
        expected = json.loads((ROOT / "orm/testdata/in-django61.json").read_text())
        self.assertEqual(actual, expected)
        self.assertEqual(len(actual["cases"]), 120)
        self.assertEqual(len({case["name"] for case in actual["cases"]}), 120)
