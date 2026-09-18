import json
import unittest
from conformance.runners.django import datetime_field_reference


class DateTimeFieldReferenceTests(unittest.TestCase):
    def test_pinned_datetime_observations_match_checked_in_fixture(self):
        actual = datetime_field_reference.observe()
        expected = json.loads(datetime_field_reference.OBSERVATIONS.read_text(encoding="utf-8"))
        self.assertEqual(actual, expected)
        inputs = json.loads(datetime_field_reference.INPUTS.read_text(encoding="utf-8"))
        self.assertEqual(len(actual["cases"]), 2 * len(inputs))
