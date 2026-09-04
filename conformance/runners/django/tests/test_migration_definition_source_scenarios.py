from __future__ import annotations

import ast
import hashlib
import inspect
import json
import os
import subprocess
import sys
import tempfile
import unittest
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

from django.db import connections
from django.db.migrations.executor import MigrationExecutor

from conformance.runners.django import (
    migration_definition_source_scenarios as scenarios,
)


ROOT = Path(__file__).resolve().parents[4]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = (
    ROOT / "conformance/contracts/migration-definition-source-manifest.json"
)
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64"
    / "migration-definition-source-oracle.json"
)
STATIC = (
    ROOT
    / "conformance/fixtures"
    / "godj-migration-definition-source-not-implemented.json"
)


def denormalize(value):
    value_type = value["type"]
    if value_type == "null":
        return None
    if value_type in {"bool", "string"}:
        return value["value"]
    if value_type == "int":
        return int(value["value"])
    if value_type == "list":
        return [denormalize(item) for item in value["items"]]
    if value_type == "object":
        return {
            field["name"]: denormalize(field["value"])
            for field in value["fields"]
        }
    raise AssertionError(f"unexpected normalized value type: {value_type!r}")


def observed(scenario, contract_id):
    observation = scenario(contract_id)
    return {
        "db": (
            denormalize(observation["db_state"])
            if observation["db_state"] is not None
            else None
        ),
        "error": observation["error"],
        "metrics": denormalize(observation["metrics"]),
        "raw": observation,
        "result": (
            denormalize(observation["result"])
            if observation["result"] is not None
            else None
        ),
    }


def load_error(sources):
    try:
        scenarios._load(sources)
    except scenarios._DefinitionSourceError as error:
        return error
    raise AssertionError("definition source unexpectedly loaded")


def source(source_id, value, *, pretty=False, sort_keys=False):
    return scenarios.SourceDocument(
        source_id,
        scenarios._encode_document(
            value,
            pretty=pretty,
            sort_keys=sort_keys,
        ),
    )


class MigrationDefinitionSourceScenarioTests(unittest.TestCase):
    def assert_atomic_failure(self, error, *, code, stage, reason):
        self.assertEqual(error.code, code)
        metrics = error.metrics
        self.assertEqual(metrics["definition_sets_published"], 0)
        self.assertEqual(metrics["definitions_published"], 0)
        self.assertEqual(metrics["session_open_calls"], 0)
        self.assertEqual(metrics["source_reads_after_snapshot"], 0)
        self.assertEqual(metrics["failure"]["stage"], stage)
        self.assertEqual(metrics["failure"]["reason"], reason)
        self.assertEqual(
            set(metrics["failure"]),
            {
                "app",
                "json_pointer",
                "name",
                "operation_index",
                "reason",
                "source_id",
                "stage",
            },
        )

    def test_registry_order_matches_mig_057_through_064(self) -> None:
        self.assertEqual(
            list(scenarios.SCENARIOS),
            [
                "godj.migration.definition_source.canonical_batch",
                "godj.migration.definition_source.empty_source",
                "godj.migration.definition_source.canonical_syntax_and_order",
                "godj.migration.definition_source.unsupported_format",
                "godj.migration.definition_source.malformed_atomic_batch",
                "godj.migration.definition_source.duplicate_identity",
                "godj.migration.definition_source.closed_codec",
                "django.migration.definition_source.public_loaded_executor",
            ],
        )

    def test_manifest_locks_mapping_phases_comparisons_and_provenance(self) -> None:
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        self.assertEqual(manifest["format_version"], 2)
        self.assertEqual(
            manifest["profile_id"],
            "django-6.1-sqlite-darwin-arm64",
        )
        self.assertEqual(
            [contract["id"] for contract in manifest["contracts"]],
            [f"MIG-{number:03d}" for number in range(57, 65)],
        )
        self.assertEqual(
            [contract["scenario"] for contract in manifest["contracts"]],
            list(scenarios.SCENARIOS),
        )
        self.assertEqual(
            [contract["phase"] for contract in manifest["contracts"]],
            [
                "construction",
                "construction",
                "construction",
                "environment",
                "construction",
                "construction",
                "construction",
                "commit",
            ],
        )
        self.assertEqual(
            [contract["comparison"] for contract in manifest["contracts"]],
            [
                ["result", "metrics"],
                ["result", "metrics"],
                ["result", "metrics"],
                ["error", "metrics"],
                ["error", "metrics"],
                ["error", "metrics"],
                ["error", "metrics"],
                ["result", "db_state", "metrics"],
            ],
        )
        for contract in manifest["contracts"]:
            with self.subTest(contract=contract["id"]):
                self.assertEqual(contract["status"], "passing")
                self.assertIn(
                    {
                        "kind": "decision",
                        "reference": "ADR-0035",
                        "derived": False,
                    },
                    contract["provenance"],
                )
                for provenance in contract["provenance"]:
                    if provenance["kind"] == "decision":
                        self.assertNotIn("license", provenance)
                        continue
                    self.assertIn(
                        "django@fe0a859f537d4238cf49fca39073513206f83122:",
                        provenance["reference"],
                    )
                    self.assertEqual(provenance["license"], "BSD-3-Clause")
                    self.assertIs(provenance["derived"], False)

        django_provenance = {
            contract["id"]
            for contract in manifest["contracts"]
            if any(
                item["kind"] != "decision" for item in contract["provenance"]
            )
        }
        self.assertEqual(django_provenance, {"MIG-057", "MIG-064"})

    def test_static_fixture_is_explicitly_not_implemented(self) -> None:
        fixture = json.loads(STATIC.read_text(encoding="utf-8"))
        self.assertEqual(fixture["format_version"], 2)
        self.assertEqual(
            fixture["profile"]["id"],
            "django-6.1-sqlite-darwin-arm64",
        )
        self.assertEqual(
            [item["id"] for item in fixture["contracts"]],
            [f"MIG-{number:03d}" for number in range(57, 65)],
        )
        self.assertEqual(
            {item["status"] for item in fixture["contracts"]},
            {"not_implemented"},
        )
        self.assertEqual(
            [item["phase"] for item in fixture["contracts"]],
            [
                "construction",
                "construction",
                "construction",
                "environment",
                "construction",
                "construction",
                "construction",
                "commit",
            ],
        )

    def test_every_public_scenario_is_independent_of_contract_id(self) -> None:
        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 57):
            with self.subTest(scenario=scenario.__name__):
                expected = scenario(f"MIG-{number:03d}")
                arbitrary = scenario("untrusted-contract")
                self.assertEqual(expected["id"], f"MIG-{number:03d}")
                self.assertEqual(arbitrary["id"], "untrusted-contract")
                self.assertEqual(
                    {key: value for key, value in expected.items() if key != "id"},
                    {
                        key: value
                        for key, value in arbitrary.items()
                        if key != "id"
                    },
                )
        self.assertNotIn(scenarios._DATABASE_ALIAS, connections.databases)

    def test_scenario_source_has_no_contract_or_artifact_dispatch_and_no_discovery(
        self,
    ) -> None:
        text = inspect.getsource(scenarios)
        syntax = ast.parse(text)
        called_attributes = {
            node.func.attr
            for node in ast.walk(syntax)
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
        }
        called_names = {
            node.func.id
            for node in ast.walk(syntax)
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
        }
        for forbidden in {
            "MIG-",
            "migration-definition-source-oracle",
            "godj-migration-definition-source-not-implemented",
            "MigrationWriter",
            "OperationWriter",
            "import_module",
            "pkgutil",
        }:
            self.assertNotIn(forbidden, text)
        self.assertTrue(
            {"check_consistent_history", "migration_plan", "migrate"}
            <= called_attributes
        )
        self.assertTrue(
            {
                "open",
                "read_bytes",
                "read_text",
                "write_bytes",
                "write_text",
            }.isdisjoint(called_attributes | called_names)
        )
        self.assertTrue({"eval", "exec", "compile"}.isdisjoint(called_names))

    def test_canonical_batch_is_lossless_sorted_and_source_diagnostic(self) -> None:
        value = observed(scenarios.canonical_batch, "MIG-057")
        result = value["result"]
        metrics = value["metrics"]
        definitions = result["definition_set"]["definitions"]

        self.assertEqual(
            [(item["app"], item["name"]) for item in definitions],
            [("alpha", "0001_initial"), ("alpha", "0002_fields")],
        )
        self.assertEqual(
            [item["kind"] for item in definitions[1]["operations"]],
            ["add_field", "add_field"],
        )
        self.assertEqual(
            definitions[1]["operations"][0]["field"]["default"],
            {"boolean": False, "kind": "boolean"},
        )
        self.assertRegex(
            result["definition_set"]["digest"],
            r"^sha256:[0-9a-f]{64}$",
        )
        self.assertEqual(
            [item["source_id"] for item in result["sources"]],
            ["opaque-a-tail", "opaque-z-root"],
        )
        self.assertEqual(
            [item["producer"] for item in result["sources"]],
            [
                {"name": "godj-reference", "version": "0.1.0"},
                {"name": "godj-reference", "version": "0.1.0"},
            ],
        )
        self.assertEqual(result["execution"]["session_open_calls"], 0)
        self.assertFalse(result["execution"]["attempted"])
        self.assertIsNone(result["execution"]["observed_digest"])
        self.assertEqual(
            result["format"],
            {"definition_format": 1, "schema_ir": 1, "state_format": 1},
        )
        self.assertEqual(metrics["documents_received"], 2)
        self.assertEqual(metrics["headers_validated"], 2)
        self.assertEqual(metrics["operations_decoded"], 3)
        self.assertEqual(metrics["definitions_published"], 2)
        self.assertEqual(metrics["definition_sets_published"], 1)
        self.assertIsNone(metrics["failure"])

    def test_source_relabel_input_order_and_producer_do_not_change_semantics(
        self,
    ) -> None:
        baseline, _ = scenarios._load(scenarios._fixture_sources())
        root = scenarios._root_document()
        tail = scenarios._tail_document()
        root["producer"]["name"] = "another-generator"
        root["producer"]["version"] = "99.0.0"
        relabeled, _ = scenarios._load(
            (
                source("zz-tail", tail, pretty=True, sort_keys=False),
                source("aa-root", root, pretty=True, sort_keys=True),
            )
        )
        self.assertEqual(baseline.definitions, relabeled.definitions)
        self.assertEqual(baseline.digest, relabeled.digest)
        self.assertNotEqual(baseline.sources, relabeled.sources)
        self.assertEqual(
            [(item["app"], item["name"]) for item in baseline.sources],
            [(item["app"], item["name"]) for item in relabeled.sources[::-1]],
        )

    def test_empty_source_and_one_create_model_golden_digest_vectors(self) -> None:
        empty = observed(scenarios.empty_source, "MIG-058")
        self.assertEqual(
            empty["result"]["definition_set"],
            {
                "definitions": [],
                "digest": scenarios._EMPTY_DIGEST,
            },
        )
        self.assertEqual(empty["metrics"]["definitions_published"], 0)
        self.assertEqual(empty["metrics"]["definition_sets_published"], 1)
        self.assertEqual(empty["metrics"]["documents_received"], 0)
        self.assertIsNone(empty["metrics"]["failure"])

        golden = scenarios._root_document()
        golden["migration"]["operations"][0]["model"] = {
            "db_table": "alpha_widget",
            "fields": [scenarios._auto_field()],
            "go_name": "Widget",
            "name": "widget",
        }
        loaded, _ = scenarios._load((source("golden", golden),))
        canonical = scenarios._canonical_json(
            {
                "definitions": list(loaded.definitions),
                "domain": scenarios._DIGEST_DOMAIN,
                "format_version": scenarios._FORMAT_VERSION,
            }
        )
        self.assertEqual(len(canonical), 400)
        self.assertEqual(
            loaded.digest,
            "sha256:b15b980386317e4c75746910d01bf5492876a5eb31a2ed3f560722866c15a1b6",
        )
        self.assertEqual(
            loaded.digest,
            "sha256:" + hashlib.sha256(canonical).hexdigest(),
        )

    def test_equivalent_syntax_and_operation_order_matrix(self) -> None:
        value = observed(scenarios.canonical_syntax_and_order, "MIG-059")
        result = value["result"]
        digest = result["definition_set"]["digest"]
        canonicality = result["canonicality"]
        self.assertTrue(canonicality["equivalent_definition_set"])
        self.assertTrue(canonicality["source_relabel_preserved_digest"])
        self.assertEqual(canonicality["equivalent_digest"], digest)
        self.assertTrue(canonicality["operation_order_is_semantic"])
        self.assertNotEqual(
            canonicality["operation_order_changed_digest"],
            digest,
        )

    def test_current_format_precedes_semantic_decode(self) -> None:
        base = scenarios._root_document()
        for value in (0, scenarios._FORMAT_VERSION + 1):
            with self.subTest(value=value):
                document = deepcopy(base)
                document["format_version"] = value
                document["migration"]["operations"] = [
                    {"app_label": "alpha", "kind": "run_python"}
                ]
                error = load_error((source("version", document),))
                self.assert_atomic_failure(
                    error,
                    code="definition_format_incompatible",
                    stage="format",
                    reason="format_version",
                )
                self.assertEqual(error.metrics["operations_decoded"], 0)

        first_document = deepcopy(base)
        first_document["format_version"] = 2
        second_document = deepcopy(base)
        second_document["format_version"] = 3
        for inputs in (
            (
                source("z-format", first_document),
                source("a-format", second_document),
            ),
            (
                source("a-format", second_document),
                source("z-format", first_document),
            ),
        ):
            error = load_error(inputs)
            self.assertEqual(error.code, "definition_format_incompatible")
            self.assertEqual(error.metrics["failure"]["source_id"], "a-format")

        for value in (-(1 << 63) - 1, 1 << 63):
            with self.subTest(out_of_range=value):
                document = deepcopy(base)
                document["format_version"] = value
                error = load_error((source("range", document),))
                self.assert_atomic_failure(
                    error,
                    code="invalid_definition_document",
                    stage="document",
                    reason="out_of_range",
                )
                self.assertEqual(
                    error.metrics["failure"]["json_pointer"],
                    "/format_version",
                )

    def test_nested_max_length_int64_framing_precedes_format_check(self) -> None:
        create_model = scenarios._root_document()
        create_model["format_version"] = 2
        create_model["migration"]["operations"][0]["model"]["fields"][1][
            "max_length"
        ] = 1 << 63

        add_field = scenarios._tail_document()
        add_field["format_version"] = 2
        add_field["migration"]["operations"][0]["field"]["max_length"] = (
            -(1 << 63) - 1
        )

        cases = (
            (
                create_model,
                "/migration/operations/0/model/fields/1/max_length",
            ),
            (add_field, "/migration/operations/0/field/max_length"),
        )
        for document, pointer in cases:
            with self.subTest(pointer=pointer):
                error = load_error((source("integer-domain", document),))
                self.assert_atomic_failure(
                    error,
                    code="invalid_definition_document",
                    stage="document",
                    reason="out_of_range",
                )
                self.assertEqual(
                    error.metrics["failure"]["json_pointer"],
                    pointer,
                )
                self.assertEqual(error.metrics["headers_validated"], 0)
                self.assertEqual(error.metrics["operations_decoded"], 0)

        semantic_range = scenarios._root_document()
        semantic_range["migration"]["operations"][0]["model"]["fields"][1][
            "max_length"
        ] = 1 << 31
        error = load_error((source("semantic-range", semantic_range),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_document",
            stage="semantic",
            reason="out_of_range",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/operations/0/model/fields/1/max_length",
        )

    def test_empty_producer_framing_precedes_format_check(self) -> None:
        for member in ("name", "version"):
            with self.subTest(member=member):
                document = scenarios._root_document()
                document["format_version"] = 2
                document["producer"][member] = ""
                error = load_error((source("empty-producer", document),))
                self.assert_atomic_failure(
                    error,
                    code="invalid_definition_document",
                    stage="document",
                    reason="wrong_type",
                )
                self.assertEqual(
                    error.metrics["failure"]["json_pointer"],
                    f"/producer/{member}",
                )
                self.assertEqual(error.metrics["headers_validated"], 0)
                self.assertEqual(error.metrics["operations_decoded"], 0)

    def test_unsupported_payload_is_not_a_known_wire_arm(self) -> None:
        unsupported = scenarios._root_document()
        unsupported["migration"]["operations"] = [
            {
                "field": {"aaa": True, "max_length": 1 << 63},
                "kind": "run_python",
            }
        ]

        unsupported_format = deepcopy(unsupported)
        unsupported_format["format_version"] = 2
        error = load_error((source("unsupported-payload", unsupported_format),))
        self.assert_atomic_failure(
            error,
            code="definition_format_incompatible",
            stage="format",
            reason="format_version",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/format_version",
        )
        self.assertEqual(error.metrics["operations_decoded"], 0)

        error = load_error((source("unsupported-payload", unsupported),))
        self.assert_atomic_failure(
            error,
            code="unsupported_definition_operation",
            stage="semantic",
            reason="unsupported_operation",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/operations/0/kind",
        )
        self.assertEqual(error.metrics["operations_decoded"], 0)

    def test_base_failure_outcomes_have_exact_typed_failure_context(self) -> None:
        expected = [
            (
                scenarios.unsupported_format,
                "definition_format_incompatible",
                "format",
                "format_version",
                "a-version",
                "/format_version",
            ),
            (
                scenarios.malformed_atomic_batch,
                "invalid_definition_document",
                "document",
                "duplicate_key",
                "b-invalid",
                "/migration/name",
            ),
            (
                scenarios.duplicate_identity,
                "duplicate_node",
                "graph",
                "duplicate_node",
                "z-duplicate",
                "/migration",
            ),
            (
                scenarios.closed_codec,
                "unsupported_definition_operation",
                "semantic",
                "unsupported_operation",
                "a-operation",
                "/migration/operations/0/kind",
            ),
        ]
        for offset, item in enumerate(expected, 60):
            scenario, code, stage, reason, source_id, pointer = item
            with self.subTest(scenario=scenario.__name__):
                value = observed(scenario, f"MIG-{offset:03d}")
                self.assertIsNone(value["result"])
                self.assertEqual(value["error"]["code"], code)
                self.assertEqual(
                    set(value["error"]),
                    {"category", "code", "message_is_contract"},
                )
                self.assertFalse(value["error"]["message_is_contract"])
                failure = value["metrics"]["failure"]
                self.assertEqual(failure["stage"], stage)
                self.assertEqual(failure["reason"], reason)
                self.assertEqual(failure["source_id"], source_id)
                self.assertEqual(failure["json_pointer"], pointer)
                self.assertEqual(value["metrics"]["definitions_published"], 0)
                self.assertEqual(value["metrics"]["session_open_calls"], 0)

    def test_strict_document_framing_rejects_each_false_green_shape(self) -> None:
        root_bytes = scenarios._encode_document(scenarios._root_document())
        cases = {
            "invalid_utf8": b"\xff",
            "syntax": b'{"format_version":',
            "duplicate_key": root_bytes.replace(
                b'"name":"0001_initial"',
                b'"name":"0001_initial","name":"duplicate"',
                1,
            ),
            "lone_surrogate": root_bytes.replace(
                b'"godj-reference"',
                b'"\\ud800"',
                1,
            ),
            "trailing_value": root_bytes + b" {}",
        }
        for reason, document in cases.items():
            with self.subTest(reason=reason):
                error = load_error(
                    (scenarios.SourceDocument("fault", document),)
                )
                self.assert_atomic_failure(
                    error,
                    code="invalid_definition_document",
                    stage="document",
                    reason=reason,
                )

        unknown = scenarios._root_document()
        unknown["unknown"] = True
        error = load_error((source("fault", unknown),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_document",
            stage="document",
            reason="unknown_field",
        )

        missing = scenarios._root_document()
        del missing["producer"]
        error = load_error((source("fault", missing),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_document",
            stage="document",
            reason="missing_field",
        )

        exponent = root_bytes.replace(b'"max_length":64', b'"max_length":1e2')
        error = load_error((scenarios.SourceDocument("fault", exponent),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_document",
            stage="document",
            reason="wrong_type",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/operations/0/model/fields/1/max_length",
        )

        decimal_version = root_bytes.replace(
            b'"format_version":1',
            b'"format_version":1.0',
            1,
        )
        error = load_error(
            (scenarios.SourceDocument("fault", decimal_version),)
        )
        self.assert_atomic_failure(
            error,
            code="invalid_definition_document",
            stage="document",
            reason="wrong_type",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/format_version",
        )

        unsupported_format = scenarios._root_document()
        unsupported_format["format_version"] = 2
        combined = scenarios._encode_document(unsupported_format).replace(
            b'"max_length":64',
            b'"max_length":1e2',
            1,
        )
        error = load_error((scenarios.SourceDocument("fault", combined),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_document",
            stage="document",
            reason="wrong_type",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/operations/0/model/fields/1/max_length",
        )

        non_json_constant = root_bytes.replace(
            b'"max_length":64',
            b'"max_length":NaN',
            1,
        )
        error = load_error(
            (scenarios.SourceDocument("fault", non_json_constant),)
        )
        self.assert_atomic_failure(
            error,
            code="invalid_definition_document",
            stage="document",
            reason="syntax",
        )
        self.assertEqual(error.metrics["failure"]["json_pointer"], "")

        nested_first = root_bytes.replace(
            b'"format_version":1',
            b'"format_version":1,"format_version":2',
            1,
        ).replace(
            b"{",
            b'{"producer":{"name":"other","version":"1"},',
            1,
        )
        error = load_error(
            (scenarios.SourceDocument("fault", nested_first),)
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/format_version",
        )

        escaped_key = root_bytes.replace(
            b"{",
            b'{"a/b~c":1,"a/b~c":2,',
            1,
        )
        error = load_error((scenarios.SourceDocument("fault", escaped_key),))
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/a~1b~0c",
        )

    def test_document_stage_beats_version_and_is_source_order_stable(self) -> None:
        invalid = scenarios._encode_document(scenarios._tail_document()) + b" {}"
        version = scenarios._root_document()
        version["format_version"] = 2
        inputs = (
            scenarios.SourceDocument("a-document", invalid),
            source("z-version", version),
        )
        for batch in (inputs, tuple(reversed(inputs))):
            error = load_error(batch)
            self.assertEqual(error.code, "invalid_definition_document")
            self.assertEqual(error.metrics["failure"]["stage"], "document")
            self.assertEqual(
                error.metrics["failure"]["source_id"],
                "a-document",
            )

    def test_document_candidates_combine_tree_and_outer_shape_faults(
        self,
    ) -> None:
        base = scenarios._encode_document(scenarios._root_document())
        root_unknown_and_duplicate = base.replace(
            b"{",
            b'{"aaa":true,',
            1,
        ).replace(
            b'"name":"0001_initial"',
            b'"name":"0001_initial","name":"duplicate"',
            1,
        )
        error = load_error(
            (
                scenarios.SourceDocument(
                    "document-candidates",
                    root_unknown_and_duplicate,
                ),
            )
        )
        self.assert_atomic_failure(
            error,
            code="invalid_definition_document",
            stage="document",
            reason="unknown_field",
        )
        self.assertEqual(error.metrics["failure"]["json_pointer"], "/aaa")

        earlier_shape = scenarios._root_document()
        earlier_shape["migration"]["aaa"] = True
        shape_and_lone_surrogate = scenarios._encode_document(
            earlier_shape
        ).replace(b'"0.1.0"', b'"\\ud800"', 1)
        error = load_error(
            (
                scenarios.SourceDocument(
                    "document-candidates",
                    shape_and_lone_surrogate,
                ),
            )
        )
        self.assert_atomic_failure(
            error,
            code="invalid_definition_document",
            stage="document",
            reason="unknown_field",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/aaa",
        )

    def test_source_id_validation_and_duplicate_source_are_atomic(self) -> None:
        document = scenarios._encode_document(scenarios._root_document())
        cases = [
            (
                (scenarios.SourceDocument("", document),),
                "empty_source_id",
                "",
            ),
            (
                (scenarios.SourceDocument(b"\xff", document),),
                "invalid_source_id_utf8",
                "hex:ff",
            ),
            (
                (
                    scenarios.SourceDocument("same", document),
                    scenarios.SourceDocument("same", document),
                ),
                "duplicate_source_id",
                "same",
            ),
        ]
        for sources, reason, source_id in cases:
            with self.subTest(reason=reason):
                error = load_error(sources)
                self.assert_atomic_failure(
                    error,
                    code="invalid_definition_source",
                    stage="source",
                    reason=reason,
                )
                self.assertEqual(
                    error.metrics["failure"]["source_id"],
                    source_id,
                )

        mixed = (
            scenarios.SourceDocument(b"\xff", document),
            scenarios.SourceDocument("same", document),
            scenarios.SourceDocument("", document),
            scenarios.SourceDocument("same", document),
        )
        for batch in (mixed, tuple(reversed(mixed))):
            error = load_error(batch)
            self.assert_atomic_failure(
                error,
                code="invalid_definition_source",
                stage="source",
                reason="empty_source_id",
            )
            self.assertEqual(error.metrics["failure"]["source_id"], "")

    def test_duplicate_identity_never_last_wins_under_input_permutation(self) -> None:
        first = scenarios._root_document()
        second = scenarios._root_document()
        second["producer"]["version"] = "changed"
        inputs = (
            source("a-first", first),
            source("z-second", second),
        )
        for batch in (inputs, tuple(reversed(inputs))):
            error = load_error(batch)
            self.assert_atomic_failure(
                error,
                code="duplicate_node",
                stage="graph",
                reason="duplicate_node",
            )
            self.assertEqual(error.category, "migration_graph_error")
            self.assertEqual(error.metrics["failure"]["source_id"], "z-second")

    def test_broader_graph_diagnostics_are_left_to_new_planner_gate(self) -> None:
        missing = scenarios._tail_document()
        missing["migration"]["dependencies"] = [
            {"app": "missing", "name": "0001_absent"}
        ]
        with self.assertRaisesRegex(AssertionError, "NewPlanner test gate"):
            scenarios._load(
                (
                    source("root", scenarios._root_document()),
                    source("missing", missing),
                )
            )

    def test_closed_codec_rejects_executable_kinds_but_not_inert_strings(self) -> None:
        for kind in ("run_python", "run_sql", "custom_operation"):
            document = scenarios._root_document()
            document["migration"]["operations"] = [
                {"app_label": "alpha", "kind": kind}
            ]
            error = load_error((source("operation", document),))
            self.assert_atomic_failure(
                error,
                code="unsupported_definition_operation",
                stage="semantic",
                reason="unsupported_operation",
            )
            self.assertEqual(error.metrics["operations_decoded"], 0)

        for kind in (None, True, [], {}):
            with self.subTest(malformed_kind=kind):
                document = scenarios._root_document()
                document["migration"]["operations"] = [{"kind": kind}]
                error = load_error((source("malformed-kind", document),))
                self.assert_atomic_failure(
                    error,
                    code="invalid_definition_operation",
                    stage="semantic",
                    reason="invalid_operation",
                )
                self.assertEqual(
                    error.metrics["failure"]["json_pointer"],
                    "/migration/operations/0/kind",
                )
                self.assertEqual(error.metrics["operations_decoded"], 0)

        for operation in (
            {"aaa": True, "kind": "run_python"},
            {"kind": "run_python", "aaa": True},
        ):
            document = scenarios._root_document()
            document["migration"]["operations"] = [operation]
            error = load_error((source("operation-shape", document),))
            self.assert_atomic_failure(
                error,
                code="invalid_definition_operation",
                stage="semantic",
                reason="invalid_operation",
            )
            self.assertEqual(
                error.metrics["failure"]["json_pointer"],
                "/migration/operations/0/aaa",
            )
            self.assertEqual(error.metrics["operations_decoded"], 0)

        later_unknown = scenarios._root_document()
        later_unknown["migration"]["operations"] = [
            {"kind": "run_python", "zzz": True}
        ]
        error = load_error((source("operation-shape", later_unknown),))
        self.assert_atomic_failure(
            error,
            code="unsupported_definition_operation",
            stage="semantic",
            reason="unsupported_operation",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/operations/0/kind",
        )

        inert = scenarios._root_document()
        inert["migration"]["operations"][0]["model"]["fields"][1][
            "default"
        ] = {
            "kind": "string",
            "string": "print('still data'); import os; run_python",
        }
        loaded, _ = scenarios._load((source("inert", inert),))
        default = loaded.definitions[0]["operations"][0]["model"]["fields"][1][
            "default"
        ]
        self.assertEqual(
            default,
            inert["migration"]["operations"][0]["model"]["fields"][1][
                "default"
            ],
        )

    def test_semantic_candidates_sort_by_rfc6901_pointer_before_reason(
        self,
    ) -> None:
        cases = []

        field_invalid_before_missing = scenarios._root_document()
        field = field_invalid_before_missing["migration"]["operations"][0][
            "model"
        ]["fields"][1]
        field["column"] = "Invalid-Column"
        del field["primary_key"]
        cases.append(
            (
                "field-invalid-before-missing",
                field_invalid_before_missing,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/fields/1/column",
                0,
            )
        )

        field_unknown_before_invalid = scenarios._root_document()
        field = field_unknown_before_invalid["migration"]["operations"][0][
            "model"
        ]["fields"][1]
        field["aaa"] = True
        field["column"] = "Invalid-Column"
        cases.append(
            (
                "field-unknown-before-invalid",
                field_unknown_before_invalid,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/fields/1/aaa",
                0,
            )
        )

        field_missing_before_invalid = scenarios._root_document()
        field = field_missing_before_invalid["migration"]["operations"][0][
            "model"
        ]["fields"][1]
        del field["column"]
        field["go_name"] = "invalid"
        cases.append(
            (
                "field-missing-before-invalid",
                field_missing_before_invalid,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/fields/1/column",
                0,
            )
        )

        field_invalid_before_unknown = scenarios._root_document()
        field = field_invalid_before_unknown["migration"]["operations"][0][
            "model"
        ]["fields"][1]
        field["name"] = "Invalid-Name"
        field["zzz"] = True
        cases.append(
            (
                "field-invalid-before-unknown",
                field_invalid_before_unknown,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/fields/1/name",
                0,
            )
        )

        add_field_invalid_before_missing = scenarios._tail_document()
        field = add_field_invalid_before_missing["migration"]["operations"][0][
            "field"
        ]
        field["column"] = "Invalid-Column"
        del field["primary_key"]
        cases.append(
            (
                "add-field-invalid-before-missing",
                add_field_invalid_before_missing,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/field/column",
                0,
            )
        )

        model_invalid_before_missing = scenarios._root_document()
        model = model_invalid_before_missing["migration"]["operations"][0][
            "model"
        ]
        model["db_table"] = "Invalid-Table"
        del model["name"]
        cases.append(
            (
                "model-invalid-before-missing",
                model_invalid_before_missing,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/db_table",
                0,
            )
        )

        model_unknown_before_invalid = scenarios._root_document()
        model = model_unknown_before_invalid["migration"]["operations"][0][
            "model"
        ]
        model["aaa"] = True
        model["db_table"] = "Invalid-Table"
        cases.append(
            (
                "model-unknown-before-invalid",
                model_unknown_before_invalid,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/aaa",
                0,
            )
        )

        default_unknown_before_missing = scenarios._root_document()
        field = default_unknown_before_missing["migration"]["operations"][0][
            "model"
        ]["fields"][1]
        field["default"] = {"aaa": True, "kind": "string"}
        cases.append(
            (
                "default-unknown-before-missing",
                default_unknown_before_missing,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/fields/1/default/aaa",
                0,
            )
        )

        default_missing_before_unknown = scenarios._root_document()
        field = default_missing_before_unknown["migration"]["operations"][0][
            "model"
        ]["fields"][1]
        field["default"] = {"kind": "string", "zzz": True}
        cases.append(
            (
                "default-missing-before-unknown",
                default_missing_before_unknown,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/fields/1/default/string",
                0,
            )
        )

        default_invalid_before_unknown = scenarios._root_document()
        field = default_invalid_before_unknown["migration"]["operations"][0][
            "model"
        ]["fields"][1]
        field["default"] = {
            "kind": "string",
            "string": False,
            "zzz": True,
        }
        cases.append(
            (
                "default-invalid-before-unknown",
                default_invalid_before_unknown,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/fields/1/default/string",
                0,
            )
        )

        operation_missing_before_nested_invalid = scenarios._root_document()
        operation = operation_missing_before_nested_invalid["migration"][
            "operations"
        ][0]
        del operation["app_label"]
        operation["model"]["fields"][1]["column"] = "Invalid-Column"
        cases.append(
            (
                "operation-missing-before-nested-invalid",
                operation_missing_before_nested_invalid,
                "invalid_definition_operation",
                "invalid_operation",
                "/migration/operations/0/app_label",
                0,
            )
        )

        nested_invalid_before_operation_unknown = scenarios._root_document()
        operation = nested_invalid_before_operation_unknown["migration"][
            "operations"
        ][0]
        operation["model"]["fields"][1]["column"] = "Invalid-Column"
        operation["zzz"] = True
        cases.append(
            (
                "nested-invalid-before-operation-unknown",
                nested_invalid_before_operation_unknown,
                "invalid_definition_ir",
                "invalid_ir",
                "/migration/operations/0/model/fields/1/column",
                0,
            )
        )

        for label, document, code, reason, pointer, operation_index in cases:
            with self.subTest(case=label):
                error = load_error((source("semantic-table", document),))
                self.assert_atomic_failure(
                    error,
                    code=code,
                    stage="semantic",
                    reason=reason,
                )
                self.assertEqual(
                    error.metrics["failure"]["json_pointer"],
                    pointer,
                )
                self.assertEqual(
                    error.metrics["failure"]["operation_index"],
                    operation_index,
                )
                self.assertEqual(error.metrics["operations_decoded"], 0)

        index_order = scenarios._root_document()
        operation = deepcopy(index_order["migration"]["operations"][0])
        index_order["migration"]["operations"] = [
            deepcopy(operation) for _ in range(11)
        ]
        index_order["migration"]["operations"][2]["aaa"] = True
        index_order["migration"]["operations"][10]["zzz"] = True
        error = load_error((source("semantic-table", index_order),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_operation",
            stage="semantic",
            reason="invalid_operation",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/operations/10/zzz",
        )
        self.assertEqual(error.metrics["failure"]["operation_index"], 10)
        self.assertEqual(error.metrics["operations_decoded"], 0)

    def test_migration_semantic_wrong_types_use_operation_taxonomy(
        self,
    ) -> None:
        cases = (
            ("app", 7, "/migration/app"),
            ("dependencies", {}, "/migration/dependencies"),
            ("name", 7, "/migration/name"),
            ("operations", {}, "/migration/operations"),
        )
        for member, value, pointer in cases:
            with self.subTest(member=member):
                document = scenarios._root_document()
                document["migration"][member] = value
                error = load_error((source("migration-type", document),))
                self.assert_atomic_failure(
                    error,
                    code="invalid_definition_operation",
                    stage="semantic",
                    reason="invalid_operation",
                )
                self.assertEqual(
                    error.metrics["failure"]["json_pointer"],
                    pointer,
                )
                self.assertEqual(
                    error.metrics["failure"]["operation_index"],
                    -1,
                )
                self.assertEqual(error.metrics["operations_decoded"], 0)

        combined = scenarios._root_document()
        combined["migration"].update(
            {
                "app": 7,
                "dependencies": {},
                "name": 7,
                "operations": {},
            }
        )
        error = load_error((source("migration-type", combined),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_operation",
            stage="semantic",
            reason="invalid_operation",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/app",
        )
        self.assertEqual(error.metrics["failure"]["operation_index"], -1)
        self.assertEqual(error.metrics["operations_decoded"], 0)

    def test_semantic_aggregate_candidates_are_independent(self) -> None:
        no_auto = scenarios._root_document()
        model = no_auto["migration"]["operations"][0]["model"]
        model["fields"] = [
            scenarios._char_field("title", "Title", max_length=64),
            scenarios._char_field("summary", "Summary", max_length=64),
        ]
        model["zzz"] = True
        error = load_error((source("aggregate", no_auto),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_ir",
            stage="semantic",
            reason="invalid_ir",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/operations/0/model/fields",
        )

        duplicate_mutations = {
            "name": ("id", "go_name", "invalid"),
            "go_name": ("ID", "name", "Invalid-Name"),
            "column": ("id", "go_name", "invalid"),
        }
        for member, (duplicate, invalid_member, invalid) in (
            duplicate_mutations.items()
        ):
            with self.subTest(duplicate_member=member):
                document = scenarios._root_document()
                field = document["migration"]["operations"][0]["model"][
                    "fields"
                ][1]
                field[member] = duplicate
                field[invalid_member] = invalid
                error = load_error((source("aggregate", document),))
                self.assert_atomic_failure(
                    error,
                    code="invalid_definition_ir",
                    stage="semantic",
                    reason="invalid_ir",
                )
                self.assertEqual(
                    error.metrics["failure"]["json_pointer"],
                    "/migration/operations/0/model/fields",
                )

        add_auto = scenarios._tail_document()
        auto_field = scenarios._auto_field()
        auto_field["zzz"] = True
        add_auto["migration"]["operations"][0]["field"] = auto_field
        error = load_error((source("add-field", add_auto),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_ir",
            stage="semantic",
            reason="invalid_ir",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/operations/0/field/kind",
        )

        add_primary_key = scenarios._tail_document()
        primary_field = scenarios._char_field(
            "summary",
            "Summary",
            max_length=64,
        )
        primary_field["primary_key"] = True
        primary_field["zzz"] = True
        add_primary_key["migration"]["operations"][0]["field"] = (
            primary_field
        )
        error = load_error((source("add-field", add_primary_key),))
        self.assert_atomic_failure(
            error,
            code="invalid_definition_ir",
            stage="semantic",
            reason="invalid_ir",
        )
        self.assertEqual(
            error.metrics["failure"]["json_pointer"],
            "/migration/operations/0/field/primary_key",
        )

        invariant_cases = []

        char_too_long = scenarios._root_document()
        field = char_too_long["migration"]["operations"][0]["model"][
            "fields"
        ][1]
        field["default"] = {
            "kind": "string",
            "string": "x" * 65,
            "zzz": True,
        }
        invariant_cases.append(
            (
                "char-too-long-extra",
                char_too_long,
                "/migration/operations/0/model/fields/1/default",
            )
        )

        char_boolean = scenarios._root_document()
        field = char_boolean["migration"]["operations"][0]["model"][
            "fields"
        ][1]
        field["default"] = {
            "boolean": False,
            "kind": "boolean",
            "zzz": True,
        }
        invariant_cases.append(
            (
                "char-boolean-extra",
                char_boolean,
                "/migration/operations/0/model/fields/1/default",
            )
        )

        boolean_string = scenarios._root_document()
        field = boolean_string["migration"]["operations"][0]["model"][
            "fields"
        ][1]
        field.update(
            {
                "default": {
                    "kind": "string",
                    "string": "false",
                    "zzz": True,
                },
                "kind": "boolean",
                "max_length": 0,
            }
        )
        invariant_cases.append(
            (
                "boolean-string-extra",
                boolean_string,
                "/migration/operations/0/model/fields/1/default",
            )
        )

        auto_nonnull = scenarios._root_document()
        field = auto_nonnull["migration"]["operations"][0]["model"][
            "fields"
        ][0]
        field["default"] = {
            "kind": "string",
            "string": "1",
            "zzz": True,
        }
        invariant_cases.append(
            (
                "auto-nonnull-extra",
                auto_nonnull,
                "/migration/operations/0/model/fields/0/default",
            )
        )

        auto_missing_length = scenarios._root_document()
        field = auto_missing_length["migration"]["operations"][0]["model"][
            "fields"
        ][0]
        field["default"] = {"kind": "string", "string": "1"}
        del field["max_length"]
        invariant_cases.append(
            (
                "auto-default-missing-length",
                auto_missing_length,
                "/migration/operations/0/model/fields/0/default",
            )
        )

        boolean_missing_nullable = scenarios._root_document()
        field = boolean_missing_nullable["migration"]["operations"][0][
            "model"
        ]["fields"][1]
        field.update(
            {
                "default": {"kind": "string", "string": "false"},
                "kind": "boolean",
                "max_length": 0,
            }
        )
        del field["nullable"]
        invariant_cases.append(
            (
                "boolean-default-missing-nullable",
                boolean_missing_nullable,
                "/migration/operations/0/model/fields/1/default",
            )
        )

        char_invalid_nullable = scenarios._root_document()
        field = char_invalid_nullable["migration"]["operations"][0]["model"][
            "fields"
        ][1]
        field["default"] = {"kind": "string", "string": "x" * 65}
        field["nullable"] = "false"
        invariant_cases.append(
            (
                "char-default-invalid-nullable",
                char_invalid_nullable,
                "/migration/operations/0/model/fields/1/default",
            )
        )

        no_auto_missing_primary = scenarios._root_document()
        field = scenarios._char_field("title", "Title", max_length=64)
        del field["primary_key"]
        no_auto_missing_primary["migration"]["operations"][0]["model"][
            "fields"
        ] = [field]
        invariant_cases.append(
            (
                "no-auto-missing-primary",
                no_auto_missing_primary,
                "/migration/operations/0/model/fields",
            )
        )

        pk_count_invalid_kind = scenarios._root_document()
        fields = pk_count_invalid_kind["migration"]["operations"][0]["model"][
            "fields"
        ]
        fields[0]["primary_key"] = False
        fields[1]["kind"] = 7
        invariant_cases.append(
            (
                "pk-count-invalid-kind",
                pk_count_invalid_kind,
                "/migration/operations/0/model/fields",
            )
        )

        duplicate_with_unknown_member = scenarios._root_document()
        fields = duplicate_with_unknown_member["migration"]["operations"][0][
            "model"
        ]["fields"]
        fields[1]["name"] = "id"
        unknown_name = scenarios._char_field(
            "extra",
            "Extra",
            max_length=64,
        )
        del unknown_name["name"]
        fields.append(unknown_name)
        invariant_cases.append(
            (
                "known-duplicate-with-unknown-member",
                duplicate_with_unknown_member,
                "/migration/operations/0/model/fields",
            )
        )

        multiple_pk_with_unknown_member = scenarios._root_document()
        fields = multiple_pk_with_unknown_member["migration"]["operations"][
            0
        ]["model"]["fields"]
        fields[1]["primary_key"] = True
        unknown_primary = scenarios._char_field(
            "extra",
            "Extra",
            max_length=64,
        )
        del unknown_primary["primary_key"]
        fields.append(unknown_primary)
        invariant_cases.append(
            (
                "known-multiple-pk-with-unknown-member",
                multiple_pk_with_unknown_member,
                "/migration/operations/0/model/fields",
            )
        )

        for label, document, pointer in invariant_cases:
            with self.subTest(invariant=label):
                error = load_error((source("invariant", document),))
                self.assert_atomic_failure(
                    error,
                    code="invalid_definition_ir",
                    stage="semantic",
                    reason="invalid_ir",
                )
                self.assertEqual(
                    error.metrics["failure"]["json_pointer"],
                    pointer,
                )
                self.assertEqual(error.metrics["operations_decoded"], 0)

    def test_ir_normalization_equality_rejects_implicit_values_and_integer_arm(
        self,
    ) -> None:
        mutations = []

        empty_table = scenarios._root_document()
        empty_table["migration"]["operations"][0]["model"]["db_table"] = ""
        mutations.append(empty_table)

        missing_auto = scenarios._root_document()
        missing_auto["migration"]["operations"][0]["model"]["fields"] = [
            scenarios._char_field("title", "Title", max_length=64)
        ]
        mutations.append(missing_auto)

        empty_column = scenarios._root_document()
        empty_column["migration"]["operations"][0]["model"]["fields"][1][
            "column"
        ] = ""
        mutations.append(empty_column)

        integer_default = scenarios._root_document()
        integer_default["migration"]["operations"][0]["model"]["fields"][1][
            "default"
        ] = {"integer": 1, "kind": "integer"}
        mutations.append(integer_default)

        for index, document in enumerate(mutations):
            with self.subTest(index=index):
                error = load_error((source("ir", document),))
                self.assertEqual(error.code, "invalid_definition_ir")
                self.assertEqual(error.metrics["failure"]["stage"], "semantic")
                self.assertEqual(error.metrics["definitions_published"], 0)

        max_length = scenarios._root_document()
        max_length["migration"]["operations"][0]["model"]["fields"][1][
            "max_length"
        ] = 1 << 31
        error = load_error((source("range", max_length),))
        self.assertEqual(error.code, "invalid_definition_document")
        self.assertEqual(error.metrics["definitions_published"], 0)

        invalid_app = scenarios._root_document()
        invalid_app["migration"]["app"] = "Invalid-App"
        invalid_app["migration"]["operations"][0]["app_label"] = "Invalid-App"
        error = load_error((source("app", invalid_app),))
        self.assertEqual(error.code, "invalid_definition_ir")
        self.assertEqual(error.metrics["failure"]["stage"], "semantic")

    def test_add_field_sentinel_rejects_second_auto_and_avoids_name_collision(
        self,
    ) -> None:
        second_auto = scenarios._tail_document()
        second_auto["migration"]["operations"] = [
            {
                "app_label": "alpha",
                "field": scenarios._auto_field(),
                "kind": "add_field",
                "model_name": "entry",
            }
        ]
        error = load_error(
            (
                source("root", scenarios._root_document()),
                source("tail", second_auto),
            )
        )
        self.assertEqual(error.code, "invalid_definition_ir")

        collision = scenarios._tail_document()
        collision_field = scenarios._char_field(
            "_godj_loader_pk",
            "GodjLoaderPK",
            max_length=8,
        )
        collision_field["column"] = "_godj_loader_pk"
        collision["migration"]["operations"] = [
            {
                "app_label": "alpha",
                "field": collision_field,
                "kind": "add_field",
                "model_name": "entry",
            }
        ]
        loaded, _ = scenarios._load(
            (
                source("root", scenarios._root_document()),
                source("tail", collision),
            )
        )
        self.assertEqual(
            loaded.definitions[1]["operations"][0]["field"],
            collision_field,
        )

    def test_semantic_error_beats_graph_error_for_each_input_permutation(self) -> None:
        semantic = scenarios._root_document()
        semantic["migration"]["operations"] = [
            {"app_label": "alpha", "kind": "run_python"}
        ]
        graph = scenarios._tail_document()
        graph["migration"]["dependencies"] = [
            {"app": "missing", "name": "0001_absent"}
        ]
        inputs = (source("z-graph", graph), source("a-semantic", semantic))
        for batch in (inputs, tuple(reversed(inputs))):
            error = load_error(batch)
            self.assertEqual(error.code, "unsupported_definition_operation")
            self.assertEqual(error.metrics["failure"]["stage"], "semantic")
            self.assertEqual(error.metrics["failure"]["source_id"], "a-semantic")

    def test_digest_changes_for_semantic_mutations_but_not_dependency_order(self) -> None:
        baseline, _ = scenarios._load(scenarios._fixture_sources())

        changed_default = scenarios._root_document()
        changed_default["migration"]["operations"][0]["model"]["fields"][1][
            "default"
        ] = {"kind": "string", "string": "changed"}
        changed, _ = scenarios._load(
            (
                source("root", changed_default),
                source("tail", scenarios._tail_document()),
            )
        )
        self.assertNotEqual(baseline.digest, changed.digest)

        third = scenarios._tail_document()
        third["migration"]["name"] = "0003_more"
        third["migration"]["dependencies"] = [
            {"app": "alpha", "name": "0002_fields"},
            {"app": "alpha", "name": "0001_initial"},
        ]
        first_order, _ = scenarios._load(
            (
                source("root", scenarios._root_document()),
                source("tail", scenarios._tail_document()),
                source("third", third),
            )
        )
        third["migration"]["dependencies"].reverse()
        second_order, _ = scenarios._load(
            (
                source("third", third),
                source("tail", scenarios._tail_document()),
                source("root", scenarios._root_document()),
            )
        )
        self.assertEqual(first_order.definitions, second_order.definitions)
        self.assertEqual(first_order.digest, second_order.digest)

    def test_public_loaded_executor_uses_explicit_graph_and_one_migrate_call(
        self,
    ) -> None:
        original = MigrationExecutor.migrate
        calls = []

        def spy(executor, targets, plan=None, **kwargs):
            calls.append({"plan": plan, "targets": list(targets)})
            return original(executor, targets, plan=plan, **kwargs)

        with patch.object(MigrationExecutor, "migrate", spy):
            value = observed(scenarios.public_loaded_executor, "MIG-064")

        self.assertEqual(len(calls), 1)
        self.assertEqual(
            calls[0]["targets"],
            [("alpha", "0002_fields")],
        )
        self.assertEqual(value["metrics"]["session_open_calls"], 1)
        self.assertEqual(value["metrics"]["source_reads_after_snapshot"], 0)
        self.assertIsNone(value["metrics"]["failure"])
        self.assertEqual(
            value["metrics"]["lifecycle"]["route"],
            "loaded_definition_executor",
        )
        self.assertTrue(value["metrics"]["lifecycle"]["definitions_unchanged"])
        self.assertEqual(
            value["result"]["execution"],
            {
                "attempted": True,
                "observed_digest": value["result"]["definition_set"]["digest"],
                "session_open_calls": 1,
            },
        )
        self.assertEqual(
            [
                (step["app"], step["name"], step["direction"])
                for step in value["result"]["lifecycle"]["plan"]
            ],
            [
                ("alpha", "0001_initial", "forward"),
                ("alpha", "0002_fields", "forward"),
            ],
        )
        self.assertEqual(
            value["db"]["after"]["migration_records"],
            [
                {"app": "alpha", "name": "0001_initial"},
                {"app": "alpha", "name": "0002_fields"},
            ],
        )
        self.assertEqual(
            value["db"]["after"]["managed_schema"][0]["name"],
            "godj_definition_alpha_entry",
        )
        self.assertFalse(value["db"]["before"]["recorder_present"])
        self.assertTrue(value["db"]["after"]["recorder_present"])
        self.assertNotIn(scenarios._DATABASE_ALIAS, connections.databases)

    def test_lifecycle_failure_probe_is_not_retried_and_database_is_cleaned(self) -> None:
        calls = []

        def fail(_executor, _targets, plan=None, **_kwargs):
            calls.append(plan)
            raise RuntimeError("sentinel lifecycle failure")

        with patch.object(MigrationExecutor, "migrate", fail):
            with self.assertRaisesRegex(RuntimeError, "sentinel lifecycle failure"):
                scenarios.public_loaded_executor("untrusted-contract")
        self.assertEqual(len(calls), 1)
        self.assertNotIn(scenarios._DATABASE_ALIAS, connections.databases)

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_two_independent_hashseed_processes_match_checked_in_oracle(self) -> None:
        outputs = []
        with tempfile.TemporaryDirectory() as temporary_directory:
            for index, hash_seed in enumerate(("17", "982451653"), 1):
                output = Path(temporary_directory) / f"definition-source-{index}.json"
                environment = os.environ.copy()
                environment.update(
                    {
                        "LC_ALL": "C",
                        "PYTHONHASHSEED": hash_seed,
                        "TZ": "UTC",
                    }
                )
                subprocess.run(
                    [
                        sys.executable,
                        "-m",
                        "conformance.runners.django",
                        "--profile",
                        str(PROFILE),
                        "--manifest",
                        str(MANIFEST),
                        "--output",
                        str(output),
                    ],
                    cwd=ROOT,
                    env=environment,
                    check=True,
                    capture_output=True,
                    text=True,
                )
                outputs.append(output.read_bytes())
        self.assertEqual(outputs[0], outputs[1])
        self.assertEqual(outputs[0], ORACLE.read_bytes())


if __name__ == "__main__":
    unittest.main()
