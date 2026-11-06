import json
import platform
import sqlite3
import subprocess
import sys
import unittest
from contextlib import closing
from pathlib import Path


class DurationReferenceTests(unittest.TestCase):
    def test_fresh_pinned_model_form_serializer_and_database(self):
        root = Path(__file__).resolve().parents[4]
        runner = root / "conformance/runners/django/duration_reference.py"
        process = subprocess.run([sys.executable, "-W", "error", str(runner)], cwd=root, capture_output=True, text=True, check=True)
        self.assertEqual(process.stderr, "")
        actual = json.loads(process.stdout)
        expected = json.loads((root / "internal/durationtest/testdata/django61.json").read_bytes())
        self.assertEqual(actual.pop("python"), platform.python_version())
        expected.pop("python")
        runtime_sqlite = actual.pop("sqlite")
        expected.pop("sqlite")
        with closing(sqlite3.connect(":memory:")) as connection:
            source_id = connection.execute("SELECT sqlite_source_id()").fetchone()[0]
        self.assertEqual(runtime_sqlite, {"version": sqlite3.sqlite_version, "source_id": source_id})
        self.assertEqual(actual, expected)
        self.assertEqual((len(actual["model"]), len(actual["form"]), len(actual["serializer"]), len(actual["database"]["queries"])), (67, 106, 272, 9))
        self.assertEqual(len(actual["database"]["relations"]), 9)
        self.assertEqual(len(actual["json_numbers"]), 15)
        self.assertEqual(actual["database"]["aggregates"], {"minimum": "-106751992 19:59:05.224192", "maximum": "106751991 04:00:54.775807"})
        self.assertEqual(actual["database"]["after_add"], [["existing", None]])
        self.assertEqual(actual["database"]["reopened"], [["existing", None], ["minimum", "-106751992 19:59:05.224192"], ["fraction", "1 02:03:04.123456"], ["maximum", "106751991 04:00:54.775807"], ["null", None]])

        overflow = actual["database"]["storage_overflow"]
        self.assertEqual([case["microseconds"] for case in overflow], [-9223372036854775809, 9223372036854775808])
        for case in overflow:
            self.assertEqual(case["exception"], "OverflowError")
            self.assertEqual(case["rows"], actual["database"]["after_update"])


if __name__ == "__main__":
    unittest.main()
