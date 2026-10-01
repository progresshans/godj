"""Independent pinned Django/DRF slug input and indexed storage observations.

Authority: Django 6.1 model/form fields and DRF 3.18 serializer fields,
BSD-3-Clause (LICENSE.django and docs/SOURCES.md). Only synthetic input is
shared with Go; this observer never reads a Go result or expected fixture.
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
INPUTS = ROOT / "internal/slugtest/testdata/slug-inputs.json"


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    assert platform.python_version() == "3.14.3"
    inputs = json.loads(INPUTS.read_text())
    database_name = os.environ.get("GODJ_SLUG_REFERENCE_DATABASE")
    if database_name:
        assert re.fullmatch(r"godj_slug_reference_[0-9]+", database_name)
    with tempfile.TemporaryDirectory(prefix="godj-slug-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if database_name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": database_name,
                        "HOST": os.environ.get("PGHOST", "localhost"), "PORT": os.environ.get("PGPORT", "5432"),
                        "USER": os.environ.get("PGUSER", ""), "PASSWORD": os.environ.get("PGPASSWORD", "")}
        settings.configure(SECRET_KEY="synthetic-slug-reference", INSTALLED_APPS=[], USE_TZ=True,
                           USE_I18N=False, DEFAULT_AUTO_FIELD="django.db.models.AutoField",
                           DATABASES={"default": database})
        django.setup()
        from django import forms
        from django.core import validators
        from django.core.exceptions import ValidationError
        from django.db import connection, connections, models, transaction, IntegrityError
        from django.db.models import base as model_base
        from django.db.models import fields as model_fields
        from django.forms import fields as form_fields
        from django.test.utils import CaptureQueriesContext
        import rest_framework
        from rest_framework import fields as serializer_fields
        from rest_framework.exceptions import ValidationError as SerializerError
        assert rest_framework.VERSION == "3.18.0"
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
        result["model_defaults"] = {"max_length": models.SlugField().max_length,
                                    "db_index": models.SlugField().db_index,
                                    "allow_unicode": models.SlugField().allow_unicode}
        profiles = {
            "form": forms.SlugField(),
            "form_unicode": forms.SlugField(allow_unicode=True),
            "form_optional": forms.SlugField(required=False),
            "form_untrimmed": forms.SlugField(strip=False),
            "model": models.SlugField().formfield(),
            "model_unicode": models.SlugField(allow_unicode=True).formfield(),
            "model_optional": models.SlugField(blank=True).formfield(),
            "model_nullable": models.SlugField(null=True, blank=True).formfield(),
            "model_short": models.SlugField(max_length=12).formfield(),
        }
        result["forms"] = {}
        for name, field in profiles.items():
            cases = {}
            for case in inputs:
                value, codes = None, []
                try:
                    value = field.clean(case["value"])
                except ValidationError as error:
                    codes = [item.code for item in error.error_list]
                cases[case["name"]] = {"value": value, "codes": codes}
            result["forms"][name] = {"max_length": field.max_length, "required": field.required,
                                     "allow_unicode": field.allow_unicode, "strip": field.strip,
                                     "widget": type(field.widget).__name__, "cases": cases}
        result["validators"] = {}
        for name, validator in (("ascii", validators.validate_slug), ("unicode", validators.validate_unicode_slug)):
            result["validators"][name] = {}
            for case in inputs:
                try:
                    validator(case["value"])
                    result["validators"][name][case["name"]] = True
                except ValidationError:
                    result["validators"][name][case["name"]] = False
        result["serializers"] = {}
        for name, options in (
            ("required", {}), ("unicode", {"allow_unicode": True}),
            ("optional", {"allow_blank": True, "allow_null": True}),
            ("model", {"max_length": 50, "allow_blank": True}),
            ("model_unicode", {"max_length": 50, "allow_blank": True, "allow_unicode": True}),
            ("short", {"max_length": 12}), ("untrimmed", {"trim_whitespace": False}),
            ("unicode_untrimmed", {"allow_unicode": True, "trim_whitespace": False}),
        ):
            field = serializer_fields.SlugField(**options)
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
            name: serializer_fields.SlugField(default=value, allow_blank=True, allow_null=True).run_validation()
            for name, value in (("legacy", "Old Slug!"), ("padded", "  Old_Name  "), ("empty", ""), ("null", None))
        }
        result["changed"] = [
            dict(initial=initial, submitted=submitted, changed=forms.SlugField(allow_unicode=True).has_changed(initial, submitted))
            for initial, submitted in [("Old_Name", " Old_Name "), ("Old_Name", "old_name"),
                                       ("한글", "한글"), ("café", "cafe\u0301"), (None, ""), ("old", "bad/value")]
        ]

        class Before(models.Model):
            label = models.CharField(max_length=32)
            address = models.CharField(max_length=50, blank=True, null=True)

            class Meta:
                app_label = "slug_reference"
                db_table = "slug_reference_article"

        def variant(name, **options):
            class Meta:
                app_label = "slug_reference"
                db_table = "slug_reference_article"
            return type(name, (models.Model,), {"__module__": __name__, "Meta": Meta,
                        "label": models.CharField(max_length=32),
                        "address": models.SlugField(blank=True, null=True, **options)})

        indexed = variant("Indexed")
        unicode = variant("Unicode", allow_unicode=True)
        unindexed = variant("Unindexed", allow_unicode=True, db_index=False)
        unique = variant("Unique", allow_unicode=True, unique=True)

        def snapshot(model):
            with connection.cursor() as cursor:
                constraints = connection.introspection.get_constraints(cursor, model._meta.db_table)
            indexes = {name: {key: value.get(key) for key in ("columns", "unique", "primary_key", "index", "orders", "type")}
                       for name, value in constraints.items() if value.get("index") or value.get("unique")}
            return {"rows": list(model.objects.order_by("id").values_list("label", "address")), "indexes": indexes}

        with connection.schema_editor() as editor:
            editor.create_model(Before)
        try:
            for label, address in (("upper", "Old_Name"), ("lower", "old_name"), ("unicode", "한글-주소"),
                                   ("invalid", "Old Slug!"), ("empty", ""), ("null", None)):
                Before.objects.create(label=label, address=address)
            result["storage"] = {"before": snapshot(Before), "lifecycle": []}
            previous = Before
            for name, model in (("add_index", indexed), ("allow_unicode", unicode), ("remove_index", unindexed),
                                ("readd_index", unicode), ("unique", unique), ("nonunique", unicode)):
                with connection.schema_editor() as editor, CaptureQueriesContext(connection) as queries:
                    editor.alter_field(model, previous._meta.get_field("address"), model._meta.get_field("address"))
                observed = snapshot(model)
                assert observed["rows"] == result["storage"]["before"]["rows"]
                result["storage"]["lifecycle"].append(dict(stage=name, sql_count=len(queries), **observed))
                if name == "unique":
                    try:
                        with transaction.atomic():
                            model.objects.create(label="duplicate", address="Old_Name")
                    except IntegrityError:
                        result["storage"]["unique_rejects_duplicate"] = True
                    else:
                        raise AssertionError("native unique slug accepted a duplicate")
                previous = model
            saved = unicode.objects.create(label="new_invalid", address="another invalid slug")
            result["storage"]["unvalidated_save"] = unicode.objects.get(pk=saved.pk).address
            probes = {"exact": {"address": "Old_Name"}, "iexact": {"address__iexact": "OLD_NAME"},
                      "contains": {"address__icontains": "old"}, "null": {"address__isnull": True}}
            result["storage"]["queries"] = {name: list(unicode.objects.filter(**lookup).order_by("id").values_list("label", flat=True))
                                                for name, lookup in probes.items()}
            result["model_forms"] = {}
            for name, model in (("ascii", indexed), ("unicode", unicode)):
                form_type = forms.modelform_factory(model, fields=["address"])
                observations = {}
                for case, submitted in [("valid", "  New_Name-2  "), ("unicode", "  읽기-쉬운_주소  "),
                                        ("invalid", "bad/value"), ("empty", ""), ("omitted", None)]:
                    instance = model(label="candidate", address="old")
                    form = form_type(data={} if submitted is None else {"address": submitted}, instance=instance)
                    valid = form.is_valid()
                    observations[case] = dict(valid=valid, candidate=instance.address, cleaned=form.cleaned_data.get("address"),
                                              errors=[error.code for error in form.errors.as_data().get("address", [])])
                result["model_forms"][name] = observations
            form = forms.modelform_factory(unicode, fields=["address"])(
                data={"address": "  읽기-쉬운_주소  "}, instance=unicode(label="form_saved"))
            assert form.is_valid()
            candidate = form.save(commit=False)
            result["storage"]["prepared"] = {"address": candidate.address,
                                               "persisted": unicode.objects.filter(label="form_saved").exists()}
            candidate.save()
            result["storage"]["form_saved"] = unicode.objects.get(pk=candidate.pk).address
            try:
                with transaction.atomic():
                    unicode.objects.create(label="rolled_back", address="rolled-back")
                    raise RuntimeError("synthetic rollback")
            except RuntimeError:
                pass
            result["storage"]["rollback_absent"] = not unicode.objects.filter(label="rolled_back").exists()

            class Chosen(models.Model):
                address = models.SlugField(choices=[("Old Slug!", "Legacy"), ("current-name", "Current")])
                class Meta:
                    app_label = "slug_reference"

            choice_form = forms.modelform_factory(Chosen, fields=["address"])
            result["model_choices"] = {}
            for raw in ["Old Slug!", "current-name", "unknown-name"]:
                form = choice_form(data={"address": raw})
                valid = form.is_valid()
                result["model_choices"][raw] = dict(valid=valid, candidate=form.instance.address,
                                                   errors=[error.code for error in form.errors.as_data().get("address", [])])
            with connection.schema_editor() as editor, CaptureQueriesContext(connection) as queries:
                editor.alter_field(Before, unicode._meta.get_field("address"), Before._meta.get_field("address"))
            result["storage"]["reverse_sql_count"] = len(queries)
            result["storage"]["reverse"] = snapshot(Before)
        finally:
            with connection.schema_editor() as editor:
                editor.delete_model(Before)
            assert not connection.introspection.table_names()
            connections.close_all()
        return result


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
