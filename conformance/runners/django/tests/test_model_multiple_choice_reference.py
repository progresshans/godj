"""Independent public-behavior checks and meaningful oracle mutation controls."""
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.model_multiple_choice_reference"


class ModelMultipleChoiceReferenceTests(unittest.TestCase):
    def observe(self, *, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        # Each subprocess owns its SQLite file. PostgreSQL observation capture
        # has a separate, explicitly provisioned database owner.
        environment.pop("GODJ_MODEL_MULTIPLE_DATABASE", None)
        code = mutation + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__')"
        result = subprocess.run(
            [sys.executable, "-W", "error", "-c", code], cwd=ROOT,
            env=environment, check=True, capture_output=True, timeout=60,
        )
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_reference_is_deterministic_and_matches_captured_behavior(self):
        first = self.observe()
        second = self.observe(seed="813")
        self.assertEqual(first, second)
        self.assertEqual(len(first["observations"]), 476)
        expected = json.loads((ROOT / "forms/testdata/model-multiple-choice-django61-sqlite.json").read_text())
        for name in ("django", "choices", "observations", "direct_python", "out_of_range", "django_model_multiple_source_sha256"):
            self.assertEqual(first[name], expected[name], name)

    def test_meaning_changes_do_not_pass_as_reference_equivalence(self):
        original = self.observe()
        unchanged = self.observe(mutation="from django import forms\nforms.ModelMultipleChoiceField.has_changed = lambda *args: False")
        self.assertNotEqual(original["observations"], unchanged["observations"])
        self.assertTrue(any(entry["changed"] for entry in original["observations"]))
        self.assertFalse(any(entry["changed"] for entry in unchanged["observations"]))
        aliases = self.observe(mutation="""
from django import forms
original = forms.ModelMultipleChoiceField._check_values
def accept_plus_alias(self, values):
    return original(self, [value.removeprefix('+') if isinstance(value, str) else value for value in values])
forms.ModelMultipleChoiceField._check_values = accept_plus_alias
""")
        original_aliases = [entry for entry in original["observations"] if entry["raw"] == ["+7"]]
        mutated_aliases = [entry for entry in aliases["observations"] if entry["raw"] == ["+7"]]
        self.assertTrue(all(entry["errors"] == ["invalid_choice"] for entry in original_aliases))
        self.assertTrue(all(entry["errors"] == [] and entry["value"] == [7] for entry in mutated_aliases))


if __name__ == "__main__":
    unittest.main()
