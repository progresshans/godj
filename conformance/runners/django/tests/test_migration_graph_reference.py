import json
import platform
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class MigrationGraphReferenceTests(unittest.TestCase):
    def test_fresh_historical_graph_observations_match_raw_reference(self):
        result = subprocess.run(
            [sys.executable, "-m", "conformance.runners.django.migration_graph_reference"],
            cwd=ROOT, check=True, capture_output=True, timeout=30,
        )
        self.assertEqual(result.stderr, b"")
        actual = json.loads(result.stdout)
        expected = json.loads((ROOT / "internal/migrationgraphtest/testdata/django61.json").read_text())
        # Runtime fingerprint is captured, but patch/minor Python versions
        # in the portable CI matrix do not change the observable contract.
        self.assertEqual(actual.pop("python"), platform.python_version())
        self.assertEqual(expected.pop("python"), "3.14.7")
        self.assertEqual(actual, expected)
        phases = [item["phase"] for item in actual["observations"]]
        self.assertEqual(phases, ["base", "links", "self", "choices", "reopen", "reverse_self", "reverse_links", "zero", "reapply", "second_zero"])
        # Preserve the actual Django behavior, including its sequence reset.
        # DEV-0013 is a separate GoDj policy, never a rewrite of this oracle.
        self.assertEqual(actual["observations"][6]["rows"]["a"][-1], [3, "next", None])
