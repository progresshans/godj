"""Capture-reader controls for the independent data-migration observations."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import migration_data_reference as runner


class MigrationDataReferenceTests(unittest.TestCase):
    def setUp(self):
        fingerprint = json.loads((runner.ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json").read_bytes())["fingerprint"]
        self.report = {
            "backend": "sqlite", "database_version": "3.50.4", "django": "6.1", "python": "3.14.3",
            "observations": dict.fromkeys(runner.PHASES, {"rows": 1}), "command_output": dict.fromkeys(("forward", "reverse", "barrier"), {}),
            "input_sha256": runner.input_digests(), "django_commit": fingerprint["django_commit"],
            "upstream_sha256": {"module": "ab" * 32},
        }

    def capture(self, directory, report):
        data = json.dumps(report).encode()
        with patch.object(runner.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, stdout=data)):
            return runner.capture({}, directory, "sqlite", "3.50.4")

    def test_native_capture_preserves_raw_bytes_and_rejects_stale_or_missing_evidence(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            observed, receipt = self.capture(directory, self.report)
            self.assertEqual(observed, self.report)
            data = (directory / receipt["file"]).read_bytes()
            self.assertEqual(data, json.dumps(self.report).encode())
            self.assertEqual(receipt["sha256"], hashlib.sha256(data).hexdigest())
            for field, replacement in (
                ("backend", "postgresql"), ("database_version", "3.48.0"), ("django_commit", "other"),
                ("input_sha256", {"migration_data_reference.py": "ff" * 32}),
                ("observations", {k: v for k, v in self.report["observations"].items() if k != "first_reverse"}),
                ("command_output", {"forward": {}, "reverse": {}}),
            ):
                with self.subTest(field=field), self.assertRaises(ValueError):
                    self.capture(directory, self.report | {field: replacement})
                self.assertEqual((directory / receipt["file"]).read_bytes(), data)

    def test_backends_compare_observed_types_and_sources(self):
        runner.compare(self.report, self.report | {"backend": "postgresql"})
        other = copy.deepcopy(self.report)
        other["observations"]["first_reverse"] = {"rows": True}
        with self.assertRaisesRegex(ValueError, "observations"):
            runner.compare(self.report, other)
        with self.assertRaisesRegex(ValueError, "upstream_sha256"):
            runner.compare(self.report, self.report | {"upstream_sha256": {}})
