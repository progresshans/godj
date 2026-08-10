from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import unittest
from dataclasses import replace
from pathlib import Path
from typing import Any
from unittest.mock import patch

from django.db import IntegrityError, connection, models
from django.core.exceptions import FieldDoesNotExist

from conformance.runners.django import relation_scenarios as scenarios
from conformance.runners.django import relation_fixture
from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import (
    DEFAULT_PROFILE,
    DEFAULT_RELATION_MANIFEST,
    DEFAULT_RELATION_ORACLE,
    REPOSITORY_ROOT,
    _load_json,
)


STATIC_FIXTURE = (
    REPOSITORY_ROOT / "conformance/fixtures/godj-relation-not-implemented.json"
)


def _decode(value: dict[str, Any]) -> Any:
    kind = value["type"]
    if kind == "null":
        return None
    if kind == "bool":
        return value["value"]
    if kind == "int":
        return int(value["value"])
    if kind == "string":
        return value["value"]
    if kind == "pk":
        return _decode(value["value"])
    if kind == "list":
        return [_decode(item) for item in value["items"]]
    if kind == "object":
        return {
            field["name"]: _decode(field["value"])
            for field in value["fields"]
        }
    raise AssertionError(f"unsupported relation test value kind {kind!r}")


def _decoded_observation(observation: dict[str, Any]) -> dict[str, Any]:
    return {
        **observation,
        "result": (
            _decode(observation["result"])
            if observation["result"] is not None
            else None
        ),
        "db_state": (
            _decode(observation["db_state"])
            if observation["db_state"] is not None
            else None
        ),
        "metrics": (
            _decode(observation["metrics"])
            if observation["metrics"] is not None
            else None
        ),
    }


class RelationScenarioTests(unittest.TestCase):
    expected_names = [
        "django.relation.cross_app_metadata",
        "django.relation.unsaved_related_target",
        "django.relation.forward_lazy_cache",
        "django.relation.forward_lookup_join_reuse",
        "django.relation.reverse_accessor_and_lookup",
        "django.relation.nullable_access_and_isnull",
        "django.relation.protect_delete",
        "django.relation.set_null_delete",
        "django.relation.required_select_related",
        "django.relation.nullable_select_related",
        "django.relation.invalid_reverse_select_related",
        "django.relation.reverse_prefetch",
    ]
    expected_phases = [
        "metadata",
        "evaluation",
        "evaluation",
        "evaluation",
        "evaluation",
        "evaluation",
        "evaluation",
        "commit",
        "evaluation",
        "evaluation",
        "evaluation",
        "evaluation",
    ]

    def run_scenarios(self) -> dict[str, dict[str, Any]]:
        observations: dict[str, dict[str, Any]] = {}
        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 1):
            contract_id = f"REL-{number:03d}"
            observation = scenario(contract_id)
            self.assertEqual(observation["id"], contract_id)
            self.assertEqual(observation["status"], "observed")
            self.assertEqual(observation["phase"], self.expected_phases[number - 1])
            self.assertNotEqual(
                observation["result"] is None,
                observation["error"] is None,
            )
            observations[contract_id] = _decoded_observation(observation)
        return observations

    def test_registry_manifest_and_static_fixture_lock_exact_order(self) -> None:
        manifest = _load_json(DEFAULT_RELATION_MANIFEST)
        static = _load_json(STATIC_FIXTURE)
        identifiers = [f"REL-{number:03d}" for number in range(1, 13)]

        self.assertEqual(list(scenarios.SCENARIOS), self.expected_names)
        self.assertEqual(
            [contract["id"] for contract in manifest["contracts"]], identifiers
        )
        self.assertEqual(
            [contract["scenario"] for contract in manifest["contracts"]],
            self.expected_names,
        )
        self.assertEqual(
            [contract["phase"] for contract in manifest["contracts"]],
            self.expected_phases,
        )
        self.assertEqual(
            [contract["status"] for contract in manifest["contracts"]],
            [
                "passing",
                "oracle_locked",
                "passing",
                "passing",
                "passing",
                "passing",
            ]
            + ["oracle_locked"] * 5
            + ["passing"],
        )
        self.assertEqual(
            [contract["id"] for contract in static["contracts"]], identifiers
        )
        self.assertEqual(
            [contract["status"] for contract in static["contracts"]],
            ["not_implemented"] * 12,
        )
        self.assertEqual(
            [contract["phase"] for contract in static["contracts"]],
            self.expected_phases,
        )

    def test_manifest_provenance_is_pinned_independent_and_licensed(self) -> None:
        manifest = _load_json(DEFAULT_RELATION_MANIFEST)
        references = []
        for contract in manifest["contracts"]:
            self.assertTrue(contract["provenance"])
            for provenance in contract["provenance"]:
                self.assertFalse(provenance["derived"])
                self.assertEqual(provenance["license"], "BSD-3-Clause")
                reference = provenance["reference"]
                self.assertTrue(
                    reference.startswith(
                        "django@fe0a859f537d4238cf49fca39073513206f83122:"
                    )
                )
                references.append(reference)

        for marker in (
            "docs/ref/models/fields.txt",
            "docs/topics/db/examples/many_to_one.txt",
            "docs/topics/db/queries.txt",
            "tests/delete/tests.py",
            "docs/ref/models/querysets.txt#select-related",
            "docs/ref/models/querysets.txt#prefetch-related",
        ):
            self.assertTrue(
                any(marker in reference for reference in references),
                marker,
            )

    def test_exact_relation_results_errors_state_and_metrics(self) -> None:
        observations = self.run_scenarios()

        metadata = observations["REL-001"]["result"]
        self.assertEqual(
            metadata["forward"],
            [
                {
                    "name": "author",
                    "column": "author_id",
                    "target": {"app": "authors", "model": "author"},
                    "nullable": False,
                    "reverse": "posts",
                    "many_to_one": True,
                    "on_delete": "PROTECT",
                },
                {
                    "name": "reviewer",
                    "column": "reviewer_id",
                    "target": {"app": "authors", "model": "author"},
                    "nullable": True,
                    "reverse": "reviewed_posts",
                    "many_to_one": True,
                    "on_delete": "SET_NULL",
                },
            ],
        )
        self.assertEqual(
            metadata["reverse"],
            [
                {
                    "name": "posts",
                    "field": "author",
                    "target": {"app": "blog", "model": "post"},
                    "one_to_many": True,
                },
                {
                    "name": "reviewed_posts",
                    "field": "reviewer",
                    "target": {"app": "blog", "model": "post"},
                    "one_to_many": True,
                },
            ],
        )

        unsaved = observations["REL-002"]
        self.assertEqual(
            {
                "category": unsaved["error"]["category"],
                "code": unsaved["error"]["code"],
            },
            {
                "category": "model_state_error",
                "code": "unsaved_related_object",
            },
        )
        self.assertFalse(unsaved["error"]["message_is_contract"])
        self.assertEqual(unsaved["metrics"]["query_count"], 0)
        self.assertEqual(unsaved["metrics"]["row_delta"], {"authors": 0, "posts": 0})

        cache = observations["REL-003"]
        self.assertEqual(cache["result"]["cold"], {"id": 1, "name": "Ada"})
        self.assertEqual(cache["result"]["cold"], cache["result"]["warm"])
        self.assertEqual(
            [step["query_count"] for step in cache["metrics"]["steps"]],
            [1, 0],
        )

        forward = observations["REL-004"]
        self.assertEqual(
            [case["post_ids"] for case in forward["result"]["cases"]],
            [[10, 11], [10, 11]],
        )
        for case in forward["metrics"]["cases"]:
            self.assertEqual(case["construction"]["query_count"], 0)
            self.assertEqual(case["evaluation"]["query_count"], 1)
            self.assertEqual(case["evaluation"]["join_kinds"], ["INNER"])

        reverse = observations["REL-005"]
        self.assertEqual(reverse["result"]["accessor_post_ids"], [10, 11])
        self.assertEqual(reverse["result"]["lookup_author_ids"], [1])
        self.assertEqual(reverse["metrics"]["accessor"]["join_kinds"], [])
        self.assertEqual(reverse["metrics"]["lookup"]["join_kinds"], ["INNER"])

        nullable = observations["REL-006"]
        self.assertIsNone(nullable["result"]["reviewer"])
        self.assertEqual(nullable["result"]["isnull_post_ids"], [11])
        self.assertEqual(nullable["metrics"]["null_access"]["query_count"], 0)
        self.assertEqual(nullable["metrics"]["isnull_evaluation"]["join_kinds"], [])

        protected = observations["REL-007"]
        self.assertEqual(
            {
                "category": protected["error"]["category"],
                "code": protected["error"]["code"],
            },
            {
                "category": "integrity_error",
                "code": "protected_foreign_key",
            },
        )
        self.assertEqual(
            set(protected["metrics"]),
            {
                "update_statement_count",
                "delete_statement_count",
                "protected_source_rows",
            },
        )
        self.assertEqual(protected["metrics"]["protected_source_rows"], 2)
        self.assertEqual(protected["metrics"]["update_statement_count"], 0)
        self.assertEqual(protected["metrics"]["delete_statement_count"], 0)

        set_null = observations["REL-008"]
        self.assertEqual(
            set_null["result"], {"deleted_total": 1, "target_deleted": 1}
        )
        self.assertEqual(
            set(set_null["metrics"]),
            {
                "transaction_count",
                "mutation_order",
                "mutation_rows",
                "update_statement_count",
                "delete_statement_count",
                "affected_source_rows",
                "deleted_target_rows",
            },
        )
        self.assertEqual(set_null["metrics"]["transaction_count"], 1)
        self.assertEqual(set_null["metrics"]["mutation_order"], ["UPDATE", "DELETE"])
        self.assertEqual(set_null["metrics"]["affected_source_rows"], 2)
        self.assertEqual(set_null["metrics"]["deleted_target_rows"], 1)
        self.assertEqual(
            [author["id"] for author in set_null["db_state"]["authors"]],
            [1, 3],
        )
        self.assertEqual(
            [post["reviewer_id"] for post in set_null["db_state"]["posts"]],
            [None, None, None],
        )

        required = observations["REL-009"]
        expected_required = [[10, "Ada"], [11, "Ada"], [12, "Cleo"]]
        self.assertEqual(required["result"]["plain"], expected_required)
        self.assertEqual(required["result"]["eager"], expected_required)
        self.assertEqual(required["metrics"]["plain"]["query_count"], 4)
        self.assertEqual(required["metrics"]["eager"]["join_kinds"], ["INNER"])
        self.assertEqual(required["metrics"]["eager"]["access_extra_queries"], 0)

        nullable_eager = observations["REL-010"]
        self.assertEqual(
            nullable_eager["result"]["rows"],
            [[10, "Bob"], [11, None], [12, "Bob"]],
        )
        self.assertEqual(nullable_eager["metrics"]["query_count"], 1)
        self.assertEqual(nullable_eager["metrics"]["join_kinds"], ["LEFT_OUTER"])
        self.assertEqual(nullable_eager["metrics"]["access_extra_queries"], 0)

        invalid = observations["REL-011"]
        self.assertEqual(
            {
                "category": invalid["error"]["category"],
                "code": invalid["error"]["code"],
            },
            {"category": "field_error", "code": "invalid_related_path"},
        )
        self.assertEqual(invalid["metrics"]["query_count"], 0)
        self.assertEqual(invalid["metrics"]["mutation_count"], 0)

        prefetch = observations["REL-012"]
        self.assertEqual(
            prefetch["result"]["authors"],
            [[1, [10, 11]], [2, []], [3, [12]]],
        )
        self.assertEqual(prefetch["metrics"]["query_count"], 2)
        self.assertEqual(prefetch["metrics"]["batch_key_count"], 3)
        self.assertEqual(prefetch["metrics"]["batch_predicate_column"], "author_id")
        self.assertEqual(prefetch["metrics"]["join_kinds"], [])
        self.assertEqual(prefetch["metrics"]["related_access_extra_queries"], 0)

    def test_baseline_database_state_is_explicitly_ordered_and_isolated(self) -> None:
        observations = self.run_scenarios()
        baseline_ids = [1, 2, 3]
        baseline_post_ids = [10, 11, 12]
        for contract_id, observation in observations.items():
            state = observation["db_state"]
            if state is None:
                self.assertEqual(contract_id, "REL-001")
                continue
            with self.subTest(contract=contract_id):
                expected_author_ids = [1, 3] if contract_id == "REL-008" else baseline_ids
                self.assertEqual(
                    [author["id"] for author in state["authors"]],
                    expected_author_ids,
                )
                self.assertEqual(
                    [post["id"] for post in state["posts"]], baseline_post_ids
                )
        self.assertEqual(connection.introspection.table_names(), [])
        self.assertFalse(connection.in_atomic_block)
        self.assertFalse(connection.needs_rollback)
        self.assertTrue(connection.get_autocommit())

    def test_capture_windows_are_executed_for_every_non_metadata_contract(self) -> None:
        expected_windows = [0, 1, 2, 4, 2, 3, 1, 1, 4, 2, 1, 2]
        original = scenarios._StatementCapture.run

        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 1):
            windows: list[list[str]] = []

            def instrument(capture, operation):
                try:
                    return original(capture, operation)
                finally:
                    windows.append(
                        [statement.kind for statement in capture.statements]
                    )

            with (
                self.subTest(contract=f"REL-{number:03d}"),
                patch.object(scenarios._StatementCapture, "run", instrument),
            ):
                scenario(f"REL-{number:03d}")
            self.assertEqual(len(windows), expected_windows[number - 1])

    def test_raw_sql_sentinel_independently_confirms_join_and_mutation_shape(self) -> None:
        expectations = {
            "REL-004": (2, 0),
            "REL-009": (1, 0),
            "REL-010": (0, 1),
            "REL-012": (0, 0),
        }
        for contract_id, (inner_count, left_count) in expectations.items():
            statements: list[str] = []

            def capture(execute, sql, params, many, context):
                statements.append(sql)
                return execute(sql, params, many, context)

            scenario = list(scenarios.SCENARIOS.values())[int(contract_id[-3:]) - 1]
            with self.subTest(contract=contract_id), connection.execute_wrapper(capture):
                scenario(contract_id)
            rendered = "\n".join(statements).upper()
            self.assertEqual(len(re.findall(r"\bINNER\s+JOIN\b", rendered)), inner_count)
            self.assertEqual(
                len(re.findall(r"\bLEFT\s+OUTER\s+JOIN\b", rendered)),
                left_count,
            )
            if contract_id == "REL-012":
                unquoted = re.sub(r'["`\[\]]', "", rendered)
                self.assertEqual(
                    len(re.findall(r"\bAUTHOR_ID\b\s+IN\s*\(", unquoted)),
                    1,
                )

        mutation_order: list[str] = []

        def capture_mutation(execute, sql, params, many, context):
            result = execute(sql, params, many, context)
            kind = sql.lstrip().split(None, 1)[0].upper() if sql.strip() else ""
            if kind in {"UPDATE", "DELETE"}:
                mutation_order.append(kind)
            return result

        with connection.execute_wrapper(capture_mutation):
            scenarios.set_null_delete("REL-008")
        self.assertEqual(mutation_order, ["UPDATE", "DELETE"])

    def test_fixture_and_contract_mutations_cannot_replay_expected_constants(self) -> None:
        baseline_forward = canonical_json(
            scenarios.forward_lookup_join_reuse("REL-004")
        )
        baseline_reverse = canonical_json(
            scenarios.reverse_accessor_and_lookup("REL-005")
        )

        changed_authors = (
            replace(relation_fixture.AUTHOR_FIXTURES[0], name="Mutation Ada"),
            *relation_fixture.AUTHOR_FIXTURES[1:],
        )
        with patch.object(relation_fixture, "AUTHOR_FIXTURES", changed_authors):
            mutated_cache = canonical_json(scenarios.forward_lazy_cache("REL-003"))
        original_cache = canonical_json(scenarios.forward_lazy_cache("REL-003"))
        self.assertNotEqual(mutated_cache, original_cache)

        changed_posts = (
            replace(relation_fixture.POST_FIXTURES[0], title="Mutation Alpha"),
            *relation_fixture.POST_FIXTURES[1:],
        )
        with patch.object(relation_fixture, "POST_FIXTURES", changed_posts):
            self.assertNotEqual(
                canonical_json(scenarios.reverse_accessor_and_lookup("REL-005")),
                baseline_reverse,
            )

        changed_post_id = (
            replace(relation_fixture.POST_FIXTURES[0], id=110),
            *relation_fixture.POST_FIXTURES[1:],
        )
        with patch.object(relation_fixture, "POST_FIXTURES", changed_post_id):
            self.assertNotEqual(
                canonical_json(scenarios.reverse_accessor_and_lookup("REL-005")),
                baseline_reverse,
            )

        changed_name = replace(
            relation_fixture.RELATION_DEFINITION,
            author_related_name="authored_posts",
        )
        with patch.object(relation_fixture, "RELATION_DEFINITION", changed_name):
            with self.assertRaises(FieldDoesNotExist):
                scenarios.cross_app_metadata("REL-001")

        with patch.object(scenarios, "AUTHOR_NAME_PREDICATE", "Missing"):
            self.assertNotEqual(
                canonical_json(scenarios.forward_lookup_join_reuse("REL-004")),
                baseline_forward,
            )

        missing_target = replace(
            relation_fixture.RELATION_DEFINITION,
            target="authors.Missing",
        )
        with patch.object(relation_fixture, "RELATION_DEFINITION", missing_target):
            with self.assertRaisesRegex(ValueError, "cannot be resolved"):
                scenarios.cross_app_metadata("REL-001")

        required_reviewer = replace(
            relation_fixture.RELATION_DEFINITION,
            reviewer_nullable=False,
        )
        with patch.object(relation_fixture, "RELATION_DEFINITION", required_reviewer):
            with self.assertRaises(IntegrityError):
                scenarios.nullable_access_and_isnull("REL-006")

        cascading_author = replace(
            relation_fixture.RELATION_DEFINITION,
            author_on_delete=models.CASCADE,
        )
        with patch.object(relation_fixture, "RELATION_DEFINITION", cascading_author):
            with self.assertRaisesRegex(AssertionError, "must fail"):
                scenarios.protect_delete("REL-007")

        unordered_posts = replace(
            relation_fixture.RELATION_DEFINITION,
            post_ordering=(),
        )
        with patch.object(
            relation_fixture,
            "RELATION_DEFINITION",
            unordered_posts,
        ):
            with self.assertRaisesRegex(AssertionError, "total post id ordering"):
                scenarios.reverse_prefetch("REL-012")

    def test_sql_shape_parser_mutation_is_rejected_by_live_scenario_assertion(self) -> None:
        original = scenarios.normalize_sql_shape

        def drop_joins(sql: str) -> dict[str, Any]:
            shape = original(sql)
            return {**shape, "join_kinds": []}

        with patch.object(scenarios, "normalize_sql_shape", side_effect=drop_joins):
            with self.assertRaisesRegex(AssertionError, "unexpected relation SQL shape"):
                scenarios.forward_lookup_join_reuse("REL-004")

    def test_scenarios_do_not_read_oracle_or_static_fixture(self) -> None:
        with (
            patch.object(Path, "read_bytes", side_effect=AssertionError("artifact read")),
            patch.object(Path, "read_text", side_effect=AssertionError("artifact read")),
        ):
            self.run_scenarios()

    def test_two_process_oracle_bytes_ignore_python_hash_seed(self) -> None:
        script = """
import sys
from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import (
    DEFAULT_PROFILE,
    DEFAULT_RELATION_MANIFEST,
    _load_json,
    _run_contract,
    _validate_manifest_basics,
)
profile = _load_json(DEFAULT_PROFILE)
manifest = _load_json(DEFAULT_RELATION_MANIFEST)
contracts = _validate_manifest_basics(manifest, profile)
suite = {
    "format_version": 2,
    "profile": {
        "id": profile["id"],
        "fingerprint": profile["fingerprint"],
        "lock": profile["lock"],
    },
    "contracts": [_run_contract(contract) for contract in contracts],
}
sys.stdout.buffer.write(canonical_json(suite))
"""
        outputs = []
        for seed in ("1", "8675309"):
            environment = os.environ.copy()
            environment["PYTHONHASHSEED"] = seed
            completed = subprocess.run(
                [sys.executable, "-c", script],
                cwd=REPOSITORY_ROOT,
                env=environment,
                check=False,
                capture_output=True,
                timeout=30,
            )
            self.assertEqual(completed.returncode, 0, completed.stderr.decode())
            outputs.append(completed.stdout)
        self.assertEqual(outputs[0], outputs[1])
        self.assertEqual(outputs[0], DEFAULT_RELATION_ORACLE.read_bytes())

    def test_portable_runtime_uses_django_61_and_actual_sqlite(self) -> None:
        import django

        self.assertEqual(django.get_version(), "6.1")
        self.assertEqual(connection.vendor, "sqlite")


if __name__ == "__main__":
    unittest.main()
