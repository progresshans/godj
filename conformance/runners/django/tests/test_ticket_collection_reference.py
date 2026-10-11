import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.ticket_collection_reference"


class TicketCollectionReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_TICKET_COLLECTION_DATABASE", None)
        result = subprocess.run(
            [sys.executable, "-W", "error", "-c", mutation + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__')"],
            cwd=ROOT, env=environment, capture_output=True, check=True, timeout=60,
        )
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_pinned_observations_preserve_presence_identity_and_rollback(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("813"))
        expected = json.loads((ROOT / "examples/helpdesk/testdata/ticket-collection-django61-drf318-sqlite.json").read_text())
        for field in ("observations", "coercions", "rollback", "scope_changed_after_validation", "source_sha256"):
            self.assertEqual(actual[field], expected[field], field)
        self.assertEqual(len(actual["observations"]), 48)
        self.assertEqual(actual["rollback"]["subject"], "Before")
        self.assertEqual(actual["rollback"]["labels"], [7, 9])
        self.assertEqual(actual["rollback"]["retained"], {"7": [True, True], "9": [True, True]})
        # This is a deliberately recorded limitation of serializer-time scope
        # checks, not a guarantee provided by Django or DRF.
        self.assertEqual(actual["scope_changed_after_validation"]["labels"], [11])

    def test_dropping_replacement_and_clearing_omission_change_the_oracle(self):
        original = self.observe()
        for mutation in ("validated_data.pop('labels', None)", "validated_data.setdefault('labels', [])"):
            changed = self.observe(mutation="""
from rest_framework import serializers
original_update = serializers.ModelSerializer.update
def mutated_update(self, instance, validated_data):
    validated_data = dict(validated_data)
    MUTATION
    return original_update(self, instance, validated_data)
serializers.ModelSerializer.update = mutated_update
""".replace("MUTATION", mutation))
            self.assertNotEqual(original["observations"], changed["observations"])


if __name__ == "__main__":
    unittest.main()
