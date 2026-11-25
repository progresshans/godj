"""Observe historical RunPython and migration boundaries with pinned Django 6.1.

The public management commands discover an installed fixture app. Durable
observations reopen the native connection and inspect rows, catalog and recorder.
The observer has no GoDj code or expected capture as input. Django is BSD-3-Clause.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import tempfile

import django
from django.conf import settings
from django.core.management import call_command
from django.db import IntegrityError, connection, connections, transaction
from django.db.migrations.exceptions import IrreversibleError

from .migration_data_fixture import callbacks
from .migration_data_fixture.field_contract import observe_blank_tightening
from .migration_data_fixture.model_deletion import observe_model_deletion
from .migration_data_fixture.model_rename import observe_model_rename

APP = "migration_data_fixture"
MIGRATIONS = ["0001_initial", "0002_expand", "0003_backfill", "0004_contract", "0005_freeze", "0006_tail"]
FIRST, EXPAND, BACKFILL, CONTRACT, FREEZE, TAIL = MIGRATIONS
UPSTREAM = "fe0a859f537d4238cf49fca39073513206f83122"
SOURCE_SHA256 = {
    "db/backends/base/schema.py": "9dc62b1585e378fdd490e66b0dac7b87cd7bf323f8a2fee392d2b48368bc22fe",
    "db/models/fields/__init__.py": "744392031020fc9599ec341132f8c793a8c0ce1e3981b4b304d3b3fcccb8535f",
    "db/backends/postgresql/schema.py": "78617b2c44c17f3404ebadd2db5148cb63d87e078dcc7798155b9cfb932099dc",
    "db/backends/sqlite3/schema.py": "85b1c0c726e9e38f4be1169aa146473d4116efc4f7094c9c936a468dd109e1bf",
    "db/migrations/executor.py": "a41c86b8bb744c3b70128ff1f321dd373d5acd96ca46cd3513adc44c975ca26a",
    "db/migrations/loader.py": "cdc323bdf802553cd668a6df85f5032b5d37922226234a625dac60380b632f91",
    "db/migrations/migration.py": "3eeecc565bca680b83f27ba889df329050432c47e0511ee42f3f92a1dacdf81d",
    "db/migrations/operations/fields.py": "76c0f9945ad2f0c3e1df559e5b6a8bfa1c00c2049f6448342792ad2ea505ac73",
    "db/migrations/operations/models.py": "abfa523998a4bf50a88f93c72579ac24266208726f98b6e93bbc0e4a94fd8494",
    "db/migrations/operations/special.py": "42f2bf60d9919f2e792738aff7685baf4d07b59329ba0c939f95a41ba0a76229",
    "db/migrations/recorder.py": "37a0b5648c6fb0020f5f88ea37b5ce328064bd66948ba6a169c5aa29d10b7af4",
    "db/migrations/state.py": "ae5a45dc89edb7947a32ddfc6245d4dff92ab25f4adb3478fb533911e3dc0dab",
}


def command(name, target, **options):
    stream = io.StringIO()
    call_command(name, APP, target, stdout=stream, no_color=True, **options)
    return stream.getvalue()


def migrate(target):
    command("migrate", target, verbosity=0, interactive=False)


def snapshot():
    connection.close()
    with connection.cursor() as cursor:
        catalog = connection.introspection
        category = [f.name for f in catalog.get_table_description(cursor, "m1_category")]
        code, = [field for field in ("label", "legacy_code") if field in category]
        cursor.execute(f'SELECT id, "{code}" FROM m1_category ORDER BY id')
        categories = cursor.fetchall()

        fields = {f.name: f for f in catalog.get_table_description(cursor, "m1_ticket")}
        extra = fields.get("external_id")
        column = "external_id" if extra is not None else "NULL"
        cursor.execute(f"SELECT id, category_id, note, {column} FROM m1_ticket ORDER BY id")
        tickets = cursor.fetchall()
        constraints = catalog.get_constraints(cursor, "m1_ticket").values()
        unique = any(c["unique"] and c["columns"] == ["external_id"] for c in constraints)
        fk = any(c["foreign_key"] == ("m1_category", "id") and c["columns"] == ["category_id"]
                 for c in constraints)
        cursor.execute("SELECT name FROM django_migrations WHERE app = %s ORDER BY name", [APP])
        return {"applied": [row[0] for row in cursor.fetchall()], "category_column": code,
                "categories": categories, "tickets": tickets, "category_fk": fk,
                "external_nullable": extra.null_ok if extra is not None else None, "external_unique": unique}


def preview(target, expected, *, backward=False, sql_target=BACKFILL):
    before = snapshot()
    calls_before = list(callbacks.calls)
    plan = command("migrate", target, plan=True, interactive=False)
    headings = [line.removeprefix(APP + ".") for line in plan.splitlines() if line.startswith(APP + ".")]
    assert headings == expected, headings
    output = {"plan": plan}
    observed = {"migrations": headings, "irreversible": "IRREVERSIBLE" in plan, "sql_target": sql_target}
    if sql_target == FREEZE:
        try:
            command("sqlmigrate", FREEZE, backwards=True)
        except IrreversibleError as exc:
            assert f"{APP}.{FREEZE} is not reversible" in str(exc)
            observed["sql_error"] = type(exc).__name__
        else:
            raise AssertionError("SQL display crossed the irreversible operation")
    else:
        output["sql"] = command("sqlmigrate", sql_target, backwards=backward)
        observed["non_sql_operations"] = output["sql"].count("THIS OPERATION CANNOT BE WRITTEN AS SQL")
        assert observed["non_sql_operations"] == 2
    if not backward:
        output["expansion_sql"] = command("sqlmigrate", EXPAND)
        assert "ADD COLUMN" in output["expansion_sql"] and '"external_id"' in output["expansion_sql"]
    assert snapshot() == before and callbacks.calls == calls_before
    return observed, output


def check_constraints(before):
    failures = {}
    for kind, value in (("null", None), ("duplicate", before["tickets"][0][3])):
        try:
            with transaction.atomic(), connection.cursor() as cursor:
                cursor.execute("UPDATE m1_ticket SET external_id = %s WHERE id = 17", [value])
        except IntegrityError as exc:
            failures[kind] = type(exc).__name__
        else:
            raise AssertionError(f"the physical {kind} constraint accepted the write")
        assert snapshot() == before
    return failures


def reproduce_failed_backfill():
    if connection.introspection.table_names():
        raise RuntimeError("a fresh private reference database is required")
    from .migration_data_fixture.models import Category

    assert {f.name for f in Category._meta.fields} == {"id", "label"}
    call_command("makemigrations", APP, check_changes=True, interactive=False, verbosity=0, stdout=io.StringIO())
    migrate(FIRST)
    with transaction.atomic(), connection.cursor() as cursor:
        cursor.executemany("INSERT INTO m1_category (id, legacy_code) VALUES (%s, %s)",
                           [(2, "한'글"), (9, "")])
        cursor.executemany("INSERT INTO m1_ticket (id, category_id, note) VALUES (%s, %s, %s)",
                           [(11, 2, ""), (17, 2, ""), (23, 9, "")])
    base = snapshot()
    assert base["category_fk"] and base["applied"] == [FIRST] and base["external_nullable"] is None
    obs, output = {"base": base}, {}
    obs["forward_plan"], output["forward"] = preview(CONTRACT, [EXPAND, BACKFILL, CONTRACT])

    # The failed data migration follows a separately committed schema expansion.
    try:
        migrate(CONTRACT)
    except callbacks.MissingLegacyCode as exc:
        assert exc.args == (23,) and callbacks.calls == [("backfill", 11), ("backfill", 17)]
        obs["forward_error"] = {"error": type(exc).__name__, "key": exc.args[0], "calls": list(callbacks.calls)}
    else:
        raise AssertionError("missing historical input was accepted")
    expanded = snapshot()
    assert expanded == base | {"applied": MIGRATIONS[:2], "external_nullable": True}
    obs["after_failure"] = expanded
    return obs, output


def exercise():
    obs, output = reproduce_failed_backfill()
    expanded = obs["after_failure"]
    with connection.cursor() as cursor:
        cursor.execute("UPDATE m1_category SET legacy_code = %s WHERE id = 9", ["β"])
    callbacks.calls.clear()
    migrate(CONTRACT)
    contracted = snapshot()
    assert contracted["applied"] == MIGRATIONS[:4] and contracted["category_column"] == "label"
    assert contracted["category_fk"] and contracted["external_unique"] and contracted["external_nullable"] is False
    assert contracted["categories"] == [(2, "한'글"), (9, "β")]
    assert contracted["tickets"] == [(11, 2, "reviewed", "한'글/11"), (17, 2, "", "한'글/17"), (23, 9, "", "β/23")]
    assert callbacks.calls == [("backfill", 11), ("backfill", 17), ("backfill", 23), ("note", 11, "reviewed")]
    obs["contracted"] = contracted
    obs["retry_calls"] = list(callbacks.calls)
    obs["constraint_errors"] = check_constraints(contracted)
    callbacks.calls.clear()
    migrate(CONTRACT)
    assert snapshot() == contracted and not callbacks.calls
    obs["reverse_plan"], output["reverse"] = preview(EXPAND, [CONTRACT, BACKFILL], backward=True)
    migrate(EXPAND)
    reversed_state = snapshot()
    assert callbacks.calls == [("clear_backfill", 3)]
    assert reversed_state == expanded | {"categories": contracted["categories"], "tickets": [
        (11, 2, "reviewed", None), (17, 2, "", None), (23, 9, "", None)]}
    obs["reversed"] = reversed_state
    obs["reverse_calls"] = list(callbacks.calls)

    migrate(TAIL)
    full = snapshot()
    assert full == contracted | {"applied": MIGRATIONS, "tickets": [
        (11, 2, "reviewed", "한'글/11"), (17, 2, "frozen", "한'글/17"), (23, 9, "tail", "β/23")]}
    obs["before_barrier"] = full
    obs["barrier_plan"], output["barrier"] = preview(
        EXPAND, [TAIL, FREEZE, CONTRACT, BACKFILL], backward=True, sql_target=FREEZE,
    )
    assert obs["barrier_plan"]["irreversible"]

    # Only the later reversible migration may persist its reverse. The body
    # after the irreversible operation in FREEZE must never even be called.
    for attempt in ("first_reverse", "reopened_reverse"):
        callbacks.calls.clear()
        try:
            migrate(EXPAND)
        except IrreversibleError as exc:
            assert f"{APP}.{FREEZE} is not reversible" in str(exc)
        else:
            raise AssertionError("reverse crossed the irreversible migration")
        partial = snapshot()
        assert partial == full | {"applied": MIGRATIONS[:5], "tickets": [
            *full["tickets"][:2], (23, 9, "", "β/23")]}
        assert callbacks.calls == ([("note", 23, "")] if attempt == "first_reverse" else [])
        obs[attempt] = {"error": "IrreversibleError", "calls": list(callbacks.calls), "database": partial}
    migrate(TAIL)
    obs["resumed"] = snapshot()
    assert obs["resumed"] == full and callbacks.calls == [("note", 23, "tail")]
    return obs, output


def observe():
    if not __debug__ or django.get_version() != "6.1" or platform.python_version() != "3.14.3" or settings.configured:
        raise RuntimeError("a fresh, non-optimized pinned Django 6.1 process is required")
    upstream = Path(django.__file__).parent
    for module, expected in SOURCE_SHA256.items():
        assert hashlib.sha256((upstream / module).read_bytes()).hexdigest() == expected, module
    root = Path(__file__).resolve().parent
    inputs = [Path(__file__).resolve(), *sorted((root / APP).rglob("*.py"))]
    sources = {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs}
    with tempfile.TemporaryDirectory(prefix="godj-migration-data-") as temp:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(temp) / "reference.sqlite3")}
        name = os.environ.get("GODJ_MIGRATION_DATA_REFERENCE_DATABASE")
        if name is not None:
            if not re.fullmatch(r"godj_migration_data_reference_[0-9]+", name):
                raise ValueError("invalid private PostgreSQL reference database")
            import psycopg
            assert psycopg.__version__ == "3.3.6"
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name, "HOST": os.environ["PGHOST"],
                        "PORT": os.environ["PGPORT"], "USER": os.environ["PGUSER"], "PASSWORD": os.environ["PGPASSWORD"]}
        settings.configure(SECRET_KEY="historical-data-reference", USE_TZ=True, TIME_ZONE="UTC", USE_I18N=False,
                           INSTALLED_APPS=["conformance.runners.django." + APP], DATABASES={"default": database},
                           DEFAULT_AUTO_FIELD="django.db.models.AutoField")
        django.setup()
        try:
            with connection.cursor() as cursor:
                cursor.execute("SHOW server_version_num" if name else "SELECT sqlite_version()")
                version = cursor.fetchone()[0]
            assert version == ("170010" if name else "3.50.4"), version
            observations, commands = exercise()
            return {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                    "database_version": version, "django_commit": UPSTREAM, "upstream_sha256": SOURCE_SHA256,
                    "input_sha256": sources, "observations": observations, "command_output": commands,
                    "blank_tightening": observe_blank_tightening(), "model_deletion": observe_model_deletion(),
                    "model_rename": observe_model_rename()}
        finally:
            connections.close_all()


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
