import json
import platform
import sqlite3
import subprocess
import sys
import unittest
from contextlib import closing
from pathlib import Path


class DecimalReferenceTests(unittest.TestCase):
    def test_fresh_pinned_precision_and_storage_observations(self):
        root = Path(__file__).resolve().parents[4]
        process = subprocess.run([sys.executable, "-W", "error", str(root / "conformance/runners/django/decimal_reference.py")],
            cwd=root, capture_output=True, text=True, check=True)
        self.assertEqual(process.stderr, "")
        actual = json.loads(process.stdout)
        expected = json.loads((root / "internal/decimaltest/testdata/django61.json").read_bytes())
        self.assertEqual(actual.pop("python"), platform.python_version())
        expected.pop("python")
        runtime_sqlite = actual.pop("sqlite")
        expected.pop("sqlite")
        with closing(sqlite3.connect(":memory:")) as connection:
            source_id = connection.execute("SELECT sqlite_source_id()").fetchone()[0]
        self.assertEqual(runtime_sqlite, {"version": sqlite3.sqlite_version, "source_id": source_id})
        self.assertEqual(actual, expected)
        self.assertEqual(tuple(len(actual[name]) for name in ("model", "form", "serializer", "json_numbers", "precision")), (89, 136, 360, 19, 90))
        database = actual["database"]
        self.assertEqual((len(database["queries"]), len(database["relations"])), (9, 9))
        self.assertEqual(database["after_add"], [["existing", None]])
        self.assertEqual(database["after_rollback"], database["after_update"])
        self.assertEqual(database["after_remove"], [row[0] for row in database["after_update"]])
        storage = {item["label"]: item for item in database["storage"]}
        self.assertEqual(len(storage), 9)
        self.assertEqual(storage["fraction"]["read"]["text"], "0.10")
        self.assertEqual(storage["negative_zero"]["read"]["text"], "0.00")
        self.assertEqual(storage["half_positive"]["read"]["text"], "1.24")
        self.assertEqual(storage["half_negative"]["read"]["text"], "-1.24")
        self.assertEqual(storage["half_even_positive"]["read"]["text"], "1.22")
        self.assertEqual(storage["half_even_negative"]["read"]["text"], "-1.22")
        self.assertEqual(storage["precise"]["input"], "123456789012345678.123456789012")
        self.assertEqual(storage["precise"]["read"]["text"], "123456789012345680.000000000000")
        self.assertEqual(storage["maximum"]["read_exception"], "InvalidOperation")
        self.assertEqual(storage["maximum"]["raw"], 10**18)
        changed_exceptions = [item for item in actual["form"] if any("exception" in value for value in item["changed"].values())]
        self.assertEqual(len(changed_exceptions), 2)
        for item in changed_exceptions:
            self.assertEqual(item["input"], {"cost": "sNaN"})
            self.assertFalse(item["valid"])
            self.assertEqual(item["changed"], {"null": {"value": True},
                "zero": {"exception": "InvalidOperation"}, "negative_zero": {"exception": "InvalidOperation"},
                "same": {"exception": "InvalidOperation"}})
        numbers = {item["raw"]: item for item in actual["json_numbers"]}
        self.assertEqual(numbers["1.200"]["rendered"], '{"cost":"1.20"}')
        self.assertEqual(numbers["-0.0"]["rendered"], '{"cost":"-0.00"}')
        self.assertEqual(numbers["1e309"]["errors"], {"cost": ["invalid"]})


if __name__ == "__main__":
    unittest.main()
