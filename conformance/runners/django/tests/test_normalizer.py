from __future__ import annotations

import json
import random
import unittest
from datetime import datetime, timedelta, timezone
from decimal import Decimal
from uuid import UUID

from conformance.runners.django.normalizer import (
    PrimaryKey,
    canonical_json,
    normalize,
)


class NormalizerTests(unittest.TestCase):
    def test_tagged_scalars_preserve_type(self) -> None:
        value = {
            "bool": True,
            "bytes": b"\x00\xff",
            "datetime": datetime(
                2026,
                8,
                7,
                1,
                2,
                3,
                456789,
                tzinfo=timezone(timedelta(hours=9)),
            ),
            "decimal": Decimal("12.340"),
            "int": 42,
            "null": None,
            "pk": PrimaryKey(7),
            "string": "한글",
            "uuid": UUID("12345678-1234-5678-1234-567812345678"),
        }

        normalized = normalize(value)
        fields = {field["name"]: field["value"] for field in normalized["fields"]}
        self.assertEqual(fields["bool"], {"type": "bool", "value": True})
        self.assertEqual(
            fields["bytes"],
            {"type": "bytes", "encoding": "base64", "value": "AP8="},
        )
        self.assertEqual(
            fields["datetime"],
            {"type": "datetime", "value": "2026-08-06T16:02:03.456789Z"},
        )
        self.assertEqual(fields["decimal"], {"type": "decimal", "value": "12.340"})
        self.assertEqual(fields["int"], {"type": "int", "value": "42"})
        self.assertEqual(fields["null"], {"type": "null"})
        self.assertEqual(
            fields["pk"],
            {"type": "pk", "value": {"type": "int", "value": "7"}},
        )
        self.assertEqual(fields["string"], {"type": "string", "value": "한글"})
        self.assertEqual(
            fields["uuid"],
            {"type": "uuid", "value": "12345678-1234-5678-1234-567812345678"},
        )

    def test_object_input_order_does_not_change_canonical_bytes(self) -> None:
        left = normalize({"z": 1, "a": 2})
        right = normalize({"a": 2, "z": 1})
        self.assertEqual(canonical_json(left), canonical_json(right))

    def test_list_order_is_preserved(self) -> None:
        left = canonical_json(normalize([1, 2]))
        right = canonical_json(normalize([2, 1]))
        self.assertNotEqual(left, right)

    def test_randomized_map_order_property(self) -> None:
        randomizer = random.Random(20260807)
        for _ in range(250):
            items = [
                (f"key-{index}", randomizer.randint(-(10**9), 10**9))
                for index in range(randomizer.randint(0, 30))
            ]
            left = dict(items)
            randomizer.shuffle(items)
            right = dict(items)
            self.assertEqual(
                canonical_json(normalize(left)),
                canonical_json(normalize(right)),
            )

    def test_canonical_json_is_valid_utf8_json_with_one_newline(self) -> None:
        encoded = canonical_json(normalize({"message": "안녕"}))
        self.assertTrue(encoded.endswith(b"\n"))
        self.assertFalse(encoded.endswith(b"\n\n"))
        json.loads(encoded)

    def test_rejects_ambiguous_or_unsupported_values(self) -> None:
        for value in (
            1.5,
            Decimal("NaN"),
            datetime(2026, 8, 7),
            {1: "not a string key"},
            PrimaryKey(["not", "a", "scalar", "key"]),
        ):
            with self.subTest(value=value):
                with self.assertRaises((TypeError, ValueError)):
                    normalize(value)


if __name__ == "__main__":
    unittest.main()
