from __future__ import annotations

import sqlite3
import subprocess
import sys
import textwrap
import unittest
from contextlib import closing, redirect_stderr
from io import StringIO
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from django.db import IntegrityError, connection

from conformance.runners.django.relation_fixture import relation_database
from conformance.runners.django.runner import (
    DEFAULT_ARTICLE_ADMIN_MANIFEST,
    DEFAULT_ARTICLE_ADMIN_ORACLE,
    DEFAULT_AUTH_SESSION_MANIFEST,
    DEFAULT_AUTH_SESSION_ORACLE,
    DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST,
    DEFAULT_MIGRATION_DEFINITION_SOURCE_ORACLE,
    DEFAULT_MIGRATION_COMMAND_MANIFEST,
    DEFAULT_MIGRATION_COMMAND_ORACLE,
    DEFAULT_MIGRATION_WRITER_MANIFEST,
    DEFAULT_MIGRATION_WRITER_ORACLE,
    DEFAULT_MIGRATION_STATUS_MANIFEST,
    DEFAULT_MIGRATION_STATUS_ORACLE,
    DEFAULT_MIGRATION_TARGET_PLAN_MANIFEST,
    DEFAULT_MIGRATION_TARGET_PLAN_ORACLE,
    DEFAULT_MIGRATION_SQL_RENDERING_MANIFEST,
    DEFAULT_MIGRATION_SQL_RENDERING_ORACLE,
    DEFAULT_MIGRATION_PROJECT_CHECK_MANIFEST,
    DEFAULT_MIGRATION_PROJECT_CHECK_ORACLE,
    DEFAULT_MIGRATION_RELATION_MANIFEST,
    DEFAULT_MIGRATION_RELATION_ORACLE,
    DEFAULT_MIGRATION_LIFECYCLE_MANIFEST,
    DEFAULT_MIGRATION_LIFECYCLE_ORACLE,
    DEFAULT_MIGRATION_EXECUTION_MANIFEST,
    DEFAULT_MIGRATION_EXECUTION_ORACLE,
    DEFAULT_MIGRATION_PLANNING_MANIFEST,
    DEFAULT_MIGRATION_PLANNING_ORACLE,
    DEFAULT_MIGRATION_RESTART_MANIFEST,
    DEFAULT_MIGRATION_RESTART_ORACLE,
    DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST,
    DEFAULT_MIGRATION_STATE_RECONSTRUCTION_ORACLE,
    DEFAULT_QUERY_BREADTH_MANIFEST,
    DEFAULT_QUERY_BREADTH_ORACLE,
    DEFAULT_QUERY_EXPRESSION_MANIFEST,
    DEFAULT_QUERY_EXPRESSION_ORACLE,
    DEFAULT_QUERY_CACHE_MANIFEST,
    DEFAULT_QUERY_CACHE_ORACLE,
    DEFAULT_RELATION_MANIFEST,
    DEFAULT_RELATION_ORACLE,
    DEFAULT_SAVE_LIFECYCLE_MANIFEST,
    DEFAULT_SAVE_LIFECYCLE_ORACLE,
    DEFAULT_TEMPLATE_FORM_MANIFEST,
    DEFAULT_TEMPLATE_FORM_ORACLE,
    DEFAULT_SYSTEM_STATE_MANIFEST,
    DEFAULT_SYSTEM_STATE_ORACLE,
    DEFAULT_WRITE_MIGRATION_MANIFEST,
    DEFAULT_WRITE_MIGRATION_ORACLE,
    REPOSITORY_ROOT,
    main,
)


class RunnerSafetyTests(unittest.TestCase):
    def test_external_file_database_settings_fail_closed_without_mutation(self) -> None:
        with TemporaryDirectory() as temporary:
            database_path = Path(temporary) / "user.sqlite3"
            with closing(sqlite3.connect(database_path)) as database:
                database.execute("CREATE TABLE user_sentinel (value TEXT NOT NULL)")
                database.execute(
                    "INSERT INTO user_sentinel (value) VALUES (?)", ("preserve-me",)
                )
                database.commit()
            contents_before = database_path.read_bytes()

            script = textwrap.dedent(
                """
                import sys
                from django.conf import settings

                settings.configure(
                    DATABASES={
                        "default": {
                            "ENGINE": "django.db.backends.sqlite3",
                            "NAME": sys.argv[1],
                        }
                    },
                    DEFAULT_AUTO_FIELD="django.db.models.AutoField",
                    INSTALLED_APPS=[
                        "conformance.runners.django.migration_fixture.apps.GoDjMigrationFixtureConfig",
                        "conformance.runners.django.migration_failure_fixture.apps.GoDjMigrationFailureFixtureConfig",
                    ],
                    LANGUAGE_CODE="en-us",
                    SECRET_KEY="external-user-settings",
                    TIME_ZONE="UTC",
                    USE_I18N=False,
                    USE_TZ=True,
                )

                import django
                django.setup()

                try:
                    from conformance.runners.django import runner
                except RuntimeError as error:
                    if "externally configured Django settings" not in str(error):
                        raise
                    print(error)
                else:
                    runner.WRITE_MIGRATION_SCENARIOS[
                        "django.migration.create_model"
                    ]("MIG-001")
                    raise SystemExit("unsafe runner accepted an external database")
                """
            )
            completed = subprocess.run(
                [sys.executable, "-c", script, str(database_path)],
                cwd=REPOSITORY_ROOT,
                check=False,
                capture_output=True,
                text=True,
                timeout=10,
            )

            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertIn("externally configured Django settings", completed.stdout)
            self.assertEqual(database_path.read_bytes(), contents_before)
            with closing(sqlite3.connect(database_path)) as database:
                tables = database.execute(
                    "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name"
                ).fetchall()
                sentinel = database.execute(
                    "SELECT value FROM user_sentinel"
                ).fetchall()
            self.assertEqual(tables, [("user_sentinel",)])
            self.assertEqual(sentinel, [("preserve-me",)])

    def test_each_manifest_check_uses_its_oracle(self) -> None:
        cases = (
            (DEFAULT_WRITE_MIGRATION_MANIFEST, DEFAULT_WRITE_MIGRATION_ORACLE),
            (DEFAULT_SAVE_LIFECYCLE_MANIFEST, DEFAULT_SAVE_LIFECYCLE_ORACLE),
            (DEFAULT_QUERY_CACHE_MANIFEST, DEFAULT_QUERY_CACHE_ORACLE),
            (DEFAULT_QUERY_BREADTH_MANIFEST, DEFAULT_QUERY_BREADTH_ORACLE),
            (DEFAULT_QUERY_EXPRESSION_MANIFEST, DEFAULT_QUERY_EXPRESSION_ORACLE),
            (DEFAULT_MIGRATION_PLANNING_MANIFEST, DEFAULT_MIGRATION_PLANNING_ORACLE),
            (DEFAULT_MIGRATION_EXECUTION_MANIFEST, DEFAULT_MIGRATION_EXECUTION_ORACLE),
            (DEFAULT_MIGRATION_RESTART_MANIFEST, DEFAULT_MIGRATION_RESTART_ORACLE),
            (DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST, DEFAULT_MIGRATION_STATE_RECONSTRUCTION_ORACLE),
            (DEFAULT_MIGRATION_LIFECYCLE_MANIFEST, DEFAULT_MIGRATION_LIFECYCLE_ORACLE),
            (DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST, DEFAULT_MIGRATION_DEFINITION_SOURCE_ORACLE),
            (DEFAULT_MIGRATION_PROJECT_CHECK_MANIFEST, DEFAULT_MIGRATION_PROJECT_CHECK_ORACLE),
            (DEFAULT_MIGRATION_COMMAND_MANIFEST, DEFAULT_MIGRATION_COMMAND_ORACLE),
            (DEFAULT_MIGRATION_WRITER_MANIFEST, DEFAULT_MIGRATION_WRITER_ORACLE),
            (DEFAULT_MIGRATION_STATUS_MANIFEST, DEFAULT_MIGRATION_STATUS_ORACLE),
            (DEFAULT_MIGRATION_TARGET_PLAN_MANIFEST, DEFAULT_MIGRATION_TARGET_PLAN_ORACLE),
            (DEFAULT_MIGRATION_SQL_RENDERING_MANIFEST, DEFAULT_MIGRATION_SQL_RENDERING_ORACLE),
            (DEFAULT_RELATION_MANIFEST, DEFAULT_RELATION_ORACLE),
            (DEFAULT_MIGRATION_RELATION_MANIFEST, DEFAULT_MIGRATION_RELATION_ORACLE),
            (DEFAULT_SYSTEM_STATE_MANIFEST, DEFAULT_SYSTEM_STATE_ORACLE),
        )
        for manifest_path, oracle_path in cases:
            with self.subTest(manifest=manifest_path.name):
                expected = oracle_path.read_bytes()
                with (
                    patch("conformance.runners.django.runner.generate_suite", return_value={}) as generate,
                    patch("conformance.runners.django.runner.canonical_json", return_value=expected),
                ):
                    status = main(["--manifest", str(manifest_path), "--check"])
                self.assertEqual(status, 0)
                generate.assert_called_once()

    def test_gdj0043_manifests_without_output_use_only_their_oracles(self) -> None:
        for manifest, oracle in (
            (DEFAULT_TEMPLATE_FORM_MANIFEST, DEFAULT_TEMPLATE_FORM_ORACLE),
            (DEFAULT_AUTH_SESSION_MANIFEST, DEFAULT_AUTH_SESSION_ORACLE),
            (DEFAULT_ARTICLE_ADMIN_MANIFEST, DEFAULT_ARTICLE_ADMIN_ORACLE),
        ):
            with self.subTest(manifest=manifest.name):
                expected = oracle.read_bytes()
                with (
                    patch(
                        "conformance.runners.django.runner.generate_suite",
                        return_value={},
                    ) as generate_suite,
                    patch(
                        "conformance.runners.django.runner.canonical_json",
                        return_value=expected,
                    ),
                ):
                    status = main(["--manifest", str(manifest), "--check"])

                self.assertEqual(status, 0)
                generate_suite.assert_called_once()

    def test_each_manifest_regeneration_targets_only_its_oracle(self) -> None:
        cases = (
            (DEFAULT_QUERY_CACHE_MANIFEST, DEFAULT_QUERY_CACHE_ORACLE, b'{"query_cache":true}\n'),
            (DEFAULT_MIGRATION_PLANNING_MANIFEST, DEFAULT_MIGRATION_PLANNING_ORACLE, b'{"migration_planning":true}\n'),
            (DEFAULT_MIGRATION_EXECUTION_MANIFEST, DEFAULT_MIGRATION_EXECUTION_ORACLE, b'{"migration_execution":true}\n'),
            (DEFAULT_MIGRATION_RESTART_MANIFEST, DEFAULT_MIGRATION_RESTART_ORACLE, b'{"migration_restart":true}\n'),
            (DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST, DEFAULT_MIGRATION_STATE_RECONSTRUCTION_ORACLE, b'{"migration_state_reconstruction":true}\n'),
            (DEFAULT_MIGRATION_LIFECYCLE_MANIFEST, DEFAULT_MIGRATION_LIFECYCLE_ORACLE, b'{"migration_lifecycle":true}\n'),
            (DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST, DEFAULT_MIGRATION_DEFINITION_SOURCE_ORACLE, b'{"migration_definition_source":true}\n'),
            (DEFAULT_MIGRATION_PROJECT_CHECK_MANIFEST, DEFAULT_MIGRATION_PROJECT_CHECK_ORACLE, b'{"migration_project_check":true}\n'),
            (DEFAULT_MIGRATION_COMMAND_MANIFEST, DEFAULT_MIGRATION_COMMAND_ORACLE, b'{"migration_command":true}\n'),
            (DEFAULT_MIGRATION_WRITER_MANIFEST, DEFAULT_MIGRATION_WRITER_ORACLE, b'{"migration_writer":true}\n'),
            (DEFAULT_MIGRATION_STATUS_MANIFEST, DEFAULT_MIGRATION_STATUS_ORACLE, b'{"migration_status":true}\n'),
            (DEFAULT_MIGRATION_TARGET_PLAN_MANIFEST, DEFAULT_MIGRATION_TARGET_PLAN_ORACLE, b'{"migration_target_plan":true}\n'),
            (DEFAULT_MIGRATION_SQL_RENDERING_MANIFEST, DEFAULT_MIGRATION_SQL_RENDERING_ORACLE, b'{"migration_sql_rendering":true}\n'),
            (DEFAULT_RELATION_MANIFEST, DEFAULT_RELATION_ORACLE, b'{"relation":true}\n'),
            (DEFAULT_MIGRATION_RELATION_MANIFEST, DEFAULT_MIGRATION_RELATION_ORACLE, b'{"migration_relation":true}\n'),
            (DEFAULT_SYSTEM_STATE_MANIFEST, DEFAULT_SYSTEM_STATE_ORACLE, b'{"system_state":true}\n'),
        )
        for manifest_path, oracle_path, generated in cases:
            with self.subTest(manifest=manifest_path.name):
                with (
                    patch("conformance.runners.django.runner.generate_suite", return_value={}),
                    patch("conformance.runners.django.runner.canonical_json", return_value=generated),
                    patch("conformance.runners.django.runner.write_atomic") as write_atomic,
                ):
                    status = main(["--manifest", str(manifest_path)])
                self.assertEqual(status, 0)
                write_atomic.assert_called_once_with(oracle_path, generated)

    def test_relation_fixture_uses_sqlite_foreign_keys_and_rejects_orphans(
        self,
    ) -> None:
        with relation_database() as fixture:
            with connection.cursor() as cursor:
                enabled = cursor.execute("PRAGMA foreign_keys").fetchone()[0]
            self.assertEqual(enabled, 1)
            before = fixture.Post.objects.count()
            with self.assertRaises(IntegrityError):
                with connection.cursor() as cursor:
                    cursor.execute(
                        """
                        INSERT INTO godj_relation_post
                            (id, title, author_id, reviewer_id)
                        VALUES (%s, %s, %s, %s)
                        """,
                        (999, "Orphan", 999, None),
                    )
            self.assertEqual(fixture.Post.objects.count(), before)

    def test_unknown_manifest_requires_explicit_output(self) -> None:
        with TemporaryDirectory() as temporary:
            unknown_manifest = Path(temporary) / "unknown-manifest.json"
            stderr = StringIO()
            with (
                patch(
                    "conformance.runners.django.runner.generate_suite"
                ) as generate_suite,
                redirect_stderr(stderr),
            ):
                status = main(["--manifest", str(unknown_manifest)])

        self.assertEqual(status, 2)
        self.assertIn("--output is required for unknown manifest", stderr.getvalue())
        generate_suite.assert_not_called()

    def test_unknown_manifest_accepts_explicit_output(self) -> None:
        with TemporaryDirectory() as temporary:
            unknown_manifest = Path(temporary) / "unknown-manifest.json"
            output = Path(temporary) / "oracle.json"
            generated = b'{"explicit":true}\n'
            with (
                patch(
                    "conformance.runners.django.runner.generate_suite", return_value={}
                ),
                patch(
                    "conformance.runners.django.runner.canonical_json",
                    return_value=generated,
                ),
            ):
                status = main(
                    [
                        "--manifest",
                        str(unknown_manifest),
                        "--output",
                        str(output),
                    ]
                )

            self.assertEqual(status, 0)
            self.assertEqual(output.read_bytes(), generated)


if __name__ == "__main__":
    unittest.main()
