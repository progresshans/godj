"""Independent binary64 model/form/DRF and SQLite behavior from pinned public APIs.

This preparation runner does not read GoDj source or expected output. Upstream
Django and DRF are BSD-3-Clause references; float bits preserve signed zero and
non-finite observations without writing non-standard JSON numbers.
"""
import datetime
import decimal
import json
import math
import platform
import struct
import tempfile
from pathlib import Path
import django
import rest_framework
from django.conf import settings


def rendered(value):
    if isinstance(value, float):
        return {"bits": struct.pack(">d", value).hex(), "repr": repr(value)}
    if isinstance(value, decimal.Decimal):
        return {"decimal": str(value)}
    if isinstance(value, (list, tuple)):
        return [rendered(item) for item in value]
    if isinstance(value, dict):
        return {key: rendered(item) for key, item in value.items()}
    return value


def observe():
    if django.get_version() != "6.1" or rest_framework.VERSION != "3.18.0":
        raise RuntimeError("float reference requires pinned Django and DRF")
    with tempfile.TemporaryDirectory(prefix="godj-float-reference-") as directory:
        settings.configure(SECRET_KEY="independent-reference-only", INSTALLED_APPS=[],
            DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}},
            USE_TZ=True, TIME_ZONE="UTC", LANGUAGE_CODE="en-us")
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError
        from django.db import connection, models, transaction
        from rest_framework import serializers
        from rest_framework.renderers import JSONRenderer

        strings = ["", " ", "0", "-0", "+0.0", "-0.0", "0.1", "1.5", "+1.5", "-2.5", "1.", ".5", "-.5",
            "1e2", "1e-2", "1E+2", " 1.5 ", "\t1.5\n", "\u00a01.5\u00a0", "\x1c1.5\x1f", "1\n\n", "1 2",
            "1_000.25", "1_0.5_0e+0_2", "1__0", "_1", "1_", "1_.0", "1._0", "1e_2", "1e+_2",
            "١٢.٥", "１２.５", "𝟙.𝟝", "١_٢.٥", "1,5", "0x1p0", "0b10", "01", "1\x00", "1e", "--1", ".",
            "NaN", "nan", "+nan", "-NaN", "inf", "INF", "Infinity", "+Infinity", "-inf", "infinity_", "nan(1)",
            "1e309", "-1e309", "1e-9999", "-1e-9999", "5e-324", "2.4703282292062327e-324", "2.4703282292062328e-324",
            "1.7976931348623157e308", "1.7976931348623159e308", "9007199254740993", "0." + "0" * 1000 + "1", "1" * 1000, "1" * 1001]
        typed = [None, True, False, 0, 1, -1, 9007199254740993, 10**400,
            0.0, -0.0, 0.1, 2.5, 5e-324, float.fromhex("0x1.fffffffffffffp+1023"),
            float("nan"), float("inf"), float("-inf"),
            decimal.Decimal("0.1"), decimal.Decimal("NaN"), decimal.Decimal("Infinity"), [], {}, *strings]
        model = []
        field = models.FloatField(null=True)
        for value in typed:
            entry = {"type": type(value).__name__, "input": rendered(value)}
            for name, operation in [("to_python", field.to_python), ("get_prep_value", field.get_prep_value)]:
                try:
                    entry[name] = {"value": rendered(operation(value)), "codes": []}
                except ValidationError as error:
                    entry[name] = {"codes": [error.code]}
                except (ValueError, TypeError, OverflowError) as error:
                    entry[name] = {"exception": type(error).__name__}
            model.append(entry)

        form = []
        for required in (False, True):
            class FloatForm(forms.Form):
                effort = forms.FloatField(required=required)
            for raw in (None, *strings):
                data = {} if raw is None else {"effort": raw}
                instance = FloatForm(data=data)
                valid = instance.is_valid()
                form.append({"required": required, "input": data, "valid": valid,
                    "cleaned": rendered(instance.cleaned_data),
                    "errors": {key: [error.code for error in errors] for key, errors in instance.errors.as_data().items()},
                    "changed": {name: FloatForm(data=data, initial={"effort": initial}).has_changed()
                        for name, initial in (("null", None), ("zero", 0.0), ("negative_zero", -0.0), ("same", 1.5))},
                    "widget": type(instance.fields["effort"].widget).__name__, "widget_attrs": instance.fields["effort"].widget.attrs})

        class OptionalSerializer(serializers.Serializer):
            effort = serializers.FloatField(allow_null=True, required=False)
        class DefaultSerializer(serializers.Serializer):
            effort = serializers.FloatField(allow_null=True, default=1.5)

        serializer = []
        for name, cls in (("optional", OptionalSerializer), ("default", DefaultSerializer)):
            for partial in (False, True):
                for data in ({}, *[{"effort": value} for value in typed]):
                    instance = cls(data=data, partial=partial)
                    entry = {"serializer": name, "partial": partial,
                        "input": {key: {"type": type(value).__name__, "value": rendered(value)} for key,value in data.items()}}
                    try:
                        valid = instance.is_valid()
                        entry.update(valid=valid, validated=rendered(instance.validated_data),
                            errors={key: [error.code for error in errors] for key, errors in instance.errors.items()})
                        if valid:
                            try:
                                entry["rendered"] = JSONRenderer().render(instance.data).decode()
                            except (TypeError, ValueError, OverflowError) as error:
                                entry["render_exception"] = type(error).__name__
                    except (TypeError, ValueError, OverflowError) as error:
                        entry["exception"] = type(error).__name__
                    serializer.append(entry)

        json_numbers = []
        for raw in ("0", "-0", "-0.0", "0.1", "1e2", "9007199254740993", "9007199254740993.0", "5e-324",
            "2.4703282292062327e-324", "2.4703282292062328e-324", "1.7976931348623157e308", "1e309", "-1e309", "1e-9999", "-1e-9999", "1" * 400):
            instance = OptionalSerializer(data=json.loads('{"effort":' + raw + '}'))
            valid = instance.is_valid()
            entry = {"raw": raw, "valid": valid, "validated": rendered(instance.validated_data),
                "errors": {key: [error.code for error in errors] for key, errors in instance.errors.items()}}
            if valid:
                try:
                    entry["rendered"] = JSONRenderer().render(instance.data).decode()
                except (ValueError, TypeError, OverflowError) as error:
                    entry["render_exception"] = type(error).__name__
            json_numbers.append(entry)

        class Original(models.Model):
            label = models.TextField()
            class Meta:
                app_label = "floatref"
                db_table = "floatref_record"
        class Current(models.Model):
            label = models.TextField()
            effort = models.FloatField(null=True)
            class Meta:
                app_label = "floatref"
                db_table = "floatref_record"
        class Link(models.Model):
            label = models.TextField()
            record = models.ForeignKey(Current, null=True, on_delete=models.SET_NULL, related_name="links")
            class Meta:
                app_label = "floatref"
                db_table = "floatref_link"

        def rows():
            return [[label, rendered(value)] for label, value in Current.objects.order_by("id").values_list("label", "effort")]
        with connection.cursor() as cursor:
            cursor.execute("SELECT sqlite_version(), sqlite_source_id()")
            sqlite_version, sqlite_source_id = cursor.fetchone()
        with connection.schema_editor() as editor:
            editor.create_model(Original)
        Original.objects.create(label="existing")
        with connection.schema_editor() as editor:
            editor.add_field(Original, Current._meta.get_field("effort"))
            editor.create_model(Link)
        after_add = rows()
        for label, value in (("minimum", -float.fromhex("0x1.fffffffffffffp+1023")), ("negative_zero", -0.0),
            ("zero", 0.0), ("subnormal", 5e-324), ("fraction", 1.5), ("maximum", float.fromhex("0x1.fffffffffffffp+1023")), ("null", None)):
            Current.objects.create(label=label, effort=value)
        connection.close()
        reopened = rows()
        def predicates(prefix):
            return {"exact": models.Q(**{prefix:1.5}), "not_exact": ~models.Q(**{prefix:1.5}),
                "null": models.Q(**{prefix+"__isnull":True}), "gt": models.Q(**{prefix+"__gt":1.5}),
                "gte": models.Q(**{prefix+"__gte":1.5}), "lt": models.Q(**{prefix+"__lt":1.5}),
                "lte": models.Q(**{prefix+"__lte":1.5}), "in_null": models.Q(**{prefix+"__in":[1.5,None]}),
                "not_in_null": ~models.Q(**{prefix+"__in":[1.5,None]})}
        queries = {name:list(Current.objects.filter(predicate).order_by("id").values_list("label",flat=True)) for name,predicate in predicates("effort").items()}
        aggregates = rendered(Current.objects.aggregate(minimum=models.Min("effort"), maximum=models.Max("effort")))
        for record in Current.objects.order_by("id"):
            Link.objects.create(label=record.label, record=record)
        Link.objects.create(label="fraction_again", record=Current.objects.get(label="fraction"))
        Link.objects.create(label="missing", record=None)
        relations = {name:list(Link.objects.filter(predicate).order_by("id").values_list("label",flat=True)) for name,predicate in predicates("record__effort").items()}
        Current.objects.filter(label="fraction").update(effort=None)
        Current.objects.filter(label="null").update(effort=0.1)
        after_update = rows()
        try:
            with transaction.atomic():
                Current.objects.filter(label="minimum").update(effort=2.5)
                raise RuntimeError("independent rollback probe")
        except RuntimeError:
            pass
        after_rollback = rows()
        storage_special = []
        for label,value in (("nan",float("nan")),("positive_infinity",float("inf")),("negative_infinity",float("-inf")),("negative_zero",-0.0)):
            record = Current.objects.create(label="special_"+label,effort=value)
            record.refresh_from_db()
            with connection.cursor() as cursor:
                cursor.execute("SELECT typeof(effort) FROM floatref_record WHERE id = %s",[record.pk])
                storage_type = cursor.fetchone()[0]
            storage_special.append({"label":label,"input":rendered(value),"stored":rendered(record.effort),"sqlite_type":storage_type})
        with connection.schema_editor() as editor:
            editor.delete_model(Link)
            editor.remove_field(Current,Current._meta.get_field("effort"))
        after_remove = list(Original.objects.order_by("id").values_list("label",flat=True))
        connection.close()
        return {"django":django.get_version(),"drf":rest_framework.VERSION,"python":platform.python_version(),
            "timezone":"UTC","language":"en-us","sqlite":{"version":sqlite_version,"source_id":sqlite_source_id},
            "model":model,"form":form,"serializer":serializer,"json_numbers":json_numbers,
            "database":{"after_add":after_add,"reopened":reopened,"queries":queries,"relations":relations,"aggregates":aggregates,
                "after_update":after_update,"after_rollback":after_rollback,"after_remove":after_remove,"storage_special":storage_special}}

if __name__ == "__main__":
    print(json.dumps(observe(),sort_keys=True,separators=(",",":"),allow_nan=False))
