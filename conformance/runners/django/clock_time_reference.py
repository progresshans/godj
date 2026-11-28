"""Independent clock-time observations from Django 6.1 / DRF 3.18.0.

Both upstream projects are BSD-3-Clause references. This runner uses public
field, form, serializer and schema-editor APIs without reading GoDj or expected
artifacts. Time, date and datetime inputs remain distinct in the raw observations.
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
    return value.isoformat() if isinstance(value, (datetime.date, datetime.datetime, datetime.time)) else value


def observe():
    if django.get_version() != "6.1" or rest_framework.VERSION != "3.18.0":
        raise RuntimeError("clock Time reference requires pinned Django/DRF")
    with tempfile.TemporaryDirectory(prefix="godj-clock-time-reference-") as directory:
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

        strings = ["", "00:00:00", "00:00", "12:34:56.123456", "23:59:59.999999", "00:00:00.000001",
                   "00:00:00.000000", "12:34", "1:2:3", "123456", "T12:34:56", "12:34:56.1",
                   "12:34:56.1234567", "12:34:56,123456", "12:34:56Z", "12:34:56+09:00", "12:34:56-12:00",
                   " 12:34:56 ", "  ", "24:00", "24:00:00", "24:00:00.000000", "24:00:00.000001",
                   "24:01:00", "25:00:00", "12:60:00", "12:34:60", "-1:00:00", "12:34:56.abcdef",
                   "2000-01-01", "2000-01-01T12:34:56", "12:34:56\x00tail", "12:34:56\n", "invalid"]
        strings += ["12", "1", "1234", "123", "T123456", "t12:34:56", "12.5", "12:34.5", "12:34,5",
                    "12:34:56.", "12:34:56,", "12:34:56.1234567890123", "12:34:56.0000001",
                    "24", "2400", "T24:00", "24:00:00.0000001", "24:00:00+09:00",
                    "12:34:56+09", "12:34:56+0900", "12:34:56+09:30:00.5", "12:34:56-00:00",
                    "12:34:56+23:59", "12:34:56+24:00", "12:34:56+00:60", "12:34:56+00:00:60",
                    "1:2", "1:02:3.4", "01:02:03.000000", "01:02:03.1234567", "12:34:59",
                    "١٢:٣٤:٥٦", "12:٣٤:٥٦", "１２:３４:５６", "12:34:56.１２３", "12:34:56Zjunk",
                    "12:34:56\n\n", "\t12:34:56\r", "12:34:56 Z", "12:34:56\x00", "12:34:56.1\x00tail"]
        typed = [None, True, False, 0, 1, *strings, datetime.time(), datetime.time(12, 34, 56, 123456),
                 datetime.time(12, 34, 56, 123456, tzinfo=datetime.timezone(datetime.timedelta(hours=9))),
                 datetime.date(2026, 9, 20), datetime.datetime(2026, 9, 20, 12, 34, 56)]
        field = models.TimeField(null=True)
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

        class MicrosecondTimeInput(forms.TimeInput):
            supports_microseconds = True

        form_profiles = {}
        for profile, widget in [("form", forms.TimeInput), ("microsecond_form", MicrosecondTimeInput(format="%H:%M:%S.%f"))]:
            observations = []
            for required in [False, True]:
                class TimeForm(forms.Form):
                    at = forms.TimeField(required=required, widget=widget)

                for raw in [None, *strings]:
                    data = {} if raw is None else {"at": raw}
                    form = TimeForm(data=data)
                    valid = form.is_valid()
                    observations.append({
                        "required": required, "input": data, "valid": valid,
                        "cleaned": {key: rendered(value) for key, value in form.cleaned_data.items()},
                        "errors": {key: [error.code for error in values] for key, values in form.errors.as_data().items()},
                        "changed": {name: TimeForm(data=data, initial={"at": initial}).has_changed()
                                    for name, initial in [("null", None), ("same", datetime.time(12, 34, 56, 123456))]},
                        "widget": type(form.fields["at"].widget).__name__,
                        "supports_microseconds": form.fields["at"].widget.supports_microseconds,
                    })

            form_profiles[profile] = observations

        class OptionalSerializer(serializers.Serializer):
            at = serializers.TimeField(allow_null=True, required=False)

        class DefaultSerializer(serializers.Serializer):
            at = serializers.TimeField(allow_null=True, default=datetime.time(12, 34, 56, 123456))

        serializer_observations = []
        for name, serializer in [("optional", OptionalSerializer), ("default", DefaultSerializer)]:
            for partial in [False, True]:
                for data in [{}, *[{"at": value} for value in typed]]:
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
                app_label = "clockref"
                db_table = "clockref_record"

        class Current(models.Model):
            label = models.TextField()
            at = models.TimeField(null=True)

            class Meta:
                app_label = "clockref"
                db_table = "clockref_record"

        class Link(models.Model):
            label = models.TextField()
            record = models.ForeignKey(Current, null=True, on_delete=models.SET_NULL, related_name="links")

            class Meta:
                app_label = "clockref"
                db_table = "clockref_link"

        def rows():
            return [[label, rendered(at)] for label, at in Current.objects.order_by("id").values_list("label", "at")]

        with connection.cursor() as cursor:
            cursor.execute("SELECT sqlite_version(), sqlite_source_id()")
            sqlite_version, sqlite_source_id = cursor.fetchone()
        with connection.schema_editor() as editor:
            editor.create_model(Original)
        Original.objects.create(label="existing")
        with connection.schema_editor() as editor:
            editor.add_field(Original, Current._meta.get_field("at"))
            editor.create_model(Link)
        after_add = rows()
        for label, at in [("minimum", datetime.time()), ("fraction", datetime.time(12, 34, 56, 123456)),
                           ("maximum", datetime.time(23, 59, 59, 999999)), ("null", None)]:
            Current.objects.create(label=label, at=at)
        connection.close()
        reopened = rows()
        fraction = datetime.time(12, 34, 56, 123456)
        predicates = {
            "exact": Current.objects.filter(at=fraction), "not_exact": Current.objects.exclude(at=fraction),
            "null": Current.objects.filter(at__isnull=True), "gt": Current.objects.filter(at__gt=fraction),
            "gte": Current.objects.filter(at__gte=fraction), "lt": Current.objects.filter(at__lt=fraction),
            "lte": Current.objects.filter(at__lte=fraction), "in_null": Current.objects.filter(at__in=[fraction, None]),
            "not_in_null": Current.objects.exclude(at__in=[fraction, None]),
        }
        queries = {name: list(query.order_by("id").values_list("label", flat=True)) for name, query in predicates.items()}
        aggregates = {key: rendered(value) for key, value in Current.objects.aggregate(minimum=models.Min("at"), maximum=models.Max("at")).items()}
        for record in Current.objects.order_by("id"):
            Link.objects.create(label=record.label, record=record)
        Link.objects.create(label="fraction_again", record=Current.objects.get(label="fraction"))
        Link.objects.create(label="missing", record=None)
        related = {
            "exact": Link.objects.filter(record__at=fraction), "not_exact": Link.objects.exclude(record__at=fraction),
            "null": Link.objects.filter(record__at__isnull=True), "gt": Link.objects.filter(record__at__gt=fraction),
            "gte": Link.objects.filter(record__at__gte=fraction), "lt": Link.objects.filter(record__at__lt=fraction),
            "lte": Link.objects.filter(record__at__lte=fraction), "in_null": Link.objects.filter(record__at__in=[fraction, None]),
            "not_in_null": Link.objects.exclude(record__at__in=[fraction, None]),
        }
        relations = {name: list(query.order_by("id").values_list("label", flat=True)) for name, query in related.items()}
        Current.objects.filter(label="fraction").update(at=None)
        Current.objects.filter(label="null").update(at=fraction)
        after_update = rows()
        with connection.schema_editor() as editor:
            editor.delete_model(Link)
            editor.remove_field(Current, Current._meta.get_field("at"))
        after_remove = list(Original.objects.order_by("id").values_list("label", flat=True))
        connection.close()
        return {
            "django": django.get_version(), "drf": rest_framework.VERSION, "python": platform.python_version(),
            "timezone": "UTC", "language": "en-us", "sqlite": {"version": sqlite_version, "source_id": sqlite_source_id},
            "model": model_observations, **form_profiles, "serializer": serializer_observations,
            "database": {"after_add": after_add, "reopened": reopened, "queries": queries, "relations": relations, "aggregates": aggregates, "after_update": after_update, "after_remove": after_remove},
        }


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, separators=(",", ":"), allow_nan=False))
