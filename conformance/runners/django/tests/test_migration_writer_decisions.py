from __future__ import annotations

import ast
import hashlib
import unittest
from pathlib import Path
from typing import Any

from conformance.runners.django import migration_writer_decisions as decisions
from conformance.runners.django.normalizer import canonical_json


EXPECTED_SCENARIOS = (
    "godj.migration.writer.deterministic_candidate",
    "godj.migration.writer.unsupported_delta_fail_closed",
    "godj.migration.writer.snapshot_and_protocol_boundary",
    "godj.migration.writer.atomic_concurrent_publication",
    "godj.migration.writer.interruption_recovery_and_roundtrip",
)


def _semantic(value: Any) -> Any:
    if value is None or not isinstance(value, dict) or "type" not in value:
        return value
    kind = value["type"]
    if kind == "object":
        return {
            field["name"]: _semantic(field["value"])
            for field in value["fields"]
        }
    if kind == "list":
        return [_semantic(item) for item in value["items"]]
    if kind == "null":
        return None
    if kind == "int":
        return int(value["value"])
    if kind == "bytes":
        return value["value"]
    return value["value"]


class MigrationWriterDecisionTests(unittest.TestCase):
    def test_registry_is_exact_and_observations_are_deterministic(self) -> None:
        self.assertEqual(tuple(decisions.SCENARIOS), EXPECTED_SCENARIOS)
        contract_ids = ("MIG-102", "MIG-107", "MIG-108", "MIG-109", "MIG-110")
        for contract_id, scenario in zip(
            contract_ids, decisions.SCENARIOS.values(), strict=True
        ):
            with self.subTest(contract=contract_id):
                self.assertEqual(
                    canonical_json(scenario(contract_id)),
                    canonical_json(scenario(contract_id)),
                )

    def test_candidate_bytes_and_digest_are_stable_without_time_or_randomness(self) -> None:
        observation = decisions.deterministic_candidate("MIG-102")
        result = _semantic(observation["result"])
        metrics = _semantic(observation["metrics"])
        documents = [case["document"] for case in result["cases"]]
        digests = [case["sha256"] for case in result["cases"]]
        self.assertEqual(len(set(documents)), 1)
        self.assertEqual(len(set(digests)), 1)
        document = decisions._sample_document()
        self.assertEqual(
            digests[0],
            "sha256:" + hashlib.sha256(document).hexdigest(),
        )
        self.assertEqual(metrics["distinct_documents"], 1)
        self.assertEqual(metrics["random_values"], 0)
        self.assertEqual(result["timestamp_fields"], 0)

    def test_unsupported_delta_protocol_and_publication_decisions_fail_closed(self) -> None:
        unsupported = _semantic(
            decisions.unsupported_delta_fail_closed("MIG-107")["result"]
        )
        self.assertFalse(unsupported["partial_success"])
        self.assertEqual({case["candidate_count"] for case in unsupported["cases"]}, {0})
        self.assertEqual(
            {case["category"] for case in unsupported["cases"]},
            {"migration_autodetect_error"},
        )

        boundary = _semantic(
            decisions.snapshot_and_protocol_boundary("MIG-108")["result"]
        )
        self.assertEqual(boundary["catalog_and_schema_snapshot"], "one_private_request")
        self.assertFalse(boundary["existing_protocol_bytes_changed"])
        self.assertFalse(boundary["response_contains_database_configuration"])

        publication = _semantic(
            decisions.atomic_concurrent_publication("MIG-109")["result"]
        )
        self.assertFalse(publication["stale_false_success"])
        self.assertEqual({case["overwrites"] for case in publication["cases"]}, {0})

        recovery = _semantic(
            decisions.interruption_recovery_and_roundtrip("MIG-110")["result"]
        )
        self.assertFalse(recovery["existing_sources_mutated"])
        self.assertEqual(recovery["unsafe_residue"], 0)
        self.assertTrue(all(case["strict_loadable"] for case in recovery["cases"]))

    def test_source_has_no_django_or_expected_artifact_dependency(self) -> None:
        source = Path(decisions.__file__).read_text(encoding="utf-8")
        tree = ast.parse(source)
        imports: set[str] = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imports.update(alias.name for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module is not None:
                imports.add(node.module)
        self.assertFalse(
            any(name == "django" or name.startswith("django.") for name in imports)
        )
        for forbidden in (
            "conformance/contracts",
            "conformance/oracles",
            "conformance/fixtures",
            "not_implemented",
            "not-implemented",
            "sqlite3",
        ):
            self.assertNotIn(forbidden, source)


if __name__ == "__main__":
    unittest.main()
