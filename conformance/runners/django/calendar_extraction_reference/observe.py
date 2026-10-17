"""Independent pinned Django calendar extraction observations (BSD-3-Clause).

Only synthetic inputs and upstream module identities are inputs. GoDj code,
expected results and product receipts are not read. Each case owns a rollback
boundary and records SQL, native values, conversion errors and physical storage.
"""
import argparse
from datetime import date, datetime, time as wall_time, timedelta, timezone
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
    if isinstance(value, date):
        return value.isoformat()
    if isinstance(value, wall_time):
        assert value.tzinfo is None, 'converted Time acquired an offset'
        return value.isoformat(timespec='microseconds' if value.microsecond else 'seconds')
    if isinstance(value, dict):
        return {key: canonical(item) for key, item in value.items()}
    if isinstance(value, (tuple, list)):
        return [canonical(item) for item in value]
    raise TypeError('unsupported converted type: ' + type(value).__name__)


def native(value):
    record = {'type': type(value).__name__}
    if isinstance(value, datetime):
        record.update(value=value.isoformat(timespec='microseconds'), aware=value.tzinfo is not None)
    elif isinstance(value, date):
        record['value'] = value.isoformat()
    elif isinstance(value, wall_time):
        record.update(value=value.isoformat(timespec='microseconds'), aware=value.tzinfo is not None)
    elif isinstance(value, timedelta):
        record.update(days=value.days, seconds=value.seconds, microseconds=value.microseconds)
    elif isinstance(value, Decimal):
        parts = value.as_tuple()
        record.update(value=str(value), sign=parts.sign, digits=list(parts.digits), exponent=parts.exponent)
    elif isinstance(value, float):
        record.update(value=repr(value), binary64=value.hex())
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
        assert len(installed) == identity['bytes'], name
        assert hashlib.sha256(installed).hexdigest() == identity['sha256'], name
    assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
    os.environ['TZ'] = 'UTC'
    time.tzset()
    locale.setlocale(locale.LC_ALL, 'C')
    input_bytes = (directory / 'input.json').read_bytes()
    inputs = json.loads(input_bytes)
    owned = tempfile.TemporaryDirectory(prefix='godj-calendar-reference-')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3')}
    runtime = {'python': platform.python_version(), 'django': django.get_version(), 'platform': platform.system(),
               'machine': platform.machine(), 'sqlite': sqlite3.sqlite_version,
               'locale': locale.setlocale(locale.LC_ALL), 'timezone': 'UTC'}
    control, namespace = None, None
    namespace_created = False
    result = None
    try:
        if vendor == 'postgres':
            import psycopg
            from psycopg import sql
            assert psycopg.__version__ == '3.3.6'
            endpoint = urlsplit(os.environ['GODJ_TEST_POSTGRES_URL'])
            assert endpoint.scheme in ('postgres', 'postgresql')
            control = psycopg.connect(os.environ['GODJ_TEST_POSTGRES_URL'], autocommit=True)
            runtime['postgresql'] = control.execute('SHOW server_version').fetchone()[0]
            assert int(control.execute('SHOW server_version_num').fetchone()[0]) == 170010
            runtime['psycopg'] = psycopg.__version__
            namespace = 'godj_calendar_reference_' + uuid.uuid4().hex
            control.execute(sql.SQL('CREATE SCHEMA {}').format(sql.Identifier(namespace)))
            namespace_created = True
            database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': unquote(endpoint.path.lstrip('/')),
                        'HOST': endpoint.hostname, 'PORT': endpoint.port or 5432,
                        'USER': unquote(endpoint.username or ''), 'PASSWORD': unquote(endpoint.password or ''),
                        'OPTIONS': {'sslmode': parse_qs(endpoint.query).get('sslmode', ['prefer'])[0],
                                    'options': '-c search_path=' + namespace + ' -c statement_timeout=30000 -c lock_timeout=20000'}}

        class ObserverConfig(AppConfig):
            name = __name__
            label = 'calendar_extraction_reference'
            path = owned.name

        setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
        settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                           SECRET_KEY='synthetic-calendar-reference', USE_TZ=True, TIME_ZONE='UTC',
                           USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
        django.setup()
        from django.db import connection, connections, models, transaction
        from django.db.models import Avg, Case, Count, F, Max, Min, Q, Sum, Value, When
        from django.db.models.functions import (
            ExtractDay, ExtractHour, ExtractIsoWeekDay, ExtractIsoYear, ExtractMinute,
            ExtractMonth, ExtractQuarter, ExtractSecond, ExtractWeek, ExtractWeekDay,
            ExtractYear, TruncDate, TruncTime,
        )

        class Schedule(models.Model):
            code = models.CharField(max_length=24, unique=True)
            anchor = models.DateTimeField(null=True)
            day = models.DateField(null=True)
            wall = models.TimeField(null=True)
            class Meta:
                app_label = 'calendar_extraction_reference'
                db_table = 'gdj_calendar_schedule'

        class Event(models.Model):
            code = models.CharField(max_length=24, unique=True)
            bucket = models.CharField(max_length=24)
            enabled = models.BooleanField()
            start = models.DateTimeField()
            at = models.DateTimeField(null=True)
            day = models.DateField(null=True)
            wall = models.TimeField(null=True)
            part = models.BigIntegerField(null=True)
            schedule = models.ForeignKey(Schedule, on_delete=models.SET_NULL, null=True, related_name='events')
            class Meta:
                app_label = 'calendar_extraction_reference'
                db_table = 'gdj_calendar_event'

        class Detail(models.Model):
            event = models.OneToOneField(Event, on_delete=models.CASCADE, related_name='detail')
            at = models.DateTimeField(null=True)
            day = models.DateField(null=True)
            wall = models.TimeField(null=True)
            class Meta:
                app_label = 'calendar_extraction_reference'
                db_table = 'gdj_calendar_detail'

        instant = lambda value: None if value is None else datetime.fromisoformat(value)
        day_value = lambda value: None if value is None else date.fromisoformat(value)
        clock_value = lambda value: None if value is None else wall_time.fromisoformat(value)
        owned_models = (Schedule, Event, Detail)
        assert not connection.introspection.table_names()
        with connection.schema_editor() as editor:
            for model in owned_models:
                editor.create_model(model)
        schedules = {row['code']: Schedule.objects.create(code=row['code'], anchor=instant(row['anchor']),
                     day=day_value(row['day']), wall=clock_value(row['wall'])) for row in inputs['schedules']}
        events = {}
        for row in inputs['events']:
            events[row['code']] = Event.objects.create(code=row['code'], bucket=row['bucket'], enabled=row['enabled'],
                start=instant(row['start']), at=instant(row['at']), day=day_value(row['day']), wall=clock_value(row['wall']),
                part=row['part'], schedule=schedules.get(row['schedule']))
        for row in inputs['details']:
            Detail.objects.create(event=events[row['event']], at=instant(row['at']),
                                  day=day_value(row['day']), wall=clock_value(row['wall']))

        def physical():
            snapshot = []
            with connection.cursor() as cursor:
                for model in owned_models:
                    expressions = []
                    for field in model._meta.local_concrete_fields:
                        column = connection.ops.quote_name(field.column)
                        if vendor == 'sqlite':
                            expressions.extend(['typeof(' + column + ')', 'quote(' + column + ')'])
                        else:
                            expressions.extend(['pg_typeof(' + column + ')::text', column + '::text'])
                    cursor.execute('SELECT ' + ','.join(expressions) + ' FROM ' + connection.ops.quote_name(model._meta.db_table) + ' ORDER BY id')
                    snapshot.append([list(row) for row in cursor.fetchall()])
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
                case['native_before'] = physical()
                try:
                    with transaction.atomic():
                        with connection.execute_wrapper(capture):
                            case['result'] = canonical(callback())
                        case['native_after'] = physical()
                        transaction.set_rollback(True)
                except Exception as error:
                    stage = 'sql' if any('error' in record for record, _ in statements) else 'conversion' if statements else 'expression'
                    case['error'] = failure(error, stage)
                if replay:
                    for record, parameters in statements:
                        if not record['sql'].lstrip().upper().startswith('SELECT'):
                            continue
                        stage = 'native_execute'
                        try:
                            with transaction.atomic(), connection.cursor() as cursor:
                                cursor.execute(record['sql'], parameters)
                                stage = 'native_fetch'
                                rows = cursor.fetchall()
                                transaction.set_rollback(True)
                        except Exception as error:
                            record['native_error'] = failure(error, stage)
                        else:
                            record['native_rows'] = [[native(value) for value in row] for row in rows]
                transaction.set_rollback(True)
            case['statements'] = [record for record, _ in statements]
            case['storage_unchanged'] = physical() == before
            assert case['storage_unchanged'], name
            cases.append(case)

        base = Event.objects.order_by('code')

        def selected(source):
            active['case']['output_fields'] = {name: value.output_field.get_internal_type() for name, value in source.query.annotation_select.items()}
            return list(source)

        def scalar(expression, source=base):
            return selected(source.annotate(value=expression).values('code', 'value'))

        def aggregated(source, **expressions):
            active['case']['output_fields'] = {
                name: expression.resolve_expression(source.query, summarize=True).output_field.get_internal_type()
                for name, expression in expressions.items()
            }
            return source.aggregate(**expressions)

        calendar_parts = [('year', ExtractYear), ('iso_year', ExtractIsoYear), ('quarter', ExtractQuarter),
                          ('month', ExtractMonth), ('week', ExtractWeek), ('week_day', ExtractWeekDay),
                          ('iso_week_day', ExtractIsoWeekDay), ('day', ExtractDay)]
        clock_parts = [('hour', ExtractHour), ('minute', ExtractMinute), ('second', ExtractSecond)]
        action('datetime_date', lambda: scalar(TruncDate('at', tzinfo=timezone.utc)))
        action('datetime_time', lambda: scalar(TruncTime('at', tzinfo=timezone.utc)))
        for name, function in calendar_parts + clock_parts:
            action('datetime_' + name, lambda function=function: scalar(function('at', tzinfo=timezone.utc)))
        for name, function in calendar_parts:
            action('date_' + name, lambda function=function: scalar(function('day')))
        for name, function in clock_parts:
            action('time_' + name, lambda function=function: scalar(function('wall')))
        action('composed_date_year', lambda: scalar(ExtractYear(TruncDate('at', tzinfo=timezone.utc))))
        action('composed_time_second', lambda: scalar(ExtractSecond(TruncTime('at', tzinfo=timezone.utc))))
        action('literal_components', lambda: selected(base.annotate(
            year=ExtractYear(Value(date(2021, 1, 1))),
            iso_year=ExtractIsoYear(Value(date(2021, 1, 1))),
            clock=TruncTime(Value(datetime(9999, 12, 31, 23, 59, 59, 1, tzinfo=timezone.utc)), tzinfo=timezone.utc),
        ).values('code', 'year', 'iso_year', 'clock')))
        action('filter_year', lambda: selected(base.annotate(value=ExtractYear('at', tzinfo=timezone.utc)).filter(value__gte=2021).values('code', 'value')))
        action('filter_date_range', lambda: selected(base.annotate(value=TruncDate('at', tzinfo=timezone.utc)).filter(value__gte=date(2020, 12, 31), value__lte=date(2021, 1, 1)).values('code', 'value')))
        action('order_clock', lambda: selected(base.annotate(value=TruncTime('at', tzinfo=timezone.utc)).values('code', 'value').order_by(F('value').asc(nulls_last=True), 'code')))
        action('group_by_date', lambda: selected(Event.objects.values(value=TruncDate('at', tzinfo=timezone.utc)).annotate(
            total=Count('id'), opened=Count('id', filter=Q(enabled=True)),
            first=Min(TruncTime('at', tzinfo=timezone.utc)), last=Max(TruncTime('at', tzinfo=timezone.utc)),
        ).order_by(F('value').asc(nulls_last=True))))
        action('group_iso_week', lambda: selected(Event.objects.values(year=ExtractIsoYear('at', tzinfo=timezone.utc), week=ExtractWeek('at', tzinfo=timezone.utc)).annotate(total=Count('id')).order_by(F('year').asc(nulls_last=True), F('week').asc(nulls_last=True))))
        action('aggregate_parts', lambda: aggregated(Event.objects.all(), years=Sum(ExtractYear('day')), seconds=Avg(ExtractSecond('wall')),
               first=Min(TruncDate('at', tzinfo=timezone.utc)), last=Max(TruncTime('at', tzinfo=timezone.utc))))
        action('filtered_aggregate', lambda: aggregated(Event.objects.all(), years=Sum(ExtractYear('day'), filter=Q(enabled=True)),
               dates=Count(TruncDate('at', tzinfo=timezone.utc), distinct=True, filter=Q(enabled=True))))
        action('case_date', lambda: scalar(Case(When(at__isnull=True, then=Value(date(2000, 1, 1))), default=TruncDate('at', tzinfo=timezone.utc))))
        action('case_time', lambda: scalar(Case(When(at__isnull=True, then=Value(wall_time(0))), default=TruncTime('at', tzinfo=timezone.utc))))
        action('date_after_shift', lambda: scalar(TruncDate(F('at') + Value(timedelta(microseconds=1)), tzinfo=timezone.utc), base.exclude(code='h')))
        action('related_parts', lambda: selected(base.annotate(
            anchor=TruncDate('schedule__anchor', tzinfo=timezone.utc),
            schedule_year=ExtractIsoYear('schedule__day'), schedule_second=ExtractSecond('schedule__wall'),
            detail_date=TruncDate('detail__at', tzinfo=timezone.utc), detail_day=ExtractWeekDay('detail__day'),
        ).values('code', 'anchor', 'schedule_year', 'schedule_second', 'detail_date', 'detail_day')))
        action('collection_years', lambda: selected(Schedule.objects.values('code').annotate(
            years=Sum(ExtractYear('events__at', tzinfo=timezone.utc)),
            opened=Sum(ExtractYear('events__at', tzinfo=timezone.utc), filter=Q(events__enabled=True)),
            first=Min(TruncDate('events__at', tzinfo=timezone.utc)),
        ).order_by('code')))
        action('distinct_dates', lambda: selected(Event.objects.annotate(value=TruncDate('at', tzinfo=timezone.utc)).values('value').order_by(F('value').asc(nulls_last=True)).distinct()))
        action('literal_aggregate', lambda: aggregated(Event.objects.all(), years=Sum(ExtractYear(Value(date(2004, 2, 29))))))
        action('empty_aggregate', lambda: aggregated(Event.objects.filter(pk__in=[]), years=Sum(ExtractYear('day')), first=Min(TruncDate('at', tzinfo=timezone.utc)), count=Count('id')))
        action('empty_group', lambda: selected(Event.objects.filter(pk__in=[]).values(value=TruncDate('at', tzinfo=timezone.utc)).annotate(total=Count('id'))))

        def update_calendar():
            changed = Event.objects.update(day=TruncDate('at', tzinfo=timezone.utc), wall=TruncTime('at', tzinfo=timezone.utc), part=ExtractYear('day'))
            return {'changed': changed, 'rows': list(base.values('code', 'day', 'wall', 'part'))}

        def update_filtered():
            changed = Event.objects.annotate(year=ExtractYear('at', tzinfo=timezone.utc)).filter(year__gte=2021).update(part=ExtractSecond('wall'))
            return {'changed': changed, 'rows': list(base.values('code', 'part'))}

        action('update_calendar', update_calendar, replay=False)
        action('update_filtered', update_filtered, replay=False)
        action('invalid_date_hour', lambda: scalar(ExtractHour('day')))
        action('invalid_time_year', lambda: scalar(ExtractYear('wall')))
        action('invalid_duration_year', lambda: scalar(ExtractYear(Value(timedelta(days=1)))))

        def corrupt(column, expression):
            with connection.cursor() as cursor:
                cursor.execute('UPDATE ' + connection.ops.quote_name(Event._meta.db_table) + ' SET ' + connection.ops.quote_name(column) + '=' + expression + ' WHERE code=%s', ['a'])

        bad_date = "'not-a-date'" if vendor == 'sqlite' else "DATE '10000-01-01'"
        outside_date = "'0000-01-01'" if vendor == 'sqlite' else "DATE '0001-01-01 BC'"
        bad_instant = "'2026-02-30 00:00:00'" if vendor == 'sqlite' else "TIMESTAMPTZ '10000-01-01 00:00:00+00'"
        infinite = "'infinity'" if vendor == 'sqlite' else "TIMESTAMPTZ 'infinity'"
        bad_clock = "'24:00:00'" if vendor == 'sqlite' else "TIME '24:00:00'"
        action('invalid_date_storage', lambda: scalar(ExtractMonth('day')), setup=lambda: corrupt('day', bad_date))
        action('outside_date_storage', lambda: scalar(ExtractYear('day')), setup=lambda: corrupt('day', outside_date))
        action('invalid_datetime_storage', lambda: scalar(ExtractMonth('at', tzinfo=timezone.utc)), setup=lambda: corrupt('at', bad_instant))
        action('infinite_datetime_storage', lambda: scalar(ExtractYear('at', tzinfo=timezone.utc)), setup=lambda: corrupt('at', infinite))
        action('clock_24_storage', lambda: scalar(ExtractHour('wall')), setup=lambda: corrupt('wall', bad_clock))
        action('unselected_corrupt_date', lambda: scalar(Case(When(code='never', then=ExtractYear('day')), default=Value(0))), setup=lambda: corrupt('day', bad_date))
        action('filtered_corrupt_date', lambda: aggregated(Event.objects.all(), years=Sum(ExtractYear('day'), filter=~Q(code='a'))), setup=lambda: corrupt('day', bad_date))

        assert physical() == before
        with connection.schema_editor() as editor:
            for model in reversed(owned_models):
                editor.delete_model(model)
        assert not connection.introspection.table_names()
        connections.close_all()
        result = {'kind': 'django-calendar-extraction-reference-v1', 'backend': vendor,
                  'django_commit': sources['django_commit'], 'runtime': runtime,
                  'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'input_sha256': hashlib.sha256(input_bytes).hexdigest(),
                  'source_verification_sha256': hashlib.sha256(source_bytes).hexdigest(),
                  'cases': cases, 'storage_unchanged': True}
    finally:
        try:
            if 'django.db' in sys.modules:
                from django.db import connections
                connections.close_all()
            if control is not None:
                try:
                    if namespace_created:
                        control.execute(sql.SQL('DROP SCHEMA {} CASCADE').format(sql.Identifier(namespace)))
                        assert not control.execute('SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=%s)', (namespace,)).fetchone()[0]
                finally:
                    control.close()
        finally:
            owned.cleanup()
            assert not Path(owned.name).exists()
    assert result is not None
    result['storage_cleaned'] = True
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--vendor', required=True, choices=['sqlite', 'postgres'])
    parser.add_argument('--output', required=True, type=Path)
    arguments = parser.parse_args()
    document = observe(arguments.vendor)
    arguments.output.parent.mkdir(parents=True, exist_ok=True)
    with arguments.output.open('x', encoding='utf-8') as output:
        json.dump(document, output, ensure_ascii=False, indent=2, allow_nan=False)
        output.write('\n')
    names = [case['name'] for case in document['cases']]
    assert len(names) == len(set(names)) == 55
    assert document['storage_unchanged'] and document['storage_cleaned']
    allowed_errors = {'invalid_date_hour', 'invalid_time_year', 'invalid_duration_year', 'infinite_datetime_storage'}
    unexpected = {case['name']: case['error'] for case in document['cases'] if 'error' in case and case['name'] not in allowed_errors}
    assert not unexpected, json.dumps(unexpected, ensure_ascii=False, sort_keys=True)
    print(json.dumps({'backend': arguments.vendor, 'cases': len(names), 'sha256': hashlib.sha256(arguments.output.read_bytes()).hexdigest()}))
