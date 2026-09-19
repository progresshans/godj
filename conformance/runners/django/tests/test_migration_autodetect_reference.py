import json
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path


class MigrationAutodetectReferenceTests(unittest.TestCase):
    def test_fresh_pinned_autodetector_and_database_replay(self):
        root = Path(__file__).resolve().parents[4]
        runner = root / "conformance/runners/django/migration_autodetect_reference.py"
        process = subprocess.run([sys.executable, str(runner)], cwd=root, capture_output=True, text=True, check=True)
        self.assertEqual(process.stderr, "")
        actual = json.loads(process.stdout)
        expected = json.loads((root / "internal/migrationgraphtest/testdata/django-autodetect61.json").read_text())
        self.assertEqual(actual["python"], platform.python_version())
        actual.pop("python")
        expected.pop("python")
        runtime_sqlite = actual.pop("sqlite")
        expected.pop("sqlite")
        with sqlite3.connect(":memory:") as connection:
            source_id = connection.execute("SELECT sqlite_source_id()").fetchone()[0]
        self.assertEqual(runtime_sqlite, {"version": sqlite3.sqlite_version, "source_id": source_id})
        self.assertEqual(actual, expected)
        self.assertEqual([value["case"] for value in actual["observations"]], ["same_app", "cross_app"])
        for value in actual["observations"]:
            self.assertEqual([len(rows) for rows in value["rows"].values()], [1, 1])
            self.assertEqual([len(keys) for keys in value["row_keys"].values()], [1, 1])
            self.assertEqual(value["zero"], {"history_count": 0, "tables": ["django_migrations"]})
            self.assertEqual(value["reapply"]["a_count"], 0)
            self.assertEqual(value["reapply"]["b_count"], 0)


if __name__ == "__main__":
    unittest.main()
