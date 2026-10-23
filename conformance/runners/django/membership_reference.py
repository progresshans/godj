"""Independent scalar IN observations for the locked Django 6.1 reference.

Authority: django/db/models/lookups.py In and django/db/models/sql/query.py
negated nullable lookups (BSD-3-Clause). Scenarios are independent (derived=false).
Run as a fresh process: the reference owns an isolated in-memory SQLite database.
This is not a GoDj implementation or current PostgreSQL verification.
"""
import datetime
import json

import django
from django.conf import settings


def observe():
    if django.get_version() != "6.1" or settings.configured:
        raise RuntimeError("membership reference requires fresh locked Django 6.1")
    settings.configure(
        SECRET_KEY="membership-reference-only",
        USE_TZ=True,
        TIME_ZONE="UTC",
        USE_I18N=False,
        INSTALLED_APPS=[],
        DATABASES={
            "default": {"ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}
        },
    )
    django.setup()
    from django.db import connection, models
    from django.db.models import Q
    from django.test.utils import CaptureQueriesContext

    class Entry(models.Model):
        name = models.CharField(max_length=20)
        note = models.TextField(null=True)
        rank = models.BigIntegerField(null=True)
        at = models.DateTimeField(null=True)
        active = models.BooleanField(default=False)

        class Meta:
            app_label = "membership_reference"

    with connection.schema_editor() as editor:
        editor.create_model(Entry)
    instant = datetime.datetime(2026, 9, 19, 3, 34, 56, 123456, tzinfo=datetime.UTC)
    minimum = datetime.datetime(1, 1, 1, tzinfo=datetime.UTC)
    maximum = datetime.datetime(9999, 12, 31, 23, 59, 59, 999999, tzinfo=datetime.UTC)
    offset = instant.astimezone(datetime.timezone(datetime.timedelta(hours=9)))
    Entry.objects.bulk_create(
        [
            Entry(name="alpha"),
            Entry(name="beta", note="beta", rank=-1, at=instant, active=True),
            Entry(name="gamma", note="", rank=0, at=minimum),
            Entry(name="delta", note="delta", rank=2**63 - 1, at=maximum, active=True),
        ]
    )
    cases = []

    def encode_input(value):
        if value is None:
            return {"kind": "null"}
        if isinstance(value, bool):
            return {"kind": "boolean", "boolean": value}
        if isinstance(value, int):
            return {"kind": "integer", "integer": value}
        if isinstance(value, datetime.datetime):
            return {"kind": "datetime", "datetime": value.isoformat()}
        if isinstance(value, str):
            return {"kind": "string", "string": value}
        raise TypeError("unsupported independent reference input")

    def record(name, query, field, values, composition):
        with CaptureQueriesContext(connection) as captured:
            ids = list(query.order_by("id").values_list("id", flat=True))
        cases.append({"name": name, "field": field, "values": [encode_input(v) for v in values],
                      "composition": composition, "ids": ids, "queries": len(captured)})

    for field, values, missing in [
        ("id", [2, 2, 4], [999]),
        ("name", ["beta", "beta", "delta"], ["missing"]),
        ("note", [None, "", ""], ["missing"]),
        ("rank", [None, -1, 0, 0], [999]),
        ("at", [None, instant, offset], [datetime.datetime(2020, 1, 1, tzinfo=datetime.UTC)]),
        ("active", [False, False], [True]),
    ]:
        for variant, items in [
            ("values", values),
            ("without-null", [value for value in values if value is not None]),
            ("empty", []),
            ("null-only", [None]),
            ("missing", missing),
        ]:
            condition = Q(**{field + "__in": items})
            name = field + ":" + variant
            record(name, Entry.objects.filter(condition), field, items, "plain")
            record(name + ":not", Entry.objects.filter(~condition), field, items, "not")
            record(name + ":or", Entry.objects.filter(condition | Q(name="alpha")), field, items, "or")
            record(name + ":and", Entry.objects.filter(condition & Q(active=True)), field, items, "and")
    return {"django": django.get_version(), "timezone": "UTC", "backend": "sqlite", "cases": cases}


if __name__ == "__main__":
    print(json.dumps(observe(), indent=2))
