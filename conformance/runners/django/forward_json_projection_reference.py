"""Pinned Django 6.1 public forward JSON-path selection (BSD-3-Clause).

Authored inputs; this runner imports neither GoDj code nor expected observations.
The four routes independently exercise required and nullable parent/child edges.
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
    label = 'forwardjsonref'
    path = str(Path(__file__).parent)


assert django.get_version() == '6.1' and not settings.configured
with tempfile.TemporaryDirectory(prefix='godj-forward-json-projection-') as temporary:
    database = os.environ.get('GODJ_FORWARD_JSON_PROJECTION_DATABASE')
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
        label = models.CharField(max_length=60)
        payload = models.JSONField(null=True)

        class Meta:
            app_label = 'forwardjsonref'

    class Shelf(models.Model):
        primary = models.ForeignKey(Document, on_delete=models.PROTECT, related_name='primary_shelves')
        secondary = models.ForeignKey(Document, on_delete=models.SET_NULL, null=True, related_name='secondary_shelves')

        class Meta:
            app_label = 'forwardjsonref'

    class Entry(models.Model):
        label = models.CharField(max_length=60)
        primary = models.ForeignKey(Shelf, on_delete=models.PROTECT, related_name='primary_entries')
        secondary = models.ForeignKey(Shelf, on_delete=models.SET_NULL, null=True, related_name='secondary_entries')

        class Meta:
            app_label = 'forwardjsonref'

    with connection.schema_editor() as editor:
        for model in (Document, Shelf, Entry):
            editor.create_model(model)
    document_inputs = [
        ('one', '{"a":1}'), ('same', '{"a":1}'), ('null', '{"a":null}'),
        ('missing', '{}'), ('sql_null', None), ('array', '{"a":[1,true,{"n":"x"}]}'),
        ('document_null', 'null'),
    ]
    documents = []
    for label, raw in document_inputs:
        value = None if raw is None else models.JSONNull() if raw == 'null' else json.loads(raw)
        documents.append(Document.objects.create(label=label, payload=value))
    shelf_inputs = [(0, 1), (2, None), (3, 4), (5, 2), (4, 6)]
    shelves = [Shelf.objects.create(primary=documents[first], secondary=None if second is None else documents[second])
               for first, second in shelf_inputs]
    entry_inputs = [(0, None), (1, 0), (2, 1), (3, 2), (4, 3), (0, 4), (1, 0), (3, None)]
    for index, (first, second) in enumerate(entry_inputs):
        Entry.objects.create(label=f'e{index}', primary=shelves[first], secondary=None if second is None else shelves[second])
    routes = ['primary__primary', 'secondary__primary', 'primary__secondary', 'secondary__secondary']
    paths = [route + '__payload__a' for route in routes]
    definitions = [
        ('all', Q()),
        ('primary_one', Q(primary__primary__label='one')),
        ('not_primary_one', ~Q(primary__primary__label='one')),
        ('optional_parent_one', Q(secondary__primary__label='one')),
        ('not_optional_parent_one', ~Q(secondary__primary__label='one')),
        ('optional_or_local', Q(secondary__primary__label='one') | Q(label='e0')),
        ('nested_or', Q(secondary__primary__payload__has_key='a') | Q(secondary__secondary__payload__isnull=True)),
        ('empty', Q(primary__primary__label='one') & Q(primary__primary__label='null')),
    ]
    observations = []
    for name, condition in definitions:
        for selection in [*paths, 'paths_only']:
            fields = paths if selection == 'paths_only' else ['id', 'label', selection]
            for distinct in (False, True):
                source = Entry.objects.filter(condition).order_by(*([] if selection == 'paths_only' else ['id'])).values_list(*fields)
                if distinct:
                    source = source.distinct()
                for sliced in (False, True):
                    if sliced and selection == 'paths_only':
                        continue
                    query = source.all()
                    if sliced:
                        query = query[1:4]
                    row = {'name': name, 'selection': selection, 'distinct': distinct, 'sliced': sliced}
                    row['count'] = query.count()
                    row['rows'] = [list(value) for value in query]
                    if selection == 'paths_only':
                        row['rows'].sort(key=lambda value: json.dumps(value, sort_keys=True))
                    observations.append(row)
    absence = []
    for route, path in zip(routes, paths):
        def ids(lookup):
            return list(Entry.objects.filter(**{lookup: True}).order_by('id').values_list('id', flat=True))
        absence.append({'path': path, 'target_absent': ids(route + '__isnull'),
                        'source_sql_null_or_absent': ids(route + '__payload__isnull'), 'missing': ids(path + '__isnull')})
    with connection.cursor() as cursor:
        cursor.execute('SELECT version()' if database else 'SELECT sqlite_version()')
        version = cursor.fetchone()[0]
    with connection.schema_editor() as editor:
        for model in (Entry, Shelf, Document):
            editor.delete_model(model)
    result = {
        'django': django.get_version(), 'python': platform.python_version(), 'backend': connection.vendor,
        'database_version': version, 'documents': [{'label': label, 'raw': raw} for label, raw in document_inputs],
        'shelves': [{'primary': first, 'secondary': second} for first, second in shelf_inputs],
        'entries': [{'label': f'e{i}', 'primary': first, 'secondary': second} for i, (first, second) in enumerate(entry_inputs)],
        'paths': paths, 'absence': absence, 'observations': observations,
    }
    if database:
        result['psycopg'] = importlib.metadata.version('psycopg')
    print(json.dumps(result, ensure_ascii=True, sort_keys=True, separators=(',', ':')))
    connection.close()
