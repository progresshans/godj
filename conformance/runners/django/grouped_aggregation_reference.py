"""Independent pinned Django grouping, aggregate filter and HAVING observations.

Only synthetic input and the locked Django implementation are read. Every case
owns its tables. No Go source, output, or expected fixture is consumed.
Django is BSD-3-Clause; see docs/SOURCES.md.
"""
import datetime
from decimal import Decimal
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
import uuid


def observe():
    import django
    from django.apps import AppConfig
    from django.conf import settings
    assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
    assert not settings.configured
    owned = tempfile.TemporaryDirectory(prefix='godj-grouped-reference-')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3')}
    name = os.environ.get('GODJ_GROUPED_REFERENCE_DATABASE')
    if name:
        assert re.fullmatch(r'godj_grouped_reference_[0-9]+', name)
        import psycopg
        assert psycopg.__version__ == '3.3.6'
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': name, 'HOST': os.environ['PGHOST'],
                    'PORT': os.environ['PGPORT'], 'USER': os.environ['PGUSER'], 'PASSWORD': os.environ['PGPASSWORD'],
                    'OPTIONS': {'options': '-c statement_timeout=30000 -c lock_timeout=20000'}}

    class ObserverConfig(AppConfig):
        name = __name__
        label = 'grouped_reference'
        path = owned.name

    setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
    settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                       SECRET_KEY='synthetic-grouped-reference', USE_TZ=True, TIME_ZONE='UTC', USE_I18N=False,
                       DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
    django.setup()
    from django.db import connection, connections, models, transaction
    from django.db.models import Count, F, Max, Min, Q
    from django.db.models.aggregates import Aggregate
    from django.db.models.query import QuerySet
    from django.db.models.sql.compiler import SQLCompiler
    from django.db.models.sql.query import Query
    from django.test.utils import CaptureQueriesContext

    class Group(models.Model):
        name = models.CharField(max_length=32)
        region = models.CharField(max_length=32, null=True)

        class Meta:
            app_label = 'grouped_reference'
            db_table = 'gdj_grouped_group'

    class Item(models.Model):
        name = models.CharField(max_length=32)
        amount = models.BigIntegerField()
        rank = models.BigIntegerField(null=True)
        note = models.CharField(max_length=32, null=True)
        enabled = models.BooleanField()
        score = models.FloatField()
        price = models.DecimalField(max_digits=9, decimal_places=2)
        identity = models.UUIDField()
        binary = models.BinaryField()
        date = models.DateField()
        time = models.TimeField()
        stamp = models.DateTimeField()
        duration = models.DurationField()
        document = models.JSONField()
        group = models.ForeignKey(Group, null=True, on_delete=models.CASCADE, related_name='items')
        peers = models.ManyToManyField('self', symmetrical=False)

        class Meta:
            app_label = 'grouped_reference'
            db_table = 'gdj_grouped_item'
            ordering = ['name']

    def canonical(value):
        if isinstance(value, Decimal):
            return str(value)
        if isinstance(value, (datetime.datetime, datetime.date, datetime.time)):
            return value.isoformat()
        if isinstance(value, datetime.timedelta):
            return (value.days * 86400 + value.seconds) * 1000000 + value.microseconds
        if isinstance(value, uuid.UUID):
            return str(value)
        if isinstance(value, (bytes, memoryview)):
            return bytes(value).hex()
        if isinstance(value, dict):
            return {key: canonical(entry) for key, entry in value.items()}
        if isinstance(value, (tuple, list)):
            return [canonical(entry) for entry in value]
        if isinstance(value, QuerySet):
            return canonical(list(value))
        return value

    def seed():
        Group.objects.bulk_create([Group(id=1, name='first', region=None), Group(id=2, name='second', region='east'),
                                   Group(id=3, name='empty', region=None)])
        inputs = [(None, None, True, 1), (None, 'first', False, 1), (0, 'same', True, 2),
                  (0, 'same', True, None), (1, '', False, 2), (1, '', True, None),
                  (7, 'later', True, 1), (None, None, False, None)]
        rows = []
        for index, (rank, note, enabled, group_id) in enumerate(inputs, 1):
            choice = index % 2
            rows.append(Item(id=index, name='item-' + str(index), amount=index * 10, rank=rank, note=note,
                             enabled=enabled, score=choice + .5, price=Decimal(choice) + Decimal('.25'),
                             identity=uuid.UUID(int=choice), binary=bytes([choice, 0, 255]),
                             date=datetime.date(2026, 1, choice + 1), time=datetime.time(12, 30, choice, 123456),
                             stamp=datetime.datetime(2026, 1, choice + 1, 12, 30, tzinfo=datetime.timezone.utc),
                             duration=datetime.timedelta(microseconds=choice * 1000000), document={'key': choice},
                             group_id=group_id))
        Item.objects.bulk_create(rows)
        Item.peers.through.objects.bulk_create([
            Item.peers.through(from_item_id=1, to_item_id=2), Item.peers.through(from_item_id=1, to_item_id=3),
            Item.peers.through(from_item_id=3, to_item_id=2)])

    cases = []

    def record(case, action):
        with connection.schema_editor() as editor:
            editor.create_model(Group)
            editor.create_model(Item)
        try:
            seed()
            result = {'case': case}
            with CaptureQueriesContext(connection) as captured:
                try:
                    result['result'] = canonical(action())
                except Exception as error:
                    result['error'] = type(error).__name__
                    result['sqlstate'] = getattr(error.__cause__, 'sqlstate', None)
            result['statements'] = [entry['sql'].split()[0] for entry in captured]
            result['rows_after'] = list(Item.objects.order_by('id').values_list('id', 'rank', 'enabled'))
            cases.append(result)
        finally:
            with connection.schema_editor() as editor:
                editor.delete_model(Item)
                editor.delete_model(Group)

    def grouped(source=None):
        if source is None:
            source = Item.objects.all()
        return source.values('rank').annotate(total=Count('*'), present=Count('note'), unique=Count('note', distinct=True),
                                             opened=Count('id', filter=Q(enabled=True)),
                                             minimum=Min('amount'), maximum=Max('amount')).order_by(F('rank').asc(nulls_last=True))

    def cached():
        query = grouped()
        sibling = query.all()
        before = list(query)
        Item.objects.filter(id=8).update(rank=9)
        with CaptureQueriesContext(connection) as capture:
            count = query.count()
            held = list(query)
        return {'before': before, 'cached': held, 'count': count, 'cached_reads': len(capture),
                'fresh': list(query.all()), 'sibling': list(sibling)}

    def group_count(query):
        count = query.count()
        return {'count': count, 'rows': list(query), 'cached_count': query.count()}

    def locked_atomic():
        with transaction.atomic():
            return list(grouped(Item.objects.select_for_update()))

    assert not connection.introspection.table_names()
    try:
        record('nullable_key', grouped)
        record('multi_key', lambda: Item.objects.values('rank', 'enabled').annotate(total=Count('*')).order_by(
            F('rank').asc(nulls_last=True), 'enabled'))
        record('key_order_asc_native', lambda: grouped().order_by('rank'))
        record('key_order_desc_native', lambda: grouped().order_by('-rank'))
        record('key_order_desc_null_first', lambda: grouped().order_by(F('rank').desc(nulls_first=True)))
        record('where_before_group', lambda: grouped(Item.objects.filter(enabled=True)))
        record('where_after_group', lambda: grouped().filter(enabled=True))
        record('having_total', lambda: grouped().filter(total__gte=3))
        record('having_opened', lambda: grouped().filter(opened__gte=2))
        record('having_or', lambda: grouped().filter(Q(opened__gte=2) | Q(total__gte=3)))
        record('having_not', lambda: grouped().exclude(opened__gte=2))
        record('having_key_not', lambda: grouped().exclude(rank=1))
        record('having_mixed_where_or', lambda: grouped().filter(Q(opened__gte=2) | Q(rank=7)))
        record('having_nullable_min', lambda: Item.objects.values('rank').annotate(minimum=Min('note')).filter(
            minimum__isnull=True).order_by(F('rank').asc(nulls_last=True)))
        record('having_nullable_filtered_min', lambda: Item.objects.values('rank').annotate(
            minimum=Min('amount', filter=Q(enabled=False))).filter(minimum__isnull=True).order_by(F('rank').asc(nulls_last=True)))
        record('having_nullable_not', lambda: Item.objects.values('rank').annotate(
            minimum=Min('amount', filter=Q(enabled=False))).exclude(minimum__gte=50).order_by(F('rank').asc(nulls_last=True)))
        record('having_nullable_note_not', lambda: Item.objects.values('rank').annotate(
            minimum=Min('note', filter=Q(enabled=False))).exclude(minimum__gte='a').order_by(F('rank').asc(nulls_last=True)))
        record('having_no_groups', lambda: grouped().filter(total__gt=99))
        record('empty_native', lambda: grouped(Item.objects.filter(id__gt=99)))
        record('empty_folded', lambda: grouped(Item.objects.none()))
        record('empty_slice', lambda: group_count(grouped()[:0]))
        record('page_groups', lambda: group_count(grouped()[1:3]))
        record('page_past_end', lambda: group_count(grouped()[10:12]))
        record('count_groups', lambda: group_count(grouped().filter(opened__gte=2)))
        record('order_aggregate', lambda: grouped().order_by('-opened', F('rank').asc(nulls_last=True)))
        record('source_ordering', lambda: grouped(Item.objects.order_by('amount')))
        record('retained_source_ordering', lambda: Item.objects.order_by('amount').values('rank').annotate(total=Count('*')))
        record('default_model_ordering', lambda: Item.objects.values('rank').annotate(total=Count('*')).order_by(
            F('rank').asc(nulls_last=True)))
        record('source_slice', lambda: grouped(Item.objects.order_by('id')[:3]))
        record('source_slice_inherited_ordering', lambda: Item.objects.order_by('id')[:3].values('rank').annotate(total=Count('*')))
        record('source_distinct', lambda: grouped(Item.objects.distinct()))
        record('distinct_fields', lambda: grouped(Item.objects.distinct('rank')))
        record('filtered_count_star', lambda: Item.objects.values('rank').annotate(opened=Count('*', filter=Q(enabled=True))))
        record('filtered_aggregates', lambda: Item.objects.values('rank').annotate(
            minimum=Min('amount', filter=Q(enabled=False)), maximum=Max('note', filter=Q(enabled=False)),
            total=Count('note', distinct=True, filter=Q(enabled=True))).order_by(F('rank').asc(nulls_last=True)))
        record('count_null_and_empty', lambda: Item.objects.values('note').annotate(total=Count('*'), present=Count('note')).order_by(
            F('note').asc(nulls_last=True)))
        record('forward_key', lambda: Item.objects.values('group__name').annotate(total=Count('*')).order_by(
            F('group__name').asc(nulls_last=True)))
        record('forward_nullable_key', lambda: Item.objects.values('group__region').annotate(total=Count('*')).order_by(
            F('group__region').asc(nulls_last=True)))
        record('forward_filter', lambda: grouped(Item.objects.filter(group__name='first')))
        record('forward_conditional_count', lambda: Item.objects.values('rank').annotate(
            total=Count('id', filter=Q(group__region='east'))).order_by(F('rank').asc(nulls_last=True)))
        record('forward_missing_conditional_count', lambda: Item.objects.values('rank').annotate(
            total=Count('id', filter=Q(group__isnull=True))).order_by(F('rank').asc(nulls_last=True)))
        record('conditional_negation', lambda: Item.objects.values('rank').annotate(
            total=Count('id', filter=~Q(note='same'))).order_by(F('rank').asc(nulls_last=True)))
        record('collection_filter', lambda: grouped(Item.objects.filter(peers__id__in=[2, 3])))
        record('collection_negation', lambda: grouped(Item.objects.exclude(peers__id=2)))
        record('collection_key', lambda: Item.objects.values('peers__rank').annotate(total=Count('*')).order_by(
            F('peers__rank').asc(nulls_last=True)))
        record('reverse_counts', lambda: Group.objects.values('name').annotate(total=Count('items'), unique=Count('items', distinct=True)).order_by('name'))
        record('eager_source', lambda: grouped(Item.objects.select_related('group').prefetch_related('peers')))
        record('row_locked_group', lambda: grouped(Item.objects.select_for_update()))
        record('row_locked_group_in_atomic', locked_atomic)
        record('cached_snapshot', cached)
        record('missing_key', lambda: Item.objects.values('missing').annotate(total=Count('*')))
        record('missing_aggregate_field', lambda: Item.objects.values('rank').annotate(total=Count('missing')))
        record('alias_conflict', lambda: Item.objects.values('rank').annotate(rank=Count('*')))
        record('missing_having_alias', lambda: grouped().filter(missing__gte=1))
        record('filter_after_slice', lambda: grouped()[:2].filter(total__gte=1))
        record('ordering_after_slice', lambda: grouped()[:2].order_by('-total'))
        record('aggregate_only_empty', lambda: Item.objects.none().aggregate(total=Count('*'), minimum=Min('amount')))
        for field in ('enabled', 'score', 'price', 'identity', 'binary', 'date', 'time', 'stamp', 'duration', 'document'):
            record('codec_key_' + field, lambda field=field: Item.objects.values(field).annotate(total=Count('*')).order_by(field))
        assert not connection.introspection.table_names()
        data = {'kind': 'django-grouped-aggregation-reference-v1', 'django': django.get_version(),
                'python': platform.python_version(), 'backend': connection.vendor,
                'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'source_sha256': {cls.__name__: hashlib.sha256(inspect.getsource(inspect.getmodule(cls)).encode()).hexdigest()
                                  for cls in (QuerySet, Query, SQLCompiler, Aggregate)},
                'sqlite': sqlite3.sqlite_version if connection.vendor == 'sqlite' else None, 'cases': cases}
        print(json.dumps(data, sort_keys=True, separators=(',', ':'), allow_nan=False))
    finally:
        assert not connection.introspection.table_names()
        connections.close_all()
        owned.cleanup()


if __name__ == '__main__':
    observe()
