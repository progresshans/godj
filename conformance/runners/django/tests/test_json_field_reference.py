import json
import platform
import sqlite3
import subprocess
import sys
import unittest
from contextlib import closing
from pathlib import Path


class JSONFieldReferenceTests(unittest.TestCase):
    def test_fresh_pinned_json_field_observations(self):
        root = Path(__file__).resolve().parents[4]
        result = subprocess.run([sys.executable, "-W", "error", str(root / "conformance/runners/django/json_field_reference.py")],
                                cwd=root, capture_output=True, text=True, check=True)
        self.assertEqual(result.stderr, "")
        actual = json.loads(result.stdout)
        expected = json.loads((root / "internal/jsontest/testdata/django61.json").read_bytes())
        self.assertEqual(actual.pop("python"), platform.python_version())
        expected.pop("python")
        with closing(sqlite3.connect(":memory:")) as connection:
            source_id = connection.execute("SELECT sqlite_source_id()").fetchone()[0]
        self.assertEqual(actual.pop("sqlite"), {"version": sqlite3.sqlite_version, "source_id": source_id})
        expected.pop("sqlite")
        self.assertEqual(actual, expected)
        self.assertEqual((actual["django"], actual["drf"]), ("6.1", "3.18.0"))
        self.assertEqual({key: len(actual[key]) for key in ("model", "form", "serializer", "parsed")},
                         {"model": 54, "form": 66, "serializer": 168, "parsed": 32})
        database = actual["database"]
        self.assertEqual(database["type"], "text")
        self.assertIn("JSON_VALID", database["table_sql"])
        self.assertEqual(database["queries"]["sql_null"], ["existing", "sql_null"])
        self.assertEqual(database["queries"]["json_null"], ["json_null"])
        self.assertEqual(database["deprecated_none"], {"rows": ["json_null"], "warnings": ["RemovedInDjango70Warning"]})
        self.assertEqual(database["queries"]["one"], ["one"])
        self.assertEqual(database["queries"]["float"], ["float"])
        self.assertEqual(database["queries"]["object"], ["object"])
        self.assertEqual(database["queries"]["key_a"], ["object", "reordered"])
        self.assertEqual(database["queries"]["contains"], {"exception": "NotSupportedError"})
        self.assertEqual(database["before_rollback"], database["after_rollback"])
        self.assertEqual(database["before_rollback"], database["reopened"])
        self.assertEqual(len(database["after_remove"]), 12)
        for row in database["rejected_writes"] + database["external"]:
            self.assertTrue(row["rows_preserved"], row["label"])
        physical = {row[0]: row[1:] for row in database["physical"]}
        self.assertEqual(physical["sql_null"], [None, "null", None])
        self.assertEqual(physical["json_null"], ["null", "text", "null"])
        self.assertEqual(physical["bigint"], ["340282366920938463463374607431768211455", "text", "integer"])
        # SQLite's key extraction reads the first duplicate while Python's
        # whole-document decoder keeps the last: retain both observations.
        duplicate = next(row for row in database["external"] if row["label"] == "duplicate")
        self.assertTrue(duplicate["key_matches_one"])
        self.assertFalse(duplicate["key_matches_two"])
        self.assertEqual(duplicate["value"]["value"][0][1], {"kind": "integer", "value": "2"})
        serializers = [row for row in actual["serializer"] if row["serializer"] == "optional" and not row["partial"]]
        by_label = {row["label"]: row for row in serializers}
        for label in ("nan", "infinity", "negative_infinity", "bytes", "decimal", "datetime"):
            self.assertEqual(by_label[label]["errors"], {"payload": ["invalid"]})
        self.assertEqual(by_label["large_integer"]["rendered"], '{"payload":340282366920938463463374607431768211455}')
        self.assertTrue(by_label["surrogate"]["valid"])
        self.assertEqual(by_label["surrogate"]["render_exception"], "UnicodeEncodeError")
        self.assertEqual(by_label["json_string"]["rendered"], '{"payload":"{\\"a\\":1}"}')
        required = {(row["label"], row["partial"]): row for row in actual["serializer"] if row["serializer"] == "required"}
        self.assertEqual(required["omitted", False]["errors"], {"payload": ["required"]})
        self.assertEqual(required["null", False]["errors"], {"payload": ["null"]})
        self.assertTrue(required["omitted", True]["valid"])
        self.assertEqual(required["omitted", True]["render_exception"], "KeyError")
        self.assertEqual(actual["representations"]["required"], actual["representations"]["optional"])
        self.assertEqual(actual["representations"]["required"]["null"], '{"payload":null}')


if __name__ == "__main__":
    unittest.main()
