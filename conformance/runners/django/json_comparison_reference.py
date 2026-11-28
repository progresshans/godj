"""Django 6.1 public JSON range/order observations (BSD-3-Clause).

The default profile observes Django's storage unchanged. The explicitly named
canonical profile observes the existing GoDj JSON storage policy through a
public JSONField encoder; it is not Django's default JSON representation.
No GoDj implementation or expected results are imported.
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
    label = 'jsoncomparisonref'
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


RAW = ['null', 'false', 'true', '-2', '0', '1', '1.0', '1.5', '12', '9007199254740993',
       '340282366920938463463374607431768211455', '""', '"0"', '"a"', '"null"', '"한글"',
       '[]', '[0]', '[1]', '{}', '{"x":null}', '{"x":false}', '{"x":true}', '{"x":-2}',
       '{"x":0}', '{"x":1}', '{"x":1.5}', '{"x":"0"}', '{"x":"a"}', '{"x":"null"}',
       '{"x":"true"}', '{"x":"한글"}', '{"x":[]}', '{"x":{}}', '{"x":[0]}',
       '{"x":340282366920938463463374607431768211455}']
OPERANDS = ['null', 'false', 'true', '-1', '0', '1', '1.5', '""', '"0"', '"a"', '"null"', '[]', '{}', '[0]']


assert django.get_version() == '6.1' and not settings.configured
database = os.environ.get('GODJ_JSON_COMPARISON_DATABASE')
canonical = os.environ.get('GODJ_JSON_CANONICAL_STORAGE') == '1'
assert not (canonical and database), 'canonical storage profile belongs to SQLite'
with tempfile.TemporaryDirectory(prefix='godj-json-comparison-reference-') as temporary:
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
            app_label = 'jsoncomparisonref'

    class Link(models.Model):
        label = models.CharField(max_length=40)
        record = models.ForeignKey(Document, on_delete=models.SET_NULL, null=True, related_name='links')

        class Meta:
            app_label = 'jsoncomparisonref'

    samples = [{'label': 'sql_null', 'raw': None}, *[{'label': f'd{i:02}', 'raw': raw} for i, raw in enumerate(RAW)]]
    with connection.schema_editor() as editor:
        editor.create_model(Document)
        editor.create_model(Link)
    try:
        for sample in samples:
            raw = sample['raw']
            value = None if raw is None else models.JSONNull() if raw == 'null' else json.loads(raw)
            document = Document.objects.create(label=sample['label'], payload=value)
            Link.objects.create(label='l_' + sample['label'], record=document)
        Link.objects.create(label='l_absent', record=None)
        observations = []
        for related in (False, True):
            model = Link if related else Document
            for scope in ('root', 'x'):
                field = ('record__' if related else '') + 'payload' + ('__x' if scope == 'x' else '')
                for lookup in ('gt', 'gte', 'lt', 'lte'):
                    for raw in OPERANDS:
                        value = models.JSONNull() if raw == 'null' else json.loads(raw)
                        for mode in ('filter', 'exclude'):
                            row = {'name': f'{"forward" if related else "root"}/{scope}/{lookup}/{raw}/{mode}',
                                   'related': related, 'scope': scope, 'lookup': lookup, 'rhs': raw, 'mode': mode}
                            try:
                                qs = getattr(model.objects, mode)(**{field + '__' + lookup: value}).order_by('id')
                                row['count'] = qs.count()
                                row['rows'] = list(qs.values_list('label', flat=True))
                            except Exception as error:
                                row['exception'] = type(error).__name__
                            observations.append(row)
        compositions = []
        for name, condition in [
            ('forward_gt_or_absent', Q(record__payload__x__gt=1) | Q(record__isnull=True)),
            ('forward_between', Q(record__payload__x__gte=0) & Q(record__payload__x__lte=1)),
            ('forward_not_gt_or_absent', ~(Q(record__payload__x__gt=1) | Q(record__isnull=True))),
            ('forward_gt_or_label', Q(record__payload__x__gt=1) | Q(label='l_absent')),
        ]:
            qs = Link.objects.filter(condition).order_by('id')
            compositions.append({'name': name, 'count': qs.count(), 'rows': list(qs.values_list('label', flat=True))})
        orderings = []
        for scope in ('root', 'x'):
            field = 'payload' + ('__x' if scope == 'x' else '')
            for descending in (False, True):
                qs = Document.objects.order_by(('-' if descending else '') + field, 'id')
                orderings.append({'scope': scope, 'descending': descending, 'rows': list(qs.values_list('label', flat=True))})
        ordering_cases = []
        for related in (False, True):
            model = Link if related else Document
            for scope in ('root', 'x'):
                field = ('record__' if related else '') + 'payload' + ('__x' if scope == 'x' else '')
                for descending in (False, True):
                    for distinct in (False, True):
                        for sliced in (False, True):
                            qs = model.objects.order_by(('-' if descending else '') + field, 'id')
                            if distinct:
                                qs = qs.distinct()
                            if sliced:
                                qs = qs[1:8]
                            count = qs.count()
                            rows = [row.label for row in qs]
                            selected = qs.values_list('id', 'label', field)
                            ordering_cases.append({'related': related, 'scope': scope, 'descending': descending,
                                                   'distinct': distinct, 'sliced': sliced, 'count': count,
                                                   'rows': rows, 'projected': [row[1] for row in selected]})
        with connection.cursor() as cursor:
            cursor.execute('SELECT version()' if database else 'SELECT sqlite_version()')
            version = cursor.fetchone()[0]
        result = {'django': django.get_version(), 'python': platform.python_version(), 'backend': connection.vendor,
                  'database_version': version, 'storage': 'godj_canonical' if canonical else 'django_default',
                  'samples': samples, 'operands': OPERANDS, 'observations': observations,
                  'compositions': compositions, 'orderings': orderings, 'ordering_cases': ordering_cases}
        if database:
            result['psycopg'] = importlib.metadata.version('psycopg')
        print(json.dumps(result, ensure_ascii=True, sort_keys=True, separators=(',', ':')))
    finally:
        with connection.schema_editor() as editor:
            editor.delete_model(Link)
            editor.delete_model(Document)
        connection.close()
