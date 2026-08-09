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

from conformance.runners.django.runner import (
    DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST,
    DEFAULT_MIGRATION_DEFINITION_SOURCE_ORACLE,
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
    DEFAULT_QUERY_CACHE_MANIFEST,
    DEFAULT_QUERY_CACHE_ORACLE,
    DEFAULT_SAVE_LIFECYCLE_MANIFEST,
    DEFAULT_SAVE_LIFECYCLE_ORACLE,
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

    def test_write_manifest_without_output_uses_write_oracle(self) -> None:
        expected = DEFAULT_WRITE_MIGRATION_ORACLE.read_bytes()
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ) as generate_suite,
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=expected,
            ),
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_WRITE_MIGRATION_MANIFEST),
                    "--check",
                ]
            )

        self.assertEqual(status, 0)
        generate_suite.assert_called_once()

    def test_save_lifecycle_manifest_without_output_uses_its_oracle(self) -> None:
        expected = DEFAULT_SAVE_LIFECYCLE_ORACLE.read_bytes()
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ) as generate_suite,
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=expected,
            ),
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_SAVE_LIFECYCLE_MANIFEST),
                    "--check",
                ]
            )

        self.assertEqual(status, 0)
        generate_suite.assert_called_once()

    def test_query_cache_manifest_without_output_uses_its_oracle(self) -> None:
        expected = DEFAULT_QUERY_CACHE_ORACLE.read_bytes()
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ) as generate_suite,
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=expected,
            ),
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_QUERY_CACHE_MANIFEST),
                    "--check",
                ]
            )

        self.assertEqual(status, 0)
        generate_suite.assert_called_once()

    def test_migration_planning_manifest_without_output_uses_its_oracle(self) -> None:
        expected = DEFAULT_MIGRATION_PLANNING_ORACLE.read_bytes()
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ) as generate_suite,
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=expected,
            ),
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_MIGRATION_PLANNING_MANIFEST),
                    "--check",
                ]
            )

        self.assertEqual(status, 0)
        generate_suite.assert_called_once()

    def test_migration_execution_manifest_without_output_uses_its_oracle(self) -> None:
        expected = DEFAULT_MIGRATION_EXECUTION_ORACLE.read_bytes()
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ) as generate_suite,
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=expected,
            ),
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_MIGRATION_EXECUTION_MANIFEST),
                    "--check",
                ]
            )

        self.assertEqual(status, 0)
        generate_suite.assert_called_once()

    def test_migration_restart_manifest_without_output_uses_its_oracle(self) -> None:
        expected = DEFAULT_MIGRATION_RESTART_ORACLE.read_bytes()
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ) as generate_suite,
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=expected,
            ),
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_MIGRATION_RESTART_MANIFEST),
                    "--check",
                ]
            )

        self.assertEqual(status, 0)
        generate_suite.assert_called_once()

    def test_migration_state_reconstruction_manifest_without_output_uses_its_oracle(
        self,
    ) -> None:
        expected = DEFAULT_MIGRATION_STATE_RECONSTRUCTION_ORACLE.read_bytes()
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ) as generate_suite,
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=expected,
            ),
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST),
                    "--check",
                ]
            )

        self.assertEqual(status, 0)
        generate_suite.assert_called_once()

    def test_migration_lifecycle_manifest_without_output_uses_its_oracle(self) -> None:
        expected = DEFAULT_MIGRATION_LIFECYCLE_ORACLE.read_bytes()
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ) as generate_suite,
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=expected,
            ),
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_MIGRATION_LIFECYCLE_MANIFEST),
                    "--check",
                ]
            )

        self.assertEqual(status, 0)
        generate_suite.assert_called_once()

    def test_migration_definition_source_manifest_without_output_uses_its_oracle(
        self,
    ) -> None:
        expected = DEFAULT_MIGRATION_DEFINITION_SOURCE_ORACLE.read_bytes()
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ) as generate_suite,
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=expected,
            ),
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST),
                    "--check",
                ]
            )

        self.assertEqual(status, 0)
        generate_suite.assert_called_once()

    def test_query_cache_manifest_regeneration_targets_only_its_oracle(self) -> None:
        generated = b'{"query_cache":true}\n'
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ),
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=generated,
            ),
            patch("conformance.runners.django.runner._write_atomic") as write_atomic,
        ):
            status = main(["--manifest", str(DEFAULT_QUERY_CACHE_MANIFEST)])

        self.assertEqual(status, 0)
        write_atomic.assert_called_once_with(DEFAULT_QUERY_CACHE_ORACLE, generated)

    def test_migration_planning_regeneration_targets_only_its_oracle(self) -> None:
        generated = b'{"migration_planning":true}\n'
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ),
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=generated,
            ),
            patch("conformance.runners.django.runner._write_atomic") as write_atomic,
        ):
            status = main(
                ["--manifest", str(DEFAULT_MIGRATION_PLANNING_MANIFEST)]
            )

        self.assertEqual(status, 0)
        write_atomic.assert_called_once_with(
            DEFAULT_MIGRATION_PLANNING_ORACLE,
            generated,
        )

    def test_migration_execution_regeneration_targets_only_its_oracle(self) -> None:
        generated = b'{"migration_execution":true}\n'
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ),
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=generated,
            ),
            patch("conformance.runners.django.runner._write_atomic") as write_atomic,
        ):
            status = main(
                ["--manifest", str(DEFAULT_MIGRATION_EXECUTION_MANIFEST)]
            )

        self.assertEqual(status, 0)
        write_atomic.assert_called_once_with(
            DEFAULT_MIGRATION_EXECUTION_ORACLE,
            generated,
        )

    def test_migration_restart_regeneration_targets_only_its_oracle(self) -> None:
        generated = b'{"migration_restart":true}\n'
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ),
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=generated,
            ),
            patch("conformance.runners.django.runner._write_atomic") as write_atomic,
        ):
            status = main(
                ["--manifest", str(DEFAULT_MIGRATION_RESTART_MANIFEST)]
            )

        self.assertEqual(status, 0)
        write_atomic.assert_called_once_with(
            DEFAULT_MIGRATION_RESTART_ORACLE,
            generated,
        )

    def test_migration_state_reconstruction_regeneration_targets_only_its_oracle(
        self,
    ) -> None:
        generated = b'{"migration_state_reconstruction":true}\n'
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ),
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=generated,
            ),
            patch("conformance.runners.django.runner._write_atomic") as write_atomic,
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_MIGRATION_STATE_RECONSTRUCTION_MANIFEST),
                ]
            )

        self.assertEqual(status, 0)
        write_atomic.assert_called_once_with(
            DEFAULT_MIGRATION_STATE_RECONSTRUCTION_ORACLE,
            generated,
        )

    def test_migration_lifecycle_regeneration_targets_only_its_oracle(self) -> None:
        generated = b'{"migration_lifecycle":true}\n'
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ),
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=generated,
            ),
            patch("conformance.runners.django.runner._write_atomic") as write_atomic,
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_MIGRATION_LIFECYCLE_MANIFEST),
                ]
            )

        self.assertEqual(status, 0)
        write_atomic.assert_called_once_with(
            DEFAULT_MIGRATION_LIFECYCLE_ORACLE,
            generated,
        )

    def test_migration_definition_source_regeneration_targets_only_its_oracle(
        self,
    ) -> None:
        generated = b'{"migration_definition_source":true}\n'
        with (
            patch(
                "conformance.runners.django.runner.generate_suite", return_value={}
            ),
            patch(
                "conformance.runners.django.runner.canonical_json",
                return_value=generated,
            ),
            patch("conformance.runners.django.runner._write_atomic") as write_atomic,
        ):
            status = main(
                [
                    "--manifest",
                    str(DEFAULT_MIGRATION_DEFINITION_SOURCE_MANIFEST),
                ]
            )

        self.assertEqual(status, 0)
        write_atomic.assert_called_once_with(
            DEFAULT_MIGRATION_DEFINITION_SOURCE_ORACLE,
            generated,
        )

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
