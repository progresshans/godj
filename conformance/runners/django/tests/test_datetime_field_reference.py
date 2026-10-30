import json
import sys
import unittest
from conformance.runners.django import datetime_field_reference


class DateTimeFieldReferenceTests(unittest.TestCase):
    def test_datetime_observations_match_the_python_profile(self):
        actual = datetime_field_reference.observe()
        expected = json.loads(datetime_field_reference.OBSERVATIONS.read_text(encoding="utf-8"))
        # The checked-in Go reference is Python 3.14.3. Direct observations in
        # the supported Python 3.12.13/3.13.15 lanes reject end-of-day 24:00,
        # while 3.14.3/3.14.7 normalize it to the next day. Keep this exact
        # profile difference visible without skipping compatibility coverage
        # or deriving expected values from the implementation under test.
        if sys.version_info < (3, 14):
            adjusted = 0
            for case in expected["cases"]:
                if case["input"] == "2026-09-19T24:00:00Z":
                    case.update(value=None, utc=None, codes=["invalid"])
                    adjusted += 1
            self.assertEqual(adjusted, 2)
        self.assertEqual(actual, expected)
        inputs = json.loads(datetime_field_reference.INPUTS.read_text(encoding="utf-8"))
        self.assertEqual(len(actual["cases"]), 2 * len(inputs))
