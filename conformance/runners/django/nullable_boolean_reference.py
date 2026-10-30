"""Independent observations of pinned Django/DRF nullable Boolean behavior.

Django 6.1 and DRF 3.18.0 are BSD-3-Clause references. This runner exercises
their public model, widget, form, serializer and schema-editor APIs; it does
not read GoDj code or expected artifacts.
"""

import json
import platform
import tempfile
from pathlib import Path

import django
import rest_framework
from django.conf import settings


def error_codes(errors):
    return {name: [error.code for error in values] for name, values in errors.items()}


def observe():
    if django.get_version() != "6.1" or rest_framework.VERSION != "3.18.0":
        raise RuntimeError("nullable Boolean reference requires pinned Django/DRF")
    with tempfile.TemporaryDirectory(prefix="godj-nullable-boolean-reference-") as directory:
        settings.configure(
            SECRET_KEY="independent-reference-only",
            INSTALLED_APPS=[],
            DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}},
            USE_TZ=True,
        )
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError
        from django.db import connection, models
        from rest_framework import serializers

        class NullableForm(forms.Form):
            flag = forms.NullBooleanField(required=False)

        class RequiredNullableForm(forms.Form):
            flag = forms.NullBooleanField(required=True)

        class OptionalSerializer(serializers.Serializer):
            flag = serializers.BooleanField(allow_null=True, required=False)

        class DefaultSerializer(serializers.Serializer):
            flag = serializers.BooleanField(allow_null=True, default=False)

        form_inputs = [None, "", "unknown", "true", "false", "True", "False", "1", "0", "2", "3", "yes", "off", "invalid", " true "]
        form_observations = []
        for required, form_type, value in [(required, form_type, value)
                                           for required, form_type in [(False, NullableForm), (True, RequiredNullableForm)]
                                           for value in form_inputs]:
            data = {} if value is None else {"flag": value}
            form = form_type(data=data)
            valid = form.is_valid()
            form_observations.append({
                "required": required, "input": data,
                "widget_value": form.fields["flag"].widget.value_from_datadict(data, {}, "flag"),
                "valid": valid,
                "cleaned": form.cleaned_data,
                "errors": error_codes(form.errors.as_data()),
                "changed": {name: form_type(data=data, initial={"flag": initial}).has_changed()
                            for name, initial in [("null", None), ("false", False), ("true", True)]},
            })

        direct_form = []
        model_field = []
        scalar_inputs = [None, True, False, 0, 1, 2, "", "true", "false", "True", "False", "1", "0", "2", "invalid"]
        field = models.BooleanField(null=True)
        for value in scalar_inputs:
            direct_form.append({"input": value, "value": forms.NullBooleanField().clean(value)})
            try:
                model_field.append({"input": value, "value": field.to_python(value), "error": None})
            except ValidationError as error:
                model_field.append({"input": value, "error": error.code})

        serializer_observations = []
        for name, serializer in [("optional", OptionalSerializer), ("default_false", DefaultSerializer)]:
            for partial in [False, True]:
                for data in [{}, *[{"flag": value} for value in scalar_inputs]]:
                    instance = serializer(data=data, partial=partial)
                    valid = instance.is_valid()
                    serializer_observations.append({
                        "serializer": name, "partial": partial, "input": data,
                        "valid": valid, "validated": dict(instance.validated_data),
                        "errors": {key: [error.code for error in values] for key, values in instance.errors.items()},
                    })

        class Original(models.Model):
            label = models.TextField()

            class Meta:
                app_label = "nullablebool"
                db_table = "nullablebool_record"

        class Current(models.Model):
            label = models.TextField()
            flag = models.BooleanField(null=True)

            class Meta:
                app_label = "nullablebool"
                db_table = "nullablebool_record"

        with connection.cursor() as cursor:
            cursor.execute("SELECT sqlite_version(), sqlite_source_id()")
            sqlite_version, sqlite_source_id = cursor.fetchone()
        with connection.schema_editor() as editor:
            editor.create_model(Original)
        Original.objects.create(label="existing")
        with connection.schema_editor() as editor:
            editor.add_field(Original, Current._meta.get_field("flag"))
        after_add = list(Current.objects.order_by("id").values_list("label", "flag"))
        Current.objects.create(label="false", flag=False)
        Current.objects.create(label="true", flag=True)
        Current.objects.create(label="null", flag=None)
        connection.close()
        reopened = list(Current.objects.order_by("id").values_list("label", "flag"))
        predicates = {
            "false": Current.objects.filter(flag=False),
            "true": Current.objects.filter(flag=True),
            "null": Current.objects.filter(flag__isnull=True),
            "not_false": Current.objects.exclude(flag=False),
            "in_false_null": Current.objects.filter(flag__in=[False, None]),
            "not_in_false_null": Current.objects.exclude(flag__in=[False, None]),
        }
        queries = {name: list(query.order_by("id").values_list("label", flat=True)) for name, query in predicates.items()}

        class Link(models.Model):
            label = models.TextField()
            record = models.ForeignKey(Current, null=True, on_delete=models.SET_NULL)

            class Meta:
                app_label = "nullablebool"
                db_table = "nullablebool_link"

        with connection.schema_editor() as editor:
            editor.create_model(Link)
        for record in Current.objects.order_by("id"):
            Link.objects.create(label=record.label, record=record)
        Link.objects.create(label="missing", record=None)
        related_predicates = {
            "false": Link.objects.filter(record__flag=False),
            "true": Link.objects.filter(record__flag=True),
            "null": Link.objects.filter(record__flag__isnull=True),
            "not_false": Link.objects.exclude(record__flag=False),
            "in_false_null": Link.objects.filter(record__flag__in=[False, None]),
            "not_in_false_null": Link.objects.exclude(record__flag__in=[False, None]),
        }
        relations = {name: list(query.order_by("id").values_list("label", flat=True)) for name, query in related_predicates.items()}
        with connection.schema_editor() as editor:
            editor.delete_model(Link)
        Current.objects.filter(label="false").update(flag=None)
        Current.objects.filter(label="null").update(flag=False)
        after_update = list(Current.objects.order_by("id").values_list("label", "flag"))
        with connection.schema_editor() as editor:
            editor.remove_field(Current, Current._meta.get_field("flag"))
        after_remove = list(Original.objects.order_by("id").values_list("label", flat=True))
        connection.close()
        return {
            "django": django.get_version(), "drf": rest_framework.VERSION,
            "python": platform.python_version(),
            "sqlite": {"version": sqlite_version, "source_id": sqlite_source_id},
            "form": form_observations, "direct_form": direct_form, "model_field": model_field,
            "serializer": serializer_observations,
            "database": {"after_add": after_add, "reopened": reopened, "queries": queries, "relations": relations,
                         "after_update": after_update, "after_remove": after_remove},
        }


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, separators=(",", ":"), allow_nan=False))
