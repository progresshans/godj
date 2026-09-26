"""Pinned Django 6.1 public forward scalar selections (BSD-3-Clause).

Authored inputs and public ORM only. No GoDj implementation/expected imports.
Representations preserve integer/Decimal precision and binary64 bits; SQL NULL
presence for JSON is observed independently of Python's None readback.
"""
import datetime as dt
import decimal
import importlib.metadata
import json
import os
import platform
import struct
import tempfile
import uuid
from pathlib import Path

import django
from django.apps import AppConfig
from django.conf import settings


class ProbeConfig(AppConfig):
    name = __name__
    label = 'forwardscalarref'
    path = str(Path(__file__).parent)


KINDS = ['integer', 'text', 'boolean', 'float', 'decimal', 'datetime', 'date', 'time', 'duration', 'uuid', 'json']
A = ['-9007199254740993', "quoted '_% 한글", 'true', '-1.25', '123.456789', '2024-02-29T12:34:56.123456+00:00',
     '2000-02-29', '23:59:58.654321', '-1', '00112233-4455-6677-8899-aabbccddeeff',
     '{"large":340282366920938463463374607431768211455,"flag":true,"seq":[null,{}]}']
B = ['0', '', 'false', '0', '0', '2000-01-01T00:00:00+00:00', '1970-01-01', '00:00:00', '0',
     '00000000-0000-0000-0000-000000000000', 'null']
C = ['9223372036854775807', 'third', 'false', '0.1', '-999.125', '1999-12-31T23:59:59.999999+00:00',
     '9999-12-31', '12:34:00.000001', '86400000001', 'ffffffff-ffff-ffff-ffff-ffffffffffff', '{"a":[true,0,null],"other":"x"}']
INPUTS = [{'label': 'A', 'required': A, 'nullable': [None] * len(KINDS)},
          {'label': 'B', 'required': B, 'nullable': B}, {'label': 'C', 'required': C, 'nullable': A}]


def convert(kind, value, models):
    if value is None:
        return None
    return {
        'integer': int, 'text': str, 'boolean': lambda x: x == 'true', 'float': float, 'decimal': decimal.Decimal,
        'datetime': dt.datetime.fromisoformat, 'date': dt.date.fromisoformat, 'time': dt.time.fromisoformat,
        'duration': lambda x: dt.timedelta(microseconds=int(x)), 'uuid': uuid.UUID,
        'json': lambda x: models.JSONNull() if x == 'null' else json.loads(x),
    }[kind](value)


def normalized(kind, value):
    if value is None:
        return None
    if kind == 'boolean':
        return 'true' if value else 'false'
    if kind == 'float':
        return struct.pack('>d', value).hex()
    if kind == 'decimal':
        return format(value, '.6f')
    if kind == 'datetime':
        return value.astimezone(dt.timezone.utc).isoformat(timespec='microseconds').replace('+00:00', 'Z')
    if kind == 'date':
        return value.isoformat()
    if kind == 'time':
        return value.isoformat(timespec='microseconds')
    if kind == 'duration':
        return str((value.days * 86400 + value.seconds) * 1000000 + value.microseconds)
    if kind == 'json':
        return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    return str(value)


assert django.get_version() == '6.1' and not settings.configured
with tempfile.TemporaryDirectory(prefix='godj-forward-scalar-reference-') as temporary:
    database = os.environ.get('GODJ_FORWARD_SCALAR_DATABASE')
    config = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(temporary) / 'reference.sqlite3')}
    if database:
        assert importlib.metadata.version('psycopg') == '3.3.6'
        config = {'ENGINE': 'django.db.backends.postgresql', 'NAME': database, 'HOST': 'localhost', 'PORT': 5432}
    settings.configure(SECRET_KEY='independent-reference-only', INSTALLED_APPS=['__main__.ProbeConfig'],
                       DATABASES={'default': config}, USE_TZ=True)
    django.setup()
    from django.db import connection, models
    from django.db.models import Q

    factories = dict(zip(KINDS, [models.BigIntegerField, models.TextField, models.BooleanField, models.FloatField,
                               lambda **kwargs: models.DecimalField(max_digits=30, decimal_places=6, **kwargs),
                               models.DateTimeField, models.DateField, models.TimeField, models.DurationField,
                               models.UUIDField, models.JSONField]))
    names = [prefix + kind for prefix in ('v_', 'n_') for kind in KINDS]
    attrs = {'__module__': __name__, 'label': models.CharField(max_length=30),
             'Meta': type('Meta', (), {'app_label': 'forwardscalarref'})}
    for name in names:
        attrs[name] = factories[name[2:]](null=name.startswith('n_'))
    Datum = type('Datum', (models.Model,), attrs)

    class Holder(models.Model):
        primary = models.ForeignKey(Datum, on_delete=models.PROTECT, related_name='primary_holders')
        secondary = models.ForeignKey(Datum, on_delete=models.SET_NULL, null=True, related_name='secondary_holders')

        class Meta:
            app_label = 'forwardscalarref'

    class Entry(models.Model):
        label = models.CharField(max_length=30)
        primary = models.ForeignKey(Holder, on_delete=models.PROTECT, related_name='primary_entries')
        secondary = models.ForeignKey(Holder, on_delete=models.SET_NULL, null=True, related_name='secondary_entries')

        class Meta:
            app_label = 'forwardscalarref'

    with connection.schema_editor() as editor:
        for model in (Datum, Holder, Entry):
            editor.create_model(model)
    data = []
    for row in INPUTS:
        values = {prefix + kind: convert(kind, value, models)
                  for prefix, key in [('v_', 'required'), ('n_', 'nullable')] for kind, value in zip(KINDS, row[key])}
        data.append(Datum.objects.create(label=row['label'], **values))
    holder_inputs = [(0, 1), (1, None), (2, 0), (0, 2)]
    holders = [Holder.objects.create(primary=data[first], secondary=None if second is None else data[second])
               for first, second in holder_inputs]
    entry_inputs = [(0, None), (1, 0), (2, 1), (3, 2), (0, 3), (2, None)]
    for i, (first, second) in enumerate(entry_inputs):
        Entry.objects.create(label=f'e{i}', primary=holders[first], secondary=None if second is None else holders[second])
    routes = ['primary__primary', 'secondary__primary', 'primary__secondary', 'secondary__secondary']
    definitions = [('all', Q()), ('optional_a', Q(secondary__primary__label='A')),
                   ('not_optional_a', ~Q(secondary__primary__label='A')),
                   ('optional_or_root', Q(secondary__primary__label='A') | Q(label='e0')),
                   ('nested_or', Q(secondary__primary__n_integer=0) | Q(secondary__secondary__n_integer__isnull=True)),
                   ('empty', Q(primary__primary__label='A') & Q(primary__primary__label='B'))]
    observations = []
    for name, condition in definitions:
        for route in routes:
            for distinct in (False, True):
                for sliced in (False, True):
                    qs = Entry.objects.filter(condition).order_by('id').values_list('id', *[route + '__' + field for field in names])
                    if distinct:
                        qs = qs.distinct()
                    if sliced:
                        qs = qs[1:4]
                    count = qs.count()
                    rows = [[row[0], *[normalized(field[2:], value) for field, value in zip(names, row[1:])]] for row in qs]
                    observations.append({'name': name, 'route': route, 'distinct': distinct, 'sliced': sliced, 'count': count, 'rows': rows})
    bags = []
    json_nulls = []
    for route in routes:
        for field in names:
            qs = Entry.objects.order_by().values_list(route + '__' + field, flat=True).distinct()
            values = [normalized(field[2:], value) for value in qs]
            values.sort(key=lambda value: json.dumps(value, ensure_ascii=False, sort_keys=True))
            bags.append({'route': route, 'field': field, 'values': values})
        for field in ('v_json', 'n_json'):
            ids = list(Entry.objects.filter(**{route + '__' + field + '__isnull': True}).order_by('id').values_list('id', flat=True))
            json_nulls.append({'route': route, 'field': field, 'sql_null_or_absent': ids})
    orderings = []
    for route in routes:
        for field in names:
            for descending in (False, True):
                for distinct in (False, True):
                    for sliced in (False, True):
                        term = route + '__' + field
                        qs = Entry.objects.order_by(('-' if descending else '') + term, 'id')
                        if distinct:
                            qs = qs.distinct()
                        if sliced:
                            qs = qs[1:4]
                        count = qs.count()
                        rows = [row.id for row in qs]
                        selected = qs.values_list('id', term)
                        orderings.append({'route': route, 'field': field, 'descending': descending,
                                          'distinct': distinct, 'sliced': sliced, 'count': count,
                                          'rows': rows, 'projected': [row[0] for row in selected]})
    with connection.cursor() as cursor:
        cursor.execute('SELECT version()' if database else 'SELECT sqlite_version()')
        version = cursor.fetchone()[0]
    with connection.schema_editor() as editor:
        for model in (Entry, Holder, Datum):
            editor.delete_model(model)
    result = {'django': django.get_version(), 'python': platform.python_version(), 'backend': connection.vendor,
              'database_version': version, 'kinds': KINDS, 'fields': names, 'inputs': INPUTS,
              'holders': [{'primary': a, 'secondary': b} for a, b in holder_inputs],
              'entries': [{'label': f'e{i}', 'primary': a, 'secondary': b} for i, (a, b) in enumerate(entry_inputs)],
              'routes': routes, 'observations': observations, 'distinct': bags, 'json_nulls': json_nulls, 'orderings': orderings}
    if database:
        result['psycopg'] = importlib.metadata.version('psycopg')
    print(json.dumps(result, ensure_ascii=True, sort_keys=True, separators=(',', ':')))
    connection.close()
