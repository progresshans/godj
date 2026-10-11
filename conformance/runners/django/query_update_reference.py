"""Independent pinned Django QuerySet.update and scalar-expression observations.

Inputs are synthetic and every case owns fresh tables. No Go source, output,
or expected fixture is read. Django is BSD-3-Clause; see docs/SOURCES.md.
"""
import hashlib
import inspect
import json
import math
import os
from pathlib import Path
import platform
import re
import sqlite3
import sys
import tempfile
from decimal import Decimal


def observe():
    import django
    from django.apps import AppConfig
    from django.conf import settings
    assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
    assert not settings.configured
    owned = tempfile.TemporaryDirectory(prefix='godj-query-update-reference-')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3'),
                'OPTIONS': {'timeout': 10}}
    name = os.environ.get('GODJ_QUERY_UPDATE_REFERENCE_DATABASE')
    if name:
        assert re.fullmatch(r'godj_query_update_reference_[0-9]+', name)
        import psycopg
        assert psycopg.__version__ == '3.3.6'
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': name, 'HOST': os.environ['PGHOST'],
                    'PORT': os.environ['PGPORT'], 'USER': os.environ['PGUSER'], 'PASSWORD': os.environ['PGPASSWORD'],
                    'OPTIONS': {'options': '-c statement_timeout=30000 -c lock_timeout=20000'}}

    class ObserverConfig(AppConfig):
        name = __name__
        label = 'query_update_reference'
        path = owned.name

    setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
    settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                       SECRET_KEY='synthetic-query-update-reference', USE_TZ=True, TIME_ZONE='UTC', USE_I18N=False,
                       DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
    django.setup()
    from django.db import connection, connections, models, transaction
    from django.db.models import F, Value
    from django.db.models.expressions import CombinedExpression
    from django.db.models.query import QuerySet
    from django.db.models.sql.compiler import SQLUpdateCompiler
    from django.db.models.sql.subqueries import UpdateQuery
    from django.db.models.signals import pre_save, post_save
    from django.test.utils import CaptureQueriesContext

    callbacks = []

    class Group(models.Model):
        name = models.CharField(max_length=32)

        class Meta:
            app_label = 'query_update_reference'
            db_table = 'gdj_query_update_group'

    class Item(models.Model):
        name = models.CharField(max_length=32, unique=True)
        amount = models.BigIntegerField(default=0)
        other = models.BigIntegerField(default=0)
        rank = models.BigIntegerField(null=True)
        note = models.CharField(max_length=32, null=True)
        enabled = models.BooleanField(default=True)
        score = models.FloatField(default=0)
        price = models.DecimalField(max_digits=9, decimal_places=2, default=Decimal('0'))
        group = models.ForeignKey(Group, null=True, on_delete=models.CASCADE)
        peers = models.ManyToManyField('self', symmetrical=False)

        def save(self, *args, **kwargs):
            callbacks.append('save')
            return super().save(*args, **kwargs)

        def clean(self):
            callbacks.append('clean')
            return super().clean()

        class Meta:
            app_label = 'query_update_reference'
            db_table = 'gdj_query_update_item'
            constraints = [models.CheckConstraint(condition=models.Q(other__gte=-10), name='query_update_other_floor')]

    def saved(sender, **kwargs):
        callbacks.append('save-signal')

    pre_save.connect(saved, sender=Item, weak=False)
    post_save.connect(saved, sender=Item, weak=False)
    columns = ('id', 'name', 'amount', 'other', 'rank', 'note', 'enabled', 'score', 'price', 'group_id')

    def canonical(value):
        if isinstance(value, Decimal):
            return str(value)
        if isinstance(value, float) and not math.isfinite(value):
            return str(value)
        if isinstance(value, dict):
            return {key: canonical(entry) for key, entry in value.items()}
        if isinstance(value, (tuple, list)):
            return [canonical(entry) for entry in value]
        return value

    def rows(values=None):
        if values is None:
            values = Item.objects.order_by('id')
        return [canonical({column: getattr(value, column) for column in columns}) for value in values]

    def error_value(error):
        return {'error': type(error).__name__, 'sqlstate': getattr(error.__cause__, 'sqlstate', None)}

    def update(queryset=None, **assignments):
        try:
            source = queryset if queryset is not None else Item.objects
            return {'count': source.update(**assignments)}
        except Exception as error:
            return error_value(error)

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

    cases, tables = [], []

    def record(case_name, action, setup=None):
        assert case_name not in {case['case'] for case in cases}
        try:
            with connection.schema_editor() as editor:
                for model in (Group, Item):
                    editor.create_model(model)
                    tables.append(model)
            Group.objects.bulk_create([Group(id=1, name='first'), Group(id=2, name='second')])
            initial = Item.objects.bulk_create([
                Item(id=1, name='one', amount=7, other=2, rank=1, note='first', score=1.25, price=Decimal('1.25'), group_id=1),
                Item(id=2, name='two', amount=-7, other=3, rank=-1, note='', enabled=False, score=-1.25, price=Decimal('-1.25'), group_id=2),
                Item(id=3, name='three', amount=0, other=4),
            ])
            initial[0].peers.add(initial[1], initial[2])
            if setup:
                setup()
            callbacks.clear()
            with CaptureQueriesContext(connection) as captured:
                result = action()
            statements = [entry['sql'].strip() for entry in captured]
            try:
                state = {'rows': rows()}
            except Exception as error:
                state = {'read_error': error_value(error)}
            with connection.cursor() as cursor:
                if connection.vendor == 'sqlite':
                    cursor.execute('SELECT id, typeof(amount), typeof(other), typeof(rank), typeof(score), typeof(price) '
                                   'FROM gdj_query_update_item ORDER BY id')
                else:
                    cursor.execute('SELECT id, pg_typeof(amount)::text, pg_typeof(other)::text, pg_typeof(rank)::text, '
                                   'pg_typeof(score)::text, pg_typeof(price)::text FROM gdj_query_update_item ORDER BY id')
                storage = [list(row) for row in cursor.fetchall()]
                cursor.execute('SELECT id, amount, other, rank, score, price FROM gdj_query_update_item ORDER BY id')
                raw_numeric = canonical(cursor.fetchall())
            cases.append({'case': case_name, 'value': canonical(result), **state, 'storage_types': storage, 'raw_numeric': raw_numeric,
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
        result = None
        try:
            with transaction.atomic():
                result = update(amount=F('amount') + 1)
                if rollback:
                    raise ParentRollback()
        except ParentRollback:
            pass
        return result

    def failure_in_parent(savepoint):
        result = {}
        with transaction.atomic():
            result['before'] = update(Item.objects.filter(id=1), amount=99)
            if savepoint:
                try:
                    with transaction.atomic():
                        Item.objects.update(name='duplicate')
                except Exception as error:
                    result['child'] = error_value(error)
            else:
                result['child'] = update(name='duplicate')
            try:
                result['parent_count'] = Item.objects.count()
                result['after'] = update(Item.objects.filter(id=2), amount=101)
            except Exception as error:
                result['parent_error'] = type(error).__name__
        return result

    def cached(assignments):
        source = Item.objects.order_by('id')
        sibling = source.all()
        held = list(source)
        before, sibling_before = rows(held), rows(sibling)
        result = update(source, **assignments)
        cleared = source._result_cache is None
        return {'before': before, 'sibling_before': sibling_before, 'updated': result,
                'cache_cleared': cleared, 'after': rows(source), 'held': rows(held), 'sibling': rows(sibling)}

    def no_links():
        Item.peers.through.objects.all().delete()

    def invalid_number(**values):
        return lambda: Item.objects.filter(id=1).update(**values)

    assert not connection.introspection.table_names()
    try:
        record('empty_assignments', lambda: update())
        record('unknown_field', lambda: update(missing=1))
        record('many_to_many_field', lambda: update(peers=1))
        record('related_assignment', lambda: update(**{'group__name': 'new'}))
        record('pk_alias_assignment', lambda: update(Item.objects.filter(id=1), pk=41), no_links)
        record('primary_key_constant', lambda: update(Item.objects.filter(id=1), id=41), no_links)
        record('primary_key_expression', lambda: update(id=F('id') + 10), no_links)
        record('primary_key_collision', lambda: update(id=1), no_links)
        record('primary_key_referenced', lambda: update(Item.objects.filter(id=1), id=41))
        record('constant', lambda: update(amount=11, note=None))
        record('unchanged_count', lambda: update(amount=F('amount')))
        record('no_matches', lambda: update(Item.objects.filter(id=999), amount=11))
        record('empty_query', lambda: update(Item.objects.filter(id__in=[]), amount=11))
        record('empty_query_unknown_field', lambda: update(Item.objects.none(), missing=1))
        record('empty_query_invalid_value', lambda: update(Item.objects.none(), amount='not-integer'))
        record('query_filter', lambda: update(Item.objects.filter(amount__gt=0), amount=11))
        record('forward_filter', lambda: update(Item.objects.filter(group__name='first'), amount=11))
        record('many_to_many_filter', lambda: update(Item.objects.filter(peers__name__in=['two', 'three']), amount=11))
        record('boolean_filter', lambda: update(Item.objects.filter(models.Q(name='one') | models.Q(group__name='second')), amount=11))
        record('ordered_distinct', lambda: update(Item.objects.order_by('-name').distinct(), amount=11))
        record('distinct_fields', lambda: update(Item.objects.distinct('name'), amount=11))
        record('sliced', lambda: update(Item.objects.order_by('id')[:1], amount=11))
        record('sliced_empty_assignments', lambda: update(Item.objects.order_by('id')[:1]))
        record('union', lambda: update(Item.objects.filter(id=1).union(Item.objects.filter(id=2)), amount=11))
        record('row_lock_read_shape', lambda: update(Item.objects.select_for_update(nowait=True), amount=11))
        record('eager_prefetch_read_shape', lambda: update(Item.objects.select_related('group').prefetch_related('peers'), amount=11))
        record('projection_read_shape', lambda: update(Item.objects.values('name'), amount=11))
        record('original_row_swap', lambda: update(amount=F('other'), other=F('amount')))
        record('original_row_order', lambda: update(amount=F('amount') + 1, other=F('amount') + 5))
        record('original_row_reverse_order', lambda: update(other=F('amount') + 5, amount=F('amount') + 1))
        record('add', lambda: update(amount=F('amount') + 2))
        record('subtract', lambda: update(amount=F('amount') - 2))
        record('multiply', lambda: update(amount=F('amount') * 3))
        record('divide', lambda: update(amount=F('amount') / 2))
        record('remainder', lambda: update(amount=F('amount') % 2))
        record('negate', lambda: update(amount=-F('amount')))
        record('nested', lambda: update(amount=(F('amount') + F('other')) * 2 - 1))
        record('nullable_add', lambda: update(rank=F('rank') + 1))
        record('nullable_copy', lambda: update(rank=F('amount')))
        record('null_to_nonnullable', lambda: update(amount=F('rank')))
        record('literal_null', lambda: update(rank=None))
        record('null_expression', lambda: update(rank=F('rank') + Value(None)))
        record('field_string', lambda: update(note=F('name')))
        record('field_boolean', lambda: update(enabled=F('enabled')))
        record('field_fk', lambda: update(group=F('group_id')))
        record('set_fk_id', lambda: update(group_id=1))
        record('set_fk_null', lambda: update(group=None))
        record('missing_fk', lambda: update(group_id=999))
        record('joined_reference', lambda: update(note=F('group__name')))
        record('missing_reference', lambda: update(amount=F('missing')))
        record('aggregate_reference', lambda: update(amount=models.Max('amount')))
        record('failure_unique', lambda: update(name='same'))
        record('failure_check', lambda: update(amount=F('amount') + 1, other=F('amount') - 4))
        record('failure_not_null', lambda: update(name=None))
        record('cached_success', lambda: cached({'amount': F('amount') + 1}))
        record('cached_empty_assignments', lambda: cached({}))
        record('cached_failure', lambda: cached({'name': 'same'}))
        record('parent_commit', lambda: parent(False))
        record('parent_rollback', lambda: parent(True))
        record('borrowed_failure', lambda: failure_in_parent(False))
        record('savepoint_failure', lambda: failure_in_parent(True))
        for case_name, amount, expression in (
                ('overflow_add', 9223372036854775807, F('amount') + 1),
                ('overflow_subtract', -9223372036854775808, F('amount') - 1),
                ('overflow_multiply', 9223372036854775807, F('amount') * 2),
                ('overflow_negate', -9223372036854775808, -F('amount')),
                ('overflow_divide', -9223372036854775808, F('amount') / -1),
                ('minimum_remainder', -9223372036854775808, F('amount') % -1),
                ('max_exact_add', 9223372036854775806, F('amount') + 1),
                ('min_exact_subtract', -9223372036854775807, F('amount') - 1)):
            record(case_name, lambda expression=expression: update(Item.objects.filter(id=1), amount=expression),
                   invalid_number(amount=amount))
        record('divide_by_zero', lambda: update(amount=F('amount') / 0))
        record('nullable_divide_by_zero', lambda: update(rank=F('rank') / 0))
        record('remainder_by_zero', lambda: update(rank=F('rank') % 0))
        record('float_add', lambda: update(score=F('score') + 0.5))
        record('float_divide', lambda: update(score=F('score') / 2.0))
        record('float_divide_by_zero', lambda: update(score=F('score') / 0.0))
        record('float_to_integer', lambda: update(amount=F('score')))
        record('integer_mixed_float', lambda: update(amount=F('amount') + 0.5))
        record('decimal_add', lambda: update(price=F('price') + Decimal('0.25')))
        record('decimal_multiply', lambda: update(price=F('price') * Decimal('1.5')))
        record('decimal_divide_by_zero', lambda: update(price=F('price') / Decimal('0')))
        record('decimal_overflow', lambda: update(price=F('price') * Decimal('10000000')))
        assert all(not case['callbacks'] for case in cases)
        assert not connection.introspection.table_names()
        data = {'kind': 'django-query-update-reference-v1', 'django': django.get_version(), 'python': platform.python_version(),
                'backend': connection.vendor, 'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'source_sha256': {cls.__name__: hashlib.sha256(inspect.getsource(inspect.getmodule(cls)).encode()).hexdigest()
                                  for cls in (QuerySet, transaction.Atomic, SQLUpdateCompiler, UpdateQuery, CombinedExpression)},
                'sqlite': sqlite3.sqlite_version if connection.vendor == 'sqlite' else None, 'cases': cases}
        print(json.dumps(data, sort_keys=True, separators=(',', ':'), allow_nan=False))
    finally:
        assert not connection.introspection.table_names()
        connections.close_all()
        owned.cleanup()


if __name__ == '__main__':
    observe()
