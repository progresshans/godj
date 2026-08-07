from __future__ import annotations

import unittest
from typing import Any
from unittest.mock import patch

from django.db import connection

from conformance.runners.django import query_cache_scenarios as scenarios
from conformance.runners.django import scenarios as base_scenarios


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
    raise AssertionError(f"unsupported test value kind {kind!r}")


class QueryCacheScenarioTests(unittest.TestCase):
    expected_query_counts = {
        "QRY-011": [1, 0],
        "QRY-012": [1, 0],
        "QRY-013": [1, 0, 1],
        "QRY-014": [1, 1, 0],
        "QRY-015": [1, 1, 0],
        "QRY-016": [1, 1, 0],
        "QRY-017": [1, 1, 0],
        "QRY-018": [1, 1, 1, 0],
        "QRY-019": [1, 1, 0],
        "QRY-020": [1, 0, 1, 0],
        "QRY-021": [1, 1, 1, 0],
    }

    def run_scenarios(self) -> dict[str, dict[str, Any]]:
        observations: dict[str, dict[str, Any]] = {}
        for index, scenario in enumerate(scenarios.SCENARIOS.values(), 11):
            contract_id = f"QRY-{index:03d}"
            observation = scenario(contract_id)
            self.assertEqual(observation["id"], contract_id)
            self.assertEqual(observation["status"], "observed")
            self.assertEqual(observation["phase"], "evaluation")
            self.assertIsNone(observation["error"])
            observations[contract_id] = {
                "result": _decode(observation["result"]),
                "db_state": _decode(observation["db_state"]),
                "metrics": _decode(observation["metrics"]),
            }
        return observations

    def test_step_results_and_real_query_windows_lock_cache_boundaries(self) -> None:
        observations = self.run_scenarios()

        for contract_id, observation in observations.items():
            with self.subTest(contract=contract_id):
                result_steps = observation["result"]["steps"]
                metric_steps = observation["metrics"]["steps"]
                self.assertEqual(
                    [step["name"] for step in result_steps],
                    [step["name"] for step in metric_steps],
                )
                counts = [step["query_count"] for step in metric_steps]
                self.assertEqual(counts, self.expected_query_counts[contract_id])
                self.assertEqual(
                    [step["statement_kinds"] for step in metric_steps],
                    [["SELECT"] if count else [] for count in counts],
                )

        steps = observations["QRY-011"]["result"]["steps"]
        self.assertEqual(steps[0]["value"], steps[1]["value"])

        steps = observations["QRY-012"]["result"]["steps"]
        self.assertEqual(steps[0]["value"], [])
        self.assertEqual(steps[1]["value"], [])
        self.assertEqual(len(observations["QRY-012"]["db_state"]["articles"]), 5)

        steps = observations["QRY-013"]["result"]["steps"]
        self.assertEqual(steps[0]["value"], steps[1]["value"])
        self.assertEqual(len(steps[2]["value"]), 5)

        steps = observations["QRY-014"]["result"]["steps"]
        self.assertEqual(steps[0]["value"], steps[2]["value"])
        self.assertEqual([row["id"] for row in steps[1]["value"]], [3, 5])

        steps = observations["QRY-015"]["result"]["steps"]
        self.assertEqual(steps[0]["value"], 4)
        self.assertEqual(len(steps[1]["value"]), 5)
        self.assertEqual(steps[2]["value"], 5)

        steps = observations["QRY-016"]["result"]["steps"]
        self.assertFalse(steps[0]["value"])
        self.assertEqual([row["id"] for row in steps[1]["value"]], [5])
        self.assertTrue(steps[2]["value"])

        steps = observations["QRY-017"]["result"]["steps"]
        self.assertEqual(steps[0]["value"], steps[2]["value"])
        self.assertEqual(len(steps[1]["value"]), 5)

        steps = observations["QRY-018"]["result"]["steps"]
        self.assertEqual(steps[0]["value"]["id"], 4)
        self.assertEqual(steps[1]["value"]["id"], 5)
        self.assertEqual(steps[2]["value"][0]["id"], 5)
        self.assertEqual(steps[3]["value"]["id"], 5)
        self.assertEqual(len(observations["QRY-018"]["db_state"]["articles"]), 6)

        steps = observations["QRY-019"]["result"]["steps"]
        self.assertEqual(
            steps[0]["error"],
            {"category": "backend_error", "code": "missing_table"},
        )
        self.assertEqual(steps[1]["value"], steps[2]["value"])

        steps = observations["QRY-020"]["result"]["steps"]
        self.assertTrue(steps[1]["value"]["completed"])
        self.assertEqual(len(steps[2]["value"]), 5)
        self.assertEqual(steps[0]["value"], steps[3]["value"])

        steps = observations["QRY-021"]["result"]["steps"]
        self.assertEqual(steps[0]["value"]["id"], 4)
        self.assertEqual(steps[1]["value"]["id"], 5)
        self.assertEqual(steps[2]["value"][0]["id"], 5)
        self.assertEqual(steps[3]["value"]["id"], 5)

    def test_capture_records_actual_statement_count_and_kind(self) -> None:
        def two_queries() -> tuple[int, int]:
            with connection.cursor() as cursor:
                first = cursor.execute("SELECT 1").fetchone()[0]
                second = cursor.execute("SELECT 2").fetchone()[0]
            return first, second

        result, metrics = scenarios._capture(two_queries)
        self.assertEqual(result, (1, 2))
        self.assertEqual(metrics["query_count"], 2)
        self.assertEqual(metrics["statement_kinds"], ["SELECT", "SELECT"])

        result, metrics = scenarios._capture(lambda: "no I/O")
        self.assertEqual(result, "no I/O")
        self.assertEqual(metrics, {"query_count": 0, "statement_kinds": []})

    def test_each_scenario_executes_its_declared_capture_steps(self) -> None:
        expected_calls = {
            "QRY-011": (2, 0),
            "QRY-012": (2, 0),
            "QRY-013": (3, 0),
            "QRY-014": (3, 0),
            "QRY-015": (3, 0),
            "QRY-016": (3, 0),
            "QRY-017": (3, 0),
            "QRY-018": (4, 0),
            "QRY-019": (2, 1),
            "QRY-020": (4, 0),
            "QRY-021": (4, 0),
        }

        for index, scenario in enumerate(scenarios.SCENARIOS.values(), 11):
            contract_id = f"QRY-{index:03d}"
            capture_index = 0
            original_capture = scenarios._capture
            original_missing_table = scenarios._capture_missing_table

            def instrument_capture(operation):
                nonlocal capture_index
                result, metrics = original_capture(operation)
                capture_index += 1
                metrics["capture_token"] = f"capture-{capture_index}"
                return result, metrics

            def instrument_missing_table(operation):
                nonlocal capture_index
                result, metrics = original_missing_table(operation)
                capture_index += 1
                metrics["capture_token"] = f"missing-table-{capture_index}"
                return result, metrics

            with (
                self.subTest(contract=contract_id),
                patch.object(
                    scenarios,
                    "_capture",
                    side_effect=instrument_capture,
                ) as capture,
                patch.object(
                    scenarios,
                    "_capture_missing_table",
                    side_effect=instrument_missing_table,
                ) as missing_table,
            ):
                observation = scenario(contract_id)

            self.assertEqual(
                (capture.call_count, missing_table.call_count),
                expected_calls[contract_id],
            )
            metric_steps = _decode(observation["metrics"])["steps"]
            self.assertEqual(
                len({step["capture_token"] for step in metric_steps}),
                len(metric_steps),
            )

    def test_live_fixture_changes_propagate_to_every_scenario(self) -> None:
        sentinel_title = "live fixture sentinel"
        fixture = tuple(
            scenarios.Article(
                id=row.id,
                title=sentinel_title if row.id == 1 else row.title,
                published=row.published,
                summary=row.summary,
            )
            for row in scenarios.FIXTURES
        )

        with (
            patch.object(base_scenarios, "FIXTURES", fixture),
            patch.object(scenarios, "FIXTURES", fixture),
        ):
            for index, scenario in enumerate(scenarios.SCENARIOS.values(), 11):
                contract_id = f"QRY-{index:03d}"
                with self.subTest(contract=contract_id):
                    observation = scenario(contract_id)
                    rows = _decode(observation["db_state"])["articles"]
                    sentinel = next(row for row in rows if row["id"] == 1)
                    self.assertEqual(sentinel["title"], sentinel_title)

    def test_observation_rejects_result_metric_step_mismatch(self) -> None:
        with self.assertRaisesRegex(AssertionError, "do not match"):
            scenarios._observed(
                "QRY-999",
                [scenarios._result_step("result", True)],
                [
                    scenarios._metric_step(
                        "different",
                        {"query_count": 0, "statement_kinds": []},
                    )
                ],
            )


if __name__ == "__main__":
    unittest.main()
