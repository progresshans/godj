"""Independent Django 6.1 unique-field observations (BSD-3-Clause).

Public ORM, ModelForm, migration state/schema-editor and introspection APIs
provide the observations. No GoDj code or expected results are imported.
"""
import datetime
import decimal
import importlib.metadata
import json
import os
import platform
import tempfile
import threading
import uuid
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import django
from django.conf import settings


class CanonicalStorageEncoder(json.JSONEncoder):
    """Separate public encoder observation of GoDj's existing JSON storage policy."""
    def __init__(self, *args, **kwargs):
        kwargs.update(ensure_ascii=False, sort_keys=True, separators=(',', ':'))
        super().__init__(*args, **kwargs)

    def encode(self, value):
        text = super().encode(value)
        for char, escaped in [('<', r'\u003c'), ('>', r'\u003e'), ('&', r'\u0026'),
                              ('\u2028', r'\u2028'), ('\u2029', r'\u2029')]:
            text = text.replace(char, escaped)
        return text


def value_json(value):
    # JSON-decoded Python equality otherwise treats True, 1 and 1.0 as equal,
    # and loses the sign of zero when fresh observations are compared.
    if isinstance(value, bool):
        return {'boolean': value}
    if isinstance(value, int):
        return {'integer': str(value)}
    if isinstance(value, uuid.UUID):
        return {'uuid': str(value)}
    if isinstance(value, decimal.Decimal):
        return {'decimal': str(value)}
    if isinstance(value, (datetime.datetime, datetime.date, datetime.time)):
        return {type(value).__name__: value.isoformat()}
    if isinstance(value, datetime.timedelta):
        return {'microseconds': value // datetime.timedelta(microseconds=1)}
    if isinstance(value, float):
        return {'float': repr(value)}
    if isinstance(value, (tuple, list)):
        return [value_json(item) for item in value]
    if isinstance(value, dict):
        return {key: value_json(item) for key, item in value.items()}
    return value


def failure(error):
    cause = error.__cause__
    result = {'exception': type(error).__name__}
    if code := getattr(cause, 'sqlstate', None):
        result['sqlstate'] = code
    if code := getattr(cause, 'sqlite_errorname', None):
        result['sqlite_errorname'] = code
    return result


def observe():
    assert django.get_version() == '6.1' and not settings.configured
    database = os.environ.get('GODJ_UNIQUE_DATABASE')
    with tempfile.TemporaryDirectory(prefix='godj-unique-reference-') as temporary:
        config = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(temporary) / 'reference.sqlite3'),
                  'OPTIONS': {'timeout': 20}}
        if database:
            assert importlib.metadata.version('psycopg') == '3.3.6'
            config = {'ENGINE': 'django.db.backends.postgresql', 'NAME': database, 'HOST': 'localhost', 'PORT': 5432}
        settings.configure(SECRET_KEY='independent-reference-only', INSTALLED_APPS=[],
                           DATABASES={'default': config}, USE_TZ=True, TIME_ZONE='UTC', LANGUAGE_CODE='en-us')
        django.setup()
        from django.db import connection, connections, models, transaction
        from django.db.migrations import Migration
        from django.db.migrations.autodetector import MigrationAutodetector
        from django.db.migrations.graph import MigrationGraph
        from django.db.migrations.operations import AddField, AlterField, CreateModel
        from django.db.migrations.state import ProjectState
        from django.forms import modelform_factory

        app = 'uniqueprobe'
        uuid_a = uuid.UUID('12345678-9abc-4def-8123-456789abcdef')
        uuid_b = uuid.UUID('22345678-9abc-4def-8123-456789abcdef')
        uuid_c = uuid.UUID('32345678-9abc-4def-8123-456789abcdef')

        def create(name, field, *, extra=()):
            empty = ProjectState()
            op = CreateModel(name=name, fields=[('id', models.AutoField(primary_key=True)),
                                               ('label', models.CharField(max_length=60)), *extra, ('value', field)],
                             options={'db_table': 'uniqueprobe_' + name.lower()})
            state = empty.clone()
            op.state_forwards(app, state)
            with connection.schema_editor() as editor:
                op.database_forwards(app, editor, empty, state)
            return state, op, state.apps.get_model(app, name)

        def constraints(model):
            with connection.cursor() as cursor:
                raw = connection.introspection.get_constraints(cursor, model._meta.db_table)
            # Keep physical ownership without volatile OIDs or framework name equality.
            return sorted([{'name': name, 'columns': info['columns'], 'primary_key': info['primary_key'],
                            'unique': info['unique'], 'index': info['index'], 'check': info['check'],
                            'foreign_key': value_json(info['foreign_key'])}
                           for name, info in raw.items()], key=lambda row: row['name'])

        def snapshot(model, *, with_value=True):
            quote = connection.ops.quote_name
            selected = 'id,label' + (',value,value IS NULL' if with_value else '')
            with connection.cursor() as cursor:
                cursor.execute('SELECT ' + selected + ' FROM ' + quote(model._meta.db_table) + ' ORDER BY id')
                physical = value_json(cursor.fetchall())
                columns = [{'name': info.name, 'type': connection.introspection.get_field_type(info.type_code, info),
                            'nullable': info.null_ok, 'default': info.default}
                           for info in connection.introspection.get_table_description(cursor, model._meta.db_table)]
            rows = [{'label': obj.label, 'value': value_json(obj.value)} if with_value else {'label': obj.label}
                    for obj in model.objects.order_by('id')]
            return {'rows': rows, 'physical': physical, 'columns': columns, 'constraints': constraints(model)}

        def remove(model):
            with connection.schema_editor() as editor:
                editor.delete_model(model)

        profiles = [
            ('char', lambda: models.CharField(max_length=60), ['', '', 'Alpha', 'alpha', 'Alpha', '한글', '한글']),
            ('text', models.TextField, ['text\nvalue', 'text\nvalue', '', '']),
            ('integer', models.BigIntegerField, [0, 0, -1, -1, 2**63-1, 2**63-1]),
            ('boolean', models.BooleanField, [False, False, True, True]),
            ('float', models.FloatField, [0.0, -0.0, 1.0, 1, float('nan'), float('nan'), float('inf'), float('inf')]),
            ('decimal', lambda: models.DecimalField(max_digits=14, decimal_places=2),
             [decimal.Decimal('1.00'), decimal.Decimal('1.0'), decimal.Decimal('-0.00'), decimal.Decimal('0.00')]),
            ('uuid', models.UUIDField, [uuid_a, uuid_a.hex, '{' + str(uuid_a) + '}', uuid_b, uuid.UUID(int=0), uuid.UUID(int=0)]),
            ('date', models.DateField, [datetime.date(2026, 9, 21), datetime.date(2026, 9, 21), datetime.date(1, 1, 1)]),
            ('datetime', models.DateTimeField, [datetime.datetime(2026, 9, 21, tzinfo=datetime.timezone.utc),
                                              datetime.datetime(2026, 9, 21, 9, tzinfo=datetime.timezone(datetime.timedelta(hours=9)))]),
            ('time', models.TimeField, [datetime.time(0), datetime.time(0), datetime.time(23, 59, 59, 999999)]),
            ('duration', models.DurationField, [datetime.timedelta(seconds=1), datetime.timedelta(microseconds=1000000), datetime.timedelta(0)]),
            ('json', models.JSONField, [models.JSONNull(), models.JSONNull(), 1, 1.0, {'a': 1, 'b': 2}, {'b': 2, 'a': 1}, True, 'true', [], []]),
        ]
        profiles.append(('json_canonical', lambda: models.JSONField(encoder=CanonicalStorageEncoder), profiles[-1][2]))
        output = []
        for name, factory, values in profiles:
            field = factory()
            # Clone through the public constructor/deconstruction contract.
            _, _, args, kwargs = field.deconstruct()
            kwargs.update(unique=True, null=True, blank=True)
            field = type(field)(*args, **kwargs)
            _, _, model = create(name.title(), field)
            attempts = []
            try:
                for i, value in enumerate([None, None, *values]):
                    row = {'index': i, 'input': {'json_null': True} if isinstance(value, models.JSONNull) else value_json(value)}
                    try:
                        with transaction.atomic():
                            saved = model.objects.create(label='input_' + str(i), value=value)
                        row.update(saved=True, read=value_json(model.objects.get(pk=saved.pk).value))
                    except Exception as error:
                        row.update(saved=False, **failure(error))
                    attempts.append(row)
                before = snapshot(model)
                connection.close()
                output.append({'name': name, 'attempts': attempts, 'snapshot': before,
                               'reopened': snapshot(model), 'unique': model._meta.get_field('value').unique})
            finally:
                remove(model)

        # Form validation excludes the current instance, normalizes UUID aliases,
        # and treats an empty nullable field separately from a stored zero UUID.
        _, _, form_model = create('FormRecord', models.UUIDField(unique=True, null=True, blank=True))
        forms = []
        try:
            owner = form_model.objects.create(label='owner', value=uuid_a)
            other = form_model.objects.create(label='other', value=uuid_b)
            form_type = modelform_factory(form_model, fields=('label', 'value'))
            for name, instance, raw in [('duplicate', None, str(uuid_a)), ('alias', None, uuid_a.hex),
                                         ('self', owner, uuid_a.hex), ('other', other, str(uuid_a)),
                                         ('empty', None, ''), ('zero', None, str(uuid.UUID(int=0)))]:
                form = form_type(data={'label': 'candidate', 'value': raw}, instance=instance)
                valid = form.is_valid()
                forms.append({'name': name, 'valid': valid, 'errors': {field: [error.code for error in errors]
                              for field, errors in form.errors.as_data().items()},
                              'cleaned': value_json(form.cleaned_data.get('value'))})
        finally:
            remove(form_model)

        migrations = []
        for name, unique, values in [('add_clean', False, [uuid_a, uuid_b, None, None]),
                                      ('add_duplicates', False, [uuid_a, uuid_a, None, None]),
                                      ('drop', True, [uuid_a, uuid_b, None, None])]:
            state, initial, old_model = create('AlterRecord', models.UUIDField(null=True, unique=unique))
            for i, value in enumerate(values):
                old_model.objects.create(label='row_' + str(i), value=value)
            child_operation = CreateModel(name='Dependent', fields=[('id', models.AutoField(primary_key=True)),
                ('parent', models.ForeignKey(app + '.alterrecord', on_delete=models.CASCADE))],
                options={'db_table': 'uniqueprobe_dependent'})
            before_child = state.clone()
            child_operation.state_forwards(app, state)
            with connection.schema_editor() as editor:
                child_operation.database_forwards(app, editor, before_child, state)
            dependent = state.apps.get_model(app, 'Dependent')
            for key in old_model.objects.order_by('id').values_list('id', flat=True):
                dependent.objects.create(parent_id=key)
            operation = AlterField(model_name='alterrecord', name='value', field=models.UUIDField(null=True, unique=not unique))
            target = state.clone()
            operation.state_forwards(app, target)
            graph = MigrationGraph()
            initial_migration = Migration('0001_initial', app)
            initial_migration.operations = [initial, child_operation]
            graph.add_node((app, '0001_initial'), initial_migration)
            detected = MigrationAutodetector(state.clone(), target.clone()).changes(graph=graph)
            item = {'name': name, 'autodetected': [type(op).__name__ for m in detected.get(app, []) for op in m.operations],
                    'initial': snapshot(old_model), 'steps': [],
                    'dependents': list(dependent.objects.order_by('id').values_list('id', 'parent_id'))}
            current = state

            def apply(direction, source, destination):
                statements = []

                def capture(execute, sql, params, many, context):
                    statements.append(sql)
                    return execute(sql, params, many, context)

                try:
                    with connection.execute_wrapper(capture), connection.schema_editor() as editor:
                        if direction == 'forward':
                            operation.database_forwards(app, editor, source, destination)
                        else:
                            operation.database_backwards(app, editor, source, destination)
                    result = {'applied': True}
                    actual = destination
                except Exception as error:
                    result = {'applied': False, **failure(error)}
                    actual = source
                model = actual.apps.get_model(app, 'AlterRecord')
                result.update(direction=direction, sql=statements, snapshot=snapshot(model))
                connection.close()
                result['reopened'] = snapshot(model)
                result['dependents'] = list(dependent.objects.order_by('id').values_list('id', 'parent_id'))
                connection.check_constraints(table_names=[model._meta.db_table, dependent._meta.db_table])
                result['referential_integrity'] = True
                item['steps'].append(result)
                return actual

            try:
                current = apply('forward', state, target)
                if name == 'add_duplicates':
                    old_model.objects.filter(label='row_1').update(value=uuid_b)
                    current = apply('forward', state, target)
                if name == 'drop':
                    target.apps.get_model(app, 'AlterRecord').objects.create(label='new_duplicate', value=uuid_a)
                    current = apply('reverse', target, state)
                    target.apps.get_model(app, 'AlterRecord').objects.filter(label='new_duplicate').update(value=uuid_c)
                current = apply('reverse', target, state)
                migrations.append(item)
            finally:
                remove(dependent)
                remove(current.apps.get_model(app, 'AlterRecord'))

        additions = []
        for name, default in [('nullable', None), ('constant_default', uuid_a)]:
            state, _, model = create('AddedRecord', models.UUIDField(null=True))
            model.objects.create(label='first', value=uuid_a)
            model.objects.create(label='second', value=uuid_b)
            field = models.UUIDField(null=True, unique=True, **({} if default is None else {'default': default}))
            operation = AddField(model_name='addedrecord', name='additional', field=field)
            target = state.clone()
            operation.state_forwards(app, target)
            item = {'name': name, 'initial': snapshot(model)}
            actual = state
            try:
                try:
                    with connection.schema_editor() as editor:
                        operation.database_forwards(app, editor, state, target)
                    item['applied'] = True
                    actual = target
                except Exception as error:
                    item.update(applied=False, **failure(error))
                result_model = actual.apps.get_model(app, 'AddedRecord')
                item['snapshot'] = snapshot(result_model)
                connection.close()
                item['reopened'] = snapshot(result_model)
                if item['applied']:
                    item['new_values'] = list(result_model.objects.order_by('id').values_list('additional', flat=True))
                    with connection.schema_editor() as editor:
                        operation.database_backwards(app, editor, target, state)
                    actual = state
                    item['reversed'] = snapshot(model)
                additions.append(item)
            finally:
                remove(actual.apps.get_model(app, 'AddedRecord'))

        # ForeignKey(unique=True) retains a many-to-one field and a collection
        # reverse descriptor; one-to-one Python object shape is a different field.
        state, _, parent = create('Parent', models.CharField(max_length=60))
        parents = [parent.objects.create(label=str(i), value=str(i)) for i in range(2)]
        relation_op = CreateModel(name='UniqueRelation', fields=[('id', models.AutoField(primary_key=True)),
            ('parent', models.ForeignKey(app + '.parent', on_delete=models.CASCADE, unique=True, null=True,
                                         related_name='unique_links'))], options={'db_table': 'uniqueprobe_relation'})
        before_relation = state.clone()
        relation_op.state_forwards(app, state)
        with connection.schema_editor() as editor:
            relation_op.database_forwards(app, editor, before_relation, state)
        relation = state.apps.get_model(app, 'UniqueRelation')
        parent = state.apps.get_model(app, 'Parent')
        try:
            attempts = []
            for key in [None, None, parents[0].pk, parents[0].pk, parents[1].pk]:
                try:
                    with transaction.atomic():
                        relation.objects.create(parent_id=key)
                    attempts.append({'parent_id': key, 'saved': True})
                except Exception as error:
                    attempts.append({'parent_id': key, 'saved': False, **failure(error)})
            field = relation._meta.get_field('parent')
            reverse = parent._meta.get_field('unique_links')
            relation_result = {'attempts': attempts, 'constraints': constraints(relation),
                'many_to_one': field.many_to_one, 'one_to_one': field.one_to_one,
                'reverse_one_to_many': reverse.one_to_many,
                'reverse_count': parent.objects.get(pk=parents[0].pk).unique_links.count()}
        finally:
            remove(relation)
            remove(parent)

        # An outer transaction remains usable when the duplicate is isolated by
        # a savepoint. Rollback also restores the successful write preceding it.
        _, _, transactional = create('Transactional', models.UUIDField(unique=True, null=True))
        try:
            transactional.objects.create(label='existing', value=uuid_a)
            with transaction.atomic():
                transactional.objects.create(label='outer', value=uuid_b)
                try:
                    with transaction.atomic():
                        transactional.objects.create(label='duplicate', value=uuid_a)
                except Exception as error:
                    duplicate = failure(error)
                transactional.objects.create(label='after_savepoint', value=uuid_c)
                inside = list(transactional.objects.order_by('id').values_list('label', flat=True))
                transaction.set_rollback(True)
            after = list(transactional.objects.order_by('id').values_list('label', flat=True))
            rollback = {'duplicate': duplicate, 'inside': inside, 'after': after}
        finally:
            remove(transactional)

        _, _, concurrent = create('Concurrent', models.UUIDField(unique=True))
        barrier = threading.Barrier(2)

        def writer(index):
            try:
                barrier.wait(timeout=20)
                concurrent.objects.create(label='writer_' + str(index), value=uuid_a)
                return {'saved': True}
            except Exception as error:
                return {'saved': False, **failure(error)}
            finally:
                connections['default'].close()

        try:
            with ThreadPoolExecutor(max_workers=2) as pool:
                futures = [pool.submit(writer, i) for i in range(2)]
                outcomes = sorted([f.result(timeout=30) for f in futures], key=lambda row: row['saved'])
            concurrency = {'outcomes': outcomes, 'count': concurrent.objects.count(),
                           'values': [value_json(v) for v in concurrent.objects.values_list('value', flat=True)]}
        finally:
            remove(concurrent)

        with connection.cursor() as cursor:
            cursor.execute('SELECT version()' if database else 'SELECT sqlite_version()')
            version = cursor.fetchone()[0]
        connection.close()
        return {'django': django.get_version(), 'python': platform.python_version(), 'backend': connection.vendor,
                'database_version': version, 'profiles': output, 'forms': forms, 'migrations': migrations,
                'additions': additions, 'relation': relation_result, 'rollback': rollback, 'concurrency': concurrency}


if __name__ == '__main__':
    print(json.dumps(observe(), ensure_ascii=True, sort_keys=True, separators=(',', ':'), allow_nan=False))
