"""Pinned Django 6.1 public JSON icontains observations (BSD-3-Clause).

The canonical SQLite profile is the existing GoDj storage policy exposed through
Django's public JSONField encoder. It is not the default Django representation.
No GoDj code or expected results are imported.
"""
import importlib.metadata
import json
import os
import platform
import tempfile
from pathlib import Path

import django
from django.apps import AppConfig
from django.conf import settings


class ProbeConfig(AppConfig):
    name = __name__
    label = 'jsontextref'
    path = str(Path(__file__).parent)


class CanonicalStorageEncoder(json.JSONEncoder):
    def __init__(self, *args, **kwargs):
        kwargs.update(ensure_ascii=False, sort_keys=True, separators=(',', ':'))
        super().__init__(*args, **kwargs)

    def encode(self, value):
        result = super().encode(value)
        for char, escaped in [('<', r'\u003c'), ('>', r'\u003e'), ('&', r'\u0026'),
                              ('\u2028', r'\u2028'), ('\u2029', r'\u2029')]:
            result = result.replace(char, escaped)
        return result


VALUES = ['null', 'false', 'true', '0', '-10', '12.5', '1.0', '1e-8',
          '9007199254740993', '340282366920938463463374607431768211455',
          '""', '"Alpha Beta"', '"50%_Back\\\\slash"', '"quote\\\"line\\nnext"',
          '"한글"', '"Ææ Éé"', '"Straße STRASSE"', '"İıIi"', '"null"', '"true"',
          '"before\\u0000after"', '[]', '["Alpha",null,12.5]', '{}', '{"name":"Alpha","n":12.5}']
NEEDLES = ['', 'ALPHA', 'beta', '%_', '\\', '"', '\n', '한글', 'Æ', 'æ', 'é', 'SS', 'i',
           'null', 'true', '12.5', '1.0', '1e-8', '4740993', '768211455', 'after', '\u0000', 'before\u0000after']

assert django.get_version() == '6.1' and not settings.configured
database = os.environ.get('GODJ_JSON_TEXT_DATABASE')
canonical = os.environ.get('GODJ_JSON_CANONICAL_STORAGE') == '1'
assert not (canonical and database)
with tempfile.TemporaryDirectory(prefix='godj-json-text-reference-') as temporary:
    config = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(temporary) / 'reference.sqlite3')}
    if database:
        assert importlib.metadata.version('psycopg') == '3.3.6'
        config = {'ENGINE': 'django.db.backends.postgresql', 'NAME': database, 'HOST': 'localhost', 'PORT': 5432}
    settings.configure(SECRET_KEY='independent-reference-only', INSTALLED_APPS=['__main__.ProbeConfig'],
                       DATABASES={'default': config}, USE_TZ=True)
    django.setup()
    from django.db import connection, models
    from django.db.models import Q

    class Document(models.Model):
        label = models.CharField(max_length=40)
        payload = models.JSONField(null=True, encoder=CanonicalStorageEncoder if canonical else None)

        class Meta:
            app_label = 'jsontextref'

    class Link(models.Model):
        label = models.CharField(max_length=40)
        record = models.ForeignKey(Document, on_delete=models.SET_NULL, null=True, related_name='links')

        class Meta:
            app_label = 'jsontextref'

    samples = [{'label': 'sql_null', 'raw': None}]
    for i, raw in enumerate(VALUES):
        samples.append({'label': f'root_{i:02}', 'raw': raw})
        samples.append({'label': f'path_{i:02}', 'raw': '{"x":' + raw + '}'})
    samples += [{'label': 'empty_key', 'raw': '{"":"Alpha","x":"wrong"}'},
                {'label': 'nul_key', 'raw': '{"x\\u0000":"Alpha","x":"wrong"}'}]
    with connection.schema_editor() as editor:
        editor.create_model(Document)
        editor.create_model(Link)
    rejected = []
    try:
        for sample in samples:
            raw = sample['raw']
            value = None if raw is None else models.JSONNull() if raw == 'null' else json.loads(raw)
            try:
                document = Document.objects.create(label=sample['label'], payload=value)
            except Exception as error:
                rejected.append({'label': sample['label'], 'exception': type(error).__name__})
                continue
            Link.objects.create(label='l_' + sample['label'], record=document)
        Link.objects.create(label='l_absent', record=None)
        observations = []
        for related in (False, True):
            model = Link if related else Document
            for scope in ('root', 'x'):
                field = ('record__' if related else '') + 'payload' + ('__x' if scope == 'x' else '')
                for needle in NEEDLES:
                    for mode in ('filter', 'exclude'):
                        row = {'related': related, 'scope': scope, 'needle': needle, 'mode': mode}
                        try:
                            qs = getattr(model.objects, mode)(**{field + '__icontains': needle}).order_by('id')
                            row['count'] = qs.count()
                            row['rows'] = list(qs.values_list('label', flat=True))
                        except Exception as error:
                            row['exception'] = type(error).__name__
                        observations.append(row)
        compositions = []
        match = Q(record__payload__x__icontains='ALPHA')
        for name, condition in [('match_or_absent', match | Q(record__isnull=True)),
                                ('not_match_or_absent', ~(match | Q(record__isnull=True))),
                                ('match_and_label', match & Q(label__icontains='path'))]:
            qs = Link.objects.filter(condition).order_by('id')
            compositions.append({'name': name, 'count': qs.count(), 'rows': list(qs.values_list('label', flat=True))})
        with connection.cursor() as cursor:
            cursor.execute('SELECT version()' if database else 'SELECT sqlite_version()')
            version = cursor.fetchone()[0]
        result = {'django': django.get_version(), 'python': platform.python_version(), 'backend': connection.vendor,
                  'database_version': version, 'storage': 'godj_canonical' if canonical else 'django_default',
                  'samples': samples, 'rejected': rejected, 'needles': NEEDLES,
                  'observations': observations, 'compositions': compositions}
        if database:
            result['psycopg'] = importlib.metadata.version('psycopg')
        print(json.dumps(result, ensure_ascii=True, sort_keys=True, separators=(',', ':')))
    finally:
        with connection.schema_editor() as editor:
            editor.delete_model(Link)
            editor.delete_model(Document)
        connection.close()
