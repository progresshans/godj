# Independent Django 6.1 observer, commit fe0a859f537d4238cf49fca39073513206f83122.
# Upstream behavior reference: BSD-3-Clause, LICENSE.django.
import hashlib
import inspect
import json
import platform
import tempfile
from pathlib import Path
from unittest.mock import patch

import django
from django.conf import settings

assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
settings.configure(
    SECRET_KEY="synthetic-model-file-reference",
    USE_I18N=False,
    DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}},
)
django.setup()
from django import forms
from django.core.files.base import ContentFile
from django.core.files.storage import FileSystemStorage
from django.core.files.uploadedfile import SimpleUploadedFile
from django.db import connection, models, transaction
from django.db.models.fields import files as file_fields


class Document(models.Model):
    title = models.CharField(max_length=30, unique=True)
    document = models.FileField(upload_to="documents", max_length=40, blank=True, default="defaults/seed.txt")
    optional = models.FileField(upload_to="optional", blank=True, null=True)

    class Meta:
        app_label = "model_file_reference"


class DocumentForm(forms.ModelForm):
    class Meta:
        model = Document
        fields = ["document", "optional"]


observations = []
for case in [
    {"name": "default"},
    {"name": "retain", "initial": "old/original.txt"},
    {"name": "empty", "initial": ""},
    {"name": "clear", "initial": "old/original.txt", "clear": True},
    {"name": "upload", "initial": "old/original.txt", "upload": "new.txt"},
    {"name": "upload_and_clear", "initial": "old/original.txt", "upload": "new.txt", "clear": True},
    {"name": "forged_text", "initial": "old/original.txt", "text": "other/private.txt"},
    {"name": "long_name", "upload": "x" * 41},
    {"name": "optional_null", "optional": None},
    {"name": "optional_empty", "optional": ""},
    {"name": "optional_clear", "optional": "old/optional.txt", "optional_clear": True},
]:
    instance = Document(title=case["name"])
    if "initial" in case:
        instance.document = case["initial"]
    if "optional" in case:
        instance.optional = case["optional"]
    values = {}
    if case.get("clear"):
        values["document-clear"] = "on"
    if case.get("optional_clear"):
        values["optional-clear"] = "on"
    if "text" in case:
        values["document"] = case["text"]
    uploads = {}
    if "upload" in case:
        uploads["document"] = SimpleUploadedFile(case["upload"], b"new content", content_type="text/plain")
    form = DocumentForm(data=values, files=uploads, instance=instance)
    valid = form.is_valid()
    observations.append({
        "case": case,
        "valid": valid,
        "document": form.instance.document.name,
        "optional": form.instance.optional.name,
        "changed": form.changed_data,
        "errors": {name: [error.code for error in errors] for name, errors in form.errors.as_data().items()},
    })

with connection.schema_editor() as editor:
    editor.create_model(Document)
with tempfile.TemporaryDirectory() as directory, patch("django.core.files.storage.base.get_random_string", return_value="AAAAAAA"):
    backend = FileSystemStorage(location=directory)
    Document._meta.get_field("document").storage = backend
    first = Document(title="first", document=ContentFile(b"original", name="same.txt"))
    first.save()
    second = Document(title="second", document=ContentFile(b"replacement", name="same.txt"))
    second.save()
    collision = [first.document.name, second.document.name]
    try:
        with transaction.atomic():
            first.document = ContentFile(b"rollback upload", name="rollback.txt")
            first.save()
            raise RuntimeError("synthetic rollback")
    except RuntimeError:
        pass
    proposed_after_rollback = first.document.name
    first.refresh_from_db()
    storage_result = {
        "collision": collision,
        "db_after_rollback": first.document.name,
        "published_after_rollback": proposed_after_rollback,
        "published_survives_rollback": backend.exists(proposed_after_rollback),
        "old_survives_replace": backend.exists(collision[0]),
    }
    first.document = ""
    first.save()
    first.refresh_from_db()
    storage_result["cleared_reference"] = first.document.name
    storage_result["old_survives_clear"] = backend.exists(collision[0])
    second.delete()
    storage_result["file_survives_model_delete"] = backend.exists(collision[1])

print(json.dumps({
    "django": django.get_version(),
    "python": platform.python_version(),
    "source_sha256": hashlib.sha256(Path(inspect.getfile(file_fields)).read_bytes()).hexdigest(),
    "default_max_length": models.FileField().max_length,
    "forms": observations,
    "storage": storage_result,
}, ensure_ascii=False, indent=2, sort_keys=True))
