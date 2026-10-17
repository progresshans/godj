"""Independent pinned Django temporal-expression observations (BSD-3-Clause).

Inputs are synthetic rows and pinned upstream module identities. GoDj code,
expected values and execution results are not inputs. Each case owns a rollback
boundary; native storage and namespace cleanup are checked independently.
"""
import argparse
from datetime import datetime, timedelta, timezone
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
    if isinstance(value, datetime):
        assert value.tzinfo is not None, 'converted DateTime lost its offset'
        return value.astimezone(timezone.utc).isoformat(timespec='microseconds').replace('+00:00', 'Z')
    if isinstance(value, timedelta):
        from django.utils.duration import duration_string
        return duration_string(value)
    if isinstance(value, dict):
        return {key: canonical(item) for key, item in value.items()}
    if isinstance(value, (tuple, list)):
        return [canonical(item) for item in value]
    raise TypeError('unsupported converted type: ' + type(value).__name__)


def native(value):
    record = {'type': type(value).__name__}
    if isinstance(value, datetime):
        record.update(value=value.isoformat(timespec='microseconds'), aware=value.tzinfo is not None)
    elif isinstance(value, timedelta):
        record.update(days=value.days, seconds=value.seconds, microseconds=value.microseconds)
    elif isinstance(value, float):
        record.update(value=repr(value), binary64=value.hex())
    elif isinstance(value, Decimal):
        record['value'] = str(value)
    elif isinstance(value, (tuple, list)):
        record['value'] = [native(item) for item in value]
    elif isinstance(value, bytes):
        record['value'] = value.hex()
    elif value is None or isinstance(value, (str, bool, int)):
        record['value'] = value
    else:
        raise TypeError('unsupported native type: ' + type(value).__name__)
    return record


def failure(error, stage):
    return {'type': type(error).__name__, 'message': str(error), 'stage': stage,
            'sqlstate': getattr(error.__cause__, 'sqlstate', None)}


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
    owned = tempfile.TemporaryDirectory(prefix='godj-temporal-reference-')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3')}
    runtime = {'python': platform.python_version(), 'django': django.get_version(), 'platform': platform.system(),
               'machine': platform.machine(), 'sqlite': sqlite3.sqlite_version, 'locale': locale.setlocale(locale.LC_ALL), 'timezone': 'UTC'}
    control, namespace = None, None
    result = None
    namespace_created = False
    try:
        if vendor == 'postgres':
            import psycopg
            from psycopg import sql
            assert psycopg.__version__ == '3.3.6'
            raw = os.environ['GODJ_TEST_POSTGRES_URL']
            endpoint = urlsplit(raw)
            assert endpoint.scheme in ('postgres', 'postgresql')
            control = psycopg.connect(raw, autocommit=True)
            runtime['postgresql'] = control.execute('SHOW server_version').fetchone()[0]
            assert int(control.execute('SHOW server_version_num').fetchone()[0]) == 170010
            runtime['psycopg'] = psycopg.__version__
            namespace = 'godj_temporal_reference_' + uuid.uuid4().hex
            control.execute(sql.SQL('CREATE SCHEMA {}').format(sql.Identifier(namespace)))
            namespace_created = True
            database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': unquote(endpoint.path.lstrip('/')),
                        'HOST': endpoint.hostname, 'PORT': endpoint.port or 5432,
                        'USER': unquote(endpoint.username or ''), 'PASSWORD': unquote(endpoint.password or ''),
                        'OPTIONS': {'sslmode': parse_qs(endpoint.query).get('sslmode', ['prefer'])[0],
                                    'options': '-c search_path=' + namespace + ' -c statement_timeout=30000 -c lock_timeout=20000'}}

        class ObserverConfig(AppConfig):
            name = __name__
            label = 'temporal_expression_reference'
            path = owned.name

        setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
        settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                           SECRET_KEY='synthetic-temporal-reference', USE_TZ=True, TIME_ZONE='UTC',
                           USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
        django.setup()
        from django.db import connection, connections, models, transaction
        from django.db.models import Avg, Case, Count, ExpressionWrapper, F, Max, Min, Q, Sum, Value, When
        from django.utils.dateparse import parse_duration

        class Schedule(models.Model):
            code = models.CharField(max_length=24, unique=True)
            offset = models.DurationField(null=True)
            anchor = models.DateTimeField(null=True)
            class Meta:
                app_label = 'temporal_expression_reference'
                db_table = 'gdj_temporal_schedule'

        class Event(models.Model):
            code = models.CharField(max_length=24, unique=True)
            bucket = models.CharField(max_length=24)
            enabled = models.BooleanField()
            start = models.DateTimeField()
            finish = models.DateTimeField(null=True)
            span = models.DurationField(null=True)
            other = models.DurationField(null=True)
            schedule = models.ForeignKey(Schedule, on_delete=models.SET_NULL, null=True, related_name='events')
            class Meta:
                app_label = 'temporal_expression_reference'
                db_table = 'gdj_temporal_event'

        class Detail(models.Model):
            event = models.OneToOneField(Event, on_delete=models.CASCADE, related_name='detail')
            offset = models.DurationField(null=True)
            occurred = models.DateTimeField(null=True)
            class Meta:
                app_label = 'temporal_expression_reference'
                db_table = 'gdj_temporal_detail'

        instant = lambda value: None if value is None else datetime.fromisoformat(value)
        period = lambda value: None if value is None else parse_duration(value)
        owned_models = (Schedule, Event, Detail)
        assert not connection.introspection.table_names()
        with connection.schema_editor() as editor:
            for model in owned_models:
                editor.create_model(model)
        schedules = {row['code']: Schedule.objects.create(code=row['code'], offset=period(row['offset']), anchor=instant(row['anchor'])) for row in inputs['schedules']}
        events = {}
        for row in inputs['events']:
            events[row['code']] = Event.objects.create(code=row['code'], bucket=row['bucket'], enabled=row['enabled'],
                start=instant(row['start']), finish=instant(row['finish']), span=period(row['span']), other=period(row['other']), schedule=schedules.get(row['schedule']))
        for row in inputs['details']:
            Detail.objects.create(event=events[row['event']], offset=period(row['offset']), occurred=instant(row['occurred']))

        def physical():
            snapshot = []
            with connection.cursor() as cursor:
                for model in owned_models:
                    cursor.execute('SELECT * FROM ' + connection.ops.quote_name(model._meta.db_table) + ' ORDER BY id')
                    snapshot.append([[native(value) for value in row] for row in cursor.fetchall()])
            return snapshot

        before, cases, active = physical(), [], {}

        def action(name, callback, *, setup=None, replay=True):
            case, statements = {'name': name}, []
            active.clear()
            active['case'] = case

            def capture(execute, statement, parameters, many, context):
                if statement.lstrip().split(None, 1)[0].upper() not in ('SELECT', 'UPDATE', 'INSERT', 'DELETE', 'WITH'):
                    return execute(statement, parameters, many, context)
                assert not many, 'observation unexpectedly became executemany'
                record = {'sql': statement, 'parameters': [native(value) for value in (parameters or ())]}
                statements.append((record, tuple(parameters or ())))
                try:
                    output = execute(statement, parameters, many, context)
                    record['rowcount'] = context['cursor'].rowcount
                    return output
                except Exception as error:
                    record['error'] = failure(error, 'sql')
                    raise

            with transaction.atomic():
                if setup is not None:
                    setup()
                try:
                    with transaction.atomic():
                        with connection.execute_wrapper(capture):
                            case['result'] = canonical(callback())
                        # Capture the actual post-operation storage types before
                        # rollback, outside the query capture itself. In
                        # particular, SQLite INTEGER affinity can store a REAL
                        # after overflowing arithmetic in an UPDATE.
                        case['native_after'] = physical()
                        transaction.set_rollback(True)
                except Exception as error:
                    stage = 'sql' if any('error' in record for record, _ in statements) else 'conversion' if statements else 'expression'
                    case['error'] = failure(error, stage)
                # Replay read statements against the same prepared source in
                # their own rollback boundary, before Django result converters.
                # UPDATE callbacks are never replayed after their rollback.
                if replay:
                    for record, parameters in statements:
                        if not record['sql'].lstrip().upper().startswith('SELECT'):
                            continue
                        native_stage = 'native_execute'
                        try:
                            with transaction.atomic(), connection.cursor() as cursor:
                                cursor.execute(record['sql'], parameters)
                                native_stage = 'native_fetch'
                                raw_rows = cursor.fetchall()
                                transaction.set_rollback(True)
                        except Exception as error:
                            record['native_error'] = failure(error, native_stage)
                        else:
                            record['native_rows'] = [[native(value) for value in row] for row in raw_rows]
                transaction.set_rollback(True)
            case['statements'] = [record for record, _ in statements]
            case['storage_unchanged'] = physical() == before
            assert case['storage_unchanged'], name
            cases.append(case)

        base = Event.objects.order_by('code')

        def selected(source):
            active['case']['output_fields'] = {name: value.output_field.get_internal_type() for name, value in source.query.annotation_select.items()}
            return list(source)

        def rows(expression, source=base):
            return selected(source.annotate(value=expression).values('code', 'value'))

        def aggregate(source, **expressions):
            active['case']['output_fields'] = {name: expression.resolve_expression(source.query, summarize=True).output_field.get_internal_type() for name, expression in expressions.items()}
            return source.aggregate(**expressions)

        def update(**assignments):
            count = base.update(**assignments)
            return {'count': count, 'rows': list(base.values('code', 'start', 'finish', 'span', 'other'))}

        tick = timedelta(microseconds=1)
        zero = timedelta()
        first = datetime(1, 1, 1, tzinfo=timezone.utc)
        last = datetime(9999, 12, 31, 23, 59, 59, 999999, tzinfo=timezone.utc)
        far = datetime(9999, 1, 1, 0, 0, 0, 1, tzinfo=timezone.utc)
        difference = F('finish') - F('start')
        shifted = F('finish') + F('span')
        combined = F('span') + F('other')
        summary = lambda source: aggregate(source, total=Sum(difference), average=Avg(difference), minimum=Min(difference), maximum=Max(difference), present=Count(difference))
        action('datetime_add_duration', lambda: rows(shifted))
        action('duration_add_datetime', lambda: rows(F('span') + F('finish')))
        action('datetime_subtract_duration', lambda: rows(F('finish') - F('span')))
        action('datetime_difference', lambda: rows(difference))
        action('duration_add', lambda: rows(combined))
        action('duration_subtract', lambda: rows(F('span') - F('other')))
        action('duration_negate', lambda: rows(ExpressionWrapper(-F('span'), output_field=models.DurationField())))
        action('typed_null_duration', lambda: rows(F('finish') + Value(None, output_field=models.DurationField())))
        action('typed_null_datetime', lambda: rows(Value(None, output_field=models.DateTimeField()) + F('span')))
        action('null_difference', lambda: rows(F('finish') - Value(None, output_field=models.DateTimeField())))
        action('case_fallback', lambda: rows(Case(When(finish__isnull=True, then=F('start') + tick), default=shifted)))
        action('related_shift', lambda: rows(F('finish') + F('schedule__offset')))
        action('reverse_shift', lambda: rows(F('finish') + F('detail__offset')))
        action('compare_computed', lambda: selected(base.annotate(value=shifted, boundary=F('start')+tick).filter(value__gt=F('boundary')).values('code', 'value')))
        action('order_page', lambda: selected(base.annotate(value=shifted).filter(value__isnull=False).order_by('-value', 'code').values('code', 'value')[1:4]))
        action('computed_aggregate', lambda: summary(base))
        action('distinct_filtered_span', lambda: aggregate(base, total=Sum(combined, distinct=True, filter=Q(enabled=True)), average=Avg(combined, distinct=True, filter=Q(enabled=True))))
        action('groups_having', lambda: selected(base.order_by().values('bucket').annotate(total=Sum(difference), average=Avg(difference)).filter(total__gte=zero).order_by('bucket')))
        action('empty_aggregate', lambda: summary(base.filter(pk__in=[])))
        action('all_null_aggregate', lambda: summary(base.filter(code='c')))
        action('collection_duration', lambda: selected(Schedule.objects.annotate(total=Sum(F('events__finish')-F('events__start')), present=Count(F('events__span')+tick)).order_by('code').values('code', 'total', 'present')))
        action('collection_shift_min', lambda: selected(Schedule.objects.annotate(value=Min(F('events__finish')+F('events__span'))).order_by('code').values('code', 'value')))
        action('derived_duration', lambda: aggregate(base.annotate(value=combined), total=Sum('value'), maximum=Max('value')))
        action('update_shift', lambda: update(finish=shifted), replay=False)
        action('update_old_row', lambda: update(finish=shifted, span=difference), replay=False)
        action('update_null', lambda: update(finish=F('finish')+Value(None, output_field=models.DurationField())), replay=False)
        action('large_datetime_difference', lambda: rows(Value(last)-Value(first), base.filter(code='a')))
        action('negative_datetime_difference', lambda: rows(Value(first)-Value(last), base.filter(code='a')))
        action('datetime_upper_overflow', lambda: rows(Value(last)+tick, base.filter(code='a')))
        action('datetime_lower_overflow', lambda: rows(Value(first)-tick, base.filter(code='a')))
        action('duration_int64_overflow', lambda: rows(Value(timedelta(microseconds=2**63-1))+tick, base.filter(code='a')))
        action('duration_full_model_literal', lambda: rows(Value(timedelta.max), base.filter(code='a')))
        action('duration_model_overflow', lambda: rows(Value(timedelta.max)+tick, base.filter(code='a')))
        action('case_unselected_overflow', lambda: rows(Case(When(code='never', then=Value(last)+F('span')), default=F('finish'))))

        def mean_source():
            return base.filter(code__in=['a', 'b']).order_by().values('bucket').annotate(period=Avg('span'))

        def mean_setup(values):
            for code, value in zip(('a', 'b'), values, strict=True):
                Event.objects.filter(code=code).update(span=None if value is None else timedelta(microseconds=value))

        for name, values in [('mean_fractional_shift', (1, 2)), ('mean_integral_shift', (1, 3)), ('mean_zero_shift', (-1, 1)), ('mean_null_shift', (None, None))]:
            action(name, lambda: selected(mean_source().annotate(value=Value(far)+F('period')).values('bucket', 'period', 'value')), setup=lambda values=values: mean_setup(values))
        action('mean_reverse_shift', lambda: selected(mean_source().annotate(value=F('period')+Value(far)).values('bucket', 'period', 'value')), setup=lambda: mean_setup((1, 2)))
        action('mean_add_duration', lambda: selected(mean_source().annotate(value=F('period')+tick).values('bucket', 'value')), setup=lambda: mean_setup((1, 2)))
        action('mean_negate_duration', lambda: selected(mean_source().annotate(value=ExpressionWrapper(-F('period'), output_field=models.DurationField())).values('bucket', 'value')), setup=lambda: mean_setup((1, 2)))
        action('mean_reaggregate', lambda: aggregate(mean_source(), total=Sum('period'), average=Avg('period')), setup=lambda: mean_setup((1, 2)))
        action('update_datetime_overflow', lambda: update(finish=F('finish')+tick), setup=lambda: Event.objects.filter(code='f').update(finish=last), replay=False)
        action('update_duration_overflow', lambda: update(span=F('span')+tick), setup=lambda: Event.objects.filter(code='f').update(span=timedelta(microseconds=2**63-1)), replay=False)

        # Django requires explicit output metadata for these mixed units. The
        # wrapper declares the result type; it does not replace native numeric
        # operators or copy GoDj's rational implementation into the observer.
        duration_expression = lambda expression: ExpressionWrapper(expression, output_field=models.DurationField())
        half = duration_expression(F('span') / 2)
        restored = duration_expression(duration_expression(F('span') / 3) * 3)
        action('scale_integer', lambda: rows(duration_expression(F('span') * 2)))
        action('scale_integer_reverse', lambda: rows(duration_expression(2 * F('span'))))
        action('scale_divide_restore', lambda: rows(restored))
        action('scale_half_final', lambda: rows(half))
        action('scale_float_tie', lambda: rows(duration_expression(Value(timedelta(microseconds=5)) * Value(0.1)), base.filter(code='a')))
        action('scale_decimal_tie', lambda: rows(duration_expression(Value(timedelta(microseconds=5)) * Value(Decimal('0.1'))), base.filter(code='a')))
        action('fraction_sum', lambda: aggregate(base.filter(code__in=['a', 'e']), total=Sum(half)))
        action('fraction_distinct_sum', lambda: aggregate(base.filter(code__in=['a', 'e']), total=Sum(half, distinct=True)))
        action('fraction_shift', lambda: rows(F('finish') + half, base.filter(code='a')))
        action('scale_zero_divisor', lambda: rows(duration_expression(F('span') / 0), base.filter(code='a')))
        action('duration_ratio', lambda: rows(ExpressionWrapper(F('span') / F('other'), output_field=models.FloatField())))

        result = {'kind': 'django-temporal-expression-reference-v1', 'backend': vendor, 'django_commit': sources['django_commit'],
                  'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'input_sha256': hashlib.sha256(input_bytes).hexdigest(), 'source_verification_sha256': hashlib.sha256(source_bytes).hexdigest(),
                  'runtime': runtime, 'cases': cases, 'storage_unchanged': physical() == before, 'storage_cleaned': False}
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
                if namespace_created:
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
    assert document['storage_unchanged'] and document['storage_cleaned'], 'temporal observer changed storage or failed cleanup'
    names = [case['name'] for case in document['cases']]
    assert len(names) == len(set(names)) == 55
    # These cases intentionally observe native range/precision differences.
    # Their exact result, error stage, SQLSTATE and native values remain in the
    # raw document and are checked by the separate consuming comparison.
    boundary = {'datetime_upper_overflow', 'datetime_lower_overflow', 'duration_full_model_literal', 'duration_model_overflow',
                'mean_fractional_shift', 'mean_integral_shift', 'mean_zero_shift', 'mean_null_shift', 'mean_reverse_shift',
                'update_datetime_overflow', 'update_duration_overflow', 'scale_zero_divisor', 'duration_ratio'}
    unexpected = {case['name']: case['error'] for case in document['cases'] if 'error' in case and case['name'] not in boundary}
    assert not unexpected, json.dumps(unexpected, ensure_ascii=False, sort_keys=True)
    print(json.dumps({'backend': args.vendor, 'cases': len(names), 'sha256': hashlib.sha256(args.output.read_bytes()).hexdigest()}))
