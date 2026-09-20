"""Independent pinned Django/DRF UUID observations through public APIs.

Django and DRF are BSD-3-Clause references. No GoDj implementation or expected
fixture is imported. UUID values include all 128-bit patterns, not only v4.
"""
import json
import platform
import tempfile
import unicodedata
import uuid
from pathlib import Path

import django
import rest_framework
from django.conf import settings


def rendered(value):
    if isinstance(value, uuid.UUID):
        return {"text": str(value), "hex": value.hex, "int": str(value.int)}
    if isinstance(value, bytes):
        return {"hex": value.hex()}
    if isinstance(value, (list, tuple)):
        return [rendered(item) for item in value]
    if isinstance(value, dict):
        return {key: rendered(item) for key, item in value.items()}
    return value


def observe():
    if django.get_version() != "6.1" or rest_framework.VERSION != "3.18.0":
        raise RuntimeError("UUID reference requires pinned Django and DRF")
    with tempfile.TemporaryDirectory(prefix="godj-uuid-reference-") as directory:
        settings.configure(SECRET_KEY="independent-reference-only", INSTALLED_APPS=[],
            DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}},
            USE_TZ=True, TIME_ZONE="UTC", LANGUAGE_CODE="en-us")
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError
        from django.db import connection, models, transaction
        from rest_framework import serializers
        from rest_framework.renderers import JSONRenderer

        zero = uuid.UUID(int=0)
        sample = uuid.UUID("12345678-9abc-4def-8123-456789abcdef")
        maximum = uuid.UUID(int=(1 << 128) - 1)
        strings = ["", " ", str(zero), zero.hex, str(sample), sample.hex, str(sample).upper(), sample.hex.upper(),
            "{" + str(sample) + "}", "{" + sample.hex + "}", "{{" + sample.hex + "}}", sample.urn,
            "URN:UUID:" + str(sample), "urn:uuid:" + sample.hex.upper(), "uuid:" + str(sample),
            " " + str(sample) + " ", "\t" + str(sample) + "\n", "\u00a0" + sample.hex + "\u00a0",
            "\x1c" + sample.hex + "\x1f", str(sample) + "\x00", "-".join(sample.hex), sample.hex + "-", "-" + sample.hex,
            "0" * 31, "0" * 33, "g" + "0" * 31, "0x" + "0" * 30, "+" + "0" * 30 + "1", "-" + "0" * 31,
            "0" * 30 + "_1", "_" + "0" * 31, "0" * 31 + "_", " " + "0" * 30 + "1", "0" * 31 + " ",
            "０" * 32, "٠" * 31 + "١", "00000000-0000-0000-0000-000000000001", str(maximum),
            "7fffffff-ffff-ffff-ffff-ffffffffffff", "80000000-0000-0000-0000-000000000000", "{not-a-uuid}", "null"]
        typed = [None, True, False, 0, 1, -1, (1 << 64) - 1, 1 << 64, (1 << 128) - 1, 1 << 128,
            0.0, 1.0, 1.5, [], {}, b"", sample.bytes, zero, sample, maximum, *strings]

        def outcome(operation, value):
            try:
                return {"value": rendered(operation(value)), "codes": []}
            except ValidationError as error:
                return {"codes": [item.code for item in error.error_list]}
            except (AttributeError, TypeError, ValueError, OverflowError) as error:
                return {"exception": type(error).__name__}

        field = models.UUIDField(null=True, blank=True)
        model = [{"type": type(value).__name__, "input": rendered(value),
            "to_python": outcome(field.to_python, value), "get_prep_value": outcome(field.get_prep_value, value),
            "clean": outcome(lambda item: field.clean(item, None), value)} for value in typed]
        form = []
        for required in (False, True):
            class ReferenceForm(forms.Form):
                reference = forms.UUIDField(required=required)
            for raw in (None, *strings):
                data = {} if raw is None else {"reference": raw}
                instance = ReferenceForm(data=data)
                valid = instance.is_valid()
                form.append({"required": required, "input": data, "valid": valid,
                    "cleaned": rendered(instance.cleaned_data),
                    "errors": {key: [error.code for error in errors] for key, errors in instance.errors.as_data().items()},
                    "changed": {name: ReferenceForm(data=data, initial={"reference": initial}).has_changed()
                        for name, initial in (("null", None), ("zero", zero), ("same", sample))},
                    "widget": type(instance.fields["reference"].widget).__name__, "widget_attrs": instance.fields["reference"].widget.attrs})

        class OptionalSerializer(serializers.Serializer):
            reference = serializers.UUIDField(allow_null=True, required=False)
        class DefaultSerializer(serializers.Serializer):
            reference = serializers.UUIDField(allow_null=True, default=zero)

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
                serializer.append({"serializer": name, "partial": partial, "input": {},
                    **serializer_result(cls(data={}, partial=partial))})
                for value in typed:
                    serializer.append({"serializer": name, "partial": partial,
                        "input": {"reference": {"type": type(value).__name__, "value": rendered(value)}},
                        **serializer_result(cls(data={"reference": value}, partial=partial))})
        json_numbers = []
        for raw in ("0", "1", "-1", "18446744073709551616", "340282366920938463463374607431768211455",
                    "340282366920938463463374607431768211456", "1.0", "1e0", "-0", "-0.0", "true", "false", "null"):
            json_numbers.append({"raw": raw, **serializer_result(OptionalSerializer(data=json.loads('{"reference":' + raw + '}')))})
        formats = {kind: {"zero": rendered(serializers.UUIDField(format=kind).to_representation(zero)),
                          "sample": rendered(serializers.UUIDField(format=kind).to_representation(sample)),
                          "maximum": rendered(serializers.UUIDField(format=kind).to_representation(maximum))}
                   for kind in ("hex_verbose", "hex", "int", "urn")}

        # Observe the complete decimal-digit repertoire through the public
        # model, Form, and serializer, independently of Go's Unicode tables.
        unicode_ranges = []
        for first in range(0x110000):
            if unicodedata.decimal(chr(first), None) != 0:
                continue
            values = []
            for digit in range(10):
                text = chr(first + digit) * 32
                if unicodedata.decimal(chr(first + digit), None) != digit:
                    raise RuntimeError("noncontiguous Unicode decimal range")
                converted = field.to_python(text)
                if forms.UUIDField().clean(text) != converted or serializers.UUIDField().run_validation(text) != converted:
                    raise RuntimeError("UUID Unicode input differs across public adapters")
                values.append(str(converted))
            unicode_ranges.append({"first": first, "last": first + 9, "values": values})

        class Legacy(models.Model):
            label = models.CharField(max_length=32)
            origin = models.UUIDField(default=zero)
            mirror = models.UUIDField(null=True)
            class Meta:
                app_label = "uuidref"
                db_table = "uuidref_record"
        class Record(models.Model):
            label = models.CharField(max_length=32)
            origin = models.UUIDField(default=zero)
            mirror = models.UUIDField(null=True)
            reference = models.UUIDField(null=True)
            class Meta:
                app_label = "uuidref"
                db_table = "uuidref_record"
        class Link(models.Model):
            label = models.CharField(max_length=32)
            record = models.ForeignKey(Record, null=True, on_delete=models.SET_NULL)
            class Meta:
                app_label = "uuidref"
        with connection.schema_editor() as editor:
            editor.create_model(Legacy)
        Legacy.objects.create(label="existing")
        with connection.schema_editor() as editor:
            editor.add_field(Record, Record._meta.get_field("reference"))
            editor.create_model(Link)
        after_add = rendered(list(Record.objects.order_by("id").values_list("label", "reference", "origin")))
        samples = [("zero", zero), ("one", uuid.UUID(int=1)), ("sample", sample),
                   ("lower_half", uuid.UUID(int=(1 << 127) - 1)), ("upper_half", uuid.UUID(int=1 << 127)),
                   ("maximum", maximum), ("null", None)]
        for label, value in samples:
            row = Record.objects.create(label=label, reference=value, mirror=value)
            Link.objects.create(label=label, record=row)
        Link.objects.create(label="missing")
        def labels(query):
            return list(query.order_by("id").values_list("label", flat=True))
        queries, relations = {}, {}
        for name, lookup, value in (("exact", "exact", sample), ("gt", "gt", sample), ("gte", "gte", sample),
                ("lt", "lt", sample), ("lte", "lte", sample), ("in_null", "in", [sample, None]), ("null", "isnull", True)):
            queries[name] = labels(Record.objects.filter(**{"reference__" + lookup: value}))
            relations[name] = labels(Link.objects.filter(**{"record__reference__" + lookup: value}))
        queries["not_exact"] = labels(Record.objects.exclude(reference=sample))
        queries["field_equal"] = labels(Record.objects.filter(reference=models.F("mirror")))
        queries["order"] = list(Record.objects.order_by("reference", "id").values_list("label", flat=True))
        aggregates = rendered(Record.objects.aggregate(minimum=models.Min("reference"), maximum=models.Max("reference")))
        with connection.cursor() as cursor:
            cursor.execute("SELECT label, reference, typeof(reference), origin FROM uuidref_record ORDER BY id")
            physical = cursor.fetchall()
            cursor.execute("SELECT sqlite_version(), sqlite_source_id()")
            sqlite_version, source_id = cursor.fetchone()
        before_rollback = rendered(list(Record.objects.order_by("id").values_list("label", "reference")))
        with transaction.atomic():
            Record.objects.filter(label="sample").update(reference=zero)
            transaction.set_rollback(True)
        after_rollback = rendered(list(Record.objects.order_by("id").values_list("label", "reference")))
        connection.close()
        reopened = rendered(list(Record.objects.order_by("id").values_list("label", "reference")))
        with connection.schema_editor() as editor:
            editor.remove_field(Record, Record._meta.get_field("reference"))
        after_remove = list(Legacy.objects.order_by("id").values_list("label", flat=True))
        return {"django": django.get_version(), "drf": rest_framework.VERSION, "python": platform.python_version(),
            "sqlite": {"version": sqlite_version, "source_id": source_id}, "model": model, "form": form,
            "serializer": serializer, "json_numbers": json_numbers, "formats": formats,
            "unicode_decimal": {"version": unicodedata.unidata_version, "ranges": unicode_ranges},
            "database": {"type": field.db_type(connection), "after_add": after_add, "queries": queries, "relations": relations,
                "aggregates": aggregates, "physical": physical, "before_rollback": before_rollback, "after_rollback": after_rollback,
                "reopened": reopened, "after_remove": after_remove}}


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=True, sort_keys=True, separators=(",", ":")))
