"""Independent Django 6.1 numeric aggregate observations (BSD-3-Clause).

Only synthetic inputs and source-verification.json are read. No GoDj source,
Go output, or expected result fixture is consumed. This exploratory observer
does not assert a GoDj aggregate contract or product conformance.
"""
from datetime import timedelta
from decimal import Decimal
import hashlib
import json
import locale
import os
from pathlib import Path
import platform
import re
import sqlite3
import sys
import time

import django
from django.conf import settings


def canonical(value):
    if value is None or isinstance(value, (str, bool)):
        return value
    if isinstance(value, Decimal):
        return {"type": "Decimal", "text": str(value)}
    if isinstance(value, timedelta):
        total = (value.days * 86400 + value.seconds) * 1000000 + value.microseconds
        return {"type": "timedelta", "microseconds": str(total)}
    if isinstance(value, int):
        return {"type": "int", "text": str(value)}
    if isinstance(value, float):
        return {"type": "float", "text": repr(value), "hex": value.hex()}
    if isinstance(value, dict):
        return {key: canonical(entry) for key, entry in value.items()}
    if isinstance(value, (tuple, list)):
        return [canonical(entry) for entry in value]
    raise TypeError("unsupported observed value type: " + type(value).__name__)


def observe():
    os.environ["TZ"] = "UTC"
    time.tzset()
    locale.setlocale(locale.LC_ALL, "C")
    directory = Path(__file__).resolve().parent
    source_receipt = directory / "source-verification.json"
    receipt = json.loads(source_receipt.read_text())
    root = Path(django.__file__).parent
    for relative, source in receipt["sources"].items():
        assert hashlib.sha256((root / relative).read_bytes()).hexdigest() == source["sha256"]
    assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
    assert sqlite3.sqlite_version == receipt["runtime"]["sqlite"]
    assert not settings.configured
    database = {"ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}
    name = os.environ.get("GODJ_NUMERIC_REFERENCE_DATABASE")
    if name:
        assert re.fullmatch(r"godj_numeric_reference_[0-9]+", name)
        import psycopg
        assert psycopg.__version__ == "3.3.6"
        database = {
            "ENGINE": "django.db.backends.postgresql", "NAME": name,
            "HOST": os.environ["PGHOST"], "PORT": os.environ["PGPORT"],
            "USER": os.environ["PGUSER"], "PASSWORD": os.environ["PGPASSWORD"],
            "OPTIONS": {"options": "-c statement_timeout=30000 -c lock_timeout=20000"},
        }
    settings.configure(
        INSTALLED_APPS=[],
        DATABASES={"default": database},
        SECRET_KEY="synthetic-numeric-reference",
        USE_TZ=True, TIME_ZONE="UTC", USE_I18N=False,
        DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
    )
    django.setup()
    from django.db import connection, connections, models
    from django.db.models import Avg, Q, Sum
    from django.test.utils import CaptureQueriesContext

    class Measure(models.Model):
        group_key = models.CharField(max_length=20, null=True)
        enabled = models.BooleanField(default=True)
        amount = models.BigIntegerField(null=True)
        score = models.FloatField(null=True)
        price = models.DecimalField(max_digits=14, decimal_places=2, null=True)
        precise_price = models.DecimalField(max_digits=40, decimal_places=24, null=True)
        wide_price = models.DecimalField(max_digits=40, decimal_places=2, null=True)
        full_price = models.DecimalField(max_digits=1000, decimal_places=1000, null=True)
        full_whole_price = models.DecimalField(max_digits=1000, decimal_places=0, null=True)
        elapsed = models.DurationField(null=True)

        class Meta:
            app_label = "numeric_aggregate_observer"
            db_table = "numeric_aggregate_measure"

    assert not connection.introspection.table_names()
    with connection.schema_editor() as editor:
        editor.create_model(Measure)
    observed = []

    def action(name, callback):
        result = {"name": name}
        with CaptureQueriesContext(connection) as queries:
            try:
                result["result"] = canonical(callback())
            except Exception as error:
                result["error"] = {"type": type(error).__name__, "message": str(error),
                                   "sqlstate": getattr(error.__cause__, "sqlstate", None)}
        result["sql"] = [query["sql"] for query in queries]
        return result

    def record(name, rows, fields, grouped=False):
        Measure.objects.all().delete()
        Measure.objects.bulk_create(Measure(**row) for row in rows)
        item = {"name": name, "input": canonical(rows), "actions": []}
        for field in fields:
            for aggregate in (Sum, Avg):
                label = aggregate.__name__ + "_" + field
                for variant, source, options in (
                    ("plain", Measure.objects.all(), {}),
                    ("distinct", Measure.objects.all(), {"distinct": True}),
                    ("filtered", Measure.objects.all(), {"filter": Q(enabled=True)}),
                    ("filter_empty", Measure.objects.all(), {"filter": Q(pk__lt=0)}),
                    ("native_empty", Measure.objects.filter(pk__lt=0), {}),
                    ("folded_empty", Measure.objects.none(), {}),
                ):
                    expression = aggregate(field, **options)
                    item["actions"].append(action(
                        label + "_" + variant,
                        lambda source=source, expression=expression: source.aggregate(value=expression),
                    ))
                if grouped:
                    item["actions"].append(action(
                        label + "_groups",
                        lambda field=field, aggregate=aggregate: list(
                            Measure.objects.values("group_key").annotate(
                                total=aggregate(field), opened=aggregate(field, filter=Q(enabled=True)),
                                unique=aggregate(field, distinct=True),
                            ).order_by("group_key")
                        ),
                    ))
                    item["actions"].append(action(
                        label + "_having_null",
                        lambda field=field, aggregate=aggregate: list(
                            Measure.objects.values("group_key").annotate(total=aggregate(field))
                            .filter(total__isnull=True).order_by("group_key")
                        ),
                    ))
        for lookup, bound in (("gte", 1), ("gte", 2), ("lte", 1), ("lte", 2)):
            item["actions"].append(action(
                "Avg_elapsed_having_" + lookup + "_" + str(bound),
                lambda lookup=lookup, bound=bound: list(
                    Measure.objects.values("group_key").annotate(total=Avg("elapsed"))
                    .filter(**{"total__" + lookup: timedelta(microseconds=bound)}).order_by("group_key")
                ),
            ))
        with connection.cursor() as cursor:
            cursor.execute("SELECT group_key, enabled, amount, score, price, elapsed, precise_price, wide_price, full_price, full_whole_price FROM numeric_aggregate_measure ORDER BY id")
            item["physical_after"] = canonical(cursor.fetchall())
        item["row_count_after"] = Measure.objects.count()
        assert item["row_count_after"] == len(rows)
        observed.append(item)

    try:
        for name, values in (
            ("duration_half_even_up", [1, 2]),
            ("duration_half_even_down", [2, 3]),
            ("duration_upper_int64_identity", [(1 << 63) - 1]),
            ("duration_lower_int64_identity", [-(1 << 63)]),
        ):
            record(name, [{"group_key": "all", "elapsed": timedelta(microseconds=value)} for value in values], ("elapsed",), grouped=True)
        types = {}
        for field in ("amount", "score", "price", "elapsed", "precise_price", "wide_price", "full_price", "full_whole_price"):
            for aggregate in (Sum, Avg):
                output = aggregate(field).resolve_expression(Measure.objects.all().query).output_field
                types[aggregate.__name__ + "_" + field] = {
                    "class": type(output).__name__, "null": output.null,
                    "max_digits": getattr(output, "max_digits", None),
                    "decimal_places": getattr(output, "decimal_places", None),
                }
        if connection.vendor == "sqlite":
            with connection.cursor() as cursor:
                cursor.execute("SELECT sqlite_source_id()")
                assert cursor.fetchone()[0] == receipt["runtime"]["sqlite_source_id"]
            database_profile = {key: receipt["runtime"][key] for key in ("sqlite", "sqlite_source_id")}
        else:
            with connection.cursor() as cursor:
                cursor.execute("SELECT current_setting('server_version_num'), current_setting('server_encoding'), "
                               "datlocprovider, datcollate, datctype FROM pg_database WHERE datname=current_database()")
                values = cursor.fetchone()
            assert values == ("170010", "UTF8", "c", "C", "C")
            database_profile = dict(zip(("server_version_num", "encoding", "locale_provider", "collate", "ctype"), values))
        return {
            "kind": "django-duration-aggregate-boundary-reference-v1",
            "scope": "reference observations only; no GoDj implementation or conformance claim",
            "backend": connection.vendor, "database_profile": database_profile,
            "runtime": {key: value for key, value in receipt["runtime"].items() if not key.startswith("sqlite")},
            "django_commit": receipt["django_commit"],
            "license": "BSD-3-Clause", "source_verification_sha256": hashlib.sha256(source_receipt.read_bytes()).hexdigest(),
            "observer_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            "result_field_types": types, "cases": observed,
        }
    finally:
        with connection.schema_editor() as editor:
            editor.delete_model(Measure)
        assert not connection.introspection.table_names()
        connections.close_all()


if __name__ == "__main__":
    data = observe()
    print(json.dumps(data, sort_keys=True, indent=2, allow_nan=False))
