"""Negative controls for the compatibility scenario inventory and reference bytes."""
import hashlib
import json
from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest

import python_semantics as runner


class PythonSemanticTests(unittest.TestCase):
    def test_compare_requires_every_observation_and_preserves_types(self):
        expected = {"first": {"id": "A", "answer": 1}, "second": {"id": "B", "answer": True}}
        scenarios = {"first": lambda name: {"id": name, "answer": 1}, "second": lambda name: {"id": name, "answer": True}}
        raw = b'first\0{"answer":1,"id":"PYCOMPAT-first"}\n\0second\0{"answer":true,"id":"PYCOMPAT-second"}\n\0'
        self.assertEqual(runner.compare(scenarios, expected), (len(raw), hashlib.sha256(raw).hexdigest()))
        with self.assertRaisesRegex(ValueError, "inventory"):
            runner.compare({"first": scenarios["first"]}, expected)
        with self.assertRaisesRegex(ValueError, "contract B"):
            runner.compare(scenarios | {"second": scenarios["first"]}, expected)
        with self.assertRaisesRegex(ValueError, "contract A"):
            runner.compare(scenarios | {"first": lambda _: expected["first"]}, expected)

    def test_rejects_wrong_membership_and_unsealed_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            oracle_dir = root / "observations"
            oracle_dir.mkdir()
            oracle = oracle_dir / "oracle.json"
            manifest = root / "manifest.json"
            suite = SimpleNamespace(name="read", profile="default", manifest=manifest, oracle=oracle)
            profile = {"id": "test", "fingerprint": {}, "lock": {}}
            profile_path = root / "profile.json"
            profile_path.write_text(json.dumps(profile))
            catalog = SimpleNamespace(profiles={"default": SimpleNamespace(path=profile_path)}, suites=[suite])
            record = {"id": "A", "answer": 1}
            oracle.write_text(json.dumps({"profile": profile, "contracts": [record]}))
            (oracle_dir / "SHA256SUMS").write_text(hashlib.sha256(oracle.read_bytes()).hexdigest() + "  oracle.json\n")
            manifest.write_text(json.dumps({"profile_id": "test", "contracts": [{"id": "A", "scenario": "first"}]}))
            self.assertEqual(runner.expectations(catalog), {"first": record})
            catalog.suites.append(suite)
            with self.assertRaisesRegex(ValueError, "duplicated"):
                runner.expectations(catalog)
            catalog.suites.pop()
            manifest.write_text(json.dumps({"profile_id": "test", "contracts": [{"id": "B", "scenario": "first"}]}))
            with self.assertRaisesRegex(ValueError, "contract B"):
                runner.expectations(catalog)
            oracle.write_text("{}")
            with self.assertRaisesRegex(ValueError, "checksum"):
                runner.expectations(catalog)
