# Independent observer of Django 6.1, commit
# fe0a859f537d4238cf49fca39073513206f83122 (BSD-3-Clause, LICENSE.django).
# Pillow 12.3.0 (MIT-CMU) creates only synthetic image content.
import base64
import hashlib
import inspect
import io
import json
import platform
import tempfile
from pathlib import Path

import django
import PIL
from PIL import Image
from django.conf import settings

assert django.get_version() == "6.1"
assert platform.python_version() == "3.14.3" and PIL.__version__ == "12.3.0"
settings.configure(
    SECRET_KEY="synthetic-stored-image-reference",
    USE_I18N=False,
    DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}},
)
django.setup()

from django.core.files import File
from django.core.files import images as image_files
from django.core.files.base import ContentFile
from django.core.files.storage import FileSystemStorage
from django.db import connection, models, transaction
from django.db.models.fields import files as model_files


def content(width, height):
    output = io.BytesIO()
    Image.new("RGB", (width, height), (21, 43, 65)).save(output, format="PNG")
    return output.getvalue()


class ObservedFile(File):
    def __init__(self, source, storage):
        super().__init__(source)
        self.storage = storage

    def close(self):
        self.storage.closes += 1
        return super().close()


class ObservedStorage(FileSystemStorage):
    opens = 0
    closes = 0

    def _open(self, name, mode="rb"):
        self.opens += 1
        return ObservedFile(super()._open(name, mode), self)


class Photograph(models.Model):
    photo = models.ImageField(blank=True, null=True, width_field="width", height_field="height")
    width = models.IntegerField(null=True)
    height = models.IntegerField(null=True)

    class Meta:
        app_label = "stored_image_reference"


def snapshot(instance):
    return dict(name=instance.photo.name, width=instance.width, height=instance.height)


field = Photograph._meta.get_field("photo")
payloads = dict(full=content(3, 2), replacement=content(7, 5), corrupt=b"not an image")
payloads["header_only"] = payloads["full"][:41]
cases = []
with tempfile.TemporaryDirectory() as directory:
    backend = ObservedStorage(location=directory)
    field.storage = backend
    for name, data in payloads.items():
        backend.save("images/" + name + ".png", ContentFile(data))
    for case in [
        dict(name="stale", file="images/full.png", width=True, height=True),
        dict(name="width_only", file="images/full.png", width=True, height=False),
        dict(name="height_only", file="images/full.png", width=False, height=True),
        dict(name="no_dimensions", file="images/full.png", width=False, height=False),
        dict(name="null", file=None, width=True, height=True),
        dict(name="empty", file="", width=True, height=True),
        dict(name="missing", file="images/missing.png", width=True, height=True),
        dict(name="corrupt", file="images/corrupt.png", width=True, height=True),
        dict(name="header_only", file="images/header_only.png", width=True, height=True),
    ]:
        field.width_field = "width" if case["width"] else None
        field.height_field = "height" if case["height"] else None
        instance = Photograph(photo=case["file"], width=19, height=23)
        before = snapshot(instance)
        opens, closes = backend.opens, backend.closes
        error = None
        try:
            field.update_dimension_fields(instance, force=True)
        except Exception as failure:
            error = type(failure).__name__
        cases.append(dict(case=case, before=before, after=snapshot(instance), error=error, opens=backend.opens-opens, closes=backend.closes-closes))

    field.width_field, field.height_field = "width", "height"
    cached = Photograph(photo="images/full.png", width=19, height=23)
    field.update_dimension_fields(cached, force=True)
    first = snapshot(cached)
    backend.delete("images/full.png")
    backend.save("images/full.png", ContentFile(payloads["replacement"]))
    opens = backend.opens
    field.update_dimension_fields(cached, force=True)
    reused = dict(value=snapshot(cached), opens=backend.opens-opens)
    fresh = Photograph(photo="images/full.png", width=19, height=23)
    opens = backend.opens
    field.update_dimension_fields(fresh, force=True)
    reopened = dict(value=snapshot(fresh), opens=backend.opens-opens)

    with connection.schema_editor() as editor:
        editor.create_model(Photograph)
    persisted = Photograph(photo="images/full.png", width=19, height=23)
    persisted.save()

    def stored():
        return Photograph.objects.filter(pk=persisted.pk).values("photo", "width", "height").get()

    before = stored()
    field.update_dimension_fields(persisted, force=True)
    candidate, before_save = snapshot(persisted), stored()
    try:
        with transaction.atomic():
            persisted.save(update_fields=["width", "height"])
            raise RuntimeError("synthetic rollback")
    except RuntimeError:
        pass
    after_rollback = stored()
    persisted.save(update_fields=["width", "height"])
    after_save = stored()

print(json.dumps(dict(
    django=django.get_version(), python=platform.python_version(), pillow=PIL.__version__,
    sources={module.__name__: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for module in [model_files, image_files]},
    payloads={name: base64.b64encode(value).decode() for name, value in payloads.items()},
    cases=cases,
    cache=dict(first=first, reused=reused, fresh=reopened),
    persistence=dict(before=before, candidate=candidate, before_save=before_save, after_rollback=after_rollback, after_save=after_save),
), ensure_ascii=False, indent=2))
