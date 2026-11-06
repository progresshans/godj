"""Authored Django 6.1 ModelMultipleChoiceField observations (BSD-3-Clause).

Public model/form inputs only. No GoDj imports or expected product results.
"""
import hashlib
import importlib.metadata
import inspect
import json
import os
import platform
import tempfile
from pathlib import Path

import django
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    database = os.environ.get("GODJ_MODEL_MULTIPLE_DATABASE")
    if database:
        assert database.startswith("godj_model_multiple_")
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-model-multiple-reference-") as directory:
        config = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if database:
            config = {"ENGINE": "django.db.backends.postgresql", "NAME": database, "HOST": "localhost", "PORT": 5432}
        settings.configure(SECRET_KEY="independent-reference-only", INSTALLED_APPS=[], DATABASES={"default": config}, USE_TZ=True, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField")
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError
        from django.db import DatabaseError, connection, models

        class Record(models.Model):
            tenant = models.IntegerField()
            label = models.CharField(max_length=80)

            class Meta:
                app_label = "modelmultiple"
                db_table = "model_multiple_record"

        assert not connection.introspection.table_names()
        with connection.schema_editor() as editor:
            editor.create_model(Record)
        try:
            for key, tenant, label in ((-4, 1, "Negative"), (0, 1, "Zero"), (7, 1, '<script>visible & quoted</script>'), (12, 2, "private other tenant"), (1 << 60, 1, "Large")):
                Record.objects.create(pk=key, tenant=tenant, label=label)
            field = forms.ModelMultipleChoiceField(queryset=Record.objects.filter(tenant=1).order_by("id"))
            field.label_from_instance = lambda value: value.label
            choices = [[int(str(value)), label] for value, label in field.choices]
            raw_values = [None, [], [""], ["0"], ["-0"], ["7"], ["7", "0"], ["0", "7"], ["7", "7"], ["+7"], ["007"], [" 7 "], ["７"], ["٧"], ["0_7"], ["7_0"], ["7.0"], ["7."], ["7e0"], ["12"], ["999"], ["-4"], ["True"], ["bad"], ["\x00"], ["7\x00"], ["\x1c7"], ["7\x1f"], ["7 0"], [str(1 << 60)], ["7", "bad"], ["7", "12"], ["7", "+7"], ["7", "7", "0"]]
            observations = []
            for required in (True, False):
                field.required = required
                for initial in (None, [], [0], [7], [0, 7], [12], [7, 7]):
                    for raw in raw_values:
                        item = {"required": required, "initial": initial, "raw": raw, "changed": field.has_changed(initial, raw), "errors": []}
                        try:
                            item["value"] = [record.pk for record in field.clean(raw)]
                        except ValidationError as error:
                            item["errors"] = [entry.code for entry in error.error_list]
                        observations.append(item)
            # Direct Python inputs that HTML's list-of-text transport cannot
            # represent are separate observations, never coerced into that set.
            direct = []
            for raw in ("7", 7, True, [[7]], {"id": 7}):
                try:
                    value = [record.pk for record in field.clean(raw)]
                    direct.append({"raw": raw, "value": value, "errors": []})
                except ValidationError as error:
                    direct.append({"raw": raw, "errors": [entry.code for entry in error.error_list]})
            out_of_range = []
            for raw in (["9223372036854775808"], ["-9223372036854775809"]):
                item = {"raw": raw}
                try:
                    item["value"] = [record.pk for record in field.clean(raw)]
                except ValidationError as error:
                    item["errors"] = [entry.code for entry in error.error_list]
                except (OverflowError, DatabaseError) as error:
                    # Preserve native failure classification; this is not a
                    # validation success or a cross-backend equivalence claim.
                    item["execution_error"] = type(error).__name__
                out_of_range.append(item)
            with connection.cursor() as cursor:
                cursor.execute("SHOW server_version_num" if database else "SELECT sqlite_version()")
                version = cursor.fetchone()[0]
            return {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                    "database_version": version, "choices": choices, "observations": observations, "direct_python": direct, "out_of_range": out_of_range,
                    "django_model_multiple_source_sha256": hashlib.sha256(Path(inspect.getsourcefile(forms.ModelMultipleChoiceField)).read_bytes()).hexdigest()}
        finally:
            with connection.schema_editor() as editor:
                editor.delete_model(Record)
            connection.close()


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, separators=(",", ":")))
