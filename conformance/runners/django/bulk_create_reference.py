"""Pinned Django bulk-create observations from authored synthetic inputs.

No Go source, Go output or expected fixtures are read. Django is BSD-3-Clause;
upstream provenance and deliberate Go ownership differences are recorded in
docs/SOURCES.md and ADR-0088. Each case recreates its tables and native sequence.
"""
import hashlib
import inspect
import json
import os
from pathlib import Path
import platform
import re
import sqlite3
import tempfile


def transaction_commands(queries):
    result = []
    for entry in queries:
        statement = entry['sql'].strip()
        for prefix in ('ROLLBACK TO SAVEPOINT', 'RELEASE SAVEPOINT', 'SAVEPOINT'):
            if statement.startswith(prefix + ' '):
                result.append(prefix)
                break
        else:
            if statement in ('BEGIN', 'COMMIT', 'ROLLBACK'):
                result.append(statement)
    return result


def observe():
    import django
    from django.conf import settings
    assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
    assert not settings.configured
    owned = tempfile.TemporaryDirectory(prefix='godj-bulk-create-reference-')
    name = os.environ.get('GODJ_BULK_REFERENCE_DATABASE')
    database = {'ENGINE': 'django.db.backends.sqlite3',
                'NAME': str(Path(owned.name) / 'reference.sqlite3'), 'OPTIONS': {'timeout': 10}}
    if name:
        assert re.fullmatch(r'godj_bulk_reference_[0-9]+', name)
        import psycopg
        assert psycopg.__version__ == '3.3.6'
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': name,
                    'HOST': os.environ['PGHOST'], 'PORT': os.environ['PGPORT'],
                    'USER': os.environ['PGUSER'], 'PASSWORD': os.environ['PGPASSWORD'],
                    'OPTIONS': {'options': '-c statement_timeout=30000 -c lock_timeout=20000'}}
    settings.configure(INSTALLED_APPS=[], DATABASES={'default': database}, SECRET_KEY='synthetic-bulk-reference',
                       USE_TZ=True, TIME_ZONE='UTC', USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
    django.setup()
    from django.db import connection, connections, models, transaction, IntegrityError
    from django.db.models.query import QuerySet
    from django.db.models.sql.compiler import SQLInsertCompiler
    from django.db.models.signals import pre_save, post_save
    from django.db.transaction import TransactionManagementError
    from django.test.utils import CaptureQueriesContext

    calls = []

    class Group(models.Model):
        name = models.CharField(max_length=32)

        class Meta:
            app_label = 'bulk_reference'
            db_table = 'gdj_bulk_group'

    class Item(models.Model):
        name = models.CharField(max_length=32, unique=True)
        amount = models.IntegerField(default=0)
        note = models.CharField(max_length=32, null=True)
        group = models.ForeignKey(Group, null=True, on_delete=models.CASCADE)

        def save(self, *args, **kwargs):
            calls.append('save')
            return super().save(*args, **kwargs)

        def clean(self):
            calls.append('clean')
            return super().clean()

        class Meta:
            app_label = 'bulk_reference'
            db_table = 'gdj_bulk_item'
            constraints = [models.CheckConstraint(condition=models.Q(amount__gte=0), name='bulk_amount_nonnegative')]

    def signal(sender, **kwargs):
        calls.append('save-signal')

    pre_save.connect(signal, sender=Item, weak=False)
    post_save.connect(signal, sender=Item, weak=False)

    def objects(values):
        return [{'id': value.pk, 'name': value.name, 'amount': value.amount, 'note': value.note,
                 'group_id': value.group_id, 'adding': value._state.adding, 'db': value._state.db}
                for value in values]

    def create(values, queryset=None, **options):
        try:
            result = (queryset if queryset is not None else Item.objects).bulk_create(values, **options)
            outcome = {'returned': objects(result)}
        except Exception as error:
            outcome = {'error': type(error).__name__, 'sqlstate': getattr(error.__cause__, 'sqlstate', None)}
        return outcome | {'inputs': objects(values)}

    cases = []
    created_tables = []

    def record(case_name, action):
        with connection.schema_editor() as editor:
            for model in (Group, Item):
                editor.create_model(model)
                created_tables.append(model)
        try:
            group = Group.objects.create(name='existing group')
            calls.clear()
            with CaptureQueriesContext(connection) as captured:
                value = action(group)
            sql = [entry['sql'].strip() for entry in captured]
            cases.append({'case': case_name, 'value': value,
                          'rows': list(Item.objects.order_by('name').values('id', 'name', 'amount', 'note', 'group_id')),
                          'groups': list(Group.objects.order_by('id').values('id', 'name')),
                          'callbacks': list(calls), 'transaction_commands': transaction_commands(captured),
                          'insert_statements': sum(statement.startswith('INSERT ') for statement in sql),
                          'select_statements': sum(statement.startswith('SELECT ') for statement in sql)})
        finally:
            with connection.schema_editor() as editor:
                for model in reversed(created_tables):
                    editor.delete_model(model)
            created_tables.clear()

    def batch_values():
        return [Item(name='c'), Item(name='a', amount=2), Item(name='b', note='kept')]

    assert not connection.introspection.table_names()
    try:
        record('empty', lambda _: create([]))
        record('empty_invalid_conflicts', lambda _: create([], ignore_conflicts=True, update_conflicts=True))
        record('empty_invalid_batch', lambda _: create([], batch_size=0))
        record('one_batch', lambda _: create(batch_values()))
        record('batched', lambda _: create(batch_values(), batch_size=2))
        record('mixed_explicit_keys', lambda _: create([
            Item(name='auto-first'), Item(id=9007199254740993, name='large'), Item(id=0, name='zero'),
            Item(name='auto-last'), Item(id=-9, name='negative')], batch_size=2))
        record('failure_later_batch_unique', lambda _: create([Item(name='one'), Item(name='two'), Item(name='one')], batch_size=1))

        def ignore_unique(_):
            Item.objects.bulk_create([Item(name='existing', amount=4)])
            return create([Item(name='existing', amount=8), Item(name='new', amount=9)], ignore_conflicts=True)

        record('ignore_unique', ignore_unique)
        record('ignore_check', lambda _: create([Item(name='invalid', amount=-1), Item(name='valid')], ignore_conflicts=True))
        record('ignore_not_null', lambda _: create([Item(name=None), Item(name='valid')], ignore_conflicts=True))
        record('invalid_conflict_options', lambda _: create([Item(name='a')], ignore_conflicts=True, update_conflicts=True))
        record('update_missing_fields', lambda _: create([Item(name='a')], update_conflicts=True, unique_fields=['name']))
        record('update_missing_target', lambda _: create([Item(name='a')], update_conflicts=True, update_fields=['amount']))
        record('update_primary_key', lambda _: create([Item(name='a')], update_conflicts=True, update_fields=['id'], unique_fields=['name']))

        def update_conflicts(_):
            Item.objects.bulk_create([Item(name='existing', amount=4, note='retained')])
            return create([Item(name='existing', amount=8, note='ignored'), Item(name='new', amount=9)],
                          update_conflicts=True, update_fields=['amount'], unique_fields=['name'])

        record('update_conflicts', update_conflicts)
        for size, suffix in ((2, 'one_batch'), (1, 'two_batches')):
            record('duplicate_upsert_' + suffix, lambda _, size=size: create(
                [Item(name='same', amount=1), Item(name='same', amount=2)], batch_size=size,
                update_conflicts=True, update_fields=['amount'], unique_fields=['name']))
        record('existing_fk', lambda group: create([Item(name='child', group_id=group.pk)]))
        record('missing_fk', lambda _: create([Item(name='child', group_id=-999)]))

        class ParentRollback(Exception):
            pass

        def parent_rollback(_):
            returned = []
            inputs = [Item(name='rollback')]
            try:
                with transaction.atomic():
                    returned = Item.objects.bulk_create(inputs)
                    raise ParentRollback()
            except ParentRollback:
                pass
            return {'returned': objects(returned), 'inputs': objects(inputs)}

        record('parent_rollback', parent_rollback)

        def borrowed_failure(_):
            Item.objects.bulk_create([Item(name='existing')])
            failure = parent_error = None
            with transaction.atomic():
                Group.objects.create(name='before failed child')
                try:
                    Item.objects.bulk_create([Item(name='private'), Item(name='existing')], batch_size=1)
                except IntegrityError as error:
                    failure = type(error).__name__
                try:
                    Item.objects.count()
                except TransactionManagementError as error:
                    parent_error = type(error).__name__
            return {'error': failure, 'parent_error': parent_error}

        record('borrowed_failure', borrowed_failure)
        record('no_model_clean', lambda _: create([Item(name='')]))
        record('filtered_query_ignores_read_shape', lambda _: create(batch_values(),
               queryset=Item.objects.filter(name='outside').order_by('-name')[:0]))
        assert len(cases) == 23 and len({case['case'] for case in cases}) == len(cases)
        assert not connection.introspection.table_names()
        native_version = connection.connection.info.server_version if name else sqlite3.sqlite_version
        return {'kind': 'django-bulk-create-reference-v1', 'django': django.get_version(),
                'python': platform.python_version(), 'backend': connection.vendor, 'native_version': native_version,
                'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'source_sha256': {key: hashlib.sha256(Path(inspect.getsourcefile(value)).read_bytes()).hexdigest()
                                  for key, value in [('QuerySet', QuerySet), ('Atomic', transaction.Atomic), ('SQLInsertCompiler', SQLInsertCompiler)]},
                'cases': cases}
    finally:
        if created_tables:
            with connection.schema_editor() as editor:
                for model in reversed(created_tables):
                    editor.delete_model(model)
        connections.close_all()
        owned.cleanup()


if __name__ == '__main__':
    print(json.dumps(observe(), sort_keys=True, separators=(',', ':')))
