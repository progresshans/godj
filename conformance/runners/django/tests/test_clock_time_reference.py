import json
import platform
import sqlite3
import subprocess
import sys
import unittest
from contextlib import closing
from pathlib import Path


class ClockTimeReferenceTests(unittest.TestCase):
    def test_fresh_pinned_model_form_serializer_and_database(self):
        root = Path(__file__).resolve().parents[4]
        runner = root / "conformance/runners/django/clock_time_reference.py"
        process = subprocess.run([sys.executable, "-W", "error", str(runner)], cwd=root, capture_output=True, text=True, check=True)
        self.assertEqual(process.stderr, "")
        actual = json.loads(process.stdout)
        expected = json.loads((root / "internal/clocktimetest/testdata/django61.json").read_bytes())
        self.assertEqual(actual.pop("python"), platform.python_version())
        expected.pop("python")
        runtime_sqlite = actual.pop("sqlite")
        expected.pop("sqlite")
        with closing(sqlite3.connect(":memory:")) as connection:
            source_id = connection.execute("SELECT sqlite_source_id()").fetchone()[0]
        self.assertEqual(runtime_sqlite, {"version": sqlite3.sqlite_version, "source_id": source_id})
        # This explicit profile comes from fresh pinned public-API runs. The
        # product target is Python 3.14.3; older lanes retain their real errors
        # and fractional-second grammar instead of skipping mismatched cases.
        if sys.version_info < (3, 14):
            invalid = {"24:00": "invalid_time", "24:00:00": "invalid_time", "24:00:00.000000": "invalid_time",
                       "24": "invalid", "2400": "invalid", "T24:00": "invalid",
                       "24:00:00.0000001": "invalid_time", "24:00:00+09:00": "invalid"}
            fractions = {"12.5": "12:00:00.500000", "12:34.5": "12:34:00.500000", "12:34,5": "12:34:00.500000"}
            model_changes = serializer_changes = 0
            for case in expected["model"]:
                raw = case["input"]
                if case["type"] != "str":
                    continue
                if raw in invalid:
                    case.pop("value")
                    case["codes"] = [invalid[raw]]
                    model_changes += 1
                elif raw in fractions:
                    case.update(value=fractions[raw], codes=[])
                    model_changes += 1
            for case in expected["serializer"]:
                entry = case["input"].get("at", {})
                raw = entry.get("value")
                if entry.get("type") != "str":
                    continue
                if raw in invalid:
                    case.update(valid=False, validated={}, errors={"at": ["invalid"]})
                    serializer_changes += 1
                elif raw in fractions:
                    case.update(valid=True, validated={"at": fractions[raw]}, errors={})
                    serializer_changes += 1
            self.assertEqual((model_changes, serializer_changes), (11, 44))
        self.assertEqual(actual, expected)
        self.assertEqual((len(actual["model"]), len(actual["form"]), len(actual["serializer"]), len(actual["database"]["queries"])), (85, 152, 344, 9))
        self.assertEqual(len(actual["microsecond_form"]), 152)
        changed_differences = 0
        for standard, precise in zip(actual["form"], actual["microsecond_form"]):
            self.assertFalse(standard["supports_microseconds"])
            self.assertTrue(precise["supports_microseconds"])
            self.assertEqual(standard["cleaned"], precise["cleaned"])
            self.assertEqual(standard["errors"], precise["errors"])
            changed_differences += standard["changed"] != precise["changed"]
        self.assertEqual(changed_differences, 10)
        self.assertEqual(len(actual["database"]["relations"]), 9)
        self.assertEqual(actual["database"]["aggregates"], {"minimum": "00:00:00", "maximum": "23:59:59.999999"})
        self.assertEqual(actual["database"]["after_add"], [["existing", None]])
        self.assertEqual(actual["database"]["reopened"], [["existing", None], ["minimum", "00:00:00"], ["fraction", "12:34:56.123456"], ["maximum", "23:59:59.999999"], ["null", None]])


if __name__ == "__main__":
    unittest.main()
