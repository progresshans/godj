"""Independent Django 6.1 OneToOne observations (BSD-3-Clause).

Authored public ORM, ModelForm and migration inputs; no GoDj or expected
fixture imports. The two installed apps exercise a cross-app reverse edge.
Database error messages and temporary paths are deliberately not serialized.
"""

import hashlib
import importlib.metadata
import inspect
import json
import os
import platform
import sys
import tempfile
import types
from pathlib import Path

import django
from django.apps import AppConfig
from django.conf import settings


def observe():
    assert django.get_version() == '6.1' and not settings.configured
    database = os.environ.get('GODJ_ONE_TO_ONE_DATABASE')
    if database:
        assert database.startswith('godj_one_to_one_')
        assert importlib.metadata.version('psycopg') == '3.3.6'
    with tempfile.TemporaryDirectory(prefix='godj-one-to-one-reference-') as temporary:
        installed = []
        for name in ('otoparents', 'otodetails'):
            module = types.ModuleType(name)
            module.__file__ = str(Path(temporary) / (name + '.py'))
            module.__path__ = [temporary]
            module.ReferenceConfig = type('ReferenceConfig', (AppConfig,), {
                'name': name, 'label': name, 'path': temporary, '__module__': name,
            })
            sys.modules[name] = module
            installed.append(name + '.ReferenceConfig')
        config = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(temporary) / 'reference.sqlite3')}
        if database:
            config = {'ENGINE': 'django.db.backends.postgresql', 'NAME': database, 'HOST': 'localhost', 'PORT': 5432}
        settings.configure(SECRET_KEY='independent-reference-only', INSTALLED_APPS=installed,
                           DATABASES={'default': config}, DEFAULT_AUTO_FIELD='django.db.models.AutoField',
                           USE_TZ=True, TIME_ZONE='UTC', LANGUAGE_CODE='en-us')
        django.setup()
        from django.db import connection, models, transaction
        from django.db.models import Q
        from django.db.migrations import Migration
        from django.db.migrations.autodetector import MigrationAutodetector
        from django.db.migrations.graph import MigrationGraph
        from django.db.migrations.operations import AlterField, CreateModel
        from django.db.migrations.state import ProjectState
        from django.forms import modelform_factory

        class Parent(models.Model):
            name = models.CharField(max_length=50)

            class Meta:
                app_label = 'otoparents'
                db_table = 'oto_parent'

        class Detail(models.Model):
            parent = models.OneToOneField(Parent, on_delete=models.PROTECT, related_name='detail')
            note = models.CharField(max_length=50)

            class Meta:
                app_label = 'otodetails'
                db_table = 'oto_detail'

        class OptionalDetail(models.Model):
            parent = models.OneToOneField(Parent, on_delete=models.SET_NULL, null=True, blank=True,
                                         related_name='optional_detail')

            class Meta:
                app_label = 'otodetails'
                db_table = 'oto_optional'

        class UniqueChild(models.Model):
            parent = models.ForeignKey(Parent, on_delete=models.PROTECT, unique=True, related_name='unique_children')

            class Meta:
                app_label = 'otodetails'
                db_table = 'oto_unique_child'

        class DefaultDetail(models.Model):
            parent = models.OneToOneField(Parent, on_delete=models.PROTECT)

            class Meta:
                app_label = 'otodetails'
                db_table = 'oto_default'

        class HiddenDetail(models.Model):
            parent = models.OneToOneField(Parent, on_delete=models.PROTECT, related_name='+')

            class Meta:
                app_label = 'otodetails'
                db_table = 'oto_hidden'

        def field_shape(field):
            return {**{name: getattr(field, name) for name in ('unique', 'many_to_one', 'one_to_one', 'null')},
                    'reverse_one_to_one': field.remote_field.one_to_one,
                    'reverse_one_to_many': field.remote_field.one_to_many,
                    'reverse_name': field.remote_field.get_accessor_name(), 'hidden': field.remote_field.hidden}

        observations = []

        def record(name, operation):
            statements = []

            def execute(next_execute, sql, params, many, context):
                statements.append(sql.lstrip().split(' ', 1)[0].upper())
                return next_execute(sql, params, many, context)

            with connection.execute_wrapper(execute):
                try:
                    result = {'name': name, 'value': operation()}
                except Exception as error:
                    result = {'name': name, 'exception': type(error).__name__}
            result.update(selects=statements.count('SELECT'), statements=statements)
            observations.append(result)
            return result

        def forms(model, data, instance=None):
            form = modelform_factory(model, fields=['parent'])(data, instance=instance)
            valid = form.is_valid()
            return {'valid': valid, 'errors': {key: [error.code for error in errors]
                                             for key, errors in form.errors.as_data().items()}}

        def constraints(model):
            with connection.cursor() as cursor:
                found = connection.introspection.get_constraints(cursor, model._meta.db_table)
            return [{'name': name, **{key: value[key] for key in ('columns', 'primary_key', 'unique', 'foreign_key', 'index')}}
                    for name, value in sorted(found.items())]

        created = []
        try:
            assert not connection.introspection.table_names()
            for model in (Parent, Detail, OptionalDetail, UniqueChild, DefaultDetail, HiddenDetail):
                with connection.schema_editor() as editor:
                    editor.create_model(model)
                created.append(model)
            first, second, third = [Parent.objects.create(name=name) for name in ('first', 'second', 'third')]
            parent = Parent.objects.get(pk=first.pk)
            record('missing_reverse', lambda: parent.detail.pk)
            record('cached_missing_reverse', lambda: parent.detail.pk)
            detail = Detail.objects.create(parent_id=first.pk, note='original')
            record('missing_cache_after_external_insert', lambda: parent.detail.pk)
            parent.refresh_from_db()
            record('reverse_after_refresh', lambda: parent.detail.pk)
            record('cached_reverse', lambda: parent.detail.pk)
            record('reciprocal_forward_cache', lambda: parent.detail.parent.pk)
            record('reverse_select_related', lambda: [
                [row.pk, hasattr(row, 'detail')] for row in Parent.objects.select_related('detail').order_by('id')])
            record('reverse_prefetch_related', lambda: [
                [row.pk, hasattr(row, 'detail')] for row in Parent.objects.prefetch_related('detail').order_by('id')])
            record('reverse_filter', lambda: list(Parent.objects.filter(detail__note='original').values_list('id', flat=True)))
            record('missing_reverse_filter', lambda: list(
                Parent.objects.filter(detail__isnull=True).order_by('id').values_list('id', flat=True)))
            record('reverse_exclude', lambda: list(
                Parent.objects.exclude(detail__note='original').order_by('id').values_list('id', flat=True)))
            record('reverse_or_absent', lambda: list(Parent.objects.filter(
                Q(detail__note='original') | Q(detail__isnull=True)).order_by('id').values_list('id', flat=True)))
            record('required_form_duplicate', lambda: forms(Detail, {'parent': first.pk}))
            record('required_form_self', lambda: forms(Detail, {'parent': first.pk}, detail))
            record('required_form_available', lambda: forms(Detail, {'parent': second.pk}))
            record('required_form_missing', lambda: forms(Detail, {'parent': ''}))
            record('form_invalid_target', lambda: forms(Detail, {'parent': 99999}))
            record('optional_form_blank', lambda: forms(OptionalDetail, {'parent': ''}))

            def duplicate():
                with transaction.atomic():
                    Parent.objects.filter(pk=second.pk).update(name='must rollback')
                    Detail.objects.create(parent_id=first.pk, note='duplicate')

            record('duplicate_rolls_back', duplicate)
            record('state_after_duplicate', lambda: [Parent.objects.get(pk=second.pk).name, Detail.objects.count()])
            orphan = OptionalDetail.objects.create()
            OptionalDetail.objects.create()
            record('nullable_forward', lambda: orphan.parent)
            record('multiple_nulls', lambda: OptionalDetail.objects.filter(parent__isnull=True).count())
            linked = OptionalDetail.objects.create(parent=third)
            record('set_null_delete', lambda: third.delete()[0])
            linked.refresh_from_db()
            record('set_null_child_retained', lambda: [linked.parent_id, OptionalDetail.objects.count()])
            record('protect_delete', lambda: first.delete())
            record('state_after_protect', lambda: [Parent.objects.filter(pk=first.pk).exists(), Detail.objects.count()])
            fresh = Parent.objects.get(pk=second.pk)

            def assign_reverse():
                fresh.detail = detail
                return [detail.parent_id, Detail.objects.get(pk=detail.pk).parent_id]

            record('reverse_assignment_before_save', assign_reverse)
            record('save_reassigned_detail', lambda: detail.save())
            record('reassigned_detail_stored', lambda: Detail.objects.get(pk=detail.pk).parent_id)
            record('unsaved_target_save', lambda: Detail(parent=Parent(name='unsaved'), note='bad').save())
            record('unsaved_owner_reverse', lambda: Parent(name='unsaved').detail.pk)
            UniqueChild.objects.create(parent_id=first.pk)
            record('unique_fk_reverse_is_manager', lambda: hasattr(Parent.objects.get(pk=first.pk).unique_children, 'all'))
            record('default_reverse_missing', lambda: Parent.objects.get(pk=first.pk).defaultdetail.pk)
            record('hidden_reverse_absent', lambda: hasattr(Parent.objects.get(pk=first.pk), 'hiddendetail'))

            def clear_reverse():
                fresh.detail = None
                return [detail.parent_id, Detail.objects.get(pk=detail.pk).parent_id]

            record('clear_required_reverse_in_memory', clear_reverse)
            record('save_cleared_required_reverse', lambda: detail.save())
            record('failed_clear_preserves_storage', lambda: Detail.objects.get(pk=detail.pk).parent_id)
            shapes = {model.__name__: field_shape(model._meta.get_field('parent'))
                      for model in (Detail, OptionalDetail, UniqueChild, DefaultDetail, HiddenDetail)}

            # A historical FK+unique -> OneToOne change has the same physical
            # constraint but different public reverse cardinality. A non-unique
            # FK conversion additionally has to fail atomically on duplicate rows.
            migrations = []
            for initially_unique in (False, True):
                app = 'otohistory'
                state = ProjectState()
                operations = [CreateModel(name='Owner', fields=[('id', models.AutoField(primary_key=True))]),
                              CreateModel(name='Child', fields=[('id', models.AutoField(primary_key=True)),
                                  ('owner', models.ForeignKey(app + '.Owner', on_delete=models.PROTECT,
                                                             related_name='children', unique=initially_unique))])]
                for operation in operations:
                    after = state.clone()
                    operation.state_forwards(app, after)
                    with connection.schema_editor() as editor:
                        operation.database_forwards(app, editor, state, after)
                    state = after
                    created.append(state.apps.get_model(app, operation.name))
                owner = state.apps.get_model(app, 'Owner')
                child = state.apps.get_model(app, 'Child')
                instance = owner.objects.create()
                child.objects.create(owner_id=instance.pk)
                if not initially_unique:
                    child.objects.create(owner_id=instance.pk)
                operation = AlterField('child', 'owner', models.OneToOneField(
                    app + '.Owner', on_delete=models.PROTECT, related_name='children'))
                after = state.clone()
                operation.state_forwards(app, after)
                graph = MigrationGraph()
                initial = Migration('0001_initial', app)
                initial.operations = operations
                graph.add_node((app, '0001_initial'), initial)
                detected = MigrationAutodetector(state.clone(), after.clone()).changes(graph=graph)
                result = {'initially_unique': initially_unique,
                          'autodetected': [type(op).__name__ for migration in detected.get(app, []) for op in migration.operations],
                          'before': field_shape(child._meta.get_field('owner')),
                          'after': field_shape(after.apps.get_model(app, 'Child')._meta.get_field('owner')),
                          'initial_rows': list(child.objects.order_by('id').values_list('id', 'owner_id')),
                          'initial_constraints': constraints(child)}
                try:
                    with connection.schema_editor() as editor:
                        operation.database_forwards(app, editor, state, after)
                    result['applied'] = True
                except Exception as error:
                    result.update(applied=False, exception=type(error).__name__)
                result['rows_after_attempt'] = list(child.objects.order_by('id').values_list('id', 'owner_id'))
                result['constraints_after_attempt'] = constraints(child)
                if not result['applied']:
                    child.objects.order_by('-id').first().delete()
                    with connection.schema_editor() as editor:
                        operation.database_forwards(app, editor, state, after)
                connection.close()
                current_child = after.apps.get_model(app, 'Child')
                result['reopened_rows'] = list(current_child.objects.order_by('id').values_list('id', 'owner_id'))
                result['reopened_constraints'] = constraints(current_child)
                with connection.schema_editor() as editor:
                    operation.database_backwards(app, editor, after, state)
                result['reversed_constraints'] = constraints(child)
                # The restored FK keeps uniqueness only when it originally had it.
                try:
                    with transaction.atomic():
                        child.objects.create(owner_id=instance.pk)
                    result['reverse_duplicate_saved'] = True
                except Exception as error:
                    result.update(reverse_duplicate_saved=False, reverse_exception=type(error).__name__)
                migrations.append(result)
                with connection.schema_editor() as editor:
                    editor.delete_model(child)
                    editor.delete_model(owner)
                del created[-2:]

            with connection.cursor() as cursor:
                cursor.execute('SHOW server_version_num' if database else 'SELECT sqlite_version()')
                version = cursor.fetchone()[0]
            output = {'django': django.get_version(), 'python': platform.python_version(),
                      'backend': connection.vendor, 'database_version': version,
                      'field_shapes': shapes, 'observations': observations, 'migrations': migrations,
                      'django_related_field_source_sha256': hashlib.sha256(
                          Path(inspect.getsourcefile(models.OneToOneField)).read_bytes()).hexdigest()}
            if database:
                output['psycopg'] = importlib.metadata.version('psycopg')
            return output
        finally:
            for model in reversed(created):
                with connection.schema_editor() as editor:
                    editor.delete_model(model)
            connection.close()


if __name__ == '__main__':
    print(json.dumps(observe(), sort_keys=True, separators=(',', ':')))
