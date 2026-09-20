"""Independent Django/DRF JSONField observations through public APIs.

Django and DRF are BSD-3-Clause references. No GoDj implementation or expected
fixture is imported. Storage, validation, and rendering are separate results.
"""
import datetime
import decimal
import io
import json
import math
import platform
import tempfile
import warnings
from pathlib import Path

import django
import rest_framework
from django.conf import settings


def rendered(value):
    if value is None:
        return {"kind": "null"}
    if isinstance(value, bool):
        return {"kind": "bool", "value": value}
    if isinstance(value, int):
        return {"kind": "integer", "value": str(value)}
    if isinstance(value, float):
        return {"kind": "float", "value": repr(value)}
    if isinstance(value, str):
        return {"kind": "string", "value": value}
    if isinstance(value, bytes):
        return {"kind": "bytes", "value": value.hex()}
    if isinstance(value, (list, tuple)):
        return {"kind": type(value).__name__, "value": [rendered(item) for item in value]}
    if isinstance(value, dict):
        return {"kind": "object", "value": [[rendered(key), rendered(item)] for key, item in value.items()]}
    return {"kind": type(value).__name__, "value": repr(value)}


def observe():
    if django.get_version() != "6.1" or rest_framework.VERSION != "3.18.0":
        raise RuntimeError("JSONField reference requires pinned Django and DRF")
    with tempfile.TemporaryDirectory(prefix="godj-json-reference-") as directory:
        settings.configure(SECRET_KEY="independent-reference-only", INSTALLED_APPS=[],
            DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}},
            USE_TZ=True, TIME_ZONE="UTC", LANGUAGE_CODE="en-us")
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError
        from django.db import connection, models, transaction
        from django.db.utils import IntegrityError, NotSupportedError
        from rest_framework import serializers
        from rest_framework.exceptions import ParseError
        from rest_framework.parsers import JSONParser
        from rest_framework.renderers import JSONRenderer

        values = [("null", None), ("false", False), ("true", True), ("zero", 0), ("one", 1), ("negative", -1),
            ("large_integer", (1 << 128) - 1), ("float_one", 1.0), ("float_negative_zero", -0.0),
            ("nan", math.nan), ("infinity", math.inf), ("negative_infinity", -math.inf),
            ("empty_object", {}), ("empty_array", []), ("empty_string", ""), ("string", "null"),
            ("json_string", '{"a":1}'), ("unicode", "한글😀"), ("nul", "\0"), ("surrogate", "\ud800"),
            ("object", {"a": 1, "b": [True, None, "text"]}), ("reordered", {"b": 2, "a": 1}),
            ("integer_key", {1: "integer key"}), ("tuple", (1, False)), ("bytes", b"{}"),
            ("decimal", decimal.Decimal("0.1")), ("datetime", datetime.datetime(2026, 9, 20, tzinfo=datetime.UTC))]

        def outcome(operation, value):
            try:
                return {"value": rendered(operation(value)), "codes": []}
            except ValidationError as error:
                return {"codes": [item.code for item in error.error_list]}
            except (AttributeError, TypeError, ValueError, OverflowError, UnicodeError) as error:
                return {"exception": type(error).__name__}

        model = []
        for nullable, blank in ((False, False), (True, True)):
            field = models.JSONField(null=nullable, blank=blank)
            for label, value in values:
                model.append({"label": label, "nullable": nullable, "blank": blank, "input": rendered(value),
                    "to_python": outcome(field.to_python, value), "clean": outcome(lambda item: field.clean(item, None), value),
                    "get_prep_value": outcome(field.get_prep_value, value)})

        texts = [None, "", " ", "null", "{}", "[]", "false", "true", "0", "-0", "1", "1.0", "1e0",
            "340282366920938463463374607431768211455", "9007199254740993.0", '""', '"null"', '"한글😀"',
            '{"a":1,"b":2}', '{"b":2,"a":1}', '{"a":1,"a":2}', '{"__proto__":{"x":1}}',
            "NaN", "Infinity", "-Infinity", '{"v":1e400}', '"\\u0000"', '"\\ud800"',
            '"\\ud83d\\ude00"', "{bad}", "[1,]", "01", "{} []"]
        form = []
        initials = {"null": None, "one": 1, "float_one": 1.0, "true": True, "object": {"a": 1, "b": 2}}
        for required in (False, True):
            field = forms.JSONField(required=required)
            for raw in texts:
                cleaned = outcome(field.clean, raw)
                form.append({"required": required, "input": raw, "cleaned": cleaned,
                    "changed": {name: outcome(lambda item: field.has_changed(initial, item), raw) for name, initial in initials.items()},
                    "bound": outcome(lambda item: field.prepare_value(field.bound_data(item, None)), raw),
                    "widget": type(field.widget).__name__})

        class OptionalSerializer(serializers.Serializer):
            payload = serializers.JSONField(allow_null=True, required=False)

        class DefaultSerializer(serializers.Serializer):
            payload = serializers.JSONField(allow_null=True, default=dict)

        def serializer_result(instance):
            valid = instance.is_valid()
            result = {"valid": valid, "validated": rendered(instance.validated_data),
                "errors": {key: [error.code for error in errors] for key, errors in instance.errors.items()}}
            if valid:
                try:
                    result["rendered"] = JSONRenderer().render(instance.data).decode()
                except (TypeError, ValueError, OverflowError, UnicodeError) as error:
                    result["render_exception"] = type(error).__name__
            return result

        serializer = []
        for name, cls in (("optional", OptionalSerializer), ("default", DefaultSerializer)):
            for partial in (False, True):
                serializer.append({"serializer": name, "partial": partial, "label": "omitted",
                    **serializer_result(cls(data={}, partial=partial))})
                for label, value in values:
                    serializer.append({"serializer": name, "partial": partial, "label": label,
                        **serializer_result(cls(data={"payload": value}, partial=partial))})
        parsed = []
        for text in texts[1:]:
            try:
                data = JSONParser().parse(io.BytesIO(('{"payload":' + text + '}').encode()))
                result = serializer_result(OptionalSerializer(data=data))
            except ParseError as error:
                result = {"parse_exception": type(error).__name__}
            parsed.append({"input": text, **result})

        class Legacy(models.Model):
            label = models.CharField(max_length=40)

            class Meta:
                app_label = "jsonref"
                db_table = "jsonref_record"

        class Record(models.Model):
            label = models.CharField(max_length=40)
            payload = models.JSONField(null=True, blank=True)

            class Meta:
                app_label = "jsonref"
                db_table = "jsonref_record"

        with connection.schema_editor() as editor:
            editor.create_model(Legacy)
        Legacy.objects.create(label="existing")
        field = Record._meta.get_field("payload")
        with connection.schema_editor() as editor:
            editor.add_field(Record, field)
        after_add = rendered(list(Record.objects.values_list("label", "payload")))
        for label, value in [("sql_null", None), ("json_null", models.JSONNull()), ("false", False), ("zero", 0),
                ("one", 1), ("float", 1.0), ("object", {"a": 1, "b": 2}), ("reordered", {"b": 2, "a": 1}),
                ("array", [1, True, None]), ("string", "null"), ("bigint", (1 << 128) - 1)]:
            Record.objects.create(label=label, payload=value)
        queries = {}
        for name, predicate in [("sql_null", {"payload__isnull": True}), ("not_sql_null", {"payload__isnull": False}),
                ("json_null", {"payload": models.JSONNull()}), ("false", {"payload": False}), ("zero", {"payload": 0}),
                ("one", {"payload": 1}), ("float", {"payload": 1.0}), ("object", {"payload": {"a": 1, "b": 2}}),
                ("contains", {"payload__contains": {"a": 1}}), ("key_a", {"payload__a": 1})]:
            try:
                queries[name] = list(Record.objects.filter(**predicate).order_by("id").values_list("label", flat=True))
            except NotSupportedError as error:
                queries[name] = {"exception": type(error).__name__}
        # Capture the public deprecation explicitly; do not silence warnings
        # around the remaining reference or turn them into validation errors.
        with warnings.catch_warnings(record=True) as observed_warnings:
            warnings.simplefilter("always")
            legacy_none = list(Record.objects.filter(payload=None).order_by("id").values_list("label", flat=True))
        deprecated = {"rows": legacy_none, "warnings": [type(item.message).__name__ for item in observed_warnings]}
        rejected_writes = []
        for label, value in (("nan", math.nan), ("infinity", math.inf), ("surrogate", "\ud800"), ("bytes", b"{}")):
            before = Record.objects.count()
            try:
                with transaction.atomic():
                    Record.objects.create(label="rejected_" + label, payload=value)
                    transaction.set_rollback(True)
                result = {"accepted": True}
            except (IntegrityError, TypeError, ValueError, OverflowError) as error:
                result = {"exception": type(error).__name__}
            rejected_writes.append({"label": label, **result, "rows_preserved": Record.objects.count() == before})
        external = []
        for name, text in (("malformed", "not-json"), ("duplicate", '{"a":1,"a":2}'), ("nul", '"\\u0000"')):
            before = Record.objects.count()
            try:
                with transaction.atomic():
                    with connection.cursor() as cursor:
                        cursor.execute("INSERT INTO jsonref_record (label,payload) VALUES (%s,%s)", ["external_" + name, text])
                    record = Record.objects.get(label="external_" + name)
                    result = {"value": rendered(record.payload),
                        "key_matches_one": Record.objects.filter(pk=record.pk, payload__a=1).exists(),
                        "key_matches_two": Record.objects.filter(pk=record.pk, payload__a=2).exists()}
                    transaction.set_rollback(True)
            except IntegrityError as error:
                result = {"exception": type(error).__name__}
            external.append({"label": name, **result, "rows_preserved": Record.objects.count() == before})
        with connection.cursor() as cursor:
            cursor.execute("SELECT label,payload,typeof(payload),json_type(payload) FROM jsonref_record ORDER BY id")
            physical = cursor.fetchall()
            cursor.execute("SELECT sql FROM sqlite_master WHERE type='table' AND name='jsonref_record'")
            table_sql = cursor.fetchone()[0]
            cursor.execute("SELECT sqlite_version(),sqlite_source_id()")
            sqlite_version, source_id = cursor.fetchone()
        before_rollback = rendered(list(Record.objects.order_by("id").values_list("label", "payload")))
        with transaction.atomic():
            Record.objects.filter(label="object").update(payload={"rollback": True})
            transaction.set_rollback(True)
        after_rollback = rendered(list(Record.objects.order_by("id").values_list("label", "payload")))
        connection.close()
        reopened = rendered(list(Record.objects.order_by("id").values_list("label", "payload")))
        with connection.schema_editor() as editor:
            editor.remove_field(Record, field)
        after_remove = list(Legacy.objects.order_by("id").values_list("label", flat=True))
        return {"django": django.get_version(), "drf": rest_framework.VERSION, "python": platform.python_version(),
            "sqlite": {"version": sqlite_version, "source_id": source_id}, "model": model, "form": form,
            "serializer": serializer, "parsed": parsed,
            "database": {"type": field.db_type(connection), "after_add": after_add, "queries": queries, "deprecated_none": deprecated,
                "rejected_writes": rejected_writes, "external": external,
                "physical": physical, "table_sql": table_sql, "before_rollback": before_rollback, "after_rollback": after_rollback,
                "reopened": reopened, "after_remove": after_remove}}


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=True, sort_keys=True, separators=(",", ":"), allow_nan=False))
