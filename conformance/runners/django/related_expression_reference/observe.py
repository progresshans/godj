"""Independent pinned Django relation-expression observations (BSD-3-Clause).

Reads synthetic input and pinned upstream module digests only. It never reads
GoDj results or expected-output fixtures. Every execution owns and removes its
SQLite file or PostgreSQL schema, and records its actual native runtime.
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
    owned = tempfile.TemporaryDirectory(prefix='godj-related-reference-')
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
        namespace = 'godj_related_reference_' + uuid.uuid4().hex
        control.execute(sql.SQL('CREATE SCHEMA {}').format(sql.Identifier(namespace)))
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': unquote(endpoint.path.lstrip('/')),
                    'HOST': endpoint.hostname, 'PORT': endpoint.port or 5432,
                    'USER': unquote(endpoint.username or ''), 'PASSWORD': unquote(endpoint.password or ''),
                    'OPTIONS': {'sslmode': parse_qs(endpoint.query).get('sslmode', ['prefer'])[0],
                                'options': '-c search_path=' + namespace + ' -c statement_timeout=30000 -c lock_timeout=20000'}}

    class ObserverConfig(AppConfig):
        name = __name__
        label = 'related_expression_reference'
        path = owned.name

    setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
    result = None
    try:
        settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                           SECRET_KEY='synthetic-related-reference', USE_TZ=True, TIME_ZONE='UTC',
                           USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
        django.setup()
        from django.db import connection, connections, models, transaction
        from django.db.models import Avg, Case, Count, F, Q, Sum, Value, When
        from django.db.models.lookups import Exact, LessThan
        from django.test.utils import CaptureQueriesContext

        class Group(models.Model):
            limit = models.BigIntegerField()
            class Meta:
                app_label = 'related_expression_reference'
                db_table = 'gdj_related_group'

        class Datum(models.Model):
            code = models.CharField(max_length=24, unique=True)
            amount = models.BigIntegerField(null=True)
            flag = models.BooleanField(default=False)
            score = models.FloatField(null=True)
            price = models.DecimalField(max_digits=14, decimal_places=2, null=True)
            elapsed = models.DurationField(null=True)
            group = models.ForeignKey(Group, on_delete=models.SET_NULL, null=True, related_name='data')
            class Meta:
                app_label = 'related_expression_reference'
                db_table = 'gdj_related_datum'

        class Measure(models.Model):
            code = models.CharField(max_length=24, unique=True)
            amount = models.BigIntegerField()
            other = models.BigIntegerField(null=True)
            enabled = models.BooleanField(default=False)
            datum = models.ForeignKey(Datum, on_delete=models.SET_NULL, null=True, related_name='measures')
            class Meta:
                app_label = 'related_expression_reference'
                db_table = 'gdj_related_measure'

        class Detail(models.Model):
            measure = models.OneToOneField(Measure, on_delete=models.CASCADE, related_name='detail')
            bonus = models.BigIntegerField()
            enabled = models.BooleanField(default=False)
            class Meta:
                app_label = 'related_expression_reference'
                db_table = 'gdj_related_detail'

        assert not connection.introspection.table_names()
        with connection.schema_editor() as editor:
            for model in (Group, Datum, Measure, Detail):
                editor.create_model(model)
        groups = {row['key']: Group.objects.create(limit=row['limit']) for row in inputs['groups']}
        data = {}
        for row in inputs['data']:
            data[row['key']] = Datum.objects.create(code=row['key'], amount=row['amount'], score=row['score'],
                price=None if row['price'] is None else Decimal(row['price']),
                elapsed=None if row['elapsed'] is None else timedelta(microseconds=row['elapsed']),
                flag=row['flag'], group=groups.get(row['group']))
        measures = {row['code']: Measure.objects.create(code=row['code'], amount=row['amount'], other=row['other'],
                    enabled=row['enabled'], datum=data.get(row['datum'])) for row in inputs['measures']}
        for row in inputs['details']:
            Detail.objects.create(measure=measures[row['measure']], bonus=row['bonus'], enabled=row['enabled'])

        def physical():
            return [list(model.objects.order_by('id').values()) for model in (Group, Datum, Measure, Detail)]

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

        base = Measure.objects.order_by('id')
        added = F('amount') + F('datum__amount')
        equal = Exact(F('amount') + Value(1), F('datum__amount') * Value(2))
        rows = lambda expression: list(base.annotate(value=expression).values('code', 'value'))
        codes = lambda condition: list(base.filter(condition).values_list('code', flat=True))
        action('forward_add', lambda: rows(added))
        action('forward_chain_add', lambda: rows(F('amount') + F('datum__group__limit')))
        action('rhs_exact', lambda: codes(equal))
        action('rhs_ordered', lambda: codes(LessThan(F('amount') + Value(1), F('datum__amount') * Value(2))))
        action('rhs_not', lambda: codes(~equal))
        action('related_both_sides', lambda: codes(LessThan(F('datum__amount'), F('detail__bonus'))))
        action('predicate_or_missing', lambda: codes(Q(equal) | Q(datum__isnull=True)))
        action('case_rhs_related', lambda: rows(Case(When(equal, then=Value('same')), default=Value('other'))))
        action('case_related_predicate', lambda: rows(Case(When(datum__flag=True, then=F('datum__amount')), default=F('amount'))))
        action('reverse_add', lambda: rows(F('amount') + F('detail__bonus')))
        action('mixed_route', lambda: rows(F('detail__measure__datum__amount')))
        action('related_group', lambda: list(base.annotate(key=F('datum__amount')).values('key').annotate(
            sum=Sum(added), avg=Avg(added), count=Count(added)).order_by(F('key').asc(nulls_first=True))))
        action('filtered_count', lambda: list(base.values('enabled').annotate(count=Count('id', filter=equal)).order_by('enabled')))
        action('order_slice', lambda: list(base.annotate(value=added).order_by(F('value').desc(nulls_last=True), 'id').values('code', 'value')[1:4]))
        action('distinct_computed', lambda: list(base.annotate(value=added).order_by(F('value').asc(nulls_first=True)).values_list('value', flat=True).distinct()))
        action('aggregate_computed', lambda: base.aggregate(sum=Sum(added), avg=Avg(added), count=Count(added)))
        action('related_codecs', lambda: list(base.annotate(price=F('datum__price'), elapsed=F('datum__elapsed'),
            score=F('datum__score'), flag=F('datum__flag')).values('code', 'price', 'elapsed', 'score', 'flag')))
        result = {'kind': 'django-related-expression-reference-v1', 'backend': vendor, 'django_commit': sources['django_commit'],
                  'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'input_sha256': hashlib.sha256(input_bytes).hexdigest(), 'source_verification_sha256': hashlib.sha256(source_bytes).hexdigest(),
                  'runtime': native, 'cases': cases, 'storage_unchanged': physical() == before, 'storage_cleaned': False}
        with connection.schema_editor() as editor:
            for model in (Detail, Measure, Datum, Group):
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
    assert document['storage_unchanged'] and document['storage_cleaned'] and all('error' not in case for case in document['cases'])
    print(json.dumps({'backend': args.vendor, 'cases': len(document['cases']), 'sha256': hashlib.sha256(args.output.read_bytes()).hexdigest()}))
