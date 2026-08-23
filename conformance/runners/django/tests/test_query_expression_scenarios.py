from __future__ import annotations

import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

from conformance.runners.django import query_expression_scenarios as scenarios
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
    raise AssertionError(f"unsupported query-expression value kind {kind!r}")


def _decoded(observation: dict[str, Any]) -> dict[str, Any]:
    return {
        **observation,
        "result": _decode(observation["result"]),
        "db_state": _decode(observation["db_state"]),
        "metrics": _decode(observation["metrics"]),
    }


class QueryExpressionScenarioTests(unittest.TestCase):
    expected_names = [
        "django.query.expression.scalar_exact_or",
        "django.query.expression.escaped_ascii_icontains_or",
        "django.query.expression.grouped_or_and_reuse",
        "django.query.expression.nonnull_scalar_not",
        "django.query.expression.nullable_negation_truth_table",
        "django.query.expression.implicit_filter_and",
        "django.query.expression.nested_connector_order_and_source_independence",
        "django.query.expression.composite_distinct_stable_page",
        "django.query.expression.projection_outside_predicate",
        "django.query.expression.composite_count_max",
        "django.query.expression.integer_gt_literal_boundary",
        "django.query.expression.integer_gte_literal_boundary",
        "django.query.expression.integer_lt_literal_boundary",
        "django.query.expression.integer_lte_literal_boundary",
        "django.query.expression.range_composition_negation_and_reuse",
        "django.query.expression.same_field_reference_boundaries",
        "django.query.expression.same_model_field_reference_and_nullable_negation",
        "django.query.expression.nullable_ordering_negation_truth_table",
        "django.query.expression.field_reference_stable_projection",
        "django.query.expression.field_reference_count_max",
    ]

    def run_scenarios(self) -> dict[str, dict[str, Any]]:
        observations: dict[str, dict[str, Any]] = {}
        for number, scenario in enumerate(scenarios.SCENARIOS.values(), 34):
            contract_id = f"QRY-{number:03d}"
            observation = scenario(contract_id)
            self.assertEqual(observation["id"], contract_id)
            self.assertEqual(observation["status"], "observed")
            self.assertEqual(observation["phase"], "evaluation")
            self.assertIsNone(observation["error"])
            observations[contract_id] = _decoded(observation)
        return observations

    def test_registry_is_exact_and_ordered(self) -> None:
        self.assertEqual(list(scenarios.SCENARIOS), self.expected_names)

    def test_boolean_results_and_nullable_negation_truth_table(self) -> None:
        observations = self.run_scenarios()

        self.assertEqual(
            observations["QRY-034"]["result"],
            {"operator": "or", "rows": [1, 4]},
        )
        self.assertEqual(
            observations["QRY-035"]["result"],
            {"input_after": "%_", "input_before": "%_", "rows": [2, 5]},
        )
        self.assertEqual(
            observations["QRY-036"]["result"]["steps"],
            [
                {"name": "published", "value": [3, 4]},
                {"name": "unpublished", "value": [2]},
            ],
        )
        self.assertEqual(
            observations["QRY-037"]["result"],
            {"operator": "not", "rows": [1, 4]},
        )
        self.assertEqual(
            observations["QRY-038"]["result"]["steps"],
            [
                {"name": "not_exact_orm", "value": [1, 3, 4]},
                {"name": "not_icontains_orm", "value": [1, 3, 4]},
                {"name": "not_isnull_true", "value": [2, 3]},
                {"name": "not_isnull_false", "value": [1, 4]},
            ],
        )

    def test_filter_reuse_pagination_projection_and_aggregate_results(self) -> None:
        observations = self.run_scenarios()

        self.assertEqual(
            observations["QRY-039"]["result"]["steps"],
            [
                {"name": "variadic_filter", "value": [3]},
                {"name": "repeated_filter", "value": [3]},
            ],
        )
        self.assertEqual(
            observations["QRY-040"]["result"]["steps"],
            [
                {"name": "first_derived", "value": [3, 4]},
                {"name": "second_derived", "value": [1, 4]},
                {"name": "base_after_derivation", "value": [1, 3, 4]},
                {"name": "reused_predicate", "value": [2]},
            ],
        )
        self.assertEqual(
            observations["QRY-041"]["result"],
            {
                "fields": ["id", "title"],
                "rows": [[2, "django Tips"], [3, "Django Deep Dive"]],
            },
        )
        self.assertEqual(
            observations["QRY-042"]["result"],
            {
                "filter_fields": ["summary", "published"],
                "projection_fields": ["id", "title"],
                "rows": [
                    [1, "Alpine Guide"],
                    [2, "django Tips"],
                    [4, "Other"],
                ],
            },
        )
        self.assertEqual(
            observations["QRY-043"]["result"]["steps"],
            [
                {
                    "name": "nonempty",
                    "value": {
                        "fields": ["row_count", "latest_id"],
                        "values": [4, 4],
                    },
                },
                {
                    "name": "empty",
                    "value": {
                        "fields": ["row_count", "latest_id"],
                        "values": [0, None],
                    },
                },
            ],
        )

    def test_literal_comparison_and_range_composition_results(self) -> None:
        observations = self.run_scenarios()

        self.assertEqual(
            observations["QRY-044"]["result"],
            {"lookup": "gt", "rhs": 2, "rows": [3, 4]},
        )
        self.assertEqual(
            observations["QRY-045"]["result"],
            {"lookup": "gte", "rhs": 2, "rows": [2, 3, 4]},
        )
        self.assertEqual(
            observations["QRY-046"]["result"],
            {"lookup": "lt", "rhs": 3, "rows": [1, 2]},
        )
        self.assertEqual(
            observations["QRY-047"]["result"],
            {"lookup": "lte", "rhs": 3, "rows": [1, 2, 3]},
        )
        self.assertEqual(
            observations["QRY-048"]["result"]["steps"],
            [
                {"name": "explicit_q_range", "value": [2, 3]},
                {"name": "keyword_range", "value": [2, 3]},
                {"name": "negated_range", "value": [1, 4]},
                {"name": "reused_published", "value": [3]},
            ],
        )

    def test_field_reference_nullable_projection_and_aggregate_results(
        self,
    ) -> None:
        observations = self.run_scenarios()

        self.assertEqual(
            observations["QRY-049"]["result"]["steps"],
            [
                {"name": "id_exact_id", "value": [1, 2, 3, 4]},
                {"name": "id_gt_id", "value": []},
                {"name": "id_gte_id", "value": [1, 2, 3, 4]},
                {"name": "id_lt_id", "value": []},
                {"name": "id_lte_id", "value": [1, 2, 3, 4]},
            ],
        )
        self.assertEqual(
            observations["QRY-050"]["result"]["steps"],
            [
                {"name": "cross_field_exact", "value": [5]},
                {"name": "cross_field_not_exact", "value": [1, 2, 3, 4]},
                {"name": "equal_row_gt", "value": []},
                {"name": "nullable_rhs_direct", "value": []},
            ],
        )
        self.assertEqual(
            observations["QRY-051"]["result"]["steps"],
            [
                {"name": "not_gt_empty", "value": [1, 3, 4]},
                {"name": "not_gte_empty", "value": [1, 4]},
                {"name": "not_lt_orm", "value": [1, 2, 4]},
                {"name": "not_lte_orm", "value": [1, 4]},
            ],
        )
        self.assertEqual(
            observations["QRY-052"]["result"],
            {
                "filter_fields": ["id", "summary"],
                "order_fields": ["-id"],
                "projection_fields": ["id", "title"],
                "rows": [
                    [4, "Other"],
                    [3, "Django Deep Dive"],
                    [2, "django Tips"],
                ],
            },
        )
        self.assertEqual(
            observations["QRY-053"]["result"]["steps"],
            [
                {
                    "name": "nonempty",
                    "value": {
                        "fields": ["row_count", "latest_id"],
                        "values": [3, 4],
                    },
                },
                {
                    "name": "empty",
                    "value": {
                        "fields": ["row_count", "latest_id"],
                        "values": [0, None],
                    },
                },
            ],
        )

        articles = observations["QRY-050"]["db_state"]["articles"]
        self.assertEqual(
            articles[-1],
            {
                "id": 5,
                "published": False,
                "summary": "same",
                "title": "same",
            },
        )

    def test_real_query_shapes_and_counts_are_bounded(self) -> None:
        observations = self.run_scenarios()
        expected_counts = {
            "QRY-034": [1],
            "QRY-035": [1],
            "QRY-036": [1, 1],
            "QRY-037": [1],
            "QRY-038": [1, 1, 1, 1],
            "QRY-039": [1, 1],
            "QRY-040": [1, 1, 1, 1],
            "QRY-041": [1],
            "QRY-042": [1],
            "QRY-043": [1, 1],
            "QRY-044": [1],
            "QRY-045": [1],
            "QRY-046": [1],
            "QRY-047": [1],
            "QRY-048": [1, 1, 1, 1],
            "QRY-049": [1, 1, 1, 1, 1],
            "QRY-050": [1, 1, 1, 1],
            "QRY-051": [1, 1, 1, 1],
            "QRY-052": [1],
            "QRY-053": [1, 1],
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

        pagination = observations["QRY-041"]["metrics"]["statements"][0]
        self.assertTrue(pagination["distinct"])
        self.assertTrue(pagination["has_limit"])
        self.assertTrue(pagination["has_offset"])

        for step in observations["QRY-043"]["metrics"]["steps"]:
            self.assertEqual(
                step["statements"][0]["aggregate_functions"],
                ["COUNT", "MAX"],
            )
        for step in observations["QRY-053"]["metrics"]["steps"]:
            self.assertEqual(
                step["statements"][0]["aggregate_functions"],
                ["COUNT", "MAX"],
            )

    def test_logical_shape_nullable_guards_and_parameter_order_are_exact(self) -> None:
        observations = self.run_scenarios()

        escaped = observations["QRY-035"]["metrics"]["statements"][0]
        self.assertEqual(
            escaped["logical_operators"],
            {"and": 0, "not": 0, "or": 1},
        )
        self.assertEqual(escaped["parameters"], ["%\\%\\_%", "%orm%"])

        nonnull_not = observations["QRY-037"]["metrics"]["statements"][0]
        self.assertEqual(
            nonnull_not["logical_operators"],
            {"and": 0, "not": 1, "or": 0},
        )
        self.assertEqual(nonnull_not["parameters"], ["%django%"])

        nullable = observations["QRY-038"]["metrics"]["steps"]
        self.assertEqual(
            [step["statements"][0]["logical_operators"] for step in nullable],
            [
                {"and": 1, "not": 1, "or": 0},
                {"and": 1, "not": 1, "or": 0},
                {"and": 0, "not": 1, "or": 0},
                {"and": 0, "not": 1, "or": 0},
            ],
        )
        self.assertEqual(
            [step["statements"][0]["null_predicates"] for step in nullable],
            [
                {"is_not_null": 1, "is_null": 0},
                {"is_not_null": 1, "is_null": 0},
                {"is_not_null": 0, "is_null": 1},
                {"is_not_null": 1, "is_null": 0},
            ],
        )
        self.assertEqual(
            [step["statements"][0]["parameters"] for step in nullable],
            [["ORM"], ["%orm%"], [], []],
        )

        reused = observations["QRY-040"]["metrics"]["steps"]
        self.assertEqual(
            [step["statements"][0]["parameters"] for step in reused],
            [
                [True, "%django%", "Other"],
                [True, "Alpine Guide", "Other"],
                [True],
                ["%django%", "Other", False],
            ],
        )

        literal_parameters = [
            observations[contract_id]["metrics"]["statements"][0][
                "parameters"
            ]
            for contract_id in ("QRY-044", "QRY-045", "QRY-046", "QRY-047")
        ]
        self.assertEqual(literal_parameters, [[2], [2], [3], [3]])

        range_steps = observations["QRY-048"]["metrics"]["steps"]
        self.assertEqual(
            [step["statements"][0]["parameters"] for step in range_steps],
            [[1, 3], [1, 3], [1, 3], [1, 3, True]],
        )
        self.assertEqual(
            [
                step["statements"][0]["logical_operators"]
                for step in range_steps
            ],
            [
                {"and": 1, "not": 0, "or": 0},
                {"and": 1, "not": 0, "or": 0},
                {"and": 1, "not": 1, "or": 0},
                {"and": 2, "not": 0, "or": 0},
            ],
        )

        same_field = observations["QRY-049"]["metrics"]["steps"]
        self.assertEqual(
            [step["statements"][0]["parameters"] for step in same_field],
            [[], [], [], [], []],
        )

        cross_field = observations["QRY-050"]["metrics"]["steps"]
        self.assertEqual(
            [step["statements"][0]["parameters"] for step in cross_field],
            [[], [], [5], [1]],
        )
        self.assertEqual(
            cross_field[1]["statements"][0]["logical_operators"],
            {"and": 1, "not": 1, "or": 0},
        )
        self.assertEqual(
            cross_field[1]["statements"][0]["null_predicates"],
            {"is_not_null": 1, "is_null": 0},
        )

        nullable_ordering = observations["QRY-051"]["metrics"]["steps"]
        self.assertEqual(
            [
                step["statements"][0]["logical_operators"]
                for step in nullable_ordering
            ],
            [{"and": 1, "not": 1, "or": 0}] * 4,
        )
        self.assertEqual(
            [
                step["statements"][0]["null_predicates"]
                for step in nullable_ordering
            ],
            [{"is_not_null": 1, "is_null": 0}] * 4,
        )
        self.assertEqual(
            [step["statements"][0]["parameters"] for step in nullable_ordering],
            [[""], [""], ["ORM"], ["ORM"]],
        )

        projection = observations["QRY-052"]["metrics"]["statements"][0]
        self.assertEqual(
            projection["logical_operators"],
            {"and": 1, "not": 0, "or": 1},
        )
        self.assertEqual(
            projection["null_predicates"],
            {"is_not_null": 0, "is_null": 1},
        )
        self.assertEqual(projection["parameters"], [2])

        aggregate_steps = observations["QRY-053"]["metrics"]["steps"]
        self.assertEqual(
            [step["statements"][0]["parameters"] for step in aggregate_steps],
            [[2], []],
        )

    def test_live_fixture_changes_propagate_to_every_scenario(self) -> None:
        sentinel_title = "query expression live fixture sentinel"
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
            for number, scenario in enumerate(scenarios.SCENARIOS.values(), 34):
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
            "query-expression-manifest",
            "query-expression-oracle",
            "godj-query-expression-not-implemented",
            "/oracles/",
            "/fixtures/",
        ):
            self.assertNotIn(forbidden, source)


if __name__ == "__main__":
    unittest.main()
