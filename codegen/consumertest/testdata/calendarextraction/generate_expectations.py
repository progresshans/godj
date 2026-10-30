"""Build literal review fixtures with only Python's standard library.

This does not execute GoDj or Django and is not execution evidence. The pinned
observer separately executes every case; its native and converted records must
match these closed expectations before a comparison can pass.
"""
from datetime import date, datetime, time, timedelta, timezone
from fractions import Fraction
from pathlib import Path
import hashlib
import json
import argparse
import os
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--check', action='store_true', help='reject drift without writing the fixture')
arguments = parser.parse_args()
root = Path(__file__).resolve().parents[4]
raw = (root / 'conformance/runners/django/calendar_extraction_reference/input.json').read_bytes()
source = json.loads(raw)
rows = sorted(source['events'], key=lambda row: row['code'])
schedules = {row['code']: row for row in source['schedules']}
period = timedelta(hours=1, microseconds=1)
clock_period = timedelta(days=2, hours=1, microseconds=1)
DAY = 86400000000

def day(text): return None if text is None else date.fromisoformat(text)
def instant(text): return None if text is None else datetime.fromisoformat(text).astimezone(timezone.utc)
def wall(text): return None if text is None else time.fromisoformat(text)
def micros(value): return value.days * DAY + value.seconds * 1000000 + value.microseconds
def clock_micros(value): return ((value.hour * 60 + value.minute) * 60 + value.second) * 1000000 + value.microsecond
def clock_text(value):
    value %= DAY
    seconds, us = divmod(value, 1000000)
    hours, seconds = divmod(seconds, 3600)
    minutes, seconds = divmod(seconds, 60)
    return f'{hours:02}:{minutes:02}:{seconds:02}' + (f'.{us:06}' if us else '')
def duration_text(value):
    value = micros(value) if isinstance(value, timedelta) else value
    days, remainder = divmod(value, DAY)
    return (str(days) + ' ' if days else '') + clock_text(remainder)
def duration_parts(value):
    days, text = value.split(' ', 1) if ' ' in value else ('0', value)
    clock_value = wall(text)
    return int(days), clock_value.hour * 3600 + clock_value.minute * 60 + clock_value.second, clock_value.microsecond
def midnight(value): return datetime.combine(value, time(0), timezone.utc)
def timestamp(value, aware=True):
    if value is None: return None
    result = value.replace(tzinfo=None).isoformat(timespec='microseconds')
    return result + 'Z' if aware else result
def start(row, vendor):
    if row['day'] is None or row['wall'] is None: return None
    value = midnight(day(row['day']))
    if vendor != 'sqlite': value += timedelta(microseconds=clock_micros(wall(row['wall'])))
    return timestamp(value, vendor != 'postgres')

fields = {}
cases = []
def native_value(value, kind, vendor, sqlite_clock='date'):
    if value is None: return {'type': 'NoneType', 'value': None}
    if kind == 'str': return {'type': 'str', 'value': value}
    if kind == 'IntegerField': return {'type': 'int', 'value': value}
    if kind == 'DurationField':
        days, seconds, us = duration_parts(value)
        if vendor == 'sqlite': return {'type': 'int', 'value': days * DAY + seconds * 1000000 + us}
        return {'type': 'timedelta', 'days': days, 'seconds': seconds, 'microseconds': us}
    if kind == 'TimeField':
        parsed = wall(value)
        if vendor == 'sqlite': return {'type': 'str', 'value': value}
        return {'type': 'time', 'value': parsed.isoformat(timespec='microseconds'), 'aware': False}
    assert kind == 'DateTimeField', kind
    parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
    if vendor == 'sqlite':
        text = parsed.date().isoformat() if sqlite_clock == 'date' else parsed.replace(tzinfo=None).isoformat(sep=' ')
        return {'type': 'str', 'value': text}
    return {'type': 'datetime', 'value': parsed.isoformat(timespec='microseconds'), 'aware': parsed.tzinfo is not None}

def append(name, output_fields, values, columns=('code', 'value'), sqlite_clock='date', errors=None, godj_error=False):
    record = {'name': name, 'fields': output_fields, 'godj_error': godj_error, 'godj': values['godj']}
    for vendor in ('sqlite', 'postgres'):
        if errors and vendor in errors:
            record[vendor] = {'error': errors[vendor]}
            continue
        result = values[vendor]
        part = {'result': result}
        if not name.startswith('update_'):
            result_rows = result if isinstance(result, list) else [result]
            part['native_rows'] = [[native_value(row[column], output_fields.get(column, 'str'), vendor, sqlite_clock) for column in columns] for row in result_rows]
        record[vendor] = part
    cases.append(record)

for name in ('arith_date_add', 'arith_date_reverse', 'arith_date_subtract', 'arith_date_difference',
             'arith_date_instant_difference', 'arith_instant_date_difference', 'arith_time_add',
             'arith_time_reverse', 'arith_time_subtract', 'arith_time_difference', 'arith_composed_start',
             'arith_filter_start', 'arith_related_start'):
    kind = 'DurationField' if 'difference' in name else 'TimeField' if name.startswith('arith_time_') else 'DateTimeField'
    values = {}
    for vendor in ('godj', 'sqlite', 'postgres'):
        observed = []
        for row in rows:
            if name == 'arith_date_subtract' and row['code'] == 'g': continue
            d, w, at = day(row['day']), wall(row['wall']), instant(row['at'])
            value = None
            if name in ('arith_date_add', 'arith_date_reverse', 'arith_date_subtract') and d is not None:
                value = midnight(d)
                if vendor != 'sqlite': value += -period if name == 'arith_date_subtract' else period
                value = timestamp(value, vendor != 'postgres')
            elif name == 'arith_date_difference' and d is not None:
                value = duration_text(d - date(2000, 1, 1))
            elif name in ('arith_date_instant_difference', 'arith_instant_date_difference') and d is not None and at is not None:
                value = d.year - at.year if vendor == 'sqlite' else micros(midnight(d) - at)
                if name == 'arith_instant_date_difference': value = -value
                value = duration_text(value)
            elif name in ('arith_time_add', 'arith_time_reverse', 'arith_time_subtract') and w is not None and vendor != 'sqlite':
                sign = -1 if name == 'arith_time_subtract' else 1
                value = clock_text(clock_micros(w) + sign * micros(clock_period))
            elif name == 'arith_time_difference' and w is not None:
                value = duration_text(clock_micros(w) - 43200000000)
            elif name in ('arith_composed_start', 'arith_filter_start'):
                value = start(row, vendor)
                if name == 'arith_filter_start' and (value is None or not '2020' <= value[:4] < '2022'): continue
            elif name == 'arith_related_start':
                related = schedules.get(row['schedule'])
                value = None if related is None else start(related, vendor)
            observed.append({'code': row['code'], 'value': value})
        values[vendor] = observed
    append(name, {'value': kind}, values)

values = {}
for vendor in ('godj', 'sqlite', 'postgres'):
    observed = []
    for code in sorted(schedules):
        related = [row for row in rows if row['schedule'] == code]
        all_values = [value for row in related if (value := start(row, vendor)) is not None]
        enabled = [value for row in related if row['enabled'] and (value := start(row, vendor)) is not None]
        observed.append({'code': code, 'first': min(all_values, default=None), 'last': max(enabled, default=None), 'count': len(all_values)})
    values[vendor] = observed
append('arith_collection_start', {'first': 'DateTimeField', 'last': 'DateTimeField', 'count': 'IntegerField'}, values, ('code', 'first', 'last', 'count'))
observed = {'first': duration_text(min(clock_micros(wall(row['wall'])) for row in rows if row['wall'] is not None)),
            'last': duration_text(max(day(row['day']) - date(2000, 1, 1) for row in rows if row['day'] is not None)),
            'total': duration_text(sum(clock_micros(wall(row['wall'])) for row in rows if row['enabled'] and row['wall'] is not None))}
append('arith_aggregate_difference', dict.fromkeys(('first', 'last', 'total'), 'DurationField'), dict.fromkeys(('godj', 'sqlite', 'postgres'), observed), ('first', 'last', 'total'))

for name in ('arith_case_time', 'arith_date_fraction', 'arith_time_fraction', 'arith_invalid_date_storage', 'arith_clock_24_storage', 'arith_unselected_corrupt_date'):
    kind = 'TimeField' if name in ('arith_case_time', 'arith_time_fraction', 'arith_clock_24_storage') else 'DateTimeField'
    values = {}
    for vendor in ('godj', 'sqlite', 'postgres'):
        if vendor == 'godj' and name in ('arith_invalid_date_storage', 'arith_clock_24_storage'):
            values[vendor] = None
            continue
        observed = []
        for row in rows:
            d, w = day(row['day']), wall(row['wall'])
            value = None
            if name == 'arith_case_time' and w is not None and vendor != 'sqlite':
                value = clock_text(clock_micros(w) + (1 if row['enabled'] else -1) * micros(clock_period))
            elif name == 'arith_date_fraction' and d is not None and vendor != 'sqlite':
                value = timestamp(midnight(d), vendor != 'postgres')
            elif name == 'arith_time_fraction' and w is not None and vendor != 'sqlite':
                ticks = clock_micros(w)
                value = clock_text(round(Fraction(ticks * 2 + 1, 2)) if vendor == 'godj' else ticks)
            elif name == 'arith_invalid_date_storage' and d is not None and row['code'] != 'a':
                value = timestamp(midnight(d), vendor != 'postgres')
            elif name == 'arith_clock_24_storage' and w is not None and vendor == 'postgres':
                value = '00:00:00' if row['code'] == 'a' else row['wall']
            elif name == 'arith_unselected_corrupt_date':
                value = '2000-01-01T00:00:00.000000Z'
            observed.append({'code': row['code'], 'value': value})
        values[vendor] = observed
    errors = {'postgres': {'type': 'DataError', 'stage': 'conversion', 'sqlstate': '', 'native_stage': 'native_fetch'}} if name == 'arith_invalid_date_storage' else None
    append(name, {'value': kind}, values, sqlite_clock='timestamp' if name == 'arith_unselected_corrupt_date' else 'date', errors=errors, godj_error=name in ('arith_invalid_date_storage', 'arith_clock_24_storage'))

values = {vendor: {'first': timestamp(midnight(date(1, 1, 1)) + (timedelta() if vendor == 'sqlite' else period), vendor != 'postgres')} for vendor in ('godj', 'sqlite', 'postgres')}
append('arith_filtered_corrupt_date', {'first': 'DateTimeField'}, values, ('first',))
values = {}
for vendor in ('godj', 'sqlite', 'postgres'):
    observed = []
    for row in rows:
        w = wall(row['wall'])
        value = start(row, vendor)
        if value is not None and vendor == 'postgres': value += 'Z'  # Stored DateTime is read back as an aware instant.
        observed.append({'code': row['code'], 'at': value, 'day': row['day'],
                         'wall': None if w is None or vendor == 'sqlite' else clock_text(clock_micros(w) + 1), 'part': row['part']})
    values[vendor] = {'changed': 11, 'rows': observed}
append('update_arithmetic_calendar', None, values)
assert len(cases) == 23 and len({case['name'] for case in cases}) == 23
document = {'kind': 'calendar-arithmetic-expectations-v1', 'input_sha256': hashlib.sha256(raw).hexdigest(),
            'basis': 'Literal expectations constructed with Python standard-library date/datetime/timedelta and Fraction; no GoDj or Django execution. They are assertions, not observed evidence.',
            'cases': cases}
destination = root / 'codegen/consumertest/testdata/calendarextraction/arithmetic-expectations.json'
data = (json.dumps(document, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
if arguments.check:
    if destination.read_bytes() != data:
        raise SystemExit('calendar arithmetic expectation drift')
else:
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=destination.parent, prefix='.arithmetic-', delete=False) as output:
            temporary = Path(output.name)
            output.write(data)
        os.chmod(temporary, 0o644)
        os.replace(temporary, destination)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
print(json.dumps({'cases': len(cases), 'path': str(destination), 'bytes': destination.stat().st_size, 'sha256': hashlib.sha256(destination.read_bytes()).hexdigest()}))
