from __future__ import annotations

import ast
import unittest
from pathlib import Path
from typing import Any

from conformance.runners.django import migration_target_plan_decisions as decisions
from conformance.runners.django.normalizer import canonical_json


EXPECTED_SCENARIOS = (
    "godj.migration.target_plan.target_argv_and_pre_io_rejection",
    "godj.migration.target_plan.target_noop_and_legacy_zero",
    "godj.migration.target_plan.plan_exact_and_no_mutation",
    "godj.migration.target_plan.preview_drift_fresh_execute",
    "godj.migration.target_plan.reverse_middle_failure_resume",
    "godj.migration.target_plan.reverse_commit_outcomes",
    "godj.migration.target_plan.project_protocol_and_ownership",
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
    return value["value"]


class MigrationTargetPlanDecisionTests(unittest.TestCase):
    def test_registry_set_slug_phases_and_shapes_are_exact(self) -> None:
        self.assertEqual(decisions.SET_SLUG, "migration-target-plan")
        self.assertEqual(tuple(decisions.SCENARIOS), EXPECTED_SCENARIOS)
        ids = (
            "MIG-119",
            "MIG-123",
            "MIG-124",
            "MIG-125",
            "MIG-126",
            "MIG-127",
            "MIG-128",
        )
        observations = [
            scenario(contract_id)
            for contract_id, scenario in zip(
                ids,
                decisions.SCENARIOS.values(),
                strict=True,
            )
        ]
        self.assertEqual(
            [observation["phase"] for observation in observations],
            [
                "environment",
                "evaluation",
                "evaluation",
                "commit",
                "rollback",
                "commit",
                "environment",
            ],
        )
        self.assertEqual(
            [observation["db_state"] is not None for observation in observations],
            [False, False, True, True, True, True, True],
        )
        self.assertTrue(
            all(observation["result"] is not None for observation in observations)
        )
        self.assertTrue(
            all(observation["metrics"] is not None for observation in observations)
        )
        self.assertTrue(
            all(observation["error"] is None for observation in observations)
        )

    def test_exact_argv_families_and_rejections_are_pre_io(self) -> None:
        observation = decisions.target_argv_and_pre_io_rejection("MIG-119")
        result = _semantic(observation["result"])
        metrics = _semantic(observation["metrics"])
        self.assertEqual(result["exact_public_families"], 8)
        self.assertEqual(len(result["accepted"]), 8)
        self.assertEqual(
            [case["argv"] for case in result["accepted"]],
            [
                ["migrate"],
                ["migrate", "--project", "./godj.toml"],
                ["migrate", "--plan"],
                ["migrate", "--plan", "--project", "./godj.toml"],
                ["migrate", "blog", "0001_article"],
                ["migrate", "blog", "0001_article", "--project", "./godj.toml"],
                ["migrate", "blog", "0001_article", "--plan"],
                [
                    "migrate",
                    "blog",
                    "0001_article",
                    "--plan",
                    "--project",
                    "./godj.toml",
                ],
            ],
        )
        for case in result["rejected"]:
            self.assertEqual(case["category"], "migration_project_command_error")
            self.assertEqual(case["code"], "invalid_arguments")
            self.assertEqual(case["project_discoveries"], 0)
            self.assertEqual(case["builds"], 0)
            self.assertEqual(case["backend_opens"], 0)
        self.assertEqual(
            result["post_discovery_rejections"],
            [
                {
                    "argv": ["migrate", "blog", "0001"],
                    "backend_opens": 1,
                    "builds": 1,
                    "case": "prefix_looking_exact_miss",
                    "catalog_exact_name": "0001_initial",
                    "category": "migration_plan_error",
                    "code": "target_not_found",
                    "history_reads": 1,
                    "migration_begins": 0,
                    "project_discoveries": 1,
                    "requested_name": "0001",
                }
            ],
        )
        self.assertEqual(metrics["project_discoveries_for_rejected"], 0)
        self.assertEqual(metrics["builds_for_rejected"], 0)
        self.assertEqual(metrics["backend_opens_for_rejected"], 0)
        self.assertEqual(metrics["post_discovery_target_not_found_cases"], 1)

    def test_noop_plan_and_fresh_execute_decisions_are_non_authoritative(self) -> None:
        noop = _semantic(decisions.target_noop_and_legacy_zero("MIG-123")["result"])
        noop_cases = {case["case"]: case for case in noop["cases"]}
        self.assertEqual(noop_cases["applied_named_leaf"]["plan"], [])
        self.assertEqual(noop_cases["legacy_zero_unknown_app"]["plan"], [])
        self.assertEqual(
            (
                noop_cases["public_known_zero_unknown_app"]["category"],
                noop_cases["public_known_zero_unknown_app"]["code"],
            ),
            ("migration_plan_error", "target_not_found"),
        )

        exact = decisions.plan_exact_and_no_mutation("MIG-124")
        exact_result = _semantic(exact["result"])
        exact_state = _semantic(exact["db_state"])
        exact_metrics = _semantic(exact["metrics"])
        self.assertEqual(
            [case["stdout"] for case in exact_result["cases"]],
            [
                (
                    '{"plan":[{"app":"blog","name":"0002_editor",'
                    '"direction":"backward"}]}\n'
                ),
                '{"plan":[]}\n',
            ],
        )
        self.assertFalse(exact_result["plan_is_execution_authority"])
        self.assertTrue(
            all(case["before"] == case["after"] for case in exact_state["cases"])
        )
        for field in (
            "application_mutations",
            "migration_begins",
            "recorder_mutations",
            "revision_mutations",
            "schema_mutations",
        ):
            self.assertEqual(exact_metrics[field], 0)

        drift = decisions.preview_drift_fresh_execute("MIG-125")
        drift_result = _semantic(drift["result"])
        drift_state = _semantic(drift["db_state"])
        drift_metrics = _semantic(drift["metrics"])
        self.assertEqual(len(drift_result["preview_plan"]), 1)
        self.assertEqual(drift_result["execute_plan"], [])
        self.assertFalse(drift_result["preview_token_accepted"])
        self.assertTrue(drift_result["replanned_from_fresh_history"])
        self.assertEqual(drift_state["preview_mutations"], 0)
        self.assertEqual(drift_metrics["preview_migration_begins"], 0)

    def test_reverse_failure_and_commit_outcomes_are_case_local(self) -> None:
        resume = decisions.reverse_middle_failure_resume("MIG-126")
        resume_result = _semantic(resume["result"])
        resume_state = _semantic(resume["db_state"])
        resume_metrics = _semantic(resume["metrics"])
        resume_cases = {case["case"]: case for case in resume_result["cases"]}
        self.assertEqual(
            resume_cases["first_process"],
            {
                "case": "first_process",
                "category": "migration_execution_error",
                "code": "operation_failed",
                "committed": ["blog.0004_archive"],
                "plan": [
                    "blog.0004_archive",
                    "blog.0003_publish",
                    "blog.0002_editor",
                ],
                "rolled_back": ["blog.0003_publish"],
                "unstarted": ["blog.0002_editor"],
            },
        )
        self.assertEqual(
            resume_cases["fresh_resume"],
            {
                "case": "fresh_resume",
                "category": None,
                "code": None,
                "committed": ["blog.0003_publish", "blog.0002_editor"],
                "plan": ["blog.0003_publish", "blog.0002_editor"],
                "rolled_back": [],
                "unstarted": [],
            },
        )
        self.assertFalse(resume_result["unstarted_tail_started"])
        self.assertEqual(
            resume_state,
            {
                "after_failure_history": [
                    "blog.0001_article",
                    "blog.0002_editor",
                    "blog.0003_publish",
                ],
                "after_resume_history": ["blog.0001_article"],
                "durable_prefix_preserved": True,
                "initial_history": [
                    "blog.0001_article",
                    "blog.0002_editor",
                    "blog.0003_publish",
                    "blog.0004_archive",
                ],
                "rolled_back_step_preserved": True,
                "unstarted_tail_preserved": True,
            },
        )
        self.assertEqual(
            resume_metrics,
            {
                "automatic_retries": 0,
                "first_process_reverse_commits": 1,
                "first_process_reverse_rollbacks": 1,
                "first_process_unstarted_steps": 1,
                "fresh_processes": 1,
                "fresh_resume_reverse_commits": 2,
                "fresh_resume_reverse_rollbacks": 0,
                "reverse_commits": 3,
                "reverse_rollbacks": 1,
                "started_steps": 4,
                "unstarted_steps": 1,
            },
        )

        outcomes = decisions.reverse_commit_outcomes("MIG-127")
        outcome_result = _semantic(outcomes["result"])
        outcome_metrics = _semantic(outcomes["metrics"])
        cases = {case["case"]: case for case in outcome_result["cases"]}
        self.assertEqual(
            cases["commit_outcome_unknown"]["code"],
            "commit_outcome_unknown",
        )
        self.assertEqual(cases["commit_outcome_unknown"]["automatic_retries"], 0)
        self.assertEqual(cases["commit_outcome_unknown"]["rollback_after_outcome"], 0)
        self.assertEqual(
            cases["committed_cleanup_failure"]["history"],
            "committed_successor",
        )
        self.assertEqual(outcome_metrics["automatic_retries"], 0)
        self.assertIsNone(outcomes["error"])

    def test_protocol_decision_is_current_only_owned_and_redacted(self) -> None:
        observation = decisions.project_protocol_and_ownership("MIG-128")
        result = _semantic(observation["result"])
        state = _semantic(observation["db_state"])
        metrics = _semantic(observation["metrics"])

        self.assertEqual(
            set(result),
            {
                "cases",
                "current_private_protocol_version",
                "identity_normalization",
                "legacy_private_reader",
                "load_before_backend_open",
                "plan_invariants",
                "private_argument",
                "raw_causes_published",
                "redaction",
                "resource_limits",
                "result_union_bound_to_mode",
                "valid_replacement_rune_preserved",
                "wire_rejections",
            },
        )
        cases = {case["case"]: case for case in result["cases"]}
        self.assertEqual(result["current_private_protocol_version"], 2)
        self.assertEqual(
            result["private_argument"], "__godj_project_migrate_runner_v2"
        )
        self.assertFalse(result["legacy_private_reader"])
        self.assertTrue(result["result_union_bound_to_mode"])
        self.assertTrue(result["load_before_backend_open"])
        self.assertFalse(result["raw_causes_published"])
        self.assertEqual(result["identity_normalization"], "none")
        self.assertTrue(result["valid_replacement_rune_preserved"])
        self.assertEqual(
            result["plan_invariants"],
            {
                "closed_directions": ["forward", "backward"],
                "duplicate_identity": "rejected",
                "maximum_unique_rows": 2_048,
                "mixed_direction": "rejected",
                "row_order": "preserved",
            },
        )
        self.assertEqual(
            list(cases),
            [
                "execute_success",
                "plan_success",
                "outer_close_failure",
                "cancellation_cleanup",
                "partial_output_non_publication",
                "terminal_short_write",
            ],
        )
        for name in ("execute_success", "plan_success"):
            self.assertEqual(cases[name]["backend_opens"], 1)
            self.assertEqual(cases[name]["lifecycle_calls"], 1)
            self.assertEqual(cases[name]["backend_closes"], 1)
        self.assertTrue(cases["execute_success"]["public_result_published"])
        self.assertTrue(cases["plan_success"]["public_plan_published"])
        self.assertTrue(cases["outer_close_failure"]["cleanup_failed"])
        self.assertFalse(cases["outer_close_failure"]["public_plan_published"])
        self.assertEqual(
            cases["cancellation_cleanup"],
            {
                "case": "cancellation_cleanup",
                "category": "migration_project_process_error",
                "child_started": True,
                "cleanup_failed": False,
                "code": "project_canceled",
                "direct_reaps": 1,
                "mode": "plan",
                "partial_response_republished": False,
                "process_group_terminations": 1,
                "process_groups_remaining": 0,
                "public_plan_published": False,
                "sigint_attempts_maximum": 1,
                "sigkill_attempts_maximum": 1,
            },
        )
        self.assertEqual(
            cases["partial_output_non_publication"],
            {
                "case": "partial_output_non_publication",
                "category": "migration_project_protocol_error",
                "child_started": True,
                "cleanup_failed": False,
                "code": "invalid_project_migrate_runner_response",
                "complete_private_documents": 0,
                "direct_reaps": 1,
                "mode": "plan",
                "partial_private_chunks": 1,
                "partial_response_republished": False,
                "public_plan_published": False,
            },
        )
        self.assertEqual(
            cases["terminal_short_write"],
            {
                "backend_closes": 1,
                "backend_opens": 1,
                "case": "terminal_short_write",
                "category": "migration_project_internal_error",
                "code": "project_internal_error",
                "lifecycle_calls": 1,
                "mode": "plan",
                "private_response_write_attempts": 1,
                "private_response_writes_completed": 0,
                "public_plan_published": False,
            },
        )

        expected_rejections = [
            {
                "accepted": False,
                "boundary": boundary,
                "case": f"{boundary}_{name}",
                "category": "migration_project_protocol_error",
                "code": (
                    "invalid_project_migrate_runner_request"
                    if boundary == "request"
                    else "invalid_project_migrate_runner_response"
                ),
            }
            for boundary in ("request", "response")
            for name in (
                "duplicate_key",
                "unknown_key",
                "trailing_bytes",
                "noncanonical_number",
                "invalid_utf8",
                "unpaired_utf16_surrogate",
            )
        ]
        expected_rejections.extend(
            [
                {
                    "accepted": False,
                    "boundary": "request",
                    "case": "request_retired_protocol_version",
                    "category": "migration_project_protocol_error",
                    "code": "project_migrate_protocol_incompatible",
                },
                {
                    "accepted": False,
                    "boundary": "response",
                    "case": "response_mode_result_mismatch",
                    "category": "migration_project_protocol_error",
                    "code": "invalid_project_migrate_runner_response",
                },
                {
                    "accepted": False,
                    "boundary": "request",
                    "case": "request_invalid_mode",
                    "category": "migration_project_protocol_error",
                    "code": "invalid_project_migrate_runner_request",
                },
                {
                    "accepted": False,
                    "boundary": "request",
                    "case": "request_invalid_target_kind",
                    "category": "migration_project_protocol_error",
                    "code": "invalid_project_migrate_runner_request",
                },
                {
                    "accepted": False,
                    "boundary": "response",
                    "case": "response_invalid_direction",
                    "category": "migration_project_protocol_error",
                    "code": "invalid_project_migrate_runner_response",
                },
            ]
        )
        self.assertEqual(result["wire_rejections"], expected_rejections)
        self.assertEqual(
            result["resource_limits"],
            [
                {
                    "boundary": "request",
                    "case": "request_bytes",
                    "maximum": 16_777_216,
                    "overflow": "rejected",
                    "unit": "bytes",
                },
                {
                    "boundary": "response",
                    "case": "response_bytes",
                    "maximum": 105_906_176,
                    "overflow": "rejected",
                    "unit": "bytes",
                },
                {
                    "boundary": "request_and_response",
                    "case": "identity_bytes",
                    "maximum": 1_048_576,
                    "overflow": "rejected",
                    "unit": "bytes",
                },
                {
                    "boundary": "request_and_response",
                    "case": "identity_aggregate_bytes",
                    "maximum": 16_777_216,
                    "overflow": "rejected",
                    "unit": "bytes",
                },
                {
                    "boundary": "response",
                    "case": "plan_rows",
                    "maximum": 2_048,
                    "overflow": "rejected",
                    "unit": "rows",
                },
            ],
        )
        self.assertEqual(
            result["redaction"],
            {
                "published_raw_causes": 0,
                "published_secret_values": 0,
                "sensitive_classes": [
                    "backend_dsn",
                    "raw_error_cause",
                    "runner_stderr",
                ],
            },
        )
        self.assertEqual(
            state,
            {
                "canceled_process_groups_remaining": 0,
                "failed_plan_published": False,
                "partial_response_republished": False,
                "plan_mutations": 0,
                "secret_values_published": 0,
            },
        )
        self.assertEqual(
            metrics,
            {
                "automatic_retries": 0,
                "cancellation_direct_reaps": 1,
                "cancellation_process_group_terminations": 1,
                "legacy_reader_paths": 0,
                "ownership_cases": 6,
                "partial_responses_republished": 0,
                "raw_secret_occurrences": 0,
                "resource_limit_cases": 5,
                "strict_wire_rejection_cases": 17,
                "successful_mode_calls": 2,
            },
        )

    def test_decisions_are_deterministic_bounded_and_independent(self) -> None:
        contract_ids = (
            "MIG-119",
            "MIG-123",
            "MIG-124",
            "MIG-125",
            "MIG-126",
            "MIG-127",
            "MIG-128",
        )
        first = canonical_json(
            [
                scenario(contract_id)
                for contract_id, scenario in zip(
                    contract_ids,
                    decisions.SCENARIOS.values(),
                    strict=True,
                )
            ]
        )
        second = canonical_json(
            [
                scenario(contract_id)
                for contract_id, scenario in zip(
                    contract_ids,
                    decisions.SCENARIOS.values(),
                    strict=True,
                )
            ]
        )
        self.assertEqual(first, second)
        self.assertLess(len(first), 64 * 1024)

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
            "sqlite3",
            "not_implemented",
            "not-implemented",
        ):
            self.assertNotIn(forbidden, source)


if __name__ == "__main__":
    unittest.main()
