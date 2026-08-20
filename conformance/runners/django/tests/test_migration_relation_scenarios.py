from __future__ import annotations

import ast
import inspect
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from django.db import connections

from conformance.runners.django import migration_relation_scenarios as scenarios
from conformance.runners.django.normalizer import canonical_json


ROOT = Path(__file__).resolve().parents[4]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = ROOT / "conformance/contracts/migration-relation-manifest.json"
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64"
    / "migration-relation-oracle.json"
)
STATIC = (
    ROOT
    / "conformance/fixtures"
    / "godj-migration-relation-not-implemented.json"
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
    raw = scenario(contract_id)
    return {
        "db": (
            denormalize(raw["db_state"])
            if raw["db_state"] is not None
            else None
        ),
        "metrics": (
            denormalize(raw["metrics"])
            if raw["metrics"] is not None
            else None
        ),
        "raw": raw,
        "result": (
            denormalize(raw["result"])
            if raw["result"] is not None
            else None
        ),
    }


def rows(state, table):
    return next(item["rows"] for item in state["rows"] if item["table"] == table)


def columns(state, table):
    return [
        item["name"]
        for item in next(
            value["columns"] for value in state["tables"] if value["name"] == table
        )
    ]


def records(state):
    return [
        (item["app"], item["name"])
        for item in state["migration_records"]
    ]


class MigrationRelationScenarioTests(unittest.TestCase):
    def tearDown(self) -> None:
        self.assertNotIn(scenarios._DATABASE_ALIAS, connections.databases)

    def test_registry_order_matches_mig_075_through_086(self) -> None:
        self.assertEqual(
            list(scenarios.SCENARIOS),
            [
                "godj.migration.relation.current_abi",
                "godj.migration.relation.current_format_validation",
                "godj.migration.relation.current_digest",
                "godj.migration.relation.current_state",
                "godj.migration.relation.structural_preflight",
                "django.migration.relation.create_lifecycle",
                "django.migration.relation.add_nullable_populated",
                "django.migration.relation.remove_remake",
                "django.migration.relation.physical_fk_policy",
                "django.migration.relation.file_restart",
                "django.migration.relation.precommit_faults",
                "godj.migration.relation.commit_outcomes",
            ],
        )

    def test_manifest_locks_mapping_phase_comparison_and_provenance(self) -> None:
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        contracts = manifest["contracts"]
        self.assertEqual(manifest["format_version"], 2)
        self.assertEqual(
            manifest["profile_id"],
            "django-6.1-sqlite-darwin-arm64",
        )
        self.assertEqual(
            [contract["id"] for contract in contracts],
            [f"MIG-{number:03d}" for number in range(75, 87)],
        )
        self.assertEqual(
            [contract["scenario"] for contract in contracts],
            list(scenarios.SCENARIOS),
        )
        self.assertEqual(
            [contract["phase"] for contract in contracts],
            [
                "construction",
                "environment",
                "construction",
                "construction",
                "evaluation",
                "commit",
                "commit",
                "commit",
                "commit",
                "commit",
                "rollback",
                "commit",
            ],
        )
        self.assertEqual(
            [contract["comparison"] for contract in contracts],
            [
                ["result", "metrics"],
                ["result", "metrics"],
                ["result", "metrics"],
                ["result", "metrics"],
                ["result", "metrics"],
                ["result", "db_state", "metrics"],
                ["result", "db_state", "metrics"],
                ["result", "db_state", "metrics"],
                ["result", "db_state", "metrics"],
                ["result", "db_state", "metrics"],
                ["result", "db_state", "metrics"],
                ["result", "metrics"],
            ],
        )

        proposal_ids: set[str] = set()
        django_ids = {
            "MIG-080",
            "MIG-081",
            "MIG-082",
            "MIG-083",
            "MIG-084",
            "MIG-085",
        }
        for contract in contracts:
            with self.subTest(contract=contract["id"]):
                self.assertEqual(contract["status"], "oracle_locked")
                provenance = contract["provenance"]
                self.assertTrue(provenance)
                self.assertNotIn("ADR-0034", json.dumps(provenance))
                self.assertIn(
                    "ADR-0035",
                    {item["reference"] for item in provenance},
                )
                proposals = [item for item in provenance if item["kind"] == "proposal"]
                django_sources = [
                    item for item in provenance if item["kind"] in {"source", "test"}
                ]
                self.assertEqual(bool(proposals), contract["id"] in proposal_ids)
                self.assertEqual(bool(django_sources), contract["id"] in django_ids)
                for item in provenance:
                    self.assertIs(item["derived"], False)
                    if item["kind"] == "proposal":
                        self.assertEqual(item["reference"], "GDJ-0036")
                        self.assertNotIn("license", item)
                    elif item["kind"] == "decision":
                        self.assertIn(
                            item["reference"],
                            {"ADR-0010", "ADR-0017", "ADR-0035"},
                        )
                        self.assertNotIn("license", item)
                    else:
                        self.assertIn(
                            "django@fe0a859f537d4238cf49fca39073513206f83122:",
                            item["reference"],
                        )
                        self.assertEqual(item["license"], "BSD-3-Clause")

    def test_static_fixture_is_ordered_and_explicitly_not_implemented(self) -> None:
        fixture = json.loads(STATIC.read_text(encoding="utf-8"))
        profile = json.loads(PROFILE.read_text(encoding="utf-8"))
        self.assertEqual(fixture["format_version"], 2)
        self.assertEqual(
            fixture["profile"],
            {
                "id": profile["id"],
                "fingerprint": profile["fingerprint"],
                "lock": profile["lock"],
            },
        )
        self.assertEqual(
            [item["id"] for item in fixture["contracts"]],
            [f"MIG-{number:03d}" for number in range(75, 87)],
        )
        self.assertEqual(
            {item["status"] for item in fixture["contracts"]},
            {"not_implemented"},
        )
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        self.assertEqual(
            [item["phase"] for item in fixture["contracts"]],
            [item["phase"] for item in manifest["contracts"]],
        )

    def test_scenarios_are_byte_deterministic_and_contract_id_independent(self) -> None:
        for number, (name, scenario) in enumerate(scenarios.SCENARIOS.items(), 75):
            with self.subTest(scenario=name):
                expected_id = f"MIG-{number:03d}"
                first = scenario(expected_id)
                second = scenario(expected_id)
                arbitrary = scenario("untrusted-contract-id")
                self.assertEqual(canonical_json(first), canonical_json(second))
                self.assertEqual(first["id"], expected_id)
                self.assertEqual(arbitrary["id"], "untrusted-contract-id")
                self.assertEqual(
                    {key: value for key, value in first.items() if key != "id"},
                    {
                        key: value
                        for key, value in arbitrary.items()
                        if key != "id"
                    },
                )

    def test_scenario_source_has_no_artifact_or_contract_dispatch(self) -> None:
        source = inspect.getsource(scenarios)
        syntax = ast.parse(source)
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
            "migration-relation-oracle",
            "godj-migration-relation-not-implemented",
            "ADR-0034",
            "import_module",
            "pkgutil",
        }:
            self.assertNotIn(forbidden, source)
        self.assertTrue({"migration_plan", "migrate"} <= called_attributes)
        self.assertTrue(
            {"read_bytes", "read_text", "write_bytes", "write_text"}.isdisjoint(
                called_attributes
            )
        )
        self.assertTrue({"open", "eval", "exec", "compile"}.isdisjoint(called_names))

    def test_runtime_cannot_read_checked_artifacts(self) -> None:
        with patch.object(
            Path,
            "read_text",
            side_effect=AssertionError("scenario attempted artifact read"),
        ), patch.object(
            Path,
            "read_bytes",
            side_effect=AssertionError("scenario attempted artifact read"),
        ):
            for number, scenario in enumerate(scenarios.SCENARIOS.values(), 75):
                scenario(f"MIG-{number:03d}")

    def test_current_abi_is_single_deterministic_and_tuple_free(self) -> None:
        value = observed(scenarios.current_abi, "MIG-075")
        current = value["result"]["current"]
        self.assertEqual(
            current["format"],
            {"definition": 1, "schema_ir": 1, "state": 1},
        )
        self.assertEqual(current["digest_domain"], "godj:migration-definition-set:v1")
        self.assertEqual(current["canonical_sha256"], current["definition_set_digest"])
        self.assertRegex(current["definition_set_digest"], r"^sha256:[0-9a-f]{64}$")
        self.assertTrue(value["result"]["repeat_equal"])
        self.assertTrue(value["result"]["scalar_and_relation_share_format"])
        self.assertFalse(value["result"]["retired_compatibility_tuple_present"])
        self.assertEqual(value["metrics"]["compatibility_upgrades"], 0)

    def test_current_format_rejects_invalid_and_retired_envelopes(self) -> None:
        value = observed(scenarios.current_format_validation, "MIG-076")
        cases = value["result"]["cases"]
        self.assertEqual([item["case"] for item in cases], [
            "exact_current",
            "missing_format_version",
            "unknown_format_version",
            "wrong_type_format_version",
            "overflow_format_version",
            "retired_compatibility_tuple",
        ])
        self.assertEqual(
            [item["accepted"] for item in cases],
            [True, False, False, False, False, False],
        )
        self.assertEqual(
            [item["error"]["code"] for item in cases[1:]],
            [
                "invalid_definition_document",
                "definition_format_incompatible",
                "invalid_definition_document",
                "invalid_definition_document",
                "invalid_definition_document",
            ],
        )
        self.assertEqual(
            [item["error"]["reason"] for item in cases[1:]],
            [
                "missing_field",
                "format_version",
                "wrong_type",
                "out_of_range",
                "unknown_field",
            ],
        )
        self.assertEqual(value["metrics"]["accepted_documents"], 1)
        self.assertEqual(value["metrics"]["rejected_documents"], 5)
        self.assertEqual(value["metrics"]["database_io"], 0)

    def test_current_digest_is_ordered_single_domain_and_profile_free(self) -> None:
        value = observed(scenarios.current_digest, "MIG-077")
        result = value["result"]
        self.assertEqual(result["scalar_only"]["domain"], "godj:migration-definition-set:v1")
        self.assertEqual(result["relation_only"]["domain"], "godj:migration-definition-set:v1")
        self.assertEqual(result["combined"]["domain"], "godj:migration-definition-set:v1")
        self.assertTrue(result["combined"]["permutation_equal"])
        self.assertFalse(result["profile_metadata_present"])
        self.assertEqual(
            [item["app"] for item in result["combined"]["canonical_definitions"]],
            ["alpha", "blog"],
        )
        self.assertEqual(
            len(
                {
                    result["scalar_only"]["digest"],
                    result["relation_only"]["digest"],
                    result["combined"]["digest"],
                }
            ),
            3,
        )

    def test_current_state_is_single_format_alias_free_and_transition_free(self) -> None:
        value = observed(scenarios.current_state, "MIG-078")
        result = value["result"]
        self.assertTrue(result["alias_free"])
        self.assertTrue(result["single_format"])
        self.assertFalse(result["format_transition_required"])
        self.assertEqual(result["scalar_state"]["format_version"], 1)
        self.assertEqual(result["relation_state"]["format_version"], 1)
        self.assertEqual(value["metrics"]["format_transitions"], 0)

    def test_structural_preflight_has_three_staged_no_mutation_lanes(self) -> None:
        value = observed(scenarios.structural_preflight, "MIG-079")
        lanes = value["result"]["lanes"]
        self.assertEqual(
            [item["lane"] for item in lanes],
            [
                "static_invalid",
                "history_invalid",
                "physical_populated_required",
            ],
        )
        self.assertEqual(lanes[0]["trace_events"], 0)
        self.assertEqual(
            lanes[1]["forbidden_trace"],
            ["begin_migration", "ddl", "record", "revision"],
        )
        self.assertTrue(lanes[2]["reads_allowed"])
        self.assertEqual(lanes[2]["mutation_events"], 0)
        self.assertTrue(lanes[2]["durable_unchanged"])
        capability = value["result"]["mandatory_backend_capability"]
        self.assertTrue(capability["optional_relation_port_retired"])
        self.assertEqual(
            capability["replacement_error"],
            "migration_capability_unavailable",
        )
        self.assertEqual(value["metrics"]["lanes_checked"], 3)
        self.assertEqual(value["metrics"]["mutation_events"], 0)

    def test_create_apply_unapply_reapply_uses_live_schema_and_recorder(self) -> None:
        value = observed(scenarios.create_lifecycle, "MIG-080")
        transitions = value["result"]["transitions"]
        self.assertEqual([item["label"] for item in transitions], ["apply", "unapply", "reapply"])
        self.assertEqual(
            [item["plan"][0]["direction"] for item in transitions],
            ["forward", "backward", "forward"],
        )
        self.assertEqual([len(item["state"]["tables"]) for item in transitions], [2, 0, 2])
        self.assertEqual(records(value["db"]), [scenarios._M1])
        self.assertEqual(columns(value["db"], scenarios._ARTICLE_TABLE), ["id", "title", "author_id"])

    def test_nullable_addfield_preserves_populated_rows_and_required_policy_is_separate(self) -> None:
        value = observed(scenarios.add_nullable_populated, "MIG-081")
        before = rows(value["db"]["before"], scenarios._ARTICLE_TABLE)
        after = rows(value["db"]["after"], scenarios._ARTICLE_TABLE)
        self.assertEqual(
            [{key: row[key] for key in ("id", "title", "author_id")} for row in after],
            before,
        )
        self.assertEqual([row["editor_id"] for row in after], [None, None])
        self.assertTrue(value["result"]["django_observation"]["existing_rows_received_null"])
        required = value["result"]["gdj_required_populated_policy"]
        self.assertTrue(required["pre_ddl"])
        self.assertEqual(required["mutation_count"], 0)
        self.assertEqual(required["error"]["code"], "required_foreign_key_requires_backfill")
        self.assertEqual(value["metrics"]["required_policy_database_io"], 0)

    def test_reverse_remake_preserves_rows_ids_and_real_sqlite_sequence(self) -> None:
        value = observed(scenarios.remove_remake, "MIG-082")
        before = value["db"]["before"]
        after = value["db"]["after"]
        self.assertIn("editor_id", columns(before, scenarios._ARTICLE_TABLE))
        self.assertNotIn("editor_id", columns(after, scenarios._ARTICLE_TABLE))
        before_rows = rows(before, scenarios._ARTICLE_TABLE)
        after_rows = rows(after, scenarios._ARTICLE_TABLE)
        self.assertEqual(
            [{key: row[key] for key in ("id", "title", "author_id")} for row in before_rows],
            after_rows,
        )
        expected_sequences = [
            {"sequence": 8, "table": scenarios._ARTICLE_TABLE},
            {"sequence": 5, "table": scenarios._AUTHOR_TABLE},
        ]
        self.assertEqual(before["sequences"], expected_sequences)
        self.assertEqual(after["sequences"], expected_sequences)
        self.assertTrue(value["result"]["preservation"]["article_ids_preserved"])

    def test_physical_fk_actions_and_connection_pragma_are_observed_separately(self) -> None:
        value = observed(scenarios.physical_fk_policy, "MIG-083")
        django = value["result"]["django_observation"]
        self.assertEqual(
            django["pragma_sequence"],
            [
                {"point": "before_editor", "value": 1},
                {"point": "inside_editor", "value": 0},
                {"point": "after_editor", "value": 1},
                {"point": "after_migrate", "value": 1},
            ],
        )
        self.assertEqual(
            [(item["column"], item["on_delete"]) for item in django["constraint_actions"]],
            [("editor_id", "NO ACTION"), ("author_id", "NO ACTION")],
        )
        policy = value["result"]["gdj_pinned_policy"]
        self.assertEqual(policy["physical_actions"], {"protect": "NO ACTION", "set_null": "NO ACTION"})
        self.assertTrue(policy["same_physical_connection_required"])

    def test_file_restart_uses_fresh_wrapper_and_reconstructs_latest_state(self) -> None:
        value = observed(scenarios.file_restart, "MIG-084")
        self.assertTrue(value["result"]["connection_replaced"])
        self.assertEqual(value["result"]["fresh_plan"], [])
        self.assertEqual(
            value["result"]["reconstructed_models"],
            [
                {"app": scenarios._APP, "model": "article"},
                {"app": scenarios._APP, "model": "author"},
            ],
        )
        self.assertEqual(value["db"]["before_close"], value["db"]["after_reopen"])
        self.assertEqual(records(value["db"]["after_reopen"]), [scenarios._M1, scenarios._M2])

    def test_django_fault_boundaries_do_not_impersonate_godj_atomic_policy(self) -> None:
        value = observed(scenarios.precommit_faults, "MIG-085")
        ddl, recorder = value["result"]["django_faults"]
        self.assertEqual(
            (ddl["fault"], ddl["fully_rolled_back"], ddl["schema_changed"]),
            ("ddl", True, False),
        )
        self.assertEqual(ddl["transaction_boundary"], "rolled_back_before_ddl")
        self.assertEqual(
            (recorder["fault"], recorder["fully_rolled_back"], recorder["schema_changed"]),
            ("recorder", False, True),
        )
        self.assertEqual(
            recorder["transaction_boundary"],
            "schema_committed_before_recorder_failure",
        )
        self.assertFalse(ddl["failed_migration_record_published"])
        self.assertFalse(recorder["failed_migration_record_published"])
        self.assertTrue(ddl["connection_replaced"])
        self.assertTrue(recorder["connection_replaced"])
        self.assertTrue(ddl["fresh_reopen_durable"])
        self.assertTrue(recorder["fresh_reopen_durable"])
        self.assertEqual(value["db"]["ddl"]["before"], value["db"]["ddl"]["after"])
        self.assertEqual(value["db"]["ddl"]["after"], value["db"]["ddl"]["after_reopen"])
        self.assertNotEqual(value["db"]["recorder"]["before"], value["db"]["recorder"]["after"])
        self.assertEqual(
            value["db"]["recorder"]["after"],
            value["db"]["recorder"]["after_reopen"],
        )
        durable = value["db"]["recorder"]["after_reopen"]
        self.assertIn("editor_id", columns(durable, scenarios._ARTICLE_TABLE))
        self.assertEqual(records(durable), [scenarios._M1])
        self.assertEqual(
            [row["id"] for row in rows(durable, scenarios._ARTICLE_TABLE)],
            [3, 8],
        )
        self.assertEqual(value["metrics"]["faults_fully_rolled_back"], 1)
        self.assertEqual(value["metrics"]["fresh_reopens"], 2)
        self.assertEqual(value["metrics"]["schema_committed_record_missing"], 1)
        policy = value["result"]["gdj_revision_fault_policy"]
        self.assertTrue(policy["same_transaction"])
        self.assertFalse(policy["published_successor_revision"])

    def test_commit_outcomes_are_closed_and_never_retried(self) -> None:
        value = observed(scenarios.commit_outcomes, "MIG-086")
        cases = value["result"]["cases"]
        self.assertEqual([item["outcome"] for item in cases], ["success", "definite_failure", "unknown"])
        self.assertEqual([item["commit_calls"] for item in cases], [1, 1, 1])
        self.assertEqual([item["retry_calls"] for item in cases], [0, 0, 0])
        self.assertEqual([item["state_published"] for item in cases], [True, False, False])
        self.assertFalse(cases[2]["durable_result_known"])
        self.assertEqual(value["metrics"]["retry_calls"], 0)

    def test_semantic_mutation_matrix_changes_each_comparable_observation(self) -> None:
        def comparable(scenario, contract_id):
            raw = scenario(contract_id)
            return canonical_json(
                {
                    key: raw[key]
                    for key in ("result", "db_state", "metrics")
                    if raw[key] is not None
                }
            )

        with patch.object(scenarios, "_CURRENT_DIGEST_DOMAIN", "godj:mutation:v1"):
            with self.assertRaisesRegex(AssertionError, "canonical bytes"):
                scenarios.current_abi("MIG-075")

        mutations = []

        baseline = comparable(scenarios.current_format_validation, "MIG-076")
        original_format_case = scenarios._format_case

        def changed_format_case(name, document):
            result = original_format_case(name, document)
            if name == "retired_compatibility_tuple":
                result["error"]["code"] = "mutated_format_error"
            return result

        with patch.object(scenarios, "_format_case", changed_format_case):
            mutations.append(("MIG-076", baseline, comparable(scenarios.current_format_validation, "MIG-076")))

        baseline = comparable(scenarios.current_digest, "MIG-077")
        with patch.object(scenarios, "_CURRENT_DIGEST_DOMAIN", "godj:mutation:v1"):
            mutations.append(("MIG-077", baseline, comparable(scenarios.current_digest, "MIG-077")))

        original_scalar_state = scenarios._scalar_state

        def changed_scalar_state():
            state = original_scalar_state()
            state["models"][0]["table"] = "mutation_article"
            return state

        baseline = comparable(scenarios.current_state, "MIG-078")
        with patch.object(scenarios, "_scalar_state", changed_scalar_state):
            mutations.append(("MIG-078", baseline, comparable(scenarios.current_state, "MIG-078")))

        def changed_error(code, *, stage, reason):
            return {
                "category": "migration_relation_decision_error",
                "code": "mutated_" + code,
                "message_is_contract": False,
                "reason": reason,
                "stage": stage,
            }

        baseline = comparable(scenarios.structural_preflight, "MIG-079")
        with patch.object(scenarios, "_decision_error", changed_error):
            mutations.append(("MIG-079", baseline, comparable(scenarios.structural_preflight, "MIG-079")))

        baseline = comparable(scenarios.create_lifecycle, "MIG-080")
        with patch.object(scenarios, "_M1", scenarios._M2):
            mutations.append(("MIG-080", baseline, comparable(scenarios.create_lifecycle, "MIG-080")))

        original_seed = scenarios._seed_0001

        def changed_seed(connection):
            original_seed(connection)
            with connection.cursor() as cursor:
                cursor.execute(
                    f"UPDATE {connection.ops.quote_name(scenarios._ARTICLE_TABLE)} "
                    "SET title = %s WHERE id = %s",
                    ["Mutated", 3],
                )

        baseline = comparable(scenarios.add_nullable_populated, "MIG-081")
        with patch.object(scenarios, "_seed_0001", changed_seed):
            mutations.append(("MIG-081", baseline, comparable(scenarios.add_nullable_populated, "MIG-081")))

        baseline = comparable(scenarios.remove_remake, "MIG-082")
        with patch.object(scenarios, "_set_editors", lambda connection: None):
            mutations.append(("MIG-082", baseline, comparable(scenarios.remove_remake, "MIG-082")))

        baseline = comparable(scenarios.physical_fk_policy, "MIG-083")
        with patch.object(scenarios, "_pragma_foreign_keys", lambda connection: 0):
            mutations.append(("MIG-083", baseline, comparable(scenarios.physical_fk_policy, "MIG-083")))

        baseline = comparable(scenarios.file_restart, "MIG-084")
        with patch.object(scenarios._DatabaseSession, "reopen", lambda self: False):
            mutations.append(("MIG-084", baseline, comparable(scenarios.file_restart, "MIG-084")))

        baseline = comparable(scenarios.precommit_faults, "MIG-085")
        with patch.object(scenarios, "_statement_kind", lambda sql: "MUTATED"):
            mutations.append(("MIG-085", baseline, comparable(scenarios.precommit_faults, "MIG-085")))

        baseline = comparable(scenarios.commit_outcomes, "MIG-086")
        with patch.object(scenarios, "_decision_error", changed_error):
            mutations.append(("MIG-086", baseline, comparable(scenarios.commit_outcomes, "MIG-086")))

        self.assertEqual([item[0] for item in mutations], [f"MIG-{number:03d}" for number in range(76, 87)])
        for contract_id, baseline, mutated in mutations:
            with self.subTest(contract=contract_id):
                self.assertNotEqual(baseline, mutated)

    @unittest.skipUnless(
        os.environ.get("GODJ_EXACT_PROFILE") == "1",
        "requires the locked darwin/arm64 reference profile",
    )
    def test_two_hashseed_processes_match_checked_in_oracle(self) -> None:
        outputs = []
        with tempfile.TemporaryDirectory() as temporary_directory:
            for index, hash_seed in enumerate(("17", "982451653"), 1):
                output = Path(temporary_directory) / f"migration-relation-{index}.json"
                environment = os.environ.copy()
                environment.update({"LC_ALL": "C", "PYTHONHASHSEED": hash_seed, "TZ": "UTC"})
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
