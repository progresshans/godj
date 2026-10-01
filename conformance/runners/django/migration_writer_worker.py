"""Isolated Django command worker for migration-writer reference observations."""

from __future__ import annotations

import argparse
import json
import os
import sys
import tempfile
from contextlib import redirect_stderr, redirect_stdout
from io import StringIO
from pathlib import Path
from typing import Any


APP_LABEL = "godj_migration_writer"
APP_CONFIG = (
    "conformance.runners.django.migration_writer_fixture.apps."
    "GoDjMigrationWriterFixtureConfig"
)


def _configure(migration_module: str) -> None:
    from django.conf import settings

    if settings.configured:
        raise RuntimeError("migration-writer worker settings are already configured")
    settings.configure(
        DATABASES={
            "default": {
                "ENGINE": "django.db.backends.sqlite3",
                "NAME": ":memory:",
            }
        },
        DEFAULT_AUTO_FIELD="django.db.models.AutoField",
        INSTALLED_APPS=[APP_CONFIG],
        LANGUAGE_CODE="en-us",
        MIGRATION_MODULES={APP_LABEL: migration_module},
        SECRET_KEY="godj-migration-writer-reference",
        TIME_ZONE="UTC",
        USE_I18N=False,
        USE_TZ=True,
    )
    import django

    django.setup()


def _write_package(root: Path, name: str, *, clean: bool) -> Path:
    package = root / name
    package.mkdir()
    (package / "__init__.py").write_text("", encoding="utf-8")
    if clean:
        (package / "0001_initial.py").write_text(
            """from django.db import migrations, models


class Migration(migrations.Migration):
    initial = True
    dependencies = []
    operations = [
        migrations.CreateModel(
            name=\"Article\",
            fields=[
                (
                    \"id\",
                    models.AutoField(
                        auto_created=True,
                        primary_key=True,
                        serialize=False,
                        verbose_name=\"ID\",
                    ),
                ),
                (\"title\", models.CharField(max_length=200)),
                (\"published\", models.BooleanField(default=False)),
            ],
        ),
    ]
""",
            encoding="utf-8",
        )
    return package


def _files(package: Path) -> list[str]:
    return sorted(
        path.name
        for path in package.iterdir()
        if path.is_file() and path.name != "__pycache__"
    )


def _tables() -> list[str]:
    from django.db import connection

    with connection.cursor() as cursor:
        return sorted(connection.introspection.table_names(cursor))


def _run_command(*arguments: str) -> tuple[int, str, str]:
    from django.core.management import call_command

    stdout = StringIO()
    stderr = StringIO()
    exit_code = 0
    try:
        with redirect_stdout(stdout), redirect_stderr(stderr):
            call_command("makemigrations", *arguments, verbosity=1)
    except SystemExit as error:
        if not isinstance(error.code, int):
            raise RuntimeError("makemigrations emitted a non-integer exit code")
        exit_code = error.code
    return exit_code, stdout.getvalue(), stderr.getvalue()


def _normalized_output(output: str, package: Path) -> list[str]:
    normalized: list[str] = []
    for line in output.splitlines():
        rendered = line.strip()
        if not rendered:
            continue
        rendered = rendered.replace(str(package), "<migration-root>")
        normalized.append(rendered)
    return normalized


def observe(action: str) -> dict[str, Any]:
    with tempfile.TemporaryDirectory(
        prefix="godj-migration-writer-reference-"
    ) as directory:
        root = Path(directory)
        package_name = "writer_migrations"
        package = _write_package(root, package_name, clean=action == "check_clean")
        sys.path.insert(0, directory)
        try:
            _configure(package_name)
            files_before = _files(package)
            tables_before = _tables()
            if action == "dry_run":
                arguments = (APP_LABEL, "--dry-run", "--no-header")
            elif action in {"check_clean", "check_drift"}:
                arguments = (APP_LABEL, "--check", "--no-header")
            else:
                raise ValueError(f"unknown migration-writer worker action: {action}")
            exit_code, stdout, stderr = _run_command(*arguments)
            files_after = _files(package)
            tables_after = _tables()
        finally:
            sys.path.remove(directory)

    return {
        "action": action,
        "exit_code": exit_code,
        "files_before": files_before,
        "files_after": files_after,
        "output": _normalized_output(stdout, package),
        "stderr": _normalized_output(stderr, package),
        "tables_before": tables_before,
        "tables_after": tables_after,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "action", choices=("dry_run", "check_clean", "check_drift")
    )
    arguments = parser.parse_args(argv)
    json.dump(observe(arguments.action), sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
