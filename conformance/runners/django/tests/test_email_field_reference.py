import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]


class EmailFieldReferenceTests(unittest.TestCase):
    def run_reference(self, prefix="", seed="0"):
        command = prefix + "\nimport json\nfrom conformance.runners.django.email_field_reference import observe\nprint(json.dumps(observe(),sort_keys=True))"
        result = subprocess.run([sys.executable, "-W", "error", "-c", command], cwd=ROOT,
                                env=os.environ | {"PYTHONDONTWRITEBYTECODE": "1", "PYTHONHASHSEED": seed},
                                text=True, capture_output=True, timeout=90)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        value = json.loads(result.stdout)
        value.pop("python")
        return value

    def test_native_forms_serializers_and_storage_match_fixture(self):
        actual = self.run_reference()
        expected = json.loads((ROOT / "internal/emailtest/testdata/email-django61-sqlite.json").read_text())
        expected.pop("python")
        self.assertEqual(actual, expected)
        self.assertEqual(self.run_reference(seed="8675309"), actual)
        self.assertEqual(sum(len(profile["cases"]) for profile in actual["forms"].values()), 144)
        self.assertEqual(sum(len(profile) for profile in actual["serializers"].values()), 96)
        self.assertEqual(actual["storage"]["before"], actual["storage"]["after"])
        self.assertEqual(actual["storage"]["unvalidated_save"], "also-not-email")
        self.assertEqual(actual["serializer_defaults"], {"legacy": "not-an-email", "padded": "  User@Example.COM  ", "empty": "", "null": None})

    def test_native_validator_mutations_change_observations(self):
        baseline = self.run_reference()
        form_mutation = self.run_reference("from django.forms import EmailField\nEmailField.default_validators = []")
        self.assertNotEqual(form_mutation["forms"], baseline["forms"])
        self.assertEqual(form_mutation["serializers"], baseline["serializers"])
        serializer_mutation = self.run_reference("from rest_framework import fields\nfields.EmailValidator = lambda **kwargs: lambda value: None")
        self.assertNotEqual(serializer_mutation["serializers"], baseline["serializers"])
        self.assertEqual(serializer_mutation["forms"], baseline["forms"])


if __name__ == "__main__":
    unittest.main()
