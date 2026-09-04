from __future__ import annotations

import ast
import inspect
import unittest

from conformance.runners.django import migration_status_decisions as decisions
from conformance.runners.django.normalizer import canonical_json


EXPECTED_SCENARIOS = (
    "godj.migration.status.empty_catalog",
    "godj.migration.status.unknown_record_visible",
    "godj.migration.status.inconsistent_known_history",
    "godj.migration.status.project_boundary",
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


class MigrationStatusDecisionTests(unittest.TestCase):
    def test_registry_is_exact_and_phases_are_bounded(self) -> None:
        self.assertEqual(tuple(decisions.SCENARIOS), EXPECTED_SCENARIOS)
        self.assertEqual(
            [
                decisions.empty_catalog("MIG-111")["phase"],
                decisions.unknown_record_visible("MIG-116")["phase"],
                decisions.inconsistent_known_history("MIG-117")["phase"],
                decisions.project_boundary("MIG-118")["phase"],
            ],
            ["evaluation", "evaluation", "evaluation", "environment"],
        )

    def test_empty_and_unknown_are_visible_read_only_snapshots(self) -> None:
        empty = decisions.empty_catalog("MIG-111")
        empty_result = denormalize(empty["result"])
        empty_state = denormalize(empty["db_state"])
        empty_metrics = denormalize(empty["metrics"])
        self.assertEqual(empty_result["stdout"], "(no migrations)\n")
        self.assertEqual(empty_result["rows"], [])
        self.assertTrue(empty_result["point_in_time_snapshot"])
        self.assertEqual(empty_state["history"], "empty")
        self.assertEqual(empty_metrics["backend_opens"], 1)
        self.assertEqual(empty_metrics["revision_session_opens"], 1)
        self.assertEqual(empty_metrics["applied_history_reads"], 1)
        self.assertEqual(empty_metrics["revision_session_closes"], 1)
        self.assertEqual(empty_metrics["backend_closes"], 1)

        unknown = decisions.unknown_record_visible("MIG-116")
        unknown_result = denormalize(unknown["result"])
        self.assertEqual(
            [row["status"] for row in unknown_result["rows"]],
            ["applied", "applied", "unapplied", "unknown", "unknown", "unknown"],
        )
        self.assertEqual(
            [row["name"] for row in unknown_result["recorded_unknown_input_order"]],
            ["9999_removed", "0000_removed", "0001_gone"],
        )
        self.assertEqual(
            [
                row["name"]
                for row in unknown_result["rows"]
                if row["app"] == "blog" and row["status"] == "unknown"
            ],
            ["0000_removed", "9999_removed"],
        )
        self.assertTrue(unknown_result["known_rows_preserved"])
        self.assertTrue(unknown_result["unknown_only_apps_visible"])
        self.assertTrue(unknown_result["unknown_tail_names_sorted"])
        self.assertIn(" [?] 0000_removed\n [?] 9999_removed\n", unknown_result["stdout"])
        self.assertIn("legacy\n [?] 0001_gone\n", unknown_result["stdout"])

        for observation in (empty, unknown):
            state = denormalize(observation["db_state"])
            metrics = denormalize(observation["metrics"])
            for field in (
                "application_mutations",
                "recorder_mutations",
                "revision_mutations",
                "schema_mutations",
            ):
                self.assertEqual(state[field], 0)
                self.assertEqual(metrics[field], 0)

    def test_inconsistent_known_history_fails_before_stdout_and_mutation(self) -> None:
        observation = decisions.inconsistent_known_history("MIG-117")
        self.assertIsNone(observation["result"])
        self.assertEqual(
            observation["error"],
            {
                "category": "migration_history_error",
                "code": "inconsistent_applied_history",
                "message_is_contract": False,
            },
        )
        state = denormalize(observation["db_state"])
        metrics = denormalize(observation["metrics"])
        self.assertEqual(metrics["stdout_writes"], 0)
        self.assertEqual(metrics["stderr_writes"], 1)
        self.assertEqual(metrics["migration_begins"], 0)
        self.assertEqual(metrics["applied_history_reads"], 1)
        self.assertEqual(metrics["revision_session_closes"], 1)
        self.assertEqual(metrics["backend_closes"], 1)
        for field in (
            "application_mutations",
            "schema_mutations",
            "recorder_mutations",
            "revision_mutations",
        ):
            self.assertEqual(state[field], 0)
            self.assertEqual(metrics[field], 0)

    def test_project_boundary_has_exact_precedence_cleanup_and_redaction_cases(self) -> None:
        observation = decisions.project_boundary("MIG-118")
        self.assertIsNotNone(observation["result"])
        self.assertIsNotNone(observation["db_state"])
        self.assertIsNotNone(observation["metrics"])
        result = denormalize(observation["result"])
        state = denormalize(observation["db_state"])
        metrics = denormalize(observation["metrics"])
        cases = {case["name"]: case for case in result["cases"]}

        self.assertEqual(
            list(cases),
            [
                "invalid_arguments",
                "invalid_definition",
                "pre_acquisition_cancel",
                "success",
                "partial_backend_acquisition",
                "partial_session_acquisition",
                "history_read_failure",
                "revision_fence_adoption_required",
                "stale_history_revision",
                "history_revision_contended",
                "history_revision_integrity",
                "session_close_failure",
                "outer_close_failure",
                "closed_snapshot_then_cancel",
                "terminal_stdout_short_write",
                "terminal_stdout_error",
            ],
        )

        self.assertEqual(
            result["failure_precedence"],
            [
                "argument_validation",
                "definition_load",
                "backend_open",
                "revision_session_open",
                "history_read",
                "revision_session_close",
                "backend_close",
                "response_publication",
            ],
        )
        self.assertEqual(cases["invalid_arguments"]["backend_open_calls"], 0)
        self.assertEqual(cases["invalid_arguments"]["project_selections"], 0)
        self.assertEqual(cases["invalid_arguments"]["build_calls"], 0)
        self.assertEqual(cases["invalid_definition"]["backend_open_calls"], 0)
        self.assertEqual(cases["pre_acquisition_cancel"]["backend_open_calls"], 0)
        for name in ("success", "closed_snapshot_then_cancel"):
            case = cases[name]
            self.assertEqual(
                (
                    case["backend_open_calls"],
                    case["session_open_calls"],
                    case["history_reads"],
                    case["session_closes"],
                    case["backend_closes"],
                ),
                (1, 1, 1, 1, 1),
            )
            self.assertTrue(case["snapshot_published"])
        self.assertEqual(cases["partial_backend_acquisition"]["backend_closes"], 1)
        self.assertEqual(cases["partial_session_acquisition"]["session_closes"], 1)
        self.assertEqual(cases["partial_session_acquisition"]["backend_closes"], 1)
        self.assertEqual(cases["history_read_failure"]["session_closes"], 1)
        self.assertEqual(cases["history_read_failure"]["backend_closes"], 1)
        self.assertTrue(cases["session_close_failure"]["cleanup_failed"])
        self.assertTrue(cases["outer_close_failure"]["cleanup_failed"])
        revision_cases = {
            "revision_fence_adoption_required": (
                "migration_capability_error",
                "revision_fence_adoption_required",
                1,
            ),
            "stale_history_revision": (
                "migration_conflict_error",
                "stale_history_revision",
                3,
            ),
            "history_revision_contended": (
                "migration_transaction_error",
                "history_revision_contended",
                3,
            ),
            "history_revision_integrity": (
                "migration_history_error",
                "history_revision_integrity",
                1,
            ),
        }
        for name, (category, code, exit_code) in revision_cases.items():
            case = cases[name]
            self.assertEqual(case["category"], category)
            self.assertEqual(case["code"], code)
            self.assertEqual(case["exit_code"], exit_code)
            self.assertEqual(case["history_reads"], 1)
            self.assertEqual(case["session_closes"], 1)
            self.assertEqual(case["backend_closes"], 1)

        short = cases["terminal_stdout_short_write"]
        terminal_error = cases["terminal_stdout_error"]
        for case in (short, terminal_error):
            self.assertEqual(case["category"], "migration_project_internal_error")
            self.assertEqual(case["code"], "project_internal_error")
            self.assertEqual(case["exit_code"], 3)
            self.assertEqual(case["automatic_retries"], 0)
            self.assertEqual(case["stderr_republications"], 0)
            self.assertEqual(case["stdout_write_attempts"], 1)
        self.assertEqual(short["partial_stdout_writes"], 1)
        self.assertEqual(short["stdout_write_errors"], 0)
        self.assertEqual(terminal_error["partial_stdout_writes"], 0)
        self.assertEqual(terminal_error["stdout_write_errors"], 1)
        self.assertTrue(result["closed_snapshot_survives_later_cancel"])
        self.assertFalse(result["private_causes_published"])
        self.assertEqual(state["application_mutations"], 0)
        self.assertTrue(state["all_cases_preserve_schema"])
        self.assertTrue(state["successful_snapshot_closed_before_publication"])
        self.assertEqual(metrics["cases"], 16)
        self.assertEqual(metrics["cleanup_failure_cases"], 2)
        self.assertEqual(metrics["successful_snapshot_cases"], 2)
        self.assertEqual(metrics["revision_fence_failure_cases"], 4)
        self.assertEqual(metrics["terminal_publication_failure_cases"], 2)
        for channel in ("artifact", "protocol", "stderr", "stdout"):
            self.assertEqual(metrics[f"{channel}_secret_occurrences"], 0)

    def test_decisions_are_byte_deterministic_and_contract_id_agnostic(self) -> None:
        contract_ids = ("MIG-111", "MIG-116", "MIG-117", "MIG-118")
        for contract_id, scenario in zip(contract_ids, decisions.SCENARIOS.values()):
            with self.subTest(scenario=scenario.__name__):
                expected = scenario(contract_id)
                repeated = scenario(contract_id)
                arbitrary = scenario("untrusted-contract")
                self.assertEqual(canonical_json(expected), canonical_json(repeated))
                self.assertLess(len(canonical_json(expected)), 32 * 1024)
                self.assertEqual(arbitrary["id"], "untrusted-contract")
                self.assertEqual(
                    {key: value for key, value in expected.items() if key != "id"},
                    {key: value for key, value in arbitrary.items() if key != "id"},
                )

    def test_source_has_no_django_artifact_io_or_process_dependency(self) -> None:
        source = inspect.getsource(decisions)
        syntax = ast.parse(source)
        imports: set[str] = set()
        called_attributes = set()
        called_names = set()
        for node in ast.walk(syntax):
            if isinstance(node, ast.Import):
                imports.update(alias.name for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module is not None:
                imports.add(node.module)
            elif isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
                called_attributes.add(node.func.attr)
            elif isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
                called_names.add(node.func.id)
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
        self.assertTrue(
            {
                "open",
                "read_bytes",
                "read_text",
                "write_bytes",
                "write_text",
                "run",
                "Popen",
            }.isdisjoint(called_attributes | called_names)
        )
        self.assertTrue({"eval", "exec", "compile"}.isdisjoint(called_names))


if __name__ == "__main__":
    unittest.main()
