import json
import unittest

from conformance.runners.django import integer_field_reference


class IntegerFieldReferenceTests(unittest.TestCase):
    def test_pinned_big_integer_observations_match_checked_in_fixture(self):
        actual = integer_field_reference.observe()
        expected = json.loads(integer_field_reference.OBSERVATIONS.read_text(encoding="utf-8"))
        self.assertEqual(actual, expected)
        inputs = json.loads(integer_field_reference.INPUTS.read_text(encoding="utf-8"))
        self.assertEqual(len(actual["cases"]), 2 * len(inputs))
