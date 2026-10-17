"""Independent duration observations from Django 6.1 / DRF 3.18.0.

Both upstream projects are BSD-3-Clause references. This runner uses public
field, form, serializer and schema-editor APIs without reading GoDj or expected
artifacts. Duration, time, date and datetime inputs remain distinct in the raw observations.
"""

import datetime
import json
import platform
import tempfile
from pathlib import Path

import django
import rest_framework
from django.conf import settings


def rendered(value):
    if isinstance(value, datetime.timedelta):
        from django.utils.duration import duration_string
        return duration_string(value)
    return value.isoformat() if isinstance(value, (datetime.date, datetime.datetime, datetime.time)) else value


def observe():
    if django.get_version() != "6.1" or rest_framework.VERSION != "3.18.0":
        raise RuntimeError("duration reference requires pinned Django/DRF")
    with tempfile.TemporaryDirectory(prefix="godj-duration-reference-") as directory:
        settings.configure(
            SECRET_KEY="independent-reference-only", INSTALLED_APPS=[],
            DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}},
            USE_TZ=True, TIME_ZONE="UTC", LANGUAGE_CODE="en-us",
        )
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError
        from django.db import connection, models
        from rest_framework import serializers

        strings = ["", "0", "00:00:00", "1", "-1", "1 00:00:00", "-1 23:59:59.999999", "00:00:00.000001",
                   "1 02:03:04.123456", "00:00:00.0000005", "00:00:00.0000015", "-00:00:00.0000015",
                   "1:2:3", "100:00:00", "00:60:00", "00:00:60", "1 day, 02:03:04.123456", "2 days 02:03:04", "-1 day, 00:00:01",
                   "P", "PT", "P1W", "P1DT2H3M4.123456S", "PT0.0000005S", "PT0.0000015S", "-PT0.0000015S",
                   "P0.5D", "P0.5W", "+P1D", "-P1D", "P1Y", "P1M", "PT1H2M", "1 days", "-1 days +23:59:59.999999",
                   " 1 ", "  ", "1\n", "1\n\n", "١", "1\x00", "1.5", "1,5", "invalid",
                   "106751991 04:00:54.775807", "-106751992 19:59:05.224192",
                   "106751991 04:00:54.775808", "-106751992 19:59:05.224191",
                   "999999999 23:59:59.999999", "-999999999 00:00:00", "1000000000 00:00:00", "-1000000000 00:00:00"]
        typed = [None, True, False, 0, 1, -1, 0.5, *strings, datetime.timedelta(),
                 datetime.timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=123456),
                 datetime.timedelta(microseconds=-1), datetime.timedelta.min, datetime.timedelta.max,
                 datetime.date(2026, 9, 20), datetime.datetime(2026, 9, 20, 12, 34, 56), datetime.time(12, 34, 56)]
        field = models.DurationField(null=True)
        model_observations = []
        for value in typed:
            entry = {"type": type(value).__name__, "input": rendered(value)}
            try:
                entry.update(value=rendered(field.to_python(value)), codes=[])
            except ValidationError as error:
                entry["codes"] = [error.code]
            except (TypeError, ValueError, OverflowError) as error:
                entry["exception"] = type(error).__name__
            model_observations.append(entry)

        form_observations = []
        for required in [False, True]:
            class DurationForm(forms.Form):
                elapsed = forms.DurationField(required=required)

            for raw in [None, *strings]:
                data = {} if raw is None else {"elapsed": raw}
                form = DurationForm(data=data)
                valid = form.is_valid()
                form_observations.append({
                    "required": required, "input": data, "valid": valid,
                    "cleaned": {key: rendered(value) for key, value in form.cleaned_data.items()},
                    "errors": {key: [error.code for error in values] for key, values in form.errors.as_data().items()},
                    "changed": {name: DurationForm(data=data, initial={"elapsed": initial}).has_changed()
                                for name, initial in [("null", None), ("zero", datetime.timedelta()),
                                                      ("same", datetime.timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=123456))]},
                    "widget": type(form.fields["elapsed"].widget).__name__,
                })

        class OptionalSerializer(serializers.Serializer):
            elapsed = serializers.DurationField(allow_null=True, required=False)

        class DefaultSerializer(serializers.Serializer):
            elapsed = serializers.DurationField(allow_null=True, default=datetime.timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=123456))

        serializer_observations = []
        for name, serializer in [("optional", OptionalSerializer), ("default", DefaultSerializer)]:
            for partial in [False, True]:
                for data in [{}, *[{"elapsed": value} for value in typed]]:
                    instance = serializer(data=data, partial=partial)
                    valid = instance.is_valid()
                    serializer_observations.append({
                        "serializer": name, "partial": partial,
                        "input": {key: {"type": type(value).__name__, "value": rendered(value)} for key, value in data.items()},
                        "valid": valid,
                        "validated": {key: rendered(value) for key, value in instance.validated_data.items()},
                        "errors": {key: [error.code for error in values] for key, values in instance.errors.items()},
                    })

        json_number_observations = []
        for raw in ["0.5", "1e2", "1.0", "-0", "-0.0", "1e-4", "1e-5", "0.000001", "0.0000005", "1.2345678", "9007199254740993", "999999.99999999999999999", "1e16", "1e309", "1e-9999"]:
            instance = OptionalSerializer(data=json.loads('{"elapsed":' + raw + '}'))
            valid = instance.is_valid()
            json_number_observations.append({"raw": raw, "valid": valid,
                "validated": {key: rendered(value) for key, value in instance.validated_data.items()},
                "errors": {key: [error.code for error in values] for key, values in instance.errors.items()}})

        class Original(models.Model):
            label = models.TextField()

            class Meta:
                app_label = "durationref"
                db_table = "durationref_record"

        class Current(models.Model):
            label = models.TextField()
            elapsed = models.DurationField(null=True)

            class Meta:
                app_label = "durationref"
                db_table = "durationref_record"

        class Link(models.Model):
            label = models.TextField()
            record = models.ForeignKey(Current, null=True, on_delete=models.SET_NULL, related_name="links")

            class Meta:
                app_label = "durationref"
                db_table = "durationref_link"

        def rows():
            return [[label, rendered(elapsed)] for label, elapsed in Current.objects.order_by("id").values_list("label", "elapsed")]

        with connection.cursor() as cursor:
            cursor.execute("SELECT sqlite_version(), sqlite_source_id()")
            sqlite_version, sqlite_source_id = cursor.fetchone()
        with connection.schema_editor() as editor:
            editor.create_model(Original)
        Original.objects.create(label="existing")
        with connection.schema_editor() as editor:
            editor.add_field(Original, Current._meta.get_field("elapsed"))
            editor.create_model(Link)
        after_add = rows()
        for label, elapsed in [("minimum", datetime.timedelta(microseconds=-9223372036854775808)), ("fraction", datetime.timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=123456)),
                           ("maximum", datetime.timedelta(microseconds=9223372036854775807)), ("null", None)]:
            Current.objects.create(label=label, elapsed=elapsed)
        connection.close()
        reopened = rows()
        fraction = datetime.timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=123456)
        predicates = {
            "exact": Current.objects.filter(elapsed=fraction), "not_exact": Current.objects.exclude(elapsed=fraction),
            "null": Current.objects.filter(elapsed__isnull=True), "gt": Current.objects.filter(elapsed__gt=fraction),
            "gte": Current.objects.filter(elapsed__gte=fraction), "lt": Current.objects.filter(elapsed__lt=fraction),
            "lte": Current.objects.filter(elapsed__lte=fraction), "in_null": Current.objects.filter(elapsed__in=[fraction, None]),
            "not_in_null": Current.objects.exclude(elapsed__in=[fraction, None]),
        }
        queries = {name: list(query.order_by("id").values_list("label", flat=True)) for name, query in predicates.items()}
        aggregates = {key: rendered(value) for key, value in Current.objects.aggregate(minimum=models.Min("elapsed"), maximum=models.Max("elapsed")).items()}
        for record in Current.objects.order_by("id"):
            Link.objects.create(label=record.label, record=record)
        Link.objects.create(label="fraction_again", record=Current.objects.get(label="fraction"))
        Link.objects.create(label="missing", record=None)
        related = {
            "exact": Link.objects.filter(record__elapsed=fraction), "not_exact": Link.objects.exclude(record__elapsed=fraction),
            "null": Link.objects.filter(record__elapsed__isnull=True), "gt": Link.objects.filter(record__elapsed__gt=fraction),
            "gte": Link.objects.filter(record__elapsed__gte=fraction), "lt": Link.objects.filter(record__elapsed__lt=fraction),
            "lte": Link.objects.filter(record__elapsed__lte=fraction), "in_null": Link.objects.filter(record__elapsed__in=[fraction, None]),
            "not_in_null": Link.objects.exclude(record__elapsed__in=[fraction, None]),
        }
        relations = {name: list(query.order_by("id").values_list("label", flat=True)) for name, query in related.items()}
        Current.objects.filter(label="fraction").update(elapsed=None)
        Current.objects.filter(label="null").update(elapsed=fraction)
        after_update = rows()
        storage_overflow = []
        for micros in [-9223372036854775809, 9223372036854775808]:
            try:
                Current.objects.create(label="overflow", elapsed=datetime.timedelta(microseconds=micros))
                storage_overflow.append({"microseconds": micros, "exception": None, "rows": rows()})
            except (OverflowError, ValueError) as error:
                storage_overflow.append({"microseconds": micros, "exception": type(error).__name__, "rows": rows()})

        with connection.schema_editor() as editor:
            editor.delete_model(Link)
            editor.remove_field(Current, Current._meta.get_field("elapsed"))
        after_remove = list(Original.objects.order_by("id").values_list("label", flat=True))
        connection.close()
        return {
            "django": django.get_version(), "drf": rest_framework.VERSION, "python": platform.python_version(),
            "timezone": "UTC", "language": "en-us", "sqlite": {"version": sqlite_version, "source_id": sqlite_source_id},
            "model": model_observations, "form": form_observations, "serializer": serializer_observations, "json_numbers": json_number_observations,
            "database": {"after_add": after_add, "reopened": reopened, "queries": queries, "relations": relations, "aggregates": aggregates, "after_update": after_update, "after_remove": after_remove, "storage_overflow": storage_overflow},
        }


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, separators=(",", ":"), allow_nan=False))
