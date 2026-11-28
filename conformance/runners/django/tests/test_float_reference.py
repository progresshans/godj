import json
import platform
import sqlite3
import subprocess
import sys
import unittest
from contextlib import closing
from pathlib import Path


class FloatReferenceTests(unittest.TestCase):
    def test_fresh_pinned_model_form_serializer_and_database(self):
        root = Path(__file__).resolve().parents[4]
        runner = root / "conformance/runners/django/float_reference.py"
        process = subprocess.run([sys.executable, "-W", "error", str(runner)], cwd=root, capture_output=True, text=True, check=True)
        self.assertEqual(process.stderr, "")
        actual = json.loads(process.stdout)
        expected = json.loads((root / "internal/floattest/testdata/django61.json").read_bytes())
        self.assertEqual(actual.pop("python"), platform.python_version())
        expected.pop("python")
        runtime_sqlite = actual.pop("sqlite")
        expected.pop("sqlite")
        with closing(sqlite3.connect(":memory:")) as connection:
            source_id = connection.execute("SELECT sqlite_source_id()").fetchone()[0]
        self.assertEqual(runtime_sqlite, {"version": sqlite3.sqlite_version, "source_id": source_id})
        self.assertEqual(actual, expected)
        self.assertEqual(tuple(len(actual[name]) for name in ("model", "form", "serializer", "json_numbers")), (89, 136, 360, 16))
        database = actual["database"]
        self.assertEqual((len(database["queries"]), len(database["relations"])), (9, 9))
        self.assertEqual(database["after_add"], [["existing", None]])
        self.assertEqual(database["after_rollback"], database["after_update"])
        stored = {item["label"]: item for item in database["storage_special"]}
        self.assertIsNone(stored["nan"]["stored"])
        self.assertEqual(stored["nan"]["sqlite_type"], "null")
        self.assertEqual(stored["negative_zero"]["input"]["bits"], "8000000000000000")
        self.assertEqual(stored["negative_zero"]["stored"]["bits"], "0000000000000000")
        self.assertEqual(stored["positive_infinity"]["stored"]["bits"], "7ff0000000000000")
        self.assertEqual(stored["negative_infinity"]["stored"]["bits"], "fff0000000000000")
        numbers = {item["raw"]: item for item in actual["json_numbers"]}
        self.assertEqual(numbers["0.1"]["validated"]["effort"]["bits"], "3fb999999999999a")
        self.assertEqual(numbers["5e-324"]["validated"]["effort"]["bits"], "0000000000000001")
        self.assertTrue(numbers["1e309"]["valid"])
        self.assertEqual(numbers["1e309"]["render_exception"], "ValueError")


if __name__ == "__main__":
    unittest.main()
