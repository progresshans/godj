"""Independent pinned Django/DRF URL input and storage observations.

Authority: Django 6.1 model/form fields and DRF 3.18 serializer fields, both
BSD-3-Clause (repository-root LICENSE.django and docs/SOURCES.md).
Only the synthetic input corpus is shared with Go. No Go output
or expected fixture is read while observing behavior.
"""
import hashlib
import inspect
import json
import os
from pathlib import Path
import platform
import re
import tempfile

import django
from django.conf import settings

ROOT = Path(__file__).resolve().parents[3]
INPUTS = ROOT / "internal/urltest/testdata/url-inputs.json"


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    inputs = json.loads(INPUTS.read_text())
    database_name = os.environ.get("GODJ_URL_REFERENCE_DATABASE")
    if database_name:
        assert re.fullmatch(r"godj_url_reference_[0-9]+", database_name)
    with tempfile.TemporaryDirectory(prefix="godj-url-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if database_name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": database_name,
                        "HOST": os.environ.get("PGHOST", "localhost"), "PORT": os.environ.get("PGPORT", "5432"),
                        "USER": os.environ.get("PGUSER", ""), "PASSWORD": os.environ.get("PGPASSWORD", "")}
        settings.configure(SECRET_KEY="synthetic-url-reference", INSTALLED_APPS=[], USE_TZ=True,
                           USE_I18N=False, DEFAULT_AUTO_FIELD="django.db.models.AutoField",
                           DATABASES={"default": database})
        django.setup()
        from django import forms
        from django.core import validators
        from django.core.exceptions import ValidationError
        from django.db import connection, connections, models
        from django.db.models import base as model_base
        from django.db.models import fields as model_fields
        from django.forms import fields as form_fields
        from django.test.utils import CaptureQueriesContext
        import rest_framework
        from rest_framework import fields as serializer_fields
        from rest_framework.exceptions import ValidationError as SerializerError
        assert rest_framework.VERSION == "3.18.0" and platform.python_version() == "3.14.3"
        if database_name:
            import psycopg
            assert psycopg.__version__ == "3.3.6"
        assert not connection.introspection.table_names()
        result = {"django": django.get_version(), "drf": rest_framework.VERSION,
                  "python": platform.python_version(), "backend": connection.vendor,
                  "input_sha256": hashlib.sha256(INPUTS.read_bytes()).hexdigest(),
                  "source_sha256": {name: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest()
                                    for name, module in {"model_fields": model_fields, "model_base": model_base,
                                                         "form_fields": form_fields, "validators": validators,
                                                         "serializer_fields": serializer_fields}.items()}}
        profiles = {
            "form": forms.URLField(),
            "form_optional": forms.URLField(required=False),
            "form_http": forms.URLField(assume_scheme="http"),
            "model": models.URLField().formfield(),
            "model_optional": models.URLField(blank=True).formfield(),
            "model_nullable": models.URLField(null=True, blank=True).formfield(),
            "model_short": models.URLField(max_length=24).formfield(),
        }
        observations = {}
        for name, field in profiles.items():
            cases = {}
            for case in inputs:
                value, codes = None, []
                try:
                    value = field.clean(case["value"])
                except ValidationError as error:
                    codes = [item.code for item in error.error_list]
                cases[case["name"]] = {"value": value, "codes": codes}
            observations[name] = {"max_length": field.max_length, "required": field.required,
                                  "assume_scheme": field.assume_scheme,
                                  "widget": type(field.widget).__name__, "cases": cases}
        result["forms"] = observations
        result["validator"] = {}
        for case in inputs:
            try:
                validators.URLValidator()(case["value"])
                result["validator"][case["name"]] = True
            except ValidationError:
                result["validator"][case["name"]] = False
        result["serializers"] = {}
        for name, options in (("required", {}), ("optional", {"allow_blank": True, "allow_null": True}),
                              ("model", {"max_length": 200, "allow_blank": True}),
                              ("short", {"max_length": 24})):
            field = serializer_fields.URLField(**options)
            cases = {}
            for case in inputs:
                value, codes = None, []
                try:
                    value = field.run_validation(case["value"])
                except SerializerError as error:
                    codes = [item.code for item in error.detail]
                cases[case["name"]] = {"value": value, "codes": codes}
            result["serializers"][name] = cases

        result["serializer_defaults"] = {
            name: serializer_fields.URLField(default=value, allow_blank=True, allow_null=True).run_validation()
            for name, value in (("legacy", "not-an-url"), ("padded", "  HTTPS://Example.COM/Path  "), ("empty", ""), ("null", None))
        }
        result["changed"] = [
            dict(initial=initial, submitted=submitted, changed=forms.URLField().has_changed(initial, submitted))
            for initial, submitted in [
                ("https://example.com", "example.com"), ("https://Example.COM", "//Example.COM"),
                ("example.com", "example.com"), ("https://example.com", "HTTPS://example.com"),
                (None, ""), ("https://example.com", "not a URL"),
            ]
        ]

        class Before(models.Model):
            label = models.CharField(max_length=32)
            address = models.CharField(max_length=200, blank=True, null=True, unique=True)

            class Meta:
                app_label = "url_reference"
                db_table = "url_reference_contact"

        class After(models.Model):
            label = models.CharField(max_length=32)
            address = models.URLField(blank=True, null=True, unique=True)

            class Meta:
                app_label = "url_reference"
                db_table = "url_reference_contact"

        with connection.schema_editor() as editor:
            editor.create_model(Before)
        try:
            for label, address in (("upper", "https://Example.com/Path"), ("lower", "https://example.com/path"),
                                   ("invalid", "not-an-url"), ("empty", ""), ("null", None)):
                Before.objects.create(label=label, address=address)
            result["storage"] = {"before": list(Before.objects.order_by("id").values_list("label", "address"))}
            with connection.schema_editor() as editor, CaptureQueriesContext(connection) as captured:
                editor.alter_field(After, Before._meta.get_field("address"), After._meta.get_field("address"))
            result["storage"]["alter_sql_count"] = len(captured)
            result["storage"]["after"] = list(After.objects.order_by("id").values_list("label", "address"))
            # Native save does not implicitly invoke full_clean/URLValidator.
            created = After.objects.create(label="new_invalid", address="also-not-url")
            result["storage"]["unvalidated_save"] = After.objects.get(pk=created.pk).address
            probes = {"exact": {"address": "https://Example.com/Path"},
                      "iexact": {"address__iexact": "HTTPS://EXAMPLE.COM/PATH"},
                      "contains": {"address__icontains": "example"}, "null": {"address__isnull": True}}
            result["storage"]["queries"] = {name: list(After.objects.filter(**lookup).order_by("id").values_list("label", flat=True))
                                                for name, lookup in probes.items()}

            class URLModelForm(forms.ModelForm):
                class Meta:
                    model = After
                    fields = ["address"]

            result["model_forms"] = {}
            for name, submitted in [("bare", "new.example.com/path"), ("relative", "//new.example.com"),
                                    ("invalid", "javascript://example.com"), ("empty", ""), ("omitted", None)]:
                instance = After(label="candidate", address="https://old.example.com")
                form = URLModelForm(data={} if submitted is None else {"address": submitted}, instance=instance)
                valid = form.is_valid()
                result["model_forms"][name] = dict(valid=valid, candidate=instance.address,
                    cleaned=form.cleaned_data.get("address"), errors=[item.code for item in form.errors.as_data().get("address", [])])

            class Chosen(models.Model):
                address = models.URLField(choices=[("legacy", "Legacy"), ("https://new.example.com", "Current")])
                class Meta:
                    app_label = "url_reference"

            class ChoiceForm(forms.ModelForm):
                class Meta:
                    model = Chosen
                    fields = ["address"]

            result["model_choices"] = {}
            for raw in ["legacy", "https://new.example.com", "new.example.com"]:
                form = ChoiceForm(data={"address": raw})
                valid = form.is_valid()
                result["model_choices"][raw] = dict(valid=valid, candidate=form.instance.address,
                    errors=[item.code for item in form.errors.as_data().get("address", [])])
            with connection.schema_editor() as editor, CaptureQueriesContext(connection) as captured:
                editor.alter_field(Before, After._meta.get_field("address"), Before._meta.get_field("address"))
            result["storage"]["reverse_sql_count"] = len(captured)
            result["storage"]["reverse"] = list(Before.objects.order_by("id").values_list("label", "address"))
        finally:
            with connection.schema_editor() as editor:
                editor.delete_model(Before)
            assert not connection.introspection.table_names()
            connections.close_all()
        return result


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
