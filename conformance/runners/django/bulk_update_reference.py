"""Independent pinned Django bulk-update observations from synthetic inputs.

No Go source, Go output or expected fixture is read. Django is BSD-3-Clause.
Each case owns fresh tables and sequences; only external results, errors,
queries and transaction boundaries are observed.
"""
import hashlib
import inspect
import json
import os
from pathlib import Path
import platform
import re
import sqlite3
import sys
import tempfile


def transaction_commands(queries):
    commands = []
    for entry in queries:
        sql = entry['sql'].strip()
        for prefix in ('ROLLBACK TO SAVEPOINT', 'RELEASE SAVEPOINT', 'SAVEPOINT'):
            if sql.startswith(prefix + ' '):
                commands.append(prefix)
                break
        else:
            if sql in ('BEGIN', 'COMMIT', 'ROLLBACK'):
                commands.append(sql)
    return commands


def observe():
    import django
    from django.apps import AppConfig
    from django.conf import settings
    assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
    assert not settings.configured
    owned = tempfile.TemporaryDirectory(prefix='godj-bulk-update-reference-')
    name = os.environ.get('GODJ_BULK_REFERENCE_DATABASE')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3'),
                'OPTIONS': {'timeout': 10}}
    if name:
        assert re.fullmatch(r'godj_bulk_reference_[0-9]+', name)
        import psycopg
        assert psycopg.__version__ == '3.3.6'
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': name, 'HOST': os.environ['PGHOST'],
                    'PORT': os.environ['PGPORT'], 'USER': os.environ['PGUSER'], 'PASSWORD': os.environ['PGPASSWORD'],
                    'OPTIONS': {'options': '-c statement_timeout=30000 -c lock_timeout=20000'}}

    class ObserverConfig(AppConfig):
        name = __name__
        label = 'bulk_update_reference'
        path = owned.name

    setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
    settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                       SECRET_KEY='synthetic-bulk-update-reference', USE_TZ=True, TIME_ZONE='UTC', USE_I18N=False,
                       DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
    django.setup()
    from django.db import connection, connections, models, transaction, IntegrityError
    from django.db.models.query import QuerySet
    from django.db.models.sql.compiler import SQLUpdateCompiler
    from django.db.models.signals import pre_save, post_save
    from django.db.transaction import TransactionManagementError
    from django.test.utils import CaptureQueriesContext

    callbacks = []

    class Group(models.Model):
        name = models.CharField(max_length=32)

        class Meta:
            app_label = 'bulk_update_reference'
            db_table = 'gdj_bulk_update_group'

    class Item(models.Model):
        name = models.CharField(max_length=32, unique=True)
        amount = models.IntegerField(default=0)
        note = models.CharField(max_length=32, null=True)
        group = models.ForeignKey(Group, null=True, on_delete=models.CASCADE)
        peers = models.ManyToManyField('self', symmetrical=False)

        def save(self, *args, **kwargs):
            callbacks.append('save')
            return super().save(*args, **kwargs)

        def clean(self):
            callbacks.append('clean')
            return super().clean()

        class Meta:
            app_label = 'bulk_update_reference'
            db_table = 'gdj_bulk_update_item'
            constraints = [models.CheckConstraint(condition=models.Q(amount__gte=0), name='bulk_update_nonnegative')]

    def saved(sender, **kwargs):
        callbacks.append('save-signal')

    pre_save.connect(saved, sender=Item, weak=False)
    post_save.connect(saved, sender=Item, weak=False)

    def objects(values):
        return [{'id': item.pk, 'name': item.name, 'amount': item.amount, 'note': item.note,
                 'group_id': item.group_id, 'adding': item._state.adding, 'db': item._state.db} for item in values]

    def update(values, fields=('amount',), queryset=None, **options):
        try:
            count = (queryset if queryset is not None else Item.objects).bulk_update(values, fields, **options)
            result = {'count': count}
        except Exception as error:
            result = {'error': type(error).__name__, 'sqlstate': getattr(error.__cause__, 'sqlstate', None)}
        return result | {'inputs': objects(values)}

    def values():
        return [Item(id=1, name='ignored-one', amount=11, note='ignored-note'),
                Item(id=2, name='ignored-two', amount=22)]

    cases = []
    tables = []

    def record(case_name, action, setup=None):
        try:
            with connection.schema_editor() as editor:
                for model in (Group, Item):
                    editor.create_model(model)
                    tables.append(model)
            Group.objects.bulk_create([Group(id=1, name='first'), Group(id=2, name='second')])
            initial = Item.objects.bulk_create([Item(id=1, name='one', amount=1, note='kept-one', group_id=1),
                                               Item(id=2, name='two', amount=2, note='kept-two', group_id=2),
                                               Item(id=3, name='three', amount=3)])
            initial[0].peers.add(initial[1], initial[2])
            if setup:
                setup()
            callbacks.clear()
            with CaptureQueriesContext(connection) as captured:
                result = action()
            statements = [entry['sql'].strip() for entry in captured]
            cases.append({'case': case_name, 'value': result,
                          'rows': list(Item.objects.order_by('id').values('id', 'name', 'amount', 'note', 'group_id')),
                          'callbacks': list(callbacks), 'transaction_commands': transaction_commands(captured),
                          'update_statements': sum(sql.startswith('UPDATE ') for sql in statements),
                          'select_statements': sum(sql.startswith('SELECT ') for sql in statements)})
        finally:
            with connection.schema_editor() as editor:
                for model in reversed(tables):
                    editor.delete_model(model)
            tables.clear()

    class ParentRollback(Exception):
        pass

    def parent(rollback):
        outcome = None
        try:
            with transaction.atomic():
                outcome = update(values(), batch_size=1)
                if rollback:
                    raise ParentRollback()
        except ParentRollback:
            pass
        return outcome

    def borrowed_failure():
        result = {}
        with transaction.atomic():
            result['child'] = update([Item(id=1, amount=12), Item(id=2, amount=-1)], batch_size=1)
            try:
                result['parent_count'] = Item.objects.count()
            except TransactionManagementError as error:
                result['parent_error'] = type(error).__name__
        return result

    def cached():
        source = Item.objects.order_by('id')
        before = objects(list(source))
        result = update(values(), queryset=source)
        return {'before': before, 'after': objects(list(source)), 'updated': result}

    def unsaved_related():
        item = Item(id=1, amount=11, group=Group(name='not saved'))
        return update([item], fields=['group'])

    assert not connection.introspection.table_names()
    try:
        record('empty', lambda: update([]))
        record('empty_missing_fields', lambda: update([], fields=[]))
        record('empty_unknown_field', lambda: update([], fields=['missing']))
        record('empty_primary_key', lambda: update([], fields=['id']))
        record('empty_invalid_batch', lambda: update([], batch_size=0))
        record('missing_primary_key', lambda: update([Item(name='unsaved', amount=11)]))
        record('primary_key_field', lambda: update(values(), fields=['id']))
        record('unknown_field', lambda: update(values(), fields=['missing']))
        record('many_to_many_field', lambda: update(values(), fields=['peers']))
        record('one_batch_selected_fields', lambda: update(values()))
        record('two_fields_and_null', lambda: update([Item(id=1, amount=15, note=None), Item(id=2, amount=0, note='')], fields=['amount', 'note']))
        record('batched', lambda: update(values(), batch_size=1))
        record('duplicate_field_names', lambda: update(values(), fields=['amount', 'amount']))
        record('duplicate_keys_one_batch', lambda: update([Item(id=1, amount=10), Item(id=1, amount=20)], batch_size=2))
        record('duplicate_keys_two_batches', lambda: update([Item(id=1, amount=10), Item(id=1, amount=20)], batch_size=1))
        record('missing_key_count', lambda: update([Item(id=1, amount=11), Item(id=999, amount=22)]))
        record('all_missing', lambda: update([Item(id=999, amount=22)]))
        record('unchanged_value_count', lambda: update([Item(id=1, amount=1), Item(id=2, amount=2)]))
        record('large_zero_negative_keys', lambda: update([Item(id=9007199254740993, amount=11), Item(id=0, amount=22), Item(id=-9, amount=33)], batch_size=2),
               lambda: Item.objects.bulk_create([Item(id=9007199254740993, name='large'), Item(id=0, name='zero'), Item(id=-9, name='negative')]))
        record('query_filter', lambda: update(values(), queryset=Item.objects.filter(name='one')))
        record('forward_filter', lambda: update(values(), queryset=Item.objects.filter(group__name='first')))
        record('many_to_many_filter', lambda: update(values(), queryset=Item.objects.filter(peers__name__in=['two', 'three'])))
        record('boolean_filter', lambda: update(values(), queryset=Item.objects.filter(models.Q(name='one') | models.Q(group__name='second'))))
        record('empty_query_filter', lambda: update(values(), queryset=Item.objects.filter(id__in=[])))
        record('ordering_and_distinct', lambda: update(values(), queryset=Item.objects.order_by('-name').distinct()))
        record('sliced', lambda: update(values(), queryset=Item.objects.order_by('id')[:1]))
        record('sliced_empty_input', lambda: update([], queryset=Item.objects.order_by('id')[:1]))
        record('row_lock_read_shape', lambda: update(values(), queryset=Item.objects.select_for_update(nowait=True)))
        record('eager_and_prefetch_read_shape', lambda: update(values(), queryset=Item.objects.select_related('group').prefetch_related('peers')))
        record('cached_query', cached)
        record('failure_later_unique', lambda: update([Item(id=1, name='changed'), Item(id=2, name='three')], fields=['name'], batch_size=1))
        record('failure_later_check', lambda: update([Item(id=1, amount=11), Item(id=2, amount=-1)], batch_size=1))
        record('failure_later_not_null', lambda: update([Item(id=1, name='changed'), Item(id=2, name=None)], fields=['name'], batch_size=1))
        record('unselected_invalid_value', lambda: update([Item(id=1, name=None, amount=11)]))
        record('selected_foreign_key', lambda: update([Item(id=1, group_id=None), Item(id=2, group_id=1)], fields=['group']))
        record('missing_foreign_key', lambda: update([Item(id=1, group_id=999)], fields=['group']))
        record('unsaved_related_object', unsaved_related)
        record('parent_commit', lambda: parent(False))
        record('parent_rollback', lambda: parent(True))
        record('borrowed_failure', borrowed_failure)
        assert len(cases) == 40
        assert all(not case['callbacks'] for case in cases)
        data = {'kind': 'django-bulk-update-reference-v1', 'django': django.get_version(), 'python': platform.python_version(),
                'backend': connection.vendor, 'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'source_sha256': {cls.__name__: hashlib.sha256(inspect.getsource(inspect.getmodule(cls)).encode()).hexdigest()
                                  for cls in (QuerySet, transaction.Atomic, SQLUpdateCompiler)},
                'sqlite': sqlite3.sqlite_version if connection.vendor == 'sqlite' else None, 'cases': cases}
        print(json.dumps(data, sort_keys=True, separators=(',', ':')))
    finally:
        assert not connection.introspection.table_names()
        connections.close_all()
        owned.cleanup()


if __name__ == '__main__':
    observe()
