"""Independent calendar-date observations from Django 6.1 / DRF 3.18.0.

Both upstream projects are BSD-3-Clause references. This runner uses public
field, form, serializer and schema-editor APIs without reading GoDj or expected
artifacts. Date and datetime inputs remain distinct in the raw observations.
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
    return value.isoformat() if isinstance(value, (datetime.date, datetime.datetime)) else value


def observe():
    if django.get_version() != "6.1" or rest_framework.VERSION != "3.18.0":
        raise RuntimeError("calendar Date reference requires pinned Django/DRF")
    with tempfile.TemporaryDirectory(prefix="godj-calendar-date-reference-") as directory:
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

        strings = ["", "2026-09-20", "2026-9-2", " 2026-09-20 ", "20260920", "2026-W38-7",
                   "0001-01-01", "9999-12-31", "0000-01-01", "10000-01-01", "1900-02-29",
                   "2000-02-29", "2024-02-29", "2026-02-29", "2026-04-31", "2026-00-10", "2026-13-10",
                   "2026-09-00", "2026-09-32", "09/20/2026", "09/20/26", "September 20, 2026",
                   "2026-09-20T00:00:00Z", "2026-09-20 00:00:00", "2026-09-20\x00tail", "invalid"]
        strings += ["2026-W01", "2026W387", "2026W38", "2026-W53-7", "2021-W53-1", "2026-W00-1",
                    "9999-W52-7", "0001-W01-1", "2026-W38-0", "2026-W38-8", "2026-w38-7", "2026-W387",
                    "2026-09-20\n", "２０２６-０９-２０", "2026-٠٩-٢٠", "00010101", "99991231", "  ",
                    "Sep 20 2026", "Sep 20, 2026", "20 Sep 2026", "20 Sep, 2026", "September 20 2026",
                    "20 September 2026", "20 September, 2026", "September  2, 2026", "  9/2/26  ",
                    "1/1/68", "1/1/69", "2026-9- 2", "2026- 9-2", "2026-1-2x", "2026-1-2\nX"]
        typed = [None, True, False, 0, 1, *strings,
                 datetime.date(2026, 9, 20), datetime.datetime(2026, 9, 20, 0, 30),
                 datetime.datetime(2026, 9, 20, 0, 30, tzinfo=datetime.timezone(datetime.timedelta(hours=9)))]
        field = models.DateField(null=True)
        model_observations = []
        for value in typed:
            entry = {"type": type(value).__name__, "input": rendered(value)}
            try:
                entry.update(value=rendered(field.to_python(value)), codes=[])
            except ValidationError as error:
                entry["codes"] = [error.code]
            except (TypeError, ValueError) as error:
                entry["exception"] = type(error).__name__
            model_observations.append(entry)

        form_observations = []
        for required in [False, True]:
            class DateForm(forms.Form):
                day = forms.DateField(required=required)

            for raw in [None, *strings]:
                data = {} if raw is None else {"day": raw}
                form = DateForm(data=data)
                valid = form.is_valid()
                form_observations.append({
                    "required": required, "input": data, "valid": valid,
                    "cleaned": {key: rendered(value) for key, value in form.cleaned_data.items()},
                    "errors": {key: [error.code for error in values] for key, values in form.errors.as_data().items()},
                    "changed": {name: DateForm(data=data, initial={"day": initial}).has_changed()
                                for name, initial in [("null", None), ("same", datetime.date(2026, 9, 20))]},
                    "widget": type(form.fields["day"].widget).__name__,
                })

        class OptionalSerializer(serializers.Serializer):
            day = serializers.DateField(allow_null=True, required=False)

        class DefaultSerializer(serializers.Serializer):
            day = serializers.DateField(allow_null=True, default=datetime.date(2026, 9, 20))

        serializer_observations = []
        for name, serializer in [("optional", OptionalSerializer), ("default", DefaultSerializer)]:
            for partial in [False, True]:
                for data in [{}, *[{"day": value} for value in typed]]:
                    instance = serializer(data=data, partial=partial)
                    valid = instance.is_valid()
                    serializer_observations.append({
                        "serializer": name, "partial": partial,
                        "input": {key: {"type": type(value).__name__, "value": rendered(value)} for key, value in data.items()},
                        "valid": valid,
                        "validated": {key: rendered(value) for key, value in instance.validated_data.items()},
                        "errors": {key: [error.code for error in values] for key, values in instance.errors.items()},
                    })

        class Original(models.Model):
            label = models.TextField()

            class Meta:
                app_label = "calendarref"
                db_table = "calendarref_record"

        class Current(models.Model):
            label = models.TextField()
            day = models.DateField(null=True)

            class Meta:
                app_label = "calendarref"
                db_table = "calendarref_record"

        class Link(models.Model):
            label = models.TextField()
            record = models.ForeignKey(Current, null=True, on_delete=models.SET_NULL, related_name="links")

            class Meta:
                app_label = "calendarref"
                db_table = "calendarref_link"

        def rows():
            return [[label, rendered(day)] for label, day in Current.objects.order_by("id").values_list("label", "day")]

        with connection.cursor() as cursor:
            cursor.execute("SELECT sqlite_version(), sqlite_source_id()")
            sqlite_version, sqlite_source_id = cursor.fetchone()
        with connection.schema_editor() as editor:
            editor.create_model(Original)
        Original.objects.create(label="existing")
        with connection.schema_editor() as editor:
            editor.add_field(Original, Current._meta.get_field("day"))
            editor.create_model(Link)
        after_add = rows()
        for label, day in [("minimum", datetime.date(1, 1, 1)), ("leap", datetime.date(2000, 2, 29)),
                           ("maximum", datetime.date(9999, 12, 31)), ("null", None)]:
            Current.objects.create(label=label, day=day)
        connection.close()
        reopened = rows()
        leap = datetime.date(2000, 2, 29)
        predicates = {
            "exact": Current.objects.filter(day=leap), "not_exact": Current.objects.exclude(day=leap),
            "null": Current.objects.filter(day__isnull=True), "gt": Current.objects.filter(day__gt=leap),
            "gte": Current.objects.filter(day__gte=leap), "lt": Current.objects.filter(day__lt=leap),
            "lte": Current.objects.filter(day__lte=leap), "in_null": Current.objects.filter(day__in=[leap, None]),
            "not_in_null": Current.objects.exclude(day__in=[leap, None]),
        }
        queries = {name: list(query.order_by("id").values_list("label", flat=True)) for name, query in predicates.items()}
        aggregates = {key: rendered(value) for key, value in Current.objects.aggregate(minimum=models.Min("day"), maximum=models.Max("day")).items()}
        for record in Current.objects.order_by("id"):
            Link.objects.create(label=record.label, record=record)
        Link.objects.create(label="leap_again", record=Current.objects.get(label="leap"))
        Link.objects.create(label="missing", record=None)
        related = {
            "exact": Link.objects.filter(record__day=leap), "not_exact": Link.objects.exclude(record__day=leap),
            "null": Link.objects.filter(record__day__isnull=True), "gt": Link.objects.filter(record__day__gt=leap),
            "gte": Link.objects.filter(record__day__gte=leap), "lt": Link.objects.filter(record__day__lt=leap),
            "lte": Link.objects.filter(record__day__lte=leap), "in_null": Link.objects.filter(record__day__in=[leap, None]),
            "not_in_null": Link.objects.exclude(record__day__in=[leap, None]),
        }
        relations = {name: list(query.order_by("id").values_list("label", flat=True)) for name, query in related.items()}
        Current.objects.filter(label="leap").update(day=None)
        Current.objects.filter(label="null").update(day=leap)
        after_update = rows()
        with connection.schema_editor() as editor:
            editor.delete_model(Link)
            editor.remove_field(Current, Current._meta.get_field("day"))
        after_remove = list(Original.objects.order_by("id").values_list("label", flat=True))
        connection.close()
        return {
            "django": django.get_version(), "drf": rest_framework.VERSION, "python": platform.python_version(),
            "timezone": "UTC", "language": "en-us", "sqlite": {"version": sqlite_version, "source_id": sqlite_source_id},
            "model": model_observations, "form": form_observations, "serializer": serializer_observations,
            "database": {"after_add": after_add, "reopened": reopened, "queries": queries, "relations": relations, "aggregates": aggregates, "after_update": after_update, "after_remove": after_remove},
        }


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, separators=(",", ":"), allow_nan=False))
