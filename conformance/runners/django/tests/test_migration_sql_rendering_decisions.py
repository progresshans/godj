from __future__ import annotations

import ast
import inspect
import unittest
from typing import Any

from conformance.runners.django import migration_sql_rendering_decisions as decisions
from conformance.runners.django.normalizer import canonical_json


EXPECTED_SCENARIOS = (
    "godj.migration.sql_rendering.argv_and_pre_io_rejection",
    "godj.migration.sql_rendering.complete_load_exact_lookup_and_request",
    "godj.migration.sql_rendering.postgres_current_projection",
    "godj.migration.sql_rendering.canonical_deterministic_output",
    "godj.migration.sql_rendering.database_and_history_zero_calls",
    "godj.migration.sql_rendering.renderer_and_operation_fail_closed",
    "godj.migration.sql_rendering.resource_cleanup_redaction_and_write",
    "godj.migration.sql_rendering.external_project_configuration",
)
CONTRACT_IDS = (
    "MIG-129",
    "MIG-130",
    "MIG-133",
    "MIG-134",
    "MIG-135",
    "MIG-136",
    "MIG-137",
    "MIG-138",
)


def _semantic(value: Any) -> Any:
    if value is None or not isinstance(value, dict) or "type" not in value:
        return value
    kind = value["type"]
    if kind == "object":
        return {field["name"]: _semantic(field["value"]) for field in value["fields"]}
    if kind == "list":
        return [_semantic(item) for item in value["items"]]
    if kind == "null":
        return None
    if kind == "int":
        return int(value["value"])
    return value["value"]


class MigrationSQLRenderingDecisionTests(unittest.TestCase):
    def test_registry_slug_phases_and_shapes_are_exact(self) -> None:
        self.assertEqual(decisions.SET_SLUG, "migration-sql-rendering")
        self.assertEqual(tuple(decisions.SCENARIOS), EXPECTED_SCENARIOS)
        observations = [
            scenario(contract_id)
            for contract_id, scenario in zip(
                CONTRACT_IDS,
                decisions.SCENARIOS.values(),
                strict=True,
            )
        ]
        self.assertEqual(
            [observation["phase"] for observation in observations],
            [
                "environment",
                "construction",
                "construction",
                "evaluation",
                "environment",
                "evaluation",
                "environment",
                "environment",
            ],
        )
        self.assertTrue(
            all(observation["status"] == "observed" for observation in observations)
        )
        self.assertTrue(
            all(observation["error"] is None for observation in observations)
        )
        self.assertTrue(
            all(observation["result"] is not None for observation in observations)
        )
        self.assertTrue(
            all(observation["metrics"] is not None for observation in observations)
        )
        self.assertEqual(
            [observation["db_state"] is not None for observation in observations],
            [False, False, False, False, True, False, False, True],
        )

    def test_exact_two_argv_forms_and_all_rejections_are_pre_io(self) -> None:
        observation = decisions.argv_and_pre_io_rejection("MIG-129")
        result = _semantic(observation["result"])
        metrics = _semantic(observation["metrics"])
        self.assertEqual(result["exact_public_forms"], 2)
        self.assertEqual(
            [case["argv"] for case in result["accepted"]],
            [
                ["sqlmigrate", "blog", "0002_render_sql"],
                [
                    "sqlmigrate",
                    "blog",
                    "0002_render_sql",
                    "--project",
                    "./godj.toml",
                ],
            ],
        )
        self.assertEqual(result["migration_name_resolution"], "exact_only")
        self.assertEqual(result["zero_name_policy"], "literal_exact_name")
        self.assertEqual(len(result["rejected"]), 9)
        for case in result["rejected"]:
            self.assertEqual(case["category"], "migration_project_command_error")
            self.assertEqual(case["code"], "invalid_arguments")
            for field in (
                "backend_opens",
                "builds",
                "project_discoveries",
                "renderer_observations",
                "source_loads",
            ):
                self.assertEqual(case[field], 0)
        for field in (
            "backend_opens_for_rejected",
            "builds_for_rejected",
            "project_discoveries_for_rejected",
            "renderer_observations_for_rejected",
            "source_loads_for_rejected",
        ):
            self.assertEqual(metrics[field], 0)

    def test_complete_load_exact_lookup_and_request_precedence_is_closed(self) -> None:
        observation = decisions.complete_load_exact_lookup_and_request("MIG-130")
        result = _semantic(observation["result"])
        metrics = _semantic(observation["metrics"])
        self.assertEqual(
            result["stages"],
            [
                "complete_definition_load",
                "graph_validation",
                "chronology_validation",
                "exact_target_lookup",
                "target_before_state_reconstruction",
                "forward_request_materialization",
                "renderer_validation",
                "render_once",
            ],
        )
        self.assertEqual(
            (result["request"]["app"], result["request"]["name"]),
            ("blog", "0002_render_sql"),
        )
        self.assertEqual(result["request"]["direction"], "forward")
        self.assertEqual(
            [
                operation["kind"]
                for operation in result["request"]["intent"]["operations"]
            ],
            ["CreateModel", "AddField"],
        )
        self.assertTrue(result["detached_request"])
        self.assertTrue(result["operation_order_preserved"])
        self.assertFalse(result["request_zero_value_valid"])
        failures = {case["case"]: case for case in result["failures"]}
        self.assertEqual(
            failures["invalid_unrelated_definition"]["failed_stage"],
            "complete_definition_load",
        )
        self.assertEqual(
            failures["prefix_looking_exact_miss"]["failed_stage"],
            "exact_target_lookup",
        )
        self.assertEqual(
            failures["renderer_unavailable"]["failed_stage"],
            "renderer_validation",
        )
        self.assertEqual(failures["renderer_unavailable"]["renderer_calls"], 0)
        self.assertEqual(metrics["renderer_calls"], 1)
        self.assertEqual(metrics["history_reads"], 0)
        self.assertEqual(metrics["transactions"], 0)

    def test_postgres_projection_is_schema_only_and_database_free(self) -> None:
        observation = decisions.postgres_current_projection("MIG-133")
        result = _semantic(observation["result"])
        metrics = _semantic(observation["metrics"])
        self.assertEqual(
            result["configuration"],
            {"immutable": True, "inputs": ["schema"], "schema": "public"},
        )
        self.assertEqual(
            result["forbidden_configuration_inputs"],
            [
                "database_url",
                "credential",
                "database_handle",
                "server_connection",
            ],
        )
        self.assertTrue(result["schema_qualified"])
        self.assertFalse(result["raw_sql_bytes_are_reference_contract"])
        self.assertEqual(
            [(row["kind"], row["schema"]) for row in result["normalized_operations"]],
            [("create_table", "public"), ("add_column", "public")],
        )
        for field in (
            "backend_opens",
            "catalog_reads",
            "credential_values",
            "history_reads",
            "network_calls",
            "server_profile_reads",
        ):
            self.assertEqual(metrics[field], 0)

    def test_canonical_output_is_cardinality_bound_and_byte_deterministic(self) -> None:
        observation = decisions.canonical_deterministic_output("MIG-134")
        result = _semantic(observation["result"])
        metrics = _semantic(observation["metrics"])
        self.assertEqual(len(result["bodies"]), 2)
        self.assertTrue(all(";" not in body for body in result["bodies"]))
        outputs = [case["output"] for case in result["observations"]]
        self.assertEqual(len(set(outputs)), 1)
        self.assertEqual(
            outputs[0],
            "".join(f"{body};\n" for body in result["bodies"]),
        )
        self.assertEqual(result["global_terminator"], ";\n")
        self.assertEqual(
            result["empty_intent"],
            {
                "internal_result": "non_nil_empty",
                "output": "",
                "stdout_write_attempts": 0,
            },
        )
        self.assertEqual(metrics["operations"], metrics["statements"])
        self.assertEqual(metrics["distinct_nonempty_outputs"], 1)

    def test_success_failure_and_cancel_have_zero_database_lifecycle_calls(
        self,
    ) -> None:
        observation = decisions.database_and_history_zero_calls("MIG-135")
        result = _semantic(observation["result"])
        db_state = _semantic(observation["db_state"])
        metrics = _semantic(observation["metrics"])
        self.assertEqual(
            [case["case"] for case in result["cases"]],
            ["success", "render_failure", "canceled"],
        )
        self.assertFalse(result["custom_renderer_io_is_proven_absent"])
        self.assertFalse(result["offline_or_sandboxed_claimed"])
        self.assertEqual(db_state["before"], db_state["after"])
        for case in result["cases"]:
            for field in (
                "backend_opens",
                "history_reads",
                "migration_begins",
                "recorder_calls",
                "revision_fence_calls",
                "schema_editor_calls",
                "schema_mutations",
                "session_opens",
                "transaction_begins",
            ):
                self.assertEqual(case[field], 0)
                self.assertEqual(metrics[field], 0)

    def test_nil_render_failure_unsupported_and_malformed_results_fail_before_publication(
        self,
    ) -> None:
        observation = decisions.renderer_and_operation_fail_closed("MIG-136")
        result = _semantic(observation["result"])
        metrics = _semantic(observation["metrics"])
        cases = {case["case"]: case for case in result["cases"]}
        self.assertEqual(
            list(cases),
            [
                "nil_renderer",
                "typed_nil_renderer",
                "unsupported_operation",
                "custom_data_operation",
                "renderer_returned_error",
                "malformed_empty_body",
                "malformed_invalid_utf8_body",
                "malformed_leading_ascii_whitespace_body",
                "malformed_trailing_ascii_whitespace_body",
                "malformed_semicolon_body",
                "malformed_control_rune_body",
                "malformed_cardinality",
            ],
        )
        self.assertEqual(cases["typed_nil_renderer"]["renderer_calls"], 0)
        self.assertEqual(cases["typed_nil_renderer"]["exit_code"], 3)
        self.assertEqual(cases["unsupported_operation"]["exit_code"], 1)
        self.assertEqual(cases["custom_data_operation"]["exit_code"], 1)
        self.assertEqual(cases["renderer_returned_error"]["code"], "render_failed")
        self.assertTrue(
            cases["renderer_returned_error"]["partial_renderer_sql_returned"]
        )
        self.assertTrue(cases["renderer_returned_error"]["raw_cause_contains_secret"])
        for name in (
            "malformed_empty_body",
            "malformed_invalid_utf8_body",
            "malformed_leading_ascii_whitespace_body",
            "malformed_trailing_ascii_whitespace_body",
            "malformed_semicolon_body",
            "malformed_control_rune_body",
            "malformed_cardinality",
        ):
            self.assertEqual(cases[name]["code"], "invalid_rendered_sql")
        for case in cases.values():
            self.assertEqual(case["logical_sql_bytes_published"], 0)
            self.assertFalse(case["partial_renderer_sql_published"])
            self.assertFalse(case["raw_cause_retained"])
            self.assertFalse(case["unwrap_exposes_raw_cause"])
        self.assertEqual(metrics["cases"], 12)
        self.assertEqual(metrics["logical_sql_bytes_published"], 0)
        self.assertEqual(metrics["typed_nil_method_calls"], 0)
        self.assertEqual(result["reverse_argv_owned_by"], "MIG-129")

    def test_resource_limits_cleanup_redaction_and_write_nonclaim_are_exact(
        self,
    ) -> None:
        observation = decisions.resource_cleanup_redaction_and_write("MIG-137")
        result = _semantic(observation["result"])
        metrics = _semantic(observation["metrics"])
        cases = {case["case"]: case for case in result["resource_cases"]}
        self.assertEqual(cases["statement_count_exact_limit"]["limit"], 2_048)
        self.assertTrue(cases["statement_count_exact_limit"]["accepted"])
        self.assertEqual(
            cases["statement_count_one_over"]["observed"],
            cases["statement_count_one_over"]["limit"] + 1,
        )
        self.assertEqual(
            cases["aggregate_body_bytes_exact_limit"]["limit"],
            16 << 20,
        )
        self.assertEqual(
            cases["aggregate_body_bytes_one_over"]["observed"],
            cases["aggregate_body_bytes_one_over"]["limit"] + 1,
        )
        self.assertEqual(
            cases["private_response_exact_limit"]["limit"],
            101 << 20,
        )
        self.assertEqual(metrics["one_over_rejections"], 3)
        self.assertEqual(result["scan_order"], ["resource_bounds", "semantic_shape"])
        self.assertTrue(result["logical_output_validated_before_write"])
        self.assertFalse(result["os_atomic_write_claimed"])
        self.assertTrue(result["child_cleanup"]["bounded"])
        self.assertTrue(result["child_cleanup"]["process_group_absence_verified"])
        self.assertTrue(all(not row["published"] for row in result["redaction"]))
        writes = {case["case"]: case for case in result["write_cases"]}
        self.assertFalse(writes["success"]["physical_prefix_may_be_visible"])
        self.assertTrue(writes["short_write"]["physical_prefix_may_be_visible"])
        self.assertTrue(writes["write_error"]["physical_prefix_may_be_visible"])
        self.assertEqual(metrics["automatic_retries"], 0)
        self.assertEqual(metrics["stderr_republications"], 0)

    def test_external_project_configuration_is_public_and_bounded(self) -> None:
        observation = decisions.external_project_configuration("MIG-138")
        result = _semantic(observation["result"])
        db_state = _semantic(observation["db_state"])
        metrics = _semantic(observation["metrics"])
        self.assertEqual(result["direct_project_config_field"], "MigrationSQLRenderer")
        self.assertEqual(result["sqlite_constructor_inputs"], [])
        self.assertEqual(result["postgres_constructor_inputs"], ["schema"])
        self.assertTrue(
            result["renderer_and_opener_derived_from_one_builtin_selection"]
        )
        self.assertTrue(result["supported_builtin_renderer_db_free"])
        self.assertFalse(result["custom_opener_renderer_coherence_proven"])
        self.assertTrue(
            all(case["repository_external"] for case in result["compile_cases"])
        )
        self.assertEqual(db_state["before"], db_state["after"])
        for field in (
            "backend_opens",
            "credential_values",
            "database_handles",
            "history_reads",
            "network_calls",
            "schema_editor_calls",
        ):
            self.assertEqual(metrics[field], 0)

    def test_observations_are_deterministic_and_contract_id_agnostic(self) -> None:
        for contract_id, scenario in zip(
            CONTRACT_IDS,
            decisions.SCENARIOS.values(),
            strict=True,
        ):
            with self.subTest(contract=contract_id):
                first = scenario(contract_id)
                second = scenario(contract_id)
                arbitrary = scenario("untrusted-contract")
                self.assertEqual(canonical_json(first), canonical_json(second))
                self.assertEqual(arbitrary["id"], "untrusted-contract")
                self.assertEqual(
                    {key: value for key, value in first.items() if key != "id"},
                    {key: value for key, value in arbitrary.items() if key != "id"},
                )
                self.assertLess(len(canonical_json(first)), 16 * 1024)

    def test_decision_source_is_artifact_blind_and_has_no_io_or_django(self) -> None:
        source = inspect.getsource(decisions)
        syntax = ast.parse(source)
        imported_roots = {
            alias.name.split(".", 1)[0]
            for node in ast.walk(syntax)
            if isinstance(node, ast.Import)
            for alias in node.names
        }
        imported_roots.update(
            node.module.split(".", 1)[0]
            for node in ast.walk(syntax)
            if isinstance(node, ast.ImportFrom)
            and node.module is not None
            and node.level == 0
        )
        called_names = {
            node.func.id
            for node in ast.walk(syntax)
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
        }
        called_attributes = {
            node.func.attr
            for node in ast.walk(syntax)
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
        }
        for forbidden in (
            "conformance/contracts",
            "conformance/oracles",
            "conformance/fixtures",
            "not_implemented",
            "not-implemented",
        ):
            self.assertNotIn(forbidden, source)
        self.assertNotIn("django", imported_roots)
        self.assertTrue(
            {
                "open",
                "read_bytes",
                "read_text",
                "write_bytes",
                "write_text",
                "run",
                "Popen",
            }.isdisjoint(called_names | called_attributes)
        )
        self.assertTrue({"eval", "exec", "compile"}.isdisjoint(called_names))


if __name__ == "__main__":
    unittest.main()
