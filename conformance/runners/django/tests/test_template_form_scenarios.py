from __future__ import annotations

import json
import subprocess
import sys
import unittest

from conformance.runners.django import template_form_scenarios as scenarios
from conformance.templateform.reference import EXPECTED_IDS, generate_suite


class TemplateFormScenarioTests(unittest.TestCase):
    def test_registry_and_observations_are_exact_and_secret_free(self) -> None:
        self.assertEqual(len(scenarios.SCENARIOS), 12)
        self.assertEqual(
            list(scenarios.SCENARIOS),
            [
                "django.template_form.scalar_and_missing",
                "django.template_form.dotted_lookup_precedence",
                "django.template_form.autoescape_and_safe",
                "django.template_form.if_for_and_empty",
                "django.template_form.closed_filters",
                "django.template_form.construction_failures",
                "django.template_form.callable_exposure",
                "django.template_form.unbound_and_bound_empty",
                "django.template_form.valid_article_clean",
                "django.template_form.field_error_codes",
                "django.template_form.cross_field_validation",
                "django.template_form.model_form_write_boundary",
            ],
        )
        phases = (
            "evaluation",
            "evaluation",
            "evaluation",
            "evaluation",
            "evaluation",
            "construction",
            "evaluation",
            "evaluation",
            "evaluation",
            "evaluation",
            "evaluation",
            "commit",
        )
        for contract_id, phase, scenario in zip(
            EXPECTED_IDS, phases, scenarios.SCENARIOS.values(), strict=True
        ):
            observation = scenario(contract_id)
            self.assertEqual(observation["id"], contract_id)
            self.assertEqual(observation["status"], "observed")
            self.assertEqual(observation["phase"], phase)
            serialized = json.dumps(observation, sort_keys=True)
            self.assertNotIn("<html", serialized.lower())
            self.assertNotIn("<form", serialized.lower())
            self.assertNotIn("reference-password", serialized)

    def test_full_reference_suite_is_byte_deterministic(self) -> None:
        first = json.dumps(generate_suite(), sort_keys=True, separators=(",", ":"))
        second = json.dumps(generate_suite(), sort_keys=True, separators=(",", ":"))
        self.assertEqual(first, second)

    def test_fresh_process_reference_bytes_are_deterministic(self) -> None:
        program = (
            "import sys; "
            "from conformance.runners.django.normalizer import canonical_json; "
            "from conformance.templateform.reference import generate_suite; "
            "sys.stdout.buffer.write(canonical_json(generate_suite()))"
        )
        outputs = []
        for _ in range(2):
            process = subprocess.run(
                [sys.executable, "-c", program],
                check=False,
                capture_output=True,
            )
            self.assertEqual(process.returncode, 0, process.stderr.decode())
            self.assertEqual(process.stderr, b"")
            outputs.append(process.stdout)
        self.assertEqual(outputs[0], outputs[1])


if __name__ == "__main__":
    unittest.main()
