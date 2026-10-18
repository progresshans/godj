"""Pinned Django/ZoneInfo observations using an independent IANA source build.

No GoDj source, embedded data, expected results or product receipts are read.
The UTC observer's value/error encoders are shared, and their source is bound
separately. Each native case records actual SQL, replay, storage and rollback.
"""
import argparse
from datetime import date, datetime, time as wall_time, timezone
from decimal import Decimal
import hashlib
import importlib.util
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
import zoneinfo

DIRECTORY = Path(__file__).resolve().parent
SHARED = DIRECTORY.parent / 'calendar_extraction_reference'
_shared_spec = importlib.util.spec_from_file_location('calendar_reference_encoders', SHARED / 'observe.py')
_shared = importlib.util.module_from_spec(_shared_spec)
_shared_spec.loader.exec_module(_shared)
canonical, failure, native = _shared.canonical, _shared.failure, _shared.native


def digest(data):
    return hashlib.sha256(data).hexdigest()


_data_spec = importlib.util.spec_from_file_location('zoned_reference_data', DIRECTORY / 'prepare_data.py')
_data = importlib.util.module_from_spec(_data_spec)
_data_spec.loader.exec_module(_data)
verify_zone_data = _data.verify_files


def provider_observations(inputs, source):
    values = []
    for name in source['names']:
        zone = zoneinfo.ZoneInfo(name)
        rows = []
        for text in inputs['provider_instants']:
            instant = datetime.fromisoformat(text)
            try:
                local = instant.astimezone(zone)
            except OverflowError as error:
                rows.append({'error': type(error).__name__})
            else:
                offset = local.utcoffset()
                assert offset.microseconds == 0
                rows.append({'local': local.replace(tzinfo=None).isoformat(timespec='microseconds'),
                             'offset_seconds': offset.days * 86400 + offset.seconds})
        values.append({'name': name, 'values': rows})
    return values


def observe(vendor, data_directory):
    import django
    from django.apps import AppConfig
    from django.conf import settings

    source_bytes = (SHARED / 'source-verification.json').read_bytes()
    sources = json.loads(source_bytes)
    for name, identity in sources['sources'].items():
        installed = (Path(django.__file__).parent / name).read_bytes()
        assert len(installed) == identity['bytes'] and digest(installed) == identity['sha256'], name
    assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
    data_bytes = (DIRECTORY / 'data-source.json').read_bytes()
    data_source = json.loads(data_bytes)
    assert data_source['iana_version'] == '2025c'
    data_root = data_directory.resolve()
    receipt_bytes = (data_root / 'build.json').read_bytes()
    receipt = json.loads(receipt_bytes)
    prepare_hash = digest((DIRECTORY / 'prepare_data.py').read_bytes())
    assert receipt['kind'] == 'independent-iana-build-v1' and receipt['complete'] is True
    assert receipt['iana_version'] == '2025c' and receipt['build'] == data_source['build']
    assert receipt['data_source_sha256'] == digest(data_bytes) and receipt['prepare_sha256'] == prepare_hash
    assert receipt['archives'] == {name: value['sha256'] for name, value in data_source['archives'].items()}
    assert receipt['files'] == 598 and receipt['names_sha256'] == data_source['names_sha256']
    assert receipt['tzdata_zi_sha256'] == data_source['tzdata_zi']['sha256'] == digest((data_root / 'tzdata.zi').read_bytes())
    assert receipt['compiler'] and len(bytes.fromhex(receipt['zic_binary_sha256'])) == 32
    verify_zone_data(data_root / 'zoneinfo', data_source)
    zoneinfo.reset_tzpath([str(data_root / 'zoneinfo')])
    zoneinfo.ZoneInfo.clear_cache()
    assert zoneinfo.TZPATH == (str(data_root / 'zoneinfo'),)
    os.environ['TZ'] = 'UTC'
    time.tzset()
    locale.setlocale(locale.LC_ALL, 'C')
    input_bytes = (DIRECTORY / 'input.json').read_bytes()
    inputs = json.loads(input_bytes)
    assert inputs['kind'] == 'independent-zoned-calendar-inputs-v1'
    provider = provider_observations(inputs, data_source)
    owned = tempfile.TemporaryDirectory(prefix='godj-zoned-calendar-reference-')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3')}
    runtime = {'python': platform.python_version(), 'django': django.get_version(), 'platform': platform.system(),
               'machine': platform.machine(), 'sqlite': sqlite3.sqlite_version,
               'locale': locale.setlocale(locale.LC_ALL), 'timezone': 'UTC',
               'data_profile': 'backzone/zone.tab', 'iana': '2025c',
               'zoneinfo_module_sha256': digest(Path(zoneinfo.__file__).read_bytes())}
    control, namespace, result = None, None, None
    namespace_created = False
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
            namespace = 'godj_zoned_reference_' + uuid.uuid4().hex
            control.execute(sql.SQL('CREATE SCHEMA {}').format(sql.Identifier(namespace)))
            namespace_created = True
            database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': unquote(endpoint.path.lstrip('/')),
                        'HOST': endpoint.hostname, 'PORT': endpoint.port or 5432,
                        'USER': unquote(endpoint.username or ''), 'PASSWORD': unquote(endpoint.password or ''),
                        'OPTIONS': {'sslmode': parse_qs(endpoint.query).get('sslmode', ['prefer'])[0],
                                    'options': '-c search_path=' + namespace + ' -c statement_timeout=30000 -c lock_timeout=20000'}}

        class ObserverConfig(AppConfig):
            name = __name__
            label = 'zoned_calendar_reference'
            path = owned.name

        setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
        settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                           SECRET_KEY='synthetic-zoned-reference', USE_TZ=True, TIME_ZONE='UTC',
                           USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
        django.setup()
        from django.db import connection, connections, models, transaction
        from django.db.models import Case, Count, F, Max, Min, Q, Sum, Value, When
        from django.db.models.functions import (
            ExtractDay, ExtractHour, ExtractIsoWeekDay, ExtractIsoYear, ExtractMinute,
            ExtractMonth, ExtractQuarter, ExtractSecond, ExtractWeek, ExtractWeekDay,
            ExtractYear, TruncDate, TruncTime, TruncYear, TruncQuarter, TruncMonth,
            TruncWeek, TruncDay, TruncHour, TruncMinute, TruncSecond,
        )

        class Schedule(models.Model):
            code = models.CharField(max_length=24, unique=True)
            anchor = models.DateTimeField(null=True)
            day = models.DateField(null=True)
            wall = models.TimeField(null=True)
            class Meta:
                app_label = 'zoned_calendar_reference'
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
                app_label = 'zoned_calendar_reference'
                db_table = 'gdj_calendar_event'

        class Detail(models.Model):
            event = models.OneToOneField(Event, on_delete=models.CASCADE, related_name='detail')
            at = models.DateTimeField(null=True)
            day = models.DateField(null=True)
            wall = models.TimeField(null=True)
            class Meta:
                app_label = 'zoned_calendar_reference'
                db_table = 'gdj_calendar_detail'

        owned_models = (Schedule, Event, Detail)
        assert not connection.introspection.table_names()
        with connection.schema_editor() as editor:
            for model in owned_models:
                editor.create_model(model)

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
                    table = connection.ops.quote_name(model._meta.db_table)
                    # PostgreSQL must sort the underlying integer key, not the
                    # selected id::text column's output alias.
                    cursor.execute('SELECT ' + ','.join(expressions) + ' FROM ' + table + ' ORDER BY ' + table + '.id')
                    snapshot.append([list(row) for row in cursor.fetchall()])
            return snapshot

        def seed(instants):
            north = Schedule.objects.create(code='north', anchor=datetime.fromisoformat(instants[0]),
                                            day=date(2000, 1, 1), wall=wall_time(0))
            Schedule.objects.create(code='unused')
            for index, text in enumerate(instants):
                instant = None if text is None else datetime.fromisoformat(text)
                row = Event.objects.create(code=f'e{index:02}', bucket='first' if index % 2 == 0 else 'second',
                    enabled=index % 2 == 0, start=datetime(2024, 2, 29, tzinfo=timezone.utc), at=instant,
                    day=date(2000 + index, 1, 1), wall=wall_time(12, 34, 56, index), part=100 + index,
                    schedule=north if index % 3 != 2 else None)
                if index < 2:
                    Detail.objects.create(event=row, at=instant, day=date(2000, 1, 1), wall=wall_time(0))

        def clear():
            for model in reversed(owned_models):
                model.objects.all().delete()
            assert physical() == [[], [], []]

        active, cases, probes = {}, [], []
        functions = {
            'date': TruncDate, 'time': TruncTime, 'year': ExtractYear, 'iso_year': ExtractIsoYear,
            'quarter': ExtractQuarter, 'month': ExtractMonth, 'week': ExtractWeek, 'week_day': ExtractWeekDay,
            'iso_week_day': ExtractIsoWeekDay, 'day': ExtractDay, 'hour': ExtractHour, 'minute': ExtractMinute,
            'second': ExtractSecond, 'trunc_year': TruncYear, 'trunc_quarter': TruncQuarter,
            'trunc_month': TruncMonth, 'trunc_week': TruncWeek, 'trunc_day': TruncDay,
            'trunc_hour': TruncHour, 'trunc_minute': TruncMinute, 'trunc_second': TruncSecond,
        }
        assert inputs['operations'][:21] == list(functions)

        def selected(source):
            active['case']['output_fields'] = {name: value.output_field.get_internal_type() for name, value in source.query.annotation_select.items()}
            return list(source)

        def action(name, zone, callback, *, setup=None, replay=True, wall_result=False):
            before = physical()
            case, statements = {'name': name, 'zone': zone.key, 'output_fields': {}}, []
            active['case'] = case

            def capture(execute, statement, parameters, many, context):
                if statement.lstrip().split(None, 1)[0].upper() not in ('SELECT', 'UPDATE', 'INSERT', 'DELETE', 'WITH'):
                    return execute(statement, parameters, many, context)
                assert not many
                record = {'sql': statement, 'parameters': [native(value) for value in (parameters or ())]}
                statements.append((record, tuple(parameters or ())))
                try:
                    result = execute(statement, parameters, many, context)
                    record['rowcount'] = context['cursor'].rowcount
                    return result
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
                                record['columns'] = [column[0] for column in cursor.description]
                                stage = 'native_fetch'
                                rows = cursor.fetchall()
                                record['native_rows'] = [[native(value) for value in row] for row in rows]
                                if wall_result:
                                    stage = 'zoneinfo_conversion'
                                    conversions = []
                                    for code, value in rows:
                                        if value is None:
                                            conversions.append(None)
                                            continue
                                        wall = datetime.fromisoformat(value) if isinstance(value, str) else value
                                        assert wall.tzinfo is None
                                        aware = wall.replace(tzinfo=zone, fold=0)
                                        offset = aware.utcoffset()
                                        conversions.append({'wall': wall.isoformat(timespec='microseconds'),
                                            'offset_seconds': offset.days * 86400 + offset.seconds,
                                            'fold': aware.fold, 'instant': canonical(aware)})
                                    record['wall_conversions'] = conversions
                                transaction.set_rollback(True)
                        except Exception as error:
                            record['native_error'] = failure(error, stage)
                transaction.set_rollback(True)
            case['statements'] = [record for record, _ in statements]
            case['storage_unchanged'] = physical() == before
            assert case['storage_unchanged'], name
            cases.append(case)

        def probe_native_rules(fixture, zone):
            # The product does not depend on PostgreSQL's timezone database.
            # Django does: retain its real offsets and require agreement for
            # every ordinary source instant used by these query observations.
            for text in fixture['instants']:
                if text is None:
                    continue
                instant = datetime.fromisoformat(text)
                offset = instant.astimezone(zone).utcoffset()
                expected = offset.days * 86400 + offset.seconds
                record = {'fixture': fixture['name'], 'zone': zone.key, 'instant': canonical(instant),
                          'zoneinfo_offset_seconds': expected}
                if vendor == 'postgres':
                    with connection.cursor() as cursor:
                        cursor.execute("SELECT EXTRACT(EPOCH FROM (%s::timestamptz AT TIME ZONE %s) - (%s::timestamptz AT TIME ZONE 'UTC'))", [instant, zone.key, instant])
                        native_offset = cursor.fetchone()[0]
                    assert isinstance(native_offset, Decimal) and native_offset == int(native_offset)
                    record['postgres_offset_seconds'] = int(native_offset)
                probes.append(record)

        base = Event.objects.order_by('code')
        for fixture in inputs['fixtures']:
            zone = zoneinfo.ZoneInfo(fixture['zone'])
            seed(fixture['instants'])
            probe_native_rules(fixture, zone)
            prefix = fixture['name'] + '/'
            for name, function in functions.items():
                action(prefix + name, zone, lambda function=function: selected(base.annotate(value=function('at', tzinfo=zone)).values('code', 'value')),
                       wall_result=name.startswith('trunc_'))
            action(prefix + 'composed_date_year', zone, lambda: selected(base.annotate(value=ExtractYear(TruncDate('at', tzinfo=zone))).values('code', 'value')))
            action(prefix + 'filter_date', zone, lambda: selected(base.annotate(value=TruncDate('at', tzinfo=zone)).filter(value__gte=date(2024, 1, 1)).values('code', 'value')))
            action(prefix + 'group_date', zone, lambda: selected(Event.objects.values(value=TruncDate('at', tzinfo=zone)).annotate(
                total=Count('id'), opened=Count('id', filter=Q(enabled=True)), first=Min('at'), last=Max('at')).order_by(F('value').asc(nulls_last=True))))
            action(prefix + 'related_date', zone, lambda: selected(base.annotate(anchor=TruncDate('schedule__anchor', tzinfo=zone),
                detail_date=TruncDate('detail__at', tzinfo=zone)).values('code', 'anchor', 'detail_date')))
            action(prefix + 'collection_date', zone, lambda: selected(Schedule.objects.values('code').annotate(
                total=Count('events__id'), days=Count(TruncDate('events__at', tzinfo=zone), distinct=True),
                first=Min(TruncDate('events__at', tzinfo=zone)), last=Max('events__at')).order_by('code')))

            def update_calendar():
                changed = Event.objects.update(day=TruncDate('at', tzinfo=zone), wall=TruncTime('at', tzinfo=zone), part=ExtractYear('day'))
                return {'changed': changed, 'rows': list(base.values('code', 'at', 'day', 'wall', 'part'))}

            def update_truncated():
                changed = Event.objects.update(at=TruncHour('at', tzinfo=zone), part=ExtractYear('at', tzinfo=zone))
                return {'changed': changed, 'rows': list(base.values('code', 'at', 'day', 'wall', 'part'))}

            action(prefix + 'update_calendar', zone, update_calendar, replay=False)
            action(prefix + 'update_truncated', zone, update_truncated, replay=False)
            clear()

        for fixture in inputs['failures']:
            zone = zoneinfo.ZoneInfo(fixture['zone'])
            seed([fixture['at'], '2024-06-01T00:00:00Z', None])

            def corrupt():
                expression = "'2026-02-30 00:00:00'" if vendor == 'sqlite' else "TIMESTAMPTZ '10000-01-01 00:00:00+00'"
                with connection.cursor() as cursor:
                    cursor.execute('UPDATE gdj_calendar_event SET at=' + expression + ' WHERE code=%s', ['e00'])

            name = fixture['function']
            if name == 'case_year':
                callback = lambda: selected(base.annotate(value=Case(When(code='never', then=ExtractYear('at', tzinfo=zone)), default=Value(0))).values('code', 'value'))
            elif name == 'filtered_year':
                def callback():
                    expression = Sum(ExtractYear('at', tzinfo=zone), filter=~Q(code='e00'))
                    active['case']['output_fields'] = {'value': expression.resolve_expression(base.query, summarize=True).output_field.get_internal_type()}
                    return Event.objects.aggregate(value=expression)
            else:
                callback = lambda: selected(base.annotate(value=functions[name]('at', tzinfo=zone)).values('code', 'value'))
            action('failure/' + fixture['name'], zone, callback,
                   setup=corrupt if fixture.get('corrupt') else None, wall_result=name.startswith('trunc_'))
            clear()

        assert physical() == [[], [], []]
        with connection.schema_editor() as editor:
            for model in reversed(owned_models):
                editor.delete_model(model)
        assert not connection.introspection.table_names()
        connections.close_all()
        expected_names = [fixture['name'] + '/' + operation for fixture in inputs['fixtures'] for operation in inputs['operations']]
        expected_names += ['failure/' + fixture['name'] for fixture in inputs['failures']]
        assert [case['name'] for case in cases] == expected_names
        result = {'kind': 'django-zoned-calendar-reference-v1', 'backend': vendor,
                  'django_commit': sources['django_commit'], 'runtime': runtime,
                  'observer_sha256': digest(Path(__file__).read_bytes()),
                  'shared_observer_sha256': digest((SHARED / 'observe.py').read_bytes()),
                  'input_sha256': digest(input_bytes), 'data_source_sha256': digest(data_bytes),
                  'prepare_sha256': prepare_hash, 'build_receipt_sha256': digest(receipt_bytes), 'build_receipt': receipt,
                  'source_verification_sha256': digest(source_bytes),
                  'provider': provider, 'native_zone_probes': probes, 'cases': cases, 'storage_unchanged': True}
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
    parser.add_argument('--data-directory', required=True, type=Path)
    arguments = parser.parse_args()
    document = observe(arguments.vendor, arguments.data_directory)
    arguments.output.parent.mkdir(parents=True, exist_ok=True)
    with arguments.output.open('x', encoding='utf-8') as output:
        json.dump(document, output, ensure_ascii=False, indent=2, allow_nan=False)
        output.write('\n')
    unexpected = {case['name']: case['error'] for case in document['cases']
                  if 'error' in case and not case['name'].startswith('failure/')}
    assert not unexpected, json.dumps(unexpected, ensure_ascii=False, sort_keys=True)
    incompatible = [probe for probe in document['native_zone_probes']
                    if 'postgres_offset_seconds' in probe and probe['postgres_offset_seconds'] != probe['zoneinfo_offset_seconds']]
    assert not incompatible, json.dumps(incompatible, ensure_ascii=False, sort_keys=True)
    print(json.dumps({'backend': arguments.vendor, 'cases': len(document['cases']), 'zones': len(document['provider']),
                      'sha256': digest(arguments.output.read_bytes())}))
