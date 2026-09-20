"""Independent pinned Decimal model/Form/DRF and SQLite observations.

No GoDj source or expected observations are imported. Django and DRF public
APIs are BSD-3-Clause references. Decimal spelling and tuples preserve scale
and signed zero; native float storage is recorded separately with binary bits.
"""
import decimal
import json
import platform
import struct
import tempfile
from pathlib import Path

import django
import rest_framework
from django.conf import settings


def rendered(value):
    if isinstance(value, decimal.Decimal):
        sign, digits, exponent = value.as_tuple()
        return {"text": str(value), "sign": sign, "digits": list(digits), "exponent": exponent}
    if isinstance(value, float):
        return {"bits": struct.pack(">d", value).hex(), "repr": repr(value)}
    if isinstance(value, (list, tuple)):
        return [rendered(item) for item in value]
    if isinstance(value, dict):
        return {key: rendered(item) for key, item in value.items()}
    return value


def observe():
    if django.get_version() != "6.1" or rest_framework.VERSION != "3.18.0":
        raise RuntimeError("decimal reference requires pinned Django and DRF")
    with tempfile.TemporaryDirectory(prefix="godj-decimal-reference-") as directory:
        settings.configure(SECRET_KEY="independent-reference-only", INSTALLED_APPS=[],
            DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}},
            USE_TZ=True, TIME_ZONE="UTC", LANGUAGE_CODE="en-us")
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError
        from django.db import connection, models, transaction
        from rest_framework import serializers
        from rest_framework.renderers import JSONRenderer

        def outcome(operation, value):
            try:
                return {"value": rendered(operation(value)), "codes": []}
            except ValidationError as error:
                return {"codes": [item.code for item in error.error_list]}
            except (decimal.DecimalException, TypeError, ValueError, OverflowError) as error:
                return {"exception": type(error).__name__}

        strings = ["", " ", "0", "-0", "+0.00", "-0.00", "0.000", "-0.000", "0E+20", "0E-20",
            "0.1", "1.50", "+1.50", "-2.50", "1.", ".5", "-.5", "01.20", "0001.20", "1.2300",
            "1e2", "1e-2", "1E+2", "1e-3", "1.2345e2", "9999999999.99", "-9999999999.99", "10000000000", "1000000000000",
            " 1.50 ", "\t1.50\n", "\u00a01.50\u00a0", "\x1c1.50\x1f", "1 2", "1_000.25", "1__0", "_1", "1_", "1_.0", "1._0", "1e_2", "1e+_2",
            "١٢.٥٠", "１２.５０", "𝟙.𝟝", "١_٢.٥", "1,5", "0x1p0", "0b10", "1\x00", "1e", "--1", ".",
            "NaN", "-NaN", "sNaN", "NaN123", "Infinity", "-Infinity", "inf", "1e999999", "1e-999999",
            "9007199254740993", "123456789012345678.123456789012", "0." + "0" * 1000 + "1", "1" * 1000, "1" * 1001]
        typed = [None, True, False, 0, 1, -1, 9007199254740993, 10**100,
            0.0, -0.0, 0.1, 1.235, float("nan"), float("inf"), float("-inf"),
            decimal.Decimal("0.10"), decimal.Decimal("-0.00"), decimal.Decimal("1.2300"), decimal.Decimal("NaN"), decimal.Decimal("Infinity"), [], {}, *strings]
        field = models.DecimalField(max_digits=12, decimal_places=2, null=True, blank=True)
        model = [{"type": type(value).__name__, "input": rendered(value),
            "to_python": outcome(field.to_python, value), "get_prep_value": outcome(field.get_prep_value, value),
            "clean": outcome(lambda item: field.clean(item, None), value)} for value in typed]

        form = []
        def changed(instance):
            try:
                return {"value": instance.has_changed()}
            except decimal.DecimalException as error:
                return {"exception": type(error).__name__}

        for required in (False, True):
            class DecimalForm(forms.Form):
                cost = forms.DecimalField(max_digits=12, decimal_places=2, required=required)
            for raw in (None, *strings):
                data = {} if raw is None else {"cost": raw}
                instance = DecimalForm(data=data)
                valid = instance.is_valid()
                form.append({"required": required, "input": data, "valid": valid,
                    "cleaned": rendered(instance.cleaned_data),
                    "errors": {key: [error.code for error in errors] for key, errors in instance.errors.as_data().items()},
                    "changed": {name: changed(DecimalForm(data=data, initial={"cost": initial}))
                        for name, initial in (("null", None), ("zero", decimal.Decimal(0)), ("negative_zero", decimal.Decimal("-0.00")), ("same", decimal.Decimal("1.5")))},
                    "widget": type(instance.fields["cost"].widget).__name__, "widget_attrs": instance.fields["cost"].widget.attrs})

        class OptionalSerializer(serializers.Serializer):
            cost = serializers.DecimalField(max_digits=12, decimal_places=2, allow_null=True, required=False)
        class DefaultSerializer(serializers.Serializer):
            cost = serializers.DecimalField(max_digits=12, decimal_places=2, allow_null=True, default=decimal.Decimal("1.50"))

        def serializer_result(instance):
            valid = instance.is_valid()
            result = {"valid": valid, "validated": rendered(instance.validated_data),
                "errors": {key: [error.code for error in errors] for key, errors in instance.errors.items()}}
            if valid:
                result["rendered"] = JSONRenderer().render(instance.data).decode()
            return result

        serializer = []
        for name, cls in (("optional", OptionalSerializer), ("default", DefaultSerializer)):
            for partial in (False, True):
                for data in ({}, *[{"cost": value} for value in typed]):
                    entry = {"serializer": name, "partial": partial,
                        "input": {key: {"type": type(value).__name__, "value": rendered(value)} for key, value in data.items()}}
                    entry.update(serializer_result(cls(data=data, partial=partial)))
                    serializer.append(entry)

        json_numbers = []
        for raw in ("0", "-0", "-0.0", "0.1", "1.20", "1.200", "1e2", "1e-2", "1e-3", "9999999999.99", "9999999999.999",
            "9007199254740993", "9007199254740993.0", "5e-324", "1e309", "-1e309", "1e-9999", "-1e-9999", "1" * 100):
            entry = {"raw": raw}
            entry.update(serializer_result(OptionalSerializer(data=json.loads('{"cost":' + raw + '}'))))
            json_numbers.append(entry)

        precision = []
        for digits, places in ((5, 2), (4, 4), (4, 0), (30, 12), (None, None)):
            model_field = models.DecimalField(max_digits=digits, decimal_places=places, null=True, blank=True)
            form_field = forms.DecimalField(max_digits=digits, decimal_places=places, required=False)
            api_field = serializers.DecimalField(max_digits=digits, decimal_places=places, allow_null=True, required=False)
            for value in ("0", "-0.00", "0.00000", "0E+20", "0E-20", "1", "1.0", "1.230", "1.234", "1234", "123.45", "999.99", "1000", "0.0001", "0.00001", "1E+4", "1E-4", "123456789012345678.123456789012"):
                entry = {"max_digits": digits, "decimal_places": places, "input": value,
                    "model": outcome(lambda item: model_field.clean(item, None), value), "form": outcome(form_field.clean, value)}
                try:
                    cleaned = api_field.run_validation(value)
                    entry["serializer"] = {"value": rendered(cleaned), "output": rendered(api_field.to_representation(cleaned)), "codes": []}
                except serializers.ValidationError as error:
                    entry["serializer"] = {"codes": [item.code for item in error.detail]}
                precision.append(entry)

        class Original(models.Model):
            label = models.TextField()
            class Meta:
                app_label = "decimalref"
                db_table = "decimalref_record"
        class Current(models.Model):
            label = models.TextField()
            cost = models.DecimalField(max_digits=12, decimal_places=2, null=True)
            class Meta:
                app_label = "decimalref"
                db_table = "decimalref_record"
        class Link(models.Model):
            label = models.TextField()
            record = models.ForeignKey(Current, null=True, on_delete=models.SET_NULL, related_name="links")
            class Meta:
                app_label = "decimalref"
                db_table = "decimalref_link"
        class Precise(models.Model):
            cost = models.DecimalField(max_digits=30, decimal_places=12, null=True)
            class Meta:
                app_label = "decimalref"
                db_table = "decimalref_precise"

        def rows():
            return [[label, rendered(value)] for label, value in Current.objects.order_by("id").values_list("label", "cost")]
        with connection.cursor() as cursor:
            cursor.execute("SELECT sqlite_version(), sqlite_source_id()")
            sqlite_version, sqlite_source_id = cursor.fetchone()
        with connection.schema_editor() as editor:
            editor.create_model(Original)
        Original.objects.create(label="existing")
        with connection.schema_editor() as editor:
            editor.add_field(Original, Current._meta.get_field("cost"))
            editor.create_model(Link)
            editor.create_model(Precise)
        after_add = rows()
        for label, value in (("minimum", "-9999999999.99"), ("negative_zero", "-0.00"), ("zero", "0.00"), ("small", "0.01"), ("fraction", "1.50"), ("maximum", "9999999999.99"), ("null", None)):
            Current.objects.create(label=label, cost=None if value is None else decimal.Decimal(value))
        connection.close()
        reopened = rows()
        def predicates(prefix):
            value = decimal.Decimal("1.50")
            return {"exact": models.Q(**{prefix: value}), "not_exact": ~models.Q(**{prefix: value}), "null": models.Q(**{prefix + "__isnull": True}),
                "gt": models.Q(**{prefix + "__gt": value}), "gte": models.Q(**{prefix + "__gte": value}),
                "lt": models.Q(**{prefix + "__lt": value}), "lte": models.Q(**{prefix + "__lte": value}),
                "in_null": models.Q(**{prefix + "__in": [value, None]}), "not_in_null": ~models.Q(**{prefix + "__in": [value, None]})}
        queries = {name: list(Current.objects.filter(predicate).order_by("id").values_list("label", flat=True)) for name, predicate in predicates("cost").items()}
        aggregates = rendered(Current.objects.aggregate(minimum=models.Min("cost"), maximum=models.Max("cost")))
        for record in Current.objects.order_by("id"):
            Link.objects.create(label=record.label, record=record)
        Link.objects.create(label="fraction_again", record=Current.objects.get(label="fraction"))
        Link.objects.create(label="missing", record=None)
        relations = {name: list(Link.objects.filter(predicate).order_by("id").values_list("label", flat=True)) for name, predicate in predicates("record__cost").items()}
        Current.objects.filter(label="fraction").update(cost=None)
        Current.objects.filter(label="null").update(cost=decimal.Decimal("0.10"))
        after_update = rows()
        try:
            with transaction.atomic():
                Current.objects.filter(label="minimum").update(cost=decimal.Decimal("2.50"))
                raise RuntimeError("independent rollback probe")
        except RuntimeError:
            pass
        after_rollback = rows()

        storage = []
        for label, text in (("fraction", "0.10"), ("trailing_zero", "1.2300"), ("negative_zero", "-0.00"),
            ("half_positive", "1.235"), ("half_negative", "-1.235"), ("half_even_positive", "1.225"), ("half_even_negative", "-1.225"),
            ("precise", "123456789012345678.123456789012"), ("maximum", "999999999999999999.999999999999")):
            model_class = Precise if label in ("precise", "maximum") else Current
            with transaction.atomic():
                entry = {"label": label, "input": text, "model": model_class.__name__}
                kwargs = {"cost": decimal.Decimal(text)}
                if model_class is Current:
                    kwargs["label"] = "probe_" + label
                record = model_class.objects.create(**kwargs)
                table = model_class._meta.db_table
                with connection.cursor() as cursor:
                    cursor.execute('SELECT cost, typeof(cost) FROM "' + table + '" WHERE id = %s', [record.pk])
                    raw, kind = cursor.fetchone()
                entry.update(raw=rendered(raw), sqlite_type=kind)
                try:
                    record.refresh_from_db()
                    entry["read"] = rendered(record.cost)
                except (decimal.DecimalException, ValueError, TypeError) as error:
                    entry["read_exception"] = type(error).__name__
                storage.append(entry)
                transaction.set_rollback(True)
        with connection.schema_editor() as editor:
            editor.delete_model(Link)
            editor.delete_model(Precise)
            editor.remove_field(Current, Current._meta.get_field("cost"))
        after_remove = list(Original.objects.order_by("id").values_list("label", flat=True))
        connection.close()
        return {"django": django.get_version(), "drf": rest_framework.VERSION, "python": platform.python_version(),
            "timezone": "UTC", "language": "en-us", "sqlite": {"version": sqlite_version, "source_id": sqlite_source_id},
            "model": model, "form": form, "serializer": serializer, "json_numbers": json_numbers, "precision": precision,
            "database": {"after_add": after_add, "reopened": reopened, "queries": queries, "relations": relations, "aggregates": aggregates,
                "after_update": after_update, "after_rollback": after_rollback, "after_remove": after_remove, "storage": storage}}


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, separators=(",", ":"), allow_nan=False))
