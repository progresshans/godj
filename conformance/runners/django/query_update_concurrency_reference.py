"""Independent pinned Django/PostgreSQL update(F()) lock-wait observations.

Only synthetic input, native SQL, and server-confirmed blocking are used.
No Go source, expected fixture or Go output is loaded. Django: BSD-3-Clause.
"""
import hashlib
import inspect
import json
import os
from pathlib import Path
import platform
import queue
import re
import threading
import time

import django
import psycopg
from django.conf import settings

assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
assert psycopg.__version__ == '3.3.6'
database = os.environ['GODJ_QUERY_UPDATE_REFERENCE_DATABASE']
assert re.fullmatch(r'godj_query_update_reference_[0-9]+', database)
settings.configure(SECRET_KEY='synthetic-query-update-wait', INSTALLED_APPS=[], USE_TZ=True,
                   DEFAULT_AUTO_FIELD='django.db.models.BigAutoField', DATABASES={'default': {
                       'ENGINE': 'django.db.backends.postgresql', 'NAME': database,
                       'HOST': os.environ['PGHOST'], 'PORT': os.environ['PGPORT'],
                       'USER': os.environ['PGUSER'], 'PASSWORD': os.environ['PGPASSWORD'],
                       'OPTIONS': {'options': '-c statement_timeout=15000 -c lock_timeout=10000'},
                   }})
django.setup()

from django.db import connection, connections, models, transaction
from django.db.models import F
from django.db.models.query import QuerySet
from django.db.models.sql.compiler import SQLUpdateCompiler
from django.test.utils import CaptureQueriesContext


class Parent(models.Model):
    name = models.CharField(max_length=30)

    class Meta:
        app_label = 'query_update_wait'
        db_table = 'query_update_wait_parent'


class Item(models.Model):
    amount = models.BigIntegerField()
    note = models.CharField(max_length=30)
    parent = models.ForeignKey(Parent, on_delete=models.PROTECT)

    class Meta:
        app_label = 'query_update_wait'
        db_table = 'query_update_wait_item'


class Child(models.Model):
    item = models.ForeignKey(Item, on_delete=models.CASCADE)
    name = models.CharField(max_length=30)

    class Meta:
        app_label = 'query_update_wait'
        db_table = 'query_update_wait_child'


def observe(name):
    Child.objects.all().delete()
    Item.objects.all().delete()
    Item.objects.create(pk=1, amount=1, note='original', parent_id=1)
    started, result = queue.Queue(), queue.Queue()

    def worker():
        try:
            with connection.cursor() as cursor:
                cursor.execute('SELECT pg_backend_pid()')
                started.put(cursor.fetchone()[0])
            source = Item.objects.filter(amount=1)
            if name == 'root_still_matches':
                source = Item.objects.filter(amount__gte=1)
            elif name == 'root_foreign_key_changed':
                source = Item.objects.filter(parent_id=1)
            elif name in ('joined_root_changed', 'joined_foreign_key_changed'):
                source = source.filter(parent__name='allowed')
            elif name == 'trimmed_relation_root_changed':
                source = source.filter(parent__isnull=False)
            elif name == 'negated_collection_root_changed':
                source = source.filter(~models.Exists(Child.objects.filter(item_id=models.OuterRef('pk'), name='blocked')))
            with CaptureQueriesContext(connection) as capture:
                count = source.update(amount=F('amount') + 10)
            result.put({'count': count, 'sql': [entry['sql'] for entry in capture.captured_queries]})
        except BaseException as error:
            result.put({'error': type(error).__name__, 'message': str(error)})
        finally:
            connection.close()

    waiter = threading.Thread(target=worker)
    blocked = False
    try:
        with transaction.atomic():
            with connection.cursor() as cursor:
                cursor.execute('SELECT pg_backend_pid()')
                writer = cursor.fetchone()[0]
            source = Item.objects.filter(pk=1)
            if name in ('root_changed', 'root_still_matches', 'joined_root_changed',
                        'trimmed_relation_root_changed', 'negated_collection_root_changed'):
                source.update(amount=2)
            elif name in ('root_foreign_key_changed', 'joined_foreign_key_changed'):
                source.update(parent_id=2)
            elif name == 'root_deleted':
                source.delete()
            else:
                assert name == 'root_other_changed'
                source.update(note='concurrent')
            waiter.start()
            pid = started.get(timeout=5)
            until = time.monotonic() + 8
            while time.monotonic() < until:
                with connection.cursor() as cursor:
                    cursor.execute('SELECT %s = ANY(pg_blocking_pids(%s))', [writer, pid])
                    blocked = cursor.fetchone()[0]
                if blocked:
                    break
                time.sleep(0.01)
            if not blocked:
                raise RuntimeError('no server-confirmed lock wait')
    finally:
        if waiter.ident is not None:
            waiter.join(timeout=18)
            if waiter.is_alive():
                raise RuntimeError('waiter did not terminate')
    observed = result.get(timeout=1)
    if 'error' in observed:
        raise RuntimeError(observed)
    observed['blocked'] = blocked
    observed['stored'] = list(Item.objects.order_by('pk').values('id', 'amount', 'note', 'parent_id'))
    return observed


created = []
assert not connection.introspection.table_names()
try:
    with connection.schema_editor() as editor:
        for model in (Parent, Item, Child):
            editor.create_model(model)
            created.append(model)
    Parent.objects.bulk_create([Parent(pk=1, name='allowed'), Parent(pk=2, name='denied')])
    with connection.cursor() as cursor:
        cursor.execute('SHOW server_version')
        version = cursor.fetchone()[0]
    observations = {name: observe(name) for name in (
        'root_changed', 'root_still_matches', 'root_other_changed', 'root_foreign_key_changed',
        'joined_root_changed', 'joined_foreign_key_changed', 'root_deleted',
        'trimmed_relation_root_changed', 'negated_collection_root_changed',
    )}
    sources = {owner.__name__: hashlib.sha256(Path(inspect.getsourcefile(owner)).read_bytes()).hexdigest()
               for owner in (QuerySet, SQLUpdateCompiler)}
    print(json.dumps({'kind': 'django-query-update-concurrency-reference-v1', 'django': django.get_version(),
                      'python': platform.python_version(), 'postgres': version,
                      'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                      'source_sha256': sources, 'cases': observations}, sort_keys=True, separators=(',', ':')))
finally:
    with connection.schema_editor() as editor:
        for model in reversed(created):
            editor.delete_model(model)
    assert not connection.introspection.table_names()
    connections.close_all()
