"""Independent pinned Django decimal-expression observations (BSD-3-Clause).

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
        text = format(value, 'f')
        if '.' in text:
            text = text.rstrip('0').rstrip('.')
        return '0' if text in ('0', '-0') else text
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
    owned = tempfile.TemporaryDirectory(prefix='godj-decimal-reference-')
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
        namespace = 'godj_decimal_reference_' + uuid.uuid4().hex
        control.execute(sql.SQL('CREATE SCHEMA {}').format(sql.Identifier(namespace)))
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': unquote(endpoint.path.lstrip('/')),
                    'HOST': endpoint.hostname, 'PORT': endpoint.port or 5432,
                    'USER': unquote(endpoint.username or ''), 'PASSWORD': unquote(endpoint.password or ''),
                    'OPTIONS': {'sslmode': parse_qs(endpoint.query).get('sslmode', ['prefer'])[0],
                                'options': '-c search_path=' + namespace + ' -c statement_timeout=30000 -c lock_timeout=20000'}}

    class ObserverConfig(AppConfig):
        name = __name__
        label = 'decimal_expression_reference'
        path = owned.name

    setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
    result = None
    try:
        settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                           SECRET_KEY='synthetic-decimal-reference', USE_TZ=True, TIME_ZONE='UTC',
                           USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
        django.setup()
        from django.db import connection, connections, models, transaction
        from django.db.models import Avg, Count, Max, Min, Q, Sum
        from django.test.utils import CaptureQueriesContext

        class Rate(models.Model):
            code = models.CharField(max_length=24, unique=True)
            factor = models.DecimalField(max_digits=12, decimal_places=4, null=True)
            flat = models.DecimalField(max_digits=14, decimal_places=2, null=True)
            class Meta:
                app_label = 'decimal_expression_reference'
                db_table = 'gdj_decimal_rate'

        class Entry(models.Model):
            code = models.CharField(max_length=24, unique=True)
            bucket = models.CharField(max_length=24)
            enabled = models.BooleanField()
            amount = models.DecimalField(max_digits=14, decimal_places=2, null=True)
            balance = models.DecimalField(max_digits=14, decimal_places=2)
            fine = models.DecimalField(max_digits=20, decimal_places=6, null=True)
            wide = models.DecimalField(max_digits=1000, decimal_places=1000, null=True)
            rate = models.ForeignKey(Rate, on_delete=models.SET_NULL, null=True, related_name='entries')
            class Meta:
                app_label = 'decimal_expression_reference'
                db_table = 'gdj_decimal_entry'

        class Adjustment(models.Model):
            entry = models.OneToOneField(Entry, on_delete=models.CASCADE, related_name='adjustment')
            bonus = models.DecimalField(max_digits=14, decimal_places=2, null=True)
            class Meta:
                app_label = 'decimal_expression_reference'
                db_table = 'gdj_decimal_adjustment'

        owned_models = (Rate, Entry, Adjustment)
        assert not connection.introspection.table_names()
        with connection.schema_editor() as editor:
            for model in owned_models:
                editor.create_model(model)
        dec = lambda value: None if value is None else Decimal(value)
        rates = {row['code']: Rate.objects.create(code=row['code'], factor=dec(row['factor']), flat=dec(row['flat'])) for row in inputs['rates']}
        entries = {}
        for row in inputs['entries']:
            entries[row['code']] = Entry.objects.create(code=row['code'], bucket=row['bucket'], enabled=row['enabled'],
                amount=dec(row['amount']), balance=dec(row['balance']), fine=dec(row['fine']), wide=dec(row['wide']), rate=rates.get(row['rate']))
        for row in inputs['adjustments']:
            Adjustment.objects.create(entry=entries[row['entry']], bonus=dec(row['bonus']))

        def physical():
            # Capture every native column and its driver representation without
            # Django field conversion, which could round or reject an invalid row.
            snapshot = []
            with connection.cursor() as cursor:
                for model in owned_models:
                    cursor.execute('SELECT * FROM ' + connection.ops.quote_name(model._meta.db_table) + ' ORDER BY id')
                    snapshot.append([[(type(value).__name__, repr(value)) for value in row] for row in cursor.fetchall()])
            return snapshot

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
            case['storage_unchanged'] = physical() == before
            assert case['storage_unchanged'], name
            cases.append(case)

        from django.db.models import Case, F, Value, When
        base = Entry.objects.order_by('code')
        rows = lambda expression, source=base: list(source.annotate(value=expression).values('code', 'value'))
        cost = F('amount') * Decimal('1.25') + Decimal('0.5')
        summary = lambda source: source.aggregate(total=Sum(cost), average=Avg(cost), minimum=Min(cost), maximum=Max(cost), present=Count(cost))
        def update(**assignments):
            count = base.update(**assignments)
            return {'count': count, 'rows': list(base.values('code', 'amount', 'balance'))}

        action('add', lambda: rows(F('amount') + Decimal('0.25')))
        action('subtract', lambda: rows(F('amount') - F('balance')))
        action('multiply', lambda: rows(cost))
        action('negate', lambda: rows(-F('amount')))
        action('case_null', lambda: rows(Case(When(amount__isnull=True, then=Value(Decimal('7.5'))), default=cost)))
        action('related_arithmetic', lambda: rows(F('amount') * F('rate__factor') + F('rate__flat')))
        action('reverse_arithmetic', lambda: rows(F('amount') + F('adjustment__bonus')))
        action('comparison_rhs', lambda: list(base.annotate(left=F('amount')+Decimal('1'), right=F('balance')*Decimal('2')).filter(left__gt=F('right')).values_list('code', flat=True)))
        action('order_page', lambda: list(base.annotate(value=cost).filter(value__gte=Decimal('0')).order_by('-value','code').values('code','value')[1:3]))
        action('computed_aggregate', lambda: summary(base))
        action('distinct_filtered_aggregate', lambda: base.aggregate(total=Sum(cost,distinct=True,filter=Q(enabled=True)), average=Avg(cost,distinct=True,filter=Q(enabled=True))))
        action('groups_having', lambda: list(base.filter(enabled=True).order_by().values('bucket').annotate(total=Sum(cost), average=Avg(cost)).filter(total__gte=Decimal('1')).order_by('bucket')))
        action('empty_aggregate', lambda: summary(base.filter(pk__in=[])))
        action('all_null_aggregate', lambda: summary(base.filter(code='c')))
        action('derived_aggregate', lambda: base.annotate(value=cost).aggregate(total=Sum('value'), maximum=Max('value')))
        action('update_add', lambda: update(amount=F('amount') + Decimal('0.25')))
        action('update_old_row', lambda: update(amount=F('balance')+Decimal('1'), balance=F('balance')*Decimal('2')))
        action('update_null', lambda: update(amount=F('amount')+Value(None, output_field=models.DecimalField())))
        action('divide', lambda: rows(F('amount') / Decimal('3')))
        action('divide_integer', lambda: rows(Value(Decimal('100')) / Value(Decimal('3')), base.filter(code='a')))
        action('divide_native_scale', lambda: rows(F('wide') / Decimal('3'), base.filter(code='a')))
        action('remainder', lambda: rows(F('amount') % Decimal('0.3')))
        action('divide_zero', lambda: rows(F('amount') / Decimal('0'), base.filter(code='a')))
        action('remainder_zero', lambda: rows(F('amount') % Decimal('0'), base.filter(code='a')))
        action('large_literal', lambda: rows(Value(Decimal('9007199254740993')) + Value(Decimal('0.1')), base.filter(code='a')))
        action('update_rounding', lambda: update(amount=F('amount')*Decimal('1.5')))
        action('update_overflow', lambda: update(amount=F('amount')*Decimal('1000000000000')))
        action('case_unselected_zero', lambda: rows(Case(When(code='never', then=F('amount')/Decimal('0')), default=F('amount')+Decimal('1'))))
        square = F('wide') * F('wide')
        action('wide_product', lambda: rows((square * square) * F('wide'), base.filter(code='a')))
        result = {'kind': 'django-decimal-expression-reference-v1', 'backend': vendor, 'django_commit': sources['django_commit'],
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
    assert document['storage_unchanged'] and document['storage_cleaned'], 'decimal observer storage was changed or not cleaned'
    expected = {'update_overflow': ('InvalidOperation', None)} if args.vendor == 'sqlite' else {
        'divide_zero': ('DataError', '22012'), 'remainder_zero': ('DataError', '22012'), 'update_overflow': ('DataError', '22003')}
    actual = {case['name']: (case['error']['type'], case['error']['sqlstate']) for case in document['cases'] if 'error' in case}
    assert actual == expected, json.dumps(actual, ensure_ascii=False, sort_keys=True)
    print(json.dumps({'backend': args.vendor, 'cases': len(document['cases']), 'sha256': hashlib.sha256(args.output.read_bytes()).hexdigest()}))
