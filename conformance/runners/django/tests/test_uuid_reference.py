import json
import platform
import sqlite3
import subprocess
import sys
import unittest
import unicodedata
from contextlib import closing
from pathlib import Path


class UUIDReferenceTests(unittest.TestCase):
    def test_fresh_pinned_uuid_observations(self):
        root = Path(__file__).resolve().parents[4]
        result = subprocess.run([sys.executable, "-W", "error", str(root / "conformance/runners/django/uuid_reference.py")],
                                cwd=root, capture_output=True, text=True, check=True)
        self.assertEqual(result.stderr, "")
        actual = json.loads(result.stdout)
        expected = json.loads((root / "internal/uuidtest/testdata/django61.json").read_bytes())
        self.assertEqual(actual.pop("python"), platform.python_version())
        expected.pop("python")
        with closing(sqlite3.connect(":memory:")) as connection:
            source_id = connection.execute("SELECT sqlite_source_id()").fetchone()[0]
        self.assertEqual(actual.pop("sqlite"), {"version": sqlite3.sqlite_version, "source_id": source_id})
        expected.pop("sqlite")

        unicode_actual = actual.pop("unicode_decimal")
        unicode_expected = expected.pop("unicode_decimal")
        self.assertEqual(unicode_expected["version"], "16.0.0")
        self.assertEqual(unicode_actual["version"], unicodedata.unidata_version)
        self.assertIn(unicodedata.unidata_version, ("15.0.0", "15.1.0", "16.0.0"))
        self.assertEqual(len(unicode_expected["ranges"]), 76)
        self.assertEqual(len(unicode_actual["ranges"]), 76 if unicodedata.unidata_version == "16.0.0" else 68)
        # Older supported Python versions use Unicode 15. These explicitly
        # lack the eight digit ranges added by the pinned Python 3.14 profile.
        self.assertEqual(unicode_actual["ranges"], [item for item in unicode_expected["ranges"]
                         if unicodedata.decimal(chr(item["first"]), None) == 0])
        self.assertEqual(actual, expected)
        self.assertEqual((actual["django"], actual["drf"]), ("6.1", "3.18.0"))
        self.assertEqual({key: len(actual[key]) for key in ("model", "form", "serializer", "json_numbers")},
                         {"model": 62, "form": 86, "serializer": 252, "json_numbers": 13})
        numbers = {item["raw"]: item for item in actual["json_numbers"]}
        for raw in ("0", "1", "18446744073709551616", "340282366920938463463374607431768211455", "-0", "true", "false", "null"):
            self.assertTrue(numbers[raw]["valid"], raw)
        for raw in ("-1", "340282366920938463463374607431768211456", "1.0", "1e0", "-0.0"):
            self.assertEqual(numbers[raw]["errors"], {"reference": ["invalid"]}, raw)
        # uuid.UUID(int=bool) keeps the bool in its public .int attribute;
        # its canonical UUID value and default wire rendering are still 0/1.
        self.assertEqual(numbers["false"]["validated"]["reference"]["int"], "False")
        self.assertEqual(numbers["true"]["validated"]["reference"]["int"], "True")
        self.assertEqual(numbers["false"]["rendered"], numbers["0"]["rendered"])
        self.assertEqual(numbers["true"]["rendered"], numbers["1"]["rendered"])
        database = actual["database"]
        self.assertEqual(database["type"], "char(32)")
        self.assertEqual(database["before_rollback"], database["after_rollback"])
        self.assertEqual(database["before_rollback"], database["reopened"])
        self.assertEqual(database["after_remove"], [row[0] for row in database["before_rollback"]])
        self.assertEqual(database["after_add"], [["existing", None, {"text": "00000000-0000-0000-0000-000000000000", "hex": "0" * 32, "int": "0"}]])
        for _, value, storage, origin in database["physical"]:
            self.assertEqual(origin, "0" * 32)
            if value is None:
                self.assertEqual(storage, "null")
            else:
                self.assertEqual(storage, "text")
                self.assertEqual(len(value), 32)
                self.assertRegex(value, "^[0-9a-f]{32}$")
        self.assertEqual(database["queries"]["order"], ["existing", "null", "zero", "one", "sample", "lower_half", "upper_half", "maximum"])
        self.assertEqual(database["aggregates"]["minimum"]["int"], "0")
        self.assertEqual(database["aggregates"]["maximum"]["int"], str((1 << 128) - 1))
        self.assertEqual(set(actual["formats"]), {"hex_verbose", "hex", "int", "urn"})


if __name__ == "__main__":
    unittest.main()
