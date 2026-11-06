import json
import platform
import sqlite3
import subprocess
import sys
import unittest
from contextlib import closing
from pathlib import Path


class NullableBooleanReferenceTests(unittest.TestCase):
    def test_fresh_pinned_model_form_serializer_and_database(self):
        root = Path(__file__).resolve().parents[4]
        runner = root / "conformance/runners/django/nullable_boolean_reference.py"
        process = subprocess.run([sys.executable, str(runner)], cwd=root, capture_output=True, text=True, check=True)
        self.assertEqual(process.stderr, "")
        actual = json.loads(process.stdout)
        expected = json.loads((root / "internal/nullablebooleantest/testdata/django61.json").read_bytes())
        self.assertEqual(actual["python"], platform.python_version())
        actual.pop("python")
        expected.pop("python")
        runtime_sqlite = actual.pop("sqlite")
        expected.pop("sqlite")
        with closing(sqlite3.connect(":memory:")) as connection:
            source_id = connection.execute("SELECT sqlite_source_id()").fetchone()[0]
        self.assertEqual(runtime_sqlite, {"version": sqlite3.sqlite_version, "source_id": source_id})
        self.assertEqual(actual, expected)
        self.assertEqual(actual["database"]["after_add"], [["existing", None]])
        self.assertEqual(actual["database"]["reopened"], [["existing", None], ["false", False], ["true", True], ["null", None]])


if __name__ == "__main__":
    unittest.main()
