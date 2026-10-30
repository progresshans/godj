# Independent observer of Django 6.1, commit
# fe0a859f537d4238cf49fca39073513206f83122 (BSD-3-Clause, LICENSE.django).
# Pillow 12.3.0 (MIT-CMU) creates only synthetic test images.
import hashlib
import inspect
import io
import json
import platform
import tempfile
from pathlib import Path
from unittest.mock import patch
import django
import PIL
from PIL import Image
from django.conf import settings
assert django.get_version() == "6.1" and platform.python_version() == "3.14.3" and PIL.__version__ == "12.3.0"
settings.configure(SECRET_KEY="synthetic-model-image-reference", USE_I18N=False, DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}})
django.setup()
from django import forms
from django.core.files.base import ContentFile
from django.core.files.storage import FileSystemStorage
from django.core.files.uploadedfile import SimpleUploadedFile
from django.db import connection, models, transaction
from django.db.models.fields import files as file_fields


def content(width, height):
    out = io.BytesIO()
    Image.new("RGB", (width, height), (21, 43, 65)).save(out, format="PNG")
    return out.getvalue()


class CountingStorage(FileSystemStorage):
    reads = 0
    def _open(self, name, mode="rb"):
        self.reads += 1
        return super()._open(name, mode)


class Photograph(models.Model):
    title = models.CharField(max_length=30, unique=True)
    photo = models.ImageField(upload_to="photos", blank=True, null=True, width_field="width", height_field="height")
    width = models.IntegerField(null=True, editable=False)
    height = models.IntegerField(null=True, editable=False)
    class Meta:
        app_label = "model_image_reference"
    def clean(self):
        clean_calls.append(dict(photo=self.photo.name, width=self.width, height=self.height))


class PhotoForm(forms.ModelForm):
    class Meta:
        model = Photograph
        fields = ["photo"]


cases = []
clean_calls = []
with connection.schema_editor() as editor:
    editor.create_model(Photograph)
with tempfile.TemporaryDirectory() as directory, patch("django.core.files.storage.base.get_random_string", return_value="AAAAAAA"):
    backend = CountingStorage(location=directory)
    field = Photograph._meta.get_field("photo")
    field.storage = backend
    backend.save("photos/old.png", ContentFile(content(3, 2)))
    for case in [
        dict(name="new_empty"),
        dict(name="retain", existing=True),
        dict(name="retain_stale_dimensions", existing=True, width=19, height=23),
        dict(name="clear", existing=True, clear=True),
        dict(name="clear_empty", clear=True),
        dict(name="upload", upload=True),
        dict(name="replace", existing=True, upload=True),
        dict(name="replace_same_name", existing=True, upload=True, filename="old.png"),
        dict(name="contradiction", existing=True, upload=True, clear=True),
        dict(name="invalid", existing=True, invalid=True),
        dict(name="forged_reference", existing=True, forged=True),
        dict(name="forged_dimensions", existing=True, spoof=True),
        dict(name="upload_ignores_dimensions", upload=True, spoof=True),
        dict(name="late_form_error", existing=True, upload=True, late=True),
        dict(name="excluded_image", existing=True, upload=True, excluded=True),
    ]:
        clean_calls = []
        photo = "photos/old.png" if case.get("existing") else None
        width = case.get("width", 3) if case.get("existing") else None
        height = case.get("height", 2) if case.get("existing") else None
        instance = Photograph(title=case["name"], photo=photo, width=width, height=height)
        raw = {}
        if case.get("clear"): raw["photo-clear"] = "on"
        if case.get("forged"): raw["photo"] = "other/private.png"
        if case.get("spoof"): raw.update(width="999", height="888")
        files = {}
        if case.get("upload") or case.get("invalid"):
            files["photo"] = SimpleUploadedFile(case.get("filename", "new.png"), b"invalid" if case.get("invalid") else content(7, 5), "application/x-untrusted")
        selected = [] if case.get("excluded") else ["photo"]
        Form = forms.modelform_factory(Photograph, fields=selected)
        if case.get("late"):
            class Form(Form):
                def clean(self):
                    values = super().clean()
                    self.add_error(None, forms.ValidationError("synthetic late error", code="late"))
                    return values
        before_reads = backend.reads
        form = Form(data=raw, files=files, instance=instance)
        valid = form.is_valid()
        cases.append(dict(case=case, valid=valid, changed=form.changed_data, errors={name: [e.code for e in errors] for name, errors in form.errors.as_data().items()}, photo=instance.photo.name, width=instance.width, height=instance.height, reads=backend.reads-before_reads, clean=list(clean_calls)))
    first = Photograph(title="stored", photo=SimpleUploadedFile("same.png", content(7, 5)))
    first.save()
    first_saved = dict(name=first.photo.name, width=first.width, height=first.height)
    second = Photograph(title="collision", photo=SimpleUploadedFile("same.png", content(9, 6)))
    second.save()
    collision = dict(name=second.photo.name, width=second.width, height=second.height)
    try:
        with transaction.atomic():
            first.photo = SimpleUploadedFile("rollback.png", content(11, 8))
            first.save()
            raise RuntimeError("synthetic rollback")
    except RuntimeError:
        pass
    after_assignment = dict(name=first.photo.name, width=first.width, height=first.height)
    first.refresh_from_db()
    after_rollback = dict(name=first.photo.name, width=first.width, height=first.height)
    rollback_file_exists = backend.exists(after_assignment["name"])
    form = PhotoForm(data={"photo-clear": "on"}, instance=first)
    assert form.is_valid()
    form.save()
    cleared = Photograph.objects.get(pk=first.pk)
    after_clear = dict(name=cleared.photo.name, width=cleared.width, height=cleared.height, original_exists=backend.exists(first_saved["name"]))
    second_name = second.photo.name
    second.delete()
    after_delete = dict(file_exists=backend.exists(second_name))

print(json.dumps(dict(django=django.get_version(), python=platform.python_version(), pillow=PIL.__version__, sources={file_fields.__name__: hashlib.sha256(Path(inspect.getfile(file_fields)).read_bytes()).hexdigest()}, metadata=dict(max_length=field.max_length, width_field=field.width_field, height_field=field.height_field), cases=cases, storage=dict(first=first_saved, collision=collision, assigned_before_rollback=after_assignment, after_rollback=after_rollback, rollback_file_exists=rollback_file_exists, cleared=after_clear, deleted=after_delete)), ensure_ascii=False, indent=2))
