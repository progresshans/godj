import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import unittest


ROOT = Path(__file__).resolve().parents[4]
FIXTURES = ROOT / "codegen/consumertest/testdata/updateorcreate"


class UpdateOrCreateReferenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.observed = {}
        for name in ("update_or_create", "row_lock"):
            observer = ROOT / "conformance/runners/django" / (name + "_reference.py")
            cls.observed[name] = []
            for seed in ("0", "813"):
                environment = {
                    key: value for key, value in os.environ.items()
                    if key not in {"GODJ_UPSERT_REFERENCE_DATABASE", "GODJ_ROW_LOCK_REFERENCE_DATABASE"}
                }
                environment["PYTHONHASHSEED"] = seed
                result = subprocess.run(
                    [sys.executable, "-W", "error", str(observer)],
                    cwd=ROOT, env=environment, capture_output=True, text=True,
                    check=True, timeout=90,
                )
                if result.stderr:
                    raise AssertionError(result.stderr)
                cls.observed[name].append(json.loads(result.stdout))

    def test_fresh_sqlite_observations_match_and_ignore_hashseed(self):
        for name, observations in self.observed.items():
            with self.subTest(observer=name):
                self.assertEqual(observations[0], observations[1])
                actual = dict(observations[0])
                expected = json.loads((FIXTURES / (name + "-django61-sqlite.json")).read_text())
                self.assertEqual(actual.pop("python"), platform.python_version())
                self.assertEqual(expected.pop("python"), "3.14.3")
                self.assertEqual(actual, expected)
                self.assertEqual(actual["django"], "6.1")
                self.assertEqual(actual["backend"], "sqlite")

    def test_both_fixtures_bind_observer_and_independent_upstream_sources(self):
        # PostgreSQL execution belongs to the native DB capture and Go DB
        # consumer. This test checks provenance, not a substitute DB replay.
        for name, observations in self.observed.items():
            observer = ROOT / "conformance/runners/django" / (name + "_reference.py")
            digest = hashlib.sha256(observer.read_bytes()).hexdigest()
            for backend in ("sqlite", "postgres"):
                with self.subTest(observer=name, backend=backend):
                    fixture = json.loads((FIXTURES / (name + "-django61-" + backend + ".json")).read_text())
                    self.assertEqual(fixture["observer_sha256"], digest)
                    self.assertEqual(fixture["source_sha256"], observations[0]["source_sha256"])
                    self.assertEqual(set(fixture["source_sha256"]), {"QuerySet", "Atomic", "SQLCompiler"})
                    self.assertEqual(fixture["backend"], "postgresql" if backend == "postgres" else backend)
                    inventories = {"cases": 14, "contention": 2} if name == "update_or_create" else {
                        "terminals": 10, "targets": 8, "contention": 8 if backend == "postgres" else 0,
                    }
                    for key, size in inventories.items():
                        self.assertEqual(len(fixture[key]), size)
                        self.assertEqual(len({row["case"] for row in fixture[key]}), size)


if __name__ == "__main__":
    unittest.main()
