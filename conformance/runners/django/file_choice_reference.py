"""Independent Django 6.1 file/image choice observations.

Behavior sources: Django fe0a859f537d4238cf49fca39073513206f83122,
BSD-3-Clause. Synthetic images use Pillow 12.3.0 (MIT-CMU).
Django license: repository-root LICENSE.django; no GoDj code is imported.
"""
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

assert django.get_version() == '6.1' and platform.python_version() == '3.14.3' and PIL.__version__ == '12.3.0'
settings.configure(SECRET_KEY='synthetic-file-choice-observer', USE_I18N=False,
                   DATABASES={'default': {'ENGINE': 'django.db.backends.sqlite3', 'NAME': ':memory:'}})
django.setup()
from django import forms
from django.core.files.base import ContentFile
from django.core.files.storage import FileSystemStorage
from django.core.files.uploadedfile import SimpleUploadedFile
from django.db import connection, models, transaction
from django.db.models.fields import files as file_fields


class Document(models.Model):
    document = models.FileField(max_length=40, choices=[('files/a.txt', 'A'), ('files/b.txt', 'B')], blank=True)
    optional = models.FileField(max_length=40, choices=[('files/a.txt', 'A')], blank=True, null=True)
    defaulted = models.FileField(max_length=40, choices=[('files/a.txt', 'A'), ('files/b.txt', 'B')], blank=True, default='files/a.txt')

    class Meta:
        app_label = 'file_choice_observer'


class CountingStorage(FileSystemStorage):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.reads = []

    def _open(self, name, mode='rb'):
        self.reads.append(name)
        return super()._open(name, mode)


class Photograph(models.Model):
    photo = models.ImageField(max_length=40, choices=[('images/a.png', 'A'), ('images/b.png', 'B'), ('images/absent.png', 'Missing')],
                              blank=True, null=True, width_field='width', height_field='height')
    width = models.IntegerField(null=True, editable=False)
    height = models.IntegerField(null=True, editable=False)

    class Meta:
        app_label = 'file_choice_observer'


def png(width, height):
    data = io.BytesIO()
    Image.new('RGB', (width, height), (23, 47, 71)).save(data, format='PNG')
    return data.getvalue()


def bind(model, field, initial, data, uploads=None, **values):
    Form = forms.modelform_factory(model, fields=[field])
    instance = model(**{field: initial}, **values)
    form = Form(data=data, files=uploads or {}, instance=instance)
    result = {'model': model.__name__, 'name_field': field, 'input': data, 'has_upload': bool(uploads), 'initial': initial, 'field': type(form.fields[field]).__name__, 'widget': type(form.fields[field].widget).__name__,
              'multipart': form.is_multipart()}
    try:
        result.update(valid=form.is_valid(), changed=form.changed_data,
                      errors={key: [error.code for error in errors] for key, errors in form.errors.as_data().items()},
                      cleaned=form.cleaned_data.get(field), name=getattr(instance, field).name)
    except Exception as error:
        result['exception'] = type(error).__name__
    if model is Photograph:
        result.update(width=instance.width, height=instance.height)
    return result


with connection.schema_editor() as editor:
    editor.create_model(Document)
    editor.create_model(Photograph)

cases = []
for case, data, uploads in [
    ('same', {'document': 'files/a.txt'}, None),
    ('changed', {'document': 'files/b.txt'}, None),
    ('unknown', {'document': 'files/c.txt'}, None),
    ('empty', {'document': ''}, None),
    ('omitted', {}, None),
    ('clear', {'document-clear': 'on'}, None),
    ('upload_only', {}, {'document': SimpleUploadedFile('a.txt', b'synthetic')}),
    ('text_and_upload', {'document': 'files/b.txt'}, {'document': SimpleUploadedFile('a.txt', b'synthetic')}),
]:
    cases.append({'case': case, **bind(Document, 'document', 'files/a.txt', data, uploads)})
for field in ('optional', 'defaulted'):
    for case, data in [('omitted', {}), ('empty', {field: ''}), ('same', {field: 'files/a.txt'})]:
        cases.append({'case': field + '_' + case, **bind(Document, field, 'files/a.txt', data)})
with tempfile.TemporaryDirectory(prefix='godj-file-choices-native-data-') as directory:
    backend = CountingStorage(location=directory)
    Photograph._meta.get_field('photo').storage = backend
    backend.save('images/a.png', ContentFile(png(3, 2)))
    backend.save('images/b.png', ContentFile(png(7, 5)))
    for case, data in [('same', {'photo': 'images/a.png'}), ('changed', {'photo': 'images/b.png'}),
                       ('unknown', {'photo': 'images/other.png'}), ('missing_content', {'photo': 'images/absent.png'}),
                       ('empty', {'photo': ''}), ('omitted', {})]:
        backend.reads = []
        result = bind(Photograph, 'photo', 'images/a.png', data, width=19, height=23)
        cases.append({'case': 'image_' + case, **result, 'reads': backend.reads})

    stored = Photograph(photo='images/a.png', width=3, height=2)
    stored.save()
    Form = forms.modelform_factory(Photograph, fields=['photo'])
    form = Form(data={'photo': 'images/b.png'}, instance=stored)
    assert form.is_valid()
    selected = dict(name=stored.photo.name, width=stored.width, height=stored.height)
    try:
        with transaction.atomic():
            form.save()
            raise RuntimeError('synthetic rollback')
    except RuntimeError:
        pass
    restored = Photograph.objects.get(pk=stored.pk)
    persistence = dict(selected=selected, after_rollback=dict(name=restored.photo.name, width=restored.width, height=restored.height),
                       original_exists=backend.exists('images/a.png'), selected_exists=backend.exists('images/b.png'))

print(json.dumps({'django': django.get_version(), 'python': platform.python_version(), 'pillow': PIL.__version__,
                  'persistence': persistence, 'django_commit': 'fe0a859f537d4238cf49fca39073513206f83122',
                  'file_fields_sha256': hashlib.sha256(Path(inspect.getfile(file_fields)).read_bytes()).hexdigest(),
                  'cases': cases}, ensure_ascii=False, sort_keys=True, indent=2))
