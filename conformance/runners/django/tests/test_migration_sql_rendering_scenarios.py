from __future__ import annotations

import ast
import hashlib
import inspect
import json
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

import django
from django.core.management.base import BaseCommand
from django.core.management.commands import sqlmigrate
from django.db import connection
from django.db.backends.base.schema import BaseDatabaseSchemaEditor
from django.db.backends.sqlite3.schema import (
    DatabaseSchemaEditor as SQLiteDatabaseSchemaEditor,
)
from django.db.migrations.loader import MigrationLoader
from django.db.migrations.migration import Migration
from django.db.migrations.operations.fields import AddField
from django.db.migrations.operations.models import CreateModel
from django.db.migrations.recorder import MigrationRecorder

from conformance.runners.django import migration_sql_rendering_scenarios as scenarios
from conformance.runners.django.normalizer import canonical_json


EXPECTED_SCENARIOS = (
    "django.migration.sql_rendering.forward_before_state_order",
    "django.migration.sql_rendering.sqlite_create_add_semantics",
)
ROOT = Path(__file__).resolve().parents[4]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = ROOT / "conformance/contracts/migration-sql-rendering-manifest.json"
PINNED_SOURCE = {
    "django/core/management/base.py": (
        25_059,
        "6a97f5cf7c8f6bc23275074d926cb47d4912be1601688b1cd16ed1c35738a962",
    ),
    "django/core/management/commands/sqlmigrate.py": (
        3_310,
        "2d5406b5941479ce60245bc4aa59a309e78714a0be92b7c36018b90c279dbb15",
    ),
    "django/db/migrations/loader.py": (
        18_744,
        "cdc323bdf802553cd668a6df85f5032b5d37922226234a625dac60380b632f91",
    ),
    "django/db/migrations/migration.py": (
        9_765,
        "3eeecc565bca680b83f27ba889df329050432c47e0511ee42f3f92a1dacdf81d",
    ),
    "django/db/migrations/operations/models.py": (
        45_901,
        "abfa523998a4bf50a88f93c72579ac24266208726f98b6e93bbc0e4a94fd8494",
    ),
    "django/db/migrations/operations/fields.py": (
        12_787,
        "76c0f9945ad2f0c3e1df559e5b6a8bfa1c00c2049f6448342792ad2ea505ac73",
    ),
    "django/db/backends/base/schema.py": (
        85_400,
        "9dc62b1585e378fdd490e66b0dac7b87cd7bf323f8a2fee392d2b48368bc22fe",
    ),
    "django/db/backends/sqlite3/schema.py": (
        20_358,
        "85b1c0c726e9e38f4be1169aa146473d4116efc4f7094c9c936a468dd109e1bf",
    ),
}


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


def _model(result: dict[str, Any], app: str, model: str) -> dict[str, Any]:
    return next(
        row for row in result["models"] if (row["app"], row["model"]) == (app, model)
    )


class MigrationSQLRenderingScenarioTests(unittest.TestCase):
    def assert_clean_database(self) -> None:
        self.assertEqual(connection.introspection.table_names(), [])
        self.assertFalse(MigrationRecorder(connection).has_table())

    def test_reference_runtime_profile_and_source_are_exact(self) -> None:
        self.assertEqual(django.get_version(), "6.1")
        self.assertEqual(
            scenarios.DJANGO_61_COMMIT,
            "fe0a859f537d4238cf49fca39073513206f83122",
        )
        profile = json.loads(PROFILE.read_text(encoding="utf-8"))
        self.assertEqual(
            profile["fingerprint"]["django_commit"],
            scenarios.DJANGO_61_COMMIT,
        )
        django_root = Path(django.__file__).resolve().parent.parent
        for relative, (size, digest) in PINNED_SOURCE.items():
            with self.subTest(source=relative):
                source = django_root / relative
                payload = source.read_bytes()
                self.assertEqual(len(payload), size)
                self.assertEqual(hashlib.sha256(payload).hexdigest(), digest)
                git_blob = hashlib.sha1(
                    f"blob {len(payload)}\0".encode("ascii") + payload
                ).hexdigest()
                self.assertEqual(
                    git_blob,
                    scenarios.DJANGO_61_GIT_BLOBS[relative],
                )

        runtime_ranges = {
            "BaseCommand.execute": inspect.getsourcelines(BaseCommand.execute),
            "sqlmigrate.Command.execute": inspect.getsourcelines(
                sqlmigrate.Command.execute
            ),
            "sqlmigrate.Command.handle": inspect.getsourcelines(
                sqlmigrate.Command.handle
            ),
            "MigrationLoader.project_state": inspect.getsourcelines(
                MigrationLoader.project_state
            ),
            "MigrationLoader.collect_sql": inspect.getsourcelines(
                MigrationLoader.collect_sql
            ),
            "Migration.apply": inspect.getsourcelines(Migration.apply),
            "CreateModel.database_forwards": inspect.getsourcelines(
                CreateModel.database_forwards
            ),
            "AddField.database_forwards": inspect.getsourcelines(
                AddField.database_forwards
            ),
            "BaseDatabaseSchemaEditor.create_model": inspect.getsourcelines(
                BaseDatabaseSchemaEditor.create_model
            ),
            "BaseDatabaseSchemaEditor.add_field": inspect.getsourcelines(
                BaseDatabaseSchemaEditor.add_field
            ),
            "SQLiteDatabaseSchemaEditor.add_field": inspect.getsourcelines(
                SQLiteDatabaseSchemaEditor.add_field
            ),
        }
        for authority, (source_lines, first_line) in runtime_ranges.items():
            with self.subTest(authority=authority):
                self.assertEqual(
                    (first_line, first_line + len(source_lines) - 1),
                    scenarios.DJANGO_61_SOURCE_RANGES[authority],
                )
        self.assertEqual(
            scenarios.DJANGO_61_SOURCE_RANGES["MigrateTests.test_sqlmigrate_forwards"],
            (908, 964),
        )
        self.assertEqual(
            scenarios.DJANGO_61_GIT_BLOBS["tests/migrations/test_commands.py"],
            "61336f55332844bfd372b97aa6a7b1fad6cca027",
        )
        checkout_test = scenarios.DJANGO_61_CHECKOUT_ONLY_SOURCE[
            "tests/migrations/test_commands.py"
        ]
        self.assertEqual(
            checkout_test,
            {
                "bytes": 153_217,
                "git_blob": "61336f55332844bfd372b97aa6a7b1fad6cca027",
                "sha256": (
                    "15b8ca276a4aca3237cbadb062947c1b052fa095c6425d02b4dd7dffb455bcca"
                ),
                "symbol": "MigrateTests.test_sqlmigrate_forwards",
            },
        )
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        test_reference = (
            "django@fe0a859f537d4238cf49fca39073513206f83122:"
            "tests/migrations/test_commands.py::MigrateTests.test_sqlmigrate_forwards"
        )
        for contract_id in ("MIG-131", "MIG-132"):
            contract = next(
                row for row in manifest["contracts"] if row["id"] == contract_id
            )
            self.assertIn(
                test_reference,
                [item["reference"] for item in contract["provenance"]],
            )

    def test_registry_slug_phase_and_result_only_shape_are_exact(self) -> None:
        self.assertEqual(scenarios.SET_SLUG, "migration-sql-rendering")
        self.assertEqual(tuple(scenarios.SCENARIOS), EXPECTED_SCENARIOS)
        for contract_id, scenario in zip(
            ("MIG-131", "MIG-132"),
            scenarios.SCENARIOS.values(),
            strict=True,
        ):
            with self.subTest(contract=contract_id):
                observation = scenario(contract_id)
                self.assertEqual(observation["id"], contract_id)
                self.assertEqual(observation["phase"], "construction")
                self.assertEqual(observation["status"], "observed")
                self.assertIsNone(observation["error"])
                self.assertIsNone(observation["db_state"])
                self.assertIsNone(observation["metrics"])
                self.assertIsNotNone(observation["result"])
                self.assert_clean_database()

    def test_forward_observation_uses_real_command_loader_apply_and_operations(
        self,
    ) -> None:
        original_handle = sqlmigrate.Command.handle
        original_collect_sql = MigrationLoader.collect_sql
        original_apply = Migration.apply
        original_create = CreateModel.database_forwards
        original_add = AddField.database_forwards
        original_schema_create = BaseDatabaseSchemaEditor.create_model
        original_schema_add = BaseDatabaseSchemaEditor.add_field
        original_sqlite_add = SQLiteDatabaseSchemaEditor.add_field
        with (
            patch.object(
                sqlmigrate.Command,
                "handle",
                autospec=True,
                side_effect=original_handle,
            ) as handle,
            patch.object(
                MigrationLoader,
                "collect_sql",
                autospec=True,
                side_effect=original_collect_sql,
            ) as collect_sql,
            patch.object(
                Migration,
                "apply",
                autospec=True,
                side_effect=original_apply,
            ) as apply,
            patch.object(
                CreateModel,
                "database_forwards",
                autospec=True,
                side_effect=original_create,
            ) as create,
            patch.object(
                AddField,
                "database_forwards",
                autospec=True,
                side_effect=original_add,
            ) as add,
            patch.object(
                BaseDatabaseSchemaEditor,
                "create_model",
                autospec=True,
                side_effect=original_schema_create,
            ) as schema_create,
            patch.object(
                BaseDatabaseSchemaEditor,
                "add_field",
                autospec=True,
                side_effect=original_schema_add,
            ) as schema_add,
            patch.object(
                SQLiteDatabaseSchemaEditor,
                "add_field",
                autospec=True,
                side_effect=original_sqlite_add,
            ) as sqlite_add,
        ):
            scenarios.forward_before_state_order("MIG-131")

        self.assertEqual(handle.call_count, 1)
        self.assertEqual(collect_sql.call_count, 1)
        self.assertEqual(apply.call_count, 1)
        self.assertEqual(create.call_count, 1)
        self.assertEqual(add.call_count, 1)
        self.assertEqual(schema_create.call_count, 1)
        self.assertEqual(schema_add.call_count, 1)
        self.assertEqual(sqlite_add.call_count, 1)
        self.assert_clean_database()

    def test_forward_state_is_target_before_and_exactly_one_target_in_order(
        self,
    ) -> None:
        observation = scenarios.forward_before_state_order("MIG-131")
        result = _semantic(observation["result"])
        self.assertEqual(
            result["plan"],
            [
                {
                    "app": "blog",
                    "direction": "forward",
                    "name": "0002_render_sql",
                }
            ],
        )
        self.assertEqual(
            result["before_state_target"],
            {"app": "blog", "name": "0002_render_sql"},
        )
        self.assertFalse(result["before_state_at_end"])
        self.assertEqual(
            result["operation_order"],
            [
                {
                    "kind": "CreateModel",
                    "ordinal": 0,
                    "subject": "blog.Category",
                },
                {
                    "kind": "AddField",
                    "ordinal": 1,
                    "subject": "blog.article.summary",
                },
            ],
        )

        before = result["before_state"]
        self.assertEqual(
            [(row["app"], row["model"]) for row in before["models"]],
            [("authors", "author"), ("blog", "article")],
        )
        article_before = _model(before, "blog", "article")
        self.assertEqual(
            [field["name"] for field in article_before["fields"]],
            ["id", "title", "author"],
        )

        final = result["final_state"]
        self.assertEqual(
            [(row["app"], row["model"]) for row in final["models"]],
            [("authors", "author"), ("blog", "article"), ("blog", "category")],
        )
        article_after = _model(final, "blog", "article")
        self.assertEqual(
            [field["name"] for field in article_after["fields"]],
            ["id", "title", "author", "summary"],
        )
        self.assert_clean_database()

    def test_sqlite_create_add_meaning_is_normalized_without_raw_surface(self) -> None:
        observation = scenarios.sqlite_create_add_semantics("MIG-132")
        result = _semantic(observation["result"])
        self.assertEqual(result["backend"], "django.db.backends.sqlite3")
        self.assertFalse(result["raw_sql_bytes_compared"])
        self.assertFalse(result["comments_compared"])
        self.assertFalse(result["transaction_wrapper_compared"])
        self.assertEqual(
            result["normalized_operations"],
            [
                {
                    "kind": "CreateModel",
                    "ordinal": 0,
                    "statements": [
                        {
                            "columns": [
                                {
                                    "autoincrement": True,
                                    "name": "id",
                                    "nullability": "not_null",
                                    "primary_key": True,
                                    "reference": None,
                                    "sql_type": "integer",
                                },
                                {
                                    "autoincrement": False,
                                    "name": "name",
                                    "nullability": "not_null",
                                    "primary_key": False,
                                    "reference": None,
                                    "sql_type": "varchar(80)",
                                },
                            ],
                            "kind": "create_table",
                            "table": "blog_category",
                        }
                    ],
                    "subject": "blog.Category",
                },
                {
                    "kind": "AddField",
                    "ordinal": 1,
                    "statements": [
                        {
                            "column": {
                                "autoincrement": False,
                                "name": "summary",
                                "nullability": "nullable",
                                "primary_key": False,
                                "reference": None,
                                "sql_type": "varchar(120)",
                            },
                            "kind": "add_column",
                            "table": "blog_article",
                        }
                    ],
                    "subject": "blog.article.summary",
                },
            ],
        )
        payload = canonical_json(observation)
        self.assertNotIn(b'CREATE TABLE "blog_category"', payload)
        self.assertNotIn(b'ALTER TABLE "blog_article"', payload)
        self.assertNotIn(b"-- Create model", payload)
        self.assertNotIn(b"BEGIN", payload)
        self.assertNotIn(b"COMMIT", payload)
        self.assert_clean_database()

    def test_observations_are_fresh_deterministic_and_contract_id_agnostic(
        self,
    ) -> None:
        for contract_id, scenario in zip(
            ("MIG-131", "MIG-132"),
            scenarios.SCENARIOS.values(),
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
                self.assertLess(len(canonical_json(first)), 8 * 1024)
                self.assert_clean_database()

    def test_source_is_expected_artifact_blind_and_does_not_use_executor(self) -> None:
        source = inspect.getsource(scenarios)
        syntax = ast.parse(source)
        imported_modules = {
            node.module
            for node in ast.walk(syntax)
            if isinstance(node, ast.ImportFrom) and node.module is not None
        }
        for forbidden in (
            "conformance/contracts",
            "conformance/oracles",
            "conformance/fixtures",
            "not_implemented",
            "not-implemented",
            "MigrationExecutor",
        ):
            self.assertNotIn(forbidden, source)
        self.assertNotIn("django.db.migrations.executor", imported_modules)


if __name__ == "__main__":
    unittest.main()
