from __future__ import annotations

import json
import unittest
from copy import deepcopy
from pathlib import Path
from typing import Any
from unittest.mock import patch

from conformance.querybreadth import reference
from conformance.querybreadth.reference import generate_suite
from conformance.runners.django import query_breadth_scenarios as scenarios
from conformance.runners.django import scenarios as base_scenarios
from conformance.runners.django.normalizer import canonical_json


ROOT = Path(__file__).resolve().parents[4]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = ROOT / "conformance/contracts/query-breadth-manifest.json"
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/query-breadth-oracle.json"
)
STATIC_FIXTURE = (
    ROOT / "conformance/fixtures/godj-query-breadth-not-implemented.json"
)
DJANGO_PREFIX = "django@fe0a859f537d4238cf49fca39073513206f83122:"


def _load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise AssertionError(f"{path} must contain one JSON object")
    return value


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
    raise AssertionError(f"unsupported query-breadth test value kind {kind!r}")


def _decoded(observation: dict[str, Any]) -> dict[str, Any]:
    return {
        **observation,
        "result": _decode(observation["result"]),
        "db_state": _decode(observation["db_state"]),
        "metrics": _decode(observation["metrics"]),
    }


class QueryBreadthScenarioTests(unittest.TestCase):
    expected_names = [
        "django.query.breadth.ordered_projection",
        "django.query.breadth.source_fields_outside_projection",
        "django.query.breadth.projection_cache_independence",
        "django.query.breadth.distinct_projection",
        "django.query.breadth.stable_offset_limit",
        "django.query.breadth.invalid_offset_pre_io",
        "django.query.breadth.cold_count_and_warm_cache",
        "django.query.breadth.sliced_distinct_count",
        "django.query.breadth.empty_count_and_nullable_max",
        "django.query.breadth.filtered_count_and_max",
        "django.query.breadth.terminal_failure_ownership",
        "django.query.breadth.backend_parity_reference",
    ]
    expected_phases = [
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
        "evaluation",
    ]

    def run_scenarios(self) -> dict[str, dict[str, Any]]:
        observations: dict[str, dict[str, Any]] = {}
        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 22):
            contract_id = f"QRY-{number:03d}"
            observation = scenario(contract_id)
            self.assertEqual(observation["id"], contract_id)
            self.assertEqual(observation["status"], "observed")
            self.assertEqual(
                observation["phase"],
                self.expected_phases[number - 22],
            )
            self.assertIsNone(observation["error"])
            observations[contract_id] = _decoded(observation)
        return observations

    def test_registry_manifest_and_static_fixture_lock_exact_order(self) -> None:
        manifest = _load(MANIFEST)
        static = _load(STATIC_FIXTURE)
        identifiers = [f"QRY-{number:03d}" for number in range(22, 34)]

        self.assertEqual(list(scenarios.SCENARIOS), self.expected_names)
        self.assertEqual(
            [contract["id"] for contract in manifest["contracts"]],
            identifiers,
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
            ["passing"] * 12,
        )
        self.assertIn("SQLite", manifest["contracts"][-1]["title"])
        self.assertIn("backend parity", manifest["contracts"][-1]["title"])
        self.assertNotIn("cross-model", manifest["contracts"][-1]["title"])
        self.assertNotIn("compile-time", manifest["contracts"][-1]["title"])
        self.assertEqual(
            [contract["comparison"] for contract in manifest["contracts"]],
            [["result", "db_state", "metrics"]] * 12,
        )
        self.assertEqual(
            [contract["id"] for contract in static["contracts"]],
            identifiers,
        )
        self.assertEqual(
            [contract["status"] for contract in static["contracts"]],
            ["not_implemented"] * 12,
        )
        self.assertEqual(
            [contract["phase"] for contract in static["contracts"]],
            self.expected_phases,
        )

    def test_manifest_provenance_is_pinned_and_product_decisions_are_explicit(
        self,
    ) -> None:
        manifest = _load(MANIFEST)
        for contract in manifest["contracts"]:
            decisions = [
                entry
                for entry in contract["provenance"]
                if entry["kind"] == "decision"
            ]
            self.assertEqual(
                decisions,
                [{"kind": "decision", "reference": "ADR-0039", "derived": False}],
            )
            django_entries = [
                entry
                for entry in contract["provenance"]
                if entry["kind"] != "decision"
            ]
            self.assertTrue(django_entries)
            for entry in django_entries:
                self.assertTrue(entry["reference"].startswith(DJANGO_PREFIX))
                self.assertFalse(entry["derived"])
                self.assertEqual(entry["license"], "BSD-3-Clause")

    def test_exact_projection_pagination_and_aggregate_results(self) -> None:
        observations = self.run_scenarios()

        projection = observations["QRY-022"]["result"]
        self.assertEqual(
            projection["fields"],
            ["title", "id", "summary", "published"],
        )
        self.assertEqual(
            projection["rows"],
            [
                ["Alpine Guide", 1, None, True],
                ["django Tips", 2, "ORM", False],
                ["Django Deep Dive", 3, "", True],
                ["Other", 4, None, True],
            ],
        )

        source_fields = observations["QRY-023"]["result"]
        self.assertEqual(source_fields["projection_fields"], ["id"])
        self.assertEqual(source_fields["filter_fields"], ["published"])
        self.assertEqual(source_fields["order_fields"], ["title", "id"])
        self.assertEqual(source_fields["rows"], [[1], [3], [4]])

        cache_steps = observations["QRY-024"]["result"]["steps"]
        self.assertEqual(cache_steps[0]["value"], [])
        self.assertEqual(len(cache_steps[1]["value"]), 4)
        self.assertEqual(cache_steps[2]["value"], [1, 2, 3, 4, 5])
        self.assertEqual(len(cache_steps[3]["value"]), 6)
        self.assertEqual(cache_steps[4]["value"], [1, 2, 3, 4, 5])

        self.assertEqual(
            observations["QRY-025"]["result"],
            {"fields": ["published"], "rows": [[False], [True]]},
        )
        page_steps = observations["QRY-026"]["result"]["steps"]
        self.assertEqual(
            page_steps,
            [
                {
                    "name": "offset_one_limit_two",
                    "value": [[2, "django Tips"], [3, "Django Deep Dive"]],
                },
                {"name": "out_of_range", "value": []},
            ],
        )

        offset_steps = observations["QRY-027"]["result"]["steps"]
        self.assertEqual(
            [step["value"] for step in offset_steps],
            [
                {"error": {"category": "query_error", "code": "invalid_offset"}},
                {"accepted": scenarios.MAX_OFFSET},
                {"error": {"category": "query_error", "code": "invalid_offset"}},
            ],
        )

        count_steps = observations["QRY-028"]["result"]["steps"]
        self.assertEqual(
            [step["value"] for step in count_steps],
            [4, [1, 2, 3, 4, 5], 5],
        )
        sliced_steps = observations["QRY-029"]["result"]["steps"]
        self.assertEqual(
            [step["value"] for step in sliced_steps],
            [[3, 4], 2],
        )
        empty_steps = observations["QRY-030"]["result"]["steps"]
        self.assertEqual(
            [step["value"] for step in empty_steps],
            [0, None],
        )
        self.assertEqual(
            observations["QRY-031"]["result"],
            {
                "fields": ["row_count", "latest_id", "max_summary"],
                "values": [3, 4, ""],
            },
        )

        parity_steps = observations["QRY-033"]["result"]["steps"]
        self.assertEqual(
            parity_steps,
            [
                {
                    "name": "sqlite_reference_projection",
                    "value": [[3, "Django Deep Dive", True], [4, "Other", True]],
                },
                {
                    "name": "sqlite_reference_aggregate",
                    "value": {
                        "fields": ["row_count", "latest_id"],
                        "values": [2, 4],
                    },
                },
            ],
        )

    def test_real_query_shapes_lock_count_distinct_slice_and_cache_boundaries(
        self,
    ) -> None:
        observations = self.run_scenarios()
        expected_counts = {
            "QRY-022": [1],
            "QRY-023": [1],
            "QRY-024": [1, 1, 1, 1, 0],
            "QRY-025": [1],
            "QRY-026": [1, 1],
            "QRY-027": [0, 0, 0],
            "QRY-028": [1, 1, 0],
            "QRY-029": [1, 1],
            "QRY-030": [1, 1],
            "QRY-031": [1],
            "QRY-032": [1, 1, 1, 1],
            "QRY-033": [1, 1],
        }
        for contract_id, counts in expected_counts.items():
            metrics = observations[contract_id]["metrics"]
            steps = metrics.get("steps", [metrics])
            self.assertEqual(
                [step["query_count"] for step in steps],
                counts,
                contract_id,
            )
            for step in steps:
                self.assertEqual(len(step["statements"]), step["query_count"])
                self.assertTrue(
                    all(
                        statement["statement_kind"] == "SELECT"
                        for statement in step["statements"]
                    )
                )

        distinct = observations["QRY-025"]["metrics"]["statements"][0]
        self.assertTrue(distinct["distinct"])

        for step in observations["QRY-026"]["metrics"]["steps"]:
            statement = step["statements"][0]
            self.assertTrue(statement["has_limit"])
            self.assertTrue(statement["has_offset"])

        cold_count = observations["QRY-028"]["metrics"]["steps"][0]
        self.assertEqual(
            cold_count["statements"][0]["aggregate_functions"],
            ["COUNT"],
        )
        sliced_count = observations["QRY-029"]["metrics"]["steps"][1]
        self.assertEqual(
            sliced_count["statements"][0]["aggregate_functions"],
            ["COUNT"],
        )
        self.assertTrue(sliced_count["statements"][0]["derived_table"])

        empty_metrics = observations["QRY-030"]["metrics"]["steps"]
        self.assertEqual(
            [step["statements"][0]["aggregate_functions"] for step in empty_metrics],
            [["COUNT"], ["MAX"]],
        )
        self.assertEqual(
            observations["QRY-031"]["metrics"]["statements"][0][
                "aggregate_functions"
            ],
            ["COUNT", "MAX"],
        )
        parity_aggregate = observations["QRY-033"]["metrics"]["steps"][1]
        self.assertEqual(
            parity_aggregate["statements"][0]["aggregate_functions"],
            ["COUNT", "MAX"],
        )
        self.assertTrue(parity_aggregate["statements"][0]["derived_table"])

    def test_terminal_failure_cases_attempt_close_exactly_once(self) -> None:
        observation = _decoded(scenarios.terminal_failure_ownership("QRY-032"))
        result_steps = observation["result"]["steps"]
        metric_steps = observation["metrics"]["steps"]
        self.assertEqual(
            [step["name"] for step in result_steps],
            [
                "consumer_stop",
                "decode_failure",
                "iteration_failure",
                "close_failure",
            ],
        )
        self.assertEqual(
            [step["value"] for step in result_steps],
            [
                {"first": 1, "outcome": "consumer_stopped"},
                {"error": {"category": "decode_error", "code": "conversion"}},
                {"error": {"category": "backend_error", "code": "iteration"}},
                {"error": {"category": "backend_error", "code": "close"}},
            ],
        )
        self.assertEqual(
            [step["close_attempts"] for step in metric_steps],
            [1, 1, 1, 1],
        )

    def test_live_fixture_changes_propagate_to_every_reference_scenario(self) -> None:
        sentinel_title = "query breadth live fixture sentinel"
        fixture = tuple(
            scenarios.Article(
                id=row.id,
                title=sentinel_title if row.id == 1 else row.title,
                published=row.published,
                summary=row.summary,
            )
            for row in base_scenarios.FIXTURES
        )
        with patch.object(base_scenarios, "FIXTURES", fixture):
            for number, scenario in enumerate(scenarios.SCENARIOS.values(), 22):
                with self.subTest(contract=f"QRY-{number:03d}"):
                    observation = _decoded(scenario(f"QRY-{number:03d}"))
                    sentinel = next(
                        row
                        for row in observation["db_state"]["articles"]
                        if row["id"] == 1
                    )
                    self.assertEqual(sentinel["title"], sentinel_title)

    def test_scenario_source_does_not_read_locked_artifacts(self) -> None:
        source = Path(scenarios.__file__).read_text(encoding="utf-8")
        for forbidden in (
            "query-breadth-manifest",
            "query-breadth-oracle",
            "godj-query-breadth-not-implemented",
            "/oracles/",
            "/fixtures/",
        ):
            self.assertNotIn(forbidden, source)

    def test_checked_in_oracle_is_exact_deterministic_regeneration(self) -> None:
        generated = canonical_json(generate_suite())
        self.assertEqual(generated, ORACLE.read_bytes())
        self.assertEqual(generated, canonical_json(generate_suite()))

        locked_manifest = deepcopy(_load(MANIFEST))
        for contract in locked_manifest["contracts"]:
            contract["status"] = "oracle_locked"
        with patch.object(
            reference,
            "_load",
            side_effect=[_load(PROFILE), locked_manifest],
        ):
            locked = canonical_json(generate_suite())
        self.assertEqual(locked, generated)

        red_manifest = deepcopy(locked_manifest)
        red_manifest["contracts"][0]["status"] = "red"
        with patch.object(
            reference,
            "_load",
            side_effect=[_load(PROFILE), red_manifest],
        ):
            with self.assertRaisesRegex(
                RuntimeError,
                "requires oracle_locked or passing status",
            ):
                generate_suite()

    def test_observation_rejects_result_metric_step_mismatch(self) -> None:
        with scenarios.article_database():
            with self.assertRaisesRegex(AssertionError, "do not match"):
                scenarios._observed_steps(
                    "QRY-999",
                    [scenarios._step("result", True)],
                    [
                        scenarios._metric_step(
                            "different",
                            {"query_count": 0, "statements": []},
                        )
                    ],
                )


if __name__ == "__main__":
    unittest.main()
