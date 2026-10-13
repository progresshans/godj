"""Independent pinned Django collection-aggregate observations (BSD-3-Clause).

Only synthetic input and upstream module identities are read. GoDj source,
results and expected outputs are not inputs. Each invocation owns its database
namespace, rolls back each observation and verifies storage and cleanup.
"""
import argparse
from datetime import timedelta
from decimal import Decimal
import hashlib
import json
import locale
import os
from pathlib import Path
import platform
import sqlite3
import sys
import tempfile
import time
from urllib.parse import parse_qs, unquote, urlsplit
import uuid


def canonical(value):
    if value is None or isinstance(value, (str, bool, int, float)):
        return value
    if isinstance(value, Decimal):
        return format(value.normalize(), 'f')
    if isinstance(value, timedelta):
        return (value.days * 86400 + value.seconds) * 1000000 + value.microseconds
    if isinstance(value, dict):
        return {key: canonical(item) for key, item in value.items()}
    if isinstance(value, (tuple, list)):
        return [canonical(item) for item in value]
    raise TypeError('unsupported observed value: ' + type(value).__name__)


def observe(vendor):
    import django
    from django.apps import AppConfig
    from django.conf import settings

    directory = Path(__file__).resolve().parent
    source_bytes = (directory / 'source-verification.json').read_bytes()
    sources = json.loads(source_bytes)
    for name, identity in sources['sources'].items():
        installed = (Path(django.__file__).parent / name).read_bytes()
        assert len(installed) == identity['bytes']
        assert hashlib.sha256(installed).hexdigest() == identity['sha256'], name
    assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
    os.environ['TZ'] = 'UTC'
    time.tzset()
    locale.setlocale(locale.LC_ALL, 'C')
    input_bytes = (directory / 'input.json').read_bytes()
    inputs = json.loads(input_bytes)
    owned = tempfile.TemporaryDirectory(prefix='godj-collection-reference-')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3')}
    native = {'python': platform.python_version(), 'django': django.get_version(), 'platform': platform.system(),
              'machine': platform.machine(), 'sqlite': sqlite3.sqlite_version, 'locale': locale.setlocale(locale.LC_ALL), 'timezone': 'UTC'}
    control, namespace = None, None
    if vendor == 'postgres':
        import psycopg
        from psycopg import sql
        assert psycopg.__version__ == '3.3.6'
        raw = os.environ['GODJ_TEST_POSTGRES_URL']
        endpoint = urlsplit(raw)
        assert endpoint.scheme in ('postgres', 'postgresql')
        control = psycopg.connect(raw, autocommit=True)
        native['postgresql'] = control.execute('SHOW server_version').fetchone()[0]
        assert int(control.execute('SHOW server_version_num').fetchone()[0]) == 170010
        native['psycopg'] = psycopg.__version__
        namespace = 'godj_collection_reference_' + uuid.uuid4().hex
        control.execute(sql.SQL('CREATE SCHEMA {}').format(sql.Identifier(namespace)))
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': unquote(endpoint.path.lstrip('/')),
                    'HOST': endpoint.hostname, 'PORT': endpoint.port or 5432,
                    'USER': unquote(endpoint.username or ''), 'PASSWORD': unquote(endpoint.password or ''),
                    'OPTIONS': {'sslmode': parse_qs(endpoint.query).get('sslmode', ['prefer'])[0],
                                'options': '-c search_path=' + namespace + ' -c statement_timeout=30000 -c lock_timeout=20000'}}

    class ObserverConfig(AppConfig):
        name = __name__
        label = 'collection_aggregate_reference'
        path = owned.name

    setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
    result = None
    try:
        settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                           SECRET_KEY='synthetic-collection-reference', USE_TZ=True, TIME_ZONE='UTC',
                           USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
        django.setup()
        from django.db import connection, connections, models, transaction
        from django.db.models import Avg, Count, Max, Min, Q, Sum
        from django.test.utils import CaptureQueriesContext

        class Category(models.Model):
            code = models.CharField(max_length=24, unique=True)
            class Meta:
                app_label = 'collection_aggregate_reference'
                db_table = 'gdj_collection_category'

        class Label(models.Model):
            code = models.CharField(max_length=24, unique=True)
            category = models.ForeignKey(Category, on_delete=models.SET_NULL, null=True, related_name='labels')
            class Meta:
                app_label = 'collection_aggregate_reference'
                db_table = 'gdj_collection_label'

        class Ticket(models.Model):
            code = models.CharField(max_length=24, unique=True)
            amount = models.BigIntegerField(null=True)
            enabled = models.BooleanField(null=True)
            score = models.FloatField(null=True)
            price = models.DecimalField(max_digits=14, decimal_places=2, null=True)
            elapsed = models.DurationField(null=True)
            category = models.ForeignKey(Category, on_delete=models.SET_NULL, null=True, related_name='tickets')
            labels = models.ManyToManyField(Label, through='Link', related_name='tickets')
            class Meta:
                app_label = 'collection_aggregate_reference'
                db_table = 'gdj_collection_ticket'

        class Link(models.Model):
            ticket = models.ForeignKey(Ticket, on_delete=models.CASCADE, null=True, related_name='label_links')
            label = models.ForeignKey(Label, on_delete=models.CASCADE, null=True, related_name='ticket_links')
            class Meta:
                app_label = 'collection_aggregate_reference'
                db_table = 'gdj_collection_link'

        class Detail(models.Model):
            ticket = models.OneToOneField(Ticket, on_delete=models.CASCADE, related_name='detail')
            bonus = models.BigIntegerField(null=True)
            class Meta:
                app_label = 'collection_aggregate_reference'
                db_table = 'gdj_collection_detail'

        owned_models = (Category, Label, Ticket, Link, Detail)
        assert not connection.introspection.table_names()
        with connection.schema_editor() as editor:
            for model in owned_models:
                editor.create_model(model)
        categories = {code: Category.objects.create(code=code) for code in inputs['categories']}
        labels = {row['code']: Label.objects.create(code=row['code'], category=categories.get(row['category'])) for row in inputs['labels']}
        tickets = {}
        for row in inputs['tickets']:
            tickets[row['code']] = Ticket.objects.create(code=row['code'], amount=row['amount'], enabled=row['enabled'], score=row['score'],
                price=None if row['price'] is None else Decimal(row['price']),
                elapsed=None if row['elapsed'] is None else timedelta(microseconds=row['elapsed']), category=categories.get(row['category']))
        for row in inputs['links']:
            Link.objects.create(ticket=tickets.get(row['ticket']), label=labels.get(row['label']))
        for row in inputs['details']:
            Detail.objects.create(ticket=tickets[row['ticket']], bonus=row['bonus'])

        def physical():
            return [list(model.objects.order_by('id').values()) for model in owned_models]

        before = physical()
        cases = []

        def action(name, callback):
            case = {'name': name}
            with CaptureQueriesContext(connection) as queries:
                try:
                    with transaction.atomic():
                        case['result'] = canonical(callback())
                        transaction.set_rollback(True)
                except Exception as error:
                    case['error'] = {'type': type(error).__name__, 'message': str(error),
                                     'sqlstate': getattr(error.__cause__, 'sqlstate', None)}
            case['sql'] = [query['sql'] for query in queries]
            cases.append(case)

        base = Label.objects.order_by()
        grouped = lambda **values: base.values('code').annotate(**values).order_by('code')
        filtered_groups = lambda source: list(source.values('code').annotate(total=Count('tickets__id'), amount=Sum('tickets__amount'),
            average=Avg('tickets__amount'), opened=Count('tickets__id', filter=Q(tickets__enabled=True))).order_by('code'))
        action('count_and_distinct', lambda: list(grouped(total=Count('tickets__id'), unique=Count('tickets__id', distinct=True), present=Count('tickets__amount'))))
        action('numeric_aggregates', lambda: list(grouped(amount=Sum('tickets__amount'), average=Avg('tickets__amount'),
            cost=Sum('tickets__price'), mean_cost=Avg('tickets__price'), effort=Sum('tickets__score'), mean_effort=Avg('tickets__score'),
            elapsed=Sum('tickets__elapsed'), mean_elapsed=Avg('tickets__elapsed'))))
        action('min_max', lambda: list(grouped(minimum=Min('tickets__amount'), maximum=Max('tickets__amount'), first=Min('tickets__code'), last=Max('tickets__code'))))
        action('distinct_numeric', lambda: list(grouped(amount=Sum('tickets__amount', distinct=True), average=Avg('tickets__amount', distinct=True), cost=Sum('tickets__price', distinct=True))))
        action('conditional_counts', lambda: list(grouped(opened=Count('tickets__id', filter=Q(tickets__enabled=True)),
            scoped=Count('tickets__id', filter=Q(tickets__category__code='a')), amount=Sum('tickets__amount', filter=Q(tickets__enabled=True)))))
        action('negated_count', lambda: list(grouped(total=Count('tickets__id', filter=~Q(tickets__enabled=True)))))
        action('negated_root_count', lambda: list(grouped(total=Count('id', filter=~Q(tickets__enabled=True)))))
        action('nullable_or_condition', lambda: list(grouped(total=Count('tickets__id', filter=Q(tickets__amount__isnull=True) | Q(tickets__category__code='b')))))
        action('optional_ancestor', lambda: list(grouped(total=Count('category__tickets__id'), amount=Sum('category__tickets__amount'))))
        action('source_filter_before_group', lambda: filtered_groups(base.filter(tickets__enabled=True)))
        action('chained_filters', lambda: filtered_groups(base.filter(tickets__enabled=True).filter(tickets__amount__gt=0)))
        action('chained_filters_distinct_source', lambda: filtered_groups(base.filter(tickets__enabled=True).filter(tickets__amount__gt=0).distinct()))
        action('chained_filters_reversed', lambda: filtered_groups(base.filter(tickets__amount__gt=0).filter(tickets__enabled=True)))
        action('root_exclusion', lambda: filtered_groups(base.exclude(tickets__enabled=True)))
        action('root_exclusion_then_filter', lambda: filtered_groups(base.exclude(tickets__amount__gt=4).filter(tickets__enabled=True)))
        action('root_or_condition', lambda: filtered_groups(base.filter(Q(tickets__enabled=True) | Q(code='none'))))
        action('independent_collection_counts', lambda: list(Category.objects.values('code').annotate(label_count=Count('labels__id'), ticket_count=Count('tickets__id'), amount=Sum('tickets__amount')).order_by('code')))
        action('independent_distinct_aggregates', lambda: list(Category.objects.values('code').annotate(label_count=Count('labels__id', distinct=True), ticket_count=Count('tickets__id', distinct=True), amount=Sum('tickets__amount', distinct=True)).order_by('code')))
        action('nested_collections', lambda: list(grouped(total=Count('tickets__labels__id'), unique=Count('tickets__labels__id', distinct=True))))
        action('reverse_one_to_one_leaf', lambda: list(grouped(total=Count('tickets__detail__bonus'), amount=Sum('tickets__detail__bonus'))))
        action('source_key_isnull', lambda: list(grouped(total=Count('id', filter=Q(tickets__category__isnull=True)))))
        action('global_aggregates', lambda: base.aggregate(total=Count('tickets__id'), amount=Sum('tickets__amount'), average=Avg('tickets__amount'), minimum=Min('tickets__amount'), maximum=Max('tickets__amount')))
        action('empty_global', lambda: base.filter(pk__in=[]).aggregate(total=Count('tickets__id'), amount=Sum('tickets__amount'), average=Avg('tickets__amount')))
        action('empty_groups', lambda: filtered_groups(base.filter(code='missing')))
        action('having_zero', lambda: list(grouped(total=Count('tickets__id')).filter(total=0)))
        action('having_filtered', lambda: list(grouped(opened=Count('tickets__id', filter=Q(tickets__enabled=True))).filter(opened__gte=2)))
        action('duration_average_having', lambda: list(grouped(elapsed=Avg('tickets__elapsed')).filter(elapsed__gte=timedelta(microseconds=4))))
        action('order_slice', lambda: list(grouped(total=Count('tickets__id')).order_by('-total', 'code')[1:4]))
        action('page_past_end', lambda: {'total': grouped(total=Count('tickets__id')).count(), 'rows': list(grouped(total=Count('tickets__id'))[50:52])})
        action('derived_aggregate', lambda: base.annotate(total=Count('tickets__id')).aggregate(amount=Sum('total'), maximum=Max('total')))
        result = {'kind': 'django-collection-aggregate-reference-v1', 'backend': vendor, 'django_commit': sources['django_commit'],
                  'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'input_sha256': hashlib.sha256(input_bytes).hexdigest(), 'source_verification_sha256': hashlib.sha256(source_bytes).hexdigest(),
                  'runtime': native, 'cases': cases, 'storage_unchanged': physical() == before, 'storage_cleaned': False}
        with connection.schema_editor() as editor:
            for model in reversed(owned_models):
                editor.delete_model(model)
        assert not connection.introspection.table_names()
        connections.close_all()
    finally:
        if settings.configured:
            from django.db import connections
            connections.close_all()
        if control is not None:
            try:
                control.execute(sql.SQL('DROP SCHEMA {} CASCADE').format(sql.Identifier(namespace)))
                assert not control.execute('SELECT 1 FROM pg_namespace WHERE nspname = %s', (namespace,)).fetchone()
            finally:
                control.close()
        owned.cleanup()
    assert result is not None
    result['storage_cleaned'] = True
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--vendor', choices=('sqlite', 'postgres'), required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    document = observe(args.vendor)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(document, ensure_ascii=False, allow_nan=False, sort_keys=True, indent=2) + '\n')
    assert document['storage_unchanged'] and document['storage_cleaned'], 'collection observer storage was changed or not cleaned'
    failures = {case['name']: case['error'] for case in document['cases'] if 'error' in case}
    assert not failures, json.dumps(failures, ensure_ascii=False, sort_keys=True)
    print(json.dumps({'backend': args.vendor, 'cases': len(document['cases']), 'sha256': hashlib.sha256(args.output.read_bytes()).hexdigest()}))
