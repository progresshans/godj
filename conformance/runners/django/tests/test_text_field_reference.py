import json
import unittest
from conformance.runners.django import text_field_reference


class TextFieldReferenceTests(unittest.TestCase):
    def test_pinned_text_observations_match_checked_in_fixture(self):
        actual = text_field_reference.observe()
        expected = json.loads(text_field_reference.OBSERVATIONS.read_text(encoding="utf-8"))
        self.assertEqual(actual, expected)
        inputs = json.loads(text_field_reference.INPUTS.read_text(encoding="utf-8"))
        self.assertEqual(len(actual["cases"]), 8 * len(inputs))
