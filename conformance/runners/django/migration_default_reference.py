"""Independent Django 6.1 populated-table AddField defaults (BSD-3-Clause).

Uses Django migration state and real schema editors. No Go source, SQL compiler
or expected capture is read. Only fresh private reference databases are used.
"""
from datetime import date, datetime, time, timedelta, timezone
from decimal import Decimal
import hashlib
import importlib.metadata
import inspect
import json
import os
import platform
import re
import tempfile
from pathlib import Path
from uuid import UUID

import django
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    name = os.environ.get("GODJ_MIGRATION_DEFAULT_REFERENCE_DATABASE")
    if name:
        assert re.fullmatch(r"godj_migration_default_reference_[0-9]+", name)
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-migration-default-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name,
                        "HOST": os.environ.get("PGHOST", "127.0.0.1"), "PORT": os.environ.get("PGPORT", "5432"),
                        "USER": os.environ.get("PGUSER", "postgres"), "PASSWORD": os.environ.get("PGPASSWORD", "")}
        settings.configure(SECRET_KEY="independent-default-reference-only", USE_TZ=True,
                           INSTALLED_APPS=[], DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField")
        django.setup()
        from django.db import connection, connections, models, transaction, IntegrityError
        from django.db.migrations import AddField, CreateModel
        from django.db.migrations.state import ProjectState
        from django.db.migrations.operations import fields as field_operations
        from django.db.backends.base import schema as base_schema
        from django.db.backends.sqlite3 import schema as sqlite_schema
        assert not connection.introspection.table_names()
        cases = {
            "integer": models.BigIntegerField(default=1),
            "integer_min": models.BigIntegerField(default=-(2 ** 63)),
            "boolean_false": models.BooleanField(default=False),
            "empty_text": models.CharField(max_length=100, default=""),
            "quoted_text": models.TextField(default="O'Brien\\\n한"),
            "nullable_default": models.CharField(max_length=100, null=True, default="filled"),
            "float": models.FloatField(default=1.25),
            "decimal": models.DecimalField(max_digits=9, decimal_places=2, default=Decimal("1.25")),
            "uuid": models.UUIDField(default=UUID("12000000-0000-4000-8000-0000000000ff")),
            "json": models.JSONField(default={"large": 9007199254740993, "text": "한"}),
            "json_null": models.JSONField(default=None),
            "date": models.DateField(default=date(2000, 2, 29)),
            "time": models.TimeField(default=time(23, 59, 58, 123456)),
            "duration": models.DurationField(default=timedelta(microseconds=-123)),
            "datetime": models.DateTimeField(default=datetime(2026, 9, 27, 1, 2, 3, 456789, tzinfo=timezone(timedelta(hours=3)))),
        }
        # Django's JSONField default=None means SQL NULL in migration backfill,
        # while GoDj's explicit JSON null is a typed non-NULL document. That
        # existing JSON distinction is tested as a Go invariant, not conflated.
        del cases["json_null"]
        observations = {}
        try:
            for label, field in cases.items():
                expected = field.clone()
                expected.default = models.NOT_PROVIDED
                before = ProjectState()
                for operation in (
                    CreateModel("Parent", [("id", models.BigAutoField(primary_key=True)), ("expected", expected)]),
                    CreateModel("Child", [("id", models.BigAutoField(primary_key=True)), ("parent", models.ForeignKey("default_fixture.Parent", on_delete=models.CASCADE))]),
                ):
                    after = before.clone()
                    operation.state_forwards("default_fixture", after)
                    with connection.schema_editor() as editor:
                        operation.database_forwards("default_fixture", editor, before, after)
                    before = after
                parent = before.apps.get_model("default_fixture", "Parent")
                child = before.apps.get_model("default_fixture", "Child")
                for _ in range(3):
                    parent.objects.create(expected=field.get_default())
                parent.objects.filter(pk=3).delete()
                for key in (1, 2):
                    child.objects.create(parent_id=key)
                links = list(child.objects.order_by("pk").values_list("pk", "parent_id"))
                operation = AddField("parent", "added", field.clone())
                after = before.clone()
                operation.state_forwards("default_fixture", after)
                for cycle in range(2):
                    with connection.schema_editor() as editor:
                        operation.database_forwards("default_fixture", editor, before, after)
                    updated = after.apps.get_model("default_fixture", "Parent")
                    rows = list(updated.objects.order_by("pk"))
                    matched = sum(row.added == row.expected for row in rows)
                    assert list(child.objects.order_by("pk").values_list("pk", "parent_id")) == links
                    with connection.cursor() as cursor:
                        columns = connection.introspection.get_table_description(cursor, updated._meta.db_table)
                    persistent = next(column.default for column in columns if column.name == "added") is not None
                    if cycle == 0:
                        with connection.schema_editor() as editor:
                            operation.database_backwards("default_fixture", editor, after, before)
                next_row = updated.objects.create(expected=field.get_default(), added=field.get_default())
                table = connection.ops.quote_name(updated._meta.db_table)
                try:
                    with transaction.atomic(), connection.cursor() as cursor:
                        cursor.execute(f'INSERT INTO {table} ("expected") SELECT "expected" FROM {table} WHERE "id"=1')
                    missing = "null" if updated.objects.order_by("-pk").first().added is None else "default"
                except IntegrityError:
                    missing = "rejected"
                observations[label] = {"matched_rows": matched, "links_preserved": len(links) == 2,
                                       "persistent_default": persistent, "next_id": next_row.pk, "raw_missing": missing}
                with connection.schema_editor() as editor:
                    editor.delete_model(child)
                    editor.delete_model(updated)
                assert not connection.introspection.table_names()
            sources = {key: Path(inspect.getfile(module)) for key, module in {
                "field_operations": field_operations, "base_schema": base_schema, "sqlite_schema": sqlite_schema,
            }.items()}
            # Hash the pinned PostgreSQL source without importing its optional
            # driver during SQLite-only replay in the frozen CI environment.
            sources["postgres_schema"] = Path(django.__file__).resolve().parent / "db/backends/postgresql/schema.py"
            return {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                    "database_version": str(connection.pg_version) if name else connection.Database.sqlite_version,
                    "source_sha256": {key: hashlib.sha256(path.read_bytes()).hexdigest() for key, path in sources.items()},
                    "observations": observations}
        finally:
            connections.close_all()


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, indent=2))
