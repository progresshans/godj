import json
import platform
import sqlite3
import subprocess
import sys
import unittest
from contextlib import closing
from pathlib import Path


class DecimalPrecisionReferenceTests(unittest.TestCase):
    def test_fresh_pinned_precision_migration_observations(self):
        root = Path(__file__).resolve().parents[4]
        result = subprocess.run([sys.executable, "-W", "error", str(root / "conformance/runners/django/decimal_precision_reference.py")],
                                cwd=root, capture_output=True, text=True, check=True)
        self.assertEqual(result.stderr, "")
        actual = json.loads(result.stdout)
        expected = json.loads((root / "internal/decimaltest/testdata/precision-changes-django61.json").read_bytes())
        self.assertEqual(actual.pop("python"), platform.python_version())
        expected.pop("python")
        with closing(sqlite3.connect(":memory:")) as connection:
            source_id = connection.execute("SELECT sqlite_source_id()").fetchone()[0]
        self.assertEqual(actual.pop("sqlite"), {"version": sqlite3.sqlite_version, "source_id": source_id})
        expected.pop("sqlite")
        self.assertEqual(actual, expected)
        self.assertEqual(actual["django"], "6.1")
        profiles = {p["name"]: p for p in actual["profiles"]}
        self.assertEqual(len(profiles), 12)
        self.assertEqual(profiles["identity"]["autodetected"], [])
        for name, profile in profiles.items():
            if name != "identity":
                self.assertEqual(profile["autodetected"], ["AlterField"])
            for direction in ("forward", "reverse"):
                self.assertTrue(profile[direction]["applied"])
                self.assertEqual(profile[direction]["snapshot"], profile[direction]["reopened"])
                self.assertEqual(profile["initial"]["physical"], profile[direction]["snapshot"]["physical"])
            self.assertEqual(profile["initial"]["rows"], profile["reverse"]["snapshot"]["rows"])
        rounded = profiles["reduce_scale_rounding"]["forward"]["snapshot"]["rows"]
        self.assertEqual([r["value"]["text"] for r in rounded[:-1]], ["-1.24", "-1.22", "1.22", "1.24"])
        integral = profiles["zero_scale_rounding"]["forward"]["snapshot"]["rows"]
        self.assertEqual([r["value"]["text"] for r in integral[:-1]], ["-2", "2"])
        failures = {}
        for name, profile in profiles.items():
            failed = [r for r in profile["forward"]["snapshot"]["rows"] if "exception" in r]
            if failed:
                self.assertTrue(all(r["exception"] == "InvalidOperation" for r in failed))
                failures[name] = len(failed)
        self.assertEqual(failures, {"reduce_whole_overflow": 3, "scale_uses_existing_whole_capacity": 2, "zero_whole_digits": 1})


if __name__ == "__main__":
    unittest.main()
