"""Independent Django 6.1 ModelChoiceField observations (BSD-3-Clause).

Public model/form inputs only; never imports GoDj or expected fixtures.
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
    assert django.get_version() == '6.1' and not settings.configured
    database = os.environ.get('GODJ_MODEL_CHOICE_DATABASE')
    if database:
        assert database.startswith('godj_model_choice_')
        assert importlib.metadata.version('psycopg') == '3.3.6'
    with tempfile.TemporaryDirectory(prefix='godj-model-choice-reference-') as directory:
        config = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(directory) / 'reference.sqlite3')}
        if database:
            config = {'ENGINE': 'django.db.backends.postgresql', 'NAME': database, 'HOST': 'localhost', 'PORT': 5432}
        settings.configure(SECRET_KEY='independent-reference-only', INSTALLED_APPS=[], DATABASES={'default': config}, USE_TZ=True)
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError
        from django.db import connection, models

        class Record(models.Model):
            tenant = models.IntegerField()
            label = models.CharField(max_length=80)

            class Meta:
                app_label = 'modelchoices'
                db_table = 'model_choice_record'

        assert not connection.introspection.table_names()
        with connection.schema_editor() as editor:
            editor.create_model(Record)
        try:
            for key, tenant, label in ((0, 1, 'Zero'), (7, 1, '<script>visible & quoted</script>'), (12, 2, 'private other tenant')):
                Record.objects.create(pk=key, tenant=tenant, label=label)
            field = forms.ModelChoiceField(queryset=Record.objects.filter(tenant=1).order_by('id'))
            field.label_from_instance = lambda value: value.label
            choices = [[int(str(value)), label] for value, label in field.choices if str(value) != '']
            raw_values = [None, '', '0', '-0', '7', '+7', '007', ' 7 ', '７', '٧', '0_7', '7_0',
                          '7.0', '7.', '7e0', '12', '999', '-2', 'True', 'bad', '\x00', '7\x00',
                          '\x1c7', '7\x1f', '7 0', '9223372036854775808', '-9223372036854775809']
            observations = []
            for required in (True, False):
                field.required = required
                for initial in (None, 0, 7, 12):
                    for raw in raw_values:
                        item = {'required': required, 'initial': initial, 'raw': raw,
                                'changed': field.has_changed(initial, raw), 'errors': []}
                        try:
                            value = field.clean(raw)
                            item['value'] = None if value is None else value.pk
                        except ValidationError as error:
                            item['errors'] = [entry.code for entry in error.error_list]
                        observations.append(item)
            with connection.cursor() as cursor:
                cursor.execute('SHOW server_version_num' if database else 'SELECT sqlite_version()')
                version = cursor.fetchone()[0]
            return {'django': django.get_version(), 'python': platform.python_version(), 'backend': connection.vendor,
                    'database_version': version, 'choices': choices, 'observations': observations,
                    'django_model_choice_source_sha256': hashlib.sha256(Path(inspect.getsourcefile(forms.ModelChoiceField)).read_bytes()).hexdigest()}
        finally:
            with connection.schema_editor() as editor:
                editor.delete_model(Record)
            connection.close()


if __name__ == '__main__':
    print(json.dumps(observe(), sort_keys=True, separators=(',', ':')))
