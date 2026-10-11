"""Independent Django 6.1 Decimal migration observations (BSD-3-Clause).

No GoDj source or expected data is imported. State, schema editor and
autodetector public APIs preserve physical values and read failures separately.
"""
import decimal
import json
import platform
import sqlite3
import tempfile
from pathlib import Path

import django
from django.conf import settings

PROFILES = [
    ('widen_whole', (5, 2), (7, 2), ['-999.99', '-0.00', '1.50', '999.99', None]),
    ('widen_scale_and_precision', (5, 2), (7, 4), ['-999.99', '1.50', '999.99', None]),
    ('reduce_scale_exact', (5, 3), (4, 2), ['-1.200', '1.250', None]),
    ('reduce_scale_rounding', (5, 3), (4, 2), ['-1.235', '-1.225', '1.225', '1.235', None]),
    ('reduce_whole_safe', (5, 2), (4, 2), ['-99.99', '99.99', None]),
    ('reduce_whole_overflow', (5, 2), (4, 2), ['-100.00', '100.00', '999.99', None]),
    ('scale_uses_existing_whole_capacity', (5, 2), (5, 3), ['1.20', '100.00', '999.99', None]),
    ('zero_whole_digits', (4, 2), (4, 4), ['0.12', '1.00', None]),
    ('zero_scale_exact', (5, 2), (3, 0), ['-10.00', '10.00', None]),
    ('zero_scale_rounding', (5, 2), (3, 0), ['-2.50', '2.50', None]),
    ('empty_tightening', (5, 3), (3, 2), []),
    ('identity', (5, 2), (5, 2), ['1.50', None]),
]


def rendered(value):
    if isinstance(value, decimal.Decimal):
        sign, digits, exponent = value.as_tuple()
        return dict(text=str(value), sign=sign, digits=list(digits), exponent=exponent)
    if isinstance(value, float):
        return dict(type='float', repr=repr(value))
    if isinstance(value, (tuple, list)):
        return [rendered(v) for v in value]
    return value


def observe():
    assert django.get_version() == '6.1'
    with tempfile.TemporaryDirectory(prefix='django-precision-') as directory:
        settings.configure(SECRET_KEY='independent-reference-only', INSTALLED_APPS=[],
                           DATABASES={'default': {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(directory) / 'reference.sqlite3')}},
                           USE_TZ=True, TIME_ZONE='UTC', LANGUAGE_CODE='en-us')
        django.setup()
        from django.db import connection, models
        from django.db.migrations import Migration
        from django.db.migrations.autodetector import MigrationAutodetector
        from django.db.migrations.graph import MigrationGraph
        from django.db.migrations.operations import AlterField, CreateModel
        from django.db.migrations.state import ProjectState

        app = 'decimalchanges'
        results = []
        for name, before, after, values in PROFILES:
            def field(precision):
                return models.DecimalField(max_digits=precision[0], decimal_places=precision[1], null=True, blank=True)

            empty = ProjectState()
            initial = CreateModel(name='Cost', fields=[('id', models.AutoField(primary_key=True)), ('value', field(before))],
                                  options={'db_table': 'decimal_precision_cost'})
            old_state = empty.clone()
            initial.state_forwards(app, old_state)
            with connection.schema_editor() as editor:
                initial.database_forwards(app, editor, empty, old_state)
            old_model = old_state.apps.get_model(app, 'Cost')
            for raw in values:
                old_model.objects.create(value=None if raw is None else decimal.Decimal(raw))

            def snapshot(model):
                rows = []
                for key in model.objects.order_by('id').values_list('id', flat=True):
                    try:
                        rows.append({'id': key, 'value': rendered(model.objects.get(pk=key).value)})
                    except decimal.DecimalException as error:
                        rows.append({'id': key, 'exception': type(error).__name__})
                with connection.cursor() as cursor:
                    cursor.execute('SELECT id, value, typeof(value) FROM decimal_precision_cost ORDER BY id')
                    physical = rendered(cursor.fetchall())
                    cursor.execute("SELECT sql FROM sqlite_master WHERE type='table' AND name='decimal_precision_cost'")
                    schema = cursor.fetchone()[0]
                return dict(rows=rows, physical=physical, schema=schema)

            operation = AlterField(model_name='cost', name='value', field=field(after))
            new_state = old_state.clone()
            operation.state_forwards(app, new_state)
            graph = MigrationGraph()
            migration = Migration('0001_initial', app)
            migration.operations = [initial]
            graph.add_node((app, '0001_initial'), migration)
            changes = MigrationAutodetector(old_state.clone(), new_state.clone()).changes(graph=graph)
            detected = [type(op).__name__ for change in changes.get(app, []) for op in change.operations]
            entry = dict(name=name, before=list(before), after=list(after), input=values, autodetected=detected, initial=snapshot(old_model))

            for direction, source, target in [('forward', old_state, new_state), ('reverse', new_state, old_state)]:
                statements = []

                def capture(execute, sql, params, many, context):
                    statements.append(sql)
                    return execute(sql, params, many, context)

                try:
                    with connection.execute_wrapper(capture):
                        with connection.schema_editor() as editor:
                            if direction == 'forward':
                                operation.database_forwards(app, editor, source, target)
                            else:
                                operation.database_backwards(app, editor, source, target)
                    result = dict(applied=True, sql=statements)
                except Exception as error:
                    result = dict(applied=False, exception=type(error).__name__, sql=statements)
                    target = source
                model = target.apps.get_model(app, 'Cost')
                result['snapshot'] = snapshot(model)
                connection.close()
                result['reopened'] = snapshot(model)
                entry[direction] = result
                if not result['applied']:
                    break
            with connection.schema_editor() as editor:
                editor.delete_model(old_model)
            results.append(entry)
        with connection.cursor() as cursor:
            cursor.execute('SELECT sqlite_source_id()')
            source_id = cursor.fetchone()[0]
        connection.close()
        return dict(django=django.get_version(), python=platform.python_version(),
                    sqlite=dict(version=sqlite3.sqlite_version, source_id=source_id), profiles=results)


if __name__ == '__main__':
    import sys
    json.dump(observe(), sys.stdout, ensure_ascii=False, sort_keys=True, indent=2)
    print()
